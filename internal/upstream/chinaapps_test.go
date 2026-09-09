package upstream

import (
	"clash-of-tokens/catalog"
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestChinaAppCatalogDispatch(t *testing.T) {
	for _, id := range []string{"tencent-ima", "weread-ai"} {
		t.Run(id, func(t *testing.T) {
			up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				switch r.URL.Path {
				case "/cgi-bin/session_logic/init_session":
					io.WriteString(w, `{"code":0,"session_id":"s"}`)
				case "/cgi-bin/assistant/qa":
					w.Header().Set("Content-Type", "text/event-stream")
					io.WriteString(w, "event: DELTA\ndata: {\"text\":\"answer\"}\n\nevent: COMPLETED\ndata: {}\n\n")
				case "/ai/chatv2":
					io.WriteString(w, `{"result":{"text":"answer","has_more":0}}`)
				default:
					t.Error("generic route used", r.URL.Path)
					w.WriteHeader(404)
				}
			}))
			defer up.Close()
			s, e := catalog.Preset(id, "glm-5.2", up.URL)
			if e != nil {
				t.Fatal(e)
			}
			s.Project = "book"
			if id == "tencent-ima" {
				t.Setenv(s.KeyEnv, "IMA-TOKEN=test")
			} else {
				t.Setenv(s.KeyEnv, `{"vid":"reader","access_token":"test"}`)
			}
			c := New(s)
			defer c.Close()
			resp, e := c.Do(context.Background(), "chat", s.Models[0].Upstream, false, []byte(`{"messages":[{"role":"user","content":"question"}]}`), nil)
			if e != nil {
				t.Fatal(e)
			}
			defer resp.Body.Close()
			data, _ := io.ReadAll(resp.Body)
			if !strings.Contains(string(data), "answer") {
				t.Fatal(string(data))
			}
		})
	}
}
