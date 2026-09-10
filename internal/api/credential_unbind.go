package api

import (
	"errors"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"

	"clash-of-tokens/internal/config"
)

func credentialUnbindPath(path string) bool {
	return strings.HasPrefix(path, "/admin/credentials/") && strings.HasSuffix(path, "/unbind")
}

// credentialUnbindAdmin removes a protected reference from every account and
// source that uses it. The encrypted vault record is deliberately retained so
// an operator can rebind it later or delete it through the separate, guarded
// credential endpoint.
func (p *ControlPlane) credentialUnbindAdmin(w http.ResponseWriter, r *http.Request) bool {
	const prefix = "/admin/credentials/"
	if !strings.HasPrefix(r.URL.Path, prefix) {
		return false
	}
	parts := strings.Split(strings.TrimPrefix(r.URL.Path, prefix), "/")
	if len(parts) != 2 || parts[1] != "unbind" {
		return false
	}
	if r.Method != http.MethodPost {
		fail(w, http.StatusMethodNotAllowed, "use POST /admin/credentials/{id}/unbind")
		return true
	}
	id, err := url.PathUnescape(parts[0])
	if err != nil || !config.ValidCredentialRef("cred://"+id) {
		fail(w, http.StatusBadRequest, "invalid credential id")
		return true
	}
	var input struct {
		Revision uint64 `json:"revision"`
		Confirm  bool   `json:"confirm"`
	}
	if decodeInput(w, r, &input, 1024) != nil || !input.Confirm {
		fail(w, http.StatusBadRequest, "confirm unbinding this credential")
		return true
	}
	current := p.service.Current()
	expected := input.Revision
	if match := strings.Trim(r.Header.Get("If-Match"), "\""); match != "" {
		expected, err = strconv.ParseUint(match, 10, 64)
		if err != nil {
			fail(w, http.StatusBadRequest, "invalid revision")
			return true
		}
	}
	if expected == 0 {
		expected = current.Revision
	}
	if expected != current.Revision {
		fail(w, http.StatusConflict, config.ErrRevisionConflict.Error())
		return true
	}

	next := current.Config
	accountIDs := map[string]bool{}
	accounts := []string{}
	for i := range next.Accounts {
		if next.Accounts[i].CredentialRef == "cred://"+id {
			accountIDs[next.Accounts[i].ID] = true
			accounts = append(accounts, next.Accounts[i].ID)
			next.Accounts[i].CredentialRef = ""
		}
	}
	sources := []string{}
	for i := range next.Sources {
		source := &next.Sources[i]
		if source.CredentialRef == "cred://"+id || (source.CredentialRef == "" && accountIDs[source.AccountID]) {
			sources = append(sources, source.ID)
		}
		if source.CredentialRef == "cred://"+id {
			source.CredentialRef = ""
		}
	}
	if len(accounts) == 0 && len(sources) == 0 {
		fail(w, http.StatusConflict, "credential is already unbound")
		return true
	}
	sort.Strings(accounts)
	sort.Strings(sources)
	updated, err := p.service.Apply(expected, next, "Unbind credential cred://"+id)
	if err != nil {
		status := http.StatusBadRequest
		if errors.Is(err, config.ErrRevisionConflict) {
			status = http.StatusConflict
		}
		fail(w, status, err.Error())
		return true
	}
	reply(w, map[string]any{
		"status":           "unbound",
		"credential":       "cred://" + id,
		"revision":         updated.Revision,
		"accounts":         accounts,
		"sources":          sources,
		"vault_retained":   true,
		"restart_required": restartFields(p.startup, updated.Config),
	})
	return true
}
