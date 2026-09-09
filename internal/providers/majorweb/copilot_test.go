package majorweb

import (
	"clash-of-tokens/internal/config"
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"github.com/gobwas/ws"
	"github.com/gobwas/ws/wsutil"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestCopilotStartChallengeAndReplacement(t *testing.T) {
	t.Setenv("COPILOT_WEB_TEST", "own-access")
	for _, mode := range []string{"ok", "error", "truncated"} {
		t.Run(mode, func(t *testing.T) {
			s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Header.Get("Authorization") != "Bearer own-access" {
					t.Error("own auth")
				}
				if r.URL.Path == "/c/api/start" {
					var p map[string]any
					json.NewDecoder(r.Body).Decode(&p)
					if p["startNewConversation"] != true || p["teenSupportEnabled"] != false {
						t.Error("start body")
					}
					io.WriteString(w, `{"currentConversationId":"conversation-123"}`)
					return
				}
				if r.URL.Query().Get("accessToken") != "own-access" || r.URL.Query().Get("api-version") != "2" {
					t.Error("WS URL")
				}
				conn, _, _, err := ws.UpgradeHTTP(r, w)
				if err != nil {
					t.Error(err)
					return
				}
				defer conn.Close()
				read := func() map[string]any {
					raw, e := wsutil.ReadClientText(conn)
					if e != nil {
						t.Error(e)
						return nil
					}
					var p map[string]any
					json.Unmarshal(raw, &p)
					return p
				}
				p := read()
				if p["event"] != "send" || p["conversationId"] != "conversation-123" || p["mode"] != "reasoning" {
					t.Errorf("send %#v", p)
				}
				wsutil.WriteServerText(conn, []byte(`{"event":"challenge","method":"hashcash","parameter":"fixture:1"}`))
				p = read()
				token, _ := p["token"].(string)
				sum := sha256.Sum256([]byte("fixture:" + token))
				if p["event"] != "challengeResponse" || fmt.Sprintf("%x", sum)[0] != '0' {
					t.Error("invalid proof")
				}
				if read()["event"] != "send" {
					t.Error("resend")
				}
				wsutil.WriteServerText(conn, []byte(`{"event":"appendText","text":"draft"}`))
				switch mode {
				case "ok":
					wsutil.WriteServerText(conn, []byte(`{"event":"replaceText","text":"correct answer"}`))
					wsutil.WriteServerText(conn, []byte(`{"event":"done"}`))
				case "error":
					wsutil.WriteServerText(conn, []byte(`{"event":"error","error":"secret upstream"}`))
				}
			}))
			defer s.Close()
			c := New(config.Source{Adapter: "copilot-web", BaseURL: s.URL, KeyEnv: "COPILOT_WEB_TEST"})
			defer c.Close()
			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
			defer cancel()
			resp, err := c.Do(ctx, "chat", "copilot-think", false, []byte(`{"messages":[{"role":"user","content":"hello"}]}`), http.Header{"Authorization": {"Bearer caller"}})
			if mode != "ok" {
				if err == nil {
					resp.Body.Close()
					t.Fatal("expected failure")
				}
				if strings.Contains(err.Error(), "secret") {
					t.Fatal("leak")
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			defer resp.Body.Close()
			data, _ := io.ReadAll(resp.Body)
			if !strings.Contains(string(data), "correct answer") || strings.Contains(string(data), "draft") {
				t.Fatalf("%s", data)
			}
		})
	}
}

func TestCopilotHashcashBoundsAndCancellation(t *testing.T) {
	if _, err := copilotHashcash(context.Background(), "x", 100); err == nil {
		t.Fatal("unbounded difficulty")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := copilotHashcash(ctx, "x", 1); err != context.Canceled {
		t.Fatal(err)
	}
}
