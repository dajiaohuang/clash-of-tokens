package api

import (
	"net/http"
	"net/url"
	"os"
	"strings"

	"clash-of-tokens/catalog"
	"clash-of-tokens/internal/browsermeta"
	"clash-of-tokens/internal/config"
)

// accountCandidate is deliberately metadata-only. It is safe to render in the
// control plane: no credential value, cookie, token, or browser database is
// ever included.
type accountCandidate struct {
	ID         string   `json:"id"`
	Kind       string   `json:"kind"`
	Label      string   `json:"label"`
	Origin     string   `json:"origin"`
	Providers  []string `json:"providers,omitempty"`
	Confidence string   `json:"confidence"`
	Action     string   `json:"action"`
	Available  bool     `json:"available"`
}

type discoveryChannel struct {
	ID       string `json:"id"`
	Status   string `json:"status"`
	Action   string `json:"action"`
	ReadOnly bool   `json:"read_only"`
}

func candidateProviders(origin string, kind string, entries []catalog.Entry) []string {
	needle := candidateHost(origin)
	out := []string{}
	for _, p := range entries {
		accepted := false
		for _, mode := range p.Credentials.Accepted {
			if mode == kind || (kind == "browser_profile" && mode == "browser_session") {
				accepted = true
			}
		}
		if !accepted {
			continue
		}
		for _, domain := range p.Credentials.Domains {
			if needle != "" && needle == strings.TrimSuffix(strings.ToLower(domain), ".") {
				out = append(out, p.ID)
				break
			}
		}
	}
	return out
}

func candidateHost(origin string) string {
	value := strings.TrimSpace(strings.ToLower(origin))
	if strings.Contains(value, "://") {
		u, err := url.Parse(value)
		if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Hostname() == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" {
			return ""
		}
		value = u.Hostname()
	}
	if strings.ContainsAny(value, "/?#\r\n\x00") {
		return ""
	}
	return strings.TrimSuffix(value, ".")
}

func (p *ControlPlane) accountDiscoveryAdmin(w http.ResponseWriter, r *http.Request, s *Server) bool {
	if r.URL.Path != "/admin/discovery/accounts" {
		return false
	}
	if r.Method != "POST" {
		fail(w, 405, "use POST /admin/discovery/accounts")
		return true
	}
	var input struct {
		ScanBrowsers bool `json:"scan_browsers"`
	}
	if decodeInput(w, r, &input, 4096) != nil {
		fail(w, 400, "invalid account discovery request")
		return true
	}
	entries := catalog.All()
	items := []accountCandidate{}
	channels := []discoveryChannel{
		{ID: "browsers", Status: "scanned_on_request", Action: "scan_browsers", ReadOnly: true},
		{ID: "environment", Status: "configured_sources_only", Action: "import_credential", ReadOnly: true},
		{ID: "password_managers", Status: "manual_export_required", Action: "import_export", ReadOnly: true},
		{ID: "cli_sessions", Status: "manual_export_required", Action: "import_token", ReadOnly: true},
		{ID: "existing_profiles", Status: "configured_profiles", Action: "create_browser_profile", ReadOnly: true},
	}
	for _, profile := range s.cfg.BrowserProfiles {
		if profile.Enabled {
			items = append(items, accountCandidate{ID: "profile:" + profile.ID, Kind: "browser_profile", Label: profile.ID, Origin: profile.CDPURL, Providers: nil, Confidence: "configured", Action: "use_existing_profile", Available: true})
		}
	}
	if input.ScanBrowsers {
		for _, b := range browsermeta.Discover(browsermeta.StandardRoots()) {
			providers := candidateProviders(b.Browser, "browser_profile", entries)
			items = append(items, accountCandidate{ID: "browser:" + b.ID, Kind: "browser_profile", Label: b.Browser + " / " + b.Name, Origin: b.Root + " / " + b.Profile, Providers: providers, Confidence: "metadata", Action: "create_browser_profile", Available: true})
		}
	}
	for _, m := range s.vault.List() {
		origin := m.Domain
		if origin == "" {
			origin = m.Source
		}
		providers := candidateProviders(origin, m.Kind, entries)
		items = append(items, accountCandidate{ID: m.ID, Kind: m.Kind, Label: m.ID, Origin: origin, Providers: providers, Confidence: "stored", Action: "bind_credential", Available: true})
	}
	for _, source := range s.cfg.Sources {
		if source.KeyEnv == "" || source.KeyEnv == s.cfg.APIKeyEnv || source.KeyEnv == s.cfg.AdminKeyEnv {
			continue
		}
		available := configSourceEnvAvailable(source)
		items = append(items, accountCandidate{ID: "env:" + source.ID, Kind: source.CredentialMode, Label: source.ID + " environment credential", Origin: "environment:" + source.KeyEnv, Providers: []string{source.Provider}, Confidence: "configured", Action: "import_credential", Available: available})
	}
	// Keep discovery bounded and deterministic even when a user has many
	// imported references or browser profiles.
	if len(items) > 512 {
		items = items[:512]
	}
	reply(w, map[string]any{"items": items, "channels": channels, "browser_scan": input.ScanBrowsers, "secret_values": false})
	return true
}

func configSourceEnvAvailable(source config.Source) bool {
	// The value is intentionally only used as a boolean availability signal.
	if source.KeyEnv == "" || strings.TrimSpace(source.KeyEnv) == "" {
		return false
	}
	_, ok := os.LookupEnv(source.KeyEnv)
	return ok
}
