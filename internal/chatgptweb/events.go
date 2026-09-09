package chatgptweb

import (
	"encoding/json"
	"fmt"
	"io"
	"time"
)

func completed(protocol, id, model, text string) any {
	if protocol == "chat" {
		return map[string]any{"id": id, "object": "chat.completion", "created": time.Now().Unix(), "model": model, "choices": []any{map[string]any{"index": 0, "message": message{"assistant", text}, "finish_reason": "stop"}}}
	}
	return map[string]any{"id": id, "object": "response", "status": "completed", "created_at": time.Now().Unix(), "model": model, "output": []any{responseMessage(id, text, "completed")}, "usage": nil}
}
func responseMessage(id, text, status string) any {
	return map[string]any{"id": "msg_" + id, "type": "message", "role": "assistant", "status": status, "content": []any{map[string]any{"type": "output_text", "text": text, "annotations": []any{}}}}
}

type events struct {
	w                   io.Writer
	protocol, id, model string
	started             bool
	seq                 int
}

func newEvents(w io.Writer, p, id, model string) *events {
	return &events{w: w, protocol: p, id: id, model: model}
}
func (e *events) write(kind string, data map[string]any) error {
	if e.protocol == "responses" {
		data["type"] = kind
		data["sequence_number"] = e.seq
		e.seq++
	}
	b, err := json.Marshal(data)
	if err != nil {
		return err
	}
	if e.protocol == "responses" {
		_, err = fmt.Fprintf(e.w, "event: %s\ndata: %s\n\n", kind, b)
	} else {
		_, err = fmt.Fprintf(e.w, "data: %s\n\n", b)
	}
	return err
}
func (e *events) delta(text, model string) error {
	e.model = model
	if e.protocol == "chat" {
		return e.write("", map[string]any{"id": e.id, "object": "chat.completion.chunk", "created": time.Now().Unix(), "model": model, "choices": []any{map[string]any{"index": 0, "delta": map[string]string{"role": "assistant", "content": text}, "finish_reason": nil}}})
	}
	if !e.started {
		e.started = true
		for _, event := range []struct {
			name string
			data map[string]any
		}{{"response.created", map[string]any{"response": map[string]any{"id": e.id, "object": "response", "status": "in_progress", "output": []any{}, "model": model}}}, {"response.output_item.added", map[string]any{"output_index": 0, "item": map[string]any{"id": "msg_" + e.id, "type": "message", "role": "assistant", "status": "in_progress", "content": []any{}}}}, {"response.content_part.added", map[string]any{"item_id": "msg_" + e.id, "output_index": 0, "content_index": 0, "part": map[string]any{"type": "output_text", "text": "", "annotations": []any{}}}}} {
			if err := e.write(event.name, event.data); err != nil {
				return err
			}
		}
	}
	return e.write("response.output_text.delta", map[string]any{"item_id": "msg_" + e.id, "output_index": 0, "content_index": 0, "delta": text})
}
func (e *events) complete(model, text string) error {
	if e.protocol == "chat" {
		if err := e.write("", map[string]any{"id": e.id, "object": "chat.completion.chunk", "created": time.Now().Unix(), "model": model, "choices": []any{map[string]any{"index": 0, "delta": map[string]any{}, "finish_reason": "stop"}}}); err != nil {
			return err
		}
		_, err := io.WriteString(e.w, "data: [DONE]\n\n")
		return err
	}
	for _, event := range []struct {
		name string
		data map[string]any
	}{{"response.output_text.done", map[string]any{"item_id": "msg_" + e.id, "output_index": 0, "content_index": 0, "text": text}}, {"response.content_part.done", map[string]any{"item_id": "msg_" + e.id, "output_index": 0, "content_index": 0, "part": map[string]any{"type": "output_text", "text": text, "annotations": []any{}}}}, {"response.output_item.done", map[string]any{"output_index": 0, "item": responseMessage(e.id, text, "completed")}}, {"response.completed", map[string]any{"response": completed("responses", e.id, model, text)}}} {
		if err := e.write(event.name, event.data); err != nil {
			return err
		}
	}
	return nil
}
