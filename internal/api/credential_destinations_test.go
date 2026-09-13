package api

import (
	"encoding/json"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"clash-of-tokens/internal/config"
	"clash-of-tokens/internal/credentials"
)

func TestCredentialDestinationChangeRequiresConfirmation(t *testing.T) {
	dir := t.TempDir()
	vault, err := credentials.Open(filepath.Join(dir, "vault"))
	if err != nil {
		t.Fatal(err)
	}
	if err = vault.Put("cred://key", "api_key", "fixture", "canary-key"); err != nil {
		t.Fatal(err)
	}
	c := config.Default()
	c.Providers = []config.Provider{{ID: "openai"}}
	c.Accounts = []config.Account{{ID: "a", ProviderID: "openai", CredentialRef: "cred://key", BaseURL: "https://api.openai.com/v1", QuotaDomain: "a", MaxInflight: 1, Weight: 1}}
	p, err := NewControlPlane(filepath.Join(dir, "config.json"), c, testKey, adminKey, vault)
	if err != nil {
		t.Fatal(err)
	}
	defer p.Close()
	next, _ := config.Clone(c)
	next.Accounts[0].BaseURL = "https://different.example/v1"
	request := func(method, path, body string, confirm bool) *httptest.ResponseRecorder {
		r := httptest.NewRequest(method, path, strings.NewReader(body))
		r.Header.Set("Authorization", "Bearer "+adminKey)
		r.Header.Set("If-Match", "1")
		if confirm {
			r.Header.Set("X-COT-Confirm-Credential-Destinations", "true")
		}
		w := httptest.NewRecorder()
		p.ServeHTTP(w, r)
		return w
	}
	if w := request("PATCH", "/admin/accounts/a", `{"base_url":"https://different.example/v1"}`, false); w.Code != 409 {
		t.Fatal(w.Code, w.Body.String())
	}
	for _, confirm := range []bool{false, true} {
		body, _ := json.Marshal(map[string]any{"revision": 1, "config": next, "confirm_credential_destinations": confirm})
		w := request("POST", "/admin/config/preview", string(body), false)
		if w.Code != 200 || !strings.Contains(w.Body.String(), "credential_destinations") || strings.Contains(w.Body.String(), "canary-key") {
			t.Fatal(w.Code, w.Body.String())
		}
		w = request("PATCH", "/admin/config", string(body), false)
		want := 409
		if confirm {
			want = 200
		}
		if w.Code != want {
			t.Fatal(w.Code, w.Body.String())
		}
	}
	if p.service.Current().Revision != 2 {
		t.Fatal("unexpected revision")
	}
	rollback := request("POST", "/admin/config/rollback", `{"revision":2,"target_revision":1}`, false)
	if rollback.Code != 409 {
		t.Fatal("rollback bypassed destination confirmation")
	}
}
