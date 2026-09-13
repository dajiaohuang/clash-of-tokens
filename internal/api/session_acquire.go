package api

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"time"

	"clash-of-tokens/catalog"
	"clash-of-tokens/internal/browserauth"
	"clash-of-tokens/internal/browsermeta"
	"clash-of-tokens/internal/config"
	"clash-of-tokens/internal/credentials"
)

// Session acquisition is an explicit operation. It never submits a prompt or
// consumes login passwords as API credentials. Generation is validated separately.
func (p *ControlPlane) acquireSessionAdmin(w http.ResponseWriter, r *http.Request) bool {
	parts := strings.Split(strings.TrimPrefix(r.URL.Path, "/admin/accounts/"), "/")
	if !strings.HasPrefix(r.URL.Path, "/admin/accounts/") || len(parts) != 2 || parts[1] != "acquire-session" {
		return false
	}
	if r.Method != http.MethodPost {
		fail(w, 405, "use POST to acquire a session")
		return true
	}
	var input struct {
		Revision uint64 `json:"revision"`
		Confirm  bool   `json:"confirm"`
	}
	if decodeInput(w, r, &input, 1024) != nil || !input.Confirm {
		fail(w, 400, "review the account and confirm session binding")
		return true
	}
	current := p.service.Current()
	if input.Revision != current.Revision {
		fail(w, 409, config.ErrRevisionConflict.Error())
		return true
	}
	var account config.Account
	var profile config.BrowserProfile
	for _, a := range current.Config.Accounts {
		if a.ID == parts[0] {
			account = a
		}
	}
	for _, v := range current.Config.BrowserProfiles {
		if v.ID == account.BrowserProfileID {
			profile = v
		}
	}
	if account.ID == "" || profile.ID == "" || !profile.Enabled {
		fail(w, 400, "choose an account with an enabled browser profile")
		return true
	}
	adapter, origin := "", ""
	for _, entry := range catalog.All() {
		if entry.ID == account.ProviderID {
			adapter, origin = entry.Adapter, entry.BaseURL
		}
	}
	if adapter != "chatgpt-web" && adapter != "claude-web" {
		fail(w, 400, "automatic session acquisition is not implemented for this provider; import its documented invocation credential")
		return true
	}
	if (adapter == "chatgpt-web" && account.ExpectedIdentity == "") || (adapter == "claude-web" && account.Organization == "") {
		fail(w, 400, "set the expected browser identity (ChatGPT) or organization (Claude) before binding")
		return true
	}
	if account.BaseURL != "" && strings.TrimRight(account.BaseURL, "/") != strings.TrimRight(origin, "/") {
		fail(w, 400, "browser session binding requires the provider's declared destination")
		return true
	}
	for _, source := range current.Config.Sources {
		if source.AccountID == account.ID && source.CredentialRef == "" && source.BaseURL != "" && strings.TrimRight(source.BaseURL, "/") != strings.TrimRight(origin, "/") {
			fail(w, 400, "an inheriting source uses a different destination; review its binding first")
			return true
		}
	}
	check := browserauth.CheckExpected(r.Context(), profile.CDPURL, origin, adapter, profile.Engine, account.ExpectedIdentity, account.Organization)
	if check.Status != "authenticated" {
		reply(w, map[string]any{"status": check.Status, "session_ready": false, "generation_verified": false})
		return true
	}
	value := ""
	if adapter == "claude-web" {
		var count int
		var err error
		value, count, err = browsermeta.CookiesEngine(r.Context(), profile.CDPURL, origin, profile.Engine)
		if err != nil || count == 0 {
			fail(w, 400, "no applicable invocation cookies were acquired")
			return true
		}
		// Verify the exact captured credential, not a prior page snapshot. No
		// redirect is allowed to forward this cookie to a different destination.
		client := &http.Client{Timeout: 10 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
		if err = verifyClaudeTenant(r.Context(), client, origin, value, account.Organization); err != nil {
			fail(w, 400, err.Error())
			return true
		}
	}
	var updated config.Version
	var metadata credentials.Metadata
	err := commitOperation(r, func() error {
		var err error
		updated, metadata, err = p.bindAcquiredSession(input.Revision, account.ID, value)
		return err
	})
	if err != nil {
		fail(w, 409, err.Error())
		return true
	}
	reply(w, map[string]any{"status": "session_ready", "session_ready": true, "generation_verified": false, "revision": updated.Revision, "account": account.ID, "profile": profile.ID, "credential_ref": metadata.ID, "credential_version": metadata.Version, "next_action": "validate_source"})
	return true
}

func verifyClaudeTenant(ctx context.Context, client *http.Client, origin, cookie, tenant string) error {
	r, err := http.NewRequestWithContext(ctx, "GET", strings.TrimRight(origin, "/")+"/api/organizations", nil)
	if err != nil {
		return errors.New("invalid session verification destination")
	}
	r.Header.Set("Cookie", cookie)
	r.Header.Set("Accept", "application/json")
	resp, err := client.Do(r)
	if err != nil {
		return errors.New("captured session could not be verified")
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, (1<<20)+1))
	var organizations []struct {
		UUID string `json:"uuid"`
	}
	if err != nil || len(body) > 1<<20 || resp.StatusCode != 200 || json.Unmarshal(body, &organizations) != nil {
		return errors.New("captured session is not authenticated")
	}
	for _, organization := range organizations {
		if organization.UUID == tenant {
			return nil
		}
	}
	return errors.New("captured session does not match the expected organization")
}

// Called only under the control-plane commit lock. A new reference prevents an
// unsuccessful rebind from altering a credential used by another account.
func (p *ControlPlane) bindAcquiredSession(revision uint64, accountID, cookie string) (config.Version, credentials.Metadata, error) {
	current := p.service.Current()
	if current.Revision != revision {
		return config.Version{}, credentials.Metadata{}, config.ErrRevisionConflict
	}
	next := current.Config
	index := -1
	for i := range next.Accounts {
		if next.Accounts[i].ID == accountID {
			index = i
		}
	}
	if index < 0 {
		return config.Version{}, credentials.Metadata{}, errors.New("account no longer exists")
	}
	var meta credentials.Metadata
	var err error
	if cookie != "" {
		meta, err = p.vault.Create("cookie", "session:"+accountID, cookie)
		if err != nil {
			return config.Version{}, meta, err
		}
		next.Accounts[index].CredentialRef = meta.ID
		next.Accounts[index].CredentialTypeOverride = false
	}
	updated, err := p.service.Apply(revision, next, "Bind acquired session for account "+accountID)
	if err != nil && meta.ID != "" {
		if cleanupErr := p.vault.Delete(meta.ID); cleanupErr != nil {
			return config.Version{}, meta, errors.New("configuration was not committed; unbound encrypted credential " + meta.ID + " requires deletion")
		}
	}
	return updated, meta, err
}
