package embedded

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"clash-of-tokens/internal/config"
)

func TestTabbitFallbackUsesV1Endpoint(t *testing.T) {
	t.Setenv("TABBIT_FALLBACK_COOKIE", "token=test")
	var paths []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/panel/session":
			io.WriteString(w, `{"chat_session_id":"11111111-2222-4333-8444-555555555555"}`)
		case "/api/v2/chat/completion":
			paths = append(paths, r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
		case "/api/v1/chat/completion":
			paths = append(paths, r.URL.Path)
			w.Header().Set("Content-Type", "text/event-stream")
			io.WriteString(w, "event: message_chunk\ndata: {\"content\":\"ok\"}\n\nevent: finish\ndata: {}\n\n")
		}
	}))
	defer server.Close()
	c := New(config.Source{Adapter: AdapterTabbit, BaseURL: server.URL, KeyEnv: "TABBIT_FALLBACK_COOKIE"})
	defer c.Close()
	resp, err := c.Do(context.Background(), "chat", "best", true, []byte(`{"messages":[{"role":"user","content":"hi"}]}`), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if _, err := io.ReadAll(resp.Body); err != nil {
		t.Fatal(err)
	}
	if len(paths) != 2 || paths[0] != "/api/v2/chat/completion" || paths[1] != "/api/v1/chat/completion" {
		t.Fatalf("unexpected requests: %v", paths)
	}
}

func TestTransformBodyCumulativeOutputLimit(t *testing.T) {
	closed := false
	b := &transformBody{next: func() ([]byte, error) { return make([]byte, 1<<20), nil }, closeFn: func() error { closed = true; return nil }}
	n, err := io.Copy(io.Discard, b)
	if err == nil || n != 16<<20 || !closed {
		t.Fatalf("bytes=%d err=%v closed=%v", n, err, closed)
	}
}
