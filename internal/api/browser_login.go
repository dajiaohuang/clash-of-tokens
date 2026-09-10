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
	"clash-of-tokens/internal/config"
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
	if !strings.HasPrefix(r.URL.Path, "/admin/accounts/") || len(parts) != 2 || parts[1] != "login" {
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
	destination := ""
	for _, entry := range catalog.All() {
		if entry.ID == account.ProviderID {
			destination = entry.BaseURL
		}
	}
	args, err := loginArguments(profile, c.Browser.StateFile, destination)
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
	if err = cmd.Start(); err != nil {
		fail(w, 503, "cannot start login browser")
		return true
	}
	pid := cmd.Process.Pid
	go cmd.Wait()
	reply(w, map[string]any{"profile_id": profile.ID, "pid": pid, "status": "login_required", "message": "Complete login in the browser. Launching does not verify authentication. The browser remains under your control."})
	return true
}
