package api

import (
	"encoding/json"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"clash-of-tokens/internal/config"
	"clash-of-tokens/internal/credentials"
)

func TestImplementationStatusSeparatesCatalogAndRuntimeEvidence(t *testing.T) {
	dir := t.TempDir()
	vault, err := credentials.Open(filepath.Join(dir, "vault"))
	if err != nil {
		t.Fatal(err)
	}
	c := config.Default()
	c.Providers = []config.Provider{{ID: "openai", Enabled: true, AutoApproved: true}}
	p, err := NewControlPlane(filepath.Join(dir, "config.json"), c, testKey, adminKey, vault)
	if err != nil {
		t.Fatal(err)
	}
	defer p.Close()
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
		} `json:"items"`
		LiveMeans string `json:"live_means"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	for _, item := range out.Items {
		if item.ID == "openai" {
			if item.Factory != "http" || item.RuntimeVerified != 0 || item.CatalogLive {
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
