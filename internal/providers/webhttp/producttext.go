package webhttp

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"unicode/utf8"
)

func productBase(base, fallback string) (string, error) {
	base = strings.TrimRight(strings.TrimSpace(base), "/")
	if base == "" {
		base = fallback
	}
	u, err := url.Parse(base)
	if err != nil || u.Host == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" {
		return "", invalid("invalid website base URL")
	}
	loopback := strings.EqualFold(u.Hostname(), "localhost")
	if ip := net.ParseIP(u.Hostname()); ip != nil {
		loopback = ip.IsLoopback()
	}
	if u.Scheme != "https" && !(u.Scheme == "http" && loopback) {
		return "", invalid("website URL requires HTTPS outside loopback")
	}
	return base, nil
}

func (c *Client) productRequest(ctx context.Context, method, target string, body []byte, headers http.Header) (*http.Response, error) {
	r, err := http.NewRequestWithContext(ctx, method, target, bytes.NewReader(body))
	if err != nil {
		return nil, errors.New("invalid website request")
	}
	r.Header, r.GetBody = headers, nil
	resp, err := c.http.Do(r)
	if err != nil {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		return nil, errors.New("website transport failed")
	}
	return resp, nil
}

var phindNonce = regexp.MustCompile(`["']nonce["']\s*:\s*["']([a-f0-9]+)["']`)

// The inventory calls this Phind, but its pinned PhindAi module uses
// phindai.org. It is not the unrelated phind.com product.
func (c *Client) doPhindAI(ctx context.Context, model string, stream bool, prompt string) (*http.Response, error) {
	if model != "default" {
		return nil, invalid("PhindAi only exposes product-default model selection")
	}
	base, err := productBase(c.source.BaseURL, "https://phindai.org")
	if err != nil {
		return nil, err
	}
	h := http.Header{"User-Agent": {"Mozilla/5.0"}, "Accept": {"text/html"}}
	page, err := c.productRequest(ctx, http.MethodGet, base+"/", nil, h)
	if err != nil {
		return nil, err
	}
	if page.StatusCode < 200 || page.StatusCode >= 300 {
		return page, nil
	}
	data, err := io.ReadAll(io.LimitReader(page.Body, limit+1))
	page.Body.Close()
	if err != nil || len(data) > limit {
		return nil, errors.New("PhindAi homepage exceeds limit or is incomplete")
	}
	match := phindNonce.FindSubmatch(data)
	if len(match) != 2 {
		return nil, errors.New("PhindAi homepage omitted nonce")
	}
	h = http.Header{"User-Agent": {"Mozilla/5.0"}, "Accept": {"application/json"}, "Content-Type": {"application/x-www-form-urlencoded"}, "Origin": {base}, "Referer": {base + "/"}}
	// Reuse only cookies issued by this request's own configured origin.
	cookieRequest := &http.Request{Header: h}
	for _, cookie := range page.Cookies() {
		cookieRequest.AddCookie(cookie)
	}
	form := url.Values{"action": {"phind_ai_send"}, "nonce": {string(match[1])}, "message": {prompt}}
	resp, err := c.productRequest(ctx, http.MethodPost, base+"/wp-admin/admin-ajax.php", []byte(form.Encode()), h)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return resp, nil
	}
	defer resp.Body.Close()
	data, err = io.ReadAll(io.LimitReader(resp.Body, limit+1))
	if err != nil || len(data) > limit {
		return nil, errors.New("PhindAi response exceeds limit or is incomplete")
	}
	var result struct {
		Success bool `json:"success"`
		Data    struct {
			Response *string `json:"response"`
		} `json:"data"`
	}
	if json.Unmarshal(data, &result) != nil || !result.Success || result.Data.Response == nil {
		return nil, errors.New("PhindAi returned no successful response")
	}
	return output(model, stream, "buffered", func(emit func(string) error) (string, error) { return "stop", emit(*result.Data.Response) }, nil), nil
}

func (c *Client) doWhiteRabbit(ctx context.Context, model string, stream bool, messages []message) (*http.Response, error) {
	if model != "default" {
		return nil, invalid("WhiteRabbitNeo only exposes product-default model selection")
	}
	base, err := productBase(c.source.BaseURL, "https://www.whiterabbitneo.com")
	if err != nil {
		return nil, err
	}
	cookie := strings.TrimSpace(c.source.CredentialValue())
	if cookie == "" || strings.ContainsAny(cookie, "\r\n\x00") {
		return nil, &Error{401, "WhiteRabbitNeo credential is missing or invalid"}
	}
	body, err := json.Marshal(map[string]any{"messages": messages, "id": randomProductID(), "enhancePrompt": false, "useFunctions": false})
	if err != nil {
		return nil, err
	}
	h := http.Header{"Content-Type": {"text/plain;charset=UTF-8"}, "Accept": {"text/plain"}, "Cookie": {cookie}, "Origin": {base}, "Referer": {base + "/"}, "User-Agent": {"Mozilla/5.0"}}
	resp, err := c.productRequest(ctx, http.MethodPost, base+"/api/chat", body, h)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return resp, nil
	}
	if !strings.HasPrefix(strings.ToLower(resp.Header.Get("Content-Type")), "text/plain") {
		resp.Body.Close()
		return nil, errors.New("WhiteRabbitNeo did not return plain text")
	}
	return output(model, stream, "upstream", func(emit func(string) error) (string, error) { return readPlainText(resp.Body, emit) }, resp.Body), nil
}

func randomProductID() string { return rand.Text()[:6] }

// This source has no in-band terminator: a clean HTTP body EOF ends the
// answer. Transport truncation remains an error; incomplete UTF-8 is rejected.
func readPlainText(body io.Reader, emit func(string) error) (string, error) {
	buffer := make([]byte, 8192)
	pending := make([]byte, 0, 8196)
	total := 0
	for {
		n, err := body.Read(buffer)
		total += n
		if total > limit {
			return "", errors.New("website output exceeds limit")
		}
		pending = append(pending, buffer[:n]...)
		valid := 0
		for valid < len(pending) && utf8.FullRune(pending[valid:]) {
			r, size := utf8.DecodeRune(pending[valid:])
			if r == utf8.RuneError && size == 1 {
				return "", errors.New("invalid website UTF-8")
			}
			valid += size
		}
		if valid > 0 {
			if e := emit(string(pending[:valid])); e != nil {
				return "", e
			}
			pending = append(pending[:0], pending[valid:]...)
		}
		if err != nil {
			if err != io.EOF {
				return "", err
			}
			if len(pending) > 0 || total == 0 {
				return "", io.ErrUnexpectedEOF
			}
			return "stop", nil
		}
	}
}
