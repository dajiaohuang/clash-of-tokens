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

func TestNewerEvidenceUsesSequenceWhenTimestampsTie(t *testing.T) {
	checked := time.Unix(100, 0).UTC()
	if !newerEvidence(audit.Entry{CheckedAt: checked, Sequence: 2}, audit.Entry{CheckedAt: checked, Sequence: 1}) {
		t.Fatal("higher sequence did not win an equal timestamp")
	}
	if newerEvidence(audit.Entry{CheckedAt: checked, Sequence: 1}, audit.Entry{CheckedAt: checked, Sequence: 2}) {
		t.Fatal("lower sequence replaced an equal timestamp")
	}
	if !newerEvidence(audit.Entry{CheckedAt: checked.Add(time.Second), Sequence: 1}, audit.Entry{CheckedAt: checked, Sequence: 99}) {
		t.Fatal("newer timestamp did not win")
	}
}

func TestControlStatusAggregatesAccountHealthAndEvidence(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusNotImplemented) }))
	defer upstream.Close()
	c := config.Default()
	c.Providers = []config.Provider{{ID: "p", Enabled: true, PoolStrategy: "least-load"}, {ID: "disabled-provider", Enabled: false}}
	c.Accounts = []config.Account{{ID: "a", ProviderID: "p", DisplayName: "Account", Enabled: true, CredentialRef: "cred://account-key", MaxInflight: 1, Weight: 3, QuotaDomain: "q"}}
	c.Accounts = append(c.Accounts, config.Account{ID: "b", ProviderID: "p", DisplayName: "Needs login", Enabled: true, MaxInflight: 1, Weight: 1, QuotaDomain: "q-b"})
	c.Accounts = append(c.Accounts, config.Account{ID: "c", ProviderID: "disabled-provider", DisplayName: "Provider disabled", Enabled: true, MaxInflight: 1, Weight: 1, QuotaDomain: "q-c"})
	c.Sources = []config.Source{{ID: "s", Provider: "p", Adapter: "openai", BaseURL: upstream.URL, Local: true, Enabled: true, MaxInflight: 1, AccountID: "a", QuotaDomain: "q", QuotaMaxInflight: 1, Models: []config.Model{{ID: "m", Upstream: "m", Protocols: []string{"chat"}, Tier: "unrated", Tools: "none", MaxInputBytes: 1024}}}}
	dir := t.TempDir()
	vault, err := credentials.Open(filepath.Join(dir, "vault"))
	if err != nil {
		t.Fatal(err)
	}
	if err := vault.Put("cred://account-key", "api_key", "test", "secret-value"); err != nil {
		t.Fatal(err)
	}
	p, err := NewControlPlane(filepath.Join(dir, "config.json"), c, testKey, adminKey, vault)
	if err != nil {
		t.Fatal(err)
	}
	defer p.Close()
	checked := time.Now().UTC().Add(-time.Minute)
	binding := p.sourceBinding(p.current.server.cfg, p.current.server.cfg.Sources[0])
	if err := p.evidence.Append(audit.Entry{Binding: binding, Kind: "validation", Resource: "s", Model: "m", Protocol: "chat", Status: "verified", CheckedAt: checked, Method: "explicit_stream_generation", UpstreamStatus: 200, ProtocolComplete: true, OutputObserved: true}); err != nil {
		t.Fatal(err)
	}
	if err := p.evidence.Append(audit.Entry{Binding: binding, Kind: "validation", Resource: "s", Model: "m", Protocol: "chat", Status: "failed", CheckedAt: checked.Add(-time.Minute), Method: "explicit_stream_generation", UpstreamStatus: 502}); err != nil {
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
		Sources []struct {
			ID                  string     `json:"id"`
			LastValidated       *time.Time `json:"last_validated_at"`
			LastValidationState string     `json:"last_validation_state"`
		} `json:"sources"`
		Providers []struct {
			ID                  string     `json:"id"`
			Enabled             bool       `json:"enabled"`
			AutoApproved        bool       `json:"auto_approved"`
			PoolStrategy        string     `json:"pool_strategy"`
			CatalogImplemented  bool       `json:"catalog_implemented"`
			CatalogLiveVerified bool       `json:"catalog_live_verified"`
			VerifiedSources     int        `json:"verified_sources"`
			Health              string     `json:"health"`
			AuthStatus          string     `json:"auth_status"`
			Accounts            []string   `json:"accounts"`
			Sources             []string   `json:"sources"`
			LastValidated       *time.Time `json:"last_validated_at"`
			LastValidationState string     `json:"last_validation_state"`
			LastAuthChecked     *time.Time `json:"last_auth_checked_at"`
		} `json:"provider_health"`
		Accounts []struct {
			ID                  string     `json:"id"`
			CredentialState     string     `json:"credential_state"`
			CredentialVersion   uint64     `json:"credential_version"`
			Enabled             bool       `json:"enabled"`
			AutoApproved        bool       `json:"auto_approved"`
			PoolStrategy        string     `json:"pool_strategy"`
			Weight              int        `json:"weight"`
			Health              string     `json:"health"`
			AuthStatus          string     `json:"auth_status"`
			Active              int        `json:"active"`
			Limit               int        `json:"limit"`
			Sources             []string   `json:"sources"`
			LastValidated       *time.Time `json:"last_validated_at"`
			LastValidationState string     `json:"last_validation_state"`
			LastAuthChecked     *time.Time `json:"last_auth_checked_at"`
		} `json:"account_health"`
		Verification []struct {
			Source            string `json:"source"`
			CredentialState   string `json:"credential_state"`
			CredentialVersion uint64 `json:"credential_version"`
			Models            []struct {
				Status string `json:"status"`
			} `json:"models"`
		} `json:"verification"`
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
	if len(out.Sources) != 1 || out.Sources[0].ID != "s" || out.Sources[0].LastValidated == nil || !out.Sources[0].LastValidated.Equal(checked) || out.Sources[0].LastValidationState != "verified" {
		t.Fatalf("source validation state missing: %+v", out.Sources)
	}
	if len(out.Verification) != 1 || out.Verification[0].CredentialState != "protected_reference" || out.Verification[0].CredentialVersion != 1 || len(out.Verification[0].Models) != 1 || out.Verification[0].Models[0].Status != "verified" {
		t.Fatalf("inherited credential provenance missing: %+v binding=%s entries=%+v", out.Verification, binding, p.evidence.List())
	}
	if out.Providers[0].ID != "p" || out.Providers[0].Health != "auth_required" || out.Providers[0].AuthStatus != "auth_required" || len(out.Providers[0].Accounts) != 2 || len(out.Providers[0].Sources) != 1 {
		t.Fatalf("unexpected provider health: %+v", out.Providers[0])
	}
	if !out.Providers[0].Enabled || out.Providers[0].AutoApproved {
		t.Fatalf("provider policy flags missing: %+v", out.Providers[0])
	}
	if out.Providers[0].PoolStrategy != "least-load" {
		t.Fatalf("provider pool strategy missing: %+v", out.Providers[0])
	}
	if out.Providers[0].LastValidated == nil || !out.Providers[0].LastValidated.Equal(checked) {
		t.Fatalf("provider validation timestamp missing: %+v", out.Providers[0].LastValidated)
	}
	if out.Providers[0].LastValidationState != "verified" {
		t.Fatalf("provider validation state missing: %+v", out.Providers[0])
	}
	if out.Providers[0].LastAuthChecked == nil || !out.Providers[0].LastAuthChecked.Equal(checked) {
		t.Fatalf("provider auth timestamp missing: %+v", out.Providers[0].LastAuthChecked)
	}
	if out.Providers[1].ID != "disabled-provider" || out.Providers[1].Health != "disabled" {
		t.Fatalf("disabled provider was not classified: %+v", out.Providers[1])
	}
	a := out.Accounts[0]
	if a.ID != "a" || a.CredentialState != "protected_reference" || a.CredentialVersion != 1 || a.Health != "untested" || a.AuthStatus != "authenticated" || a.Active != 0 || a.Limit != 1 || len(a.Sources) != 1 || a.Sources[0] != "s" || a.LastValidated == nil {
		t.Fatalf("unexpected account health: %+v", a)
	}
	if !a.Enabled || a.AutoApproved {
		t.Fatalf("account policy flags missing: %+v", a)
	}
	if a.LastAuthChecked == nil || !a.LastAuthChecked.Equal(checked) {
		t.Fatalf("account auth timestamp missing: %+v", a.LastAuthChecked)
	}
	if a.LastValidationState != "verified" {
		t.Fatalf("account validation state missing: %+v", a)
	}
	if a.PoolStrategy != "least-load" || a.Weight != 3 {
		t.Fatalf("account pool metadata missing: %+v", a)
	}
	b := out.Accounts[1]
	if b.ID != "b" || b.CredentialState != "not_configured" || b.CredentialVersion != 0 || b.Health != "auth_required" || b.AuthStatus != "login_required" || b.Limit != 1 || len(b.Sources) != 0 {
		t.Fatalf("login-required account was not classified: %+v", b)
	}
	cHealth := out.Accounts[2]
	if cHealth.ID != "c" || cHealth.Health != "disabled" || cHealth.AuthStatus != "not_checked" {
		t.Fatalf("provider-disabled account was not classified: %+v", cHealth)
	}
}

func TestAccountHealthReportsSourceCredentialBindings(t *testing.T) {
	t.Setenv("COT_SOURCE_ACCOUNT_KEY", "environment-secret")
	c := config.Default()
	c.Providers = []config.Provider{{ID: "p", Enabled: true}}
	c.Accounts = []config.Account{{ID: "a", ProviderID: "p", Enabled: true, MaxInflight: 1, Weight: 1, QuotaDomain: "q"}, {ID: "b", ProviderID: "p", Enabled: true, MaxInflight: 1, Weight: 1, QuotaDomain: "q-b"}}
	c.Sources = []config.Source{
		{ID: "s-protected", Provider: "p", Adapter: "openai", BaseURL: "https://example.com", AccountID: "a", CredentialRef: "cred://source", MaxInflight: 1, QuotaDomain: "q", QuotaMaxInflight: 1, Models: []config.Model{{ID: "m", Upstream: "m", Protocols: []string{"chat"}, Tier: "unrated", Tools: "none", MaxInputBytes: 1024}}},
		{ID: "s-env", Provider: "p", Adapter: "openai", BaseURL: "https://example.com", AccountID: "b", KeyEnv: "COT_SOURCE_ACCOUNT_KEY", MaxInflight: 1, QuotaDomain: "q-b", QuotaMaxInflight: 1, Models: []config.Model{{ID: "m", Upstream: "m", Protocols: []string{"chat"}, Tier: "unrated", Tools: "none", MaxInputBytes: 1024}}},
	}
	dir := t.TempDir()
	vault, err := credentials.Open(filepath.Join(dir, "vault"))
	if err != nil {
		t.Fatal(err)
	}
	if err := vault.Put("cred://source", "api_key", "test", "secret"); err != nil {
		t.Fatal(err)
	}
	p, err := NewControlPlane(filepath.Join(dir, "config.json"), c, testKey, adminKey, vault)
	if err != nil {
		t.Fatal(err)
	}
	defer p.Close()
	rows := p.accountHealth(p.current.server, nil)
	if len(rows) != 2 {
		t.Fatalf("account rows = %+v", rows)
	}
	if rows[0].CredentialState != "protected_reference" || rows[0].CredentialVersion != 1 {
		t.Fatalf("source-bound protected credential was not reported: %+v", rows[0])
	}
	if rows[1].CredentialState != "environment_present" || rows[1].CredentialVersion != 0 {
		t.Fatalf("source-bound environment credential was not reported: %+v", rows[1])
	}
}

func TestProviderHealthIncludesCatalogProvenance(t *testing.T) {
	dir := t.TempDir()
	c := config.Default()
	c.Providers = []config.Provider{{ID: "openai", Enabled: true}}
	vault, err := credentials.Open(filepath.Join(dir, "vault"))
	if err != nil {
		t.Fatal(err)
	}
	p, err := NewControlPlane(filepath.Join(dir, "config.json"), c, testKey, adminKey, vault)
	if err != nil {
		t.Fatal(err)
	}
	defer p.Close()
	r := httptest.NewRequest("GET", "/admin/status", nil)
	r.Header.Set("Authorization", "Bearer "+adminKey)
	w := httptest.NewRecorder()
	p.ServeHTTP(w, r)
	var out struct {
		Providers []struct {
			ID                  string `json:"id"`
			CatalogImplemented  bool   `json:"catalog_implemented"`
			CatalogLiveVerified bool   `json:"catalog_live_verified"`
			VerifiedSources     int    `json:"verified_sources"`
		} `json:"provider_health"`
	}
	if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &out) != nil || len(out.Providers) != 1 {
		t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
	}
	if out.Providers[0].ID != "openai" || !out.Providers[0].CatalogImplemented || out.Providers[0].CatalogLiveVerified || out.Providers[0].VerifiedSources != 0 {
		t.Fatalf("unexpected catalog provenance: %+v", out.Providers[0])
	}
}
