package api

import (
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"clash-of-tokens/internal/config"
)

func TestRequestFallbackSafety(t *testing.T) {
	for _, tc := range []struct {
		name          string
		status, limit int
		kind          string
		stream        bool
		want          int
	}{
		{"rejected", 429, 0, "custom_api", false, 2},
		{"bounded", 429, 1, "custom_api", false, 1},
		{"ambiguous_server_error", 500, 0, "custom_api", false, 1},
		{"reverse_rejection", 429, 0, "product_reverse", false, 1},
		{"truncated_stream", 200, 0, "custom_api", true, 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var calls atomic.Int32
			up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				n := calls.Add(1)
				body, _ := io.ReadAll(r.Body)
				if !strings.Contains(string(body), fmt.Sprintf(`"model":"upstream-%d"`, n)) {
					t.Errorf("rewritten payload: %s", body)
				}
				if tc.stream {
					w.Header().Set("Content-Type", "text/event-stream")
				}
				if n == 1 {
					w.WriteHeader(tc.status)
					if tc.stream {
						io.WriteString(w, "data: {\"choices\":[{\"delta\":{\"content\":\"partial\"}}]}\n\n")
					}
					return
				}
				io.WriteString(w, `{"result":"second"}`)
			}))
			defer up.Close()
			c := config.Default()
			for i := 1; i <= 2; i++ {
				id := fmt.Sprintf("source-%d", i)
				c.Sources = append(c.Sources, config.Source{ID: id, Provider: "test", Adapter: "openai", BaseURL: up.URL, Local: true, SourceKind: tc.kind, BillingMode: "free_allowance", Enabled: true, AutoApproved: true, MaxInflight: 1, QuotaDomain: id, QuotaMaxInflight: 1, Models: []config.Model{{ID: "model", Upstream: fmt.Sprintf("upstream-%d", i), Protocols: []string{"chat"}, Tier: "unrated", Tools: "none", MaxInputBytes: 4096}}})
			}
			c.Groups = []config.Group{{ID: "fallback", Type: "fallback", Sources: []string{"source-1", "source-2"}, MinTier: "silver", AllowUnrated: true, MaxAttempts: tc.limit}}
			s, err := NewWithKeys(c, testKey, adminKey)
			if err != nil {
				t.Fatal(err)
			}
			defer s.Close()
			gateway := httptest.NewServer(s)
			defer gateway.Close()
			resp := request(t, gateway.URL, "/v1/chat/completions", fmt.Sprintf(`{"model":"fallback","stream":%t}`, tc.stream), testKey)
			body, readErr := io.ReadAll(resp.Body)
			resp.Body.Close()
			if calls.Load() != int32(tc.want) {
				t.Fatalf("calls=%d want=%d", calls.Load(), tc.want)
			}
			if tc.want == 2 && (resp.StatusCode != 200 || string(body) != `{"result":"second"}` || resp.Header.Get("X-COT-Source") != "source-2") {
				t.Fatalf("fallback result %d %s", resp.StatusCode, body)
			}
			if tc.stream && readErr == nil {
				t.Fatal("truncated stream reported clean completion")
			}
		})
	}
}
