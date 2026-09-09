package chinanext

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"clash-of-tokens/internal/protocol"
)

type completionState struct {
	adapter   string
	model     string
	session   *session
	id        string
	created   int64
	content   string
	reasoning string
	finish    string
	done      bool
	roleSent  bool
	sink      io.Writer
	stream    bool
	deepPath  string
}

func newCompletionState(adapter, model string, s *session, sink io.Writer, stream bool) *completionState {
	return &completionState{adapter: adapter, model: model, session: s, id: "china-next-" + uuid(), created: time.Now().Unix(), finish: "stop", sink: sink, stream: stream}
}

func (c *Client) convert(ctx context.Context, upstream *http.Response, model, adapter string, stream bool, s *session) (*http.Response, error) {
	if upstream.Body == nil {
		return nil, errors.New("china next web adapter: upstream response has no body")
	}
	if !stream {
		var out bytes.Buffer
		st := newCompletionState(adapter, model, s, &out, false)
		e := parseSSE(ctx, upstream.Body, st)
		upstream.Body.Close()
		if e != nil {
			return nil, e
		}
		result, e := st.nonStreamJSON()
		if e != nil {
			return nil, e
		}
		upstream.StatusCode, upstream.Status = 200, "200 OK"
		upstream.Header = cloneHeaders(upstream.Header)
		upstream.Header.Set("Content-Type", "application/json")
		upstream.Header.Set("Content-Length", fmt.Sprintf("%d", len(result)))
		upstream.Body = io.NopCloser(bytes.NewReader(result))
		return upstream, nil
	}
	reader, writer := io.Pipe()
	st := newCompletionState(adapter, model, s, writer, true)
	go func() {
		defer upstream.Body.Close()
		e := parseSSE(ctx, upstream.Body, st)
		if e != nil {
			_ = writer.CloseWithError(e)
		} else {
			_ = writer.Close()
		}
	}()
	upstream.Header = cloneHeaders(upstream.Header)
	upstream.Header.Set("Content-Type", "text/event-stream")
	upstream.Header.Set("Cache-Control", "no-cache")
	upstream.Header.Set("X-Accel-Buffering", "no")
	upstream.Body = reader
	return upstream, nil
}

func parseSSE(ctx context.Context, body io.Reader, st *completionState) error {
	reader := protocol.NewSSEReader(body, maxEventBytes)
	for {
		if e := ctx.Err(); e != nil {
			return e
		}
		frame, e := reader.Next()
		if e == io.EOF {
			if st.done {
				return nil
			}
			// The pinned Doubao browser contract closes the response after its
			// final event and does not promise a [DONE] marker. A non-empty
			// answer at clean EOF is a valid completion; empty EOF remains
			// truncation and is rejected by the caller.
			if st.adapter == AdapterDoubao && st.content != "" {
				return st.finishNow()
			}
			return ErrTruncated
		}
		if e != nil {
			if ctxErr := ctx.Err(); ctxErr != nil {
				return ctxErr
			}
			return fmt.Errorf("%w: %v", ErrTruncated, e)
		}
		data := bytes.TrimSpace(protocol.SSEData(frame))
		if len(data) == 0 {
			continue
		}
		if bytes.Equal(data, []byte("[DONE]")) {
			return st.finishNow()
		}
		var value map[string]any
		if e := json.Unmarshal(data, &value); e != nil {
			return fmt.Errorf("china next web adapter: malformed upstream SSE: %w", e)
		}
		event := sseEvent(frame)
		switch st.adapter {
		case AdapterDola, "doubao-web", AdapterDoubao:
			e = st.consumeDoubao(value, event)
		case AdapterYuanbao, "yuanbao-web":
			e = st.consumeYuanbao(value)
		case AdapterDeepSeek:
			e = st.consumeDeepSeek(value)
		default:
			e = fmt.Errorf("china next web adapter: unsupported adapter %q", st.adapter)
		}
		if e != nil {
			return e
		}
		if st.done {
			return nil
		}
	}
}

func sseEvent(frame []byte) string {
	for len(frame) > 0 {
		line, rest, _ := bytes.Cut(frame, []byte{'\n'})
		frame = rest
		line = bytes.TrimSuffix(line, []byte{'\r'})
		if bytes.HasPrefix(line, []byte("event:")) {
			return strings.TrimSpace(string(line[6:]))
		}
	}
	return ""
}

func (st *completionState) consumeDoubao(v map[string]any, event string) error {
	if event == "STREAM_ERROR" {
		return errors.New("china next web adapter: Doubao upstream reported an error")
	}
	if event == "SSE_REPLY_END" {
		return st.finishNow()
	}
	// The pinned China Doubao source wraps message records in an
	// event_data JSON string. Decode that source envelope before looking for
	// content blocks; the site may also send the same object directly.
	if raw, ok := v["event_data"].(string); ok && strings.TrimSpace(raw) != "" {
		var eventData map[string]any
		if json.Unmarshal([]byte(raw), &eventData) != nil {
			return errors.New("china next web adapter: malformed Doubao event")
		}
		if numberValue(v["event_type"]) == 2001 {
			if text := doubaoEventText(eventData); text != "" {
				return st.addContent(text)
			}
		}
		return nil
	}
	if numberValue(v["event_type"]) == 2001 {
		if text := doubaoEventText(v); text != "" {
			return st.addContent(text)
		}
	}
	for _, block := range doubaoBlocks(v) {
		if numberValue(block["block_type"]) == 10040 && block["is_finish"] == true {
			// Dola reasoning blocks precede the answer boundary. Drop the
			// private pre-answer phase rather than exposing it as user content.
			continue
		}
		text := nestedText(block)
		if text != "" {
			if e := st.addContent(text); e != nil {
				return e
			}
		}
	}
	return nil
}

func doubaoEventText(v map[string]any) string {
	message, _ := v["message"].(map[string]any)
	content, _ := message["content"].(string)
	if content == "" {
		content, _ = v["content"].(string)
	}
	if content == "" {
		return ""
	}
	var value struct {
		Text string `json:"text"`
	}
	if json.Unmarshal([]byte(content), &value) == nil {
		return value.Text
	}
	return content
}

func doubaoBlocks(v map[string]any) []map[string]any {
	var out []map[string]any
	appendBlocks := func(value any) {
		if blocks, ok := value.([]any); ok {
			for _, raw := range blocks {
				if block, ok := raw.(map[string]any); ok {
					out = append(out, block)
				}
			}
		}
	}
	root := v
	if data, ok := v["data"].(map[string]any); ok {
		root = data
	}
	if content, ok := root["content"].(map[string]any); ok {
		appendBlocks(content["content_block"])
	}
	if content, ok := root["content"].(map[string]any); ok {
		appendBlocks(content["contentBlock"])
	}
	if patch, ok := root["patch_op"].([]any); ok {
		for _, raw := range patch {
			op, _ := raw.(map[string]any)
			value, _ := op["patch_value"].(map[string]any)
			appendBlocks(value["content_block"])
		}
	}
	if len(out) == 0 {
		appendBlocks(root["content_block"])
	}
	return out
}

func nestedText(block map[string]any) string {
	content, _ := block["content"].(map[string]any)
	textBlock, _ := content["text_block"].(map[string]any)
	text, _ := textBlock["text"].(string)
	if text == "" {
		text, _ = block["text"].(string)
	}
	return text
}

func (st *completionState) consumeYuanbao(v map[string]any) error {
	typ, _ := v["type"].(string)
	switch typ {
	case "think":
		if text, _ := v["content"].(string); text != "" {
			return st.addReasoning(text)
		}
	case "text":
		text, _ := v["msg"].(string)
		if text == "" {
			text, _ = v["content"].(string)
		}
		return st.addContent(text)
	}
	if stop, _ := v["stopReason"].(string); stop != "" {
		return st.finishNow()
	}
	if done, _ := v["done"].(bool); done {
		return st.finishNow()
	}
	return nil
}

func (st *completionState) consumeDeepSeek(v map[string]any) error {
	if response, ok := v["response"].(map[string]any); ok {
		if enabled, ok := response["thinking_enabled"].(bool); ok {
			if enabled {
				st.deepPath = "thinking"
			} else {
				st.deepPath = "content"
			}
		}
		if fragments, ok := response["fragments"].([]any); ok {
			for _, raw := range fragments {
				if e := st.consumeDeepFragment(raw); e != nil {
					return e
				}
			}
		}
	}
	p, _ := v["p"].(string)
	if p == "response/fragments" {
		if fragments, ok := v["v"].([]any); ok {
			for _, raw := range fragments {
				if e := st.consumeDeepFragment(raw); e != nil {
					return e
				}
			}
		}
		if fragment, ok := v["v"].(map[string]any); ok {
			if e := st.consumeDeepFragment(fragment); e != nil {
				return e
			}
		}
	}
	if p == "response/status" {
		if status, _ := v["v"].(string); status == "FINISHED" {
			return st.finishNow()
		}
		return nil
	}
	if text, ok := v["v"].(string); ok && p != "response/status" {
		if st.deepPath == "thinking" {
			return st.addReasoning(text)
		}
		return st.addContent(text)
	}
	return nil
}

func (st *completionState) consumeDeepFragment(raw any) error {
	fragment, ok := raw.(map[string]any)
	if !ok {
		return nil
	}
	typ, _ := fragment["type"].(string)
	text, _ := fragment["content"].(string)
	switch strings.ToUpper(typ) {
	case "THINK":
		st.deepPath = "thinking"
	case "ANSWER", "RESPONSE":
		st.deepPath = "content"
	}
	if text == "" {
		return nil
	}
	if st.deepPath == "thinking" {
		return st.addReasoning(text)
	}
	return st.addContent(text)
}

func numberValue(v any) int {
	if f, ok := v.(float64); ok {
		return int(f)
	}
	if i, ok := v.(int); ok {
		return i
	}
	return 0
}

func (st *completionState) addContent(text string) error {
	if text == "" || st.done {
		return nil
	}
	st.content += text
	if !st.stream {
		return nil
	}
	return st.emit(map[string]any{"content": text})
}
func (st *completionState) addReasoning(text string) error {
	if text == "" || st.done {
		return nil
	}
	st.reasoning += text
	if !st.stream {
		return nil
	}
	return st.emit(map[string]any{"reasoning_content": text})
}
func (st *completionState) emit(delta map[string]any) error {
	if !st.roleSent {
		delta["role"] = "assistant"
		st.roleSent = true
	}
	value := map[string]any{"id": st.id, "object": "chat.completion.chunk", "created": st.created, "model": st.model, "choices": []any{map[string]any{"index": 0, "delta": delta, "finish_reason": nil}}}
	b, e := json.Marshal(value)
	if e != nil {
		return e
	}
	_, e = io.WriteString(st.sink, "data: "+string(b)+"\n\n")
	return e
}
func (st *completionState) finishNow() error {
	if st.done {
		return nil
	}
	st.done = true
	if !st.stream {
		return nil
	}
	if !st.roleSent {
		if e := st.emit(map[string]any{}); e != nil {
			return e
		}
	}
	b, e := json.Marshal(map[string]any{"id": st.id, "object": "chat.completion.chunk", "created": st.created, "model": st.model, "choices": []any{map[string]any{"index": 0, "delta": map[string]any{}, "finish_reason": st.finish}}})
	if e != nil {
		return e
	}
	if _, e = io.WriteString(st.sink, "data: "+string(b)+"\n\n"); e != nil {
		return e
	}
	_, e = io.WriteString(st.sink, "data: [DONE]\n\n")
	return e
}
func (st *completionState) nonStreamJSON() ([]byte, error) {
	if !st.done {
		return nil, ErrTruncated
	}
	message := map[string]any{"role": "assistant", "content": st.content}
	if st.reasoning != "" {
		message["reasoning_content"] = st.reasoning
	}
	return json.Marshal(map[string]any{"id": st.id, "object": "chat.completion", "created": st.created, "model": st.model, "choices": []any{map[string]any{"index": 0, "message": message, "finish_reason": st.finish}}})
}
