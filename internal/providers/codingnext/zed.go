package codingnext

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
)

const (
	zedProviderAnthropic = "anthropic"
	zedProviderOpenAI    = "open_ai"
	zedProviderGoogle    = "google"
	zedProviderXAI       = "x_ai"
)

func inferZedProvider(model string) string {
	m := strings.ToLower(model)
	switch {
	case strings.Contains(m, "claude") || strings.Contains(m, "anthropic"):
		return zedProviderAnthropic
	case strings.Contains(m, "gemini") || strings.Contains(m, "google"):
		return zedProviderGoogle
	case strings.Contains(m, "grok") || strings.Contains(m, "xai") || strings.Contains(m, "x-ai"):
		return zedProviderXAI
	default:
		return zedProviderOpenAI
	}
}

func (c *Client) doZedHosted(ctx context.Context, token, model string, stream bool, request openAIRequest, state *session) (*http.Response, error) {
	provider := inferZedProvider(model)
	providerRequest, err := zedProviderRequest(provider, model, request.payload, stream)
	if err != nil {
		return nil, err
	}
	threadID := state.threadID
	if explicit, ok := request.payload["thread_id"].(string); ok && explicit != "" {
		threadID = explicit
	}
	promptID := ""
	if explicit, ok := request.payload["prompt_id"].(string); ok && explicit != "" {
		promptID = explicit
	}
	if promptID == "" {
		promptID = uuid()
	}
	payload := map[string]any{
		"thread_id":        threadID,
		"prompt_id":        promptID,
		"provider":         provider,
		"model":            model,
		"provider_request": providerRequest,
	}
	encoded, err := json.Marshal(payload)
	if err != nil {
		return nil, fmt.Errorf("coding next adapter: encode Zed envelope: %w", err)
	}
	target, err := endpoint(c.source, "https://cloud.zed.dev", "/completions")
	if err != nil {
		return nil, err
	}
	headers := http.Header{}
	headers.Set("Accept", "application/x-ndjson, text/event-stream, */*")
	headers.Set("User-Agent", "OmniRoute/zed-hosted")
	headers.Set("x-zed-client-supports-status-messages", "true")
	headers.Set("x-zed-client-supports-stream-ended-request-completion-status", "true")
	return postJSON(ctx, c.http, target, token, encoded, headers)
}

// zedProviderRequest implements the narrow, lossless subset for which this
// package has a native conversion. Tool calls and multimodal parts are
// rejected because translating them to every Zed backend would otherwise drop
// data or change tool semantics.
func zedProviderRequest(provider, model string, input map[string]any, stream bool) (any, error) {
	if len(input) == 0 {
		return nil, fmt.Errorf("%w: empty Zed request", ErrUnsupported)
	}
	for key := range input {
		switch key {
		case "model", "messages", "stream", "thread_id", "prompt_id", "max_tokens", "max_completion_tokens", "temperature", "top_p", "stop", "reasoning_effort", "tools", "response_format":
		default:
			return nil, fmt.Errorf("%w: Zed field %q cannot be represented", ErrUnsupported, key)
		}
	}
	for _, key := range []string{"max_tokens", "max_completion_tokens"} {
		if value, exists := input[key]; exists {
			if _, valid := numericValue(value); !valid {
				return nil, fmt.Errorf("%w: invalid %s", ErrUnsupported, key)
			}
		}
	}
	if tools, ok := input["tools"]; ok && tools != nil {
		if list, ok := tools.([]any); !ok || len(list) > 0 {
			return nil, fmt.Errorf("%w: Zed hosted tool calls are not implemented", ErrUnsupported)
		}
	}
	if value, ok := input["response_format"]; ok && value != nil {
		return nil, fmt.Errorf("%w: Zed hosted response_format is not implemented", ErrUnsupported)
	}
	messages, ok := input["messages"].([]any)
	if !ok || len(messages) == 0 {
		return nil, fmt.Errorf("%w: Zed messages are required", ErrUnsupported)
	}
	for _, raw := range messages {
		message, ok := raw.(map[string]any)
		if !ok {
			return nil, fmt.Errorf("%w: Zed message must be an object", ErrUnsupported)
		}
		for key := range message {
			if key != "role" && key != "content" {
				return nil, fmt.Errorf("%w: unsupported Zed message field %s", ErrUnsupported, key)
			}
		}
		if message["role"] == "tool" {
			return nil, fmt.Errorf("%w: Zed tool history is unsupported", ErrUnsupported)
		}
		if content := message["content"]; content != nil && !isTextContent(content) {
			return nil, fmt.Errorf("%w: Zed hosted multimodal content is not implemented", ErrUnsupported)
		}
		if _, ok := message["tool_calls"]; ok {
			return nil, fmt.Errorf("%w: Zed hosted tool calls are not implemented", ErrUnsupported)
		}
	}
	switch provider {
	case zedProviderAnthropic:
		// The Zed aggregator's Anthropic/OpenAI translators are streaming
		// contracts even when this adapter later aggregates for the caller.
		return zedAnthropicRequest(model, input, messages, true)
	case zedProviderGoogle:
		return zedGeminiRequest(model, input, messages)
	case zedProviderOpenAI:
		return zedResponsesRequest(model, input, messages, true)
	case zedProviderXAI:
		out := clonePayload(input)
		delete(out, "thread_id")
		delete(out, "prompt_id")
		out["model"] = model
		out["stream"] = true
		return out, nil
	default:
		return nil, fmt.Errorf("%w: unknown Zed provider %q", ErrUnsupported, provider)
	}
}

func isTextContent(content any) bool {
	if content == nil {
		return true
	}
	if _, ok := content.(string); ok {
		return true
	}
	parts, ok := content.([]any)
	if !ok {
		return false
	}
	for _, raw := range parts {
		part, ok := raw.(map[string]any)
		if !ok || part["type"] != "text" {
			return false
		}
		if _, ok := part["text"].(string); !ok {
			return false
		}
		for key := range part {
			if key != "type" && key != "text" {
				return false
			}
		}
	}
	return true
}

func textContent(value any) string {
	if text, ok := value.(string); ok {
		return text
	}
	parts, _ := value.([]any)
	var out strings.Builder
	for _, raw := range parts {
		if part, ok := raw.(map[string]any); ok {
			if text, ok := part["text"].(string); ok {
				out.WriteString(text)
			}
		}
	}
	return out.String()
}

func zedAnthropicRequest(model string, input map[string]any, messages []any, stream bool) (any, error) {
	out := map[string]any{"model": model, "messages": make([]any, 0, len(messages)), "stream": stream}
	if max, ok := numericValue(input["max_completion_tokens"]); ok {
		out["max_tokens"] = max
	} else if max, ok := numericValue(input["max_tokens"]); ok {
		out["max_tokens"] = max
	} else {
		out["max_tokens"] = 4096
	}
	if value, ok := input["temperature"]; ok {
		out["temperature"] = value
	}
	if value, ok := input["top_p"]; ok {
		out["top_p"] = value
	}
	if value, ok := input["stop"]; ok {
		out["stop_sequences"] = value
	}
	if value, ok := input["reasoning_effort"]; ok && value != nil {
		return nil, fmt.Errorf("%w: Zed Anthropic reasoning_effort needs a provider-specific thinking budget", ErrUnsupported)
	}
	var system []string
	for _, raw := range messages {
		message := raw.(map[string]any)
		role, _ := message["role"].(string)
		content := textContent(message["content"])
		switch role {
		case "system", "developer":
			system = append(system, content)
		case "user", "assistant":
			out["messages"] = append(out["messages"].([]any), map[string]any{
				"role":    role,
				"content": []any{map[string]any{"type": "text", "text": content}},
			})
		default:
			return nil, fmt.Errorf("%w: Zed Anthropic role %q is unsupported", ErrUnsupported, role)
		}
	}
	if len(system) > 0 {
		blocks := make([]any, 0, len(system))
		for _, text := range system {
			blocks = append(blocks, map[string]any{"type": "text", "text": text})
		}
		out["system"] = blocks
	}
	return out, nil
}

func zedGeminiRequest(model string, input map[string]any, messages []any) (any, error) {
	contents := make([]any, 0, len(messages))
	for _, raw := range messages {
		message := raw.(map[string]any)
		role, _ := message["role"].(string)
		if role == "system" || role == "developer" {
			return nil, fmt.Errorf("%w: Gemini system instructions require a separate configured contract", ErrUnsupported)
		}
		if role != "user" && role != "assistant" {
			return nil, fmt.Errorf("%w: Gemini role %q is unsupported", ErrUnsupported, role)
		}
		wireRole := role
		if wireRole == "assistant" {
			wireRole = "model"
		}
		contents = append(contents, map[string]any{"role": wireRole, "parts": []any{map[string]any{"text": textContent(message["content"])}}})
	}
	out := map[string]any{"model": model, "contents": contents}
	generation := map[string]any{}
	if config, ok := input["temperature"]; ok {
		generation["temperature"] = config
	}
	if topP, ok := input["top_p"]; ok {
		generation["topP"] = topP
	}
	if max, ok := numericValue(input["max_completion_tokens"]); ok {
		generation["maxOutputTokens"] = max
	} else if max, ok := numericValue(input["max_tokens"]); ok {
		generation["maxOutputTokens"] = max
	}
	if stop, ok := input["stop"]; ok {
		generation["stopSequences"] = stop
	}
	if len(generation) > 0 {
		out["generationConfig"] = generation
	}
	if value, ok := input["reasoning_effort"]; ok && value != nil {
		return nil, fmt.Errorf("%w: Zed Gemini reasoning_effort is not implemented", ErrUnsupported)
	}
	return out, nil
}

func zedResponsesRequest(model string, input map[string]any, messages []any, stream bool) (any, error) {
	items := make([]any, 0, len(messages))
	for _, raw := range messages {
		message := raw.(map[string]any)
		role, _ := message["role"].(string)
		if role == "tool" {
			return nil, fmt.Errorf("%w: OpenAI tool history is not implemented for Zed", ErrUnsupported)
		}
		if role != "system" && role != "developer" && role != "user" && role != "assistant" {
			return nil, fmt.Errorf("%w: OpenAI role %q is unsupported for Zed", ErrUnsupported, role)
		}
		items = append(items, map[string]any{"role": role, "content": textContent(message["content"])})
	}
	out := map[string]any{"model": model, "input": items, "stream": stream, "store": false}
	for _, key := range []string{"temperature", "top_p"} {
		if value, ok := input[key]; ok {
			out[key] = value
		}
	}
	if value, exists := input["reasoning_effort"]; exists {
		effort, ok := value.(string)
		if !ok || effort == "" {
			return nil, fmt.Errorf("%w: invalid reasoning_effort", ErrUnsupported)
		}
		out["reasoning"] = map[string]any{"effort": effort}
	}
	if max, ok := numericValue(input["max_completion_tokens"]); ok {
		out["max_output_tokens"] = max
	} else if max, ok := numericValue(input["max_tokens"]); ok {
		out["max_output_tokens"] = max
	}
	if _, ok := input["stop"]; ok {
		return nil, fmt.Errorf("%w: OpenAI Responses stop is not implemented for Zed", ErrUnsupported)
	}
	return out, nil
}

func numericValue(value any) (int, bool) {
	switch n := value.(type) {
	case float64:
		return int(n), n > 0 && n == float64(int(n))
	case int:
		return n, n > 0
	case json.Number:
		parsed, err := n.Int64()
		return int(parsed), err == nil && parsed > 0
	default:
		return 0, false
	}
}

// convertOpenAIResponse wraps a provider response. CodeBuddy emits native
// OpenAI SSE. Zed emits NDJSON lines containing provider events and status
// messages. Both are converted to one bounded OpenAI response stream.
func (c *Client) convertOpenAIResponse(ctx context.Context, response *http.Response, model string, stream bool, adapter string) (*http.Response, error) {
	if response.Body == nil {
		return nil, errors.New("coding next adapter: upstream response has no body")
	}
	if adapter == AdapterFreebuff {
		return validateFreebuffResponse(ctx, response, model, stream)
	}
	if !stream {
		var data []byte
		var err error
		if adapter == AdapterCodeBuddy {
			data, err = streamToOpenAIJSON(ctx, response.Body, model, maxResponseBytes)
		} else {
			data, err = zedNDJSONToJSON(ctx, response.Body, model, maxResponseBytes)
		}
		response.Body.Close()
		if err != nil {
			return nil, err
		}
		response.StatusCode = http.StatusOK
		response.Status = "200 OK"
		response.Header = cloneHeaders(response.Header)
		response.Header.Set("Content-Type", "application/json")
		response.Header.Set("Content-Length", fmt.Sprintf("%d", len(data)))
		response.ContentLength = int64(len(data))
		response.Body = io.NopCloser(strings.NewReader(string(data)))
		return response, nil
	}
	reader, writer := io.Pipe()
	upstream := response.Body
	go func() {
		defer upstream.Close()
		var err error
		if adapter == AdapterCodeBuddy {
			err = copyValidatedOpenAISSE(ctx, upstream, writer)
		} else {
			err = convertZedStream(ctx, upstream, writer, model)
		}
		if err != nil {
			_ = writer.CloseWithError(err)
		} else {
			_ = writer.Close()
		}
	}()
	response.Header = cloneHeaders(response.Header)
	response.Header.Set("Content-Type", "text/event-stream")
	response.Header.Set("Cache-Control", "no-cache")
	response.Header.Set("X-Accel-Buffering", "no")
	response.Header.Del("Content-Length")
	response.ContentLength = -1
	response.Body = &upstreamPipeBody{PipeReader: reader, upstream: upstream}
	return response, nil
}

func validateFreebuffResponse(ctx context.Context, response *http.Response, model string, stream bool) (*http.Response, error) {
	if !stream {
		data, err := readLimited(response.Body, maxResponseBytes)
		response.Body.Close()
		if err != nil {
			return nil, err
		}
		var value map[string]any
		if err := json.Unmarshal(data, &value); err != nil || value == nil {
			return nil, fmt.Errorf("%w: malformed Freebuff JSON response", ErrTruncated)
		}
		if value["error"] != nil {
			return nil, errors.New("coding next adapter: Freebuff reported an error")
		}
		choices, ok := value["choices"].([]any)
		if !ok || len(choices) == 0 {
			return nil, ErrTruncated
		}
		for _, raw := range choices {
			choice, ok := raw.(map[string]any)
			if !ok || choice["message"] == nil || choice["finish_reason"] == nil {
				return nil, ErrTruncated
			}
		}
		response.Header = cloneHeaders(response.Header)
		response.Header.Set("Content-Type", "application/json")
		response.Header.Set("Content-Length", fmt.Sprintf("%d", len(data)))
		response.ContentLength = int64(len(data))
		response.Body = io.NopCloser(bytes.NewReader(data))
		return response, nil
	}
	reader, writer := io.Pipe()
	upstream := response.Body
	go func() {
		defer upstream.Close()
		err := copyValidatedOpenAISSE(ctx, upstream, writer)
		if err != nil {
			_ = writer.CloseWithError(err)
		} else {
			_ = writer.Close()
		}
	}()
	response.Header = cloneHeaders(response.Header)
	response.Header.Set("Content-Type", "text/event-stream")
	response.Header.Set("Cache-Control", "no-cache")
	response.Header.Del("Content-Length")
	response.ContentLength = -1
	response.Body = &upstreamPipeBody{PipeReader: reader, upstream: upstream}
	return response, nil
}

type upstreamPipeBody struct {
	*io.PipeReader
	upstream io.Closer
}

func (b *upstreamPipeBody) Close() error {
	err := b.PipeReader.Close()
	if b.upstream != nil {
		_ = b.upstream.Close()
	}
	return err
}
