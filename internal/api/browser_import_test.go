package api

import (
	"net/http/httptest"
	"strings"
	"testing"

	"clash-of-tokens/internal/config"
)

func TestBrowserImportRejectsUnconfiguredScope(t *testing.T) {
	s := &Server{cfg: config.Config{BrowserProfiles: []config.BrowserProfile{{ID: "disabled", Enabled: false, CDPURL: "http://127.0.0.1:1"}, {ID: "enabled", Enabled: true, CDPURL: "http://127.0.0.1:1"}}}}
	for _, body := range []string{
		`{"profile":"missing","provider":"doubao"}`,
		`{"profile":"disabled","provider":"doubao"}`,
		`{"profile":"enabled","provider":"https://example.org"}`,
		`{"profile":"enabled","provider":"openai"}`,
		`{"profile":"enabled","provider":"doubao","destination":"https://example.org"}`,
	} {
		w := httptest.NewRecorder()
		if !s.browserCookieImport(w, httptest.NewRequest("POST", "/admin/credentials/import-browser", strings.NewReader(body))) || w.Code != 400 {
			t.Fatalf("scope not rejected: %d %s", w.Code, w.Body.String())
		}
	}
	w := httptest.NewRecorder()
	s.browserCookieImport(w, httptest.NewRequest("GET", "/admin/credentials/import-browser", nil))
	if w.Code != 405 {
		t.Fatalf("GET accepted: %d", w.Code)
	}
}
