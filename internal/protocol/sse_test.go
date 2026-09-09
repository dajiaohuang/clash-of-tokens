package protocol

import (
	"io"
	"strings"
	"testing"
)

func TestSSEMultilineAndLimits(t *testing.T) {
	s := NewSSEReader(strings.NewReader(": heartbeat\r\n\r\ndata: hello\ndata: 世界\n\n"), 100)
	f, e := s.Next()
	if e != nil || len(SSEData(f)) != 0 {
		t.Fatal(e)
	}
	f, e = s.Next()
	if e != nil || string(SSEData(f)) != "hello\n世界" {
		t.Fatalf("%q %v", SSEData(f), e)
	}
	if _, e = s.Next(); e != io.EOF {
		t.Fatal(e)
	}
	s = NewSSEReader(strings.NewReader("data: "+strings.Repeat("x", 100)+"\n\n"), 32)
	if _, e = s.Next(); e == nil {
		t.Fatal("event limit not enforced")
	}
	s = NewSSEReader(strings.NewReader("data: unterminated"), 32)
	if _, e = s.Next(); e == nil || e == io.EOF {
		t.Fatal("truncation accepted")
	}
}
