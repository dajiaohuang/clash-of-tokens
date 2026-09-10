package api

import (
	"clash-of-tokens/internal/config"
	"clash-of-tokens/internal/credentials"
	"encoding/json"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
)

func TestDeviceCheckAdminIsAuthenticatedReadOnlyAndRedacted(t *testing.T) {
	dir := t.TempDir()
	vault, err := credentials.Open(filepath.Join(dir, "vault"))
	if err != nil {
		t.Fatal(err)
	}
	plane, err := NewControlPlane(filepath.Join(dir, "config.json"), config.Default(), testKey, adminKey, vault)
	if err != nil {
		t.Fatal(err)
	}
	defer plane.Close()
	for _, tc := range []struct {
		key, origin, method string
		want                int
	}{
		{"", "", "POST", 401},
		{adminKey, "https://elsewhere.test", "POST", 403},
		{adminKey, "", "GET", 405},
		{adminKey, "", "POST", 200},
	} {
		r := httptest.NewRequest(tc.method, "/admin/device/check", strings.NewReader("{}"))
		r.Header.Set("Authorization", "Bearer "+tc.key)
		if tc.origin != "" {
			r.Header.Set("Origin", tc.origin)
		}
		w := httptest.NewRecorder()
		plane.ServeHTTP(w, r)
		if w.Code != tc.want {
			t.Fatalf("%s %s: status=%d body=%s", tc.method, tc.origin, w.Code, w.Body.String())
		}
		if tc.want == 200 {
			var out struct {
				Revision uint64 `json:"revision"`
				Report   struct {
					Ready        bool `json:"ready"`
					LiveVerified bool `json:"live_verified"`
				} `json:"report"`
				Providers []any `json:"providers"`
			}
			if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil {
				t.Fatal(err)
			}
			if out.Revision != 1 || out.Report.Ready || out.Report.LiveVerified || out.Providers == nil {
				t.Fatalf("unexpected device check response: %+v", out)
			}
		}
	}
}
