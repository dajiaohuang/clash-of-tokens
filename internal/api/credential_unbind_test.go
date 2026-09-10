package api

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"clash-of-tokens/internal/config"
	"clash-of-tokens/internal/credentials"
)

func TestCredentialUnbindClearsInheritedSourcesAndRetainsVault(t *testing.T) {
	dir := t.TempDir()
	vault, err := credentials.Open(filepath.Join(dir, "vault"))
	if err != nil {
		t.Fatal(err)
	}
	if err = vault.Put("cred://shared", "api_key", "fixture", "secret-value"); err != nil {
		t.Fatal(err)
	}
	c := config.Default()
	c.Providers = []config.Provider{{ID: "openai", Enabled: true, AutoApproved: true}}
	c.Accounts = []config.Account{{ID: "account", ProviderID: "openai", CredentialRef: "cred://shared", Enabled: true, AutoApproved: true, QuotaDomain: "shared", MaxInflight: 2, Weight: 1}}
	for _, id := range []string{"first", "second"} {
		c.Sources = append(c.Sources, config.Source{ID: id, Provider: "openai", Adapter: "openai", BaseURL: "http://127.0.0.1:1", KeyEnv: "COT_TEST_KEY", AccountID: "account", SourceKind: "vendor_api", ExecutionLocation: "local", InferenceLocation: "remote", BillingMode: "free_allowance", CredentialMode: "api_key", Enabled: true, AutoApproved: true, MaxInflight: 1, QuotaDomain: "shared", QuotaMaxInflight: 2, Models: []config.Model{{ID: "model", Upstream: "model", Protocols: []string{"chat"}, Tier: "silver", RatingBasis: "fixture", Tools: "none", MaxInputBytes: 4096}}})
		c.Groups[0].Sources = append(c.Groups[0].Sources, id)
	}
	plane, err := NewControlPlane(filepath.Join(dir, "config.json"), c, testKey, adminKey, vault)
	if err != nil {
		t.Fatal(err)
	}
	defer plane.Close()

	body := bytes.NewBufferString(`{"revision":1,"confirm":true}`)
	req := httptest.NewRequest(http.MethodPost, "/admin/credentials/shared/unbind", body)
	req.Header.Set("Authorization", "Bearer "+adminKey)
	w := httptest.NewRecorder()
	plane.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("unbind status=%d body=%s", w.Code, w.Body.String())
	}
	var result struct {
		Status        string   `json:"status"`
		Accounts      []string `json:"accounts"`
		Sources       []string `json:"sources"`
		VaultRetained bool     `json:"vault_retained"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	if result.Status != "unbound" || !result.VaultRetained || strings.Join(result.Accounts, ",") != "account" || strings.Join(result.Sources, ",") != "first,second" {
		t.Fatalf("unexpected unbind result: %+v", result)
	}
	current := plane.service.Current().Config
	if current.Accounts[0].CredentialRef != "" || current.Sources[0].CredentialRef != "" || current.Sources[1].CredentialRef != "" {
		t.Fatalf("references remain after unbind: %+v", current)
	}
	if got := vault.Resolve("cred://shared"); got != "secret-value" {
		t.Fatalf("vault value was deleted or changed: %q", got)
	}

	req = httptest.NewRequest(http.MethodPost, "/admin/credentials/shared/unbind", bytes.NewBufferString(`{"revision":2,"confirm":true}`))
	req.Header.Set("Authorization", "Bearer "+adminKey)
	w = httptest.NewRecorder()
	plane.ServeHTTP(w, req)
	if w.Code != http.StatusConflict || !strings.Contains(w.Body.String(), "already unbound") {
		t.Fatalf("repeat unbind status=%d body=%s", w.Code, w.Body.String())
	}
}
