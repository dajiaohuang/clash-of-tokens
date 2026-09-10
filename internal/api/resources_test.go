package api

import (
	"clash-of-tokens/internal/config"
	"clash-of-tokens/internal/credentials"
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
