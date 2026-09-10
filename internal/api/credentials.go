package api

import (
	"clash-of-tokens/internal/config"
	"clash-of-tokens/internal/credentials"
	"encoding/json"
	"io"
	"net/http"
	"strings"
)

func NewWithVault(c config.Config, key, admin string, vault *credentials.Store) (*Server, error) {
	s, err := NewWithCredentials(c, key, admin, vault.Resolve)
	if err != nil {
		return nil, err
	}
	s.vault = vault
	return s, nil
}

func (s *Server) credentialAdmin(w http.ResponseWriter, r *http.Request) {
	if s.vault == nil {
		fail(w, 503, "credential vault not configured")
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
			Kind   string `json:"kind"`
			Source string `json:"source"`
			Value  string `json:"value"`
		}
		d := json.NewDecoder(http.MaxBytesReader(w, r.Body, (1<<20)+4096))
		d.DisallowUnknownFields()
		if d.Decode(&p) != nil || d.Decode(new(any)) != io.EOF {
			fail(w, 400, "invalid credential input")
			return
		}
		if err := s.vault.Put(id, p.Kind, p.Source, p.Value); err != nil {
			fail(w, 400, err.Error())
			return
		}
		reply(w, map[string]string{"id": id, "status": "saved"})
	case "DELETE":
		for _, a := range s.cfg.Accounts {
			if a.CredentialRef == id {
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
