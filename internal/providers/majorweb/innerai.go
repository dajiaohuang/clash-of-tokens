package majorweb

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"time"
)

func (c *Client) doInnerAI(ctx context.Context, protocol, model string, stream bool, body []byte, cred credentials) (*http.Response, error) {
	if protocol != "chat" || len(body) > 1<<20 || strings.TrimSpace(model) == "" {
		return nil, &requestError{"Inner.ai requires bounded text chat and exact product model"}
	}
	input, err := parseChatInput(body, model)
	if err != nil {
		return nil, err
	}
	token := strings.TrimPrefix(cred.value, "token=")
	if len(token) > 16384 || strings.ContainsAny(token, "\r\n; \t") {
		return nil, ErrCredential
	}
	// JWT claims are identity hints, never used here to authorize plan entitlements.
	if parts := strings.Split(token, "."); len(parts) == 3 {
		raw, e := base64.RawURLEncoding.DecodeString(parts[1])
		if e == nil {
			var claims map[string]any
			if json.Unmarshal(raw, &claims) == nil {
				if cred.deviceID == "" {
					for _, k := range []string{"device_id", "deviceId", "device-id", "did"} {
						if v, ok := claims[k].(string); ok && v != "" {
							cred.deviceID = v
							break
						}
					}
				}
				if cred.email == "" {
					if s, ok := claims["sub"].(string); ok && strings.Contains(s, "@") {
						cred.email = s
					}
				}
			}
		}
	}
	if len(cred.email) > 512 || len(cred.deviceID) > 1024 || strings.ContainsAny(cred.email+cred.deviceID, "\r\n") {
		return nil, ErrCredential
	}
	base, err := baseURL(c.source, "https://chatapi.innerai.com")
	if err != nil {
		return nil, err
	}
	h := http.Header{"Content-Type": {"application/json"}, "Cookie": {"token=" + token}, "User-Token": {token}, "Device-Id": {cred.deviceID}, "Origin": {"https://app.innerai.com"}, "Referer": {"https://app.innerai.com/"}, "User-Agent": {"Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/149.0.0.0 Safari/537.36"}}
	if cred.email != "" {
		h.Set("User-Email", cred.email)
	}
	resp, err := request(ctx, c.http, http.MethodGet, "https://platformapi.innerai.com/api/v1/ai_models", nil, h)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return resp, nil
	}
	data, err := readBounded(resp.Body, 4<<20)
	resp.Body.Close()
	if err != nil {
		return nil, err
	}
	type modelEntry struct {
		ID          string `json:"id"`
		LLM         string `json:"llm_model"`
		Enable      *bool  `json:"enable"`
		Unavailable bool   `json:"unavailable_api"`
	}
	var models []modelEntry
	if json.Unmarshal(data, &models) != nil {
		var root map[string]json.RawMessage
		if json.Unmarshal(data, &root) != nil {
			return nil, errors.New("Inner.ai invalid models response")
		}
		found := false
		for _, key := range []string{"data", "models", "ai_models"} {
			if json.Unmarshal(root[key], &models) == nil {
				found = true
				break
			}
		}
		if !found {
			return nil, errors.New("Inner.ai invalid models list")
		}
	}
	var selected *modelEntry
	for i := range models {
		m := &models[i]
		if m.LLM == model && (m.Enable == nil || *m.Enable) && !m.Unavailable && m.ID != "" {
			if selected != nil {
				return nil, errors.New("Inner.ai model ID is ambiguous")
			}
			selected = m
		}
	}
	if selected == nil {
		return nil, &requestError{"Inner.ai requested model is absent or disabled in account catalog"}
	}
	data, _ = json.Marshal(map[string]any{"message": input.Prompt, "session_id": randomUUID(), "context_type": "no_context", "ai_model": map[string]string{"id": selected.ID, "llm_model": selected.LLM}, "is_extension": false, "env": "production", "temporary": true, "use_web_search": false, "knowledge_list": []any{}})
	resp, err = request(ctx, c.http, http.MethodPost, endpoint(base, "/chat"), data, h)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return resp, nil
	}
	source := resp.Body
	id := randomID("chatcmpl-")
	created := time.Now().Unix()
	seen := false
	converted := newProviderStreamBody(source, id, model, func(event, data string) ([]byte, bool, error) {
		if data == "[DONE]" {
			return nil, false, nil
		}
		var frame struct {
			Type  string          `json:"type"`
			Item  string          `json:"item"`
			Error json.RawMessage `json:"error"`
		}
		if json.Unmarshal([]byte(data), &frame) != nil {
			return nil, false, errors.New("Inner.ai invalid event")
		}
		if len(frame.Error) > 0 && string(frame.Error) != "null" {
			return nil, false, errors.New("Inner.ai upstream error")
		}
		switch frame.Type {
		case "error", "missing_credits", "reached_limit", "rate_limit_reached", "rate_limit_longer_reached":
			return nil, false, errors.New("Inner.ai upstream rejected turn")
		case "text":
			if frame.Item != "" {
				seen = true
				return chatChunk(id, model, created, map[string]any{"content": frame.Item}, nil), false, nil
			}
		case "end_stream":
			source.Close()
			if !seen {
				return nil, false, errors.New("Inner.ai completed without text")
			}
			return nil, true, nil
		}
		return nil, false, nil
	})
	return responseFromStream(resp, converted, stream, id, model)
}
