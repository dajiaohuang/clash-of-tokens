package chinamore

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"io"
	"strings"
	"testing"
)

func TestMimoIncompleteEventIsTruncated(t *testing.T) {
	body := "event: message\ndata: {\"content\":\"partial\"}\n"
	err := convertMimo(context.Background(), strings.NewReader(body), "mimo", "id", io.Discard, false)
	if !errors.Is(err, ErrTruncated) {
		t.Fatalf("incomplete Mimo event error=%v", err)
	}
}

func TestMimoUpstreamErrorDoesNotLeakMessage(t *testing.T) {
	const secret = "upstream-private-error"
	err := consumeMimo(&mimoState{}, "error", map[string]any{"message": secret}, nil)
	if err == nil || strings.Contains(err.Error(), secret) {
		t.Fatalf("Mimo error=%v", err)
	}
}

func TestSSELineLimitIsBounded(t *testing.T) {
	reader := bufio.NewReaderSize(bytes.NewReader(bytes.Repeat([]byte{'x'}, maxEventBytes+1)), 8192)
	if _, err := nextSSEFrame(reader); err == nil {
		t.Fatal("oversized unterminated SSE line accepted")
	}
}

func TestConvertedContentAndOutputHaveCumulativeLimits(t *testing.T) {
	mimo := &mimoState{contentBytes: maxStreamBytes}
	if err := mimo.addContent("x", nil); err == nil {
		t.Fatal("Mimo cumulative content limit not enforced")
	}
	step := &stepState{contentBytes: maxStreamBytes}
	if err := step.addContent("x", nil); err == nil {
		t.Fatal("StepChat cumulative content limit not enforced")
	}
	emitter := newEmitter(io.Discard, "id", "model")
	emitter.written = maxStreamBytes
	if err := emitter.chunk(map[string]any{"content": "x"}); err == nil {
		t.Fatal("converted output limit not enforced")
	}
}

func TestConnectEndStreamTrailerFlagIsAccepted(t *testing.T) {
	frame := []byte{2, 0, 0, 0, 0}
	payload, flags, err := nextConnectFrameWithFlags(bytes.NewReader(frame))
	if err != nil || len(payload) != 0 || flags != 2 {
		t.Fatalf("trailer frame payload=%v flags=%d err=%v", payload, flags, err)
	}
}
