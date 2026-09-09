package upstream

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"

	"clash-of-tokens/internal/protocol"
)

func cloudRequest(body []byte, model, project string) ([]byte, error) {
	return json.Marshal(struct {
		Model   string          `json:"model"`
		Project string          `json:"project"`
		Request json.RawMessage `json:"request"`
	}{model, project, body})
}
func unwrapCloud(b []byte) ([]byte, error) {
	var v struct {
		Response json.RawMessage `json:"response"`
		Error    json.RawMessage `json:"error"`
	}
	if json.Unmarshal(b, &v) != nil || len(v.Response) == 0 || len(v.Error) > 0 {
		return nil, errors.New("invalid Code Assist response envelope")
	}
	return v.Response, nil
}

type cloudStream struct {
	body      io.ReadCloser
	events    *protocol.SSEReader
	pending   []byte
	maxBytes  int64
	readBytes int64
}

func (s *cloudStream) Read(b []byte) (int, error) {
	for len(s.pending) == 0 {
		frame, e := s.events.Next()
		if e != nil {
			return 0, e
		}
		s.readBytes += int64(len(frame))
		if s.readBytes > s.maxBytes {
			return 0, errors.New("Code Assist output exceeds limit")
		}
		data := protocol.SSEData(frame)
		if len(data) == 0 {
			continue
		}
		unwrapped, e := unwrapCloud(data)
		if e != nil {
			return 0, e
		}
		s.pending = make([]byte, 0, len(unwrapped)+8)
		s.pending = append(s.pending, "data: "...)
		s.pending = append(s.pending, unwrapped...)
		s.pending = append(s.pending, '\n', '\n')
	}
	n := copy(b, s.pending)
	s.pending = s.pending[n:]
	return n, nil
}
func (s *cloudStream) Close() error { return s.body.Close() }
func cloudResponse(body io.ReadCloser, stream bool) (io.ReadCloser, error) {
	if stream {
		return &cloudStream{body: body, events: protocol.NewSSEReader(body, 1<<20), maxBytes: 64 << 20}, nil
	}
	defer body.Close()
	data, e := io.ReadAll(io.LimitReader(body, (4<<20)+1))
	if e != nil || len(data) > 4<<20 {
		return nil, errors.New("Code Assist response exceeds limit")
	}
	data, e = unwrapCloud(data)
	if e != nil {
		return nil, e
	}
	return io.NopCloser(bytes.NewReader(data)), nil
}
