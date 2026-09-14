package api

import (
	"clash-of-tokens/catalog"
	"clash-of-tokens/internal/config"
	"clash-of-tokens/internal/credentials"
	"encoding/json"
	"io"
	"net/http"
	"strings"
)

func NewWithVault(c config.Config, key, admin string, vault *credentials.Store) (*Server, error) {
	if err := validateCredentialBindings(c, vault.List()); err != nil {
		return nil, err
	}
	s, err := NewWithCredentials(c, key, admin, vault.Resolve)
	if err != nil {
		return nil, err
	}
	s.vault = vault
	for i := range s.cfg.Sources {
		s.cfg.Sources[i].CredentialResolverContext = vault.ResolveContext
	}
	return s, nil
}

func (s *Server) credentialAdmin(w http.ResponseWriter, r *http.Request) {
	if s.vault == nil {
		fail(w, 503, "credential vault not configured")
		return
	}
	if s.credentialLifecycleAdmin(w, r) {
		return
	}
	if s.browserPasswordImport(w, r) {
		return
	}
	if r.Method == "DELETE" && strings.HasPrefix(r.URL.Path, "/admin/credentials/import-preview/") {
		s.imports.Drop(strings.TrimPrefix(r.URL.Path, "/admin/credentials/import-preview/"))
		reply(w, map[string]string{"status": "discarded"})
		return
	}
	if s.tokenImport(w, r) {
		return
	}
	if s.browserCookieImport(w, r) {
		return
	}
	if r.URL.Path == "/admin/credentials/import" && r.Method == "POST" {
		var p struct {
			Format   string                        `json:"format"`
			Data     string                        `json:"data"`
			Selected []int                         `json:"selected"`
			Apply    bool                          `json:"apply"`
			Ticket   string                        `json:"ticket,omitempty"`
			Choices  []credentials.ImportSelection `json:"choices,omitempty"`
		}
		d := json.NewDecoder(http.MaxBytesReader(w, r.Body, 8<<20))
		d.DisallowUnknownFields()
		if d.Decode(&p) != nil || d.Decode(new(any)) != io.EOF {
			fail(w, 400, "invalid import request")
			return
		}
		var entries []credentials.ImportEntry
		var skipped int
		var err error
		if p.Ticket != "" {
			if !p.Apply || p.Data != "" {
				fail(w, 400, "ticket applies only to a selected preview")
				return
			}
			entries, err = s.imports.Take(p.Ticket)
		} else {
			entries, skipped, err = credentials.ParseExport(p.Format, []byte(p.Data))
		}
		if err != nil {
			fail(w, 400, err.Error())
			return
		}
		if !p.Apply {
			type preview struct {
				credentials.ImportPreview
				Matches   []catalog.CredentialMatch `json:"matches"`
				Conflicts []credentials.Metadata    `json:"conflicts,omitempty"`
			}
			out := []preview{}
			conflicts := s.vault.ImportConflicts(entries)
			for _, item := range credentials.PreviewImport(entries) {
				out = append(out, preview{item, catalog.MatchCredentials(item.Domain, item.Kind), conflicts[item.Index]})
			}
			ticket, err := s.imports.Add(entries)
			if err != nil {
				fail(w, 429, err.Error())
				return
			}
			reply(w, map[string]any{"items": out, "skipped": skipped, "ticket": ticket, "expires_in_seconds": int(credentials.PreviewTTL.Seconds())})
			return
		}
		var items []credentials.Metadata
		if len(p.Choices) > 0 {
			items, err = s.vault.ImportWithConflicts(entries, p.Choices)
		} else {
			items, err = s.vault.ImportSelected(entries, p.Selected)
		}
		if err != nil {
			fail(w, 400, err.Error())
			return
		}
		reply(w, items)
		return
	}
	if r.URL.Path == "/admin/credentials" && r.Method == "GET" {
		reply(w, s.vault.List())
		return
	}
	const prefix = "/admin/credentials/"
	if !strings.HasPrefix(r.URL.Path, prefix) {
		fail(w, 404, "unknown credential endpoint")
		return
	}
	id := "cred://" + strings.TrimPrefix(r.URL.Path, prefix)
	if !config.ValidCredentialRef(id) {
		fail(w, 400, "invalid credential id")
		return
	}
	switch r.Method {
	case "PUT":
		var p struct {
			Kind     string                         `json:"kind"`
			Source   string                         `json:"source"`
			Value    string                         `json:"value"`
			External *credentials.ExternalReference `json:"external,omitempty"`
			OAuth    *credentials.OAuthGrant        `json:"oauth,omitempty"`
		}
		d := json.NewDecoder(http.MaxBytesReader(w, r.Body, (1<<20)+4096))
		d.DisallowUnknownFields()
		if d.Decode(&p) != nil || d.Decode(new(any)) != io.EOF {
			fail(w, 400, "invalid credential input")
			return
		}
		metadata := s.vault.List()
		found := false
		for i := range metadata {
			if metadata[i].ID == id {
				metadata[i].Kind = p.Kind
				found = true
			}
		}
		if !found {
			metadata = append(metadata, credentials.Metadata{ID: id, Kind: p.Kind})
		}
		if err := validateCredentialBindings(s.cfg, metadata); err != nil {
			fail(w, 409, err.Error())
			return
		}
		if (p.External != nil && p.OAuth != nil) || ((p.External != nil || p.OAuth != nil) && p.Value != "") || (p.OAuth != nil && p.Kind != "oauth") {
			fail(w, 400, "choose one credential value, external reference or OAuth grant")
			return
		}
		var saveErr error
		switch {
		case p.External != nil:
			saveErr = s.vault.PutExternal(id, p.Kind, *p.External)
		case p.OAuth != nil:
			saveErr = s.vault.PutOAuth(id, p.Source, *p.OAuth)
		default:
			saveErr = s.vault.Put(id, p.Kind, p.Source, p.Value)
		}
		if err := saveErr; err != nil {
			fail(w, 400, err.Error())
			return
		}
		reply(w, map[string]string{"id": id, "status": "saved"})
	case "DELETE":
		for _, a := range s.cfg.Accounts {
			if a.CredentialRef == id || a.LoginCredentialRef == id {
				fail(w, 409, "credential is bound to an account")
				return
			}
		}
		for _, source := range s.cfg.Sources {
			if source.CredentialRef == id {
				fail(w, 409, "credential is bound to a source")
				return
			}
		}
		if err := s.vault.Delete(id); err != nil {
			fail(w, 400, err.Error())
			return
		}
		reply(w, map[string]string{"status": "deleted"})
	default:
		fail(w, 405, "method not allowed")
	}
}

func (s *Server) credentialLifecycleAdmin(w http.ResponseWriter, r *http.Request) bool {
	parts := strings.Split(strings.TrimPrefix(r.URL.Path, "/admin/credentials/"), "/")
	if len(parts) != 2 || (parts[1] != "check" && parts[1] != "refresh") {
		return false
	}
	if r.Method != "POST" {
		fail(w, 405, "method not allowed")
		return true
	}
	id := "cred://" + parts[0]
	if !config.ValidCredentialRef(id) {
		fail(w, 400, "invalid credential reference")
		return true
	}
	var err error
	if parts[1] == "refresh" {
		err = s.vault.RefreshOAuth(r.Context(), id)
	} else {
		_, err = s.vault.ResolveContext(r.Context(), id)
	}
	if err != nil {
		fail(w, 400, err.Error())
		return true
	}
	version := uint64(0)
	for _, meta := range s.vault.List() {
		if meta.ID == id {
			version = meta.Version
		}
	}
	reply(w, map[string]any{"id": id, "available": true, "version": version, "generation_verified": false})
	return true
}
