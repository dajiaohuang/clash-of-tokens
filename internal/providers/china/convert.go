package china

import (
	"bytes"
	"context"
	"encoding/binary"
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
	adapter      string
	model        string
	session      *session
	id           string
	created      int64
	content      string
	reasoning    string
	finish       string
	usage        any
	done         bool
	roleSent     bool
	sink         io.Writer
	stream       bool
	glmParts     map[string]map[string]any
	glOrder      []string
	glmTextSent  string
	glmThinkSent string
}

func newCompletionState(adapter, model string, s *session, sink io.Writer, stream bool) *completionState {
	return &completionState{adapter: adapter, model: model, session: s, id: "china-" + uuid(), created: time.Now().Unix(), finish: "stop", sink: sink, stream: stream, glmParts: map[string]map[string]any{}}
}

func (c *Client) convert(ctx context.Context, upstreamResp *http.Response, model, adapter string, stream bool, s *session) (*http.Response, error) {
	if upstreamResp.Body == nil {
		return nil, errors.New("china web adapter: upstream response has no body")
	}
	if !stream {
		var out bytes.Buffer
		st := newCompletionState(adapter, model, s, &out, false)
		e := parseProviderStream(ctx, upstreamResp.Body, st)
		upstreamResp.Body.Close()
		if e != nil {
			return nil, e
		}
		result, e := st.nonStreamJSON()
		if e != nil {
			return nil, e
		}
		upstreamResp.StatusCode = 200
		upstreamResp.Status = "200 OK"
		upstreamResp.Header = cloneHeaders(upstreamResp.Header)
		upstreamResp.Header.Set("Content-Type", "application/json")
		upstreamResp.Header.Set("Content-Length", fmt.Sprintf("%d", len(result)))
		upstreamResp.Body = io.NopCloser(bytes.NewReader(result))
		return upstreamResp, nil
	}
	reader, writer := io.Pipe()
	st := newCompletionState(adapter, model, s, writer, true)
	upstreamBody := upstreamResp.Body
	go func() {
		defer upstreamBody.Close()
		e := parseProviderStream(ctx, upstreamBody, st)
		if e != nil {
			_ = writer.CloseWithError(e)
		} else {
			_ = writer.Close()
		}
	}()
	upstreamResp.Header = cloneHeaders(upstreamResp.Header)
	upstreamResp.Header.Set("Content-Type", "text/event-stream")
	upstreamResp.Header.Set("Cache-Control", "no-cache")
	upstreamResp.Header.Set("X-Accel-Buffering", "no")
	upstreamResp.Body = reader
	return upstreamResp, nil
}

func parseProviderStream(ctx context.Context, body io.Reader, st *completionState) error {
	if e := ctx.Err(); e != nil {
		return e
	}
	switch st.adapter {
	case adapterKimi:
		return parseKimi(ctx, body, st)
	case adapterQwen, adapterGLM, adapterZAI:
		return parseSSE(ctx, body, st)
	default:
		return fmt.Errorf("china web adapter: unsupported adapter %q", st.adapter)
	}
}

func parseSSE(ctx context.Context, body io.Reader, st *completionState) error {
	r := protocol.NewSSEReader(body, maxEventBytes)
	for {
		if e := ctx.Err(); e != nil {
			return e
		}
		frame, e := r.Next()
		if e == io.EOF {
			if st.done {
				return nil
			}
			return ErrTruncated
		}
		if e != nil {
			if ctxErr := ctx.Err(); ctxErr != nil {
				return ctxErr
			}
			return ErrTruncated
		}
		data := protocol.SSEData(frame)
		if len(data) == 0 {
			continue
		}
		if bytes.Equal(bytes.TrimSpace(data), []byte("[DONE]")) {
			if !st.done {
				if e := st.finishNow(); e != nil {
					return e
				}
			}
			return nil
		}
		var value map[string]any
		if e := json.Unmarshal(data, &value); e != nil {
			return fmt.Errorf("china web adapter: malformed upstream SSE: %w", e)
		}
		var consumeErr error
		switch st.adapter {
		case adapterQwen:
			consumeErr = st.consumeQwen(value)
		case adapterGLM:
			consumeErr = st.consumeGLM(value)
		case adapterZAI:
			consumeErr = st.consumeZAI(value)
		}
		if consumeErr != nil {
			return consumeErr
		}
		if st.done {
			return nil
		}
	}
}

func parseKimi(ctx context.Context, body io.Reader, st *completionState) error {
	pending := make([]byte, 0, 32<<10)
	readBuffer := make([]byte, 32<<10)
	for {
		if e := ctx.Err(); e != nil {
			return e
		}
		n, e := body.Read(readBuffer)
		if n > 0 {
			pending = append(pending, readBuffer[:n]...)
			if len(pending) > maxEventBytes+5 {
				return errors.New("china web adapter: Kimi buffered frame exceeds byte limit")
			}
		}
		for len(pending) >= 5 {
			length := int(binary.BigEndian.Uint32(pending[1:5]))
			if length < 0 || length > maxEventBytes {
				return errors.New("china web adapter: Kimi frame exceeds byte limit")
			}
			if len(pending) < 5+length {
				break
			}
			payload := pending[5 : 5+length]
			var value map[string]any
			if len(bytes.TrimSpace(payload)) > 0 {
				if je := json.Unmarshal(payload, &value); je != nil {
					return fmt.Errorf("china web adapter: malformed Kimi frame: %w", je)
				}
				if ce := st.consumeKimi(value); ce != nil {
					return ce
				}
			}
			pending = pending[5+length:]
			if st.done {
				return nil
			}
		}
		if e != nil {
			if e == io.EOF {
				if st.done && len(pending) == 0 {
					return nil
				}
				return ErrTruncated
			}
			if ctxErr := ctx.Err(); ctxErr != nil {
				return ctxErr
			}
			return e
		}
	}
}

func (st *completionState) consumeQwen(v map[string]any) error {
	if created, ok := v["response.created"].(map[string]any); ok {
		if id := stringValue(created["response_id"]); id != "" {
			st.id = id
		}
	}
	choices, _ := v["choices"].([]any)
	if len(choices) == 0 {
		return nil
	}
	choice, _ := choices[0].(map[string]any)
	delta, _ := choice["delta"].(map[string]any)
	phase := stringValue(delta["phase"])
	status := stringValue(delta["status"])
	if content := stringValue(delta["content"]); content != "" {
		switch phase {
		case "think":
			if status != "finished" {
				if e := st.addReasoning(content); e != nil {
					return e
				}
			}
		case "answer", "":
			if e := st.addContent(content); e != nil {
				return e
			}
		}
	}
	if phase == "thinking_summary" {
		if extra, ok := delta["extra"].(map[string]any); ok {
			if values, ok := extra["summary_thought"].(map[string]any); ok {
				if arr, ok := values["content"].([]any); ok {
					var b strings.Builder
					for _, item := range arr {
						b.WriteString(stringValue(item))
						if b.Len() > 0 {
							b.WriteByte('\n')
						}
					}
					if b.Len() > 0 {
						text := strings.TrimSuffix(b.String(), "\n")
						if len(text) > len(st.reasoning) {
							if e := st.addReasoning(text[len(st.reasoning):]); e != nil {
								return e
							}
						}
					}
				}
			}
		}
	}
	if status == "finished" && (phase == "answer" || phase == "") {
		return st.finishNow()
	}
	return nil
}

func (st *completionState) consumeGLM(v map[string]any) error {
	if id := stringValue(v["conversation_id"]); id != "" {
		st.id = id
		st.session.mu.Lock()
		st.session.glmConversation = id
		st.session.mu.Unlock()
	}
	status := stringValue(v["status"])
	if parts, ok := v["parts"].([]any); ok {
		for i, raw := range parts {
			part, ok := raw.(map[string]any)
			if !ok {
				continue
			}
			key := stringValue(part["logic_id"])
			if key == "" {
				key = fmt.Sprintf("part-%d", i)
			}
			if _, exists := st.glmParts[key]; !exists {
				st.glOrder = append(st.glOrder, key)
			}
			st.glmParts[key] = part
		}
	}
	var text, think strings.Builder
	for _, key := range st.glOrder {
		part := st.glmParts[key]
		items, _ := part["content"].([]any)
		for _, raw := range items {
			item, _ := raw.(map[string]any)
			switch stringValue(item["type"]) {
			case "text":
				text.WriteString(stringValue(item["text"]))
			case "think":
				think.WriteString(stringValue(item["think"]))
			case "code":
				text.WriteString("```\n" + stringValue(item["code"]) + "\n```")
			case "execution_output":
				text.WriteString(stringValue(item["content"]))
			}
		}
	}
	fullText, fullThink := text.String(), think.String()
	if strings.HasPrefix(fullThink, st.glmThinkSent) {
		if e := st.addReasoning(fullThink[len(st.glmThinkSent):]); e != nil {
			return e
		}
	} else if fullThink != "" {
		if e := st.addReasoning(fullThink); e != nil {
			return e
		}
	}
	st.glmThinkSent = fullThink
	if strings.HasPrefix(fullText, st.glmTextSent) {
		if e := st.addContent(fullText[len(st.glmTextSent):]); e != nil {
			return e
		}
	} else if fullText != "" {
		if e := st.addContent(fullText); e != nil {
			return e
		}
	}
	st.glmTextSent = fullText
	if status == "intervene" {
		if last, ok := v["last_error"].(map[string]any); ok {
			if msg := stringValue(last["intervene_text"]); msg != "" {
				if e := st.addContent("\n\n" + msg); e != nil {
					return e
				}
			}
		}
		return st.finishNow()
	}
	if status == "finish" {
		return st.finishNow()
	}
	return nil
}

func (st *completionState) consumeZAI(v map[string]any) error {
	if stringValue(v["type"]) != "chat:completion" {
		return nil
	}
	result, _ := v["data"].(map[string]any)
	if result == nil {
		return nil
	}
	if id := stringValue(result["id"]); id != "" && stringValue(result["role"]) == "assistant" {
		st.id = id
		st.session.mu.Lock()
		st.session.zaiParentID = id
		st.session.mu.Unlock()
	}
	if errValue := result["error"]; errValue != nil {
		return fmt.Errorf("china web adapter: Z.ai stream error: %s", stringValue(errValue))
	}
	switch stringValue(result["phase"]) {
	case "thinking":
		if e := st.addReasoning(stringValue(result["delta_content"])); e != nil {
			return e
		}
	case "answer":
		if e := st.addContent(stringValue(result["delta_content"])); e != nil {
			return e
		}
	case "done":
		if usage, ok := result["usage"]; ok {
			st.usage = usage
		}
		if result["done"] == true {
			return st.finishNow()
		}
	}
	return nil
}

func (st *completionState) consumeKimi(v map[string]any) error {
	if errValue := v["error"]; errValue != nil {
		return fmt.Errorf("china web adapter: Kimi stream error: %s", stringValue(errValue))
	}
	if chat, ok := v["chat"].(map[string]any); ok {
		if id := stringValue(chat["id"]); id != "" {
			st.id = id
			st.session.mu.Lock()
			st.session.kimiChatID = id
			st.session.mu.Unlock()
		}
	}
	if message, ok := v["message"].(map[string]any); ok {
		if stringValue(message["role"]) == "assistant" {
			if id := stringValue(message["id"]); id != "" {
				st.session.mu.Lock()
				st.session.kimiParentID = id
				st.session.mu.Unlock()
			}
		}
	}
	block, _ := v["block"].(map[string]any)
	if textBlock, ok := block["text"].(map[string]any); ok {
		value := stringValue(textBlock["content"])
		flags := stringValue(textBlock["flags"])
		if strings.Contains(flags, "think") || strings.Contains(stringValue(v["mask"]), "block.think") {
			if e := st.addReasoning(value); e != nil {
				return e
			}
		} else if value != "" {
			if e := st.addContent(value); e != nil {
				return e
			}
		}
	}
	if thinkBlock, ok := block["think"].(map[string]any); ok {
		if e := st.addReasoning(stringValue(thinkBlock["content"])); e != nil {
			return e
		}
	}
	if done, ok := v["done"]; ok && done != nil && done != false {
		return st.finishNow()
	}
	return nil
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
	chunk := map[string]any{"id": st.id, "object": "chat.completion.chunk", "created": st.created, "model": st.model, "choices": []any{map[string]any{"index": 0, "delta": delta, "finish_reason": nil}}}
	b, e := json.Marshal(chunk)
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
	if st.stream {
		chunk := map[string]any{"id": st.id, "object": "chat.completion.chunk", "created": st.created, "model": st.model, "choices": []any{map[string]any{"index": 0, "delta": map[string]any{}, "finish_reason": st.finish}}}
		b, e := json.Marshal(chunk)
		if e != nil {
			return e
		}
		if _, e = io.WriteString(st.sink, "data: "+string(b)+"\n\n"); e != nil {
			return e
		}
		_, e = io.WriteString(st.sink, "data: [DONE]\n\n")
		return e
	}
	return nil
}
func (st *completionState) nonStreamJSON() ([]byte, error) {
	if !st.done {
		return nil, ErrTruncated
	}
	message := map[string]any{"role": "assistant", "content": st.content}
	if st.reasoning != "" {
		message["reasoning_content"] = st.reasoning
	}
	result := map[string]any{"id": st.id, "object": "chat.completion", "created": st.created, "model": st.model, "choices": []any{map[string]any{"index": 0, "message": message, "finish_reason": st.finish}}}
	if st.usage != nil {
		result["usage"] = st.usage
	}
	return json.Marshal(result)
}

func stringValue(v any) string {
	s, _ := v.(string)
	return s
}
