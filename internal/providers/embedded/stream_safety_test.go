package embedded

import (
	"io"
	"strings"
	"testing"
	"time"
)

func TestStreamRequiresCompletion(t *testing.T) {
	c := &Client{}
	defer c.Close()
	for _, input := range []string{"", "event: message_chunk\ndata: {\"content\":\"partial\"}\n\n"} {
		body := c.newStreamBody(io.NopCloser(strings.NewReader(input)), "test", "model", func(event, data string) ([]byte, error) {
			return tabbitEvent(event, data, "test", "model", 0)
		})
		data, err := io.ReadAll(body)
		body.Close()
		if err == nil || strings.Contains(string(data), "[DONE]") {
			t.Fatalf("truncated stream reported success: %s, %v", data, err)
		}
	}
}

func TestCloseUnblocksRead(t *testing.T) {
	r, w := io.Pipe()
	defer w.Close()
	b := &transformBody{next: func() ([]byte, error) { out := make([]byte, 1); n, err := r.Read(out); return out[:n], err }, closeFn: r.Close}
	done := make(chan struct{})
	go func() { _, _ = b.Read(make([]byte, 1)); close(done) }()
	closed := make(chan struct{})
	go func() { b.Close(); close(closed) }()
	select {
	case <-closed:
	case <-time.After(time.Second):
		t.Fatal("Close blocked behind Read")
	}
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("Read did not unblock")
	}
}

func TestOversizedSSELine(t *testing.T) {
	d := newSSEDecoder(strings.NewReader("data: " + strings.Repeat("x", (1<<20)+1)))
	if _, _, _, err := d.next(); err == nil {
		t.Fatal("oversized event accepted")
	}
}

func TestRejectMixedImageContent(t *testing.T) {
	if err := validateSemantics([]byte(`{"messages":[{"role":"user","content":[{"type":"text","text":"describe"},{"type":"image_url","image_url":{"url":"https://example.com/a.png"}}]}]}`), AdapterTabbit); err == nil {
		t.Fatal("image silently dropped")
	}
}
