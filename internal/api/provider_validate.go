package api

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"strings"
)

// providerValidateAdmin is the provider-level validation entry point used by
// the management UI. A provider is validated through the first configured
// source/model/protocol so the check exercises the real adapter, credential
// binding, streaming completion and evidence path. Browser-only accounts fall
// back to their authentication check when they do not yet have a source.
func (p *ControlPlane) providerValidateAdmin(w http.ResponseWriter, r *http.Request, s *Server) bool {
	const prefix = "/admin/providers/"
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
	providerID, err := url.PathUnescape(parts[0])
	if err != nil || providerID == "" {
		fail(w, 400, "invalid provider id")
		return true
	}
	configured := false
	for _, provider := range s.cfg.Providers {
		if provider.ID == providerID {
			configured = true
			break
		}
	}
	if !configured {
		fail(w, 404, "unknown configured provider")
		return true
	}
	for _, source := range s.cfg.Sources {
		if source.Provider != providerID || len(source.Models) == 0 || len(source.Models[0].Protocols) == 0 {
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
	for _, account := range s.cfg.Accounts {
		if account.ProviderID != providerID {
			continue
		}
		body := bytes.NewReader([]byte(`{}`))
		forward := r.Clone(r.Context())
		forward.URL.Path = "/admin/accounts/" + url.PathEscape(account.ID) + "/validate"
		forward.Body = io.NopCloser(body)
		forward.ContentLength = 2
		p.accountValidateAdmin(w, forward, s)
		return true
	}
	fail(w, 400, "provider has no configured source or account to validate")
	return true
}
