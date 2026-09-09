package majorweb

import (
	"clash-of-tokens/internal/config"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestZenmuxWireAndLength(t *testing.T) {
	t.Setenv("ZENMUX_TEST_COOKIE", "ctoken=secret+value; session=mine")
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/anthropic/v1/messages" || r.URL.Query().Get("ctoken") != "secret+value" || r.Header.Get("Cookie") != "ctoken=secret+value; session=mine" || r.Header.Get("Authorization") != "" {
			t.Errorf("wrong request contract")
		}
		var p map[string]any
		json.NewDecoder(r.Body).Decode(&p)
		if p["model"] != "deepseek/deepseek-chat" || p["stream"] != true || p["max_tokens"] != float64(4096) {
			t.Errorf("wrong payload")
		}
		io.WriteString(w, "data: {\"type\":\"content_block_delta\",\"delta\":{\"type\":\"thinking_delta\",\"thinking\":\"thought\"}}\n\ndata: {\"type\":\"content_block_delta\",\"delta\":{\"type\":\"text_delta\",\"text\":\"answer\"}}\n\ndata: {\"type\":\"message_delta\",\"delta\":{\"stop_reason\":\"max_tokens\"}}\n\ndata: {\"type\":\"message_stop\"}\n\n")
	}))
	defer server.Close()
	c := New(config.Source{Adapter: "zenmux-web", KeyEnv: "ZENMUX_TEST_COOKIE", BaseURL: server.URL})
	defer c.Close()
	for _, stream := range []bool{false, true} {
		resp, err := c.Do(context.Background(), "chat", "deepseek/deepseek-chat", stream, []byte(`{"messages":[{"role":"user","content":"hello"}]}`), http.Header{"Authorization": {"caller-secret"}})
		if err != nil {
			t.Fatal(err)
		}
		b, err := io.ReadAll(resp.Body)
		resp.Body.Close()
		if err != nil {
			t.Fatal(err)
		}
		for _, want := range []string{`"content":"answer"`, `"reasoning_content":"thought"`, `"finish_reason":"length"`} {
			if !strings.Contains(string(b), want) {
				t.Fatalf("missing %s: %s", want, b)
			}
		}
	}
}

func TestZenmuxTruncationAndError(t *testing.T) {
	t.Setenv("ZENMUX_TEST_COOKIE", "ctoken=secret")
	for _, data := range []string{"data: {\"type\":\"message_start\"}\n\n", "data: {\"type\":\"error\",\"error\":{\"message\":\"private secret\"}}\n\n"} {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { io.WriteString(w, data) }))
		c := New(config.Source{Adapter: "zenmux-web", KeyEnv: "ZENMUX_TEST_COOKIE", BaseURL: server.URL})
		_, err := c.Do(context.Background(), "chat", "model", false, []byte(`{"messages":[{"role":"user","content":"hello"}]}`), nil)
		if err == nil || strings.Contains(err.Error(), "private secret") {
			t.Fatalf("missing safe error: %v", err)
		}
		c.Close()
		server.Close()
	}
}
