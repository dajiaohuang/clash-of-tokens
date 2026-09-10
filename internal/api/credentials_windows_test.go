//go:build windows

package api

import (
	"clash-of-tokens/internal/config"
	"clash-of-tokens/internal/credentials"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
)

func TestCredentialAdminAuthorizationAndRedaction(t *testing.T) {
	vault, err := credentials.Open(filepath.Join(t.TempDir(), "vault"))
	if err != nil {
		t.Fatal(err)
	}
	s, err := NewWithVault(config.Default(), "api-key-1234567890", "admin-key-1234567890", vault)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	call := func(method, path, key, origin, body string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(method, path, strings.NewReader(body))
		req.Header.Set("Authorization", "Bearer "+key)
		if origin != "" {
			req.Header.Set("Origin", origin)
		}
		w := httptest.NewRecorder()
		s.ServeHTTP(w, req)
		return w
	}
	body := `{"kind":"api_key","source":"manual","value":"private-value"}`
	if w := call("PUT", "/admin/credentials/test", "api-key-1234567890", "", body); w.Code != 401 {
		t.Fatal(w.Code)
	}
	if w := call("PUT", "/admin/credentials/test", "admin-key-1234567890", "https://other.test", body); w.Code != 403 {
		t.Fatal(w.Code)
	}
	if w := call("PUT", "/admin/credentials/test", "admin-key-1234567890", "", body); w.Code != http.StatusOK || strings.Contains(w.Body.String(), "private-value") {
		t.Fatal(w.Code, w.Body.String())
	}
	if w := call("GET", "/admin/credentials", "admin-key-1234567890", "", ""); strings.Contains(w.Body.String(), "private-value") || !strings.Contains(w.Body.String(), "cred://test") {
		t.Fatal(w.Body.String())
	}
	s.cfg.Accounts = []config.Account{{CredentialRef: "cred://test"}}
	if w := call("DELETE", "/admin/credentials/test", "admin-key-1234567890", "", ""); w.Code != 409 {
		t.Fatal(w.Code)
	}
	if vault.Resolve("cred://test") != "private-value" {
		t.Fatal("bound credential deleted")
	}
}
