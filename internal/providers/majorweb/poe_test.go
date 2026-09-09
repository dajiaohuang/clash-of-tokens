package majorweb

import (
	"context"
	"crypto/md5"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"clash-of-tokens/internal/config"
	"github.com/gobwas/ws"
	"github.com/gobwas/ws/wsutil"
)

func poeTestFrame(id, state, text string) []byte {
	inner, _ := json.Marshal(map[string]any{"message_type": "subscriptionUpdate", "payload": map[string]any{"subscription_name": "messageAdded", "unique_id": "messageAdded:" + id, "data": map[string]any{"messageAdded": map[string]string{"author": "bot", "text": text, "state": state}}}})
	out, _ := json.Marshal(map[string]any{"messages": []string{string(inner)}})
	return out
}

func TestPoeSignedSubscriptionContract(t *testing.T) {
	for _, mode := range []string{"ok", "truncated", "error"} {
		t.Run(mode, func(t *testing.T) {
			trigger := make(chan struct{})
			s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if strings.HasPrefix(r.URL.Path, "/up/") {
					if r.URL.Path != "/up/box/updates" || r.URL.Query().Get("channel") != "channel" || r.Header.Get("Cookie") != "" {
						t.Error("wrong channel request")
					}
					conn, _, _, err := ws.UpgradeHTTP(r, w)
					if err != nil {
						t.Error(err)
						return
					}
					defer conn.Close()
					select {
					case <-trigger:
					case <-time.After(3 * time.Second):
						return
					}
					wsutil.WriteServerText(conn, poeTestFrame("99", "complete", "unrelated"))
					wsutil.WriteServerText(conn, poeTestFrame("42", "incomplete", "draft"))
					if mode == "truncated" {
						return
					}
					state := "complete"
					if mode == "error" {
						state = "error_user_message_too_long"
					}
					wsutil.WriteServerText(conn, poeTestFrame("42", state, "final answer"))
					io.Copy(io.Discard, conn)
					return
				}
				if r.Header.Get("Cookie") != "p-b=own; p-lat=ownlat" || r.Header.Get("Poe-Formkey") != "ownform" {
					t.Error("wrong credentials")
				}
				if r.URL.Path == "/api/settings" {
					io.WriteString(w, `{"tchannelData":{"baseHost":"poe.com","boxName":"box","channel":"channel","channelHash":"hash","minSeq":0}}`)
					return
				}
				if r.URL.Path != "/api/gql_POST" {
					t.Error("wrong query path")
				}
				raw, _ := io.ReadAll(r.Body)
				sign := md5.Sum(append(raw, []byte("ownform4LxgHM6KpFqokX0Ox")...))
				if r.Header.Get("poe-tag-id") != hex.EncodeToString(sign[:]) || r.Header.Get("Poe-Tchannel") != "channel" {
					t.Error("wrong signed query headers")
				}
				var query struct {
					Name       string            `json:"queryName"`
					Variables  map[string]any    `json:"variables"`
					Extensions map[string]string `json:"extensions"`
				}
				json.Unmarshal(raw, &query)
				switch query.Name {
				case "SubscriptionsMutation":
					if query.Extensions["hash"] != poeSubscribeHash {
						t.Error("subscription hash")
					}
					io.WriteString(w, `{"data":{"subscriptionsMutation":true}}`)
				case "HandleBotLandingPageQuery":
					if query.Variables["botHandle"] != "GPT-4o" {
						t.Error("bot handle mapping")
					}
					io.WriteString(w, `{"data":{"bot":{"handle":"GPT-4o","model":"gpt4_o"}}}`)
				case "SendMessageMutation":
					if query.Extensions["hash"] != poeSendHash || query.Variables["bot"] != "gpt4_o" || query.Variables["query"] != "hello" || query.Variables["chatId"] != nil {
						t.Error("send contract")
					}
					close(trigger)
					io.WriteString(w, `{"data":{"messageEdgeCreate":{"status":"success","chat":{"chatId":42}}}}`)
				default:
					t.Error("unknown operation")
				}
			}))
			defer s.Close()
			t.Setenv("COT_TEST_POE", `{"cookie":"p-b=own; p-lat=ownlat","formkey":"ownform"}`)
			c := New(config.Source{Adapter: "poe-web", BaseURL: s.URL, KeyEnv: "COT_TEST_POE"})
			defer c.Close()
			ctx, cancel := context.WithTimeout(context.Background(), 4*time.Second)
			defer cancel()
			r, err := c.Do(ctx, "chat", "GPT-4o", false, []byte(`{"messages":[{"role":"user","content":"hello"}]}`), nil)
			if mode != "ok" {
				if err == nil {
					r.Body.Close()
					t.Fatal("accepted incomplete/error turn")
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			defer r.Body.Close()
			raw, _ := io.ReadAll(r.Body)
			if !strings.Contains(string(raw), "final answer") || strings.Contains(string(raw), "unrelated") || strings.Contains(string(raw), "draft") {
				t.Fatal(string(raw))
			}
		})
	}
}

func TestPoeChannelHostAndCorrelation(t *testing.T) {
	ch := poeChannel{BaseHost: "evil.example", Box: "box", Channel: "channel", Hash: "hash", Sequence: "0"}
	if _, err := poeChannelURL("https://poe.com", ch); err == nil {
		t.Fatal("accepted foreign channel")
	}
	ch.BaseHost = "poe.com"
	u, err := poeChannelURL("https://poe.com", ch)
	if err != nil || !strings.HasPrefix(u, "wss://") || !strings.Contains(u, ".tch.poe.com/") {
		t.Fatalf("%q %v", u, err)
	}
	if _, done, err := poeAnswerFrame(poeTestFrame("99", "complete", "other"), "42"); err != nil || done {
		t.Fatal("accepted other chat")
	}
	for _, raw := range []string{`{"error":"secret"}`, `{"messages":["bad-json"]}`, `bad`} {
		if _, _, err := poeAnswerFrame([]byte(raw), "42"); err == nil {
			t.Fatal("accepted bad frame")
		}
	}
}
