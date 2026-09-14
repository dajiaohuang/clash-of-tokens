package api

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/json"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"sync"
	"time"

	"clash-of-tokens/catalog"
)

// This queue contains metadata only. Sessions remain in each account's owned,
// persistent browser profile. It never reads personal profiles or passwords.
type loginSite struct {
	Name        string `json:"name"`
	Account     string `json:"account"`
	Provider    string `json:"provider"`
	Destination string `json:"destination"`
	State       string `json:"state"`
	Detail      string `json:"detail,omitempty"`
}
type loginBatchView struct {
	ID      string      `json:"id"`
	Browser string      `json:"browser"`
	State   string      `json:"state"`
	Sites   []loginSite `json:"sites"`
}
type loginBatchQueue struct {
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
	for _, engine := range []string{"chrome", "edge", "firefox", "brave", "vivaldi", "opera", "chromium", "arc"} {
		if _, err := browserExecutable(engine); err == nil {
			out = append(out, engine)
		}
	}
	return out
}
func (p *ControlPlane) loginSites(ids []string) []loginSite {
	out := []loginSite{}
	for _, a := range p.service.Current().Config.Accounts {
		if len(ids) > 0 && !slices.Contains(ids, a.ID) {
			continue
		}
		for _, entry := range catalog.All() {
			if entry.ID != a.ProviderID {
				continue
			}
			destination := entry.BaseURL
			if origin := p.vault.LoginOrigin(a.LoginCredentialRef); origin != "" {
				for _, match := range catalog.MatchCredentials(origin, "username_password") {
					if match.Provider == a.ProviderID {
						destination = origin
						break
					}
				}
			}
			u, err := url.Parse(destination)
			if err == nil && u.Scheme == "https" && u.Hostname() != "" && u.User == nil {
				out = append(out, loginSite{Name: a.DisplayName, Account: a.ID, Provider: a.ProviderID, Destination: u.Scheme + "://" + u.Host + "/", State: "queued"})
			}
			break
		}
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
		reply(w, map[string]any{"browsers": installedLoginBrowsers(), "sites": p.loginSites(nil), "batch": q.view})
		return true
	}
	if r.Method != "POST" {
		fail(w, 405, "use POST")
		return true
	}
	var input struct {
		Browser  string   `json:"browser"`
		Accounts []string `json:"accounts"`
		Ticket   string   `json:"ticket"`
		Confirm  bool     `json:"confirm"`
	}
	if decodeInput(w, r, &input, 8192) != nil {
		fail(w, 400, "invalid login batch request")
		return true
	}
	q.mu.Lock()
	defer q.mu.Unlock()
	if q.closed {
		fail(w, 503, "gateway is closing")
		return true
	}
	if r.URL.Path == root+"/cancel" {
		if q.cancel != nil {
			q.cancel()
		}
		reply(w, map[string]string{"state": "cancellation_requested"})
		return true
	}
	if q.view.State == "running" {
		fail(w, 409, "a login batch is already running")
		return true
	}
	if r.URL.Path == root+"/preview" {
		if !slices.Contains(installedLoginBrowsers(), input.Browser) {
			fail(w, 400, "choose an installed browser")
			return true
		}
		sites := p.loginSites(input.Accounts)
		if len(sites) == 0 || len(sites) > 64 || (len(input.Accounts) > 0 && len(sites) != len(input.Accounts)) {
			fail(w, 400, "select 1 to 64 configured website accounts")
			return true
		}
		q.ticket = rand.Text()
		q.expires = time.Now().Add(5 * time.Minute)
		q.version = p.operationVersion()
		q.view = loginBatchView{Browser: input.Browser, State: "preview", Sites: sites}
		reply(w, map[string]any{"ticket": q.ticket, "browser": input.Browser, "sites": sites, "expires_in_seconds": 300})
		return true
	}
	if !input.Confirm || input.Ticket == "" || input.Ticket != q.ticket || time.Now().After(q.expires) || q.version != p.operationVersion() {
		fail(w, 409, "confirmation expired or configuration changed; review again")
		return true
	}
	q.ticket = ""
	q.view.ID = rand.Text()
	q.view.State = "running"
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Minute)
	q.cancel = cancel
	view := q.view
	view.Sites = append([]loginSite(nil), q.view.Sites...)
	go p.runLoginBatch(ctx, view, r.Host, q.version)
	reply(w, q.view)
	return true
}

func (p *ControlPlane) loginBatchCall(ctx context.Context, host, path string, input any, expected *operationVersion) (int, map[string]any) {
	b, _ := json.Marshal(input)
	r, _ := http.NewRequestWithContext(ctx, "POST", path, bytes.NewReader(b))
	r.Host = host
	outcome := &operationOutcome{}
	r = r.WithContext(context.WithValue(context.WithValue(r.Context(), operationKey{}, *expected), operationOutcomeKey{}, outcome))
	w := &accountCheckCapture{headers: http.Header{}}
	p.ServeHTTP(w, r)
	var result map[string]any
	_ = json.Unmarshal(w.body.Bytes(), &result)
	if outcome.committed != nil {
		*expected = *outcome.committed
	} else if w.status == 200 && strings.HasSuffix(path, "/prepare-login") {
		if revision, ok := result["revision"].(float64); ok {
			expected.revision = uint64(revision)
		}
	}
	return w.status, result
}
func (p *ControlPlane) runLoginBatch(ctx context.Context, view loginBatchView, host string, expected operationVersion) {
	q := &p.loginBatch
	defer func() {
		q.mu.Lock()
		defer q.mu.Unlock()
		q.view.State = "completed"
		if recover() != nil {
			q.view.State = "failed"
		}
		if ctx.Err() != nil || expected != p.operationVersion() {
			q.view.State = "canceled"
		}
		for i := range q.view.Sites {
			if q.view.Sites[i].State == "queued" || q.view.Sites[i].State == "running" {
				q.view.Sites[i].State = "not_attempted"
			}
		}
		if q.cancel != nil {
			q.cancel()
			q.cancel = nil
		}
	}()
	for i, site := range view.Sites {
		if ctx.Err() != nil {
			return
		}
		q.mu.Lock()
		q.view.Sites[i].State = "running"
		q.mu.Unlock()
		state, detail := "needs_login", "Complete sign-in in the browser, then verify this account."
		code, _ := p.loginBatchCall(ctx, host, "/admin/accounts/"+site.Account+"/prepare-login", map[string]any{"browser": view.Browser, "revision": expected.revision}, &expected)
		if code != 200 {
			state, detail = "failed", "Could not prepare this account profile."
		} else {
			code, _ = p.loginBatchCall(ctx, host, "/admin/accounts/"+site.Account+"/login", map[string]any{}, &expected)
			owned := false
			for _, a := range p.service.Current().Config.Accounts {
				if a.ID == site.Account {
					for _, process := range p.browsers.list() {
						if process.Profile == a.BrowserProfileID && process.State == "running" {
							owned = true
						}
					}
				}
			}
			if !owned || (code != 200 && code != 409) {
				state, detail = "failed", "Could not open an owned browser; an occupied port is never attached automatically."
			} else {
				// Give a newly launched browser a bounded opportunity to initialize.
				select {
				case <-ctx.Done():
					return
				case <-time.After(2 * time.Second):
				}
				var result map[string]any
				code, result = p.loginBatchCall(ctx, host, "/admin/accounts/"+site.Account+"/verify-login", map[string]any{}, &expected)
				if code == 200 && result["verified"] == true {
					state, detail = "verified", "Authenticated browser session; generation is not verified."
				} else if result["check_status"] == "unsupported" {
					state, detail = "manual_check", "Profile persists browser state; this provider has no automatic authentication check."
				}
			}
		}
		q.mu.Lock()
		q.view.Sites[i].State = state
		q.view.Sites[i].Detail = detail
		q.mu.Unlock()
	}
}
