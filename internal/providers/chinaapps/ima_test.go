package chinaapps

import (
	"clash-of-tokens/internal/config"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestIMAWireAndBufferedCompletion(t *testing.T) {
	for _, stream := range []bool{false, true} {
		t.Run(map[bool]string{true: "stream", false: "json"}[stream], func(t *testing.T) {
			var calls atomic.Int32
			up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls.Add(1)
				if r.Header.Get("Authorization") != "" || r.Header.Get("Cookie") != "" || r.Header.Get("x-ima-cookie") != "IMA-TOKEN=own-token;IMA-UID=own-user" || r.Header.Get("x-ima-bkn") != bkn("own-token") {
					t.Error("credential isolation")
				}
				var p map[string]any
				if json.NewDecoder(r.Body).Decode(&p) != nil {
					t.Error("payload")
				}
				switch r.URL.Path {
				case "/cgi-bin/session_logic/init_session":
					if p["name"] != "你好" || p["msgs_limit"] != float64(20) {
						t.Error(p)
					}
					io.WriteString(w, `{"code":0,"session_id":"session-1"}`)
				case "/cgi-bin/assistant/qa":
					if p["question"] != "你好" || p["session_id"] != "session-1" {
						t.Error(p)
					}
					info := p["model_info"].(map[string]any)
					if info["model_type"] != float64(3000) || info["model_id"] != "official_3000" {
						t.Error(info)
					}
					w.Header().Set("Content-Type", "text/event-stream")
					io.WriteString(w, "event: DELTA\r\ndata: {\"text\":\"你\"}\r\n\r\nevent: DELTA\ndata: {\"text\":\"好\"}\n\nevent: COMPLETED\ndata: {}\n\nevent: CLOSE\ndata: {}\n\n")
				default:
					t.Error(r.URL.Path)
					w.WriteHeader(404)
				}
			}))
			defer up.Close()
			t.Setenv("IMA_TEST", "IMA-TOKEN=own-token;IMA-UID=own-user")
			c := New(config.Source{BaseURL: up.URL, KeyEnv: "IMA_TEST", MaxInflight: 2})
			defer c.Close()
			resp, err := c.Do(context.Background(), "chat", "glm-5.2", stream, []byte(`{"model":"glm-5.2","messages":[{"role":"user","content":"你好"}]}`), http.Header{"Authorization": {"Bearer caller"}, "Cookie": {"caller-secret"}})
			if err != nil {
				t.Fatal(err)
			}
			defer resp.Body.Close()
			data, _ := io.ReadAll(resp.Body)
			if calls.Load() != 2 || resp.Header.Get("X-COT-Delivery") != "buffered" || !strings.Contains(string(data), "你好") {
				t.Fatalf("%d %s", calls.Load(), data)
			}
			if stream && !strings.HasSuffix(string(data), "data: [DONE]\n\n") {
				t.Fatal("missing finish")
			}
		})
	}
}
func TestIMARejectsUnsupportedBeforeNetwork(t *testing.T) {
	var calls atomic.Int32
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { calls.Add(1) }))
	defer up.Close()
	t.Setenv("IMA_TEST", "IMA-TOKEN=own-token")
	c := New(config.Source{BaseURL: up.URL, KeyEnv: "IMA_TEST"})
	defer c.Close()
	for _, body := range []string{`{"messages":[{"role":"user","content":"x"}],"tools":[]}`, `{"messages":[{"role":"system","content":"x"}]}`, `{"messages":[{"role":"user","content":[{"type":"image_url"}]}]}`, `{"messages":[{"role":"user","content":"x"},{"role":"user","content":"y"}]}`, `{"messages":[{"role":"user","content":"x"}],"temperature":0.5}`} {
		if _, e := c.Do(context.Background(), "chat", "glm-5.2", false, []byte(body), nil); !errors.Is(e, ErrUnsupported) {
			t.Fatal(e)
		}
	}
	if _, e := c.Do(context.Background(), "chat", "unknown", false, []byte(`{"messages":[{"role":"user","content":"x"}]}`), nil); !errors.Is(e, ErrUnsupported) {
		t.Fatal(e)
	}
	if calls.Load() != 0 {
		t.Fatal("unsupported request reached provider")
	}
}
func TestIMARejectsIncompleteOrErrorStreams(t *testing.T) {
	for _, wire := range []string{
		"event: DELTA\ndata: {\"text\":\"partial\"}\n\n",
		"event: DELTA\ndata: {\"text\":\"partial\"}\n\nevent: CLOSE\ndata: {}\n\n",
		"event: COMPLETED\ndata: {}\n\n",
		"event: DELTA\ndata: {\"text\":\"partial\"}\n\nevent: ERROR\ndata: secret\n\n",
		"event: DELTA\ndata: broken\n\nevent: COMPLETED\ndata: {}\n\n",
		"event: DELTA\ndata: {\"text\":\"partial\"}\n\nevent: COMPLETED\ndata: {}\n\nevent: FAILED\ndata: secret\n\n",
		"event: DELTA\ndata: {\"text\":\"partial\"}\n\nevent: COMPLETED\ndata: {\"code\":1}\n\n",
		"event: DELTA\ndata: {\"text\":\"partial\"}\n\nevent: COMPLETED\ndata: broken\n\n",
	} {
		if _, e := decodeIMA(strings.NewReader(wire)); e == nil {
			t.Fatalf("accepted %q", wire)
		}
	}
}
func TestIMACancellationAndHTTPStatus(t *testing.T) {
	started := make(chan struct{})
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.Copy(io.Discard, r.Body)
		close(started)
		select {
		case <-r.Context().Done():
		case <-time.After(3 * time.Second):
		}
	}))
	defer up.Close()
	t.Setenv("IMA_TEST", "IMA-TOKEN=own-token")
	c := New(config.Source{BaseURL: up.URL, KeyEnv: "IMA_TEST"})
	defer c.Close()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() {
		_, e := c.Do(ctx, "chat", "glm-5.2", false, []byte(`{"messages":[{"role":"user","content":"x"}]}`), nil)
		done <- e
	}()
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("not started")
	}
	cancel()
	select {
	case e := <-done:
		if !errors.Is(e, context.Canceled) {
			t.Fatal(e)
		}
	case <-time.After(time.Second):
		t.Fatal("cancellation stalled")
	}
}
