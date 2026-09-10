package api

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"clash-of-tokens/internal/config"
	"clash-of-tokens/internal/credentials"
)

func TestControlPlaneHotUpdateKeepsHeldRequest(t *testing.T) {
	started, finish := make(chan struct{}), make(chan struct{})
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.Copy(io.Discard, r.Body)
		close(started)
		select {
		case <-finish:
		case <-r.Context().Done():
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, "{\"ok\":true}")
	}))
	defer upstream.Close()
	c := config.Default()
	c.Sources = []config.Source{{ID: "mock", Provider: "p", Adapter: "openai", BaseURL: upstream.URL, Local: true, Enabled: true, MaxInflight: 1, QuotaDomain: "one", QuotaMaxInflight: 1, Models: []config.Model{{ID: "m", Upstream: "m", Protocols: []string{"chat"}, Tier: "unrated", Tools: "none", MaxInputBytes: 1024}}}}
	dir := t.TempDir()
	path := filepath.Join(dir, "config.json")
	vault, _ := credentials.Open(filepath.Join(dir, "vault"))
	plane, err := NewControlPlane(path, c, testKey, adminKey, vault)
	if err != nil {
		t.Fatal(err)
	}
	defer plane.Close()
	done := make(chan *httptest.ResponseRecorder, 1)
	go func() {
		w := httptest.NewRecorder()
		req := httptest.NewRequest("POST", "/v1/chat/completions", strings.NewReader("{\"model\":\"mock/m\"}"))
		req.Header.Set("Authorization", "Bearer "+testKey)
		plane.ServeHTTP(w, req)
		done <- w
	}()
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("upstream did not start")
	}
	c.Sources[0].Enabled = false
	body, _ := json.Marshal(map[string]any{"revision": 1, "config": c, "summary": "Disable source"})
	w := httptest.NewRecorder()
	req := httptest.NewRequest("PATCH", "/admin/config", bytes.NewReader(body))
	req.Header.Set("Authorization", "Bearer "+adminKey)
	plane.ServeHTTP(w, req)
	if w.Code != 200 {
		close(finish)
		t.Fatal(w.Code, w.Body.String())
	}
	if plane.current.server.Router.Status()[0].Enabled {
		close(finish)
		t.Fatal("source still enabled")
	}
	if plane.current.server.Router.Status()[0].Active != 1 {
		close(finish)
		t.Fatal("old capacity lost")
	}
	close(finish)
	result := <-done
	if result.Code != 200 || result.Body.String() != "{\"ok\":true}" {
		t.Fatal("held request interrupted", result.Code, result.Body.String())
	}
	if plane.current.server.Router.Status()[0].Active != 0 {
		t.Fatal("lease leaked")
	}
	loaded, err := config.Load(path)
	if err != nil || loaded.Sources[0].Enabled {
		t.Fatal("update not persistent", err)
	}
}

func TestControlPlaneReportsPendingRestartAndRejectsStaleApply(t *testing.T) {
	dir := t.TempDir()
	vault, _ := credentials.Open(filepath.Join(dir, "vault"))
	p, err := NewControlPlane(filepath.Join(dir, "config.json"), config.Default(), testKey, adminKey, vault)
	if err != nil {
		t.Fatal(err)
	}
	defer p.Close()
	desired := p.service.Current().Config
	desired.Listen = "127.0.0.1:9000"
	if _, err = p.service.Apply(1, desired, "Change listener"); err != nil {
		t.Fatal(err)
	}
	if p.current.server.cfg.Listen != "127.0.0.1:8317" {
		t.Fatal("listener change incorrectly live")
	}
	if len(restartFields(p.startup, p.service.Current().Config)) != 1 {
		t.Fatal("restart indication missing")
	}
	body, _ := json.Marshal(map[string]any{"revision": 1, "config": desired})
	req := httptest.NewRequest("PATCH", "/admin/config", bytes.NewReader(body))
	req.Header.Set("Authorization", "Bearer "+adminKey)
	w := httptest.NewRecorder()
	p.ServeHTTP(w, req)
	if w.Code != 409 {
		t.Fatal(w.Code)
	}
}

func TestConfigRollbackRejectsStaleRevision(t *testing.T) {
	dir := t.TempDir()
	vault, _ := credentials.Open(filepath.Join(dir, "vault"))
	p, err := NewControlPlane(filepath.Join(dir, "config.json"), config.Default(), testKey, adminKey, vault)
	if err != nil {
		t.Fatal(err)
	}
	defer p.Close()
	first := p.service.Current().Config
	first.Runtime.MaxQueued++
	if _, err = p.service.Apply(1, first, "first change"); err != nil {
		t.Fatal(err)
	}
	second := p.service.Current().Config
	second.Runtime.MaxQueued++
	if _, err = p.service.Apply(2, second, "second change"); err != nil {
		t.Fatal(err)
	}
	body, _ := json.Marshal(map[string]any{"revision": 2, "target_revision": 1})
	req := httptest.NewRequest("POST", "/admin/config/rollback", bytes.NewReader(body))
	req.Header.Set("Authorization", "Bearer "+adminKey)
	w := httptest.NewRecorder()
	p.ServeHTTP(w, req)
	if w.Code != http.StatusConflict {
		t.Fatalf("stale rollback status=%d body=%s", w.Code, w.Body.String())
	}
	if got := p.service.Current().Revision; got != 3 {
		t.Fatalf("stale rollback changed revision to %d", got)
	}
}

func TestAccountProviderChangeRequiresSourceMigration(t *testing.T) {
	dir := t.TempDir()
	vault, _ := credentials.Open(filepath.Join(dir, "vault"))
	c := config.Default()
	c.Providers = []config.Provider{{ID: "openai"}, {ID: "anthropic"}}
	c.Accounts = []config.Account{{ID: "acct", ProviderID: "openai", QuotaDomain: "acct", MaxInflight: 1, Weight: 1}}
	c.Sources = []config.Source{{ID: "source", Provider: "openai", Adapter: "openai", BaseURL: "http://127.0.0.1:1", Local: true, Enabled: true, AutoApproved: true, BillingMode: "free_allowance", AccountID: "acct", QuotaDomain: "acct", MaxInflight: 1, QuotaMaxInflight: 1, Models: []config.Model{{ID: "model", Upstream: "model", Protocols: []string{"chat"}, Tier: "silver", RatingBasis: "fixture", Tools: "none", MaxInputBytes: 1024}}}}
	c.Groups[0].Sources = []string{"source"}
	p, err := NewControlPlane(filepath.Join(dir, "config.json"), c, testKey, adminKey, vault)
	if err != nil {
		t.Fatal(err)
	}
	defer p.Close()
	req := httptest.NewRequest("PATCH", "/admin/accounts/acct", strings.NewReader(`{"provider_id":"anthropic"}`))
	req.Header.Set("Authorization", "Bearer "+adminKey)
	req.Header.Set("If-Match", "1")
	w := httptest.NewRecorder()
	p.ServeHTTP(w, req)
	if w.Code != http.StatusConflict || !strings.Contains(w.Body.String(), "migrate sources first") {
		t.Fatalf("provider migration status=%d body=%s", w.Code, w.Body.String())
	}
	if got := p.service.Current().Revision; got != 1 {
		t.Fatalf("rejected provider change advanced revision to %d", got)
	}
}

func TestConfigPreviewReportsRoutingImpact(t *testing.T) {
	dir := t.TempDir()
	vault, _ := credentials.Open(filepath.Join(dir, "vault"))
	c := config.Default()
	c.Sources = []config.Source{{ID: "preview-source", Provider: "openai", Adapter: "openai", BaseURL: "http://127.0.0.1:1", Local: true, Enabled: true, AutoApproved: true, BillingMode: "free_allowance", MaxInflight: 1, QuotaDomain: "preview", QuotaMaxInflight: 1, Models: []config.Model{{ID: "model", Upstream: "model", Protocols: []string{"chat"}, Tier: "silver", RatingBasis: "fixture", Tools: "none", MaxInputBytes: 1024}}}}
	c.Groups[0].Sources = []string{"preview-source"}
	p, err := NewControlPlane(filepath.Join(dir, "config.json"), c, testKey, adminKey, vault)
	if err != nil {
		t.Fatal(err)
	}
	defer p.Close()
	desired := c
	desired.Sources = append([]config.Source(nil), c.Sources...)
	desired.Sources[0].Enabled = false
	body, _ := json.Marshal(map[string]any{"revision": 1, "config": desired})
	req := httptest.NewRequest("POST", "/admin/config/preview", bytes.NewReader(body))
	req.Header.Set("Authorization", "Bearer "+adminKey)
	w := httptest.NewRecorder()
	p.ServeHTTP(w, req)
	if w.Code != 200 {
		t.Fatalf("preview status=%d body=%s", w.Code, w.Body.String())
	}
	var response struct {
		Impact struct {
			SourcesChanged  []string `json:"sources_changed"`
			AccountsChanged []string `json:"accounts_changed"`
			GroupsChanged   []string `json:"groups_changed"`
			Groups          []struct {
				Group          string `json:"group"`
				Protocol       string `json:"protocol"`
				BeforeEligible int    `json:"before_eligible"`
				AfterEligible  int    `json:"after_eligible"`
			} `json:"groups"`
		} `json:"impact"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	if len(response.Impact.Groups) != 1 || response.Impact.Groups[0].Group != "auto" || response.Impact.Groups[0].Protocol != "chat" || response.Impact.Groups[0].BeforeEligible != 1 || response.Impact.Groups[0].AfterEligible != 0 {
		t.Fatalf("unexpected preview impact: %+v", response.Impact.Groups)
	}
	if len(response.Impact.SourcesChanged) != 1 || response.Impact.SourcesChanged[0] != "preview-source" || len(response.Impact.AccountsChanged) != 0 || len(response.Impact.GroupsChanged) != 0 {
		t.Fatalf("unexpected changed-resource impact: %+v", response.Impact)
	}
}
