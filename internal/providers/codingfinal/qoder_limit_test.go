package codingfinal

import (
	"io"
	"strings"
	"testing"
)

func TestQoderCommandPipeCannotBypassOutputLimit(t *testing.T) {
	out := &qoderLimitedBuffer{limit: 32}
	// An OS command pipe is a Reader; avoid strings.Reader's WriterTo fast path.
	_, err := io.Copy(out, struct{ io.Reader }{strings.NewReader(strings.Repeat("x", 64))})
	if err == nil || out.Len() > 32 {
		t.Fatal("command pipe bypassed output bound")
	}
}
