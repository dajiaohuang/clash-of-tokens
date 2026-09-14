package api

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"net/url"
	"slices"
	"sync"
	"time"

	"clash-of-tokens/catalog"
	"clash-of-tokens/internal/browserauth"
	"clash-of-tokens/internal/browserexec"
	"clash-of-tokens/internal/browsermeta"
	"clash-of-tokens/internal/config"
	"clash-of-tokens/internal/credentials"
	"clash-of-tokens/internal/evidence"
	"clash-of-tokens/internal/providerdef"
	"github.com/chromedp/cdproto/network"
	"github.com/chromedp/chromedp"
)

type loginSite struct {
	Name        string `json:"name"`
	Account     string `json:"account"`
	Provider    string `json:"provider"`
	Destination string `json:"destination"`
	State       string `json:"state"`
	Detail      string `json:"detail,omitempty"`
	Mode        string `json:"mode"`
	Adapter     string `json:"-"`
}
type loginConnection struct {
	ID       string `json:"id"`
	Browser  string `json:"browser"`
	Ready    bool   `json:"ready"`
	State    string `json:"state"`
	Endpoint string `json:"-"`
}
type loginBatchView struct {
	StartedAt    time.Time       `json:"started_at,omitempty"`
	FinishedAt   time.Time       `json:"finished_at,omitempty"`
	HistoryError bool            `json:"history_error,omitempty"`
	ID           string          `json:"id"`
	Browser      string          `json:"browser"`
	State        string          `json:"state"`
	Sites        []loginSite     `json:"sites"`
	Connection   loginConnection `json:"-"`
}
type loginBatchQueue struct {
	path    string
	mu      sync.Mutex
	view    loginBatchView
	ticket  string
	expires time.Time
	version operationVersion
	cancel  context.CancelFunc
	closed  bool
}

func (q *loginBatchQueue) close() {
	q.mu.Lock()
	defer q.mu.Unlock()
	q.closed = true
	if q.cancel != nil {
		q.cancel()
	}
}
func installedLoginBrowsers() []string {
	out := []string{}
	for _, engine := range config.BrowserEngines {
		if _, err := browserExecutable(engine); err == nil {
			out = append(out, engine)
		}
	}
	return out
}
func connectionID(browser, root string) string {
	s := sha256.Sum256([]byte(browser + "\x00" + root))
	return hex.EncodeToString(s[:12])
}
func existingLoginConnections() []loginConnection {
	installed := installedLoginBrowsers()
	out := []loginConnection{}
	for _, root := range browsermeta.StandardRoots() {
		if !slices.Contains(installed, root.Browser) {
			continue
		}
		c := loginConnection{ID: connectionID(root.Browser, root.Path), Browser: root.Browser, State: "authorization_required"}
		if root.Browser == "firefox" {
			c.State = "existing_profile_connection_unsupported"
		} else if endpoint, err := browsermeta.ExistingEndpoint(root.Path); err == nil {
			c.Endpoint = endpoint
			c.Ready = true
			c.State = "authorization_metadata_found"
		}
		out = append(out, c)
	}
	return out
}

// Full catalog coverage, not the existing account list. Unsupported rows remain
// visible, with no network or cookie reads for those rows.
func (p *ControlPlane) loginSites(ids []string) []loginSite {
	out := []loginSite{}
	for _, entry := range catalog.All() {
		if len(ids) > 0 && !slices.Contains(ids, entry.ID) {
			continue
		}
		s := loginSite{Name: entry.ID, Provider: entry.ID, Destination: entry.BaseURL, Adapter: entry.Adapter, State: "unsupported", Mode: "none", Detail: "No compatible browser-session collector"}
		u, err := url.Parse(entry.BaseURL)
		d, known := providerdef.Lookup(entry.Adapter)
		if err == nil && u.Scheme == "https" && u.Hostname() != "" && u.User == nil && known && entry.Implementation != "not_implemented" {
			if slices.Contains(d.InvokeCredentials, "cookie") {
				s.Mode = "cookie"
				s.State = "queued"
				s.Detail = ""
			} else if d.BrowserRequired || slices.Contains(d.InvokeCredentials, "browser_profile") {
				s.Mode = "browser"
				s.State = "queued"
				s.Detail = ""
			}
		}
		out = append(out, s)
	}
	return out
}
func (p *ControlPlane) browserLoginBatchAdmin(w http.ResponseWriter, r *http.Request) bool {
	const root = "/admin/accounts/browser-login"
	if r.URL.Path != root && r.URL.Path != root+"/preview" && r.URL.Path != root+"/start" && r.URL.Path != root+"/cancel" {
		return false
	}
	q := &p.loginBatch
	if r.URL.Path == root && r.Method == "GET" {
		q.mu.Lock()
		defer q.mu.Unlock()
		reply(w, map[string]any{"browsers": installedLoginBrowsers(), "connections": existingLoginConnections(), "sites": p.loginSites(nil), "batch": q.view})
		return true
	}
	if r.Method != "POST" {
		fail(w, 405, "use POST")
		return true
	}
	var input struct {
		Browser    string   `json:"browser"`
		Connection string   `json:"connection"`
		Providers  []string `json:"providers"`
		Accounts   []string `json:"accounts"`
		Ticket     string   `json:"ticket"`
		Confirm    bool     `json:"confirm"`
	}
	if decodeInput(w, r, &input, 8192) != nil {
		fail(w, 400, "invalid scan request")
		return true
	}
	q.mu.Lock()
	defer q.mu.Unlock()
	if q.closed {
		fail(w, 503, "gateway is closing")
		return true
	}
	if r.URL.Path == root+"/cancel" {
		q.ticket = ""
		if q.cancel != nil {
			q.cancel()
		}
		reply(w, map[string]string{"state": "cancellation_requested"})
		return true
	}
	if q.view.State == "running" {
		fail(w, 409, "a session scan is already running")
		return true
	}
	if r.URL.Path == root+"/preview" {
		var connection loginConnection
		for _, c := range existingLoginConnections() {
			if (input.Connection != "" && input.Connection == c.ID) || (input.Connection == "" && input.Browser == c.Browser) {
				connection = c
				break
			}
		}
		if !connection.Ready {
			fail(w, 409, "Open the selected browser and enable remote debugging in its existing profile first; no blank profile will be created")
			return true
		}
		ids := input.Providers
		if len(input.Accounts) > 0 {
			for _, id := range input.Accounts {
				found := false
				for _, a := range p.service.Current().Config.Accounts {
					if a.ID == id {
						ids = append(ids, a.ProviderID)
						found = true
						break
					}
				}
				if !found {
					fail(w, 400, "unknown account")
					return true
				}
			}
		}
		sites := p.loginSites(ids)
		if len(sites) == 0 || (len(input.Providers) > 0 && len(sites) != len(input.Providers)) {
			fail(w, 400, "unknown or duplicate provider selection")
			return true
		}
		q.ticket = rand.Text()
		q.expires = time.Now().Add(5 * time.Minute)
		q.version = p.operationVersion()
		q.view = loginBatchView{Browser: connection.Browser, State: "preview", Sites: sites, Connection: connection}
		reply(w, map[string]any{"ticket": q.ticket, "browser": connection.Browser, "sites": sites, "expires_in_seconds": 300})
		return true
	}
	if !input.Confirm || input.Ticket == "" || input.Ticket != q.ticket || time.Now().After(q.expires) || q.version != p.operationVersion() {
		fail(w, 409, "confirmation expired or configuration changed; review again")
		return true
	}
	fresh := false
	for _, c := range existingLoginConnections() {
		if c.ID == q.view.Connection.ID && c.Endpoint == q.view.Connection.Endpoint && c.Ready {
			fresh = true
		}
	}
	if !fresh {
		fail(w, 409, "browser connection changed; rediscover and confirm again")
		return true
	}
	q.ticket = ""
	q.view.ID = rand.Text()
	q.view.State = "running"
	q.view.StartedAt = time.Now().UTC()
	if err := q.save(); err != nil {
		q.view.State = "history_failed"
		fail(w, 503, "Cannot persist scan history; no browser connection was started")
		return true
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Minute)
	q.cancel = cancel
	view := q.view
	view.Sites = append([]loginSite(nil), view.Sites...)
	go p.runLoginBatch(ctx, view, r.Host, q.version)
	reply(w, q.view)
	return true
}
func scanAccountID(connection, provider string) string {
	s := sha256.Sum256([]byte(connection + "\x00" + provider))
	return "session-" + hex.EncodeToString(s[:12])
}
func (p *ControlPlane) saveScannedSession(ctx context.Context, connection loginConnection, site loginSite, value string, authenticated bool, expected *operationVersion) (string, error) {
	p.changes.Lock()
	defer p.changes.Unlock()
	if err := ctx.Err(); err != nil {
		return "", err
	}
	if *expected != p.operationVersion() {
		return "", config.ErrRevisionConflict
	}
	current := p.service.Current()
	next := current.Config
	next.Accounts = append([]config.Account(nil), next.Accounts...)
	next.Providers = append([]config.Provider(nil), next.Providers...)
	next.BrowserProfiles = append([]config.BrowserProfile(nil), next.BrowserProfiles...)
	profileID := "connected-" + connection.ID
	profile := config.BrowserProfile{ID: profileID, Engine: connection.Browser, CDPURL: connection.Endpoint, Enabled: true, External: true}
	found := false
	for i, b := range next.BrowserProfiles {
		if b.ID == profileID {
			if !b.External {
				return "", config.ErrRevisionConflict
			}
			next.BrowserProfiles[i] = profile
			found = true
		}
	}
	if !found {
		next.BrowserProfiles = append(next.BrowserProfiles, profile)
	}
	id := scanAccountID(connection.ID, site.Provider)
	index := -1
	for i, a := range next.Accounts {
		if a.ID == id {
			if a.ProviderID != site.Provider || a.BrowserProfileID != profileID {
				return "", config.ErrRevisionConflict
			}
			index = i
		}
	}
	if index < 0 {
		next.Accounts = append(next.Accounts, config.Account{ID: id, ProviderID: site.Provider, DisplayName: site.Provider + " · " + connection.Browser + " session", QuotaDomain: id, MaxInflight: 1, Weight: 1, CreatedAt: time.Now().UTC()})
		index = len(next.Accounts) - 1
	}
	account := &next.Accounts[index]
	account.BrowserProfileID = profileID
	var ref string
	if value != "" {
		if account.CredentialRef != "" && p.credentialMetadata(account.CredentialRef).Kind != "cookie" {
			return "", config.ErrRevisionConflict
		}
		meta, err := p.vault.CreateAccountMaterial(credentials.AccountOwner{Account: id, Provider: site.Provider}, "cookie", value)
		if err != nil {
			return "", err
		}
		ref = meta.ID
		account.CredentialRef = ref
	}
	account.Enabled = false
	account.VerificationState = "unverified"
	account.VerificationBinding = ""
	account.VerificationAt = nil
	// Browser authentication is recorded separately. It does not certify generic
	// captured cookies or grant model routing without a source-level check.
	providerFound := false
	for _, v := range next.Providers {
		if v.ID == site.Provider {
			providerFound = true
		}
	}
	if !providerFound {
		next.Providers = append(next.Providers, config.Provider{ID: site.Provider, Enabled: true})
	}
	updated, err := p.service.Apply(current.Revision, next, "Capture selected existing-browser session")
	if err != nil {
		if ref != "" {
			_ = p.vault.Delete(ref)
		}
		return "", err
	}
	*expected = p.operationVersion()
	status := "unknown"
	if authenticated {
		status = "authenticated"
	}
	_ = p.evidence.Append(evidence.Entry{Revision: updated.Revision, Kind: "authentication", Resource: id, CheckedAt: time.Now().UTC(), Method: "existing_browser_session_capture", Status: status})
	return id, nil
}
func (p *ControlPlane) runLoginBatch(ctx context.Context, view loginBatchView, _ string, expected operationVersion) {
	q := &p.loginBatch
	terminal := "completed"
	defer func() {
		if recover() != nil {
			terminal = "failed"
		}
		q.mu.Lock()
		defer q.mu.Unlock()
		if ctx.Err() != nil {
			terminal = "canceled"
		}
		q.view.State = terminal
		q.view.FinishedAt = time.Now().UTC()
		for i := range q.view.Sites {
			if q.view.Sites[i].State == "queued" || q.view.Sites[i].State == "running" {
				q.view.Sites[i].State = "not_attempted"
			}
		}
		if q.cancel != nil {
			q.cancel()
			q.cancel = nil
		}
		if q.save() != nil {
			q.view.HistoryError = true
		}
	}()
	if ctx.Err() != nil {
		return
	}
	tab, closeTab, err := browserexec.OpenAuthorizedCDP(ctx, view.Connection.Endpoint)
	if err != nil {
		terminal = "connection_failed"
		return
	}
	defer closeTab()
	for i, site := range view.Sites {
		if ctx.Err() != nil {
			return
		}
		if expected != p.operationVersion() {
			terminal = "configuration_changed"
			return
		}
		if site.State == "unsupported" {
			continue
		}
		q.mu.Lock()
		q.view.Sites[i].State = "running"
		q.mu.Unlock()
		state, detail := "no_session", "No applicable cookies or verified browser session"
		child, stop := context.WithTimeout(tab, 12*time.Second)
		var cookies []*network.Cookie
		err := chromedp.Run(child, chromedp.Navigate(site.Destination), chromedp.ActionFunc(func(c context.Context) error {
			var e error
			cookies, e = network.GetCookies().WithURLs([]string{site.Destination}).Do(c)
			return e
		}))
		auth := browserauth.Evidence{Status: "unknown"}
		if err == nil {
			auth = browserauth.CheckPage(child, site.Destination, site.Adapter)
		}
		stop()
		account := ""
		if err != nil {
			state, detail = "site_unavailable", "Navigation or session read timed out"
		} else {
			value, count, e := browsermeta.CookieSnapshot(cookies)
			if e != nil {
				state, detail = "capture_failed", "Cookie snapshot exceeded limits"
			} else if (site.Mode == "cookie" && count > 0) || (site.Mode == "browser" && auth.Status == "authenticated") {
				if site.Mode != "cookie" {
					value = ""
				}
				if ctx.Err() != nil {
					return
				}
				account, e = p.saveScannedSession(ctx, view.Connection, site, value, auth.Status == "authenticated", &expected)
				value = ""
				if e != nil {
					state, detail = "save_failed", "Configuration changed or protected storage failed"
				} else if auth.Status == "authenticated" {
					state, detail = "session_saved", "Browser authentication observed; API/model access not verified"
				} else {
					state, detail = "candidate_saved", "Cookie candidate saved; authentication not established"
				}
			}
		}
		q.mu.Lock()
		q.view.Sites[i].State = state
		q.view.Sites[i].Detail = detail
		q.view.Sites[i].Account = account
		if q.save() != nil {
			q.view.HistoryError = true
			q.mu.Unlock()
			terminal = "history_failed"
			return
		}
		q.mu.Unlock()
	}
}
