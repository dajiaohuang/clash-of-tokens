package majorweb

import (
	"clash-of-tokens/internal/config"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"
)

type adaptaTransport func(*http.Request) (*http.Response, error)

func (f adaptaTransport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestAdaptaClerkExchangeCacheAndPlaceholder(t *testing.T) {
	t.Setenv("ADAPTA_TEST", "__client=own-client-token")
	token := "eyJhbGciOiJIUzI1NiJ9." + base64.RawURLEncoding.EncodeToString([]byte(fmt.Sprintf(`{"exp":%d}`, time.Now().Unix()+3600))) + ".signature"
	authCalls, chatCalls := 0, 0
	c := New(config.Source{Adapter: "adapta-web", KeyEnv: "ADAPTA_TEST"})
	defer c.Close()
	c.http.Transport = adaptaTransport(func(r *http.Request) (*http.Response, error) {
		text := ""
		ct := "application/json"
		if r.URL.Host == "clerk.agent.adapta.one" {
			authCalls++
			if r.Header.Get("Cookie") != "__client=own-client-token" || r.Header.Get("Authorization") != "" {
				t.Error("incorrect clerk credentials")
			}
			switch r.URL.Path {
			case "/v1/client":
				text = `{"response":{"sessions":[{"id":"sess_own","status":"active"}]}}`
			case "/v1/client/sessions/sess_own/tokens":
				text = fmt.Sprintf(`{"jwt":%q}`, token)
			default:
				t.Error("wrong auth route")
			}
		} else {
			chatCalls++
			if r.URL.Host != "agent.adapta.one" || r.URL.Path != "/api/chat/stream/v1" || r.Header.Get("Authorization") != "Bearer "+token || r.Header.Get("Cookie") != "" {
				t.Error("incorrect chat route or credential")
			}
			var p map[string]any
			json.NewDecoder(r.Body).Decode(&p)
			r.Body.Close()
			if p["aiModelId"] != float64(14) {
				t.Error("wrong model")
			}
			text = "data: {\"type\":\"text-delta\",\"id\":\"quick-response\",\"delta\":\"loading\"}\n\ndata: {\"type\":\"text-delta\",\"id\":\"answer\",\"delta\":\"real answer\"}\n\ndata: {\"type\":\"done\"}\n\n"
			ct = "text/event-stream"
		}
		return &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": {ct}}, Body: io.NopCloser(strings.NewReader(text))}, nil
	})
	for _, stream := range []bool{false, true} {
		resp, err := c.Do(context.Background(), "chat", "adapta-one", stream, []byte(`{"messages":[{"role":"user","content":"hi"}]}`), nil)
		if err != nil {
			t.Fatal(err)
		}
		data, err := io.ReadAll(resp.Body)
		resp.Body.Close()
		if err != nil || strings.Contains(string(data), "loading") || !strings.Contains(string(data), "real answer") {
			t.Fatalf("%s %v", data, err)
		}
	}
	if authCalls != 2 || chatCalls != 2 {
		t.Fatalf("cache not used: auth=%d chat=%d", authCalls, chatCalls)
	}
	_, err := c.Do(context.Background(), "chat", "adapta-claude", false, []byte(`{"messages":[{"role":"user","content":"hi"}]}`), nil)
	if err == nil || authCalls != 2 {
		t.Fatal("false model alias accepted")
	}
}
