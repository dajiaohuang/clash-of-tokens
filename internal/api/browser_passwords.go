package api

import (
	"context"
	"net/http"
	"os/exec"
	"strings"
	"time"

	"clash-of-tokens/internal/browsermeta"
	"clash-of-tokens/internal/browserpassword"
	"clash-of-tokens/internal/credentials"
)

func (s *Server) browserPasswordImport(w http.ResponseWriter, r *http.Request) bool {
	return s.browserPasswordImportWith(w, r, browserpassword.Discover, browserpassword.Read)
}

func (s *Server) browserPasswordImportWith(w http.ResponseWriter, r *http.Request, discover func(context.Context) []browserpassword.Browser, read func(context.Context, string) (browserpassword.Report, error)) bool {
	const prefix = "/admin/credentials/browser-passwords/"
	if !strings.HasPrefix(r.URL.Path, prefix) {
		return false
	}
	w.Header().Set("Cache-Control", "no-store")
	if r.Method != "POST" {
		fail(w, 405, "use POST")
		return true
	}
	ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
	defer cancel()
	var input struct {
		Browser string `json:"browser"`
		Ticket  string `json:"ticket"`
		Confirm bool   `json:"confirm"`
		Format  string `json:"format"`
		Data    string `json:"data"`
	}
	limit := int64(4096)
	if strings.TrimPrefix(r.URL.Path, prefix) == "preview-file" {
		limit = 8 << 20
	}
	if decodeInput(w, r, &input, limit) != nil {
		fail(w, 400, "invalid browser import request")
		return true
	}
	switch strings.TrimPrefix(r.URL.Path, prefix) {
	case "open-export":
		pages := map[string]string{"chrome": "chrome://password-manager/settings", "chromium": "chrome://password-manager/settings", "edge": "edge://wallet/passwords", "brave": "brave://password-manager/settings", "vivaldi": "vivaldi://password-manager/settings", "opera": "opera://password-manager/settings", "firefox": "about:logins"}
		page := pages[input.Browser]
		found := false
		for _, profile := range browsermeta.Discover(browsermeta.StandardRoots()) {
			if profile.Browser == input.Browser {
				found = true
				break
			}
		}
		if page == "" || !found {
			fail(w, 400, "browser_not_found")
			return true
		}
		path, err := browserExecutable(input.Browser)
		if err != nil {
			fail(w, 400, "browser_not_found")
			return true
		}
		cmd := exec.Command(path, page)
		if cmd.Start() != nil {
			fail(w, 400, "browser_export_required")
			return true
		}
		go cmd.Wait()
		reply(w, map[string]any{"opened": true})
	case "discover":
		reply(w, map[string]any{"browsers": discover(ctx)})
	case "preview", "preview-file":
		var report browserpassword.Report
		var err error
		if strings.HasSuffix(r.URL.Path, "/preview-file") {
			report.Entries, report.Skipped, err = credentials.ParseExport(input.Format, []byte(input.Data))
		} else {
			report, err = read(ctx, input.Browser)
		}
		if err != nil {
			fail(w, 400, err.Error())
			return true
		}
		if len(report.Entries) == 0 {
			reply(w, map[string]any{"count": 0, "skipped": report.Skipped})
			return true
		}
		conflicts := s.vault.ImportConflicts(report.Entries)
		ticket, err := s.imports.Add(report.Entries)
		if err != nil {
			fail(w, 429, err.Error())
			return true
		}
		reply(w, map[string]any{"ticket": ticket, "count": len(report.Entries), "existing": len(conflicts), "duplicates": report.Duplicates, "skipped": report.Skipped})
	case "apply":
		if !input.Confirm || input.Ticket == "" {
			fail(w, 400, "confirm_browser_import")
			return true
		}
		entries, err := s.imports.Take(input.Ticket)
		if err != nil {
			fail(w, 409, "browser_preview_expired")
			return true
		}
		result, err := s.vault.ImportAll(entries, "browser-passwords")
		if err != nil {
			fail(w, 409, err.Error())
			return true
		}
		reply(w, result)
	default:
		fail(w, 404, "unknown browser import action")
	}
	return true
}
