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
	if w := call("DELETE", "/admin/providers/p", "3", ""); w.Code != 400 {
		t.Fatal("deleted referenced provider", w.Code)
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
