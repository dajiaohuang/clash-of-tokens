package api

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"clash-of-tokens/internal/config"
	"clash-of-tokens/internal/credentials"
)

func TestAcquiredSessionBindingCompensatesAndSurvivesRestart(t *testing.T) {
	for _, diskFailure := range []bool{false, true} {
		t.Run(map[bool]string{false: "restart", true: "disk_failure"}[diskFailure], func(t *testing.T) {
			dir := t.TempDir()
			vault, err := credentials.Open(filepath.Join(dir, "vault"))
			if err != nil {
				t.Fatal(err)
			}
			if err = vault.Put("cred://login", "username_password", "fixture", `{"username":"person","password":"canary-password"}`); err != nil {
				t.Fatal(err)
			}
			c := config.Default()
			c.Providers = []config.Provider{{ID: "claude-web", Enabled: true}}
			c.Accounts = []config.Account{{ID: "a", ProviderID: "claude-web", LoginCredentialRef: "cred://login", QuotaDomain: "a", MaxInflight: 1, Weight: 1}}
			path := filepath.Join(dir, "config.json")
			p, err := NewControlPlane(path, c, testKey, adminKey, vault)
			if err != nil {
				t.Fatal(err)
			}
			defer p.Close()
			if diskFailure {
				if err = os.Mkdir(path+".state", 0700); err != nil {
					t.Fatal(err)
				}
			}
			req := p.operationRequest(httptest.NewRequest("POST", "/admin/accounts/a/acquire-session", nil))
			var meta credentials.Metadata
			err = commitOperation(req, func() error {
				var e error
				_, meta, e = p.bindAcquiredSession(1, "a", "sessionKey=canary-session")
				return e
			})
			if diskFailure {
				if err == nil || len(vault.List()) != 1 || p.service.Current().Revision != 1 {
					t.Fatal("failed commit left binding or orphan", err, vault.List())
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			p.Close()
			vault, err = credentials.Open(filepath.Join(dir, "vault"))
			if err != nil {
				t.Fatal(err)
			}
			p2, err := NewControlPlane(path, c, testKey, adminKey, vault)
			if err != nil {
				t.Fatal(err)
			}
			defer p2.Close()
			a := p2.service.Current().Config.Accounts[0]
			if a.CredentialRef != meta.ID || a.LoginCredentialRef != "cred://login" || vault.Resolve(a.CredentialRef) != "sessionKey=canary-session" {
				t.Fatal("session or login binding lost")
			}
			stale := commitOperation(req, func() error { _, _, e := p2.bindAcquiredSession(1, "a", "wrong"); return e })
			if stale == nil || len(vault.List()) != 2 {
				t.Fatal("stale commit wrote credential")
			}
		})
	}
}

func TestCapturedCookieVerifiesExactTenant(t *testing.T) {
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/organizations" || r.Header.Get("Cookie") != "sessionKey=fixture" {
			t.Error("wrong verification request")
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`[{"uuid":"expected"}]`))
	}))
	defer up.Close()
	if err := verifyClaudeTenant(context.Background(), up.Client(), up.URL, "sessionKey=fixture", "expected"); err != nil {
		t.Fatal(err)
	}
	if err := verifyClaudeTenant(context.Background(), up.Client(), up.URL, "sessionKey=fixture", "different"); err == nil {
		t.Fatal("wrong tenant accepted")
	}
}
