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

func TestHyperAgentConfiguresThreadBeforeChat(t *testing.T) {
	t.Setenv("HYPER_TEST", "session=own-cookie")
	for _, mode := range []string{"ok", "error", "mismatch", "truncated"} {
		t.Run(mode, func(t *testing.T) {
			calls := 0
			s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls++
				if r.Header.Get("Cookie") != "session=own-cookie" || r.Header.Get("Authorization") != "" {
					t.Error("credential isolation")
				}
				var p map[string]any
				json.NewDecoder(r.Body).Decode(&p)
				switch calls {
				case 1:
					if r.Method != "POST" || r.URL.Path != "/api/threads" {
						t.Error("create")
					}
					io.WriteString(w, `{"id":"cmthread123456789012345678"}`)
				case 2:
					if r.Method != "PATCH" || p["modelId"] != "opus-latest" || p["defaultSubagentModel"] != "opus" || p["runtimeId"] != "claude-agents-sdk" || p["executionMode"] != "auto" {
						t.Error("configure")
					}
					io.WriteString(w, "{}")
				case 3:
					if r.URL.Path != "/api/threads/cmthread123456789012345678/chat" || p["content"] != "hello" || p["sessionId"] != nil || p["unifiedStream"] != true || p["modelId"] != nil {
						t.Errorf("chat %#v", p)
					}
					w.Header().Set("Content-Type", "text/event-stream")
					io.WriteString(w, "data: {\"type\":\"text\",\"content\":\"answer\"}\n\n")
					switch mode {
					case "ok":
						io.WriteString(w, "data: [DONE]\n\n")
						w.(http.Flusher).Flush()
						<-r.Context().Done()
					case "error":
						io.WriteString(w, "data: {\"type\":\"error\",\"message\":\"secret\"}\n\n")
					case "mismatch":
						io.WriteString(w, "data: {\"type\":\"thread_runtime_latched\",\"modelId\":\"wrong\"}\n\n")
					}
				default:
					t.Error("unexpected call")
				}
			}))
			defer s.Close()
			c := New(config.Source{Adapter: "hyperagent", BaseURL: s.URL, KeyEnv: "HYPER_TEST"})
			defer c.Close()
			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
			defer cancel()
			resp, err := c.Do(ctx, "chat", "opus-latest", true, []byte(`{"messages":[{"role":"user","content":"hello"}]}`), http.Header{"Authorization": {"Bearer caller"}})
			if err != nil {
				t.Fatal(err)
			}
			defer resp.Body.Close()
			b, err := io.ReadAll(resp.Body)
			if mode == "ok" {
				if err != nil || !strings.Contains(string(b), "[DONE]") || ctx.Err() != nil {
					t.Fatalf("%s %v", b, err)
				}
			} else if err == nil || strings.Contains(string(b), "[DONE]") || strings.Contains(err.Error(), "secret") {
				t.Fatalf("unexpected success %s %v", b, err)
			}
		})
	}
}
