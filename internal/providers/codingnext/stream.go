package codingnext

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"

	"clash-of-tokens/internal/protocol"
)

type openAIAccumulator struct {
	id        string
	created   int64
	content   strings.Builder
	reasoning strings.Builder
	finish    any
	toolCalls []any
	roleSent  bool
}

func copyValidatedOpenAISSE(ctx context.Context, body io.Reader, sink io.Writer) error {
	reader := protocol.NewSSEReader(body, maxEventBytes)
	seenDone := false
	seenEvent := false
	var total int64
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		frame, err := reader.Next()
		if err == io.EOF {
			if seenDone {
				return nil
			}
			return ErrTruncated
		}
		if err != nil {
			return fmt.Errorf("%w: %v", ErrTruncated, err)
		}
		data := bytes.TrimSpace(protocol.SSEData(frame))
		if len(data) == 0 {
			if _, err := sink.Write(frame); err != nil {
				return err
			}
			continue
		}
		if bytes.Equal(data, []byte("[DONE]")) {
			if !seenEvent {
				return ErrTruncated
			}
			seenDone = true
		}
		total += int64(len(frame))
		if total > maxResponseBytes {
			return errorsResponseTooLarge()
		}
		if !seenDone {
			var value map[string]any
			if err := json.Unmarshal(data, &value); err != nil || value == nil {
				return fmt.Errorf("%w: malformed OpenAI SSE event", ErrTruncated)
			}
			if err := openAIStreamError(value); err != nil {
				return err
			}
			seenEvent = true
		}
		if _, err := sink.Write(frame); err != nil {
			return err
		}
		if seenDone {
			return nil
		}
	}
}

func zedNDJSONToJSON(ctx context.Context, body io.Reader, model string, limit int64) ([]byte, error) {
	var output bytes.Buffer
	if err := convertZedStream(ctx, body, &output, model); err != nil {
		return nil, err
	}
	return aggregateOpenAISSE(output.Bytes(), model, limit)
}

func aggregateOpenAISSE(data []byte, model string, limit int64) ([]byte, error) {
	return streamToOpenAIJSON(context.Background(), bytes.NewReader(data), model, limit)
}

func convertZedStream(ctx context.Context, body io.Reader, sink io.Writer, model string) error {
	reader := newLineReader(body, maxEventBytes)
	acc := &openAIAccumulator{}
	var totalInput int64
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		line, err := reader.next()
		if err == io.EOF {
			return ErrTruncated
		}
		if err != nil {
			return fmt.Errorf("%w: %v", ErrTruncated, err)
		}
		totalInput += int64(len(line) + 1)
		if totalInput > maxResponseBytes {
			return errorsResponseTooLarge()
		}
		line = strings.TrimSpace(strings.TrimSuffix(line, "\r"))
		if line == "" {
			continue
		}
		if strings.HasPrefix(line, "data:") {
			line = strings.TrimSpace(strings.TrimPrefix(line, "data:"))
		}
		if line == "[DONE]" {
			return writeZedTerminal(sink, model, acc)
		}
		var envelope map[string]any
		if err := json.Unmarshal([]byte(line), &envelope); err != nil || envelope == nil {
			return fmt.Errorf("%w: malformed Zed NDJSON event", ErrTruncated)
		}
		if status, ok := envelope["status"]; ok {
			terminal, failure, message := zedStatus(status)
			if failure {
				return fmt.Errorf("coding next adapter: Zed stream failed: %s", message)
			}
			if terminal {
				return writeZedTerminal(sink, model, acc)
			}
			continue
		}
		event, ok := envelope["event"]
		if !ok {
			// Some Zed deployments send the provider event directly.
			event = envelope
		}
		chunk, done, err := zedEventToChunk(event, model, acc)
		if err != nil {
			return err
		}
		if chunk != nil {
			if err := writeOpenAIDelta(sink, model, acc, chunk); err != nil {
				return err
			}
		}
		if streamOutputBytes(acc.content.Len(), acc.reasoning.Len(), acc.toolCalls) > maxResponseBytes {
			return errorsResponseTooLarge()
		}
		if done {
			return writeZedTerminal(sink, model, acc)
		}
	}
}

func writeZedTerminal(sink io.Writer, model string, acc *openAIAccumulator) error {
	if acc.content.Len() == 0 && acc.reasoning.Len() == 0 && len(acc.toolCalls) == 0 {
		return ErrTruncated
	}
	if err := writeOpenAIFinish(sink, model, acc); err != nil {
		return err
	}
	return writeOpenAIDone(sink)
}

func zedStatus(value any) (terminal, failure bool, message string) {
	if s, ok := value.(string); ok {
		switch s {
		case "stream_ended", "completed", "done", "finished":
			return true, false, ""
		case "failed", "error":
			return false, true, "request failed"
		default:
			return false, false, ""
		}
	}
	status, ok := value.(map[string]any)
	if !ok || status == nil {
		return false, false, ""
	}
	if failed, ok := status["failed"]; ok {
		if isFalseStatusValue(failed) {
			return false, false, ""
		}
		return false, true, zedFailureMessage(failed)
	}
	for _, key := range []string{"type", "status", "state", "kind", "code"} {
		if typ, ok := status[key].(string); ok {
			return zedStatus(typ)
		}
	}
	for key, nested := range status {
		switch key {
		case "stream_ended", "completed", "done", "finished":
			return true, false, ""
		case "failed", "error":
			if isFalseStatusValue(nested) {
				continue
			}
			return false, true, zedFailureMessage(nested)
		}
	}
	return false, false, ""
}

func isFalseStatusValue(value any) bool {
	failed, ok := value.(bool)
	return ok && !failed
}

func zedFailureMessage(value any) string { return "request failed" }

func zedEventToChunk(event any, model string, acc *openAIAccumulator) (map[string]any, bool, error) {
	value, ok := event.(map[string]any)
	if !ok || value == nil {
		return nil, false, nil
	}
	if err := openAIStreamError(value); err != nil {
		return nil, false, err
	}
	if typ, _ := value["type"].(string); typ != "" {
		switch typ {
		case "response.output_text.delta", "response.reasoning_summary_text.delta", "response.reasoning_text.delta":
			delta, _ := value["delta"].(string)
			if delta == "" {
				delta, _ = value["text"].(string)
			}
			if typ == "response.output_text.delta" {
				acc.content.WriteString(delta)
				return map[string]any{"content": delta}, false, nil
			}
			acc.reasoning.WriteString(delta)
			return map[string]any{"reasoning_content": delta}, false, nil
		case "response.completed", "response.done", "message_stop":
			return nil, true, nil
		case "response.failed", "error":
			return nil, false, fmt.Errorf("coding next adapter: Zed stream failed: %s", zedFailureMessage(value))
		case "content_block_delta":
			delta, _ := value["delta"].(map[string]any)
			if text, ok := delta["text"].(string); ok {
				acc.content.WriteString(text)
				return map[string]any{"content": text}, false, nil
			}
			if thinking, ok := delta["thinking"].(string); ok {
				acc.reasoning.WriteString(thinking)
				return map[string]any{"reasoning_content": thinking}, false, nil
			}
			return nil, false, nil
		}
	}
	if nested, ok := value["response"].(map[string]any); ok {
		return zedEventToChunk(nested, model, acc)
	}
	if candidates, ok := value["candidates"].([]any); ok {
		if len(candidates) != 1 {
			return nil, false, fmt.Errorf("%w: Zed returned multiple Gemini candidates", ErrUnsupported)
		}
		candidate, ok := candidates[0].(map[string]any)
		if !ok {
			return nil, false, fmt.Errorf("%w: malformed Gemini candidate", ErrTruncated)
		}
		content, _ := candidate["content"].(map[string]any)
		parts, _ := content["parts"].([]any)
		var delta strings.Builder
		for _, rawPart := range parts {
			part, ok := rawPart.(map[string]any)
			if !ok {
				return nil, false, fmt.Errorf("%w: malformed Gemini content part", ErrTruncated)
			}
			if text, ok := part["text"].(string); ok {
				delta.WriteString(text)
			}
		}
		if finish, ok := candidate["finishReason"].(string); ok && finish != "" {
			acc.finish = geminiFinishReason(finish)
		}
		text := delta.String()
		if text == "" {
			return nil, false, nil
		}
		acc.content.WriteString(text)
		return map[string]any{"content": text}, false, nil
	}
	if choices, ok := value["choices"].([]any); ok {
		if len(choices) != 1 {
			return nil, false, fmt.Errorf("%w: Zed returned multiple choices", ErrUnsupported)
		}
		choice, ok := choices[0].(map[string]any)
		if !ok {
			return nil, false, fmt.Errorf("%w: malformed Zed choice", ErrTruncated)
		}
		if err := openAIStreamError(map[string]any{"choices": []any{choice}}); err != nil {
			return nil, false, err
		}
		if id, ok := value["id"].(string); ok {
			acc.id = id
		}
		if created, ok := value["created"].(float64); ok {
			acc.created = int64(created)
		}
		if finish := choice["finish_reason"]; finish != nil {
			acc.finish = finish
		}
		delta, _ := choice["delta"].(map[string]any)
		if rawDelta, present := choice["delta"]; present && rawDelta != nil && delta == nil {
			return nil, false, fmt.Errorf("%w: malformed Zed OpenAI delta", ErrTruncated)
		}
		if text, ok := delta["content"].(string); ok {
			acc.content.WriteString(text)
		}
		if reasoning, ok := delta["reasoning_content"].(string); ok {
			acc.reasoning.WriteString(reasoning)
		}
		if rawCalls, present := delta["tool_calls"]; present && rawCalls != nil {
			calls, ok := rawCalls.([]any)
			if !ok {
				return nil, false, fmt.Errorf("%w: malformed Zed tool call deltas", ErrTruncated)
			}
			var err error
			acc.toolCalls, err = appendToolCallDeltasChecked(acc.toolCalls, calls)
			if err != nil {
				return nil, false, err
			}
		}
		if rawCall, present := delta["function_call"]; present && rawCall != nil {
			call, ok := rawCall.(map[string]any)
			if !ok {
				return nil, false, fmt.Errorf("%w: malformed Zed function call delta", ErrTruncated)
			}
			var err error
			acc.toolCalls, err = appendToolCallDeltasChecked(acc.toolCalls, []any{map[string]any{"index": 0, "function": call}})
			if err != nil {
				return nil, false, err
			}
		}
		return delta, acc.finish != nil, nil
	}
	// Anthropic message_start/content_block_start and Gemini metadata events
	// have no text. Ignore only known structural events; unknown objects are
	// rejected to avoid silently losing a new provider event type.
	if typ, _ := value["type"].(string); typ != "" {
		switch typ {
		case "message_start", "content_block_start", "content_block_stop", "message_delta", "response.created", "response.in_progress", "response.output_item.added", "response.output_item.done", "response.output_text.done", "response.reasoning_summary_text.done":
			return nil, false, nil
		default:
			return nil, false, fmt.Errorf("%w: unsupported Zed provider event %q", ErrUnsupported, typ)
		}
	}
	return nil, false, fmt.Errorf("%w: Zed provider event has no supported shape", ErrUnsupported)
}

func geminiFinishReason(value string) string {
	switch strings.ToUpper(value) {
	case "STOP", "END_TURN":
		return "stop"
	case "MAX_TOKENS":
		return "length"
	case "SAFETY", "RECITATION", "BLOCKLIST", "PROHIBITED_CONTENT":
		return "content_filter"
	default:
		return "stop"
	}
}

func writeOpenAIFinish(sink io.Writer, model string, acc *openAIAccumulator) error {
	finish := acc.finish
	if finish == nil {
		finish = "stop"
	}
	return writeSSE(sink, map[string]any{
		"id": responseID(acc), "object": "chat.completion.chunk", "created": responseCreated(acc), "model": model,
		"choices": []any{map[string]any{"index": 0, "delta": map[string]any{}, "finish_reason": finish}},
	})
}

func writeOpenAIDelta(sink io.Writer, model string, acc *openAIAccumulator, delta map[string]any) error {
	out := make(map[string]any, len(delta)+1)
	for key, value := range delta {
		out[key] = value
	}
	if !acc.roleSent {
		out["role"] = "assistant"
		acc.roleSent = true
	}
	return writeSSE(sink, map[string]any{
		"id": responseID(acc), "object": "chat.completion.chunk", "created": responseCreated(acc), "model": model,
		"choices": []any{map[string]any{"index": 0, "delta": out, "finish_reason": nil}},
	})
}

func writeOpenAIDone(sink io.Writer) error {
	_, err := io.WriteString(sink, "data: [DONE]\n\n")
	return err
}

func writeSSE(sink io.Writer, value any) error {
	data, err := json.Marshal(value)
	if err != nil {
		return err
	}
	_, err = fmt.Fprintf(sink, "data: %s\n\n", data)
	if err == nil {
		err = nil
	}
	return err
}

func responseID(acc *openAIAccumulator) string {
	if acc.id != "" {
		return acc.id
	}
	acc.id = "chatcmpl-" + uuid()
	return acc.id
}
func responseCreated(acc *openAIAccumulator) int64 {
	if acc.created != 0 {
		return acc.created
	}
	acc.created = nowUnix()
	return acc.created
}
func truncate(value string, limit int) string {
	if len(value) > limit {
		return value[:limit]
	}
	return value
}

type boundedLineReader struct {
	reader *bufio.Reader
	limit  int
}

func newLineReader(source io.Reader, limit int) *boundedLineReader {
	return &boundedLineReader{reader: bufio.NewReaderSize(source, 8192), limit: limit}
}
func (r *boundedLineReader) next() (string, error) {
	var line []byte
	for {
		part, err := r.reader.ReadSlice('\n')
		if len(line)+len(part) > r.limit {
			return "", errors.New("Zed NDJSON line exceeds byte limit")
		}
		line = append(line, part...)
		if err == bufio.ErrBufferFull {
			continue
		}
		if err != nil {
			if err == io.EOF && len(line) == 0 {
				return "", io.EOF
			}
			return "", errors.New("truncated Zed NDJSON line")
		}
		return string(line), nil
	}
}
