package api

import (
	"net/http"
	"net/url"
	"slices"

	"clash-of-tokens/catalog"
	"clash-of-tokens/internal/browsermeta"
)

func (s *Server) browserCookieImport(w http.ResponseWriter, r *http.Request) bool {
	if r.URL.Path != "/admin/credentials/import-browser" {
		return false
	}
	if r.Method != "POST" {
		fail(w, 405, "method not allowed")
		return true
	}
	var input struct {
		Profile  string `json:"profile"`
		Provider string `json:"provider"`
		Apply    bool   `json:"apply"`
	}
	if decodeInput(w, r, &input, 4096) != nil {
		fail(w, 400, "invalid browser import request")
		return true
	}
	endpoint, destination := "", ""
	for _, p := range s.cfg.BrowserProfiles {
		if p.ID == input.Profile && p.Enabled {
			endpoint = p.CDPURL
		}
	}
	for _, p := range catalog.All() {
		if p.ID == input.Provider && slices.Contains(p.Credentials.Accepted, "cookie") {
			destination = p.BaseURL
		}
	}
	u, err := url.Parse(destination)
	if endpoint == "" || err != nil || u.Scheme != "https" || u.Hostname() == "" || u.User != nil {
		fail(w, 400, "select an enabled profile and a provider accepting cookies")
		return true
	}
	value, count, err := browsermeta.Cookies(r.Context(), endpoint, destination)
	if err != nil {
		fail(w, 400, err.Error())
		return true
	}
	if !input.Apply {
		reply(w, map[string]any{"count": count, "domain": u.Hostname(), "authentication": "not_checked", "message": "Only cookies applicable to this provider URL. Saving re-reads current cookies; bind the new reference separately."})
		return true
	}
	if count == 0 {
		fail(w, 400, "no cookies available for this provider")
		return true
	}
	metadata, err := s.vault.Create("cookie", "browser:"+input.Profile+":"+input.Provider, value)
	if err != nil {
		fail(w, 400, err.Error())
		return true
	}
	reply(w, metadata)
	return true
}
