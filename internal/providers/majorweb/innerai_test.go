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

type innerTransport func(*http.Request) (*http.Response, error)

func (f innerTransport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestInnerAIExactCatalogAndStream(t *testing.T) {
	t.Setenv("INNER_TEST", `{"token":"own-token","email":"own@example.test","device_id":"own-device"}`)
	for _, mode := range []string{"ok", "missing", "error", "truncated"} {
		t.Run(mode, func(t *testing.T) {
			chatCalls := 0
			s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Header.Get("Cookie") != "token=own-token" || r.Header.Get("User-Token") != "own-token" || r.Header.Get("User-Email") != "own@example.test" || r.Header.Get("Device-Id") != "own-device" || r.Header.Get("Authorization") != "" {
					t.Error("credential headers")
				}
				if r.URL.Path == "/api/v1/ai_models" {
					if mode == "missing" {
						io.WriteString(w, `[{"id":"other-uuid","llm_model":"other"}]`)
					} else {
						io.WriteString(w, `{"data":[{"id":"model-uuid","llm_model":"exact-model","enable":true}]}`)
					}
					return
				}
				if r.URL.Path != "/chat" {
					t.Error("path")
				}
				chatCalls++
				var p map[string]any
				json.NewDecoder(r.Body).Decode(&p)
				m, _ := p["ai_model"].(map[string]any)
				if m["id"] != "model-uuid" || m["llm_model"] != "exact-model" || p["message"] != "hello" || p["context_type"] != "no_context" || p["temporary"] != true {
					t.Errorf("body %#v", p)
				}
				w.Header().Set("Content-Type", "text/event-stream")
				io.WriteString(w, "data: {\"type\":\"text\",\"item\":\"answer\"}\n\n")
				if mode == "error" {
					io.WriteString(w, "data: {\"type\":\"missing_credits\",\"item\":\"secret\"}\n\n")
					return
				}
				if mode == "truncated" {
					return
				}
				io.WriteString(w, "data: {\"type\":\"end_stream\",\"item\":\"end\"}\n\n")
				w.(http.Flusher).Flush()
				<-r.Context().Done()
			}))
			defer s.Close()
			c := New(config.Source{Adapter: "inner-ai", BaseURL: s.URL, KeyEnv: "INNER_TEST"})
			defer c.Close()
			original := c.http.Transport
			c.http.Transport = innerTransport(func(r *http.Request) (*http.Response, error) {
				if r.URL.Host == "platformapi.innerai.com" {
					copy := r.Clone(r.Context())
					u := *copy.URL
					u.Scheme = "http"
					u.Host = strings.TrimPrefix(s.URL, "http://")
					copy.URL = &u
					return original.RoundTrip(copy)
				}
				return original.RoundTrip(r)
			})
			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
			defer cancel()
			resp, err := c.Do(ctx, "chat", "exact-model", false, []byte(`{"messages":[{"role":"user","content":"hello"}]}`), http.Header{"Authorization": {"Bearer caller"}})
			if mode != "ok" {
				if err == nil {
					resp.Body.Close()
					t.Fatal("expected failure")
				}
				if strings.Contains(err.Error(), "secret") {
					t.Fatal("leaked upstream")
				}
				if mode == "missing" && chatCalls != 0 {
					t.Fatal("sent unmatched model")
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			defer resp.Body.Close()
			b, err := io.ReadAll(resp.Body)
			if err != nil || !strings.Contains(string(b), "answer") || ctx.Err() != nil {
				t.Fatalf("%s %v", b, err)
			}
		})
	}
}
