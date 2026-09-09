package webhttp

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"clash-of-tokens/internal/config"
)

func TestToolbazExistingTokenOnly(t *testing.T) {
	t.Setenv("COT_TOOLBAZ_TEST", `{"session_id":"existing-session","token":"issued-token"}`)
	calls := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if r.Method != "POST" || r.URL.Path != "/writing.php" {
			t.Error("unexpected endpoint, must not create verification token")
		}
		r.ParseForm()
		for k, v := range map[string]string{"text": "hello", "model": "model-test", "session_id": "existing-session", "capcha": "issued-token"} {
			if r.Form.Get(k) != v {
				t.Errorf("wrong %s", k)
			}
		}
		cookie, err := r.Cookie("SessionID")
		if err != nil || cookie.Value != "existing-session" {
			t.Error("wrong session cookie")
		}
		w.Header().Set("Content-Type", "text/plain")
		io.WriteString(w, " hello<br>world &amp; friends ")
	}))
	defer srv.Close()
	c := New(config.Source{Adapter: "toolbaz", BaseURL: srv.URL, KeyEnv: "COT_TOOLBAZ_TEST"})
	defer c.Close()
	r, err := c.Do(context.Background(), "chat", "model-test", true, []byte(`{"messages":[{"role":"user","content":"hello"}]}`), http.Header{"Cookie": {"caller-secret"}})
	if err != nil {
		t.Fatal(err)
	}
	defer r.Body.Close()
	data, err := io.ReadAll(r.Body)
	if err != nil || !strings.Contains(string(data), `hello\nworld`) || !strings.HasSuffix(string(data), "data: [DONE]\n\n") {
		t.Fatalf("bad buffered SSE %s %v", data, err)
	}
	if calls != 1 || r.Header.Get("X-COT-Delivery") != "buffered" {
		t.Fatal("unexpected delivery")
	}
}
