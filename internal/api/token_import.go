package api

import (
	"net/http"
	"os"

	"clash-of-tokens/internal/credentials"
)

func (s *Server) tokenImport(w http.ResponseWriter, r *http.Request) bool {
	if r.URL.Path != "/admin/credentials/import-env" && r.URL.Path != "/admin/credentials/import-cli" {
		return false
	}
	if r.Method != "POST" {
		fail(w, 405, "method not allowed")
		return true
	}
	var input struct {
		Source string `json:"source"`
		Format string `json:"format"`
		Data   string `json:"data"`
		Kind   string `json:"kind"`
		Apply  bool   `json:"apply"`
	}
	if decodeInput(w, r, &input, 2<<20) != nil {
		fail(w, 400, "invalid token import request")
		return true
	}
	value := ""
	origin := ""
	kind := input.Kind
	if r.URL.Path == "/admin/credentials/import-env" {
		name := ""
		for _, source := range s.cfg.Sources {
			if source.ID == input.Source {
				name = source.KeyEnv
			}
		}
		if name == "" || name == s.cfg.APIKeyEnv || name == s.cfg.AdminKeyEnv {
			fail(w, 400, "source has no importable credential variable")
			return true
		}
		if kind != "api_key" && kind != "oauth" && kind != "cookie" {
			fail(w, 400, "select api_key, oauth or cookie")
			return true
		}
		value = os.Getenv(name)
		origin = "environment:" + input.Source
		if !input.Apply {
			reply(w, map[string]any{"variable": name, "available": value != "", "kind": kind})
			return true
		}
	} else {
		var err error
		value, err = credentials.ParseCLIAccessToken(input.Format, []byte(input.Data))
		if err != nil {
			fail(w, 400, err.Error())
			return true
		}
		kind = "oauth"
		origin = "cli-export:" + input.Format
		if !input.Apply {
			reply(w, map[string]any{"format": input.Format, "available": true, "kind": kind, "automatic_refresh": false, "message": "Imports only the current access token. Refresh and account-ID fields are not copied. Re-import after the CLI refreshes its token."})
			return true
		}
	}
	if value == "" {
		fail(w, 400, "selected credential is unavailable")
		return true
	}
	metadata, err := s.vault.Create(kind, origin, value)
	if err != nil {
		fail(w, 400, err.Error())
		return true
	}
	reply(w, metadata)
	return true
}
