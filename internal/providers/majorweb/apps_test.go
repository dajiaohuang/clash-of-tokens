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

func TestAppContracts(t *testing.T) {
	for _, adapter := range []string{"merlin", "sider"} {
		t.Run(adapter, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method != "POST" || r.Header.Get("Authorization") != "Bearer own-token" {
					t.Error("wrong request authentication")
				}
				var p map[string]any
				if json.NewDecoder(r.Body).Decode(&p) != nil {
					t.Error("invalid payload")
				}
				if p["model"] != "exact-model" {
					t.Error("wrong model")
				}
				if adapter == "merlin" {
					if r.URL.Path != "/v1/thread/unified" || r.Header.Get("x-merlin-version") != "web-merlin" {
						t.Error("wrong Merlin request")
					}
					m, _ := p["message"].(map[string]any)
					if m["content"] != "hello" || m["context"] != "" || m["parentId"] != "root" {
						t.Error("wrong Merlin message")
					}
					io.WriteString(w, "data: {\"data\":{\"content\":\"你好 \"}}\n\ndata: {\"data\":{\"content\":\"world\"}}\n\n")
				} else {
					if r.URL.Path != "/api/chat/v1/completions" || r.Header.Get("X-App-Name") != "ChitChat_Edge_Ext" || p["cid"] != "" {
						t.Error("wrong Sider request")
					}
					m, _ := p["multi_content"].([]any)
					if len(m) != 1 || m[0].(map[string]any)["text"] != "hello" {
						t.Error("wrong Sider message")
					}
					tools := p["tools"].(map[string]any)
					if len(tools["auto"].([]any)) != 0 {
						t.Error("unexpected tools")
					}
					io.WriteString(w, "data: {\"code\":0,\"data\":{\"type\":\"message_start\",\"model\":\"exact-model\"}}\n\ndata: {\"code\":0,\"data\":{\"type\":\"text\",\"model\":\"exact-model\",\"text\":\"你好 world\"}}\n\ndata: [DONE]\n\n")
				}
			}))
			defer srv.Close()
			t.Setenv("COT_TEST_APP", "own-token")
			c := New(config.Source{Adapter: adapter, BaseURL: srv.URL, KeyEnv: "COT_TEST_APP"})
			defer c.Close()
			r, e := c.Do(context.Background(), "chat", "exact-model", false, []byte(`{"messages":[{"role":"user","content":"hello"}]}`), http.Header{"Authorization": {"Bearer caller-secret"}})
			if e != nil {
				t.Fatal(e)
			}
			defer r.Body.Close()
			b, _ := io.ReadAll(r.Body)
			if !strings.Contains(string(b), "你好 world") {
				t.Fatal(string(b))
			}
		})
	}
}

func TestAppsRejectBrokenStreams(t *testing.T) {
	for _, raw := range []string{"data: {broken}\n\n", "data: {\"error\":\"secret\"}\n\n", "data: {\"data\":{\"content\":1}}\n\n"} {
		if _, e := readMerlin(strings.NewReader(raw)); e == nil {
			t.Fatal("accepted Merlin error")
		}
	}
	for _, raw := range []string{"data: {\"code\":0,\"data\":{\"type\":\"text\",\"text\":\"partial\"}}\n\n", "data: {\"code\":401,\"msg\":\"secret\"}\n\n", "data: {\"code\":0,\"data\":{\"type\":\"text\",\"model\":\"wrong-model\",\"text\":\"x\"}}\n\ndata: [DONE]\n\n", "data: {\"code\":0,\"data\":{\"type\":\"tool_call_start\"}}\n\n"} {
		if _, _, _, e := readSider(strings.NewReader(raw), "m"); e == nil {
			t.Fatal("accepted Sider error")
		} else if strings.Contains(e.Error(), "secret") {
			t.Fatal("leaked details")
		}
	}
}
