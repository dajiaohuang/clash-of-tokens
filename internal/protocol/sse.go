package protocol

import (
	"bufio"
	"bytes"
	"errors"
	"io"
)

// SSEReader returns complete wire frames and enforces a byte limit independently
// of line boundaries. Network packets and UTF-8 byte fragments are not events.
type SSEReader struct {
	r     *bufio.Reader
	limit int
	frame []byte
}

func NewSSEReader(r io.Reader, limit int) *SSEReader {
	return &SSEReader{r: bufio.NewReaderSize(r, 8192), limit: limit}
}
func (s *SSEReader) Next() ([]byte, error) {
	s.frame = s.frame[:0]
	lineBytes := 0
	for {
		line, e := s.r.ReadSlice('\n')
		if len(s.frame)+len(line) > s.limit {
			return nil, errors.New("SSE event exceeds byte limit")
		}
		s.frame = append(s.frame, line...)
		lineBytes += len(line)
		if e == bufio.ErrBufferFull {
			continue
		}
		if e != nil {
			if e == io.EOF && len(s.frame) == 0 {
				return nil, io.EOF
			}
			return nil, errors.New("truncated SSE event")
		}
		// An empty line is LF or CRLF. A preceding ReadSlice fragment makes it a
		// nonempty line, even when the final fragment is just the newline.
		if lineBytes == 1 || (lineBytes == 2 && len(line) == 2 && line[0] == '\r') {
			return s.frame, nil
		}
		lineBytes = 0
	}
}

// SSEData joins multiline data fields as specified by SSE. The common one-line
// path returns a view into frame without allocating.
func SSEData(frame []byte) []byte {
	var data []byte
	capacity := len(frame)
	owned := false
	for len(frame) > 0 {
		line, rest, _ := bytes.Cut(frame, []byte{'\n'})
		frame = rest
		line = bytes.TrimSuffix(line, []byte{'\r'})
		if bytes.HasPrefix(line, []byte("data:")) {
			v := bytes.TrimPrefix(line[5:], []byte{' '})
			if data == nil {
				data = v
			} else {
				if !owned {
					next := make([]byte, len(data), capacity)
					copy(next, data)
					data = next
					owned = true
				}
				data = append(data, '\n')
				data = append(data, v...)
			}
		}
	}
	return data
}
