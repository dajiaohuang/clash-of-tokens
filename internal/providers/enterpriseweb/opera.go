package enterpriseweb

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"strings"
)

const operaBase = "https://composer.opera-api.com"
const operaTokenBase = "https://oauth2.opera-api.com"

func (c *Client) doOperaAria(ctx context.Context, req chatRequest) (*http.Response, error) {
	if req.Model != "aria" && req.Model != "aria-legacy" {
		return nil, ErrUnsupported
	}
	cred, _, err := c.credential()
	if err != nil || (cred.AccessToken == "" && cred.RefreshToken == "") {
		return nil, ErrCredential
	}
	base, err := baseURL(c.source, operaBase)
	if err != nil {
		return nil, err
	}
	access := cred.AccessToken
	if access == "" {
		tokenBase := base
		// Opera's production token exchange is hosted separately from the
		// composer API. Treat an explicit standard composer URL the same as
		// the empty default; only a genuinely custom BaseURL overrides it.
		configuredBase := strings.TrimRight(strings.TrimSpace(c.source.BaseURL), "/")
		if configuredBase == "" || configuredBase == operaBase {
			tokenBase = operaTokenBase
		}
		access, err = c.operaAccessToken(ctx, tokenBase, cred.RefreshToken)
		if err != nil {
			return nil, err
		}
	}
	version := "v2"
	if req.Model == "aria-legacy" {
		version = "v1"
	}
	key := operaEncryptionKey()
	if key == "" {
		return nil, errors.New("enterpriseweb adapter: Opera encryption key unavailable")
	}
	payload := buildOperaPayload(req, version, key)
	headers := buildOperaHeaders(access, version)
	resp, err := postJSON(ctx, c.http, joinPath(base, "/api/"+version+"/a-chat"), payload, headers)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return resp, nil
	}
	if !strings.Contains(strings.ToLower(resp.Header.Get("Content-Type")), "text/event-stream") {
		_ = resp.Body.Close()
		return nil, errors.New("enterpriseweb adapter: Opera Aria did not return an event stream")
	}
	parser := func(ctx context.Context, body io.Reader, emit func(string) error) error {
		return parseOperaSSE(ctx, body, version, emit)
	}
	if req.Stream {
		resp.Body = newConvertedResponse(ctx, resp.Body, req.Model, parser)
		resp.Header.Set("Content-Type", "text/event-stream")
		resp.Header.Set("X-COT-Delivery", "upstream")
		return resp, nil
	}
	return collectResponse(ctx, resp.Body, req.Model, parser)
}

func (c *Client) operaAccessToken(ctx context.Context, base, refresh string) (string, error) {
	values := url.Values{"client_id": {"mini"}, "grant_type": {"refresh_token"}, "refresh_token": {refresh}, "scope": {"shodan:aria"}}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, joinPath(base, "/oauth2/v1/token/"), strings.NewReader(values.Encode()))
	if err != nil {
		return "", errors.New("enterpriseweb adapter: invalid Opera token request")
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("User-Agent", "okhttp/5.3.2")
	req.Header.Set("x-requested-with", "XMLHttpRequest")
	req.Header.Set("x-opera-client-cache", "1")
	resp, err := c.http.Do(req)
	if err != nil {
		if ctx.Err() != nil {
			return "", ctx.Err()
		}
		return "", errors.New("enterpriseweb adapter: Opera token request failed")
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return "", &HTTPError{Status: resp.StatusCode}
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, maxEventBytes+1))
	if err != nil || len(data) > maxEventBytes {
		return "", ErrTruncated
	}
	var value map[string]any
	if json.Unmarshal(data, &value) != nil {
		return "", errors.New("enterpriseweb adapter: Opera token response is invalid")
	}
	access, _ := value["access_token"].(string)
	if access == "" {
		return "", errors.New("enterpriseweb adapter: Opera token response is invalid")
	}
	return access, nil
}

func buildOperaPayload(req chatRequest, version, key string) []byte {
	query := formatOperaPrompt(req.Messages)
	var payload map[string]any
	if version == "v1" {
		payload = map[string]any{
			"query": query, "stream": true, "linkify": true, "linkify_version": 3,
			"sia": true, "media_attachments": []any{}, "encryption": map[string]string{"key": key},
		}
	} else {
		payload = map[string]any{
			"query": query, "sia": true, "think_harder": false, "supported_features": []any{},
			"file_attachments": []any{}, "encryption": map[string]string{"key": key},
		}
	}
	data, _ := json.Marshal(payload)
	return data
}

func operaEncryptionKey() string {
	key := make([]byte, 32)
	if _, err := rand.Read(key); err != nil {
		return ""
	}
	return base64.StdEncoding.EncodeToString(key)
}

func formatOperaPrompt(messages []chatMessage) string {
	if len(messages) <= 1 {
		if len(messages) == 1 {
			return messages[0].Content
		}
		return ""
	}
	var b strings.Builder
	for i, m := range messages {
		if i > 0 {
			b.WriteString("\n")
		}
		label := "User"
		if m.Role == "assistant" {
			label = "Assistant"
		} else if m.Role == "system" {
			label = "System"
		}
		b.WriteString(label)
		b.WriteString(": ")
		b.WriteString(m.Content)
	}
	b.WriteString("\nAssistant:")
	return b.String()
}

func buildOperaHeaders(access, version string) http.Header {
	h := http.Header{"Accept": []string{"text/event-stream"}, "Authorization": []string{"Bearer " + access}, "Content-Type": []string{"application/json"}, "X-Opera-Timezone": []string{"+02:00"}, "X-Opera-UI-Language": []string{"en"}}
	if version == "v1" {
		h.Set("Origin", "opera-aria://ui")
		h.Set("User-Agent", "Mozilla/5.0 (Linux; Android 10; K) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/135.0.0.0 Mobile Safari/537.36 OPR/89.0.0.0")
	} else {
		h.Set("Origin", "https://composer.opera-api.com")
		h.Set("Referer", "https://composer.opera-api.com/assets/aria/index.html")
		h.Set("User-Agent", "Mozilla/5.0 (Linux; U; Android 14; Pixel 8 Pro Build/UQ1A.240205.004; wv) AppleWebKit/537.36 (KHTML, like Gecko) Version/4.0 Chrome/138.0.7204.179 Mobile Safari/537.36 OPR/99.0.2254.81922")
		h.Set("X-Requested-With", "com.opera.mini.native")
	}
	return h
}

func parseOperaSSE(ctx context.Context, body io.Reader, version string, emit func(string) error) error {
	seen := false
	skipNext := false
	err := parseSSELinesWithEvents(ctx, body, func(event string) {
		if event == "thinking_status" {
			skipNext = true
		}
	}, func(data []byte) error {
		if string(data) == "[DONE]" {
			return errSourceDone
		}
		if string(data) == "null" {
			return nil
		}
		var value map[string]any
		if json.Unmarshal(data, &value) != nil {
			return nil
		}
		if skipNext {
			skipNext = false
			return nil
		}
		if streamObjectHasError(value) {
			return errors.New("enterpriseweb adapter: Opera Aria upstream stream error")
		}
		var text string
		if version == "v1" {
			text, _ = value["message"].(string)
		} else if response, ok := value["response"].(map[string]any); ok {
			if response["content_type"] == "text" {
				text, _ = response["message"].(string)
			}
		}
		if text == "" {
			return nil
		}
		seen = true
		return emit(text)
	})
	if err != nil {
		return err
	}
	if !seen {
		return errors.New("enterpriseweb adapter: Opera Aria completed without answer")
	}
	return nil
}
