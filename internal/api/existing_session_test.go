package api

import (
	"clash-of-tokens/internal/config"
	"context"
	"encoding/json"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestSessionPlanIncludesUnconfiguredProviders(t *testing.T) {
	p := loginBatchFixture(t)
	sites := p.loginSites(nil)
	if len(sites) < 100 {
		t.Fatal("scan is restricted to accounts")
	}
	for _, s := range sites {
		if s.Adapter == "chatgpt-web" {
			if s.State != "queued" {
				t.Fatal("chatgpt missing")
			}
			return
		}
	}
	t.Fatal("catalog provider not covered")
}
func TestCapturedCookieIsEncryptedOwnedAndUnverified(t *testing.T) {
	p := loginBatchFixture(t)
	c := loginConnection{ID: "fixture", Browser: "chrome", Ready: true, Endpoint: "ws://127.0.0.1:19999/devtools/browser/fixture-connection"}
	site := p.loginSites([]string{"claude-web"})
	if len(site) == 0 { // Catalog uses product id, not adapter id.
		for _, s := range p.loginSites(nil) {
			if s.Adapter == "claude-web" {
				site = append(site, s)
				break
			}
		}
	}
	if len(site) != 1 {
		t.Fatal("missing fixture provider")
	}
	expected := p.operationVersion()
	id, err := p.saveScannedSession(context.Background(), c, site[0], "sessionKey=fixture-secret", false, &expected)
	if err != nil {
		t.Fatal(err)
	}
	current := p.service.Current().Config
	var a config.Account
	for _, v := range current.Accounts {
		if v.ID == id {
			a = v
		}
	}
	if a.ID == "" || a.Enabled || a.VerificationState != "unverified" || a.CredentialRef == "" {
		t.Fatal("candidate enabled or not bound")
	}
	if !current.BrowserProfiles[0].External {
		t.Fatal("personal browser treated as owned process")
	}
	raw, _ := json.Marshal(current)
	if strings.Contains(string(raw), "fixture-secret") {
		t.Fatal("secret leaked to config")
	}
	again, err := p.saveScannedSession(context.Background(), c, site[0], "sessionKey=updated-fixture", false, &expected)
	if err != nil || again != id || len(p.service.Current().Config.Accounts) != 2 {
		t.Fatal("repeat capture duplicated accounts", err)
	}
}
func TestExistingProfileCannotBeLaunched(t *testing.T) {
	p := loginBatchFixture(t)
	next := p.service.Current().Config
	next.BrowserProfiles = []config.BrowserProfile{{ID: "external", External: true, Engine: "chrome", CDPURL: "ws://127.0.0.1:19999/devtools/browser/fixture-connection", Enabled: true}}
	next.Accounts[0].BrowserProfileID = "external"
	if _, err := p.service.Apply(p.service.Current().Revision, next, "fixture"); err != nil {
		t.Fatal(err)
	}
	// HTTP launch is explicitly forbidden; no executable is even resolved.
	w := httptest.NewRecorder()
	p.browserLoginAdmin(w, httptest.NewRequest("POST", "/admin/accounts/a/login", nil))
	if w.Code != 409 {
		t.Fatal(w.Code)
	}
	if len(p.browsers.list()) != 0 {
		t.Fatal("external browser was launched")
	}
}

func TestCanceledCaptureDoesNotPersist(t *testing.T) {
	p := loginBatchFixture(t)
	expected := p.operationVersion()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := p.saveScannedSession(ctx, loginConnection{}, loginSite{}, "fixture", false, &expected)
	if err != context.Canceled || expected != p.operationVersion() {
		t.Fatal("canceled capture changed configuration")
	}
}
