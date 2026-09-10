package api

import (
	"clash-of-tokens/internal/config"
	"clash-of-tokens/internal/credentials"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
)

func TestBrowserStatusReportsConfiguredBindingsAndCapacity(t *testing.T) {
	cdp := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/json/version":
			_, _ = io.WriteString(w, `{"Browser":"Chrome/Test","Protocol-Version":"1.3"}`)
		case "/json/list":
			_, _ = io.WriteString(w, `[{"type":"page"},{"type":"background_page"}]`)
		default:
			http.NotFound(w, r)
		}
	}))
	defer cdp.Close()

	c := config.Default()
	c.Browser.MaxSessions = 8
	c.BrowserProfiles = []config.BrowserProfile{{ID: "work", Enabled: true, Engine: "chrome", CDPURL: cdp.URL}}
	c.Providers = []config.Provider{{ID: "p", Enabled: true, AutoApproved: true, PoolStrategy: "round-robin"}}
	c.Accounts = []config.Account{
		{ID: "a", ProviderID: "p", BrowserProfileID: "work", Enabled: true, AutoApproved: true, QuotaDomain: "qa", MaxInflight: 1, Weight: 1},
		{ID: "b", ProviderID: "p", BrowserProfileID: "work", Enabled: true, AutoApproved: true, QuotaDomain: "qb", MaxInflight: 1, Weight: 1},
	}
	c.Sources = []config.Source{
		{ID: "s1", Provider: "p", Adapter: "openai", AccountID: "a", BaseURL: "https://example.com", KeyEnv: "COT_TEST_KEY", Enabled: false, MaxInflight: 1, QuotaDomain: "qa", QuotaMaxInflight: 1, Models: []config.Model{{ID: "m", Upstream: "m", Protocols: []string{"chat"}, Tier: "unrated", Tools: "native", MaxInputBytes: 1}}},
		{ID: "s2", Provider: "p", Adapter: "openai", AccountID: "b", BaseURL: "https://example.com", KeyEnv: "COT_TEST_KEY_2", Enabled: false, MaxInflight: 1, QuotaDomain: "qb", QuotaMaxInflight: 1, Models: []config.Model{{ID: "m", Upstream: "m", Protocols: []string{"chat"}, Tier: "unrated", Tools: "native", MaxInputBytes: 1}}},
	}
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
	r := httptest.NewRequest("POST", "/admin/browser_profiles/status", strings.NewReader("{}"))
	r.Header.Set("Authorization", "Bearer "+adminKey)
	w := httptest.NewRecorder()
	p.ServeHTTP(w, r)
	if w.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
	}
	var rows []struct {
		State         string `json:"state"`
		Pages         int    `json:"pages"`
		MaxSessions   int    `json:"max_sessions"`
		BoundAccounts int    `json:"bound_accounts"`
		BoundSources  int    `json:"bound_sources"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &rows); err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 || rows[0].State != "connected" || rows[0].Pages != 1 || rows[0].MaxSessions != 8 || rows[0].BoundAccounts != 2 || rows[0].BoundSources != 2 {
		t.Fatalf("unexpected browser capacity view: %+v", rows)
	}
}

func TestProfileBindingsIgnoreUnboundSources(t *testing.T) {
	c := config.Config{Accounts: []config.Account{{ID: "a", BrowserProfileID: "p"}}, Sources: []config.Source{{ID: "s", AccountID: "other"}}}
	accounts, sources := profileBindings(c, "p")
	if accounts != 1 || sources != 0 {
		t.Fatalf("unexpected bindings: accounts=%d sources=%d", accounts, sources)
	}
}
