package majorweb

import (
	"clash-of-tokens/internal/config"
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"
)

func TestEaseMateEnvelopeErrors(t *testing.T) {
	valid := "data: {\"code\":200,\"data\":\"{\\\"answer\\\":\\\"hello\\\"}\"}\n\n"
	if s, err := easeAnswer(valid); err != nil || s != "hello" {
		t.Fatalf("%s %v", s, err)
	}
	for _, raw := range []string{"", valid + "data: {\"code\":403,\"data\":\"secret\"}\n\n", valid + "data: invalid\n\n"} {
		if _, err := easeAnswer(raw); err == nil || strings.Contains(err.Error(), "secret") {
			t.Fatalf("error %v", err)
		}
	}
}

func TestEaseMateBrowserFixture(t *testing.T) {
	cdp := os.Getenv("COT_TEST_CDP")
	if cdp == "" {
		t.Skip("set COT_TEST_CDP for local browser fixture")
	}
	for _, transport := range []string{"fetch", "xhr"} {
		t.Run(transport, func(t *testing.T) {
			calls := 0
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == "/api2/stream/exec_operation" {
					t.Log("fixture endpoint received", r.Method)
					calls++
					data, _ := io.ReadAll(r.Body)
					if !strings.Contains(string(data), "fixture prompt") {
						t.Error("prompt not forwarded")
					}
					w.Header().Set("Content-Type", "text/event-stream")
					io.WriteString(w, "data: {\"code\":200,\"data\":\"{\\\"answer\\\":\\\"fixture reply\\\"}\"}\n\n")
					return
				}
				w.Header().Set("Content-Type", "text/html;charset=utf-8")
				send := `fetch('/api2/stream/exec_operation',{method:'POST',body:document.querySelector('textarea').value})`
				if transport == "xhr" {
					send = `(()=>{const x=new XMLHttpRequest();x.open('POST','/api2/stream/exec_operation');x.send(document.querySelector('textarea').value)})()`
				}
				io.WriteString(w, `<!doctype html><html><body><div class="model-select-active"><span class="text-sm">old</span></div><div class="model-item"><span onclick="document.querySelector('.text-sm').textContent='Exact Model'">Exact Model</span></div><textarea placeholder="Ask me anything…"></textarea><button class="css-1wchz4a" onclick="`+send+`">Send</button></body></html>`)
			}))
			defer server.Close()
			c := New(config.Source{Adapter: "easemate", BaseURL: server.URL}, config.Browser{Enabled: true, CDPURL: cdp})
			defer c.Close()
			ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
			defer cancel()
			resp, err := c.Do(ctx, "chat", "Exact Model", false, []byte(`{"messages":[{"role":"user","content":"fixture prompt"}]}`), nil)
			if err != nil {
				t.Fatalf("%v; upstream calls=%d", err, calls)
			}
			defer resp.Body.Close()
			b, err := io.ReadAll(resp.Body)
			if err != nil || calls != 1 || !strings.Contains(string(b), "fixture reply") {
				t.Fatalf("%s %v calls=%d", b, err, calls)
			}
		})
	}
}
