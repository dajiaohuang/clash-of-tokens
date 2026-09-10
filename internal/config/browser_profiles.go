package config

import (
	"fmt"
	"net"
	"net/url"
	"path/filepath"
	"strconv"
)

type BrowserProfile struct {
	ID      string `json:"id"`
	Enabled bool   `json:"enabled"`
	Engine  string `json:"engine"`
	CDPURL  string `json:"cdp_url"`
}

// BrowserEngines is the set of browser families supported by profile
// discovery and the dedicated login launcher. Selecting an engine does not
// claim that it is installed or that its provider session is authenticated.
var BrowserEngines = []string{"chrome", "edge", "brave", "firefox", "opera", "vivaldi", "chromium", "arc"}

func (c Config) ValidateBrowserProfiles() error {
	if len(c.BrowserProfiles) > 64 {
		return fmt.Errorf("browser registry exceeds 64 profiles")
	}
	ids := map[string]bool{}
	endpoints := map[string]bool{}
	for _, p := range c.BrowserProfiles {
		if !identifier.MatchString(p.ID) || ids[p.ID] {
			return fmt.Errorf("invalid or duplicate browser profile id")
		}
		ids[p.ID] = true
		if !containsBrowserEngine(p.Engine) {
			return fmt.Errorf("profile %s: unsupported browser engine", p.ID)
		}
		u, err := url.Parse(p.CDPURL)
		if err != nil {
			return fmt.Errorf("profile %s: invalid CDP URL", p.ID)
		}
		ip := net.ParseIP(u.Hostname())
		port, _ := strconv.Atoi(u.Port())
		if u.Scheme != "http" || ip == nil || !ip.IsLoopback() || port < 1024 || port > 65535 || u.User != nil || u.RawQuery != "" || u.Fragment != "" || (u.Path != "" && u.Path != "/") {
			return fmt.Errorf("profile %s: CDP must be explicit loopback HTTP with port 1024..65535", p.ID)
		}
		// Ports must differ even when equivalent loopback aliases are used.
		if endpoints[u.Port()] {
			return fmt.Errorf("browser profiles must use distinct CDP ports")
		}
		endpoints[u.Port()] = true
	}
	for _, a := range c.Accounts {
		if a.BrowserProfileID != "" && !ids[a.BrowserProfileID] {
			return fmt.Errorf("account %s: unknown browser profile", a.ID)
		}
	}
	return nil
}

func containsBrowserEngine(engine string) bool {
	for _, supported := range BrowserEngines {
		if engine == supported {
			return true
		}
	}
	return false
}

func (c Config) SourceBrowser(s Source) Browser {
	for _, a := range c.Accounts {
		if a.ID != s.AccountID || a.BrowserProfileID == "" {
			continue
		}
		for _, p := range c.BrowserProfiles {
			if p.ID == a.BrowserProfileID {
				b := c.Browser
				b.Enabled = p.Enabled
				b.CDPURL = p.CDPURL
				b.StateFile = filepath.Join(filepath.Dir(c.Browser.StateFile), "profiles", p.ID, "sessions.json")
				return b
			}
		}
	}
	return c.Browser
}
