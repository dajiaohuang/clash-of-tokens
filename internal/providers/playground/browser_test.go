package playground

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"clash-of-tokens/internal/config"
	"github.com/gobwas/ws"
	"github.com/gobwas/ws/wsutil"
)

func TestPlaygroundBrowserFixture(t *testing.T) {
	endpoint := os.Getenv("COT_TEST_CDP")
	if endpoint == "" {
		t.Skip("set COT_TEST_CDP for real browser fixture")
	}
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/" {
			w.Header().Set("Content-Type", "text/html")
			io.WriteString(w, "<!doctype html><html><body>fixture</body></html>")
			return
		}
		if !strings.HasPrefix(r.URL.Path, "/agents/playground/") {
			http.NotFound(w, r)
			return
		}
		conn, _, _, err := ws.UpgradeHTTP(r, w)
		if err != nil {
			t.Error(err)
			return
		}
		defer conn.Close()
		_ = conn.SetDeadline(time.Now().Add(10 * time.Second))
		for i := 0; i < 3; i++ {
			raw, err := wsutil.ReadClientText(conn)
			if err != nil {
				t.Error(err)
				return
			}
			var value struct {
				ID   string `json:"id"`
				Type string `json:"type"`
			}
			if json.Unmarshal(raw, &value) != nil {
				t.Error("invalid frame")
				return
			}
			if value.Type != "cf_agent_use_chat_request" {
				continue
			}
			for _, raw := range []string{frame(value.ID, map[string]string{"type": "text-delta", "delta": "browser answer"}, false), frame(value.ID, map[string]string{"type": "finish"}, false), frame(value.ID, nil, true)} {
				if wsutil.WriteServerText(conn, []byte(raw)) != nil {
					return
				}
			}
		}
	}))
	defer up.Close()
	c := New(config.Browser{Enabled: true, CDPURL: endpoint, Engine: os.Getenv("COT_TEST_BROWSER_ENGINE")})
	defer c.Close()
	c.start = func(ctx context.Context, id string, payload any) (frameTransport, error) {
		return c.startBrowserAt(ctx, id, payload, up.URL)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	resp, err := c.Do(ctx, "chat", "model", false, []byte(`{"messages":[{"role":"user","content":"hello"}]}`), nil)
	if err != nil {
		t.Fatal(err)
	}
	raw, err := io.ReadAll(resp.Body)
	resp.Body.Close()
	if err != nil || !strings.Contains(string(raw), "browser answer") {
		t.Fatalf("browser websocket invocation failed: %v", err)
	}
}
