package embedded

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"mime/multipart"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// WordPress AI Engine credentials are supplied explicitly. Nonces and cookies
// are never copied from reference repositories or harvested from other apps.
type wordpressCredential struct {
	Nonce     string `json:"nonce"`
	Cookie    string `json:"cookie"`
	SessionID string `json:"session_id"`
}

func (c *Client) doWordPress(ctx context.Context, protocol, model string, stream bool, body []byte) (*http.Response, error) {
	if protocol != "chat" {
		return nil, &requestError{"WordPress chat supports only chat protocol"}
	}
	key, err := c.credential()
	if err != nil {
		return nil, err
	}
	var credential wordpressCredential
	if json.Unmarshal([]byte(key), &credential) != nil || credential.Nonce == "" {
		return nil, errors.New("WordPress key_env must contain JSON with nonce and optional cookie/session_id")
	}
	if c.source.Project == "" || model == "" {
		return nil, &requestError{"WordPress project must be post_id and upstream model must be bot_id"}
	}
	if c.source.Adapter == AdapterChatGPTFree && (credential.Cookie == "" || credential.SessionID == "") {
		return nil, errors.New("chatgptfree requires explicit cookie and session_id")
	}
	base := "https://chataigpt.net"
	if c.source.Adapter == AdapterChatGPTFree {
		base = "https://chatgptfree.ai"
	}
	base, err = c.baseURL(base)
	if err != nil {
		return nil, err
	}
	request, err := parseChatRequest(body)
	if err != nil {
		return nil, err
	}
	prompt, err := textContent(request.Messages[0].Content)
	if err != nil {
		return nil, err
	}
	endpoint := base + "/wp-admin/admin-ajax.php"
	form := url.Values{"action": {"aipkit_cache_sse_message"}, "message": {prompt}, "_ajax_nonce": {credential.Nonce}, "bot_id": {model}, "user_client_message_id": {"aipkit-client-msg-" + model + "-" + strconv.FormatInt(time.Now().UnixMilli(), 10) + "-" + newUUID()}}
	headers := http.Header{"Accept": {"application/json"}, "Origin": {base}, "Referer": {base + "/"}}
	if credential.Cookie != "" {
		headers.Set("Cookie", credential.Cookie)
	}
	payload := []byte(form.Encode())
	headers.Set("Content-Type", "application/x-www-form-urlencoded")
	if c.source.Adapter == AdapterChatAIGPT {
		var b bytes.Buffer
		w := multipart.NewWriter(&b)
		for key, values := range form {
			if err := w.WriteField(key, values[0]); err != nil {
				return nil, err
			}
		}
		if err := w.Close(); err != nil {
			return nil, err
		}
		payload = b.Bytes()
		headers.Set("Content-Type", w.FormDataContentType())
	}
	resp, err := c.request(ctx, http.MethodPost, endpoint, payload, headers)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return resp, nil
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, (1<<20)+1))
	resp.Body.Close()
	if err != nil || len(data) > 1<<20 {
		return nil, errors.New("invalid WordPress cache response")
	}
	var cached struct {
		Success bool `json:"success"`
		Data    struct {
			Key string `json:"cache_key"`
		} `json:"data"`
	}
	if json.Unmarshal(data, &cached) != nil || !cached.Success || cached.Data.Key == "" {
		return nil, errors.New("WordPress cache request failed")
	}
	session := credential.SessionID
	if session == "" {
		session = newUUID()
	}
	query := url.Values{"action": {"aipkit_frontend_chat_stream"}, "cache_key": {cached.Data.Key}, "bot_id": {model}, "session_id": {session}, "conversation_uuid": {newUUID()}, "post_id": {c.source.Project}, "_ts": {strconv.FormatInt(time.Now().UnixMilli(), 10)}, "_ajax_nonce": {credential.Nonce}}
	headers.Set("Accept", "text/event-stream")
	headers.Del("Content-Type")
	resp, err = c.request(ctx, http.MethodGet, endpoint+"?"+query.Encode(), nil, headers)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return resp, nil
	}
	if !strings.Contains(resp.Header.Get("Content-Type"), "text/event-stream") {
		resp.Body.Close()
		return nil, errors.New("WordPress upstream did not return SSE")
	}
	id := chatID()
	created := time.Now().Unix()
	converted := c.newStreamBody(resp.Body, id, model, func(event, data string) ([]byte, error) {
		if event == "error" {
			return nil, errors.New("WordPress upstream error")
		}
		if event == "done" || data == "[DONE]" {
			return nil, errStreamComplete
		}
		if data == "" {
			return nil, nil
		}
		var value struct {
			Delta    string          `json:"delta"`
			Finished bool            `json:"finished"`
			Error    json.RawMessage `json:"error"`
		}
		if json.Unmarshal([]byte(data), &value) != nil {
			return nil, errors.New("malformed WordPress SSE")
		}
		if len(value.Error) > 0 && string(value.Error) != "null" {
			return nil, errors.New("WordPress upstream error")
		}
		if value.Finished {
			if value.Delta != "" {
				return chatChunk(id, model, created, map[string]any{"content": value.Delta}, nil), errStreamComplete
			}
			return nil, errStreamComplete
		}
		if value.Delta != "" {
			return chatChunk(id, model, created, map[string]any{"content": value.Delta}, nil), nil
		}
		return nil, nil
	})
	return c.responseFromStream(resp, converted, stream, id, model)
}
