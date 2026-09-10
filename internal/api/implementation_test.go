package api

import (
	"encoding/json"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"clash-of-tokens/internal/config"
	"clash-of-tokens/internal/credentials"
	audit "clash-of-tokens/internal/evidence"
)

func TestImplementationStatusSeparatesCatalogAndRuntimeEvidence(t *testing.T) {
	dir := t.TempDir()
	vault, err := credentials.Open(filepath.Join(dir, "vault"))
	if err != nil {
		t.Fatal(err)
	}
	c := config.Default()
	c.Providers = []config.Provider{{ID: "openai", Enabled: true, AutoApproved: true}}
	c.Sources = []config.Source{{ID: "openai-source", Provider: "openai", Adapter: "openai", BaseURL: "http://127.0.0.1:1", Local: true, Enabled: false, AutoApproved: false, MaxInflight: 1, QuotaDomain: "openai-quota", QuotaMaxInflight: 1, Models: []config.Model{{ID: "model", Upstream: "model", Protocols: []string{"chat"}, Tier: "unrated", Tools: "none", MaxInputBytes: 1024}}}}
	p, err := NewControlPlane(filepath.Join(dir, "config.json"), c, testKey, adminKey, vault)
	if err != nil {
		t.Fatal(err)
	}
	defer p.Close()
	if err := p.evidence.Append(audit.Entry{Kind: "validation", Resource: "openai-source", Status: "verified"}); err != nil {
		t.Fatal(err)
	}
	if err := p.evidence.Append(audit.Entry{Kind: "validation", Resource: "openai-source", Status: "failed"}); err != nil {
		t.Fatal(err)
	}
	r := httptest.NewRequest("GET", "/admin/implementation", strings.NewReader(""))
	r.Header.Set("Authorization", "Bearer "+adminKey)
	w := httptest.NewRecorder()
	p.ServeHTTP(w, r)
	if w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	var out struct {
		Items []struct {
			ID              string `json:"id"`
			Factory         string `json:"factory"`
			CatalogLive     bool   `json:"catalog_live_verified"`
			RuntimeVerified int    `json:"runtime_verified_models"`
			RuntimeFailures int    `json:"runtime_failed_models"`
		} `json:"items"`
		LiveMeans string `json:"live_means"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	for _, item := range out.Items {
		if item.ID == "openai" {
			if item.Factory != "http" || item.RuntimeVerified != 1 || item.RuntimeFailures != 1 || item.CatalogLive {
				t.Fatalf("unexpected openai status: %+v", item)
			}
			if !strings.Contains(out.LiveMeans, "explicit") {
				t.Fatal("missing live evidence boundary")
			}
			return
		}
	}
	t.Fatal("openai catalog row missing")
}
