package webnext

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"

	"clash-of-tokens/internal/protocol"
)

type eventParser func(context.Context, io.Reader, func(string) error) error

var errStreamDone = errors.New("webnext adapter: stream complete")

// newConvertedResponse converts one upstream stream to OpenAI Chat
// Completions. Closing the returned body closes the upstream body and releases
// the session gate; a context cancellation does the same while a read is
// blocked on the upstream.
func newConvertedResponse(ctx context.Context, upstream io.ReadCloser, model string, parser eventParser) io.ReadCloser {
	reader, writer := io.Pipe()
	state := &convertedBody{PipeReader: reader, writer: writer, upstream: upstream, done: make(chan struct{}), id: randomID("chatcmpl-")}
	go func() {
		select {
		case <-ctx.Done():
			// Closing the pipe reader also unblocks a writer that is waiting
			// for a downstream consumer, so cancellation cannot leave the
			// conversion goroutine parked forever.
			_ = state.Close()
		case <-state.done:
		}
	}()
	go func() {
		defer upstream.Close()
		defer state.signalDone()
		bounded := &boundedWriter{Writer: writer, limit: maxResponseBytes}
		if err := writeChunk(bounded, state.id, model, map[string]any{"role": "assistant"}, nil); err != nil {
			state.finish(err)
			return
		}
		seen := false
		err := parser(ctx, upstream, func(text string) error {
			if text == "" {
				return nil
			}
			seen = true
			return writeChunk(bounded, state.id, model, map[string]any{"content": text}, nil)
		})
		if err == nil && !seen {
			err = errors.New("webnext adapter: upstream completed without answer")
		}
		if err == nil {
			err = writeChunk(bounded, state.id, model, map[string]any{}, "stop")
			if err == nil {
				_, err = io.WriteString(bounded, "data: [DONE]\n\n")
			}
		}
		state.finish(err)
	}()
	return state
}

type boundedWriter struct {
	io.Writer
	limit int
	total int
}

func (w *boundedWriter) Write(p []byte) (int, error) {
	if len(p) > w.limit-w.total {
		return 0, errors.New("webnext adapter: converted output exceeds limit")
	}
	n, err := w.Writer.Write(p)
	w.total += n
	if err == nil && n != len(p) {
		return n, io.ErrShortWrite
	}
	return n, err
}

type convertedBody struct {
	*io.PipeReader
	writer   *io.PipeWriter
	upstream io.Closer
	done     chan struct{}
	doneOnce sync.Once
	id       string
	once     sync.Once
}

func (b *convertedBody) signalDone() { b.doneOnce.Do(func() { close(b.done) }) }

func (b *convertedBody) finish(err error) {
	if err != nil {
		_ = b.writer.CloseWithError(err)
	} else {
		_ = b.writer.Close()
	}
}
func (b *convertedBody) Close() error {
	b.once.Do(func() {
		b.signalDone()
		_ = b.upstream.Close()
	})
	return b.PipeReader.Close()
}

func collectResponse(ctx context.Context, upstream io.ReadCloser, model string, parser eventParser) (*http.Response, error) {
	defer upstream.Close()
	var content strings.Builder
	err := parser(ctx, upstream, func(text string) error {
		if content.Len()+len(text) > maxResponseBytes {
			return errors.New("webnext adapter: upstream response exceeds limit")
		}
		content.WriteString(text)
		return nil
	})
	if err != nil {
		return nil, err
	}
	if content.Len() == 0 {
		return nil, errors.New("webnext adapter: upstream completed without answer")
	}
	id := randomID("chatcmpl-")
	created := time.Now().Unix()
	result := map[string]any{
		"id": id, "object": "chat.completion", "created": created, "model": model,
		"choices": []any{map[string]any{"index": 0, "message": map[string]string{"role": "assistant", "content": content.String()}, "finish_reason": "stop"}},
		"usage":   nil,
	}
	data, err := json.Marshal(result)
	if err != nil {
		return nil, errors.New("webnext adapter: cannot encode response")
	}
	headers := make(http.Header)
	headers.Set("Content-Type", "application/json")
	headers.Set("X-COT-Delivery", "buffered")
	return &http.Response{StatusCode: 200, Header: headers, Body: io.NopCloser(bytes.NewReader(data)), ContentLength: int64(len(data))}, nil
}

func writeChunk(w io.Writer, id, model string, delta map[string]any, finish any) error {
	value := map[string]any{
		"id": id, "object": "chat.completion.chunk", "created": time.Now().Unix(), "model": model,
		"choices": []any{map[string]any{"index": 0, "delta": delta, "finish_reason": finish}},
	}
	data, err := json.Marshal(value)
	if err != nil {
		return err
	}
	_, err = fmt.Fprintf(w, "data: %s\n\n", data)
	return err
}

type boundedLineReader struct {
	r     *bufio.Reader
	limit int
	total int
}

func newBoundedLineReader(r io.Reader, limit int) *boundedLineReader {
	return &boundedLineReader{r: bufio.NewReaderSize(r, 8192), limit: limit}
}

func (r *boundedLineReader) next() (string, error) {
	var line []byte
	for {
		part, err := r.r.ReadSlice('\n')
		if len(line)+len(part) > r.limit {
			return "", errors.New("webnext adapter: upstream event exceeds limit")
		}
		line = append(line, part...)
		r.total += len(part)
		if err == bufio.ErrBufferFull {
			continue
		}
		if err != nil {
			if err == io.EOF && len(line) == 0 {
				return "", io.EOF
			}
			if err == io.EOF {
				// requests.iter_lines() yields a final line even when the
				// upstream closes without a trailing newline.
				return strings.TrimSuffix(strings.TrimSuffix(string(line), "\n"), "\r"), io.EOF
			}
			return "", err
		}
		return strings.TrimSuffix(strings.TrimSuffix(string(line), "\n"), "\r"), nil
	}
}

func parseSSE(ctx context.Context, body io.Reader, handle func([]byte) error) error {
	reader := protocol.NewSSEReader(body, maxEventBytes)
	var total int
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		frame, err := reader.Next()
		if err == io.EOF {
			return ErrTruncated
		}
		if err != nil {
			return ErrTruncated
		}
		total += len(frame)
		if total > maxResponseBytes {
			return errors.New("webnext adapter: upstream stream exceeds limit")
		}
		data := bytes.TrimSpace(protocol.SSEData(frame))
		if len(data) == 0 {
			continue
		}
		if err := handle(data); err != nil {
			if errors.Is(err, errStreamDone) {
				return nil
			}
			return err
		}
	}
}

func parseEaseMate(ctx context.Context, body io.Reader, emit func(string) error) error {
	done := false
	seen := false
	err := parseSSE(ctx, body, func(data []byte) error {
		if bytes.Equal(data, []byte("[DONE]")) {
			done = true
			return errStreamDone
		}
		var outer struct {
			Code  int             `json:"code"`
			Data  json.RawMessage `json:"data"`
			Error json.RawMessage `json:"error"`
		}
		if json.Unmarshal(data, &outer) != nil {
			return errors.New("webnext adapter: malformed EaseMate event")
		}
		if len(outer.Error) > 0 && string(outer.Error) != "null" {
			return ErrUpstream
		}
		if outer.Code != 0 && outer.Code != 200 {
			return ErrUpstream
		}
		var answer struct {
			Answer string `json:"answer"`
		}
		if len(outer.Data) > 0 && outer.Data[0] == '"' {
			var nested string
			if json.Unmarshal(outer.Data, &nested) == nil {
				_ = json.Unmarshal([]byte(nested), &answer)
			}
		} else {
			_ = json.Unmarshal(outer.Data, &answer)
		}
		if answer.Answer != "" {
			seen = true
			return emit(answer.Answer)
		}
		return nil
	})
	if err != nil {
		return err
	}
	if !done {
		return ErrTruncated
	}
	if !seen {
		return errors.New("webnext adapter: EaseMate completed without answer")
	}
	return nil
}

func parseLiaoBots(ctx context.Context, body io.Reader, emit func(string) error) error {
	seen := false
	reader := protocol.NewSSEReader(body, maxEventBytes)
	var total int
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		frame, err := reader.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return ErrTruncated
		}
		total += len(frame)
		if total > maxResponseBytes {
			return errors.New("webnext adapter: upstream stream exceeds limit")
		}
		data := bytes.TrimSpace(protocol.SSEData(frame))
		if len(data) == 0 || bytes.Equal(data, []byte("[DONE]")) {
			continue
		}
		// The pinned worker only parses JSON data records and emits the
		// provider's `content` field. Other records and malformed JSON are
		// ignored by that worker and must not be reinterpreted as text.
		var value struct {
			Content string `json:"content"`
		}
		if json.Unmarshal(data, &value) != nil || value.Content == "" {
			continue
		}
		seen = true
		if err := emit(value.Content); err != nil {
			return err
		}
	}
	if !seen {
		return errors.New("webnext adapter: LiaoBots completed without answer")
	}
	return nil
}

func parseFlowith(ctx context.Context, body io.Reader, emit func(string) error) error {
	reader := newBoundedLineReader(body, maxEventBytes)
	seen := false
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		line, err := reader.next()
		if err != nil && err != io.EOF {
			return err
		}
		if reader.total > maxResponseBytes {
			return errors.New("webnext adapter: upstream stream exceeds limit")
		}
		// The pinned Flowith adapter forwards each non-empty iter_lines()
		// value verbatim. It does not parse JSON or strip an SSE data prefix.
		if line != "" {
			seen = true
			if err := emit(line); err != nil {
				return err
			}
		}
		if err == io.EOF {
			break
		}
	}
	if !seen {
		return errors.New("webnext adapter: Flowith completed without answer")
	}
	return nil
}
