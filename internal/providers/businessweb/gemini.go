package businessweb

import (
	"bufio"
	"bytes"
	"context"
	"crypto/sha1"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

const defaultGeminiBusinessBase = "https://business.gemini.google/home"

var geminiBusinessModelCategory = map[string]int{
	"gemini-3-pro": 70, "gemini-3-ultra": 71, "gemini-3-flash": 75,
	"gemini-2.5-pro": 53, "gemini-2.5-flash": 54, "gemini-2.5-flash-thinking": 55,
	"gemini-2.0-pro": 51, "gemini-2.0-flash": 52, "gemini-2.0-flash-thinking": 56,
	"gemini-3-pro-image": 76, "gemini-2.0-flash-image": 57, "veo-3.1-generate": 80,
}

func (c *Client) doGeminiBusiness(ctx context.Context, req chatRequest) (*http.Response, error) {
	if isGeminiBusinessMediaModel(req.Model) {
		return nil, &unsupportedError{"business web adapter: Gemini Business text adapter does not support image or video models"}
	}
	cred, err := credential(c.source)
	if err != nil {
		return nil, err
	}
	cookie := cred["cookie"]
	if cookie == "" {
		cookie = joinCookie(cred, "__Secure-1PSID", "__Secure-1PSIDTS")
	}
	if cookie == "" {
		return nil, errors.New("business web adapter: Gemini Business requires __Secure-1PSID cookie credentials")
	}
	base, pathPrefix, err := businessEntry(c.source.BaseURL)
	if err != nil {
		return nil, err
	}
	prompt := lastUser(req.Messages)
	if prompt == "" {
		return nil, &unsupportedError{"business web adapter: Gemini Business requires a user message"}
	}
	if len(req.Messages) != 1 || req.Messages[0].Role != "user" {
		return nil, &unsupportedError{"business web adapter: Gemini Business supports one user message per turn"}
	}
	category := geminiBusinessModelCategory[req.Model]
	if category == 0 {
		return nil, &unsupportedError{"business web adapter: unsupported Gemini Business model"}
	}
	inner := make([]any, 80)
	inner[0] = []any{prompt, 0, nil, nil, nil, nil, 0}
	inner[1] = []any{"en"}
	inner[2] = []any{"", "", "", nil, nil, nil, nil, nil, nil, ""}
	inner[6] = []any{0}
	inner[7] = 1
	inner[10] = 1
	inner[17] = []any{[]any{0}}
	inner[18] = 0
	inner[27] = 1
	inner[30] = []any{4}
	inner[41] = []any{2}
	inner[53] = 0
	inner[59] = randomID("")
	inner[61] = []any{}
	inner[68] = 1
	inner[79] = category
	encoded, _ := json.Marshal([]any{nil, string(mustJSON(inner))})
	form := url.Values{"f.req": {string(encoded)}, "hl": {"en"}}
	endpoint := base + pathPrefix + "/_/BardChatUi/data/assistant.lamda.BardFrontendService/StreamGenerate?bl=boq_assistant-bard-web-server_20240619.16_p0&hl=en&_reqid=" + strconv.FormatInt(time.Now().UnixNano()%900000+100000, 10) + "&rt=c"
	h := http.Header{"Content-Type": {"application/x-www-form-urlencoded;charset=UTF-8"}, "Accept": {"*/*"}, "Accept-Language": {"en-US,en;q=0.9"}, "Cookie": {cookie}, "X-Same-Domain": {"1"}, "Origin": {base}, "Referer": {base + pathPrefix + "/"}, "User-Agent": {"Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 Chrome/149 Safari/537.36"}}
	if sapisid := cookieValue(cookie, "SAPISID"); sapisid != "" {
		h.Set("Authorization", sapiHash(sapisid, base))
	} else if token := cred["access_token"]; token != "" {
		h.Set("Authorization", "Bearer "+token)
	}
	resp, err := request(ctx, c.http, http.MethodPost, endpoint, []byte(form.Encode()), h)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		status := resp.StatusCode
		resp.Body.Close()
		return nil, &HTTPError{Status: status}
	}
	raw, err := readBounded(resp.Body, maxResponseBytes)
	resp.Body.Close()
	if err != nil {
		return nil, err
	}
	if bytesContains(raw, "auth.business.gemini.google/account-chooser") {
		return nil, &HTTPError{Status: http.StatusForbidden}
	}
	text, parseErr := parseGeminiBusinessResponseStrict(raw)
	if parseErr != nil {
		return nil, parseErr
	}
	if strings.TrimSpace(text) == "" {
		return nil, errors.New("business web adapter: Gemini Business returned no text")
	}
	if req.Stream {
		return streamResponse(req.Model, text, ""), nil
	}
	return jsonResponse(req.Model, text, ""), nil
}

func isGeminiBusinessMediaModel(model string) bool {
	model = strings.ToLower(strings.TrimSpace(model))
	return strings.Contains(model, "-image") || strings.HasPrefix(model, "veo")
}

func mustJSON(v any) []byte                 { b, _ := json.Marshal(v); return b }
func bytesContains(b []byte, s string) bool { return strings.Contains(string(b), s) }
func lastUser(messages []chatMessage) string {
	for i := len(messages) - 1; i >= 0; i-- {
		if messages[i].Role == "user" {
			s, _ := textContent(messages[i].Content)
			return strings.TrimSpace(s)
		}
	}
	return ""
}

func businessEntry(raw string) (string, string, error) {
	if strings.TrimSpace(raw) == "" {
		raw = defaultGeminiBusinessBase
	}
	if !strings.Contains(raw, "://") {
		raw = "https://" + raw
	}
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" {
		return "", "", errors.New("business web adapter: invalid Gemini Business entry URL")
	}
	if u.Scheme != "https" && !(u.Scheme == "http" && isLoopback(u.Hostname())) {
		return "", "", errors.New("business web adapter: Gemini Business entry must use HTTPS")
	}
	return u.Scheme + "://" + u.Host, strings.TrimRight(u.EscapedPath(), "/"), nil
}

func joinCookie(fields map[string]string, names ...string) string {
	var out []string
	for _, n := range names {
		if v := fields[n]; v != "" {
			out = append(out, n+"="+v)
		}
	}
	return strings.Join(out, "; ")
}
func cookieValue(cookie, name string) string {
	for _, p := range strings.Split(cookie, ";") {
		k, v, ok := strings.Cut(strings.TrimSpace(p), "=")
		if ok && k == name {
			return v
		}
	}
	return ""
}
func sapiHash(value, origin string) string {
	now := strconv.FormatInt(time.Now().Unix(), 10)
	h := sha1.Sum([]byte(now + " " + value + " " + origin))
	return "SAPISIDHASH " + now + "_" + hex.EncodeToString(h[:])
}

// parseGeminiBusinessResponse decodes the length-prefixed wrb.fr response
// emitted by Gemini Business. It follows the known [4][0][1] text path and
// has a conservative nested fallback for harmless response revisions.
func parseGeminiBusinessResponse(raw []byte) string {
	text, _ := parseGeminiBusinessResponseStrict(raw)
	return text
}

func parseGeminiBusinessResponseStrict(raw []byte) (string, error) {
	r := bufio.NewReader(bytes.NewReader(raw))
	first, err := r.ReadString('\n')
	if err != nil && strings.TrimSpace(first) != ")]}'" {
		return "", errors.New("business web adapter: malformed Gemini Business response prefix")
	}
	if strings.TrimSpace(strings.TrimSuffix(first, "\n")) != ")]}'" {
		return "", errors.New("business web adapter: malformed Gemini Business response prefix")
	}
	lastText := ""
	frames := 0
	for {
		lengthLine, readErr := r.ReadString('\n')
		if readErr == io.EOF && strings.TrimSpace(lengthLine) == "" {
			break
		}
		lengthText := strings.TrimSpace(lengthLine)
		if lengthText == "" && readErr == nil {
			continue
		}
		if !isDigits(lengthText) {
			return "", errors.New("business web adapter: malformed Gemini Business frame length")
		}
		length, parseErr := strconv.ParseInt(lengthText, 10, 64)
		if parseErr != nil || length <= 0 || length > int64(maxResponseBytes) {
			return "", errors.New("business web adapter: invalid Gemini Business frame length")
		}
		frameBytes := make([]byte, int(length))
		_, frameErr := io.ReadFull(r, frameBytes)
		if frameErr != nil {
			return "", ErrTruncated
		}
		frame := string(frameBytes)
		if int64(len([]byte(frame))) != length {
			return "", errors.New("business web adapter: Gemini Business frame length mismatch")
		}
		var root []any
		if json.Unmarshal([]byte(frame), &root) != nil || len(root) == 0 {
			return "", errors.New("business web adapter: malformed Gemini Business frame")
		}
		for _, value := range root {
			row, ok := value.([]any)
			if !ok || len(row) == 0 {
				return "", errors.New("business web adapter: malformed Gemini Business row")
			}
			name, ok := row[0].(string)
			if !ok {
				return "", errors.New("business web adapter: malformed Gemini Business row name")
			}
			if name == "error" || name == "er" {
				return "", errors.New("business web adapter: Gemini Business upstream error")
			}
			if name != "wrb.fr" {
				continue
			}
			if len(row) < 3 {
				return "", errors.New("business web adapter: malformed Gemini Business wrb.fr row")
			}
			payload, ok := row[2].(string)
			if !ok || payload == "" {
				return "", errors.New("business web adapter: malformed Gemini Business payload")
			}
			var inner []any
			if json.Unmarshal([]byte(payload), &inner) != nil {
				return "", errors.New("business web adapter: malformed Gemini Business payload")
			}
			if a, ok := at(inner, 4).([]any); ok {
				if b, ok := at(a, 0).([]any); ok {
					if c, ok := at(b, 1).([]any); ok {
						var snapshot strings.Builder
						for _, x := range c {
							if s, ok := x.(string); ok {
								snapshot.WriteString(s)
							}
						}
						if snapshot.Len() > 0 {
							lastText = snapshot.String()
						}
					}
				}
			}
			frames++
		}
		if frameErr == io.EOF {
			break
		}
	}
	if frames == 0 || lastText == "" {
		return "", errors.New("business web adapter: Gemini Business returned no text frame")
	}
	return lastText, nil
}
func at(a []any, i int) any {
	if i < 0 || i >= len(a) {
		return nil
	}
	return a[i]
}
func isDigits(s string) bool {
	if s == "" {
		return false
	}
	for _, r := range s {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}

var _ = fmt.Sprintf
