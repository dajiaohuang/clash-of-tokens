package chinaremaining

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"clash-of-tokens/internal/config"
)

func TestMetasoBrowserFixtureAccountIsolation(t *testing.T) {
	endpoint := os.Getenv("COT_TEST_CDP")
	if endpoint == "" {
		t.Skip("set COT_TEST_CDP for a real browser fixture")
	}
	var calls atomic.Int32
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/" {
			w.Header().Set("Content-Type", "text/html")
			io.WriteString(w, "<!doctype html><html><body>fixture</body></html>")
			return
		}
		if r.URL.Path != "/api/searchV2" {
			http.NotFound(w, r)
			return
		}
		n := calls.Add(1)
		uid, e1 := r.Cookie("uid")
		sid, e2 := r.Cookie("sid")
		if e1 != nil || e2 != nil || uid.Value != fmt.Sprint("user", n) || sid.Value != fmt.Sprint("session", n) {
			t.Errorf("cookie mismatch: request=%d uid_present=%t sid_present=%t", n, e1 == nil, e2 == nil)
			w.WriteHeader(401)
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		io.WriteString(w, "data: fixture answer\n\ndata: [DONE]\n\n")
	}))
	defer up.Close()
	c := New(config.Source{Adapter: "metaso"}, config.Browser{Enabled: true, CDPURL: endpoint, Engine: os.Getenv("COT_TEST_BROWSER_ENGINE")})
	defer c.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	for i := 1; i <= 2; i++ {
		resp, err := c.metasoBrowserStream(ctx, up.URL, fmt.Sprintf("user%d-session%d", i, i), "synthetic-metadata", "conversation", "hello", "detail")
		if err != nil {
			t.Fatal(err)
		}
		raw, err := io.ReadAll(resp.Body)
		resp.Body.Close()
		if err != nil || !strings.Contains(string(raw), "fixture answer") {
			t.Fatalf("stream failed: %v", err)
		}
	}
	if calls.Load() != 2 {
		t.Fatal("missing actual browser requests")
	}
}
