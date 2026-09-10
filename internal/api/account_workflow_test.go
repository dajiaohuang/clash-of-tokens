package api

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"clash-of-tokens/internal/config"
	"clash-of-tokens/internal/credentials"
)

// This is the smallest end-to-end control-plane path: one protected reference
// is bound to an account, two sources inherit it, and Auto routes a request.
func TestAccountWorkflowSharesOneCredentialAcrossSources(t *testing.T) {
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer shared-secret" {
			t.Errorf("credential did not resolve through the account: %q", r.Header.Get("Authorization"))
		}
		_, _ = io.ReadAll(r.Body)
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w, "data: {\"choices\":[{\"delta\":{\"content\":\"OK\"},\"finish_reason\":\"stop\"}]}\n\ndata: [DONE]\n\n")
	}))
	defer up.Close()
	dir := t.TempDir()
	vault, err := credentials.Open(filepath.Join(dir, "vault"))
	if err != nil {
		t.Fatal(err)
	}
	if err = vault.Put("cred://shared", "api_key", "manual", "shared-secret"); err != nil {
		t.Fatal(err)
	}
	c := config.Default()
	c.Providers = []config.Provider{{ID: "openai", Enabled: true, AutoApproved: true}}
	c.Groups = []config.Group{{ID: "auto", Type: "auto", Sources: []string{}, MinTier: "bronze", AllowPaid: true}}
	p, err := NewControlPlane(filepath.Join(dir, "config.json"), c, testKey, adminKey, vault)
	if err != nil {
		t.Fatal(err)
	}
	defer p.Close()
	call := func(method, path string, body any, revision uint64) (int, map[string]any) {
		data, _ := json.Marshal(body)
		r := httptest.NewRequest(method, path, bytes.NewReader(data))
		r.Header.Set("Authorization", "Bearer "+adminKey)
		if revision > 0 {
			r.Header.Set("If-Match", fmt.Sprint(revision))
		}
		w := httptest.NewRecorder()
		p.ServeHTTP(w, r)
		var out map[string]any
		_ = json.Unmarshal(w.Body.Bytes(), &out)
		return w.Code, out
	}
	code, out := call("POST", "/admin/accounts", config.Account{ID: "account", ProviderID: "openai", Enabled: true, AutoApproved: true, CredentialRef: "cred://shared", QuotaDomain: "shared-quota", MaxInflight: 2, Weight: 1}, 0)
	if code != 200 {
		t.Fatal(code, out)
	}
	revision := uint64(out["revision"].(float64))
	newSource := func(id string) config.Source {
		return config.Source{ID: id, Provider: "openai", Adapter: "openai", BaseURL: up.URL, Enabled: true, AutoApproved: true, Local: true, SourceKind: "vendor_api", ExecutionLocation: "local", InferenceLocation: "remote", BillingMode: "metered", CredentialMode: "api_key", AccountID: "account", MaxInflight: 1, QuotaDomain: "shared-quota", QuotaMaxInflight: 2, Models: []config.Model{{ID: "m", Upstream: "upstream", Protocols: []string{"chat"}, Tier: "bronze", RatingBasis: "fixture", Tools: "none", MaxInputBytes: 4096, AutoApproved: boolPtr(true)}}}
	}
	for _, id := range []string{"source-one", "source-two"} {
		code, out = call("POST", "/admin/sources", newSource(id), revision)
		if code != 200 {
			t.Fatal(code, out)
		}
		revision = uint64(out["revision"].(float64))
	}
	group := config.Group{ID: "auto", Type: "auto", Sources: []string{"source-one", "source-two"}, MinTier: "bronze", AllowPaid: true}
	code, out = call("PUT", "/admin/groups/auto", group, revision)
	if code != 200 {
		t.Fatal(code, out)
	}
	revision = uint64(out["revision"].(float64))
	_ = revision
	request := httptest.NewRequest("POST", "/v1/chat/completions", strings.NewReader(`{"model":"auto/bronze","messages":[{"role":"user","content":"hi"}],"stream":true}`))
	request.Header.Set("Authorization", "Bearer "+testKey)
	w := httptest.NewRecorder()
	p.ServeHTTP(w, request)
	if w.Code != 200 || !strings.Contains(w.Body.String(), "[DONE]") {
		t.Fatalf("Auto request failed: %d %s", w.Code, w.Body.String())
	}
	for _, source := range p.service.Current().Config.Sources {
		if source.CredentialRef != "" {
			t.Fatalf("source copied credential instead of inheriting account: %+v", source)
		}
	}
}

func boolPtr(v bool) *bool { return &v }
