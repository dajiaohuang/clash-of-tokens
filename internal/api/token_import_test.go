package api

import (
	"clash-of-tokens/internal/config"
	"clash-of-tokens/internal/credentials"
	"encoding/json"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestTokenImportFromExplicitEnvironmentVariable(t *testing.T) {
	const variable = "COT_TEST_IMPORT_TOKEN"
	if err := os.Setenv(variable, "synthetic-explicit-token"); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Unsetenv(variable) })
	vault, err := credentials.Open(filepath.Join(t.TempDir(), "vault"))
	if err != nil {
		t.Fatal(err)
	}
	s, err := NewWithVault(config.Default(), testKey, adminKey, vault)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	call := func(body string) *httptest.ResponseRecorder {
		r := httptest.NewRequest("POST", "/admin/credentials/import-env", strings.NewReader(body))
		r.Header.Set("Authorization", "Bearer "+adminKey)
		w := httptest.NewRecorder()
		s.ServeHTTP(w, r)
		return w
	}
	preview := call(`{"variable":"` + variable + `","kind":"api_key"}`)
	if preview.Code != 200 || strings.Contains(preview.Body.String(), "synthetic-explicit-token") {
		t.Fatalf("unsafe or failed preview: %d %s", preview.Code, preview.Body.String())
	}
	var result struct {
		Variable  string `json:"variable"`
		Available bool   `json:"available"`
	}
	if err := json.Unmarshal(preview.Body.Bytes(), &result); err != nil || result.Variable != variable || !result.Available {
		t.Fatalf("unexpected preview: %s", preview.Body.String())
	}
	saved := call(`{"variable":"` + variable + `","kind":"api_key","apply":true}`)
	if saved.Code != 200 || strings.Contains(saved.Body.String(), "synthetic-explicit-token") {
		t.Fatalf("unsafe or failed save: %d %s", saved.Code, saved.Body.String())
	}
	var metadata credentials.Metadata
	if err := json.Unmarshal(saved.Body.Bytes(), &metadata); err != nil || metadata.Source != "environment:"+variable {
		t.Fatalf("unexpected metadata: %s", saved.Body.String())
	}
	if got := vault.Resolve(metadata.ID); got != "synthetic-explicit-token" {
		t.Fatalf("imported value missing from vault: %q", got)
	}
	if bad := call(`{"variable":"BAD-NAME","kind":"api_key"}`); bad.Code != 400 {
		t.Fatalf("unsafe variable name accepted: %d %s", bad.Code, bad.Body.String())
	}
}
