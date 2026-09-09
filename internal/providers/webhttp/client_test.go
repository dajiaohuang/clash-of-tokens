package webhttp

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"clash-of-tokens/internal/config"
)

func TestWebsiteReferenceContracts(t *testing.T) {
	for _, adapter := range []string{"venice-web", "inkeep", "gptanon", "perfectassistant"} {
		for _, stream := range []bool{true, false} {
			t.Run(adapter+map[bool]string{true: "/stream", false: "/json"}[stream], func(t *testing.T) {
				t.Setenv("WEB_TEST_KEY", "provider-credential")
				server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					if r.Header.Get("X-Private-Client") != "" {
						t.Error("client header leaked")
					}
					var body map[string]any
					_ = json.NewDecoder(r.Body).Decode(&body)
					switch adapter {
					case "venice-web":
						if r.URL.Path != "/api/chat" || r.Header.Get("Cookie") != "provider-credential" || body["max_tokens"] != float64(4096) || body["model"] != "actual-model" {
							t.Error("Venice wire mismatch")
						}
					case "inkeep":
						if r.URL.Path != "/v1/chat/completions" || r.Header.Get("Authorization") != "Bearer provider-credential" || body["stream"] != true {
							t.Error("Inkeep wire mismatch")
						}
					case "gptanon":
						if r.URL.Path != "/api/chat/stream" || body["message"] != "hello" || body["deepSearchEnabled"] != false || body["modelIds"].([]any)[0] != "actual-model" {
							t.Error("GPTAnon wire mismatch")
						}
					case "perfectassistant":
						if r.URL.Path != "/ai/free" || body["text"] != "hello" || body["id"] != "actual-model" || body["chatId"] == "" || r.Header.Get("Content-Type") != "text/plain;charset=UTF-8" {
							t.Error("Perfect Assistant wire mismatch")
						}
						w.Header().Set("Content-Type", "application/json")
						_, _ = io.WriteString(w, `{"response":"你好 🌍"}`)
						return
					}
					w.Header().Set("Content-Type", "text/event-stream")
					if adapter == "gptanon" {
						_, _ = io.WriteString(w, "data: {\"type\":\"token\",\"token\":\"你好 🌍\"}\n\ndata: {\"type\":\"done\"}\n\n")
					} else {
						_, _ = io.WriteString(w, "data: {\"choices\":[{\"index\":0,\"delta\":{\"content\":\"你好 🌍\"},\"finish_reason\":null}]}\n\ndata: {\"choices\":[{\"index\":0,\"delta\":{},\"finish_reason\":\"stop\"}]}\n\ndata: [DONE]\n\n")
					}
				}))
				defer server.Close()
				client := New(config.Source{Adapter: adapter, BaseURL: server.URL, KeyEnv: "WEB_TEST_KEY", MaxInflight: 1})
				defer client.Close()
				r, e := client.Do(context.Background(), "chat", "actual-model", stream, []byte(`{"model":"ignored","messages":[{"role":"user","content":"hello"}]}`), http.Header{"X-Private-Client": []string{"secret"}, "Cookie": []string{"client-secret"}})
				if e != nil {
					t.Fatal(e)
				}
				b, e := io.ReadAll(r.Body)
				r.Body.Close()
				if e != nil || !strings.Contains(string(b), "你好 🌍") {
					t.Fatalf("%s %v", b, e)
				}
				if stream && !strings.Contains(string(b), "data: [DONE]") {
					t.Fatal("missing completion")
				}
				if !stream {
					var response map[string]any
					if json.Unmarshal(b, &response) != nil || response["object"] != "chat.completion" || response["usage"] != nil {
						t.Fatal("invalid completion or fabricated usage")
					}
				}
			})
		}
	}
}
func TestRejectLostSemanticsBeforeNetwork(t *testing.T) {
	var calls atomic.Int32
	s := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { calls.Add(1) }))
	defer s.Close()
	c := New(config.Source{Adapter: "gptanon", BaseURL: s.URL, Anonymous: true, MaxInflight: 1})
	defer c.Close()
	for _, body := range []string{`{"messages":[{"role":"user","content":"x"}],"tools":[]}`, `{"messages":[{"role":"system","content":"rules"},{"role":"user","content":"x"}]}`, `{"messages":[{"role":"user","content":"x","name":"a"}]}`, `{"messages":[{"role":"user","content":[{"type":"image_url","image_url":"x"}]}]}`} {
		if _, e := c.Do(context.Background(), "chat", "m", false, []byte(body), nil); e == nil {
			t.Fatal("unsupported request accepted")
		}
	}
	if calls.Load() != 0 {
		t.Fatal("unsupported input sent upstream")
	}
}
func TestTruncatedAndErrorStreamsDoNotFinish(t *testing.T) {
	for _, wire := range []string{"data: {\"choices\":[{\"delta\":{\"content\":\"partial\"}}]}\n\n", "data: {\"error\":{\"message\":\"sensitive upstream error\"}}\n\n", "data: {bad}\n\n"} {
		t.Run(wire, func(t *testing.T) {
			r := output("m", true, "upstream", func(emit func(string) error) (string, error) {
				return decode(strings.NewReader(wire), "venice-web", emit)
			}, nil)
			b, e := io.ReadAll(r.Body)
			r.Body.Close()
			if e == nil || strings.Contains(string(b), "[DONE]") || strings.Contains(string(b), "sensitive upstream error") {
				t.Fatalf("%s %v", b, e)
			}
		})
	}
}
func TestCloseCancelsUpstream(t *testing.T) {
	ended := make(chan struct{})
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.(http.Flusher).Flush()
		<-r.Context().Done()
		close(ended)
	}))
	defer s.Close()
	c := New(config.Source{Adapter: "venice-web", BaseURL: s.URL, Local: true, MaxInflight: 1})
	defer c.Close()
	r, e := c.Do(context.Background(), "chat", "m", true, []byte(`{"messages":[{"role":"user","content":"hello"}]}`), nil)
	if e != nil {
		t.Fatal(e)
	}
	r.Body.Close()
	select {
	case <-ended:
	case <-time.After(time.Second):
		t.Fatal("upstream did not cancel")
	}
}
func TestGPTAnonCompleteMustMatchPriorDeltas(t *testing.T) {
	wire := "data: {\"type\":\"token\",\"token\":\"a\"}\n\ndata: {\"type\":\"complete\",\"content\":\"b\"}\n\n"
	if _, e := decode(strings.NewReader(wire), "gptanon", func(string) error { return nil }); e == nil {
		t.Fatal("changed text accepted")
	}
}
