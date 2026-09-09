package chinaremaining

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"regexp"
	"strings"
)

type metasoCredential struct {
	Token string `json:"token"`
}

var metaTokenPattern = regexp.MustCompile(`<meta\s+id=["']meta-token["']\s+content=["']([^"']+)["']`)
var metasoIndexLabelPattern = regexp.MustCompile(`\[\[\d+\]\]`)

func removeMetasoIndexLabels(value string) string {
	return metasoIndexLabelPattern.ReplaceAllString(value, "")
}

func (c *Client) metasoCredential() (string, error) {
	if strings.TrimSpace(c.source.KeyEnv) == "" {
		return "", ErrCredential
	}
	raw := strings.TrimSpace(os.Getenv(c.source.KeyEnv))
	if len(raw) == 0 || len(raw) > maxCredentialBytes || strings.ContainsAny(raw, "\r\n\x00") {
		return "", ErrCredential
	}
	var cred metasoCredential
	dec := json.NewDecoder(strings.NewReader(raw))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&cred); err != nil {
		return "", ErrCredential
	}
	var trailing any
	if dec.Decode(&trailing) != io.EOF {
		return "", ErrCredential
	}
	token := strings.TrimSpace(cred.Token)
	parts := strings.Split(token, "-")
	if len(parts) != 2 || len(parts[0]) == 0 || len(parts[1]) == 0 || len(token) > 4096 || strings.ContainsAny(token, ";=(){}[]\\/\t ") {
		return "", ErrCredential
	}
	return token, nil
}

func (c *Client) doMetaso(ctx context.Context, model string, req chatRequest) (*http.Response, error) {
	token, err := c.metasoCredential()
	if err != nil {
		return nil, err
	}
	base, err := baseURL(c.source, defaultMetasoBase)
	if err != nil {
		return nil, err
	}
	content := latestMetasoPrompt(req.Messages)
	mode, engine := metasoMode(model)
	if mode == "" {
		return nil, fmt.Errorf("%w: Metaso model %q is unsupported", ErrUnsupported, model)
	}
	if text, _ := textContent(req.Messages[0].Content); strings.HasPrefix(text, "学术") && engine == "" {
		engine = "scholar"
	}
	meta, err := c.acquireMetaToken(ctx, base, token)
	if err != nil {
		return nil, err
	}
	conversation, err := c.createMetasoConversation(ctx, base, token, meta, content, mode, engine)
	if err != nil {
		return nil, err
	}
	// The public source captures this endpoint from a browser page. The HTTP
	// contract itself is SSE and works directly where the configured deployment
	// permits it; browser mode is selected by the caller through Browser.Enabled
	// and is implemented in metaso_browser.go.
	if c.browser.Enabled {
		return c.metasoBrowserStream(ctx, base, token, meta, conversation, content, mode)
	}
	values := url.Values{
		"sessionId": {conversation}, "question": {content}, "lang": {"zh"}, "mode": {mode},
		"url":       {base + "/search/" + url.PathEscape(conversation) + "?newSearch=true&q=" + url.QueryEscape(content)},
		"enableMix": {"true"}, "scholarSearchDomain": {"all"}, "expectedCurrentSessionSearchCount": {"1"},
		"is-mini-webview": {"0"}, "token": {meta},
	}
	h := metasoHeaders()
	h.Set("Accept", "text/event-stream")
	h.Set("Cookie", metasoCookie(token))
	h.Set("Is-Mini-Webview", "0")
	return c.do(ctx, http.MethodGet, base+"/api/searchV2?"+values.Encode(), nil, h)
}

func (c *Client) acquireMetaToken(ctx context.Context, base, token string) (string, error) {
	h := metasoHeaders()
	h.Set("Cookie", metasoCookie(token))
	h.Set("Sec-Fetch-Dest", "document")
	h.Set("Sec-Fetch-Mode", "navigate")
	h.Set("Sec-Fetch-User", "?1")
	h.Set("Upgrade-Insecure-Requests", "1")
	resp, err := c.do(ctx, http.MethodGet, base+"/", nil, h)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", readHTTPError(resp)
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, (4<<20)+1))
	if err != nil {
		return "", err
	}
	match := metaTokenPattern.FindSubmatch(data)
	if len(data) > 4<<20 || len(match) != 2 || len(match[1]) == 0 || len(match[1]) > 8192 || strings.ContainsAny(string(match[1]), "\r\n") {
		return "", fmt.Errorf("%w: metaso meta-token missing", ErrTruncated)
	}
	return string(match[1]), nil
}

func (c *Client) createMetasoConversation(ctx context.Context, base, token, meta, content, mode, engine string) (string, error) {
	payload := map[string]string{"question": content, "mode": mode, "engineType": engine, "scholarSearchDomain": "all"}
	body, _ := json.Marshal(payload)
	h := metasoHeaders()
	h.Set("Cookie", metasoCookie(token))
	h.Set("Token", meta)
	h.Set("Is-Mini-Webview", "0")
	h.Set("Content-Type", "application/json")
	resp, err := c.do(ctx, http.MethodPost, base+"/api/session", body, h)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return "", readHTTPError(resp)
	}
	return parseID(resp.Body, "metaso")
}

func latestMetasoPrompt(messages []chatMessage) string {
	if len(messages) == 0 {
		return ""
	}
	text, _ := textContent(messages[len(messages)-1].Content)
	if strings.Contains(text, "天气") {
		text += "，直接回答"
	}
	if strings.HasPrefix(text, "学术") {
		text = strings.TrimPrefix(text, "学术")
	}
	return text
}

func metasoMode(model string) (string, string) {
	parts := strings.SplitN(model, "-", 2)
	mode := parts[0]
	if mode != "concise" && mode != "detail" && mode != "research" {
		return "", ""
	}
	engine := ""
	if len(parts) == 2 && parts[1] == "scholar" {
		engine = "scholar"
	}
	return mode, engine
}

const metasoUA = "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/127.0.0.0 Safari/537.36"

func metasoHeaders() http.Header {
	return http.Header{
		"Accept": {"*/*"}, "Accept-Encoding": {"identity"}, "Accept-Language": {"zh-CN,zh;q=0.9"},
		"Origin": {"https://metaso.cn"}, "Referer": {"https://metaso.cn/"},
		"Sec-Ch-Ua":        {`"Chromium";v="122", "Not(A:Brand";v="24", "Google Chrome";v="122"`},
		"Sec-Ch-Ua-Mobile": {"?0"}, "Sec-Ch-Ua-Platform": {`"Windows"`},
		"Sec-Fetch-Dest": {"empty"}, "Sec-Fetch-Mode": {"cors"}, "Sec-Fetch-Site": {"same-origin"},
		"User-Agent": {metasoUA},
	}
}

func metasoCookie(token string) string {
	parts := strings.SplitN(token, "-", 2)
	return "uid=" + parts[0] + "; sid=" + parts[1] + ";"
}
