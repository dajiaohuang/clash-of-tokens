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
	"time"
)

func TestArenaContract(t *testing.T) {
	model := "12345678-1234-4234-8234-123456789abc"
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "POST" || r.URL.Path != "/nextjs-api/stream/create-evaluation" || r.Header.Get("Cookie") != "own=cookie" {
			t.Error("wrong request")
		}
		var p map[string]any
		if json.NewDecoder(r.Body).Decode(&p) != nil {
			t.Error("invalid payload")
		}
		if p["mode"] != "direct-battle" || p["modelAId"] != model || p["recaptchaV3Token"] != "own-token" {
			t.Error("wrong selection")
		}
		for _, k := range []string{"id", "userMessageId", "modelAMessageId"} {
			id, _ := p[k].(string)
			if len(id) != 36 || id[14] != '7' {
				t.Error("expected UUIDv7")
			}
		}
		msg, _ := p["userMessage"].(map[string]any)
		if msg["content"] != "hello" {
			t.Error("wrong prompt")
		}
		io.WriteString(w, "b0:\"other model\"\na0:\"answer\"\ne:{\"finishReason\":\"stop\"}\nad:{\"finishReason\":\"length\"}\n")
		w.(http.Flusher).Flush()
		<-r.Context().Done()
	}))
	defer srv.Close()
	t.Setenv("COT_TEST_ARENA", `{"cookie":"own=cookie","recaptchaV3Token":"own-token"}`)
	c := New(config.Source{Adapter: "arena", BaseURL: srv.URL, KeyEnv: "COT_TEST_ARENA"})
	defer c.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	r, e := c.Do(ctx, "chat", model, false, []byte(`{"messages":[{"role":"user","content":"hello"}]}`), nil)
	if e != nil {
		t.Fatal(e)
	}
	defer r.Body.Close()
	b, _ := io.ReadAll(r.Body)
	if !strings.Contains(string(b), `"content":"answer"`) || !strings.Contains(string(b), `"finish_reason":"length"`) || strings.Contains(string(b), "other model") {
		t.Fatal(string(b))
	}
}

func TestArenaInvalidFrames(t *testing.T) {
	for _, s := range []string{"0:\"partial\"\n", "0:\"partial\"\ne:{\"finishReason\":\"stop\"}\n", "3:\"secret\"\n", "ae:\"secret\"\n", "0:{\"wrong\":1}\nd:{\"finishReason\":\"stop\"}\n", "0:\"x\"\nd:{\"finishReason\":\"error\"}\n", "0:\"x\"\nd:{}\n", "0:\"x\"\nbd:{\"finishReason\":\"stop\"}\n", "0:\"x\"\nd:{", "0:\"x\"\ne:{\"error\":\"secret\"}\n"} {
		if _, _, e := readArena(strings.NewReader(s)); e == nil {
			t.Fatalf("accepted %q", s)
		} else if strings.Contains(e.Error(), "secret") {
			t.Fatal("leaked upstream details")
		}
	}
}
