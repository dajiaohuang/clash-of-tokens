package coding

import (
	"bytes"
	"io"
	"strings"
	"testing"

	wire "clash-of-tokens/internal/protocol"
)

func TestKiroAnthropicBlockLifecycle(t *testing.T) {
	frame := makeEventFrame(t, map[string]string{":event-type": "assistantResponseEvent"}, `{"content":"hello","status":"completed"}`)
	body := io.NopCloser(bytes.NewReader(frame))
	s := &kiroStream{body: body, events: newEventReader(body), protocol: "messages", model: "test"}
	data, err := io.ReadAll(s)
	s.Close()
	if err != nil {
		t.Fatal(err)
	}
	prev := -1
	for _, event := range []string{"event: message_start", "event: content_block_start", "event: content_block_delta", "event: content_block_stop", "event: message_delta", "event: message_stop"} {
		i := strings.Index(string(data), event)
		if i <= prev {
			t.Fatalf("invalid lifecycle for %s: %s", event, data)
		}
		prev = i
	}
}

func TestAntigravityCompletionRequired(t *testing.T) {
	for _, tc := range []struct {
		input string
		ok    bool
	}{
		{"", false},
		{"data: {\"response\":{\"candidates\":[{\"content\":{\"parts\":[{\"text\":\"partial\"}]}}]}}\n\n", false},
		{"data: {\"response\":{\"candidates\":[{\"finishReason\":\"STOP\"}]}}\n\n", true},
		{"data: [DONE]\n\n", false},
	} {
		b := io.NopCloser(strings.NewReader(tc.input))
		s := &antigravityStream{body: b, events: wire.NewSSEReader(b, 1<<20)}
		_, err := io.ReadAll(s)
		s.Close()
		if (err == nil) != tc.ok {
			t.Fatalf("completion=%v want %v for %q: %v", err == nil, tc.ok, tc.input, err)
		}
	}
}
