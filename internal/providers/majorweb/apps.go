package majorweb

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"time"
)

func appReply(model, answer, reason, finish string, stream bool) *http.Response {
	id, created := randomID("chatcmpl-"), time.Now().Unix()
	var b []byte
	if stream {
		delta := map[string]any{"role": "assistant", "content": answer}
		if reason != "" {
			delta["reasoning_content"] = reason
		}
		b = chatChunk(id, model, created, delta, nil)
		b = append(b, chatChunk(id, model, created, map[string]any{}, finish)...)
		b = append(b, []byte("data: [DONE]\n\n")...)
	} else {
		msg := map[string]any{"role": "assistant", "content": answer}
		if reason != "" {
			msg["reasoning_content"] = reason
		}
		b, _ = json.Marshal(map[string]any{"id": id, "object": "chat.completion", "created": created, "model": model, "choices": []any{map[string]any{"index": 0, "message": msg, "finish_reason": finish}}})
	}
	h := http.Header{"Content-Type": {"application/json"}, "X-COT-Delivery": {"buffered"}}
	if stream {
		h.Set("Content-Type", "text/event-stream")
	}
	return &http.Response{StatusCode: 200, Header: h, Body: io.NopCloser(bytes.NewReader(b)), ContentLength: int64(len(b))}
}

func (c *Client) doMerlin(ctx context.Context, protocol, model string, stream bool, body []byte, cred credentials) (*http.Response, error) {
	if protocol != "chat" || len(body) > 64<<10 {
		return nil, ErrUnsupported
	}
	in, err := parseChatInput(body, model)
	if err != nil {
		return nil, err
	}
	token := strings.TrimPrefix(cred.value, "Bearer ")
	if token == "" || len(token) > 16384 || strings.ContainsAny(token, "\r\n") {
		return nil, ErrCredential
	}
	base, err := baseURL(c.source, "https://arcane.getmerlin.in")
	if err != nil {
		return nil, err
	}
	p, _ := json.Marshal(map[string]any{"attachments": []any{}, "chatId": randomUUID(), "language": "AUTO", "message": map[string]any{"content": in.Prompt, "context": "", "childId": randomUUID(), "id": randomUUID(), "parentId": "root"}, "mode": "UNIFIED_CHAT", "model": model, "metadata": map[string]bool{"largeContext": false, "merlinMagic": false, "proFinderMode": false, "webAccess": false}})
	r, err := request(ctx, c.http, http.MethodPost, base+"/v1/thread/unified", p, http.Header{"Content-Type": {"application/json"}, "Accept": {"text/event-stream"}, "Authorization": {"Bearer " + token}, "x-merlin-version": {"web-merlin"}})
	if err != nil {
		return nil, err
	}
	defer r.Body.Close()
	if r.StatusCode < 200 || r.StatusCode >= 300 {
		return nil, &HTTPError{Status: r.StatusCode, What: "Merlin request failed"}
	}
	answer, err := readMerlin(r.Body)
	if err != nil {
		return nil, err
	}
	return appReply(model, answer, "", "stop", stream), nil
}

func readMerlin(r io.Reader) (string, error) {
	d := newSSEDecoder(r)
	var text strings.Builder
	total := 0
	for {
		event, raw, ok, err := d.next()
		if err != nil {
			return "", err
		}
		if !ok {
			break
		}
		total += len(raw)
		if total > 4<<20 {
			return "", errors.New("Merlin response exceeds limit")
		}
		if event == "error" {
			return "", errors.New("Merlin upstream error")
		}
		if raw == "[DONE]" {
			break
		}
		var v struct {
			Error any    `json:"error"`
			Type  string `json:"type"`
			Data  struct {
				Content *string `json:"content"`
				Error   any     `json:"error"`
			} `json:"data"`
		}
		if json.Unmarshal([]byte(raw), &v) != nil || v.Error != nil || v.Data.Error != nil || v.Type == "error" {
			return "", errors.New("Merlin invalid upstream event")
		}
		if v.Data.Content != nil {
			text.WriteString(*v.Data.Content)
		}
	}
	// The pinned reference uses clean transport EOF; no application finish marker is specified.
	if text.Len() == 0 {
		return "", errors.New("Merlin empty response")
	}
	return text.String(), nil
}

func (c *Client) doSider(ctx context.Context, protocol, model string, stream bool, body []byte, cred credentials) (*http.Response, error) {
	if protocol != "chat" || len(body) > 64<<10 {
		return nil, ErrUnsupported
	}
	in, err := parseChatInput(body, model)
	if err != nil {
		return nil, err
	}
	token := strings.TrimPrefix(cred.value, "Bearer ")
	if token == "" || len(token) > 16384 || strings.ContainsAny(token, "\r\n") {
		return nil, ErrCredential
	}
	base, err := baseURL(c.source, "https://sider.ai")
	if err != nil {
		return nil, err
	}
	p, _ := json.Marshal(map[string]any{"cid": "", "parent_message_id": "", "model": model, "from": "chat", "client_prompt": map[string]any{}, "multi_content": []any{map[string]string{"type": "text", "text": in.Prompt, "user_input_text": in.Prompt}}, "prompt_templates": []any{}, "tools": map[string]any{"auto": []any{}}})
	r, err := request(ctx, c.http, http.MethodPost, base+"/api/chat/v1/completions", p, http.Header{"Content-Type": {"application/json"}, "Accept": {"text/event-stream"}, "Authorization": {"Bearer " + token}, "Origin": {"chrome-extension://dhoenijjpgpeimemopealfcbiecgceod"}, "X-Time-Zone": {"UTC"}, "X-App-Version": {"5.13.0"}, "X-App-Name": {"ChitChat_Edge_Ext"}})
	if err != nil {
		return nil, err
	}
	defer r.Body.Close()
	if r.StatusCode < 200 || r.StatusCode >= 300 {
		return nil, &HTTPError{Status: r.StatusCode, What: "Sider request failed"}
	}
	answer, reason, actual, err := readSider(r.Body, model)
	if err != nil {
		return nil, err
	}
	return appReply(actual, answer, reason, "stop", stream), nil
}

func readSider(r io.Reader, model string) (string, string, string, error) {
	d := newSSEDecoder(r)
	var text, reason strings.Builder
	total := 0
	fail := func() (string, string, string, error) { return "", "", "", errors.New("Sider invalid upstream event") }
	for {
		event, raw, ok, err := d.next()
		if err != nil {
			return "", "", "", err
		}
		if !ok {
			return "", "", "", ErrTruncated
		}
		total += len(raw)
		if total > 4<<20 {
			return fail()
		}
		if event == "error" {
			return fail()
		}
		if raw == "[DONE]" {
			if text.Len() == 0 {
				return fail()
			}
			return text.String(), reason.String(), model, nil
		}
		var v struct {
			Code *int `json:"code"`
			Data struct {
				Type   string  `json:"type"`
				Model  string  `json:"model"`
				Text   *string `json:"text"`
				Reason struct {
					Status string `json:"status"`
					Text   string `json:"text"`
				} `json:"reasoning_content"`
			} `json:"data"`
		}
		if json.Unmarshal([]byte(raw), &v) != nil || v.Code == nil || *v.Code != 0 {
			return fail()
		}
		if v.Data.Model != "" && v.Data.Model != model {
			return fail()
		}
		switch v.Data.Type {
		case "credit_info", "message_start":
		case "text":
			if v.Data.Text == nil {
				return fail()
			}
			text.WriteString(*v.Data.Text)
		case "reasoning_content":
			switch v.Data.Reason.Status {
			case "start", "processing", "finish":
				reason.WriteString(v.Data.Reason.Text)
			default:
				return fail()
			}
		default:
			return fail()
		}
	}
}
