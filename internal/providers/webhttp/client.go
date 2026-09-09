// Package webhttp implements website chat protocols from pinned reference
// implementations. It never creates accounts or refreshes anonymous quotas.
package webhttp

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"strings"
	"time"

	"clash-of-tokens/internal/config"
	"clash-of-tokens/internal/protocol"
)

const limit = 1 << 20

type Error struct {
	Code    int
	Message string
}

func (e *Error) Error() string     { return e.Message }
func (e *Error) HTTPStatus() int   { return e.Code }
func invalid(message string) error { return &Error{422, message} }

type Client struct {
	source config.Source
	http   *http.Client
}

func Supports(adapter string) bool {
	switch adapter {
	case "venice-web", "inkeep", "gptanon", "perfectassistant", "aifreeforever", "toolbaz", "phindai", "whiterabbitneo":
		return true
	}
	return false
}
func New(source config.Source) *Client {
	capacity := max(1, min(source.MaxInflight, 32))
	tr := &http.Transport{Proxy: http.ProxyFromEnvironment, DialContext: (&net.Dialer{Timeout: 10 * time.Second, KeepAlive: 30 * time.Second}).DialContext, ForceAttemptHTTP2: true, MaxIdleConns: capacity, MaxIdleConnsPerHost: capacity, MaxConnsPerHost: max(1, source.MaxInflight), IdleConnTimeout: 60 * time.Second, TLSHandshakeTimeout: 10 * time.Second, ResponseHeaderTimeout: 60 * time.Second, MaxResponseHeaderBytes: 64 << 10, DisableCompression: true}
	return &Client{source, &http.Client{Transport: tr, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}}
}
func (c *Client) Close() { c.http.CloseIdleConnections() }

type message struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}
type request struct {
	Messages  []message `json:"messages"`
	MaxTokens *int      `json:"max_tokens,omitempty"`
}

func parse(body []byte, adapter string) (request, error) {
	var req request
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(body, &fields); err != nil || fields == nil {
		return req, invalid("a JSON chat request is required")
	}
	for key := range fields {
		switch key {
		case "model", "messages", "stream":
		case "max_tokens":
			if adapter != "venice-web" {
				return req, invalid("max_tokens is not supported by this website adapter")
			}
		default:
			return req, invalid("unsupported website request field: " + key)
		}
	}
	if err := json.Unmarshal(body, &req); err != nil {
		return req, invalid("website adapters require text-only messages")
	}
	if len(req.Messages) == 0 {
		return req, invalid("messages must not be empty")
	}
	var rawMessages []map[string]json.RawMessage
	_ = json.Unmarshal(fields["messages"], &rawMessages)
	for _, m := range rawMessages {
		for key := range m {
			if key != "role" && key != "content" {
				return req, invalid("unsupported message field: " + key)
			}
		}
	}
	for _, m := range req.Messages {
		if adapter == "aifreeforever" && m.Role == "system" {
			return req, invalid("this website does not expose a system instruction channel")
		}
		if m.Role != "user" && m.Role != "assistant" && m.Role != "system" {
			return req, invalid("unsupported message role")
		}
		if m.Content == "" {
			return req, invalid("empty message content")
		}
	}
	if req.Messages[len(req.Messages)-1].Role != "user" {
		return req, invalid("the last message must be a user message")
	}
	if req.MaxTokens != nil && *req.MaxTokens < 1 {
		return req, invalid("max_tokens must be positive")
	}
	if (adapter == "gptanon" || adapter == "perfectassistant" || adapter == "toolbaz" || adapter == "phindai") && (len(req.Messages) != 1 || req.Messages[0].Role != "user") {
		return req, invalid("this upstream accepts one user prompt; conversation history is unsupported")
	}
	return req, nil
}
func (c *Client) Do(ctx context.Context, p, model string, stream bool, body []byte, _ http.Header) (*http.Response, error) {
	if p != "chat" {
		return nil, invalid("website adapter supports Chat Completions only")
	}
	req, e := parse(body, c.source.Adapter)
	if e != nil {
		return nil, e
	}
	if c.source.Adapter == "toolbaz" {
		return c.doToolbaz(ctx, model, stream, req.Messages[0].Content)
	}
	if c.source.Adapter == "phindai" {
		return c.doPhindAI(ctx, model, stream, req.Messages[0].Content)
	}
	if c.source.Adapter == "whiterabbitneo" {
		return c.doWhiteRabbit(ctx, model, stream, req.Messages)
	}
	base := strings.TrimRight(c.source.BaseURL, "/")
	path := ""
	var payload any
	headers := http.Header{"Content-Type": []string{"application/json"}, "Accept": []string{"text/event-stream"}, "User-Agent": []string{"clash-tokens/0.1"}}
	key := strings.TrimSpace(os.Getenv(c.source.KeyEnv))
	if key == "" && !c.source.Anonymous && !c.source.Local {
		return nil, &Error{401, "provider credential is missing"}
	}
	switch c.source.Adapter {
	case "aifreeforever":
		path = "/api/chat"
		messages := make([]any, 0, len(req.Messages))
		for _, m := range req.Messages {
			messages = append(messages, map[string]any{"id": rand.Text(), "role": m.Role, "parts": []any{map[string]string{"type": "text", "text": m.Content}}})
		}
		payload = map[string]any{"id": rand.Text(), "modelId": model, "messages": messages, "trigger": "submit-message"}
		headers.Set("Cookie", key)
		headers.Set("Origin", base)
		headers.Set("Referer", base+"/")
	case "venice-web":
		path = "/api/chat"
		maxTokens := 4096
		if req.MaxTokens != nil {
			maxTokens = *req.MaxTokens
		}
		payload = map[string]any{"messages": req.Messages, "model": model, "stream": true, "max_tokens": maxTokens}
		headers.Set("Origin", base)
		headers.Set("Referer", base+"/")
		if key != "" {
			headers.Set("Cookie", strings.TrimPrefix(key, "Cookie: "))
		}
	case "inkeep":
		path = "/v1/chat/completions"
		payload = map[string]any{"messages": req.Messages, "model": model, "stream": true}
		if !strings.HasPrefix(key, "Bearer ") {
			key = "Bearer " + key
		}
		headers.Set("Authorization", key)
		// The document/site binding belongs to the configured Inkeep token. Do not
		// borrow another site's public embedded token or fabricate its Origin.
	case "gptanon":
		path = "/api/chat/stream"
		payload = map[string]any{"message": req.Messages[0].Content, "modelIds": []string{model}, "deepSearchEnabled": false}
		headers.Set("Origin", base)
		headers.Set("Referer", base+"/chat")
	case "perfectassistant":
		path = "/ai/free"
		payload = map[string]any{"tone": "professional", "language": "chinese", "text": req.Messages[0].Content, "chatId": rand.Text(), "id": model}
		headers.Set("Content-Type", "text/plain;charset=UTF-8")
		headers.Set("Accept", "application/json")
		headers.Set("Origin", base)
	default:
		return nil, invalid("unregistered website adapter")
	}
	b, e := json.Marshal(payload)
	if e != nil {
		return nil, e
	}
	r, e := http.NewRequestWithContext(ctx, "POST", base+path, bytes.NewReader(b))
	if e != nil {
		return nil, errors.New("invalid website endpoint")
	}
	r.Header = headers
	r.GetBody = nil
	upstream, e := c.http.Do(r)
	if e != nil {
		return nil, errors.New("website request failed")
	}
	if upstream.StatusCode < 200 || upstream.StatusCode >= 300 {
		return upstream, nil
	}
	if c.source.Adapter == "perfectassistant" {
		defer upstream.Body.Close()
		data, e := io.ReadAll(io.LimitReader(upstream.Body, limit+1))
		if e != nil || len(data) > limit {
			return nil, errors.New("website response exceeds limit or is incomplete")
		}
		var v struct {
			Response  *string  `json:"response"`
			Responses []string `json:"responses"`
		}
		if json.Unmarshal(data, &v) != nil {
			return nil, errors.New("invalid website response")
		}
		var content string
		if v.Response != nil {
			content = *v.Response
		} else if len(v.Responses) > 0 {
			content = v.Responses[0]
		} else {
			return nil, errors.New("website response contains no answer")
		}
		return output(model, stream, "buffered", func(emit func(string) error) (string, error) { return "stop", emit(content) }, nil), nil
	}
	if !strings.Contains(strings.ToLower(upstream.Header.Get("Content-Type")), "text/event-stream") {
		upstream.Body.Close()
		return nil, errors.New("website did not return the required event stream")
	}
	consume := func(emit func(string) error) (string, error) { return decode(upstream.Body, c.source.Adapter, emit) }
	return output(model, stream, "upstream", consume, upstream.Body), nil
}

// decode requires a protocol completion marker. EOF is never fabricated into
// a successful assistant reply, even if the stream already contained text.
func decode(body io.Reader, adapter string, emit func(string) error) (string, error) {
	reader := protocol.NewSSEReader(body, limit)
	reason := ""
	seen := false
	size := 0
	var accumulated strings.Builder
	send := func(s string) error {
		size += len(s)
		if size > limit {
			return errors.New("website output exceeds limit")
		}
		if adapter == "gptanon" {
			accumulated.WriteString(s)
		}
		return emit(s)
	}
	for {
		frame, e := reader.Next()
		if e == io.EOF {
			if reason != "" {
				return reason, nil
			}
			return "", io.ErrUnexpectedEOF
		}
		if e != nil {
			return "", e
		}
		data := protocol.SSEData(frame)
		if len(data) == 0 {
			continue
		}
		if string(data) == "[DONE]" {
			if !seen {
				return "", errors.New("empty website stream")
			}
			if reason == "" {
				reason = "stop"
			}
			return reason, nil
		}
		var v struct {
			Type    string          `json:"type"`
			Delta   string          `json:"delta"`
			Token   string          `json:"token"`
			Content *string         `json:"content"`
			Error   json.RawMessage `json:"error"`
			Choices []struct {
				Index int `json:"index"`
				Delta struct {
					Content      *string         `json:"content"`
					ToolCalls    json.RawMessage `json:"tool_calls"`
					FunctionCall json.RawMessage `json:"function_call"`
				} `json:"delta"`
				FinishReason *string `json:"finish_reason"`
			} `json:"choices"`
		}
		if json.Unmarshal(data, &v) != nil {
			return "", errors.New("invalid website event JSON")
		}
		if len(v.Error) > 0 && string(v.Error) != "null" {
			return "", errors.New("website reported an error event")
		}
		if v.Type == "error" {
			return "", errors.New("website reported an error event")
		}
		if adapter == "aifreeforever" {
			switch v.Type {
			case "text-delta":
				seen = true
				if e = send(v.Delta); e != nil {
					return "", e
				}
			case "finish":
				if !seen {
					return "", errors.New("website completed without answer")
				}
				return "stop", nil
			case "start", "start-step", "text-start", "text-end", "finish-step", "metadata", "message-metadata":
			default:
				return "", errors.New("unsupported website output event")
			}
			continue
		}
		if adapter == "gptanon" {
			switch v.Type {
			case "token":
				seen = true
				if e = send(v.Token); e != nil {
					return "", e
				}
			case "complete":
				if v.Content == nil {
					return "", errors.New("completion is missing content")
				}
				full := *v.Content
				if !strings.HasPrefix(full, accumulated.String()) {
					return "", errors.New("website revised emitted text")
				}
				if e = send(full[accumulated.Len():]); e != nil {
					return "", e
				}
				seen = true
				return "stop", nil
			case "done":
				if !seen {
					return "", errors.New("completion is missing answer")
				}
				return "stop", nil
			case "ping", "start", "metadata":
			default:
				return "", errors.New("unknown website event")
			}
			continue
		}
		if len(v.Choices) == 0 {
			continue
		}
		if len(v.Choices) != 1 || v.Choices[0].Index != 0 {
			return "", errors.New("website returned unexpected choices")
		}
		choice := v.Choices[0]
		seen = true
		if len(choice.Delta.ToolCalls) > 0 && string(choice.Delta.ToolCalls) != "null" || len(choice.Delta.FunctionCall) > 0 && string(choice.Delta.FunctionCall) != "null" {
			return "", errors.New("website returned unsupported tool calls")
		}
		if choice.Delta.Content != nil {
			if reason != "" {
				return "", errors.New("content after completion")
			}
			if e = send(*choice.Delta.Content); e != nil {
				return "", e
			}
		}
		if choice.FinishReason != nil {
			switch *choice.FinishReason {
			case "stop", "length", "content_filter":
				reason = *choice.FinishReason
			default:
				return "", fmt.Errorf("unsupported website finish reason")
			}
		}
	}
}

type responseBody struct {
	*io.PipeReader
	upstream io.Closer
}

func (b *responseBody) Close() error {
	e := b.PipeReader.Close()
	if b.upstream != nil {
		_ = b.upstream.Close()
	}
	return e
}
func output(model string, stream bool, delivery string, consume func(func(string) error) (string, error), upstream io.Closer) *http.Response {
	reader, writer := io.Pipe()
	headers := make(http.Header)
	headers.Set("Content-Type", "application/json")
	headers.Set("X-COT-Delivery", delivery)
	if stream {
		headers.Set("Content-Type", "text/event-stream")
	}
	go func() {
		if upstream != nil {
			defer upstream.Close()
		}
		id := "chatcmpl-" + rand.Text()
		created := time.Now().Unix()
		event := func(delta any, reason any) error {
			b, e := json.Marshal(map[string]any{"id": id, "object": "chat.completion.chunk", "created": created, "model": model, "choices": []any{map[string]any{"index": 0, "delta": delta, "finish_reason": reason}}})
			if e != nil {
				return e
			}
			_, e = fmt.Fprintf(writer, "data: %s\n\n", b)
			return e
		}
		var text strings.Builder
		if stream {
			if e := event(map[string]string{"role": "assistant"}, nil); e != nil {
				_ = writer.CloseWithError(e)
				return
			}
		}
		reason, e := consume(func(s string) error {
			if stream {
				return event(map[string]string{"content": s}, nil)
			}
			if text.Len()+len(s) > limit {
				return errors.New("website output exceeds limit")
			}
			text.WriteString(s)
			return nil
		})
		if e == nil {
			if stream {
				e = event(map[string]any{}, reason)
				if e == nil {
					_, e = io.WriteString(writer, "data: [DONE]\n\n")
				}
			} else {
				e = json.NewEncoder(writer).Encode(map[string]any{"id": id, "object": "chat.completion", "created": created, "model": model, "choices": []any{map[string]any{"index": 0, "message": map[string]string{"role": "assistant", "content": text.String()}, "finish_reason": reason}}, "usage": nil})
			}
		}
		_ = writer.CloseWithError(e)
	}()
	return &http.Response{StatusCode: 200, Header: headers, Body: &responseBody{reader, upstream}}
}
