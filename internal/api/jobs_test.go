package api

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"clash-of-tokens/internal/config"
	"clash-of-tokens/internal/credentials"
)

func TestManagementJobResponsiveStaleAndCancellation(t *testing.T) {
	for _, cancelJob := range []bool{false, true} {
		t.Run(fmt.Sprint(cancelJob), func(t *testing.T) {
			started, finish := make(chan struct{}), make(chan struct{})
			up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				close(started)
				select {
				case <-finish:
				case <-r.Context().Done():
					return
				}
				w.Header().Set("Content-Type", "text/event-stream")
				fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{\"content\":\"OK\"}}]}\n\ndata: [DONE]\n\n")
			}))
			defer up.Close()
			c := config.Default()
			c.Sources = []config.Source{{ID: "mock", Provider: "p", Adapter: "openai", BaseURL: up.URL, Local: true, Enabled: true, MaxInflight: 1, QuotaDomain: "one", QuotaMaxInflight: 1, Models: []config.Model{{ID: "m", Upstream: "m", Protocols: []string{"chat"}, Tier: "unrated", Tools: "none", MaxInputBytes: 1024}}}}
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
			request := func(method, path, body string) *httptest.ResponseRecorder {
				t.Helper()
				req := httptest.NewRequest(method, path, strings.NewReader(body))
				req.Header.Set("Authorization", "Bearer "+adminKey)
				out := httptest.NewRecorder()
				completed := make(chan struct{})
				go func() { p.ServeHTTP(out, req); close(completed) }()
				select {
				case <-completed:
				case <-time.After(time.Second):
					t.Fatal("control operation blocked: " + path)
				}
				return out
			}
			body := `{"path":"/admin/sources/mock/validate","input":{"model":"m","protocol":"chat"}}`
			accepted := request("POST", "/admin/jobs", body)
			var result struct {
				ID string `json:"job_id"`
			}
			if accepted.Code != 202 || json.Unmarshal(accepted.Body.Bytes(), &result) != nil || result.ID == "" {
				t.Fatal(accepted.Code, accepted.Body.String())
			}
			select {
			case <-started:
			case <-time.After(time.Second):
				t.Fatal("job did not start")
			}
			if out := request("GET", "/admin/status", ""); out.Code != 200 {
				t.Fatal(out.Code)
			}
			if out := request("POST", "/admin/jobs", body); out.Code != 409 {
				t.Fatal("duplicate resource accepted", out.Code)
			}
			if out := request("POST", "/admin/sources/mock/validate", `{"model":"m","protocol":"chat"}`); out.Code != 409 {
				t.Fatal("synchronous endpoint bypassed job resource exclusion", out.Code)
			}
			if cancelJob {
				if out := request("DELETE", "/admin/jobs/"+result.ID, ""); out.Code != 200 {
					t.Fatal(out.Code)
				}
			} else {
				c.Sources[0].Enabled = false
				patch, _ := json.Marshal(map[string]any{"revision": 1, "config": c, "summary": "disable during validation"})
				if out := request("PATCH", "/admin/config", string(patch)); out.Code != 200 {
					t.Fatal(out.Code, out.Body.String())
				}
			}
			close(finish)
			deadline := time.Now().Add(2 * time.Second)
			for {
				out := request("GET", "/admin/jobs/"+result.ID, "")
				var job managementJob
				if json.Unmarshal(out.Body.Bytes(), &job) != nil {
					t.Fatal(out.Body.String())
				}
				if job.State != "running" {
					expected := "stale"
					if cancelJob {
						expected = "canceled"
					}
					if job.State != expected || len(job.Result) != 0 {
						t.Fatalf("unexpected result: %+v", job)
					}
					break
				}
				if time.Now().After(deadline) {
					t.Fatal("job did not finish")
				}
				time.Sleep(5 * time.Millisecond)
			}
			if len(p.evidence.List()) != 0 {
				t.Fatal("stale/canceled evidence persisted")
			}
		})
	}
}

func TestManagementJobsRequireAdminAndSameOrigin(t *testing.T) {
	dir := t.TempDir()
	vault, _ := credentials.Open(filepath.Join(dir, "vault"))
	p, err := NewControlPlane(filepath.Join(dir, "config.json"), config.Default(), testKey, adminKey, vault)
	if err != nil {
		t.Fatal(err)
	}
	defer p.Close()
	for _, tc := range []struct {
		key, origin string
		status      int
	}{{testKey, "", 401}, {adminKey, "https://foreign.test", 403}} {
		r := httptest.NewRequest("POST", "/admin/jobs", bytes.NewBufferString(`{"path":"/admin/device/check"}`))
		r.Header.Set("Authorization", "Bearer "+tc.key)
		r.Header.Set("Origin", tc.origin)
		w := httptest.NewRecorder()
		p.ServeHTTP(w, r)
		if w.Code != tc.status {
			t.Fatal(w.Code)
		}
	}
}
