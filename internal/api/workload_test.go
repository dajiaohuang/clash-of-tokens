package api

import (
	"encoding/json"
	"math"
	"net/http/httptest"
	"testing"
	"time"

	"clash-of-tokens/internal/config"
	"clash-of-tokens/internal/routing"
)

func TestAdminWorkloadSnapshotAndAuthentication(t *testing.T) {
	c := config.Default()
	s, err := NewWithKeys(c, testKey, adminKey)
	if err != nil {
		t.Fatal(err)
	}
	s.started = time.Now().Add(-10 * time.Second)
	s.requests.Store(20)
	s.buffered.Store(4096)
	for _, key := range []string{"", testKey, adminKey} {
		r := httptest.NewRequest("GET", "/admin/status", nil)
		if key != "" {
			r.Header.Set("Authorization", "Bearer "+key)
		}
		w := httptest.NewRecorder()
		s.ServeHTTP(w, r)
		if key != adminKey {
			if w.Code == 200 {
				t.Fatal("workload exposed without admin key")
			}
			continue
		}
		var out struct {
			Workload routing.WorkloadStatus `json:"workload"`
			Buffered int64                  `json:"buffered_bytes"`
			Limit    int64                  `json:"buffered_limit_bytes"`
			Uptime   float64                `json:"uptime_seconds"`
			Rate     float64                `json:"average_requests_per_second"`
			Heap     uint64                 `json:"go_heap_bytes"`
		}
		if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &out) != nil {
			t.Fatal(w.Code, w.Body.String())
		}
		if out.Workload.ActiveLimit != c.Runtime.MaxInflight || out.Workload.QueueLimit != c.Runtime.MaxQueued || out.Buffered != 4096 || out.Limit != c.Runtime.MaxBufferedBytes || out.Heap == 0 || out.Uptime < 10 || math.Abs(out.Rate*out.Uptime-20) > 1e-8 {
			t.Fatalf("bad workload snapshot: %+v", out)
		}
	}
}
