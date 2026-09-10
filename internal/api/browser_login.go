package api

import (
	"fmt"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"clash-of-tokens/catalog"
	"clash-of-tokens/internal/browserauth"
	"clash-of-tokens/internal/chatgptweb"
	"clash-of-tokens/internal/config"
	audit "clash-of-tokens/internal/evidence"
)

func browserExecutable(engine string) (string, error) {
	paths := map[string][]string{"chrome": {"google-chrome", "google-chrome-stable"}, "edge": {"microsoft-edge"}, "chromium": {"chromium", "chromium-browser"}}[engine]
	if runtime.GOOS == "windows" {
		rel := map[string]string{"chrome": `Google\Chrome\Application\chrome.exe`, "edge": `Microsoft\Edge\Application\msedge.exe`, "chromium": `Chromium\Application\chrome.exe`}[engine]
		paths = nil
		for _, root := range []string{os.Getenv("ProgramFiles"), os.Getenv("ProgramFiles(x86)"), os.Getenv("LOCALAPPDATA")} {
			if root != "" {
				paths = append(paths, filepath.Join(root, rel))
			}
		}
	} else if runtime.GOOS == "darwin" {
		paths = map[string][]string{"chrome": {"/Applications/Google Chrome.app/Contents/MacOS/Google Chrome"}, "edge": {"/Applications/Microsoft Edge.app/Contents/MacOS/Microsoft Edge"}, "chromium": {"/Applications/Chromium.app/Contents/MacOS/Chromium"}}[engine]
	}
	for _, path := range paths {
		if resolved, err := exec.LookPath(path); err == nil {
			return resolved, nil
		}
	}
	return "", fmt.Errorf("configured browser engine is not installed")
}

func loginArguments(p config.BrowserProfile, stateFile, destination string) ([]string, error) {
	u, err := url.Parse(destination)
	if err != nil || u.Scheme != "https" || u.Hostname() == "" || u.User != nil {
		return nil, fmt.Errorf("provider has no supported HTTPS login destination")
	}
	endpoint, _ := url.Parse(p.CDPURL)
	profile, err := filepath.Abs(filepath.Join(filepath.Dir(stateFile), "profiles", p.ID, "browser-data"))
	if err != nil {
		return nil, err
	}
	return []string{"--remote-debugging-address=127.0.0.1", "--remote-debugging-port=" + endpoint.Port(), "--user-data-dir=" + profile, "--no-first-run", "--no-default-browser-check", u.Scheme + "://" + u.Host + "/"}, nil
}

func (p *ControlPlane) browserLoginAdmin(w http.ResponseWriter, r *http.Request) bool {
	parts := strings.Split(strings.TrimPrefix(r.URL.Path, "/admin/accounts/"), "/")
	if !strings.HasPrefix(r.URL.Path, "/admin/accounts/") || len(parts) != 2 || (parts[1] != "login" && parts[1] != "check-login") {
		return false
	}
	if r.Method != "POST" {
		fail(w, 405, "method not allowed")
		return true
	}
	c := p.service.Current().Config
	var account config.Account
	for _, a := range c.Accounts {
		if a.ID == parts[0] {
			account = a
		}
	}
	if account.ID == "" {
		fail(w, 404, "unknown account")
		return true
	}
	var profile config.BrowserProfile
	for _, v := range c.BrowserProfiles {
		if v.ID == account.BrowserProfileID {
			profile = v
		}
	}
	if profile.ID == "" || !profile.Enabled {
		fail(w, 400, "bind an enabled browser profile first")
		return true
	}
	if parts[1] == "check-login" {
		adapter, origin := "", ""
		for _, entry := range catalog.All() {
			if entry.ID == account.ProviderID {
				adapter = entry.Adapter
				origin = entry.BaseURL
			}
		}
		var result browserauth.Evidence
		if adapter == "chatgpt-web" {
			driver := chatgptweb.New(c.SourceBrowser(config.Source{AccountID: account.ID}), "login-check")
			v := driver.CheckAuth(r.Context())
			result = browserauth.Evidence{Status: v.Status, Method: v.Method, CheckedAt: v.CheckedAt, ComposerReady: v.ComposerReady}
		} else {
			result = browserauth.Check(r.Context(), profile.CDPURL, origin, adapter)
		}
		revision := p.service.Current().Revision
		recorded := p.evidence.Append(audit.Entry{Revision: revision, Kind: "authentication", Resource: account.ID, CheckedAt: result.CheckedAt, Method: result.Method, Status: result.Status, UpstreamStatus: result.UpstreamStatus}) == nil
		reply(w, struct {
			browserauth.Evidence
			Account         string `json:"account"`
			Profile         string `json:"profile"`
			Revision        uint64 `json:"revision"`
			HistoryRecorded bool   `json:"history_recorded"`
		}{result, account.ID, profile.ID, revision, recorded})
		return true
	}
	destination := ""
	for _, entry := range catalog.All() {
		if entry.ID == account.ProviderID {
			destination = entry.BaseURL
		}
	}
	args, err := loginArguments(profile, p.startup.Browser.StateFile, destination)
	if err != nil {
		fail(w, 400, err.Error())
		return true
	}
	endpoint, _ := url.Parse(profile.CDPURL)
	// Never silently attach to an unknown process on the configured port.
	probe, err := net.DialTimeout("tcp", endpoint.Host, time.Second)
	if err == nil {
		probe.Close()
		fail(w, 409, "profile port is already in use; complete login in its existing browser window")
		return true
	}
	executable, err := browserExecutable(profile.Engine)
	if err != nil {
		fail(w, 503, err.Error())
		return true
	}
	cmd := exec.Command(executable, args...)
	process, err := p.browsers.start(profile.ID, cmd)
	if err != nil {
		fail(w, 503, err.Error())
		return true
	}
	reply(w, map[string]any{"profile_id": profile.ID, "pid": process.PID, "launch_id": process.ID, "status": "login_required", "message": "Complete login in the browser. Launching does not verify authentication. View owned processes from Browsers."})
	return true
}

// setupBrowserLogin operates on a selected draft profile without creating an
// account. Its browser directory is still scoped to the server's profile root.
func (p *ControlPlane) setupBrowserLogin(w http.ResponseWriter, r *http.Request) bool {
	if r.URL.Path != "/admin/browser_profiles/setup-login" {
		return false
	}
	if r.Method != "POST" {
		fail(w, 405, "method not allowed")
		return true
	}
	var input struct {
		Profile  config.BrowserProfile `json:"profile"`
		Provider string                `json:"provider"`
		Action   string                `json:"action"`
	}
	if decodeInput(w, r, &input, 4096) != nil || (input.Action != "launch" && input.Action != "check") {
		fail(w, 400, "invalid browser setup request")
		return true
	}
	c := p.service.Current().Config
	validation := config.Config{BrowserProfiles: []config.BrowserProfile{input.Profile}}
	for _, profile := range c.BrowserProfiles {
		if profile != input.Profile {
			validation.BrowserProfiles = append(validation.BrowserProfiles, profile)
		}
	}
	if !input.Profile.Enabled || validation.ValidateBrowserProfiles() != nil {
		fail(w, 400, "choose an enabled valid browser profile")
		return true
	}
	for _, profile := range c.BrowserProfiles {
		if (profile.ID == input.Profile.ID || profile.CDPURL == input.Profile.CDPURL) && profile != input.Profile {
			fail(w, 409, "draft profile conflicts with an existing profile")
			return true
		}
	}
	adapter, destination := "", ""
	for _, entry := range catalog.All() {
		if entry.ID == input.Provider {
			adapter, destination = entry.Adapter, entry.BaseURL
		}
	}
	args, err := loginArguments(input.Profile, p.startup.Browser.StateFile, destination)
	if err != nil {
		fail(w, 400, err.Error())
		return true
	}
	if input.Action == "check" {
		var result browserauth.Evidence
		if adapter == "chatgpt-web" {
			browser := p.startup.Browser
			browser.Enabled = true
			browser.CDPURL = input.Profile.CDPURL
			value := chatgptweb.New(browser, "setup-check").CheckAuth(r.Context())
			result = browserauth.Evidence{Status: value.Status, Method: value.Method, CheckedAt: value.CheckedAt, ComposerReady: value.ComposerReady}
		} else {
			result = browserauth.Check(r.Context(), input.Profile.CDPURL, destination, adapter)
		}
		reply(w, struct {
			browserauth.Evidence
			Profile         string `json:"profile"`
			HistoryRecorded bool   `json:"history_recorded"`
		}{result, input.Profile.ID, false})
		return true
	}
	endpoint, _ := url.Parse(input.Profile.CDPURL)
	probe, err := net.DialTimeout("tcp", endpoint.Host, time.Second)
	if err == nil {
		probe.Close()
		fail(w, 409, "profile port is already in use; explicitly select the running browser to check login")
		return true
	}
	executable, err := browserExecutable(input.Profile.Engine)
	if err != nil {
		fail(w, 503, err.Error())
		return true
	}
	cmd := exec.Command(executable, args...)
	process, err := p.browsers.start(input.Profile.ID, cmd)
	if err != nil {
		fail(w, 503, err.Error())
		return true
	}
	reply(w, map[string]any{"profile": input.Profile.ID, "pid": process.PID, "launch_id": process.ID, "status": "login_required"})
	return true
}
