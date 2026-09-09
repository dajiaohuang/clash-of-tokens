package playground

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	"clash-of-tokens/internal/config"
)

type fakeFrames struct {
	frames []string
	closed chan struct{}
	once   sync.Once
	block  bool
}

func (f *fakeFrames) Next(ctx context.Context) (string, error) {
	if len(f.frames) > 0 {
		s := f.frames[0]
		f.frames = f.frames[1:]
		return s, nil
	}
	if f.block {
		select {
		case <-ctx.Done():
			return "", ctx.Err()
		case <-f.closed:
			return "", io.EOF
		}
	}
	return "", io.EOF
}
func (f *fakeFrames) Close() error { f.once.Do(func() { close(f.closed) }); return nil }
func frame(id string, body any, done bool) string {
	b, _ := json.Marshal(map[string]any{"type": "cf_agent_use_chat_response", "id": id, "body": body, "done": done})
	return string(b)
}

func TestFrameCorrelationAndCompletion(t *testing.T) {
	f := &fakeFrames{closed: make(chan struct{}), frames: []string{`{"type":"rpc","id":"cot-config","done":true}`, frame("other", nil, true), frame("turn", map[string]any{"type": "text-delta", "delta": "hello"}, false), frame("turn", `{"type":"finish","messageMetadata":{"finishReason":"length"}}`, false), frame("turn", nil, true)}}
	var text strings.Builder
	reason, err := consume(context.Background(), f, "turn", func(_, s string) error { text.WriteString(s); return nil })
	if err != nil || reason != "length" || text.String() != "hello" {
		t.Fatalf("reason=%s text=%s err=%v", reason, text.String(), err)
	}
}

func TestIncompleteAndErrorFramesDoNotSucceed(t *testing.T) {
	for _, frames := range [][]string{{frame("turn", nil, true)}, {frame("turn", map[string]string{"type": "text-delta", "delta": "partial"}, false)}, {`{"type":"cf_agent_use_chat_response","id":"turn","error":true,"body":"cookie=secret"}`}} {
		_, err := consume(context.Background(), &fakeFrames{frames: frames, closed: make(chan struct{})}, "turn", func(string, string) error { return nil })
		if err == nil || strings.Contains(err.Error(), "secret") {
			t.Fatalf("unexpected error: %v", err)
		}
	}
}

func TestRequestRejectsUnsupportedSemantics(t *testing.T) {
	for _, body := range []string{`{"messages":[{"role":"system","content":"hidden"}]}`, `{"messages":[{"role":"user","content":"hi"}],"tools":[]}`, `{"messages":[{"role":"user","content":[{"type":"image_url"}]}]}`, `{"messages":[{"role":"user","content":"hi"}],"temperature":null}`} {
		if _, err := requestPayload([]byte(body), "model"); err == nil {
			t.Fatalf("accepted %s", body)
		}
	}
	p, err := requestPayload([]byte(`{"messages":[{"role":"user","content":"hi"},{"role":"assistant","content":"hello"},{"role":"user","content":"next"}],"temperature":0}`), "model")
	if err != nil || p["model"] != "@cf/model" || p["temperature"] != float64(0) || len(p["messages"].([]any)) != 3 {
		t.Fatalf("payload=%v err=%v", p, err)
	}
}

func TestDownstreamCloseCancelsBrowserWait(t *testing.T) {
	c := New(config.Browser{Enabled: true})
	f := &fakeFrames{closed: make(chan struct{}), block: true}
	c.start = func(context.Context, string, any) (frameTransport, error) { return f, nil }
	resp, err := c.Do(context.Background(), "chat", "model", true, []byte(`{"messages":[{"role":"user","content":"hi"}]}`), http.Header{})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := resp.Body.Read(make([]byte, 1024)); err != nil {
		t.Fatal(err)
	}
	if err := resp.Body.Close(); err != nil {
		t.Fatal(err)
	}
	select {
	case <-f.closed:
	case <-time.After(time.Second):
		t.Fatal("browser transport did not close")
	}
}

func TestNativeJSONAggregation(t *testing.T) {
	c := New(config.Browser{Enabled: true})
	c.start = func(ctx context.Context, id string, payload any) (frameTransport, error) {
		return &fakeFrames{closed: make(chan struct{}), frames: []string{frame(id, map[string]string{"type": "text-delta", "delta": "answer"}, false), frame(id, map[string]string{"type": "finish"}, false), frame(id, nil, true)}}, nil
	}
	resp, err := c.Do(context.Background(), "chat", "model", false, []byte(`{"messages":[{"role":"user","content":"hi"}]}`), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	b, err := io.ReadAll(resp.Body)
	if err != nil || !strings.Contains(string(b), `"content":"answer"`) || strings.Contains(string(b), `"usage"`) {
		t.Fatalf("result=%s err=%v", b, err)
	}
}
