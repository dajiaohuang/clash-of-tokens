package api

import (
	"bytes"
	"clash-of-tokens/catalog"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"clash-of-tokens/internal/config"
	"clash-of-tokens/internal/credentials"
)

func (p *ControlPlane) accountMaterialAdmin(w http.ResponseWriter, r *http.Request) bool {
	if r.URL.Path == "/admin/accounts/materials" {
		if r.Method != "GET" {
			fail(w, 405, "use GET")
		} else {
			reply(w, p.vault.List())
		}
		return true
	}
	parts := strings.Split(strings.Trim(r.URL.Path, "/"), "/")
	if len(parts) == 4 && parts[0] == "admin" && parts[1] == "accounts" && parts[3] == "prepare-login" {
		return p.prepareAccountLogin(w, r, parts[2])
	}
	if len(parts) != 4 || parts[0] != "admin" || parts[1] != "accounts" || parts[3] != "material" {
		return false
	}
	if r.Method != "POST" {
		fail(w, 405, "use POST")
		return true
	}
	var input struct {
		Kind     string `json:"kind"`
		Username string `json:"username"`
		Password string `json:"password"`
		Value    string `json:"value"`
	}
	if decodeInput(w, r, &input, 1<<20) != nil {
		fail(w, 400, "invalid account material")
		return true
	}
	current := p.service.Current()
	next := current.Config
	index := -1
	for i, a := range next.Accounts {
		if a.ID == parts[2] {
			index = i
			break
		}
	}
	if index < 0 {
		fail(w, 404, "unknown account")
		return true
	}
	a := &next.Accounts[index]
	ref := "cred://account-" + rand.Text()
	value := input.Value
	if input.Kind == "username_password" {
		b, _ := json.Marshal(map[string]string{"username": input.Username, "password": input.Password})
		value = string(b)
		clear(b)
		a.LoginCredentialRef = ref
	} else {
		a.CredentialRef = ref
	}
	if err := p.vault.PutAccount(credentials.AccountOwner{Account: a.ID, Provider: a.ProviderID}, ref, input.Kind, value); err != nil {
		fail(w, 400, err.Error())
		return true
	}
	a.VerificationState = "unverified"
	a.VerificationAt = nil
	a.VerificationVersion = 0
	a.Enabled = false
	// The metadata of manually entered passwords has no imported domain. Login
	// material is explicitly scoped to this chosen account rather than inferred.
	domain := ""
	for _, entry := range catalog.All() {
		if entry.ID == a.ProviderID {
			if u, err := url.Parse(entry.BaseURL); err == nil {
				domain = u.Hostname()
			}
			break
		}
	}
	if err := p.vault.SetAccountDomain(ref, domain); err != nil {
		_ = p.vault.Delete(ref)
		fail(w, 400, err.Error())
		return true
	}
	updated, err := p.service.Apply(current.Revision, next, "Update encrypted provider account material")
	if err != nil {
		_ = p.vault.Delete(ref)
		fail(w, 400, err.Error())
		return true
	}
	reply(w, map[string]any{"account": a.ID, "state": "unverified", "revision": updated.Revision})
	return true
}

func (p *ControlPlane) prepareAccountLogin(w http.ResponseWriter, r *http.Request, id string) bool {
	if r.Method != "POST" {
		fail(w, 405, "use POST")
		return true
	}
	if r.Context().Err() != nil {
		fail(w, 409, "login canceled")
		return true
	}
	if v, ok := r.Context().Value(operationKey{}).(operationVersion); ok && v != p.operationVersion() {
		fail(w, 409, "configuration changed; confirm again")
		return true
	}
	var input struct {
		Browser  string  `json:"browser"`
		Revision *uint64 `json:"revision"`
	}
	if r.Body != nil && r.ContentLength != 0 {
		if decodeInput(w, r, &input, 1024) != nil {
			fail(w, 400, "invalid browser selection")
			return true
		}
	}
	if input.Browser == "" {
		input.Browser = "chrome"
	}
	if _, err := browserExecutable(input.Browser); err != nil {
		fail(w, 400, "selected browser is not installed")
		return true
	}
	current := p.service.Current()
	if input.Revision != nil && *input.Revision != current.Revision {
		fail(w, 409, "configuration changed; confirm again")
		return true
	}
	next := current.Config
	index := -1
	for i, a := range next.Accounts {
		if a.ID == id {
			index = i
			break
		}
	}
	if index < 0 {
		fail(w, 404, "unknown account")
		return true
	}
	if next.Accounts[index].BrowserProfileID != "" {
		for _, existing := range next.BrowserProfiles {
			if existing.ID == next.Accounts[index].BrowserProfileID && existing.Engine == input.Browser && existing.Enabled {
				reply(w, map[string]any{"profile": next.Accounts[index].BrowserProfileID})
				return true
			}
		}
	}
	used := map[string]bool{}
	for _, profile := range next.BrowserProfiles {
		u, _ := url.Parse(profile.CDPURL)
		if u != nil {
			used[u.Port()] = true
		}
	}
	port := 9223
	for used[strconv.Itoa(port)] {
		port++
	}
	profileID := "login-" + input.Browser + "-" + id
	for _, profile := range next.BrowserProfiles {
		if profile.ID == profileID {
			fail(w, 409, "login profile ID already exists; select it in account settings")
			return true
		}
	}
	next.BrowserProfiles = append(next.BrowserProfiles, config.BrowserProfile{ID: profileID, Engine: input.Browser, Enabled: true, CDPURL: "http://127.0.0.1:" + strconv.Itoa(port)})
	next.Accounts[index].BrowserProfileID = profileID
	next.Accounts[index].Enabled = false
	next.Accounts[index].VerificationState = "unverified"
	next.Accounts[index].VerificationBinding = ""
	updated, err := p.service.Apply(current.Revision, next, "Prepare isolated provider account login")
	if err != nil {
		fail(w, 400, err.Error())
		return true
	}
	reply(w, map[string]any{"profile": profileID, "revision": updated.Revision})
	return true
}

type accountCheckCapture struct {
	headers http.Header
	body    bytes.Buffer
	status  int
}

func (c *accountCheckCapture) Header() http.Header    { return c.headers }
func (c *accountCheckCapture) WriteHeader(status int) { c.status = status }
func (c *accountCheckCapture) Write(b []byte) (int, error) {
	if c.status == 0 {
		c.status = 200
	}
	return c.body.Write(b)
}

func (p *ControlPlane) accountState(a config.Account) string {
	ref := a.CredentialRef
	if ref == "" {
		ref = a.LoginCredentialRef
	}
	for _, bound := range []string{a.CredentialRef, a.LoginCredentialRef} {
		if bound != "" && (p.credentialMetadata(bound).ID == "" || p.credentialMetadata(bound).State == "expired") {
			return "invalid"
		}
	}
	if ref != "" && p.credentialMetadata(ref).ID == "" {
		return "invalid"
	}
	if ref != "" && p.credentialMetadata(ref).State == "expired" {
		return "invalid"
	}
	if a.VerificationState == "verified" || a.VerificationState == "invalid" {
		if p.credentialMetadata(ref).Version == a.VerificationVersion && a.VerificationBinding == p.accountProof(a) {
			return a.VerificationState
		}
	}
	if ref == "" && a.BrowserProfileID == "" {
		return "unfilled"
	}
	return "unverified"
}

func (p *ControlPlane) accountProof(a config.Account) string {
	return p.accountProofConfig(p.service.Current().Config, a)
}
func (p *ControlPlane) accountProofConfig(c config.Config, a config.Account) string {
	a.VerificationBinding = ""
	a.VerificationState = ""
	a.VerificationAt = nil
	a.VerificationVersion = 0
	a.Enabled = false
	a.AutoApproved = false
	sources := []config.Source{}
	versions := []uint64{p.credentialMetadata(a.CredentialRef).Version, p.credentialMetadata(a.LoginCredentialRef).Version}
	for _, s := range c.Sources {
		if s.AccountID == a.ID {
			sources = append(sources, s)
			versions = append(versions, p.credentialMetadata(s.CredentialRef).Version)
		}
	}
	var profile config.BrowserProfile
	run := ""
	for _, b := range c.BrowserProfiles {
		if b.ID == a.BrowserProfileID {
			profile = b
			run = p.runtimeID
			break
		}
	}
	b, _ := json.Marshal([]any{a, sources, versions, profile, run})
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

func (p *ControlPlane) accountVerifyAdmin(w http.ResponseWriter, r *http.Request, s *Server) bool {
	parts := strings.Split(strings.Trim(r.URL.Path, "/"), "/")
	if len(parts) != 4 || parts[0] != "admin" || parts[1] != "accounts" || (parts[3] != "verify" && parts[3] != "verify-login") {
		return false
	}
	if r.Method != "POST" {
		fail(w, 405, "use POST")
		return true
	}
	var account config.Account
	for _, a := range s.cfg.Accounts {
		if a.ID == parts[2] {
			account = a
			break
		}
	}
	if account.ID == "" {
		fail(w, 404, "unknown account")
		return true
	}
	capture := &accountCheckCapture{headers: http.Header{}}
	forward := r.Clone(r.Context())
	forward.URL.Path = "/admin/accounts/" + parts[2] + "/validate"
	if parts[3] == "verify-login" {
		forward.URL.Path = "/admin/accounts/" + parts[2] + "/check-login"
		p.browserLoginAdmin(capture, forward)
	} else {
		p.accountValidateAdmin(capture, forward, s)
	}
	var result struct {
		Verified        bool   `json:"verified"`
		Status          string `json:"status"`
		HistoryRecorded bool   `json:"history_recorded"`
	}
	_ = json.Unmarshal(capture.body.Bytes(), &result)
	success := capture.status == 200 && (result.Verified || result.Status == "authenticated") && result.HistoryRecorded
	state := "unverified"
	if success {
		state = "verified"
	} else if p.accountState(account) == "verified" || p.accountState(account) == "invalid" {
		state = "invalid"
	} else if p.accountState(account) == "unfilled" {
		state = "unfilled"
	}
	err := commitOperation(r, func() error {
		current := p.service.Current()
		next := current.Config
		for i, a := range next.Accounts {
			if a.ID == account.ID {
				ref := a.CredentialRef
				if ref == "" {
					ref = a.LoginCredentialRef
				}
				now := time.Now().UTC()
				next.Accounts[i].VerificationState = state
				next.Accounts[i].VerificationAt = &now
				next.Accounts[i].VerificationVersion = p.credentialMetadata(ref).Version
				next.Accounts[i].VerificationBinding = p.accountProof(a)
				next.Accounts[i].Enabled = success
			}
		}
		_, err := p.service.Apply(current.Revision, next, "Verify provider account and update availability")
		return err
	})
	if err != nil {
		fail(w, 409, err.Error())
		return true
	}
	reply(w, map[string]any{"account": account.ID, "state": state, "enabled": success, "verified": success, "check_status": result.Status, "needs_setup": capture.status != 200})
	return true
}

// Stored approval is not runtime evidence. A changed binding or a new browser
// runtime requires verification again without deleting the persistent session.
func (p *ControlPlane) withVerifiedAccountPolicy(c config.Config) config.Config {
	out := c
	out.Accounts = append([]config.Account(nil), c.Accounts...)
	for i, a := range out.Accounts {
		if a.VerificationState == "" {
			continue
		} // Existing explicitly managed configurations.
		if a.VerificationState != "verified" || a.VerificationBinding != p.accountProofConfig(c, a) {
			out.Accounts[i].Enabled = false
		}
		for _, ref := range []string{a.CredentialRef, a.LoginCredentialRef} {
			if ref != "" && (p.credentialMetadata(ref).ID == "" || p.credentialMetadata(ref).State == "expired") {
				out.Accounts[i].Enabled = false
			}
		}
	}
	return out
}
