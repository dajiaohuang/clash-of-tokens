package api

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"sort"
	"testing"
	"time"

	"clash-of-tokens/internal/config"
	"clash-of-tokens/internal/credentials"
)

func TestControlPlaneFiveSecondValidationLatency(t *testing.T) {
	started := make(chan struct{})
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		close(started)
		select {
		case <-time.After(5 * time.Second):
		case <-r.Context().Done():
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w, "data: {\"choices\":[{\"delta\":{\"content\":\"OK\"}}]}\n\ndata: [DONE]\n\n")
	}))
	defer up.Close()
	c := config.Default()
	c.Sources = []config.Source{{ID: "s", Provider: "fixture", Adapter: "openai", BaseURL: up.URL, Local: true, Enabled: true, MaxInflight: 1, QuotaDomain: "q", QuotaMaxInflight: 1, Models: []config.Model{{ID: "m", Upstream: "m", Protocols: []string{"chat"}, Tier: "unrated", Tools: "none", MaxInputBytes: 1024}}}}
	dir := t.TempDir()
	vault, err := credentials.Open(filepath.Join(dir, "vault"))
	if err != nil {
		t.Fatal(err)
	}
	p, err := NewControlPlane(filepath.Join(dir, "config.json"), c, testKey, adminKey, vault)
	if err != nil {
		t.Fatal(err)
	}
	defer p.Close()
	server := httptest.NewServer(p)
	defer server.Close()
	client := &http.Client{Timeout: time.Second}
	call := func(method, path string, body any) (int, time.Duration, []byte) {
		t.Helper()
		raw, _ := json.Marshal(body)
		r, err := http.NewRequest(method, server.URL+path, bytes.NewReader(raw))
		if err != nil {
			t.Fatal(err)
		}
		r.Header.Set("Authorization", "Bearer "+adminKey)
		start := time.Now()
		resp, err := client.Do(r)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		data, err := io.ReadAll(resp.Body)
		if err != nil {
			t.Fatal(err)
		}
		return resp.StatusCode, time.Since(start), data
	}
	status, _, body := call("POST", "/admin/jobs", map[string]any{"path": "/admin/sources/s/validate", "input": map[string]string{"model": "m", "protocol": "chat"}})
	if status != 202 {
		t.Fatal(status, string(body))
	}
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("validation did not start")
	}
	times := make([]time.Duration, 0, 101)
	for i := 0; i < 100; i++ {
		status, elapsed, _ := call("GET", "/admin/status", nil)
		if status != 200 {
			t.Fatal(status)
		}
		times = append(times, elapsed)
	}
	c.Sources[0].Enabled = false
	status, elapsed, body := call("PATCH", "/admin/config", map[string]any{"revision": 1, "config": c, "summary": "Disable during five-second validation"})
	if status != 200 {
		t.Fatal(status, string(body))
	}
	times = append(times, elapsed)
	sort.Slice(times, func(i, j int) bool { return times[i] < times[j] })
	p99 := times[(len(times)-1)*99/100]
	t.Logf("loopback_control_samples=%d p99_ms=%.3f disable_ms=%.3f validation_delay_ms=5000", len(times), float64(p99.Microseconds())/1000, float64(elapsed.Microseconds())/1000)
	if p99 > 200*time.Millisecond || elapsed > 200*time.Millisecond {
		t.Fatal("loopback management latency exceeded 200 ms")
	}
}
