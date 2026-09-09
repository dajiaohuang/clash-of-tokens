package majorweb

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"time"
)

func raycastSignature(stamp, device, secret string, body []byte) string {
	digest := sha256.Sum256(body)
	data := []byte(stamp + "." + device + "." + hex.EncodeToString(digest[:]))
	for i, b := range data {
		switch {
		case b >= 'a' && b <= 'z':
			data[i] = 'a' + (b-'a'+13)%26
		case b >= 'A' && b <= 'Z':
			data[i] = 'A' + (b-'A'+13)%26
		case b >= '0' && b <= '9':
			data[i] = '0' + (b-'0'+5)%10
		}
	}
	h := hmac.New(sha256.New, []byte(secret))
	h.Write(data)
	return hex.EncodeToString(h.Sum(nil))
}

func (c *Client) doRaycast(ctx context.Context, protocol, model string, stream bool, body []byte, cred credentials) (*http.Response, error) {
	if protocol != "chat" || len(body) > 64<<10 {
		return nil, ErrUnsupported
	}
	in, err := parseChatInput(body, model)
	if err != nil {
		return nil, err
	}
	token := strings.TrimPrefix(cred.value, "Bearer ")
	for _, v := range []string{token, cred.deviceID, cred.signatureSecret} {
		if v == "" || len(v) > 16384 || strings.ContainsAny(v, "\r\n") {
			return nil, ErrCredential
		}
	}
	base, err := baseURL(c.source, "https://backend.raycast.com")
	if err != nil {
		return nil, err
	}
	h := http.Header{"Authorization": {"Bearer " + token}, "X-Raycast-DeviceId": {cred.deviceID}, "X-Raycast-Timestamp": {time.Now().UTC().Format("2006-01-02T15:04:05.000Z")}, "User-Agent": {"Raycast/1.94.2 (macOS Version 15.3.2 (Build 24D81))"}, "Content-Type": {"application/json"}, "Accept": {"application/json"}}
	r, err := request(ctx, c.http, http.MethodGet, base+"/api/v1/ai/models", nil, h)
	if err != nil {
		return nil, err
	}
	if r.StatusCode != 200 {
		r.Body.Close()
		return nil, &HTTPError{Status: r.StatusCode, What: "Raycast model lookup failed"}
	}
	raw, err := readBounded(r.Body, 2<<20)
	r.Body.Close()
	if err != nil {
		return nil, err
	}
	var registry struct {
		Models []struct {
			ID       string `json:"id"`
			Provider string `json:"provider"`
			Model    string `json:"model"`
		} `json:"models"`
	}
	if json.Unmarshal(raw, &registry) != nil {
		return nil, errors.New("Raycast invalid model registry")
	}
	provider, internal := "", ""
	for _, entry := range registry.Models {
		if entry.ID == model {
			if provider != "" {
				return nil, errors.New("Raycast ambiguous model")
			}
			provider, internal = entry.Provider, entry.Model
		}
	}
	if provider == "" || internal == "" {
		return nil, &requestError{"Raycast model is not in the account model registry"}
	}
	p, _ := json.Marshal(map[string]any{"model": internal, "provider": provider, "messages": []any{map[string]any{"author": "user", "content": map[string]string{"text": in.Prompt}}}, "system_instruction": "markdown", "temperature": 0.5, "additional_system_instructions": "", "debug": false, "locale": "en-US", "source": "ai_chat", "thread_id": randomUUID(), "tools": []any{}})
	stamp := time.Now().UTC().Format("2006-01-02T15:04:05.000Z")
	h.Set("X-Raycast-Timestamp", stamp)
	h.Set("X-Raycast-Signature-v2", raycastSignature(stamp, cred.deviceID, cred.signatureSecret, p))
	r, err = request(ctx, c.http, http.MethodPost, base+"/api/v1/ai/chat_completions", p, h)
	if err != nil {
		return nil, err
	}
	defer r.Body.Close()
	if r.StatusCode < 200 || r.StatusCode >= 300 {
		return nil, &HTTPError{Status: r.StatusCode, What: "Raycast chat failed"}
	}
	answer, finish, err := readRaycast(r.Body)
	if err != nil {
		return nil, err
	}
	return appReply(model, answer, "", finish, stream), nil
}

func readRaycast(r io.Reader) (string, string, error) {
	d := newSSEDecoder(r)
	var text strings.Builder
	total := 0
	for {
		event, raw, ok, err := d.next()
		if err != nil {
			return "", "", err
		}
		if !ok {
			return "", "", ErrTruncated
		}
		total += len(raw)
		if total > 4<<20 {
			return "", "", errors.New("Raycast response exceeds limit")
		}
		if event == "error" {
			return "", "", errors.New("Raycast upstream error")
		}
		if raw == "[DONE]" {
			if text.Len() == 0 {
				return "", "", errors.New("Raycast empty response")
			}
			return text.String(), "stop", nil
		}
		var v struct {
			Text   string  `json:"text"`
			Finish *string `json:"finish_reason"`
			Error  any     `json:"error"`
		}
		if json.Unmarshal([]byte(raw), &v) != nil || v.Error != nil {
			return "", "", errors.New("Raycast invalid event")
		}
		text.WriteString(v.Text)
		if v.Finish != nil {
			if *v.Finish != "stop" && *v.Finish != "length" {
				return "", "", errors.New("Raycast unsupported finish")
			}
			if text.Len() == 0 {
				return "", "", errors.New("Raycast empty response")
			}
			return text.String(), *v.Finish, nil
		}
	}
}
