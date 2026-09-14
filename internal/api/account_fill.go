package api

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"

	"clash-of-tokens/catalog"
	"clash-of-tokens/internal/config"
	"github.com/gobwas/ws"
	"github.com/gobwas/ws/wsutil"
)

// Filling is user-triggered, limited to an exact provider origin and an owned
// isolated browser. It never submits a form, follows SSO, or returns secrets.
func (p *ControlPlane) accountFillAdmin(w http.ResponseWriter, r *http.Request) bool {
	parts := strings.Split(strings.Trim(r.URL.Path, "/"), "/")
	if len(parts) != 4 || parts[0] != "admin" || parts[1] != "accounts" || parts[3] != "fill-login" {
		return false
	}
	if r.Method != "POST" {
		fail(w, 405, "use POST")
		return true
	}
	c := p.service.Current().Config
	var a config.Account
	var profile config.BrowserProfile
	for _, item := range c.Accounts {
		if item.ID == parts[2] {
			a = item
			break
		}
	}
	for _, item := range c.BrowserProfiles {
		if item.ID == a.BrowserProfileID {
			profile = item
			break
		}
	}
	owned := false
	for _, process := range p.browsers.list() {
		if process.Profile == profile.ID && process.State == "running" {
			owned = true
			break
		}
	}
	if a.ID == "" || a.LoginCredentialRef == "" || !owned || profile.Engine == "firefox" {
		fail(w, 400, "open this account's owned Chrome login browser first")
		return true
	}
	origin := p.vault.LoginOrigin(a.LoginCredentialRef)
	if origin == "" {
		for _, entry := range catalog.All() {
			if entry.ID == a.ProviderID {
				origin = entry.BaseURL
				break
			}
		}
	}
	u, err := url.Parse(origin)
	if err != nil || u.Scheme != "https" || u.Hostname() == "" {
		fail(w, 400, "no trusted login origin")
		return true
	}
	origin = u.Scheme + "://" + u.Host
	allowed := false
	for _, match := range catalog.MatchCredentials(origin, "username_password") {
		if match.Provider == a.ProviderID {
			allowed = true
		}
	}
	if !allowed {
		fail(w, 400, "login origin does not match provider")
		return true
	}
	// Only resolve material after ownership and destination checks succeeded.
	value, err := p.vault.ResolveContext(r.Context(), a.LoginCredentialRef)
	var login struct {
		Username string `json:"username"`
		Password string `json:"password"`
	}
	if err != nil || json.Unmarshal([]byte(value), &login) != nil {
		fail(w, 400, "account login data unavailable")
		return true
	}
	filled := false
	err = commitOperation(r, func() error {
		var err error
		filled, err = fillOwnedLogin(r.Context(), profile.CDPURL, origin, login.Username, login.Password)
		return err
	})
	if err != nil {
		fail(w, 400, "login form could not be filled; keep the matching login page open and retry")
		return true
	}
	reply(w, map[string]any{"filled": filled, "submitted": false})
	return true
}

func loginFillScript(origin, username, password string) string {
	args, _ := json.Marshal([]string{origin, username, password})
	return `(()=>{const [origin,user,password]=` + string(args) + `;if(location.origin!==origin)return false;const visible=e=>!e.disabled&&!e.readOnly&&e.getClientRects().length&&getComputedStyle(e).visibility!=='hidden';const ps=[...document.querySelectorAll('input[type="password"]')].filter(visible);if(ps.length!==1)return false;const p=ps[0],f=p.form;if(!f||new URL(f.action||location.href).origin!==origin)return false;const us=[...f.querySelectorAll('input[type="email"],input[autocomplete="username"],input[name="username"],input[name="email"]')].filter(visible);if(us.length!==1)return false;const set=(e,v)=>{Object.getOwnPropertyDescriptor(HTMLInputElement.prototype,'value').set.call(e,v);e.dispatchEvent(new Event('input',{bubbles:true}));e.dispatchEvent(new Event('change',{bubbles:true}))};set(us[0],user);set(p,password);return true})()`
}

func fillOwnedLogin(parent context.Context, endpoint, origin, username, password string) (bool, error) {
	ctx, cancel := context.WithTimeout(parent, 8*time.Second)
	defer cancel()
	u, err := url.Parse(endpoint)
	if err != nil || u.Scheme != "http" || u.User != nil {
		return false, errors.New("invalid browser endpoint")
	}
	ip := net.ParseIP(u.Hostname())
	if u.Hostname() != "localhost" && (ip == nil || !ip.IsLoopback()) {
		return false, errors.New("nonlocal browser")
	}
	request, err := http.NewRequestWithContext(ctx, "GET", strings.TrimRight(endpoint, "/")+"/json/list", nil)
	if err != nil {
		return false, err
	}
	client := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	resp, err := client.Do(request)
	if err != nil {
		return false, err
	}
	defer resp.Body.Close()
	var targets []struct {
		Type      string `json:"type"`
		URL       string `json:"url"`
		WebSocket string `json:"webSocketDebuggerUrl"`
	}
	if resp.StatusCode != 200 || json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&targets) != nil {
		return false, errors.New("invalid targets")
	}
	address := ""
	for _, target := range targets {
		page, err := url.Parse(target.URL)
		if err == nil && target.Type == "page" && page.Scheme+"://"+page.Host == origin {
			if address != "" {
				return false, nil
			}
			address = target.WebSocket
		}
	}
	if address == "" {
		return false, nil
	}
	remote, err := url.Parse(address)
	if err != nil || remote.Scheme != "ws" || remote.Host != u.Host || remote.User != nil {
		return false, errors.New("unexpected debugger address")
	}
	conn, reader, _, err := ws.Dial(ctx, address)
	if err != nil {
		return false, err
	}
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(5 * time.Second))
	var input io.Reader = conn
	if reader != nil {
		input = reader
	}
	message, _ := json.Marshal(map[string]any{"id": 1, "method": "Runtime.evaluate", "params": map[string]any{"expression": loginFillScript(origin, username, password), "returnByValue": true}})
	defer clear(message)
	if err = wsutil.WriteClientMessage(conn, ws.OpText, message); err != nil {
		return false, err
	}
	for n := 0; n < 64; n++ {
		header, err := ws.ReadHeader(input)
		if err != nil {
			return false, err
		}
		if !header.Fin || header.Masked || header.Length > 1<<20 {
			return false, errors.New("invalid debugger frame")
		}
		data := make([]byte, int(header.Length))
		if _, err = io.ReadFull(input, data); err != nil {
			return false, err
		}
		var result struct {
			ID     int `json:"id"`
			Result struct {
				Result struct {
					Value bool `json:"value"`
				} `json:"result"`
			} `json:"result"`
		}
		_ = json.Unmarshal(data, &result)
		clear(data)
		if result.ID == 1 {
			return result.Result.Result.Value, nil
		}
	}
	return false, errors.New("debugger response limit")
}
