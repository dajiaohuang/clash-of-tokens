package chinafinal

import (
	"bytes"
	"clash-of-tokens/internal/protocol"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

var errResponse = errors.New("Tencent AI Studio returned an invalid or incomplete completion")

func decodeCompletion(raw []byte, full bool) (map[string]any, error) {
	var value map[string]any
	if json.Unmarshal(raw, &value) != nil || value == nil || value["error"] != nil {
		return nil, errResponse
	}
	choices, ok := value["choices"].([]any)
	if !ok || len(choices) > 1 || (full && len(choices) != 1) {
		return nil, errResponse
	}
	for _, item := range choices {
		choice, ok := item.(map[string]any)
		if !ok || choice["error"] != nil {
			return nil, errResponse
		}
		if index, present := choice["index"]; present && index != float64(0) {
			return nil, errResponse
		}
		field := "delta"
		if full {
			field = "message"
			finish, _ := choice["finish_reason"].(string)
			if finish == "" {
				return nil, errResponse
			}
		}
		msg, ok := choice[field].(map[string]any)
		if !ok {
			return nil, errResponse
		}
		for k, v := range msg {
			switch k {
			case "role":
				if v != "assistant" {
					return nil, errResponse
				}
			case "content", "reasoning_content":
				if v != nil {
					if _, ok := v.(string); !ok {
						return nil, errResponse
					}
				}
			default:
				if v != nil {
					return nil, errResponse
				}
			}
		}
	}
	return value, nil
}

func readResponseBody(body io.ReadCloser) ([]byte, error) {
	defer body.Close()
	raw, err := io.ReadAll(io.LimitReader(body, maxResponseBytes+1))
	if err != nil || len(raw) > maxResponseBytes {
		return nil, errResponse
	}
	return raw, nil
}

func normalizeNonStreamingResponse(resp *http.Response, model string) error {
	var raw []byte
	var err error
	if strings.Contains(strings.ToLower(resp.Header.Get("Content-Type")), "text/event-stream") {
		var value map[string]any
		value, err = collectTencentSSE(resp.Body, model)
		resp.Body.Close()
		if err == nil {
			raw, err = json.Marshal(value)
		}
	} else {
		raw, err = readResponseBody(resp.Body)
		if err == nil {
			_, err = decodeCompletion(raw, true)
		}
	}
	if err != nil {
		return err
	}
	resp.Body = io.NopCloser(bytes.NewReader(raw))
	resp.ContentLength = int64(len(raw))
	resp.Header.Del("Content-Length")
	resp.Header.Set("Content-Type", "application/json")
	return nil
}

func jsonToTencentSSE(value map[string]any, model string) ([]byte, error) {
	if value["id"] == nil {
		value["id"] = fmt.Sprintf("chatcmpl-tencent-%d", time.Now().UnixNano())
	}
	if value["model"] == nil {
		value["model"] = model
	}
	if value["created"] == nil {
		value["created"] = time.Now().Unix()
	}
	choice := value["choices"].([]any)[0].(map[string]any)
	base := map[string]any{"id": value["id"], "model": value["model"], "created": value["created"], "object": "chat.completion.chunk"}
	var output bytes.Buffer
	base["choices"] = []any{map[string]any{"index": 0, "delta": choice["message"], "finish_reason": nil}}
	writeTencentFrame(&output, base)
	base["choices"] = []any{map[string]any{"index": 0, "delta": map[string]any{}, "finish_reason": choice["finish_reason"]}}
	if value["usage"] != nil {
		base["usage"] = value["usage"]
	}
	writeTencentFrame(&output, base)
	output.WriteString("data: [DONE]\n\n")
	if output.Len() > maxResponseBytes {
		return nil, errResponse
	}
	return output.Bytes(), nil
}

func convertJSONResponseToSSE(resp *http.Response, model string) error {
	raw, err := readResponseBody(resp.Body)
	if err != nil {
		return err
	}
	value, err := decodeCompletion(raw, true)
	if err != nil {
		return err
	}
	data, err := jsonToTencentSSE(value, model)
	if err != nil {
		return err
	}
	resp.Body = io.NopCloser(bytes.NewReader(data))
	resp.ContentLength = int64(len(data))
	resp.Header.Del("Content-Length")
	resp.Header.Set("Content-Type", "text/event-stream")
	resp.Header.Set("X-COT-Delivery", "buffered")
	return nil
}
func writeTencentFrame(w io.Writer, value any) {
	b, _ := json.Marshal(value)
	fmt.Fprintf(w, "data: %s\n\n", b)
}

// walkTencentSSE requires both a finish reason and an OpenAI terminal frame.
func walkTencentSSE(body io.Reader, handle func([]byte, map[string]any) error) error {
	reader := protocol.NewSSEReader(body, 1<<20)
	total := 0
	finished := false
	for {
		frame, err := reader.Next()
		if err != nil {
			return errResponse
		}
		total += len(frame)
		if total > maxResponseBytes {
			return errResponse
		}
		raw := bytes.TrimSpace(protocol.SSEData(frame))
		if len(raw) == 0 {
			continue
		}
		if bytes.Equal(raw, []byte("[DONE]")) {
			if !finished {
				return errResponse
			}
			return handle(frame, nil)
		}
		value, err := decodeCompletion(raw, false)
		if err != nil {
			return err
		}
		choices := value["choices"].([]any)
		if len(choices) > 0 {
			choice := choices[0].(map[string]any)
			if reason, ok := choice["finish_reason"].(string); ok && reason != "" {
				finished = true
			}
		}
		if err := handle(frame, value); err != nil {
			return err
		}
	}
}

func collectTencentSSE(body io.Reader, model string) (map[string]any, error) {
	var text, reasoning strings.Builder
	var id, created, usage, finish any
	err := walkTencentSSE(body, func(_ []byte, value map[string]any) error {
		if value == nil {
			return nil
		}
		if id == nil {
			id = value["id"]
		}
		if created == nil {
			created = value["created"]
		}
		if value["usage"] != nil {
			usage = value["usage"]
		}
		choices := value["choices"].([]any)
		if len(choices) == 0 {
			return nil
		}
		choice := choices[0].(map[string]any)
		delta := choice["delta"].(map[string]any)
		if content, ok := delta["content"].(string); ok {
			text.WriteString(content)
		}
		if content, ok := delta["reasoning_content"].(string); ok {
			reasoning.WriteString(content)
		}
		if choice["finish_reason"] != nil {
			finish = choice["finish_reason"]
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	if id == nil {
		id = fmt.Sprintf("chatcmpl-tencent-%d", time.Now().UnixNano())
	}
	if created == nil {
		created = time.Now().Unix()
	}
	msg := map[string]any{"role": "assistant", "content": text.String()}
	if reasoning.Len() > 0 {
		msg["reasoning_content"] = reasoning.String()
	}
	result := map[string]any{"id": id, "object": "chat.completion", "created": created, "model": model, "choices": []any{map[string]any{"index": 0, "message": msg, "finish_reason": finish}}}
	if usage != nil {
		result["usage"] = usage
	}
	return result, nil
}

type tencentStreamBody struct {
	*io.PipeReader
	source io.ReadCloser
}

func (b *tencentStreamBody) Close() error { b.source.Close(); return b.PipeReader.Close() }
func validatedTencentSSE(ctx context.Context, source io.ReadCloser) io.ReadCloser {
	reader, writer := io.Pipe()
	done := make(chan struct{})
	go func() {
		select {
		case <-ctx.Done():
			reader.CloseWithError(ctx.Err())
			source.Close()
		case <-done:
		}
	}()
	go func() {
		defer close(done)
		defer source.Close()
		err := walkTencentSSE(source, func(frame []byte, _ map[string]any) error { _, err := writer.Write(frame); return err })
		writer.CloseWithError(err)
	}()
	return &tencentStreamBody{reader, source}
}
