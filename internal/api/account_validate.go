package api

import (
	"bytes"
	"clash-of-tokens/internal/config"
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"strings"
)

// accountValidateAdmin provides the account-level entry point required by the
// management API. It delegates to the existing explicit source validation so
// the same lease, completion, evidence and redaction rules apply. Browser-only
// accounts use their non-generating authentication check instead.
func (p *ControlPlane) accountValidateAdmin(w http.ResponseWriter, r *http.Request, s *Server) bool {
	const prefix = "/admin/accounts/"
	if !strings.HasPrefix(r.URL.Path, prefix) {
		return false
	}
	parts := strings.Split(strings.TrimPrefix(r.URL.Path, prefix), "/")
	if len(parts) != 2 || parts[1] != "validate" {
		return false
	}
	if r.Method != "POST" {
		fail(w, 405, "method not allowed")
		return true
	}
	accountID, err := url.PathUnescape(parts[0])
	if err != nil || accountID == "" {
		fail(w, 400, "invalid account id")
		return true
	}
	var account config.Account
	for _, candidate := range s.cfg.Accounts {
		if candidate.ID == accountID {
			account = candidate
			break
		}
	}
	if account.ID == "" {
		fail(w, 404, "unknown account")
		return true
	}
	for _, source := range s.cfg.Sources {
		if source.AccountID != account.ID || len(source.Models) == 0 || len(source.Models[0].Protocols) == 0 {
			continue
		}
		body, _ := json.Marshal(struct {
			Model    string `json:"model"`
			Protocol string `json:"protocol"`
		}{source.Models[0].ID, source.Models[0].Protocols[0]})
		forward := r.Clone(r.Context())
		forward.URL.Path = "/admin/sources/" + url.PathEscape(source.ID) + "/validate"
		forward.Body = io.NopCloser(bytes.NewReader(body))
		forward.ContentLength = int64(len(body))
		p.sourceCheckAdmin(w, forward, s)
		return true
	}
	if account.BrowserProfileID != "" {
		forward := r.Clone(r.Context())
		forward.URL.Path = "/admin/accounts/" + url.PathEscape(account.ID) + "/check-login"
		p.browserLoginAdmin(w, forward)
		return true
	}
	fail(w, 400, "account has no configured source or browser profile to validate")
	return true
}
