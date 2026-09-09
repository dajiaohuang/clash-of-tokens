package embedded

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"clash-of-tokens/internal/config"
)

func TestWordPressFlow(t *testing.T) {
	for _, adapter := range []string{AdapterChatAIGPT, AdapterChatGPTFree} {
		t.Run(adapter, func(t *testing.T) {
			t.Setenv("COT_WP_TEST", `{"nonce":"test-nonce","cookie":"test_cookie=abc","session_id":"explicit-session"}`)
			calls := 0
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls++
				if r.URL.Path != "/wp-admin/admin-ajax.php" {
					t.Errorf("unexpected path %s", r.URL.Path)
				}
				if r.Header.Get("Cookie") != "test_cookie=abc" {
					t.Error("wrong credentials")
				}
				if r.Method == "POST" {
					if adapter == AdapterChatAIGPT {
						if err := r.ParseMultipartForm(1 << 20); err != nil {
							t.Fatal(err)
						}
					} else {
						r.ParseForm()
					}
					for k, v := range map[string]string{"action": "aipkit_cache_sse_message", "message": "hello 中文", "_ajax_nonce": "test-nonce", "bot_id": "42"} {
						if r.FormValue(k) != v {
							t.Errorf("wrong %s", k)
						}
					}
					w.Header().Set("Content-Type", "application/json")
					io.WriteString(w, `{"success":true,"data":{"cache_key":"cache-test"}}`)
					return
				}
				q := r.URL.Query()
				for k, v := range map[string]string{"action": "aipkit_frontend_chat_stream", "cache_key": "cache-test", "bot_id": "42", "post_id": "123", "_ajax_nonce": "test-nonce", "session_id": "explicit-session"} {
					if q.Get(k) != v {
						t.Errorf("wrong query %s", k)
					}
				}
				if q.Get("conversation_uuid") == "" {
					t.Error("missing isolated conversation")
				}
				w.Header().Set("Content-Type", "text/event-stream")
				io.WriteString(w, "data: {\"delta\":\"hello \"}\n\ndata: {\"delta\":\"世界\",\"finished\":true}\n\n")
			}))
			defer srv.Close()
			c := New(config.Source{Adapter: adapter, BaseURL: srv.URL, KeyEnv: "COT_WP_TEST", Project: "123"})
			defer c.Close()
			resp, err := c.Do(context.Background(), "chat", "42", false, []byte(`{"messages":[{"role":"user","content":"hello 中文"}]}`), http.Header{"Cookie": {"caller-secret"}})
			if err != nil {
				t.Fatal(err)
			}
			defer resp.Body.Close()
			data, err := io.ReadAll(resp.Body)
			if err != nil {
				t.Fatal(err)
			}
			var value struct {
				Choices []struct {
					Message struct {
						Content string `json:"content"`
					} `json:"message"`
				} `json:"choices"`
			}
			if json.Unmarshal(data, &value) != nil || len(value.Choices) != 1 || value.Choices[0].Message.Content != "hello 世界" {
				t.Fatalf("unexpected output %s", data)
			}
			if calls != 2 {
				t.Fatalf("unexpected call count %d", calls)
			}
			if strings.Contains(string(data), "usage") {
				t.Fatal("fabricated usage")
			}
		})
	}
}

func TestEventOnlyCompletion(t *testing.T) {
	d := newSSEDecoder(strings.NewReader("event: done\n\n"))
	event, data, ok, err := d.next()
	if err != nil || !ok || event != "done" || data != "" {
		t.Fatalf("lost event-only completion %q %q %v %v", event, data, ok, err)
	}
}
