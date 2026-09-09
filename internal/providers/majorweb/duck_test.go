package majorweb

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"clash-of-tokens/internal/config"
)

func TestDuckParser(t *testing.T) {
	a, err := readDuck(strings.NewReader("data: {\"message\":\"hello \"}\n\ndata: {\"content\":\"world\"}\n\ndata: [DONE]\n\n"))
	if err != nil || a != "hello world" {
		t.Fatalf("%q %v", a, err)
	}
	for _, s := range []string{"data: [DONE]\n\n", "data: {\"message\":\"partial\"}\n\n", "data: {\"error\":\"secret\"}\n\n", "data: bad\n\n"} {
		if _, err := readDuck(strings.NewReader(s)); err == nil {
			t.Fatal("accepted invalid response")
		} else if strings.Contains(err.Error(), "secret") {
			t.Fatal("leaked upstream error")
		}
	}
}

func TestDuckBrowserFixture(t *testing.T) {
	cdp := os.Getenv("COT_TEST_CDP")
	if cdp == "" {
		t.Skip("set COT_TEST_CDP for isolated browser fixture")
	}
	calls := 0
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/chat" {
			w.Header().Set("Content-Type", "text/html")
			io.WriteString(w, `<textarea></textarea><button aria-label="Ask" onclick="fetch('/duckchat/v1/chat',{method:'POST',body:JSON.stringify({model:'actual-product-model',messages:[{role:'user',content:document.querySelector('textarea').value}]})})">Ask</button>`)
			return
		}
		if r.URL.Path != "/duckchat/v1/chat" {
			http.NotFound(w, r)
			return
		}
		calls++
		raw, _ := io.ReadAll(r.Body)
		if !strings.Contains(string(raw), "fixture query") {
			t.Error("missing prompt")
		}
		w.Header().Set("Content-Type", "text/event-stream")
		io.WriteString(w, "data: {\"message\":\"fixture answer\"}\n\ndata: [DONE]\n\n")
	}))
	defer s.Close()
	c := New(config.Source{Adapter: "duckduckgo-web", BaseURL: s.URL}, config.Browser{Enabled: true, CDPURL: cdp})
	defer c.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	r, err := c.Do(ctx, "chat", "web", false, []byte(`{"messages":[{"role":"user","content":"fixture query"}]}`), nil)
	if err != nil {
		t.Fatalf("%v (fixture calls=%d)", err, calls)
	}
	defer r.Body.Close()
	raw, _ := io.ReadAll(r.Body)
	if !strings.Contains(string(raw), "fixture answer") || !strings.Contains(string(raw), "actual-product-model") {
		t.Fatal(string(raw))
	}
}
