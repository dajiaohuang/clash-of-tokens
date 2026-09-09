package majorweb

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// doZenmux follows the pinned OmniRoute zenmux-free executor's wire contract.
// Only a single user turn is accepted: the reference drops prior history.
func (c *Client) doZenmux(ctx context.Context, protocol, model string, stream bool, body []byte, credential credentials) (*http.Response, error) {
	if protocol != "chat" || len(body) > 1<<20 || strings.TrimSpace(model) == "" {
		return nil, &requestError{"ZenMux requires a bounded Chat Completions request and model"}
	}
	input, err := parseChatInput(body, model)
	if err != nil {
		return nil, err
	}
	cookie := strings.TrimSpace(credential.cookie)
	if strings.HasPrefix(strings.ToLower(cookie), "cookie:") {
		cookie = strings.TrimSpace(cookie[7:])
	}
	if len(cookie) > 16384 || strings.ContainsAny(cookie, "\r\n") {
		return nil, ErrCredential
	}
	var token string
	for _, part := range strings.Split(cookie, ";") {
		key, value, ok := strings.Cut(strings.TrimSpace(part), "=")
		if ok && key == "ctoken" {
			if token != "" {
				return nil, ErrCredential
			}
			token = value
		}
	}
	if token == "" {
		return nil, ErrCredential
	}
	base, err := baseURL(c.source, "https://zenmux.ai")
	if err != nil {
		return nil, err
	}
	target := endpoint(base, "/api/anthropic/v1/messages") + "?ctoken=" + url.QueryEscape(token)
	payload, _ := json.Marshal(map[string]any{"model": model, "max_tokens": 4096, "stream": true, "messages": []any{map[string]any{"role": "user", "content": []any{map[string]any{"type": "text", "text": input.Prompt}}}}})
	headers := http.Header{}
	for k, v := range map[string]string{"Content-Type": "application/json", "Accept": "text/event-stream", "Cookie": cookie, "Origin": base, "Referer": base + "/platform/chat", "anthropic-version": "2023-06-01", "chat-request-id": strings.ReplaceAll(randomUUID(), "-", ""), "x-zenmux-accept-processing": "true, true", "x-zenmux-apikey-source": "subscription", "User-Agent": "Mozilla/5.0 (X11; Linux x86_64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/149.0.0.0 Safari/537.36"} {
		headers.Set(k, v)
	}
	callCtx, cancel := context.WithCancel(ctx)
	resp, err := request(callCtx, c.http, http.MethodPost, target, payload, headers)
	if err != nil {
		cancel()
		return nil, err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		resp.Body.Close()
		cancel()
		return nil, &HTTPError{Status: resp.StatusCode, What: "ZenMux request failed"}
	}
	source := resp.Body
	decoder := newSSEDecoder(source)
	id := randomID("chatcmpl-")
	created := time.Now().Unix()
	started, ended := false, false
	finish := ""
	converted := &transformBody{closeFn: func() error { cancel(); return source.Close() }}
	converted.next = func() ([]byte, error) {
		if !started {
			started = true
			return chatChunk(id, model, created, map[string]any{"role": "assistant"}, nil), nil
		}
		if ended {
			return nil, io.EOF
		}
		for {
			_, raw, ok, err := decoder.next()
			if err != nil {
				return nil, err
			}
			if !ok {
				return nil, ErrTruncated
			}
			var event struct {
				Type  string `json:"type"`
				Delta struct {
					Type       string `json:"type"`
					Text       string `json:"text"`
					Thinking   string `json:"thinking"`
					StopReason string `json:"stop_reason"`
				} `json:"delta"`
				Block struct {
					Type string `json:"type"`
					Text string `json:"text"`
				} `json:"content_block"`
			}
			if json.Unmarshal([]byte(raw), &event) != nil {
				return nil, errors.New("ZenMux invalid SSE event")
			}
			switch event.Type {
			case "error":
				return nil, errors.New("ZenMux upstream stream failed")
			case "content_block_start":
				if event.Block.Type != "text" && event.Block.Type != "thinking" && event.Block.Type != "redacted_thinking" {
					return nil, errors.New("ZenMux unsupported output block")
				}
				if event.Block.Text != "" {
					return chatChunk(id, model, created, map[string]any{"content": event.Block.Text}, nil), nil
				}
			case "content_block_delta":
				switch event.Delta.Type {
				case "text_delta":
					return chatChunk(id, model, created, map[string]any{"content": event.Delta.Text}, nil), nil
				case "thinking_delta":
					return chatChunk(id, model, created, map[string]any{"reasoning_content": event.Delta.Thinking}, nil), nil
				case "signature_delta":
				default:
					return nil, errors.New("ZenMux unsupported output delta")
				}
			case "message_delta":
				switch event.Delta.StopReason {
				case "end_turn", "stop_sequence":
					finish = "stop"
				case "max_tokens":
					finish = "length"
				default:
					return nil, errors.New("ZenMux unsupported stop reason")
				}
			case "message_stop":
				if finish == "" {
					return nil, ErrTruncated
				}
				ended = true
				cancel()
				source.Close()
				return append(chatChunk(id, model, created, map[string]any{}, finish), []byte("data: [DONE]\n\n")...), nil
			}
		}
	}
	if stream {
		setBody(resp, converted, "text/event-stream")
		return resp, nil
	}
	data, err := readBounded(converted, maxResponseBytes)
	converted.Close()
	if err != nil {
		return nil, err
	}
	text, reasoning, err := aggregateSSE(data)
	if err != nil {
		return nil, err
	}
	message := map[string]any{"role": "assistant", "content": text}
	if reasoning != "" {
		message["reasoning_content"] = reasoning
	}
	result, _ := json.Marshal(map[string]any{"id": id, "object": "chat.completion", "created": created, "model": model, "choices": []any{map[string]any{"index": 0, "message": message, "finish_reason": finish}}})
	setBody(resp, io.NopCloser(bytes.NewReader(result)), "application/json")
	resp.ContentLength = int64(len(result))
	return resp, nil
}
