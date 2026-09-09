package majorweb

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"clash-of-tokens/internal/config"
)

func TestT3ContractAndTerminal(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/chat" || r.Method != "POST" || r.Header.Get("Cookie") != "convex-session-id=own" {
			t.Error("wrong request contract")
		}
		b, _ := io.ReadAll(r.Body)
		if !strings.Contains(string(b), `"stream":true`) || !strings.Contains(string(b), `"content":"hello"`) {
			t.Error("wrong payload")
		}
		w.Header().Set("Content-Type", "application/x-ndjson")
		io.WriteString(w, "{\"t\":10,\"p\":{\"k\":[\"content\"],\"v\":[{\"t\":2,\"s\":\"hello world\"}]}}\n{\"done\":true}\n")
		w.(http.Flusher).Flush()
		<-r.Context().Done()
	}))
	defer server.Close()
	t.Setenv("COT_TEST_T3", "convex-session-id=own")
	c := New(config.Source{Adapter: "t3-web", BaseURL: server.URL, KeyEnv: "COT_TEST_T3"})
	defer c.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	r, err := c.Do(ctx, "chat", "product-id", false, []byte(`{"messages":[{"role":"user","content":"hello"}]}`), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer r.Body.Close()
	b, _ := io.ReadAll(r.Body)
	if !strings.Contains(string(b), "hello world") || strings.Contains(string(b), "total_tokens") {
		t.Fatalf("unexpected response %s", b)
	}
}

func TestT3RejectsTruncationAndErrors(t *testing.T) {
	for _, raw := range []string{
		"{\"text\":\"partial\"}\n",
		"{\"text\":\"partial\"}\n{\"error\":{\"secret\":\"credential\"}}\n",
		"not-json\n",
		"{\"t\":10,\"p\":{\"k\":[\"text\"],\"v\":[]}}\n",
	} {
		_, err := readT3(strings.NewReader(raw))
		if err == nil {
			t.Fatal("accepted incomplete/error response")
		}
		if strings.Contains(err.Error(), "credential") {
			t.Fatal("leaked upstream error")
		}
	}
	text, err := readT3(strings.NewReader("data: {\"text\":\"ok\"}\n\ndata: [DONE]\n\n"))
	if err != nil || text != "ok" {
		t.Fatalf("%q %v", text, err)
	}
}
