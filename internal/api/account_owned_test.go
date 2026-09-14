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

	"clash-of-tokens/internal/config"
	"clash-of-tokens/internal/credentials"
)

func TestOwnedAccountMaterialAndFourStates(t *testing.T) {
	dir := t.TempDir()
	c := config.Default()
	c.Providers = []config.Provider{{ID: "deepseek", Enabled: true}}
	c.Accounts = []config.Account{{ID: "fixture", ProviderID: "deepseek", QuotaDomain: "fixture", MaxInflight: 1, Weight: 1}}
	store, _, err := credentials.OpenProviderAccounts(dir, c)
	if err != nil {
		t.Fatal(err)
	}
	if err = store.PutAccount(credentials.AccountOwner{Account: "fixture", Provider: "deepseek"}, "cred://fixture", "username_password", `{"username":"fixture-user","password":"fixture-secret"}`); err != nil {
		t.Fatal(err)
	}
	if err = store.SetAccountDomain("cred://fixture", "platform.deepseek.com"); err != nil {
		t.Fatal(err)
	}
	c.Accounts[0].LoginCredentialRef = "cred://fixture"
	p, err := NewControlPlane(filepath.Join(dir, "config.json"), c, "", "", store)
	if err != nil {
		t.Fatal(err)
	}
	defer p.Close()
	a := p.service.Current().Config.Accounts[0]
	if p.accountState(a) != "unverified" || a.Enabled {
		t.Fatal("unverified import enabled")
	}
	empty := config.Account{ID: "empty"}
	if p.accountState(empty) != "unfilled" {
		t.Fatal("empty state")
	}
	a.VerificationState = "verified"
	a.VerificationVersion = p.credentialMetadata(a.LoginCredentialRef).Version
	a.VerificationBinding = p.accountProof(a)
	if p.accountState(a) != "verified" {
		t.Fatal("verified state")
	}
	a.VerificationState = "invalid"
	if p.accountState(a) != "invalid" {
		t.Fatal("invalid state")
	}
	a.VerificationVersion++
	if p.accountState(a) != "unverified" {
		t.Fatal("stale proof reused")
	}
	w := httptest.NewRecorder()
	p.ServeHTTP(w, httptest.NewRequest("POST", "/admin/accounts/"+a.ID+"/verify", strings.NewReader(`{}`)))
	if w.Code != 200 || !strings.Contains(w.Body.String(), `"state":"unverified"`) {
		t.Fatal("unsupported verification not unverified", w.Code, w.Body.String())
	}
	w = httptest.NewRecorder()
	p.ServeHTTP(w, httptest.NewRequest("PUT", "/admin/credentials/raw", strings.NewReader(`{"kind":"api_key","value":"secret"}`)))
	if w.Code != 410 {
		t.Fatal("independent API still accepts writes", w.Code)
	}
	material, _ := json.Marshal(map[string]string{"kind": "username_password", "username": "new-user", "password": "new-secret"})
	w = httptest.NewRecorder()
	p.ServeHTTP(w, httptest.NewRequest("POST", "/admin/accounts/"+a.ID+"/material", bytes.NewReader(material)))
	if w.Code != 200 || strings.Contains(w.Body.String(), "new-secret") {
		t.Fatal("account material save failed or leaked", w.Code, w.Body.String())
	}
	loaded, err := config.Load(filepath.Join(dir, "config.json"))
	if err != nil {
		t.Fatal(err)
	}
	b, _ := json.Marshal(loaded)
	if bytes.Contains(b, []byte("new-secret")) || bytes.Contains(b, []byte("fixture-user")) {
		t.Fatal("plaintext in config")
	}
}

func TestLoginFillNeverSubmitsAndGuardsOrigin(t *testing.T) {
	script := loginFillScript("https://example.test", "fixture-user", "fixture-secret")
	for _, guard := range []string{"location.origin!==origin", "new URL(f.action||location.href).origin!==origin", "ps.length!==1", "us.length!==1"} {
		if !strings.Contains(script, guard) {
			t.Fatal("missing fill guard", guard)
		}
	}
	if strings.Contains(script, ".submit(") || strings.Contains(script, ".click(") {
		t.Fatal("fill submits form")
	}
}

func TestAccountVerificationEnablesAndInvalidates(t *testing.T) {
	works := true
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !works {
			w.WriteHeader(401)
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{\"content\":\"OK\"}}]}\n\ndata: [DONE]\n\n")
	}))
	defer up.Close()
	c := config.Default()
	c.Providers = []config.Provider{{ID: "provider", Enabled: true}}
	c.Accounts = []config.Account{{ID: "account", ProviderID: "provider", QuotaDomain: "quota", MaxInflight: 1, Weight: 1}}
	c.Sources = []config.Source{{ID: "source", Provider: "provider", AccountID: "account", Adapter: "openai", BaseURL: up.URL, Local: true, MaxInflight: 1, QuotaDomain: "quota", QuotaMaxInflight: 1, Models: []config.Model{{ID: "model", Upstream: "model", Protocols: []string{"chat"}, Tier: "unrated", Tools: "none", MaxInputBytes: 1024}}}}
	dir := t.TempDir()
	store, _, err := credentials.OpenProviderAccounts(dir, c)
	if err != nil {
		t.Fatal(err)
	}
	p, err := NewControlPlane(filepath.Join(dir, "config.json"), c, "", "", store)
	if err != nil {
		t.Fatal(err)
	}
	defer p.Close()
	check := func(want string, enabled bool) {
		w := httptest.NewRecorder()
		p.ServeHTTP(w, httptest.NewRequest("POST", "/admin/accounts/account/verify", strings.NewReader(`{}`)))
		if w.Code != 200 {
			t.Fatal(w.Code, w.Body.String())
		}
		a := p.service.Current().Config.Accounts[0]
		if p.accountState(a) != want || a.Enabled != enabled {
			t.Fatal("state mismatch", p.accountState(a), a.Enabled, w.Body.String())
		}
	}
	check("verified", true)
	works = false
	check("invalid", false)
}
