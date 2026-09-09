package majorweb

import (
	"clash-of-tokens/internal/config"
	"context"
	"encoding/json"
	"github.com/gobwas/ws"
	"github.com/gobwas/ws/wsutil"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestUCPersonaMintAndAuthoritativeAnswer(t *testing.T) {
	t.Setenv("UC_TEST", `{"cookie":"__client=own-client","sid":"sess_own","uid":"user_own"}`)
	for _, mode := range []string{"ok", "error", "truncated"} {
		t.Run(mode, func(t *testing.T) {
			s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == "/v1/client/sessions/sess_own/tokens" {
					if r.Header.Get("Cookie") != "__client=own-client" || r.Header.Get("Origin") != "https://uncensored.com" || r.URL.Query().Get("_clerk_js_version") != "5.127.1" {
						t.Error("mint")
					}
					io.WriteString(w, `{"jwt":"own-session-jwt"}`)
					return
				}
				if r.URL.Path != "/ws/user_own" || r.URL.Query().Get("token") != "own-session-jwt" || r.Header.Get("Cookie") != "" || r.Header.Get("Authorization") != "" || r.Header.Get("Origin") != "https://uncensored.com" {
					t.Error("upgrade")
				}
				conn, _, _, err := ws.UpgradeHTTP(r, w)
				if err != nil {
					t.Error(err)
					return
				}
				defer conn.Close()
				raw, err := wsutil.ReadClientText(conn)
				if err != nil {
					t.Error(err)
					return
				}
				var p map[string]any
				json.Unmarshal(raw, &p)
				if p["text"] != "hello" || p["model"] != "persona-model" || p["user_identifier"] != "user_own" || p["app_version"] != "1.0.0-web" || p["max_tokens"] != nil || p["direct_params"] != nil || p["chat_mode"] != "chat" {
					t.Errorf("persona %#v", p)
				}
				wsutil.WriteServerText(conn, []byte("{\"message_type\":\"text\",\"text\":\"draft\"}\n"))
				switch mode {
				case "ok":
					wsutil.WriteServerText(conn, []byte(`{"message_type":"text","end_of_stream":true,"raw_text":"authoritative answer"}`))
				case "error":
					wsutil.WriteServerText(conn, []byte(`{"type":"error","code":"message_limit_exceeded","message":"secret"}`))
				}
			}))
			defer s.Close()
			c := New(config.Source{Adapter: "uc-web", BaseURL: s.URL, KeyEnv: "UC_TEST"})
			defer c.Close()
			original := c.http.Transport
			c.http.Transport = innerTransport(func(r *http.Request) (*http.Response, error) {
				if r.URL.Host == "clerk.uncensored.com" {
					cp := r.Clone(r.Context())
					u := *cp.URL
					u.Scheme = "http"
					u.Host = strings.TrimPrefix(s.URL, "http://")
					cp.URL = &u
					return original.RoundTrip(cp)
				}
				return original.RoundTrip(r)
			})
			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
			defer cancel()
			resp, err := c.Do(ctx, "chat", "persona-model", true, []byte(`{"messages":[{"role":"user","content":"hello"}]}`), http.Header{"Authorization": {"Bearer caller"}})
			if mode != "ok" {
				if err == nil {
					resp.Body.Close()
					t.Fatal("expected error")
				}
				if strings.Contains(err.Error(), "secret") {
					t.Fatal("leaked")
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			defer resp.Body.Close()
			b, err := io.ReadAll(resp.Body)
			if err != nil || !strings.Contains(string(b), "authoritative answer") || strings.Contains(string(b), "draft") || resp.Header.Get("X-COT-Delivery") != "buffered" {
				t.Fatalf("%s %v", b, err)
			}
		})
	}
}
