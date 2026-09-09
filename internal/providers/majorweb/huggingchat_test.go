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

func TestHuggingChatProductConversation(t *testing.T) {
	t.Setenv("HUGGING_TEST", "hf-chat=own-cookie")
	for _, stream := range []bool{false, true} {
		t.Run(map[bool]string{false: "buffered", true: "stream"}[stream], func(t *testing.T) {
			count := 0
			s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				count++
				if r.Header.Get("Cookie") != "hf-chat=own-cookie" || r.Header.Get("Authorization") != "" {
					t.Error("credential isolation")
				}
				switch r.URL.Path {
				case "/chat/conversation":
					var p map[string]string
					json.NewDecoder(r.Body).Decode(&p)
					if r.Method != "POST" || p["model"] != "org/exact-model" {
						t.Error("creation contract")
					}
					io.WriteString(w, `{"conversationId":"conv-123"}`)
				case "/chat/api/v2/conversations/conv-123":
					io.WriteString(w, `{"json":{"messages":[{"id":"message-456"}]}}`)
				case "/chat/conversation/conv-123":
					if err := r.ParseMultipartForm(1 << 20); err != nil {
						t.Error(err)
						return
					}
					defer r.MultipartForm.RemoveAll()
					var p map[string]any
					json.Unmarshal([]byte(r.FormValue("data")), &p)
					if p["inputs"] != "hello" || p["id"] != "message-456" || p["is_retry"] != false || p["web_search"] != false {
						t.Errorf("settings: %#v", p)
					}
					w.Header().Set("Content-Type", "application/x-ndjson")
					io.WriteString(w, "{\"type\":\"stream\",\"token\":\"hi\\u0000\"}\n{\"type\":\"finalAnswer\"}\n")
					w.(http.Flusher).Flush()
					<-r.Context().Done()
				default:
					t.Error("unexpected endpoint")
					http.NotFound(w, r)
				}
			}))
			defer s.Close()
			c := New(config.Source{Adapter: "huggingchat", BaseURL: s.URL, KeyEnv: "HUGGING_TEST"})
			defer c.Close()
			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
			defer cancel()
			resp, err := c.Do(ctx, "chat", "org/exact-model", stream, []byte(`{"messages":[{"role":"user","content":"hello"}]}`), http.Header{"Authorization": {"Bearer caller"}})
			if err != nil {
				t.Fatal(err)
			}
			defer resp.Body.Close()
			data, err := io.ReadAll(resp.Body)
			if err != nil {
				t.Fatal(err)
			}
			if !strings.Contains(string(data), "hi") || count != 3 {
				t.Fatalf("response %s count %d", data, count)
			}
			if ctx.Err() != nil {
				t.Fatal("waited for EOF instead of finalAnswer")
			}
		})
	}
}

func TestHuggingChatRejectsTruncationAndInbandError(t *testing.T) {
	for _, tail := range []string{"", `{"type":"error","error":"secret upstream"}`} {
		body := huggingChatStream(io.NopCloser(strings.NewReader("{\"type\":\"stream\",\"token\":\"partial\"}\n"+tail)), "id", "m")
		b, err := io.ReadAll(body)
		body.Close()
		if err == nil || strings.Contains(string(b), "[DONE]") || strings.Contains(err.Error(), "secret upstream") {
			t.Fatalf("data=%s err=%v", b, err)
		}
	}
}
