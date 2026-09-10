package api

import (
	"clash-of-tokens/internal/config"
	"clash-of-tokens/internal/credentials"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
)

func TestExplicitValidationOfDisabledSource(t *testing.T) {
	for _, complete := range []bool{true, false} {
		t.Run(fmt.Sprint(complete), func(t *testing.T) {
			calls := 0
			up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls++
				w.Header().Set("Content-Type", "text/event-stream")
				fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{\"content\":\"OK\"}}]}\n\n")
				if complete {
					fmt.Fprint(w, "data: [DONE]\n\n")
				}
			}))
			defer up.Close()
			c := config.Default()
			c.Sources = []config.Source{{ID: "test", Provider: "p", Adapter: "openai", BaseURL: up.URL, Local: true, Enabled: false, MaxInflight: 1, QuotaDomain: "test", QuotaMaxInflight: 1, Models: []config.Model{{ID: "model", Upstream: "model", Protocols: []string{"chat"}, Tier: "unrated", Tools: "none", MaxInputBytes: 1024}}}}
			dir := t.TempDir()
			vault, _ := credentials.Open(filepath.Join(dir, "vault"))
			p, err := NewControlPlane(filepath.Join(dir, "config.json"), c, testKey, adminKey, vault)
			if err != nil {
				t.Fatal(err)
			}
			defer p.Close()
			req := httptest.NewRequest("POST", "/admin/sources/test/validate", strings.NewReader(`{"model":"model","protocol":"chat"}`))
			req.Header.Set("Authorization", "Bearer "+adminKey)
			w := httptest.NewRecorder()
			p.ServeHTTP(w, req)
			var evidence ValidationEvidence
			if json.Unmarshal(w.Body.Bytes(), &evidence) != nil || w.Code != 200 || evidence.Verified != complete || !evidence.OutputObserved {
				t.Fatal(w.Code, w.Body.String())
			}
			if evidence.Checks.Connection != "pass" || evidence.Checks.Auth != "pass" || evidence.Checks.Request != "pass" || evidence.Checks.Streaming != "pass" || (complete && evidence.Checks.Completion != "pass") || evidence.Checks.DurationMS <= 0 {
				t.Fatalf("missing validation stage evidence: %+v", evidence.Checks)
			}
			if calls != 1 || p.current.server.Router.Status()[0].Enabled || p.current.server.Router.Status()[0].Active != 0 {
				t.Fatal("validation changed routing or leaked capacity")
			}
			if !evidence.HistoryRecorded || len(p.evidence.List()) != 1 || p.evidence.List()[0].Revision != 1 {
				t.Fatal("validation evidence missing revision history")
			}
			statusReq := httptest.NewRequest("GET", "/admin/status", nil)
			statusReq.Header.Set("Authorization", "Bearer "+adminKey)
			statusOut := httptest.NewRecorder()
			p.ServeHTTP(statusOut, statusReq)
			var status struct {
				Count        int                  `json:"live_verified_sources"`
				Verification []sourceVerification `json:"verification"`
			}
			wantCount, wantStatus := 0, "failed"
			if complete {
				wantCount, wantStatus = 1, "verified"
			}
			if statusOut.Code != 200 || json.Unmarshal(statusOut.Body.Bytes(), &status) != nil || status.Count != wantCount || len(status.Verification) != 1 || status.Verification[0].Models[0].Status != wantStatus {
				t.Fatal("validation status was not derived from evidence", statusOut.Body.String())
			}
		})
	}
}

func TestAccountValidateUsesConfiguredSourceAndRecordsAccount(t *testing.T) {
	calls := 0
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{\"content\":\"OK\"}}]}\n\n")
		fmt.Fprint(w, "data: [DONE]\n\n")
	}))
	defer up.Close()
	c := config.Default()
	c.Providers = []config.Provider{{ID: "provider"}}
	c.Accounts = []config.Account{{ID: "account", ProviderID: "provider", Enabled: true, MaxInflight: 1, Weight: 1, QuotaDomain: "account-quota"}}
	c.Sources = []config.Source{{ID: "source", Provider: "provider", Adapter: "openai", BaseURL: up.URL, Local: true, Enabled: false, MaxInflight: 1, AccountID: "account", QuotaDomain: "account-quota", QuotaMaxInflight: 1, Models: []config.Model{{ID: "model", Upstream: "upstream-model", Protocols: []string{"chat"}, Tier: "unrated", Tools: "none", MaxInputBytes: 1024}}}}
	dir := t.TempDir()
	vault, _ := credentials.Open(filepath.Join(dir, "vault"))
	p, err := NewControlPlane(filepath.Join(dir, "config.json"), c, testKey, adminKey, vault)
	if err != nil {
		t.Fatal(err)
	}
	defer p.Close()
	r := httptest.NewRequest("POST", "/admin/accounts/account/validate", strings.NewReader("{}"))
	r.Header.Set("Authorization", "Bearer "+adminKey)
	w := httptest.NewRecorder()
	p.ServeHTTP(w, r)
	var result ValidationEvidence
	if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &result) != nil || !result.Verified || result.Account != "account" || result.Source != "source" || result.Model != "model" || result.Protocol != "chat" || !result.HistoryRecorded || calls != 1 {
		t.Fatalf("status=%d calls=%d body=%s result=%+v", w.Code, calls, w.Body.String(), result)
	}
	if p.current.server.Router.Status()[0].Enabled {
		t.Fatal("account validation enabled the disabled source")
	}
}

func TestProviderValidateUsesFirstConfiguredSource(t *testing.T) {
	calls := 0
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{\"content\":\"OK\"}}]}\n\n")
		fmt.Fprint(w, "data: [DONE]\n\n")
	}))
	defer up.Close()
	c := config.Default()
	c.Providers = []config.Provider{{ID: "provider", Enabled: true}}
	c.Sources = []config.Source{{ID: "first", Provider: "provider", Adapter: "openai", BaseURL: up.URL, Local: true, Enabled: false, MaxInflight: 1, QuotaDomain: "quota", QuotaMaxInflight: 1, Models: []config.Model{{ID: "model", Upstream: "upstream", Protocols: []string{"chat"}, Tier: "unrated", Tools: "none", MaxInputBytes: 1024}}}}
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
	r := httptest.NewRequest("POST", "/admin/providers/provider/validate", nil)
	r.Header.Set("Authorization", "Bearer "+adminKey)
	w := httptest.NewRecorder()
	p.ServeHTTP(w, r)
	var result ValidationEvidence
	if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &result) != nil || !result.Verified || result.Source != "first" || result.Model != "model" || result.Protocol != "chat" || calls != 1 {
		t.Fatalf("status=%d calls=%d body=%s result=%+v", w.Code, calls, w.Body.String(), result)
	}
	if p.current.server.Router.Status()[0].Enabled {
		t.Fatal("provider validation enabled the disabled source")
	}
}

func TestProviderValidateRejectsUnconfiguredProvider(t *testing.T) {
	dir := t.TempDir()
	vault, err := credentials.Open(filepath.Join(dir, "vault"))
	if err != nil {
		t.Fatal(err)
	}
	p, err := NewControlPlane(filepath.Join(dir, "config.json"), config.Default(), testKey, adminKey, vault)
	if err != nil {
		t.Fatal(err)
	}
	defer p.Close()
	r := httptest.NewRequest("POST", "/admin/providers/missing/validate", nil)
	r.Header.Set("Authorization", "Bearer "+adminKey)
	w := httptest.NewRecorder()
	p.ServeHTTP(w, r)
	if w.Code != 404 || !strings.Contains(w.Body.String(), "unknown configured provider") {
		t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
	}
}

func TestProviderValidateFallsBackToBrowserAccount(t *testing.T) {
	dir := t.TempDir()
	c := config.Default()
	c.Providers = []config.Provider{{ID: "custom", Enabled: true}}
	c.Accounts = []config.Account{{ID: "browser-account", ProviderID: "custom", BrowserProfileID: "profile", QuotaDomain: "quota", MaxInflight: 1, Weight: 1}}
	c.BrowserProfiles = []config.BrowserProfile{{ID: "profile", Enabled: true, Engine: "chrome", CDPURL: "http://127.0.0.1:19998"}}
	vault, err := credentials.Open(filepath.Join(dir, "vault"))
	if err != nil {
		t.Fatal(err)
	}
	p, err := NewControlPlane(filepath.Join(dir, "config.json"), c, testKey, adminKey, vault)
	if err != nil {
		t.Fatal(err)
	}
	defer p.Close()
	r := httptest.NewRequest("POST", "/admin/providers/custom/validate", nil)
	r.Header.Set("Authorization", "Bearer "+adminKey)
	w := httptest.NewRecorder()
	p.ServeHTTP(w, r)
	var result struct {
		Status          string `json:"status"`
		Account         string `json:"account"`
		HistoryRecorded bool   `json:"history_recorded"`
	}
	if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &result) != nil || result.Status != "unsupported" || result.Account != "browser-account" || !result.HistoryRecorded {
		t.Fatalf("status=%d body=%s result=%+v", w.Code, w.Body.String(), result)
	}
}

func TestDiscoveredModelStaysDisabledUntilApproved(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/models" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"data":[{"id":"discovered","owned_by":"fixture"}]}`)
	}))
	defer upstream.Close()
	c := config.Default()
	c.Sources = []config.Source{{ID: "source", Provider: "p", Adapter: "openai", BaseURL: upstream.URL, Local: true, Enabled: true, AutoApproved: true, BillingMode: "free_allowance", MaxInflight: 1, QuotaDomain: "quota", QuotaMaxInflight: 1, Models: []config.Model{{ID: "configured", Upstream: "configured", Protocols: []string{"chat"}, Tier: "silver", RatingBasis: "fixture", Tools: "none", MaxInputBytes: 4096}}}}
	c.Groups[0].Sources = []string{"source"}
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
	discover := httptest.NewRequest("POST", "/admin/sources/source/discover", nil)
	discover.Header.Set("Authorization", "Bearer "+adminKey)
	discovered := httptest.NewRecorder()
	p.ServeHTTP(discovered, discover)
	if discovered.Code != http.StatusOK || !strings.Contains(discovered.Body.String(), `"id":"discovered"`) {
		t.Fatalf("discovery status=%d body=%s", discovered.Code, discovered.Body.String())
	}
	next := p.service.Current().Config
	next.Sources[0].Models = append(next.Sources[0].Models, config.Model{ID: "discovered", Upstream: "discovered", Protocols: []string{"chat"}, Tier: "unrated", Tools: "none", MaxInputBytes: 4096, Enabled: func() *bool { v := false; return &v }(), AutoApproved: func() *bool { v := false; return &v }()})
	body, _ := json.Marshal(map[string]any{"revision": uint64(1), "config": next, "summary": "Configure discovered model"})
	apply := httptest.NewRequest("PATCH", "/admin/config", strings.NewReader(string(body)))
	apply.Header.Set("Authorization", "Bearer "+adminKey)
	response := httptest.NewRecorder()
	p.ServeHTTP(response, apply)
	if response.Code != http.StatusOK {
		t.Fatalf("configure discovered model status=%d body=%s", response.Code, response.Body.String())
	}
	simulate := httptest.NewRequest("POST", "/admin/routing/simulate", strings.NewReader(`{"model":"auto/silver","protocol":"chat","detail":true}`))
	simulate.Header.Set("Authorization", "Bearer "+adminKey)
	simulated := httptest.NewRecorder()
	p.ServeHTTP(simulated, simulate)
	if simulated.Code != http.StatusOK {
		t.Fatalf("simulation status=%d body=%s", simulated.Code, simulated.Body.String())
	}
	var result struct {
		Candidates []struct {
			ID     string `json:"id"`
			Reason string `json:"reason"`
		} `json:"candidates"`
	}
	if err := json.Unmarshal(simulated.Body.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	for _, candidate := range result.Candidates {
		if candidate.ID == "source/discovered" {
			if candidate.Reason != "model_disabled" {
				t.Fatalf("discovered model routing reason=%q", candidate.Reason)
			}
			return
		}
	}
	t.Fatalf("discovered model missing from simulation: %s", simulated.Body.String())
}
