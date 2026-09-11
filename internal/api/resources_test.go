package api

import (
	"bytes"
	"clash-of-tokens/internal/config"
	"clash-of-tokens/internal/credentials"
	"encoding/json"
	"fmt"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
)

func TestPersistentResourcesValidateReferencesAndRevision(t *testing.T) {
	dir := t.TempDir()
	vault, _ := credentials.Open(filepath.Join(dir, "vault"))
	if err := vault.Put("cred://a", "api_key", "test", "secret"); err != nil {
		t.Fatal(err)
	}
	p, err := NewControlPlane(filepath.Join(dir, "config.json"), config.Default(), testKey, adminKey, vault)
	if err != nil {
		t.Fatal(err)
	}
	defer p.Close()
	call := func(method, path, revision, body string) *httptest.ResponseRecorder {
		r := httptest.NewRequest(method, path, strings.NewReader(body))
		r.Header.Set("Authorization", "Bearer "+adminKey)
		if revision != "" {
			r.Header.Set("If-Match", revision)
		}
		w := httptest.NewRecorder()
		p.ServeHTTP(w, r)
		return w
	}
	if w := call("POST", "/admin/providers", "1", `{"id":"p","enabled":true,"auto_approved":false}`); w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	if w := call("POST", "/admin/accounts", "2", `{"id":"a","provider_id":"p","max_inflight":1,"weight":1,"quota_domain":"q","credential_ref":"cred://a"}`); w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	if w := call("PATCH", "/admin/accounts/a", "2", `{"enabled":true}`); w.Code != 409 {
		t.Fatal(w.Code)
	}
	if w := call("PATCH", "/admin/accounts/a", "3", `{"typo":true}`); w.Code != 400 {
		t.Fatal(w.Code)
	}
	if w := call("DELETE", "/admin/providers/p", "3", ""); w.Code != 409 || !strings.Contains(w.Body.String(), "provider still has accounts") {
		t.Fatal("referenced provider deletion was not blocked", w.Code, w.Body.String())
	}
	if w := call("PATCH", "/admin/accounts/a", "3", `{"enabled":true,"auto_approved":true}`); w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	loaded, err := config.Load(filepath.Join(dir, "config.json"))
	if err != nil || !loaded.Accounts[0].Enabled || !loaded.Accounts[0].AutoApproved {
		t.Fatal("account edit lost", err)
	}
	if w := call("GET", "/admin/accounts/a", "", ""); w.Code != 200 || !strings.Contains(w.Body.String(), "cred://a") {
		t.Fatal(w.Code, w.Body.String())
	}
	if w := call("POST", "/admin/routing/simulate", "", `{"model":"auto","protocol":"chat","tools":true,"bytes":120000}`); w.Code != 200 {
		t.Fatal(w.Code)
	}
	var detailed struct {
		Selected   string `json:"selected"`
		Candidates []struct {
			ID       string `json:"id"`
			Order    int    `json:"order"`
			Eligible bool   `json:"eligible"`
			Selected bool   `json:"selected"`
		} `json:"candidates"`
	}
	if w := call("POST", "/admin/routing/simulate", "", `{"model":"auto","protocol":"chat","detail":true}`); w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &detailed) != nil {
		t.Fatalf("detailed simulation failed: %d %s", w.Code, w.Body.String())
	}
	if len(detailed.Candidates) != 0 || detailed.Selected != "" {
		t.Fatalf("unexpected empty-config simulation: %+v", detailed)
	}
}

func TestProviderReadIncludesCatalogAndDescriptor(t *testing.T) {
	dir := t.TempDir()
	vault, err := credentials.Open(filepath.Join(dir, "vault"))
	if err != nil {
		t.Fatal(err)
	}
	c := config.Default()
	c.Providers = []config.Provider{{ID: "openai", Enabled: true, AutoApproved: false, PoolStrategy: "round-robin"}}
	p, err := NewControlPlane(filepath.Join(dir, "config.json"), c, testKey, adminKey, vault)
	if err != nil {
		t.Fatal(err)
	}
	defer p.Close()
	req := httptest.NewRequest("GET", "/admin/providers/openai", nil)
	req.Header.Set("Authorization", "Bearer "+adminKey)
	w := httptest.NewRecorder()
	p.ServeHTTP(w, req)
	if w.Code != 200 {
		t.Fatalf("provider detail status=%d body=%s", w.Code, w.Body.String())
	}
	var result struct {
		Revision uint64          `json:"revision"`
		Item     config.Provider `json:"item"`
		Catalog  struct {
			ID      string `json:"id"`
			Adapter string `json:"adapter"`
		} `json:"catalog"`
		Descriptor struct {
			ID      string `json:"id"`
			Factory string `json:"factory"`
		} `json:"descriptor"`
		Accounts []string `json:"accounts"`
		Sources  []string `json:"sources"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	if result.Revision != 1 || result.Item.ID != "openai" || result.Catalog.ID != "openai" || result.Catalog.Adapter != "openai" || result.Descriptor.ID != "openai" || result.Descriptor.Factory != "http" {
		t.Fatalf("provider detail omitted metadata: %+v", result)
	}
	if len(result.Accounts) != 0 || len(result.Sources) != 0 {
		t.Fatalf("unexpected provider associations: %+v", result)
	}
}

func TestRoutingSimulationDetailReportsSelection(t *testing.T) {
	dir := t.TempDir()
	vault, _ := credentials.Open(filepath.Join(dir, "vault"))
	c := config.Default()
	c.Sources = []config.Source{{
		ID: "sim-source", Provider: "test", Adapter: "openai", BaseURL: "http://127.0.0.1:1",
		Local: true, Enabled: true, AutoApproved: true, BillingMode: "free_allowance", MaxInflight: 1,
		QuotaDomain: "sim-quota", QuotaMaxInflight: 1,
		Models: []config.Model{{ID: "sim-model", Upstream: "sim-model", Protocols: []string{"chat"}, Tier: "silver", RatingBasis: "fixture", Tools: "none", MaxInputBytes: 4096}},
	}}
	c.Groups[0].Sources = []string{"sim-source"}
	p, err := NewControlPlane(filepath.Join(dir, "config.json"), c, testKey, adminKey, vault)
	if err != nil {
		t.Fatal(err)
	}
	defer p.Close()
	req := httptest.NewRequest("POST", "/admin/routing/simulate", strings.NewReader(`{"model":"auto/silver","protocol":"chat","detail":true}`))
	req.Header.Set("Authorization", "Bearer "+adminKey)
	w := httptest.NewRecorder()
	p.ServeHTTP(w, req)
	if w.Code != 200 {
		t.Fatalf("simulation status=%d body=%s", w.Code, w.Body.String())
	}
	var result struct {
		Selected   string `json:"selected"`
		Candidates []struct {
			ID       string `json:"id"`
			Order    int    `json:"order"`
			Reason   string `json:"reason"`
			Eligible bool   `json:"eligible"`
			Selected bool   `json:"selected"`
		} `json:"candidates"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	if result.Selected != "sim-source/sim-model" || len(result.Candidates) != 1 {
		t.Fatalf("unexpected detailed simulation: %+v", result)
	}
	candidate := result.Candidates[0]
	if candidate.ID != result.Selected || candidate.Order != 1 || candidate.Reason != "eligible" || !candidate.Eligible || !candidate.Selected {
		t.Fatalf("unexpected candidate details: %+v", candidate)
	}
}

func TestIndependentSwitchesReachRoutingSimulator(t *testing.T) {
	dir := t.TempDir()
	vault, _ := credentials.Open(filepath.Join(dir, "vault"))
	c := config.Default()
	c.Providers = []config.Provider{{ID: "p", Enabled: true, AutoApproved: true}}
	c.Accounts = []config.Account{{ID: "a", ProviderID: "p", Enabled: true, AutoApproved: true, QuotaDomain: "q", MaxInflight: 1, Weight: 1}}
	c.Sources = []config.Source{{ID: "s", Provider: "p", Adapter: "openai", BaseURL: "http://127.0.0.1:1", Local: true, SourceKind: "vendor_api", InferenceLocation: "remote", BillingMode: "free_allowance", AccountID: "a", Enabled: true, AutoApproved: true, MaxInflight: 1, QuotaDomain: "q", QuotaMaxInflight: 1, Models: []config.Model{{ID: "m", Upstream: "m", Protocols: []string{"chat"}, Tier: "silver", RatingBasis: "fixture", Tools: "none", MaxInputBytes: 4096}}}}
	c.Groups[0].Sources = []string{"s"}
	p, err := NewControlPlane(filepath.Join(dir, "config.json"), c, testKey, adminKey, vault)
	if err != nil {
		t.Fatal(err)
	}
	defer p.Close()
	reason := func() string {
		r := httptest.NewRequest("POST", "/admin/routing/simulate", strings.NewReader(`{"model":"auto","protocol":"chat","detail":true}`))
		r.Header.Set("Authorization", "Bearer "+adminKey)
		w := httptest.NewRecorder()
		p.ServeHTTP(w, r)
		if w.Code != 200 {
			t.Fatalf("simulation status=%d body=%s", w.Code, w.Body.String())
		}
		var out struct {
			Candidates []struct {
				Reason string `json:"reason"`
			} `json:"candidates"`
		}
		if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil || len(out.Candidates) != 1 {
			t.Fatalf("simulation candidates=%s", w.Body.String())
		}
		return out.Candidates[0].Reason
	}
	if got := reason(); got != "eligible" {
		t.Fatalf("initial switch state=%q", got)
	}
	patch := func(path, body string, revision uint64) {
		r := httptest.NewRequest("PATCH", path, strings.NewReader(body))
		r.Header.Set("Authorization", "Bearer "+adminKey)
		r.Header.Set("If-Match", fmt.Sprint(revision))
		w := httptest.NewRecorder()
		p.ServeHTTP(w, r)
		if w.Code != 200 {
			t.Fatalf("patch %s status=%d body=%s", path, w.Code, w.Body.String())
		}
	}
	patch("/admin/providers/p", `{"enabled":false}`, 1)
	if got := reason(); got != "provider_or_account_disabled" {
		t.Fatalf("provider switch reason=%q", got)
	}
	patch("/admin/providers/p", `{"enabled":true}`, 2)
	patch("/admin/accounts/a", `{"enabled":false}`, 3)
	if got := reason(); got != "provider_or_account_disabled" {
		t.Fatalf("account switch reason=%q", got)
	}
	patch("/admin/accounts/a", `{"enabled":true}`, 4)
	patch("/admin/sources/s", `{"enabled":false}`, 5)
	if got := reason(); got != "disabled" {
		t.Fatalf("source switch reason=%q", got)
	}
	patch("/admin/sources/s", `{"enabled":true}`, 6)
	patch("/admin/sources/s", `{"auto_approved":false}`, 7)
	if got := reason(); got != "not_approved" {
		t.Fatalf("source Auto switch reason=%q", got)
	}
	patch("/admin/sources/s", `{"auto_approved":true,"models":[{"id":"m","upstream":"m","protocols":["chat"],"tier":"silver","rating_basis":"fixture","tools":"none","max_input_bytes":4096,"enabled":false}]}`, 8)
	if got := reason(); got != "model_disabled" {
		t.Fatalf("model switch reason=%q", got)
	}
}

func TestAccountQuotaMoveCascadesToMemberSources(t *testing.T) {
	dir := t.TempDir()
	vault, _ := credentials.Open(filepath.Join(dir, "vault"))
	c := config.Default()
	c.Providers = []config.Provider{{ID: "p", Enabled: true}}
	c.Accounts = []config.Account{{ID: "a", ProviderID: "p", Enabled: true, QuotaDomain: "old", MaxInflight: 1, Weight: 1}}
	c.Sources = []config.Source{{ID: "s", Provider: "p", Adapter: "openai", BaseURL: "http://127.0.0.1:1", Local: true, AccountID: "a", Enabled: true, AutoApproved: true, MaxInflight: 1, QuotaDomain: "old", QuotaMaxInflight: 1, Models: []config.Model{{ID: "m", Upstream: "m", Protocols: []string{"chat"}, Tier: "unrated", Tools: "none", MaxInputBytes: 1024}}}}
	p, err := NewControlPlane(filepath.Join(dir, "config.json"), c, testKey, adminKey, vault)
	if err != nil {
		t.Fatal(err)
	}
	defer p.Close()
	body := bytes.NewBufferString(`{"quota_domain":"new"}`)
	req := httptest.NewRequest("PATCH", "/admin/accounts/a", body)
	req.Header.Set("Authorization", "Bearer "+adminKey)
	req.Header.Set("If-Match", "1")
	w := httptest.NewRecorder()
	p.ServeHTTP(w, req)
	if w.Code != 200 {
		t.Fatalf("quota move status=%d body=%s", w.Code, w.Body.String())
	}
	loaded, err := config.Load(filepath.Join(dir, "config.json"))
	if err != nil {
		t.Fatal(err)
	}
	if loaded.Accounts[0].QuotaDomain != "new" || loaded.Sources[0].QuotaDomain != "new" {
		t.Fatalf("quota move did not cascade: account=%q source=%q", loaded.Accounts[0].QuotaDomain, loaded.Sources[0].QuotaDomain)
	}
	var response map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &response); err != nil || response["status"] != "saved" {
		t.Fatalf("invalid response: %s", w.Body.String())
	}
}

func TestSourceQuotaMoveCascadesToAccountAndMembers(t *testing.T) {
	dir := t.TempDir()
	vault, _ := credentials.Open(filepath.Join(dir, "vault"))
	c := config.Default()
	c.Providers = []config.Provider{{ID: "p", Enabled: true}}
	c.Accounts = []config.Account{
		{ID: "a", ProviderID: "p", Enabled: true, QuotaDomain: "old", MaxInflight: 1, Weight: 1},
		{ID: "b", ProviderID: "p", Enabled: true, QuotaDomain: "other", MaxInflight: 1, Weight: 1},
	}
	model := config.Model{ID: "m", Upstream: "m", Protocols: []string{"chat"}, Tier: "unrated", Tools: "none", MaxInputBytes: 1024}
	newSource := func(id, account, domain string) config.Source {
		return config.Source{ID: id, Provider: "p", Adapter: "openai", BaseURL: "http://127.0.0.1:1", Local: true, AccountID: account, Enabled: true, AutoApproved: true, MaxInflight: 1, QuotaDomain: domain, QuotaMaxInflight: 1, Models: []config.Model{model}}
	}
	c.Sources = []config.Source{newSource("s1", "a", "old"), newSource("s2", "a", "old"), newSource("s3", "b", "other")}
	p, err := NewControlPlane(filepath.Join(dir, "config.json"), c, testKey, adminKey, vault)
	if err != nil {
		t.Fatal(err)
	}
	defer p.Close()
	call := func(path, body string, revision uint64) *httptest.ResponseRecorder {
		req := httptest.NewRequest("PATCH", path, strings.NewReader(body))
		req.Header.Set("Authorization", "Bearer "+adminKey)
		req.Header.Set("If-Match", fmt.Sprint(revision))
		w := httptest.NewRecorder()
		p.ServeHTTP(w, req)
		return w
	}
	deleteReq := httptest.NewRequest("DELETE", "/admin/accounts/a", nil)
	deleteReq.Header.Set("Authorization", "Bearer "+adminKey)
	deleteReq.Header.Set("If-Match", "1")
	deleteResponse := httptest.NewRecorder()
	p.ServeHTTP(deleteResponse, deleteReq)
	if deleteResponse.Code != 409 || !strings.Contains(deleteResponse.Body.String(), "account still has sources") {
		t.Fatalf("bound account deletion was not blocked: status=%d body=%s", deleteResponse.Code, deleteResponse.Body.String())
	}
	if w := call("/admin/sources/s1", `{"quota_domain":"new"}`, 1); w.Code != 200 {
		t.Fatalf("source quota move status=%d body=%s", w.Code, w.Body.String())
	}
	loaded, err := config.Load(filepath.Join(dir, "config.json"))
	if err != nil {
		t.Fatal(err)
	}
	if loaded.Accounts[0].QuotaDomain != "new" || loaded.Sources[0].QuotaDomain != "new" || loaded.Sources[1].QuotaDomain != "new" || loaded.Sources[2].QuotaDomain != "other" {
		t.Fatalf("source quota move did not preserve domains: accounts=%+v sources=%+v", loaded.Accounts, loaded.Sources)
	}
	if w := call("/admin/sources/s2", `{"account_id":"b"}`, 2); w.Code != 200 {
		t.Fatalf("source account move status=%d body=%s", w.Code, w.Body.String())
	}
	loaded, err = config.Load(filepath.Join(dir, "config.json"))
	if err != nil {
		t.Fatal(err)
	}
	if loaded.Sources[1].AccountID != "b" || loaded.Sources[1].QuotaDomain != "other" || loaded.Accounts[1].QuotaDomain != "other" {
		t.Fatalf("source account move did not adopt destination domain: accounts=%+v sources=%+v", loaded.Accounts, loaded.Sources)
	}
}

func TestBrowserProfileDeletionGuard(t *testing.T) {
	dir := t.TempDir()
	vault, _ := credentials.Open(filepath.Join(dir, "vault"))
	c := config.Default()
	c.Providers = []config.Provider{{ID: "p", Enabled: true}}
	c.BrowserProfiles = []config.BrowserProfile{{ID: "profile", Enabled: true, Engine: "chrome", CDPURL: "http://127.0.0.1:9223"}}
	c.Accounts = []config.Account{{ID: "a", ProviderID: "p", BrowserProfileID: "profile", Enabled: true, QuotaDomain: "q", MaxInflight: 1, Weight: 1}}
	p, err := NewControlPlane(filepath.Join(dir, "config.json"), c, testKey, adminKey, vault)
	if err != nil {
		t.Fatal(err)
	}
	defer p.Close()
	req := httptest.NewRequest("DELETE", "/admin/browser_profiles/profile", nil)
	req.Header.Set("Authorization", "Bearer "+adminKey)
	req.Header.Set("If-Match", "1")
	w := httptest.NewRecorder()
	p.ServeHTTP(w, req)
	if w.Code != 409 || !strings.Contains(w.Body.String(), "browser profile still has accounts") {
		t.Fatalf("bound profile deletion was not blocked: status=%d body=%s", w.Code, w.Body.String())
	}
}

func TestSourceDeletionGuard(t *testing.T) {
	dir := t.TempDir()
	vault, _ := credentials.Open(filepath.Join(dir, "vault"))
	c := config.Default()
	c.Providers = []config.Provider{{ID: "p", Enabled: true}}
	c.Sources = []config.Source{{ID: "s", Provider: "p", Adapter: "openai", BaseURL: "http://127.0.0.1:1", Local: true, Enabled: true, AutoApproved: true, MaxInflight: 1, QuotaDomain: "q", QuotaMaxInflight: 1, Models: []config.Model{{ID: "m", Upstream: "m", Protocols: []string{"chat"}, Tier: "unrated", Tools: "none", MaxInputBytes: 1024}}}}
	c.Groups = []config.Group{{ID: "g", Type: "auto", Sources: []string{"s"}, MinTier: "bronze", AllowUnrated: true}}
	p, err := NewControlPlane(filepath.Join(dir, "config.json"), c, testKey, adminKey, vault)
	if err != nil {
		t.Fatal(err)
	}
	defer p.Close()
	req := httptest.NewRequest("DELETE", "/admin/sources/s", nil)
	req.Header.Set("Authorization", "Bearer "+adminKey)
	req.Header.Set("If-Match", "1")
	w := httptest.NewRecorder()
	p.ServeHTTP(w, req)
	if w.Code != 409 || !strings.Contains(w.Body.String(), "source still belongs to groups") {
		t.Fatalf("group-bound source deletion was not blocked: status=%d body=%s", w.Code, w.Body.String())
	}
}
