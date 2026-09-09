package upstream

import (
	"context"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"clash-of-tokens/internal/config"
)

func TestConnectionReuseAcrossConcurrentWaves(t *testing.T) {
	const parallel = 16
	var connections atomic.Int32
	arrived := make(chan struct{}, parallel)
	release := make(chan struct{}, parallel)
	server := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		io.Copy(io.Discard, r.Body)
		arrived <- struct{}{}
		select {
		case <-release:
		case <-r.Context().Done():
			return
		}
		io.WriteString(w, `{}`)
	}))
	server.Config.ConnState = func(_ net.Conn, state http.ConnState) {
		if state == http.StateNew {
			connections.Add(1)
		}
	}
	server.Start()
	defer server.Close()
	c := New(config.Source{Adapter: "openai", Local: true, BaseURL: server.URL, MaxInflight: parallel})
	defer c.Close()
	for wave := 0; wave < 2; wave++ {
		var wg sync.WaitGroup
		for range parallel {
			wg.Add(1)
			go func() {
				defer wg.Done()
				ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
				defer cancel()
				resp, err := c.Do(ctx, "chat", "m", false, []byte(`{"messages":[]}`), nil)
				if err != nil {
					t.Error(err)
					return
				}
				io.Copy(io.Discard, resp.Body)
				resp.Body.Close()
			}()
		}
		for range parallel {
			select {
			case <-arrived:
			case <-time.After(5 * time.Second):
				t.Fatal("wave stalled")
			}
		}
		for range parallel {
			release <- struct{}{}
		}
		wg.Wait()
	}
	if got := connections.Load(); got != parallel {
		t.Fatalf("two waves opened %d connections; want %d reusable connections", got, parallel)
	}
}
