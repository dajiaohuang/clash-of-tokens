package api

import (
	"clash-of-tokens/internal/config"
	"clash-of-tokens/internal/credentials"
	audit "clash-of-tokens/internal/evidence"
	"encoding/json"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
)

func TestLoginChecksAreAuthorizedAndPersistEvidence(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.json")
	c := config.Default()
	c.Providers = []config.Provider{{ID: "custom"}}
	c.Accounts = []config.Account{{ID: "account", ProviderID: "custom", BrowserProfileID: "profile", QuotaDomain: "quota", MaxInflight: 1, Weight: 1}}
	c.BrowserProfiles = []config.BrowserProfile{{ID: "profile", Enabled: true, Engine: "chrome", CDPURL: "http://127.0.0.1:19999"}}
	vault, err := credentials.Open(filepath.Join(dir, "vault"))
	if err != nil {
		t.Fatal(err)
	}
	p, err := NewControlPlane(path, c, testKey, adminKey, vault)
	if err != nil {
		t.Fatal(err)
	}
	defer p.Close()
	call := func(key, origin string) *httptest.ResponseRecorder {
		r := httptest.NewRequest("POST", "/admin/accounts/account/check-login", nil)
		r.Header.Set("Authorization", "Bearer "+key)
		if origin != "" {
			r.Header.Set("Origin", origin)
		}
		w := httptest.NewRecorder()
		p.ServeHTTP(w, r)
		return w
	}
	if w := call("wrong", ""); w.Code != 401 {
		t.Fatal(w.Code)
	}
	if w := call(adminKey, "https://other.example"); w.Code != 403 {
		t.Fatal(w.Code)
	}
	if len(p.evidence.List()) != 0 {
		t.Fatal("unauthorized check produced evidence")
	}
	w := call(adminKey, "")
	var result struct {
		Status          string `json:"status"`
		HistoryRecorded bool   `json:"history_recorded"`
		Revision        uint64 `json:"revision"`
	}
	if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &result) != nil || result.Status != "unsupported" || !result.HistoryRecorded || result.Revision != p.service.Current().Revision {
		t.Fatal(w.Code, w.Body.String())
	}
	stored, err := audit.Open(path + ".evidence")
	if err != nil {
		t.Fatal(err)
	}
	rows := stored.List()
	if len(rows) != 1 || rows[0].Resource != "account" || rows[0].Kind != "authentication" || rows[0].Status != "unsupported" {
		t.Fatal(rows)
	}
}

func TestLoginArgumentsUseDedicatedDirectory(t *testing.T) {
	root := t.TempDir()
	args, err := loginArguments(config.BrowserProfile{ID: "personal", CDPURL: "http://127.0.0.1:9223"}, filepath.Join(root, "sessions.json"), "https://chatgpt.com/backend-api/conversation")
	if err != nil {
		t.Fatal(err)
	}
	if args[len(args)-1] != "https://chatgpt.com/" || !strings.Contains(strings.Join(args, "\n"), filepath.Join(root, "profiles", "personal", "browser-data")) {
		t.Fatal(args)
	}
	if _, err := loginArguments(config.BrowserProfile{}, "state", "file:///secret"); err == nil {
		t.Fatal("unsafe destination accepted")
	}
}

func TestDraftBrowserCheckDoesNotSaveAccount(t *testing.T) {
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
	body := `{"profile":{"id":"draft","enabled":true,"engine":"chrome","cdp_url":"http://127.0.0.1:19999"},"provider":"openai","action":"check"}`
	r := httptest.NewRequest("POST", "/admin/browser_profiles/setup-login", strings.NewReader(body))
	r.Header.Set("Authorization", "Bearer "+adminKey)
	w := httptest.NewRecorder()
	p.ServeHTTP(w, r)
	if w.Code != 200 || !strings.Contains(w.Body.String(), `"status":"unsupported"`) {
		t.Fatal(w.Code, w.Body.String())
	}
	c := p.service.Current()
	if c.Revision != 1 || len(c.Config.Accounts) != 0 || len(c.Config.BrowserProfiles) != 0 || len(p.evidence.List()) != 0 {
		t.Fatal("draft check persisted configuration/evidence")
	}
	bad := strings.Replace(body, "127.0.0.1", "192.0.2.1", 1)
	r = httptest.NewRequest("POST", "/admin/browser_profiles/setup-login", strings.NewReader(bad))
	r.Header.Set("Authorization", "Bearer "+adminKey)
	w = httptest.NewRecorder()
	p.ServeHTTP(w, r)
	if w.Code != 400 {
		t.Fatal("nonlocal profile accepted", w.Code)
	}
}
