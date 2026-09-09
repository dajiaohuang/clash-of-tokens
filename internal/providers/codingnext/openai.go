package codingnext

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"strings"
	"time"

	"clash-of-tokens/internal/protocol"
)

// openAIRequest is intentionally a lossless map. The Freebuff and CodeBuddy
// references forward OpenAI history, tools, and provider extensions. Decoding
// into a narrow struct would silently erase fields, so only the message
// container is validated and every other field is retained verbatim.
type openAIRequest struct {
	payload map[string]any
}

func decodeOpenAIRequest(body []byte) (openAIRequest, error) {
	var request openAIRequest
	if len(body) == 0 || !json.Valid(body) {
		return request, fmt.Errorf("%w: a JSON Chat Completions request is required", ErrUnsupported)
	}
	if err := json.Unmarshal(body, &request.payload); err != nil || request.payload == nil {
		return request, fmt.Errorf("%w: request must be a JSON object", ErrUnsupported)
	}
	messages, ok := request.payload["messages"].([]any)
	if !ok || len(messages) == 0 {
		return request, fmt.Errorf("%w: messages must be a non-empty array", ErrUnsupported)
	}
	for i, raw := range messages {
		message, ok := raw.(map[string]any)
		if !ok || message == nil {
			return request, fmt.Errorf("%w: message %d must be an object", ErrUnsupported, i)
		}
		role, ok := message["role"].(string)
		if !ok || role == "" {
			return request, fmt.Errorf("%w: message %d has no role", ErrUnsupported, i)
		}
		switch role {
		case "system", "developer", "user", "assistant", "tool":
		default:
			return request, fmt.Errorf("%w: message role %q is unsupported", ErrUnsupported, role)
		}
		if _, ok := message["content"]; !ok && role != "assistant" && role != "tool" {
			return request, fmt.Errorf("%w: message %d has no content", ErrUnsupported, i)
		}
	}
	return request, nil
}

func clonePayload(in map[string]any) map[string]any {
	out := make(map[string]any, len(in))
	for key, value := range in {
		out[key] = value
	}
	return out
}

func encodePayload(payload map[string]any) ([]byte, error) {
	data, err := json.Marshal(payload)
	if err != nil {
		return nil, fmt.Errorf("coding next adapter: encode request: %w", err)
	}
	return data, nil
}

func reasoningOptIn(payload map[string]any) {
	value, ok := payload["reasoning_effort"].(string)
	if !ok || value == "" {
		return
	}
	if value == "none" || value == "off" {
		delete(payload, "reasoning_effort")
		return
	}
	payload["reasoning_summary"] = "auto"
}

// streamToOpenAIJSON aggregates an OpenAI-compatible SSE stream without
// dropping tool-call deltas. It requires [DONE] or a finish_reason; EOF alone
// is not completion evidence.
func streamToOpenAIJSON(ctx context.Context, body io.Reader, model string, limit int64) ([]byte, error) {
	reader := protocol.NewSSEReader(body, maxEventBytes)
	var content strings.Builder
	var reasoning strings.Builder
	var toolCalls []any
	var finish any
	var id string
	var created int64
	seenDone := false
	seenEvent := false
	var totalInput int64
	for {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		frame, err := reader.Next()
		if err == io.EOF {
			if seenDone {
				break
			}
			return nil, ErrTruncated
		}
		if err != nil {
			return nil, fmt.Errorf("%w: %v", ErrTruncated, err)
		}
		totalInput += int64(len(frame))
		if totalInput > maxResponseBytes {
			return nil, errorsResponseTooLarge()
		}
		data := bytes.TrimSpace(protocol.SSEData(frame))
		if len(data) == 0 {
			continue
		}
		if bytes.Equal(data, []byte("[DONE]")) {
			seenDone = true
			break
		}
		var chunk map[string]any
		if err := json.Unmarshal(data, &chunk); err != nil || chunk == nil {
			return nil, fmt.Errorf("%w: malformed OpenAI SSE event", ErrTruncated)
		}
		if err := openAIStreamError(chunk); err != nil {
			return nil, err
		}
		if value, ok := chunk["id"].(string); ok && value != "" {
			id = value
		}
		if value, ok := chunk["created"].(float64); ok {
			created = int64(value)
		}
		rawChoices, present := chunk["choices"]
		if !present {
			continue
		}
		choices, ok := rawChoices.([]any)
		if !ok || rawChoices == nil {
			return nil, fmt.Errorf("%w: malformed OpenAI choices", ErrTruncated)
		}
		if len(choices) == 0 {
			continue
		}
		seenEvent = true
		for index, rawChoice := range choices {
			choice, ok := rawChoice.(map[string]any)
			if !ok || index != 0 {
				return nil, fmt.Errorf("%w: multiple or malformed choices", ErrUnsupported)
			}
			if value, ok := choice["finish_reason"]; ok && value != nil {
				finish = value
			}
			delta, hasDelta := choice["delta"].(map[string]any)
			if rawDelta, present := choice["delta"]; present && rawDelta != nil && !hasDelta {
				return nil, fmt.Errorf("%w: malformed OpenAI delta", ErrTruncated)
			}
			if value, ok := delta["content"].(string); ok {
				content.WriteString(value)
			}
			if value, ok := delta["reasoning_content"].(string); ok {
				reasoning.WriteString(value)
			}
			if rawCalls, present := delta["tool_calls"]; present && rawCalls != nil {
				calls, ok := rawCalls.([]any)
				if !ok {
					return nil, fmt.Errorf("%w: malformed tool call deltas", ErrTruncated)
				}
				var err error
				toolCalls, err = appendToolCallDeltasChecked(toolCalls, calls)
				if err != nil {
					return nil, err
				}
			}
			if rawCall, present := delta["function_call"]; present && rawCall != nil {
				call, ok := rawCall.(map[string]any)
				if !ok {
					return nil, fmt.Errorf("%w: malformed function call delta", ErrTruncated)
				}
				var err error
				toolCalls, err = appendToolCallDeltasChecked(toolCalls, []any{map[string]any{"index": 0, "function": call}})
				if err != nil {
					return nil, err
				}
			}
		}
		if streamOutputBytes(content.Len(), reasoning.Len(), toolCalls) > limit {
			return nil, errorsResponseTooLarge()
		}
	}
	if !seenEvent {
		return nil, ErrTruncated
	}
	if !seenDone && finish == nil {
		return nil, ErrTruncated
	}
	if id == "" {
		id = "chatcmpl-" + uuid()
	}
	if created == 0 {
		created = nowUnix()
	}
	message := map[string]any{"role": "assistant", "content": content.String()}
	if reasoning.Len() > 0 {
		message["reasoning_content"] = reasoning.String()
	}
	if len(toolCalls) > 0 {
		message["tool_calls"] = toolCalls
	}
	choice := map[string]any{"index": 0, "message": message, "finish_reason": finish}
	if choice["finish_reason"] == nil {
		choice["finish_reason"] = "stop"
	}
	result, err := json.Marshal(map[string]any{
		"id": id, "object": "chat.completion", "created": created, "model": model,
		"choices": []any{choice},
	})
	if err != nil {
		return nil, err
	}
	if int64(len(result)) > limit {
		return nil, errorsResponseTooLarge()
	}
	return result, nil
}

const maxToolCalls = 4096

// appendToolCallDeltas is retained as a small compatibility helper for
// package users and tests. Stream conversion uses the checked variant so a
// malformed upstream tool delta cannot become a successful response.
func appendToolCallDeltas(existing []any, deltas []any) []any {
	updated, err := appendToolCallDeltasChecked(existing, deltas)
	if err != nil {
		return existing
	}
	return updated
}

func appendToolCallDeltasChecked(existing []any, deltas []any) ([]any, error) {
	for _, raw := range deltas {
		delta, ok := raw.(map[string]any)
		if !ok {
			return nil, fmt.Errorf("%w: malformed tool call delta", ErrTruncated)
		}
		index, err := toolCallIndex(delta["index"])
		if err != nil {
			return nil, err
		}
		if index >= maxToolCalls {
			return nil, fmt.Errorf("%w: tool call index exceeds limit", ErrUnsupported)
		}
		if rawID, present := delta["id"]; present && rawID != nil {
			if _, ok := rawID.(string); !ok {
				return nil, fmt.Errorf("%w: tool call id must be text", ErrTruncated)
			}
		}
		if rawType, present := delta["type"]; present && rawType != nil {
			if _, ok := rawType.(string); !ok {
				return nil, fmt.Errorf("%w: tool call type must be text", ErrTruncated)
			}
		}
		incoming, hasFunction := delta["function"].(map[string]any)
		if rawFunction, present := delta["function"]; present && rawFunction != nil && !hasFunction {
			return nil, fmt.Errorf("%w: malformed tool function delta", ErrTruncated)
		}
		if hasFunction {
			if value, present := incoming["name"]; present && value != nil {
				if _, ok := value.(string); !ok {
					return nil, fmt.Errorf("%w: tool call name must be text", ErrTruncated)
				}
			}
			if value, present := incoming["arguments"]; present && value != nil {
				if _, ok := value.(string); !ok {
					return nil, fmt.Errorf("%w: tool call arguments must be text", ErrTruncated)
				}
			}
		}
		for len(existing) <= index {
			existing = append(existing, map[string]any{"index": len(existing), "id": "", "type": "function", "function": map[string]any{"name": "", "arguments": ""}})
		}
		out, ok := existing[index].(map[string]any)
		if !ok || out == nil {
			return nil, fmt.Errorf("%w: malformed accumulated tool call", ErrTruncated)
		}
		if value, ok := delta["id"].(string); ok && value != "" {
			out["id"] = value
		}
		if value, ok := delta["type"].(string); ok && value != "" {
			out["type"] = value
		}
		fn, ok := out["function"].(map[string]any)
		if !ok || fn == nil {
			return nil, fmt.Errorf("%w: malformed accumulated tool function", ErrTruncated)
		}
		arguments, present := fn["arguments"]
		if !present || arguments == nil {
			arguments = ""
			fn["arguments"] = arguments
		}
		if arguments != nil {
			if _, ok := arguments.(string); !ok {
				return nil, fmt.Errorf("%w: accumulated tool arguments must be text", ErrTruncated)
			}
		}
		if !hasFunction {
			// A delta containing only id/type is valid.
			continue
		}
		if value, ok := incoming["name"].(string); ok && value != "" {
			fn["name"] = value
		}
		if value, ok := incoming["arguments"].(string); ok {
			fn["arguments"] = arguments.(string) + value
		}
		// A few OpenAI-compatible providers call the same streamed fragment
		// input. Normalize it into Chat Completions' function.arguments while
		// retaining cumulative fragments and the output size bound.
		if value, present := incoming["input"]; present {
			fragment, err := toolCallFragment(value)
			if err != nil {
				return nil, err
			}
			fn["arguments"] = fn["arguments"].(string) + fragment
		}
	}
	return existing, nil
}

func toolCallIndex(value any) (int, error) {
	if value == nil {
		return 0, nil
	}
	var index int64
	switch n := value.(type) {
	case float64:
		if math.IsNaN(n) || math.IsInf(n, 0) || n < 0 || math.Trunc(n) != n || n > float64(maxToolCalls-1) {
			return 0, fmt.Errorf("%w: invalid tool call index", ErrUnsupported)
		}
		index = int64(n)
	case float32:
		f := float64(n)
		if math.IsNaN(f) || math.IsInf(f, 0) || f < 0 || math.Trunc(f) != f || f > float64(maxToolCalls-1) {
			return 0, fmt.Errorf("%w: invalid tool call index", ErrUnsupported)
		}
		index = int64(n)
	case int:
		index = int64(n)
	case int8:
		index = int64(n)
	case int16:
		index = int64(n)
	case int32:
		index = int64(n)
	case int64:
		index = n
	case uint:
		if uint64(n) > uint64(maxToolCalls-1) {
			return 0, fmt.Errorf("%w: invalid tool call index", ErrUnsupported)
		}
		index = int64(n)
	case uint8:
		index = int64(n)
	case uint16:
		index = int64(n)
	case uint32:
		index = int64(n)
	case uint64:
		if n > uint64(maxToolCalls-1) {
			return 0, fmt.Errorf("%w: invalid tool call index", ErrUnsupported)
		}
		index = int64(n)
	case json.Number:
		parsed, err := n.Int64()
		if err != nil {
			return 0, fmt.Errorf("%w: invalid tool call index", ErrUnsupported)
		}
		index = parsed
	default:
		return 0, fmt.Errorf("%w: invalid tool call index", ErrUnsupported)
	}
	if index < 0 || index >= maxToolCalls {
		return 0, fmt.Errorf("%w: invalid tool call index", ErrUnsupported)
	}
	return int(index), nil
}

func toolCallText(value any) string {
	if text, ok := value.(string); ok {
		return text
	}
	return ""
}

func toolCallFragment(value any) (string, error) {
	if value == nil {
		return "", nil
	}
	if text, ok := value.(string); ok {
		return text, nil
	}
	data, err := json.Marshal(value)
	if err != nil {
		return "", fmt.Errorf("%w: malformed tool call input", ErrTruncated)
	}
	return string(data), nil
}

func streamOutputBytes(contentLen, reasoningLen int, toolCalls []any) int64 {
	total := int64(contentLen + reasoningLen)
	for _, raw := range toolCalls {
		call, ok := raw.(map[string]any)
		if !ok {
			continue
		}
		if function, ok := call["function"].(map[string]any); ok {
			if value, ok := function["name"].(string); ok {
				total += int64(len(value))
			}
			if value, ok := function["arguments"].(string); ok {
				total += int64(len(value))
			}
			if value, ok := function["input"].(string); ok {
				total += int64(len(value))
			}
		}
	}
	return total
}

func openAIStreamError(chunk map[string]any) error {
	if value, ok := chunk["error"]; ok && value != nil {
		return fmt.Errorf("coding next adapter: upstream error: %s", describeStreamError(value))
	}
	if choices, ok := chunk["choices"].([]any); ok {
		for _, raw := range choices {
			choice, ok := raw.(map[string]any)
			if !ok || choice == nil {
				continue
			}
			if value, ok := choice["error"]; ok && value != nil {
				return fmt.Errorf("coding next adapter: upstream error: %s", describeStreamError(value))
			}
		}
	}
	if typ, ok := chunk["type"].(string); ok {
		switch strings.ToLower(typ) {
		case "error", "response.error", "response.failed", "message.error":
			return fmt.Errorf("coding next adapter: upstream error: %s", describeStreamError(chunk))
		}
	}
	return nil
}

func describeStreamError(value any) string { return "request failed" }

func errorsResponseTooLarge() error {
	return fmt.Errorf("coding next adapter: output exceeds %d bytes", maxResponseBytes)
}
func nowUnix() int64 { return timeNow().Unix() }

// timeNow is a variable to keep generated response metadata deterministic in
// package tests without changing the wire contract.
var timeNow = func() time.Time { return time.Now() }
