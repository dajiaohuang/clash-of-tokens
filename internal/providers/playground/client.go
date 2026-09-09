// Package playground implements Cloudflare's public browser-based AI
// playground protocol through an explicitly configured Chrome CDP endpoint.
package playground

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"

	"clash-of-tokens/internal/config"
)

const origin = "https://playground.ai.cloudflare.com/"
const maxFrame = 1 << 20
const maxInput = 64 << 10
const maxOutput = 4 << 20

type Error struct {
	Code    int
	Message string
}

func (e *Error) Error() string   { return e.Message }
func (e *Error) HTTPStatus() int { return e.Code }
func invalid(msg string) error   { return &Error{422, msg} }

type frameTransport interface {
	Next(context.Context) (string, error)
	Close() error
}
type Client struct {
	browser config.Browser
	start   func(context.Context, string, any) (frameTransport, error)
}

func New(browser config.Browser) *Client {
	c := &Client{browser: browser}
	c.start = c.startBrowser
	return c
}
func (c *Client) Close() {}

type message struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

func requestPayload(body []byte, model string) (map[string]any, error) {
	if len(body) > maxInput {
		return nil, invalid("playground request exceeds byte limit")
	}
	var fields map[string]json.RawMessage
	if json.Unmarshal(body, &fields) != nil || fields == nil {
		return nil, invalid("playground requires a JSON request")
	}
	for k := range fields {
		if k != "model" && k != "messages" && k != "stream" && k != "temperature" {
			return nil, invalid("unsupported playground field: " + k)
		}
	}
	var raw []map[string]json.RawMessage
	if json.Unmarshal(fields["messages"], &raw) != nil || len(raw) == 0 {
		return nil, invalid("playground requires text messages")
	}
	messages := make([]any, 0, len(raw))
	for i, m := range raw {
		for k := range m {
			if k != "role" && k != "content" {
				return nil, invalid("unsupported playground message field: " + k)
			}
		}
		var role, content string
		if json.Unmarshal(m["role"], &role) != nil || (role != "user" && role != "assistant") || json.Unmarshal(m["content"], &content) != nil || content == "" {
			return nil, invalid("playground accepts only user/assistant text messages")
		}
		messages = append(messages, map[string]any{"id": fmt.Sprintf("m%d", i+1), "role": role, "parts": []any{map[string]string{"type": "text", "text": content}}})
	}
	if messages[len(messages)-1].(map[string]any)["role"] != "user" {
		return nil, invalid("playground last message must be user")
	}
	temperature := 0.7
	if raw, ok := fields["temperature"]; ok {
		if string(raw) == "null" || json.Unmarshal(raw, &temperature) != nil || temperature < 0 || temperature > 2 {
			return nil, invalid("playground temperature must be between 0 and 2")
		}
	}
	if strings.TrimSpace(model) == "" {
		return nil, invalid("playground model required")
	}
	if !strings.HasPrefix(model, "@cf/") {
		model = "@cf/" + model
	}
	return map[string]any{"model": model, "messages": messages, "temperature": temperature}, nil
}

func (c *Client) Do(ctx context.Context, protocol, model string, stream bool, body []byte, headers http.Header) (*http.Response, error) {
	if protocol != "chat" {
		return nil, invalid("playground supports Chat Completions only")
	}
	if headers.Get("X-COT-Session") != "" {
		return nil, invalid("playground uses request-local rooms; send full text history")
	}
	payload, err := requestPayload(body, model)
	if err != nil {
		return nil, err
	}
	if !c.browser.Enabled {
		return nil, &Error{503, "playground requires browser.enabled and a running configured Chrome"}
	}
	turn, cancel := context.WithTimeout(ctx, 120*time.Second)
	id := "chatcmpl-cfp-" + rand.Text()
	transport, err := c.start(turn, id, payload)
	if err != nil {
		cancel()
		return nil, err
	}
	cleanup := func() { cancel(); _ = transport.Close() }
	if !stream {
		defer cleanup()
		var text, reasoning strings.Builder
		finish, err := consume(turn, transport, id, func(field, value string) error {
			if field == "content" {
				text.WriteString(value)
			} else {
				reasoning.WriteString(value)
			}
			return nil
		})
		if err != nil {
			return nil, err
		}
		message := map[string]any{"role": "assistant", "content": text.String()}
		if reasoning.Len() > 0 {
			message["reasoning_content"] = reasoning.String()
		}
		b, _ := json.Marshal(map[string]any{"id": id, "object": "chat.completion", "model": model, "created": time.Now().Unix(), "choices": []any{map[string]any{"index": 0, "message": message, "finish_reason": finish}}})
		return &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": {"application/json"}}, Body: io.NopCloser(bytes.NewReader(b)), ContentLength: int64(len(b))}, nil
	}
	r, w := io.Pipe()
	go func() {
		defer cleanup()
		created := time.Now().Unix()
		emit := func(delta any, finish any) error {
			b, _ := json.Marshal(map[string]any{"id": id, "object": "chat.completion.chunk", "model": model, "created": created, "choices": []any{map[string]any{"index": 0, "delta": delta, "finish_reason": finish}}})
			_, err := fmt.Fprintf(w, "data: %s\n\n", b)
			return err
		}
		err := emit(map[string]string{"role": "assistant"}, nil)
		if err == nil {
			var finish string
			finish, err = consume(turn, transport, id, func(field, value string) error { return emit(map[string]string{field: value}, nil) })
			if err == nil {
				err = emit(map[string]any{}, finish)
			}
			if err == nil {
				_, err = io.WriteString(w, "data: [DONE]\n\n")
			}
		}
		_ = w.CloseWithError(err)
	}()
	return &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": {"text/event-stream"}}, Body: &streamBody{PipeReader: r, cleanup: cleanup}, ContentLength: -1}, nil
}

type streamBody struct {
	*io.PipeReader
	once    sync.Once
	cleanup func()
}

func (b *streamBody) Close() error { err := b.PipeReader.Close(); b.once.Do(b.cleanup); return err }

// Only chat frames for this exact request can complete the response. A
// setConfig RPC's done flag does not indicate a completed model turn.
func consume(ctx context.Context, t frameTransport, id string, emit func(string, string) error) (string, error) {
	finish := ""
	read, output := 0, 0
	for {
		raw, err := t.Next(ctx)
		if err != nil {
			if ctx.Err() != nil {
				return "", ctx.Err()
			}
			return "", io.ErrUnexpectedEOF
		}
		read += len(raw)
		if len(raw) > maxFrame || read > 16<<20 {
			return "", errors.New("playground frame limit exceeded")
		}
		var frame struct {
			Type  string          `json:"type"`
			ID    string          `json:"id"`
			Error bool            `json:"error"`
			Done  bool            `json:"done"`
			Body  json.RawMessage `json:"body"`
		}
		if json.Unmarshal([]byte(raw), &frame) != nil {
			return "", errors.New("invalid playground frame")
		}
		if frame.Type != "cf_agent_use_chat_response" || frame.ID != id {
			continue
		}
		if frame.Error {
			return "", &Error{502, "playground upstream reported an error"}
		}
		if frame.Done {
			if finish == "" {
				return "", io.ErrUnexpectedEOF
			}
			return finish, nil
		}
		data := frame.Body
		if len(data) > 0 && data[0] == '"' {
			var s string
			if json.Unmarshal(data, &s) != nil {
				return "", errors.New("invalid playground event")
			}
			data = []byte(s)
		}
		var event struct {
			Type     string `json:"type"`
			Delta    string `json:"delta"`
			Metadata struct {
				FinishReason string `json:"finishReason"`
			} `json:"messageMetadata"`
		}
		if json.Unmarshal(data, &event) != nil {
			return "", errors.New("invalid playground event")
		}
		field := ""
		switch event.Type {
		case "text-delta":
			field = "content"
		case "reasoning-delta":
			field = "reasoning_content"
		case "finish":
			finish = event.Metadata.FinishReason
			switch finish {
			case "":
				finish = "stop"
			case "stop", "length", "content-filter", "content_filter":
				if finish == "content-filter" {
					finish = "content_filter"
				}
			default:
				return "", errors.New("unsupported playground finish reason")
			}
		case "start", "start-step", "finish-step", "reasoning-start", "reasoning-end", "text-start", "text-end":
		default:
			return "", errors.New("unsupported playground output event")
		}
		if field != "" {
			if finish != "" {
				return "", errors.New("playground sent content after finish")
			}
			output += len(event.Delta)
			if output > maxOutput {
				return "", errors.New("playground output limit exceeded")
			}
			if err := emit(field, event.Delta); err != nil {
				return "", err
			}
		}
	}
}
