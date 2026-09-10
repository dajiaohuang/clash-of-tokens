package api

import (
	"encoding/json"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"clash-of-tokens/internal/config"
	"clash-of-tokens/internal/credentials"
)

func TestAccountDiscoveryIsMetadataOnlyAndExplicit(t *testing.T) {
	dir := t.TempDir()
	vault, err := credentials.Open(filepath.Join(dir, "vault"))
	if err != nil {
		t.Fatal(err)
	}
	if err = vault.Put("cred://openai", "api_key", "manual", "super-secret"); err != nil {
		t.Fatal(err)
	}
	if _, err = vault.ImportSelected([]credentials.ImportEntry{{URL: "https://chat.openai.com/login", Username: "user", Password: "private-password"}}, []int{0}); err != nil {
		t.Fatal(err)
	}
	c := config.Default()
	c.Providers = []config.Provider{{ID: "openai", Enabled: true, AutoApproved: true}}
	c.Sources = []config.Source{{ID: "openai-source", Provider: "openai", Adapter: "openai", BaseURL: "https://api.openai.com/v1", KeyEnv: "COT_TEST_DISCOVERY_KEY", Enabled: true, AutoApproved: true, Local: false, Paid: true, MaxInflight: 1, QuotaDomain: "openai", QuotaMaxInflight: 1, Models: []config.Model{{ID: "gpt", Upstream: "gpt", Protocols: []string{"chat"}, Tier: "silver", RatingBasis: "test", Tools: "native", MaxInputBytes: 1024}}}}
	old, had := os.LookupEnv("COT_TEST_DISCOVERY_KEY")
	if err := os.Setenv("COT_TEST_DISCOVERY_KEY", "secret-env"); err != nil {
		t.Fatal(err)
	}
	defer func() {
		if had {
			_ = os.Setenv("COT_TEST_DISCOVERY_KEY", old)
		} else {
			_ = os.Unsetenv("COT_TEST_DISCOVERY_KEY")
		}
	}()
	p, err := NewControlPlane(filepath.Join(dir, "config.json"), c, testKey, adminKey, vault)
	if err != nil {
		t.Fatal(err)
	}
	defer p.Close()
	call := func(body string) (int, map[string]any) {
		r := httptest.NewRequest("POST", "/admin/discovery/accounts", strings.NewReader(body))
		r.Header.Set("Authorization", "Bearer "+adminKey)
		w := httptest.NewRecorder()
		p.ServeHTTP(w, r)
		var out map[string]any
		_ = json.Unmarshal(w.Body.Bytes(), &out)
		return w.Code, out
	}
	if code, _ := call(`{"scan_browsers":false}`); code != 200 {
		t.Fatal("discovery failed", code)
	}
	_, out := call(`{"scan_browsers":false}`)
	if out["secret_values"] != false || out["browser_scan"] != false {
		t.Fatalf("unexpected discovery flags: %#v", out)
	}
	b, _ := json.Marshal(out["items"])
	text := string(b)
	if strings.Contains(text, "super-secret") || strings.Contains(text, "secret-env") {
		t.Fatal("discovery leaked a secret")
	}
	if !strings.Contains(text, "cred://openai") || !strings.Contains(text, "openai-source") || !strings.Contains(text, "chat.openai.com") || !strings.Contains(text, "bind_credential") {
		t.Fatalf("expected stored and environment candidates: %s", text)
	}
}
