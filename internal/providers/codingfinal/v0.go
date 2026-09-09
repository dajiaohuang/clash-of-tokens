package codingfinal

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"clash-of-tokens/internal/config"
)

const (
	v0DefaultBase = "https://v0.dev"
	v0ChatPath    = "/api/chat"
	// The registry also documents this OpenAI-compatible endpoint. Keep it
	// usable when an operator pins it explicitly, while the default follows the
	// pinned web executor above.
	v0CompatiblePath = "/v1/chat/completions"
	v0UserAgent      = "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/149.0.0.0 Safari/537.36"
)

// EncodeV0Request builds the exact body emitted by the pinned v0 web
// executor. v0's web path accepts only text messages, model, and stream.
func EncodeV0Request(req chatRequest, model string, stream bool) ([]byte, error) {
	if len(req.Messages) == 0 {
		return nil, fmt.Errorf("%w: messages are required", ErrUnsupported)
	}
	if len(req.Tools) > 0 || req.ToolChoice != nil {
		return nil, fmt.Errorf("%w: v0 tool execution is not implemented", ErrUnsupported)
	}
	for key := range req.Raw {
		switch key {
		case "messages", "model", "stream":
		default:
			return nil, fmt.Errorf("%w: v0 request field %q is unsupported", ErrUnsupported, key)
		}
	}
	if raw, ok := req.Raw["model"]; ok {
		var requested string
		if json.Unmarshal(raw, &requested) != nil || (requested != "" && requested != model) {
			return nil, fmt.Errorf("%w: request model conflicts with selected model", ErrUnsupported)
		}
	}
	if raw, ok := req.Raw["stream"]; ok {
		var requested bool
		if json.Unmarshal(raw, &requested) != nil || requested != stream {
			return nil, fmt.Errorf("%w: stream flag conflicts with gateway selection", ErrUnsupported)
		}
	}
	messages := make([]map[string]string, 0, len(req.Messages))
	for _, message := range req.Messages {
		if message.Role == "tool" || len(message.ToolCalls) > 0 {
			return nil, fmt.Errorf("%w: v0 tool history is not implemented", ErrUnsupported)
		}
		messages = append(messages, map[string]string{"role": message.Role, "content": message.Content})
	}
	return json.Marshal(map[string]any{"messages": messages, "model": model, "stream": stream})
}

func v0Endpoint(base string) (string, error) {
	if strings.TrimSpace(base) == "" {
		base = v0DefaultBase
	}
	root, err := endpointBase(configSource(base))
	if err != nil {
		return "", err
	}
	if strings.HasSuffix(root, v0ChatPath) || strings.HasSuffix(root, v0CompatiblePath) {
		return root, nil
	}
	return root + v0ChatPath, nil
}

// configSource avoids exposing a second URL validation helper in this adapter.
func configSource(base string) config.Source { return config.Source{BaseURL: base} }

func v0Cookie(raw string) (string, error) {
	cookie := strings.TrimSpace(raw)
	if strings.HasPrefix(strings.ToLower(cookie), "cookie:") {
		cookie = strings.TrimSpace(cookie[len("cookie:"):])
	}
	if cookie == "" || len(cookie) > maxHeaderValue || strings.ContainsAny(cookie, "\r\n\x00") {
		return "", ErrCredential
	}
	return cookie, nil
}

func (c *Client) doV0(ctx context.Context, token, model string, stream bool, req chatRequest) (*http.Response, error) {
	body, err := EncodeV0Request(req, model, stream)
	if err != nil {
		return nil, err
	}
	cookie, err := v0Cookie(token)
	if err != nil {
		return nil, err
	}
	endpoint, err := v0Endpoint(c.source.BaseURL)
	if err != nil {
		return nil, err
	}
	headers := http.Header{
		"Accept":       []string{"application/json"},
		"Content-Type": []string{"application/json"},
		"Origin":       []string{"https://v0.dev"},
		"Referer":      []string{"https://v0.dev/"},
		"User-Agent":   []string{v0UserAgent},
		"Cookie":       []string{cookie},
	}
	if stream {
		headers.Set("Accept", "text/event-stream")
	}
	return post(ctx, c.http, endpoint, body, headers)
}

func validateV0SSE(ctx context.Context, r io.Reader) ([]byte, error) {
	data, err := readOpenAISSE(ctx, r)
	if err != nil {
		return nil, err
	}
	if _, err := collectOpenAISSE(ctx, bytes.NewReader(data), "upstream"); err != nil {
		return nil, err
	}
	return data, nil
}

func v0ToJSON(ctx context.Context, r io.Reader, model string) ([]byte, error) {
	data, err := io.ReadAll(io.LimitReader(r, maxEventBytes+1))
	if err != nil {
		return nil, err
	}
	if int64(len(data)) > maxEventBytes {
		return nil, fmt.Errorf("%w: v0 response exceeds byte limit", ErrUnsupported)
	}
	trimmed := bytes.TrimSpace(data)
	if len(trimmed) == 0 {
		return nil, ErrTruncated
	}
	if trimmed[0] != '{' {
		return collectOpenAISSE(ctx, bytes.NewReader(data), model)
	}
	var root map[string]any
	if json.Unmarshal(trimmed, &root) != nil || root == nil {
		return nil, fmt.Errorf("%w: malformed v0 JSON response", ErrTruncated)
	}
	if err := parseStreamError(root); err != nil {
		return nil, err
	}
	if choices, ok := root["choices"]; ok {
		if _, ok := choices.([]any); !ok {
			return nil, fmt.Errorf("%w: malformed v0 choices", ErrTruncated)
		}
		if _, ok := root["object"]; !ok {
			root["object"] = "chat.completion"
		}
		if _, ok := root["model"]; !ok {
			root["model"] = model
		}
		return json.Marshal(root)
	}
	content, contentOK := root["content"].(string)
	if !contentOK {
		return nil, fmt.Errorf("%w: v0 response has no completion choices", ErrTruncated)
	}
	message := map[string]any{"role": "assistant", "content": content}
	if reasoning, ok := root["reasoning_content"].(string); ok && reasoning != "" {
		message["reasoning_content"] = reasoning
	}
	return json.Marshal(map[string]any{
		"id":      "chatcmpl-v0-" + randomUUID(),
		"object":  "chat.completion",
		"created": time.Now().Unix(),
		"model":   model,
		"choices": []any{map[string]any{"index": 0, "message": message, "finish_reason": "stop"}},
	})
}
