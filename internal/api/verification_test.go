package api

import (
	"clash-of-tokens/internal/config"
	"clash-of-tokens/internal/credentials"
	audit "clash-of-tokens/internal/evidence"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestVerificationBindingTracksCredentialAndRuntime(t *testing.T) {
	p := &ControlPlane{runtimeID: "first-run"}
	c := config.Default()
	source := config.Source{ID: "source", Adapter: "openai", BaseURL: "https://example.com", CredentialRef: "cred://key"}
	metadata := credentials.Metadata{ID: "cred://key", Version: 1, CreatedAt: time.Unix(1, 0), UpdatedAt: time.Unix(1, 0)}
	original := p.bindingWithMetadata(c, source, metadata)
	p.runtimeID = "second-run"
	if original != p.bindingWithMetadata(c, source, metadata) {
		t.Fatal("protected binding changed just because of restart")
	}
	metadata.Version++
	if original == p.bindingWithMetadata(c, source, metadata) {
		t.Fatal("credential replacement did not invalidate binding")
	}
	metadata.Version--
	source.BaseURL = "https://another.example.com"
	if original == p.bindingWithMetadata(c, source, metadata) {
		t.Fatal("upstream change did not invalidate binding")
	}
	source.CredentialRef = ""
	source.KeyEnv = "COT_SYNTHETIC_KEY"
	envBinding := p.bindingWithMetadata(c, source, credentials.Metadata{})
	p.runtimeID = "third-run"
	if envBinding == p.bindingWithMetadata(c, source, credentials.Metadata{}) {
		t.Fatal("environment evidence survived a restart without a credential version")
	}
}

func TestControlStatusAggregatesAccountHealthAndEvidence(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusNotImplemented) }))
	defer upstream.Close()
	c := config.Default()
	c.Providers = []config.Provider{{ID: "p", Enabled: true}, {ID: "disabled-provider", Enabled: false}}
	c.Accounts = []config.Account{{ID: "a", ProviderID: "p", DisplayName: "Account", Enabled: true, MaxInflight: 1, Weight: 1, QuotaDomain: "q"}}
	c.Accounts = append(c.Accounts, config.Account{ID: "b", ProviderID: "p", DisplayName: "Needs login", Enabled: true, MaxInflight: 1, Weight: 1, QuotaDomain: "q-b"})
	c.Accounts = append(c.Accounts, config.Account{ID: "c", ProviderID: "disabled-provider", DisplayName: "Provider disabled", Enabled: true, MaxInflight: 1, Weight: 1, QuotaDomain: "q-c"})
	c.Sources = []config.Source{{ID: "s", Provider: "p", Adapter: "openai", BaseURL: upstream.URL, Local: true, Enabled: true, MaxInflight: 1, AccountID: "a", QuotaDomain: "q", QuotaMaxInflight: 1, Models: []config.Model{{ID: "m", Upstream: "m", Protocols: []string{"chat"}, Tier: "unrated", Tools: "none", MaxInputBytes: 1024}}}}
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
	checked := time.Now().UTC().Add(-time.Minute)
	if err := p.evidence.Append(audit.Entry{Kind: "validation", Resource: "s", Status: "verified", CheckedAt: checked}); err != nil {
		t.Fatal(err)
	}
	if err := p.evidence.Append(audit.Entry{Kind: "authentication", Resource: "a", Status: "authenticated", CheckedAt: checked}); err != nil {
		t.Fatal(err)
	}
	if err := p.evidence.Append(audit.Entry{Kind: "authentication", Resource: "b", Status: "login_required", CheckedAt: checked}); err != nil {
		t.Fatal(err)
	}
	r := httptest.NewRequest("GET", "/admin/status", strings.NewReader(""))
	r.Header.Set("Authorization", "Bearer "+adminKey)
	w := httptest.NewRecorder()
	p.ServeHTTP(w, r)
	if w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	var out struct {
		Providers []struct {
			ID            string     `json:"id"`
			Health        string     `json:"health"`
			AuthStatus    string     `json:"auth_status"`
			Accounts      []string   `json:"accounts"`
			Sources       []string   `json:"sources"`
			LastValidated *time.Time `json:"last_validated_at"`
		} `json:"provider_health"`
		Accounts []struct {
			ID            string     `json:"id"`
			Health        string     `json:"health"`
			AuthStatus    string     `json:"auth_status"`
			Active        int        `json:"active"`
			Limit         int        `json:"limit"`
			Sources       []string   `json:"sources"`
			LastValidated *time.Time `json:"last_validated_at"`
		} `json:"account_health"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	if len(out.Accounts) != 3 {
		t.Fatalf("account health rows = %+v", out.Accounts)
	}
	if len(out.Providers) != 2 {
		t.Fatalf("provider health rows = %+v", out.Providers)
	}
	if out.Providers[0].ID != "p" || out.Providers[0].Health != "auth_required" || out.Providers[0].AuthStatus != "auth_required" || len(out.Providers[0].Accounts) != 2 || len(out.Providers[0].Sources) != 1 {
		t.Fatalf("unexpected provider health: %+v", out.Providers[0])
	}
	if out.Providers[0].LastValidated == nil || !out.Providers[0].LastValidated.Equal(checked) {
		t.Fatalf("provider validation timestamp missing: %+v", out.Providers[0].LastValidated)
	}
	if out.Providers[1].ID != "disabled-provider" || out.Providers[1].Health != "disabled" {
		t.Fatalf("disabled provider was not classified: %+v", out.Providers[1])
	}
	a := out.Accounts[0]
	if a.ID != "a" || a.Health != "untested" || a.AuthStatus != "authenticated" || a.Active != 0 || a.Limit != 1 || len(a.Sources) != 1 || a.Sources[0] != "s" || a.LastValidated == nil {
		t.Fatalf("unexpected account health: %+v", a)
	}
	b := out.Accounts[1]
	if b.ID != "b" || b.Health != "auth_required" || b.AuthStatus != "login_required" || b.Limit != 1 || len(b.Sources) != 0 {
		t.Fatalf("login-required account was not classified: %+v", b)
	}
	cHealth := out.Accounts[2]
	if cHealth.ID != "c" || cHealth.Health != "disabled" || cHealth.AuthStatus != "not_checked" {
		t.Fatalf("provider-disabled account was not classified: %+v", cHealth)
	}
}
