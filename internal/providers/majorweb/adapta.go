package majorweb

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"strings"
	"time"
)

type adaptaAuth struct {
	key     [32]byte
	token   string
	expires int64
}

func (c *Client) adaptaToken(ctx context.Context, cookie string) (string, error) {
	cookie = strings.TrimSpace(cookie)
	if strings.HasPrefix(cookie, "__client=") {
		cookie = strings.TrimPrefix(cookie, "__client=")
	}
	if cookie == "" || len(cookie) > 16384 || strings.ContainsAny(cookie, "\r\n;") {
		return "", ErrCredential
	}
	if err := c.adaptaGate.Lock(ctx); err != nil {
		return "", err
	}
	defer c.adaptaGate.Unlock()
	key := sha256.Sum256([]byte(cookie))
	if c.adaptaAuth.key == key && c.adaptaAuth.expires > time.Now().Unix()+30 {
		return c.adaptaAuth.token, nil
	}
	headers := http.Header{}
	headers.Set("Cookie", "__client="+cookie)
	headers.Set("Origin", "https://agent.adapta.one")
	headers.Set("Content-Type", "application/json")
	headers.Set("User-Agent", "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/149.0.0.0 Safari/537.36")
	fetch := func(method, path string) (map[string]any, error) {
		resp, err := request(ctx, c.http, method, "https://clerk.agent.adapta.one"+path, nil, headers)
		if err != nil {
			return nil, err
		}
		defer resp.Body.Close()
		if resp.StatusCode < 200 || resp.StatusCode >= 300 {
			return nil, &HTTPError{Status: resp.StatusCode, What: "Adapta session exchange failed"}
		}
		data, err := readBounded(resp.Body, 1<<20)
		if err != nil {
			return nil, err
		}
		var value map[string]any
		if json.Unmarshal(data, &value) != nil || value == nil {
			return nil, errors.New("Adapta invalid session response")
		}
		return value, nil
	}
	value, err := fetch(http.MethodGet, "/v1/client")
	if err != nil {
		return "", err
	}
	response, _ := value["response"].(map[string]any)
	sessions, _ := response["sessions"].([]any)
	sessionID := ""
	for _, item := range sessions {
		session, _ := item.(map[string]any)
		if session["status"] == "active" {
			sessionID, _ = session["id"].(string)
			if sessionID != "" {
				break
			}
		}
	}
	if !conolSessionID.MatchString(sessionID) {
		return "", errors.New("Adapta has no usable active session")
	}
	value, err = fetch(http.MethodPost, "/v1/client/sessions/"+url.PathEscape(sessionID)+"/tokens")
	if err != nil {
		return "", err
	}
	token, _ := value["jwt"].(string)
	parts := strings.Split(token, ".")
	if len(parts) != 3 || len(token) > 16384 {
		return "", errors.New("Adapta invalid session JWT")
	}
	data, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return "", errors.New("Adapta invalid JWT payload")
	}
	var claims struct {
		Exp int64 `json:"exp"`
	}
	if json.Unmarshal(data, &claims) != nil || claims.Exp <= time.Now().Unix()+5 {
		return "", errors.New("Adapta session JWT is expired or has no expiration")
	}
	c.adaptaAuth = adaptaAuth{key: key, token: token, expires: claims.Exp}
	return token, nil
}

func (c *Client) doAdapta(ctx context.Context, protocol, model string, stream bool, body []byte, cred credentials) (*http.Response, error) {
	if protocol != "chat" || len(body) > 1<<20 || (model != "default" && model != "adapta-one") {
		return nil, &requestError{"Adapta currently supports only the adapta-one/default auto model and text chat"}
	}
	input, err := parseChatInput(body, model)
	if err != nil {
		return nil, err
	}
	base, err := baseURL(c.source, "https://agent.adapta.one")
	if err != nil {
		return nil, err
	}
	token, err := c.adaptaToken(ctx, cred.value)
	if err != nil {
		return nil, err
	}
	encoded, _ := json.Marshal(map[string]any{"aiModelId": 14, "messages": []any{map[string]any{"role": "user", "parts": []any{map[string]any{"type": "text", "text": input.Prompt}}}}})
	headers := http.Header{}
	for k, v := range map[string]string{"Authorization": "Bearer " + token, "Content-Type": "application/json", "Accept": "text/event-stream", "Origin": base, "Referer": base + "/agentic-chat", "User-Agent": "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/149.0.0.0 Safari/537.36"} {
		headers.Set(k, v)
	}
	resp, err := request(ctx, c.http, http.MethodPost, endpoint(base, "/api/chat/stream/v1"), encoded, headers)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		if resp.StatusCode == 401 || resp.StatusCode == 403 {
			if err := c.adaptaGate.Lock(ctx); err == nil {
				if c.adaptaAuth.token == token {
					c.adaptaAuth = adaptaAuth{}
				}
				c.adaptaGate.Unlock()
			}
		}
		return resp, nil
	}
	id := randomID("chatcmpl-")
	out := newProviderStreamBody(resp.Body, id, model, func(_ string, raw string) ([]byte, bool, error) {
		if raw == "[DONE]" {
			return nil, true, nil
		}
		var value struct {
			Type  string `json:"type"`
			ID    string `json:"id"`
			Delta string `json:"delta"`
		}
		if json.Unmarshal([]byte(raw), &value) != nil {
			return nil, false, errors.New("Adapta invalid SSE event")
		}
		switch value.Type {
		case "text-delta":
			if value.ID != "quick-response" {
				return chatChunk(id, model, time.Now().Unix(), map[string]any{"content": value.Delta}, nil), false, nil
			}
		case "error":
			return nil, false, errors.New("Adapta upstream stream failed")
		case "done", "end":
			return nil, true, nil
		}
		return nil, false, nil
	})
	return responseFromStream(resp, out, stream, id, model)
}
