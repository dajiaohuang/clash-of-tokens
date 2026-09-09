package chinamore

import (
	"bufio"
	"bytes"
	"context"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
	"time"
)

type streamEmitter struct {
	sink    io.Writer
	id      string
	model   string
	role    bool
	created int64
	written int64
}

const maxStreamBytes int64 = 64 << 20

func newEmitter(sink io.Writer, id, model string) *streamEmitter {
	return &streamEmitter{sink: sink, id: id, model: model, created: timeNowUnix()}
}

func (e *streamEmitter) chunk(delta map[string]any) error {
	if !e.role {
		delta["role"] = "assistant"
		e.role = true
	}
	value := map[string]any{
		"id": e.id, "object": "chat.completion.chunk", "created": e.created, "model": e.model,
		"choices": []any{map[string]any{"index": 0, "delta": delta, "finish_reason": nil}},
	}
	b, err := json.Marshal(value)
	if err != nil {
		return err
	}
	return e.write(append(append([]byte("data: "), b...), '\n', '\n'))
}

func (e *streamEmitter) finish(reason string) error {
	if !e.role {
		if err := e.chunk(map[string]any{}); err != nil {
			return err
		}
	}
	b, err := json.Marshal(map[string]any{
		"id": e.id, "object": "chat.completion.chunk", "created": e.created, "model": e.model,
		"choices": []any{map[string]any{"index": 0, "delta": map[string]any{}, "finish_reason": reason}},
	})
	if err != nil {
		return err
	}
	if err = e.write(append(append([]byte("data: "), b...), '\n', '\n')); err != nil {
		return err
	}
	return e.write([]byte("data: [DONE]\n\n"))
}

func (e *streamEmitter) write(data []byte) error {
	if int64(len(data)) > maxStreamBytes-e.written {
		return errors.New("china more web adapter: converted stream exceeds byte limit")
	}
	n, err := e.sink.Write(data)
	e.written += int64(n)
	if err == nil && n != len(data) {
		return io.ErrShortWrite
	}
	return err
}

func timeNowUnix() int64 { return time.Now().Unix() }

func completionJSON(model, id, content, reasoning string) []byte {
	message := map[string]any{"role": "assistant", "content": content}
	if reasoning != "" {
		message["reasoning_content"] = reasoning
	}
	result := map[string]any{
		"id": id, "object": "chat.completion", "created": timeNowUnix(), "model": model,
		"choices": []any{map[string]any{"index": 0, "message": message, "finish_reason": "stop"}},
	}
	b, _ := json.Marshal(result)
	return b
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

func nextSSEFrame(reader *bufio.Reader) ([]byte, error) {
	frame := make([]byte, 0, 256)
	for {
		line, err := reader.ReadSlice('\n')
		if len(line) > maxEventBytes-len(frame) {
			return nil, errors.New("SSE event exceeds byte limit")
		}
		if len(line) > 0 {
			frame = append(frame, line...)
		}
		if err == bufio.ErrBufferFull {
			continue
		}
		if err != nil {
			if err == io.EOF && len(frame) == 0 {
				return nil, io.EOF
			}
			return nil, errors.New("truncated SSE event")
		}
		if len(line) == 1 || (len(line) == 2 && line[0] == '\r') {
			return frame, nil
		}
	}
}

type mimoState struct {
	content      strings.Builder
	contentBytes int64
	usage        map[string]any
	seen         bool
	done         bool
}

func (s *mimoState) addContent(text string, emitter *streamEmitter) error {
	if int64(len(text)) > maxStreamBytes-s.contentBytes {
		return errors.New("china more web adapter: Mimo content exceeds byte limit")
	}
	s.content.WriteString(text)
	s.contentBytes += int64(len(text))
	if emitter != nil {
		return emitter.chunk(map[string]any{"content": text})
	}
	return nil
}

func consumeMimo(state *mimoState, event string, data map[string]any, emitter *streamEmitter) error {
	state.seen = true
	typ := event
	if fromData, ok := data["type"].(string); ok && fromData != "" {
		typ = fromData
	}
	if typ == "error" || typ == "error_event" {
		return errors.New("china more web adapter: Mimo upstream error")
	}
	if _, hasError := data["error"]; hasError {
		return errors.New("china more web adapter: Mimo upstream error")
	}
	if text, ok := data["content"].(string); ok && (typ == "" || typ == "message" || typ == "text" || typ == "answer") {
		text = strings.ReplaceAll(text, "\x00", "")
		if text != "" {
			if err := state.addContent(text, emitter); err != nil {
				return err
			}
		}
	}
	if usage, ok := data["usage"].(map[string]any); ok {
		state.usage = usage
	}
	if typ == "finish" || typ == "done" {
		state.done = true
	}
	if finished, ok := data["done"].(bool); ok && finished {
		state.done = true
	}
	if reason, ok := data["stopReason"].(string); ok && reason != "" {
		state.done = true
	}
	return nil
}

func convertMimo(ctx context.Context, body io.Reader, model, id string, sink io.Writer, stream bool) error {
	reader := bufio.NewReaderSize(body, 8192)
	state := &mimoState{}
	var emitter *streamEmitter
	if stream {
		emitter = newEmitter(sink, id, model)
	}
	var inputBytes int64
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		frame, err := nextSSEFrame(reader)
		if err == nil {
			if int64(len(frame)) > maxStreamBytes-inputBytes {
				return errors.New("china more web adapter: Mimo upstream stream exceeds byte limit")
			}
			inputBytes += int64(len(frame))
		}
		if err == io.EOF {
			if !state.seen || !state.done {
				return ErrTruncated
			}
			break
		}
		if err != nil {
			return fmt.Errorf("%w: %v", ErrTruncated, err)
		}
		dataBytes := bytes.TrimSpace(sseData(frame))
		if len(dataBytes) == 0 {
			continue
		}
		if bytes.Equal(dataBytes, []byte("[DONE]")) {
			state.done = true
			break
		}
		var value map[string]any
		if err := json.Unmarshal(dataBytes, &value); err != nil {
			return fmt.Errorf("china more web adapter: malformed Mimo SSE: %w", err)
		}
		if err := consumeMimo(state, sseEvent(frame), value, emitter); err != nil {
			return err
		}
		if state.done {
			break
		}
	}
	if stream {
		return emitter.finish("stop")
	}
	if !state.done {
		return ErrTruncated
	}
	result := completionJSON(model, id, state.content.String(), "")
	if int64(len(result)) > maxStreamBytes {
		return errors.New("china more web adapter: Mimo converted response exceeds byte limit")
	}
	_, err := sink.Write(result)
	return err
}

func sseData(frame []byte) []byte {
	var data []byte
	for len(frame) > 0 {
		line, rest, _ := bytes.Cut(frame, []byte{'\n'})
		frame = rest
		line = bytes.TrimSuffix(line, []byte{'\r'})
		if bytes.HasPrefix(line, []byte("data:")) {
			value := bytes.TrimPrefix(line[5:], []byte{' '})
			if data == nil {
				data = value
			} else {
				data = append(append(data, '\n'), value...)
			}
		}
	}
	return data
}

func nextConnectFrame(r io.Reader) ([]byte, error) {
	payload, _, err := nextConnectFrameWithFlags(r)
	return payload, err
}

func nextConnectFrameWithFlags(r io.Reader) ([]byte, byte, error) {
	header := make([]byte, 5)
	if _, err := io.ReadFull(r, header); err != nil {
		return nil, 0, err
	}
	if header[0]&^byte(2) != 0 {
		return nil, 0, errors.New("china more web adapter: unsupported StepChat frame flags")
	}
	n := binary.BigEndian.Uint32(header[1:])
	if n > maxEventBytes {
		return nil, 0, errors.New("china more web adapter: StepChat frame exceeds byte limit")
	}
	payload := make([]byte, n)
	if _, err := io.ReadFull(r, payload); err != nil {
		return nil, 0, errors.New("china more web adapter: truncated StepChat frame")
	}
	return payload, header[0], nil
}

type stepState struct {
	content      strings.Builder
	contentBytes int64
	seen         bool
	done         bool
}

func (s *stepState) addContent(text string, emitter *streamEmitter) error {
	if int64(len(text)) > maxStreamBytes-s.contentBytes {
		return errors.New("china more web adapter: StepChat content exceeds byte limit")
	}
	s.content.WriteString(text)
	s.contentBytes += int64(len(text))
	if emitter != nil {
		return emitter.chunk(map[string]any{"content": text})
	}
	return nil
}

func consumeStep(state *stepState, value map[string]any, emitter *streamEmitter) error {
	state.seen = true
	if errorValue, ok := value["error"].(map[string]any); ok {
		return fmt.Errorf("china more web adapter: StepChat upstream error (code %d)", jsonNumber(errorValue["code"]))
	}
	if event, ok := value["textEvent"].(map[string]any); ok {
		if text := rawMapString(event, "text"); text != "" {
			if err := state.addContent(text, emitter); err != nil {
				return err
			}
		}
	}
	if event, ok := value["pipelineEvent"].(map[string]any); ok {
		if search, ok := event["eventSearch"].(map[string]any); ok {
			if results, ok := search["results"].([]any); ok {
				for _, item := range results {
					m, _ := item.(map[string]any)
					title, link := rawMapString(m, "title"), rawMapString(m, "url")
					if title == "" && link == "" {
						continue
					}
					text := title + " - " + link + "\n"
					if err := state.addContent(text, emitter); err != nil {
						return err
					}
				}
			}
		}
	}
	if _, ok := value["doneEvent"].(map[string]any); ok {
		state.done = true
	}
	return nil
}

func convertStep(ctx context.Context, body io.Reader, model, id string, sink io.Writer, stream bool) error {
	state := &stepState{}
	var emitter *streamEmitter
	if stream {
		emitter = newEmitter(sink, id, model)
	}
	var inputBytes int64
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		payload, flags, err := nextConnectFrameWithFlags(body)
		if err == io.EOF {
			if !state.done {
				return ErrTruncated
			}
			break
		}
		if err != nil {
			if errors.Is(err, io.ErrUnexpectedEOF) {
				return ErrTruncated
			}
			return err
		}
		if int64(len(payload))+5 > maxStreamBytes-inputBytes {
			return errors.New("china more web adapter: StepChat upstream stream exceeds byte limit")
		}
		inputBytes += int64(len(payload)) + 5
		var value map[string]any
		if len(bytes.TrimSpace(payload)) > 0 {
			if err := json.Unmarshal(payload, &value); err != nil {
				return fmt.Errorf("china more web adapter: malformed StepChat frame: %w", err)
			}
			if err := consumeStep(state, value, emitter); err != nil {
				return err
			}
		}
		if flags&2 != 0 {
			state.done = true
		}
		if state.done {
			break
		}
	}
	if !state.seen || !state.done {
		return ErrTruncated
	}
	if stream {
		return emitter.finish("stop")
	}
	result := completionJSON(model, id, state.content.String(), "")
	if int64(len(result)) > maxStreamBytes {
		return errors.New("china more web adapter: StepChat converted response exceeds byte limit")
	}
	_, err := sink.Write(result)
	return err
}
