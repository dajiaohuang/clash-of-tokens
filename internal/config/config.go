package config

import (
	"clash-of-tokens/internal/providerdef"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

type Config struct {
	BrowserProfiles []BrowserProfile `json:"browser_profiles,omitempty"`
	Providers       []Provider       `json:"providers,omitempty"`
	Accounts        []Account        `json:"accounts,omitempty"`
	SchemaVersion   int              `json:"schema_version"`
	Listen          string           `json:"listen"`
	APIKeyEnv       string           `json:"api_key_env"`
	AdminKeyEnv     string           `json:"admin_key_env"`
	Runtime         Runtime          `json:"runtime"`
	Browser         Browser          `json:"browser"`
	Device          Device           `json:"device"`
	Sources         []Source         `json:"sources"`
	Groups          []Group          `json:"groups"`
}

// Device is an explicitly configured physical execution environment. It is not
// an inference server and does not store mobile app login credentials.
type Device struct {
	Enabled         bool   `json:"enabled"`
	ADBPath         string `json:"adb_path"`
	Serial          string `json:"serial"`
	StateDir        string `json:"state_dir"`
	OCRPath         string `json:"ocr_path"`
	OCRDataDir      string `json:"ocr_data_dir"`
	ClipboardSyncMS int    `json:"clipboard_sync_ms"`
}
type Browser struct {
	Enabled           bool   `json:"enabled"`
	CDPURL            string `json:"cdp_url"`
	StateFile         string `json:"state_file"`
	MaxSessions       int    `json:"max_sessions"`
	SessionTTLSeconds int    `json:"session_ttl_seconds"`
	PollMS            int    `json:"poll_ms"`
	MaxPromptBytes    int    `json:"max_prompt_bytes"`
	MaxResponseBytes  int    `json:"max_response_bytes"`
}
type Runtime struct {
	MaxInflight       int   `json:"max_inflight"`
	MaxQueued         int   `json:"max_queued"`
	MaxBodyBytes      int64 `json:"max_body_bytes"`
	MaxBufferedBytes  int64 `json:"max_buffered_bytes"`
	MaxOutputBytes    int64 `json:"max_output_bytes"`
	QueueTimeoutMS    int   `json:"queue_timeout_ms"`
	RequestTimeoutMS  int   `json:"request_timeout_ms"`
	WriteTimeoutMS    int   `json:"write_timeout_ms"`
	BodyReadTimeoutMS int   `json:"body_read_timeout_ms"`
}
type Source struct {
	Weight             int                 `json:"weight,omitempty"`
	CredentialResolver func(string) string `json:"-"`
	AccountID          string              `json:"account_id,omitempty"`
	CredentialRef      string              `json:"credential_ref,omitempty"`
	SourceKind         string              `json:"source_kind,omitempty"`
	ExecutionLocation  string              `json:"execution_location,omitempty"`
	InferenceLocation  string              `json:"inference_location,omitempty"`
	BillingMode        string              `json:"billing_mode,omitempty"`
	CredentialMode     string              `json:"credential_mode,omitempty"`
	ID                 string              `json:"id"`
	Provider           string              `json:"provider"`
	Adapter            string              `json:"adapter"`
	BaseURL            string              `json:"base_url"`
	KeyEnv             string              `json:"key_env,omitempty"`
	AccountIDEnv       string              `json:"account_id_env,omitempty"`
	Project            string              `json:"project,omitempty"`
	Enabled            bool                `json:"enabled"`
	AutoApproved       bool                `json:"auto_approved"`
	Local              bool                `json:"local"`
	Anonymous          bool                `json:"anonymous,omitempty"`
	Paid               bool                `json:"paid"`
	MaxInflight        int                 `json:"max_inflight"`
	QuotaDomain        string              `json:"quota_domain"`
	QuotaMaxInflight   int                 `json:"quota_max_inflight"`
	Models             []Model             `json:"models"`
}
type Model struct {
	InputUSDPerMillion  *float64 `json:"input_usd_per_million,omitempty"`
	OutputUSDPerMillion *float64 `json:"output_usd_per_million,omitempty"`
	Enabled             *bool    `json:"enabled,omitempty"`
	AutoApproved        *bool    `json:"auto_approved,omitempty"`
	DeclaredModel       string   `json:"declared_model,omitempty"`
	CanonicalModel      string   `json:"canonical_model,omitempty"`
	ID                  string   `json:"id"`
	Upstream            string   `json:"upstream"`
	Protocols           []string `json:"protocols"`
	Tier                string   `json:"tier"`
	RatingBasis         string   `json:"rating_basis"`
	Tools               string   `json:"tools"`
	Vision              bool     `json:"vision"`
	MaxInputBytes       int64    `json:"max_input_bytes"`
}
type Group struct {
	RequireVision      bool     `json:"require_vision"`
	MaxUSDPerMillion   *float64 `json:"max_usd_per_million,omitempty"`
	Preferences        []string `json:"preferences,omitempty"`
	MaxAttempts        int      `json:"max_attempts,omitempty"`
	AllowMetered       *bool    `json:"allow_metered,omitempty"`
	AllowSubscription  *bool    `json:"allow_subscription,omitempty"`
	AllowUnknownCost   *bool    `json:"allow_unknown_cost,omitempty"`
	AllowedSourceKinds []string `json:"allowed_source_kinds,omitempty"`
	ID                 string   `json:"id"`
	Type               string   `json:"type"`
	Sources            []string `json:"sources"`
	MinTier            string   `json:"min_tier"`
	AllowUnrated       bool     `json:"allow_unrated"`
	AllowPaid          bool     `json:"allow_paid"`
	LocalOnly          bool     `json:"local_only"`
	RequireTools       bool     `json:"require_tools"`
}

func Default() Config {
	return Config{SchemaVersion: 1, Listen: "127.0.0.1:8317", APIKeyEnv: "COT_API_KEY", AdminKeyEnv: "COT_ADMIN_KEY", Runtime: Runtime{128, 128, 16 << 20, 128 << 20, 64 << 20, 5000, 300000, 15000, 15000}, Browser: Browser{CDPURL: "http://127.0.0.1:9222", StateFile: ".clash-tokens/sessions.json", MaxSessions: 256, SessionTTLSeconds: 86400, PollMS: 1000, MaxPromptBytes: 64 << 10, MaxResponseBytes: 1 << 20}, Sources: []Source{}, Groups: []Group{{ID: "auto", Type: "auto", Sources: []string{}, MinTier: "silver"}}}
}
func Load(path string) (Config, error) {
	if versions, err := readJournal(path + ".state"); err == nil {
		return versions[len(versions)-1].Config, nil
	} else if !errors.Is(err, os.ErrNotExist) {
		return Config{}, err
	}
	c := Default()
	f, e := os.Open(path)
	if e != nil {
		return c, e
	}
	defer f.Close()
	d := json.NewDecoder(io.LimitReader(f, 4<<20))
	d.DisallowUnknownFields()
	if e = d.Decode(&c); e != nil {
		return c, e
	}
	if d.Decode(new(any)) != io.EOF {
		return c, errors.New("configuration must contain one JSON object")
	}
	return c, c.Validate()
}

var identifier = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9_.-]{0,95}$`)

func Tier(s string) int {
	switch s {
	case "bronze":
		return 0
	case "silver":
		return 1
	case "gold":
		return 2
	case "platinum":
		return 3
	case "diamond":
		return 4
	}
	return -1
}
func (c Config) Validate() error {
	if err := c.ValidateAccounts(); err != nil {
		return err
	}
	if c.SchemaVersion != 1 {
		return errors.New("unsupported schema_version")
	}
	host, _, e := net.SplitHostPort(c.Listen)
	if e != nil {
		return errors.New("listen must be host:port")
	}
	if host == "" {
		return errors.New("listen must specify an explicit host")
	}
	if c.APIKeyEnv == "" || c.AdminKeyEnv == "" || c.APIKeyEnv == c.AdminKeyEnv {
		return errors.New("distinct api_key_env and admin_key_env required")
	}
	r := c.Runtime
	if r.MaxInflight < 1 || r.MaxInflight > 10000 || r.MaxQueued < 0 || r.MaxQueued > 10000 || r.MaxBodyBytes < 1 || r.MaxBodyBytes > 64<<20 || r.MaxBufferedBytes < r.MaxBodyBytes || r.MaxBufferedBytes > 2<<30 || r.MaxOutputBytes < 1 || r.QueueTimeoutMS < 1 || r.RequestTimeoutMS < 1 || r.WriteTimeoutMS < 1 || r.BodyReadTimeoutMS < 1 {
		return errors.New("invalid runtime resource limits")
	}
	ids := map[string]bool{}
	domains := map[string]int{}
	browserCount := 0
	chatgptProfiles := map[string]bool{}
	doubaoBrowserCount := 0
	geminiBrowserCount := 0
	gigaChatBrowserCount := 0
	buildBrowserCount := 0
	for _, s := range c.Sources {
		if s.Weight < 0 || s.Weight > 10000 {
			return fmt.Errorf("source %s: invalid weight", s.ID)
		}
		if err := s.ValidateMetadata(); err != nil {
			return fmt.Errorf("source %s: %w", s.ID, err)
		}
		if !identifier.MatchString(s.ID) || ids[s.ID] {
			return fmt.Errorf("invalid or duplicate source id: %s", s.ID)
		}
		ids[s.ID] = true
		descriptor, known := providerdef.Lookup(s.Adapter)
		if !known {
			return fmt.Errorf("source %s: unsupported adapter", s.ID)
		}
		if s.Adapter == "chatgpt-web" {

			browserCount++
			endpoint := c.SourceBrowser(s).CDPURL
			if chatgptProfiles[endpoint] {
				return errors.New("one ChatGPT web source per browser profile is supported")
			}
			chatgptProfiles[endpoint] = true
			if s.Enabled && !c.SourceBrowser(s).Enabled {
				return errors.New("chatgpt-web requires browser.enabled")
			}
			if s.MaxInflight != 1 || s.QuotaMaxInflight != 1 {
				return errors.New("ChatGPT web account and browser concurrency must be one")
			}
		}
		if s.Adapter == "doubao" {
			doubaoBrowserCount++
			if s.Enabled && !c.SourceBrowser(s).Enabled {
				return errors.New("doubao requires browser.enabled")
			}
		}
		if s.Adapter == "gemini-web" {
			geminiBrowserCount++
			if s.Enabled && !c.SourceBrowser(s).Enabled {
				return errors.New("gemini-web requires browser.enabled")
			}
			if s.MaxInflight != 1 || s.QuotaMaxInflight != 1 {
				return errors.New("Gemini Web account and browser concurrency must be one")
			}
		}
		if s.Adapter == "gigachat-web" {
			gigaChatBrowserCount++
			if s.Enabled && !c.SourceBrowser(s).Enabled {
				return errors.New("gigachat-web requires browser.enabled")
			}
			if s.MaxInflight != 1 || s.QuotaMaxInflight != 1 {
				return errors.New("GigaChat Web account and browser concurrency must be one")
			}
		}
		if s.Adapter == "cloudflare-playground" {
			if s.Enabled && !c.SourceBrowser(s).Enabled {
				return errors.New("cloudflare-playground requires browser.enabled")
			}
			if strings.TrimRight(s.BaseURL, "/") != "https://playground.ai.cloudflare.com" {
				return errors.New("cloudflare-playground uses its public browser origin")
			}
			if s.MaxInflight != 1 {
				return errors.New("cloudflare-playground browser concurrency must be one")
			}
		}
		if s.Adapter == "aistudio-build" {
			buildBrowserCount++
			if s.Enabled && !c.SourceBrowser(s).Enabled {
				return errors.New("aistudio-build requires browser.enabled")
			}
			if s.MaxInflight != 1 || s.QuotaMaxInflight != 1 {
				return errors.New("AI Studio Build account and browser concurrency must be one")
			}
		}
		if (s.Adapter == "gemini-cli" || s.Adapter == "antigravity") && s.Project == "" {
			return fmt.Errorf("source %s: Code Assist project required", s.ID)
		}
		if s.Adapter == "weread-ai" && strings.TrimSpace(s.Project) == "" {
			return fmt.Errorf("source %s: WeRead book ID required in project", s.ID)
		}
		if s.Adapter == "app-device" {
			models := map[string]string{"meituan-xiaotuan": "meituan_xiaotuan", "wangzhe-lingbao": "wangzhe_lingbao", "douyin-xiaohuoren": "douyin_xiaohuoren"}
			if models[s.Provider] == "" || s.BaseURL != "adb://current-app-session" || s.Local || s.AutoApproved || !s.Anonymous || s.KeyEnv != "" || s.MaxInflight != 1 || s.QuotaMaxInflight != 1 || s.QuotaDomain != "android-device" {
				return fmt.Errorf("source %s: device providers require manual routing, android-device capacity one, and adb://current-app-session", s.ID)
			}
			if s.Enabled {
				d := c.Device
				if !d.Enabled || !filepath.IsAbs(d.ADBPath) || !filepath.IsAbs(d.OCRPath) || !filepath.IsAbs(d.StateDir) || d.Serial == "" || strings.ContainsAny(d.Serial, "\r\n\x00") || d.ClipboardSyncMS < 200 || d.ClipboardSyncMS > 5000 || s.Project != "current-app-session" {
					return fmt.Errorf("source %s: enabled device requires explicit paths, serial, sync delay and project current-app-session", s.ID)
				}
			}
			for _, m := range s.Models {
				if m.Upstream != models[s.Provider] {
					return fmt.Errorf("source %s: unknown device model selector", s.ID)
				}
			}
		}
		isDevinCLI := s.Adapter == "devin-cli"
		u, e := url.Parse(s.BaseURL)
		if s.Adapter == "app-device" {
			// Validated above; this is an ADB device origin, not an HTTP service.
		} else if isDevinCLI {
			if s.BaseURL != "devin://acp/stdio" || !s.Local {
				return fmt.Errorf("source %s: Devin CLI requires local base_url devin://acp/stdio", s.ID)
			}
		} else if s.Adapter == "zcode" {
			if s.BaseURL != "zcode://app-server/stdio" || !s.Local {
				return fmt.Errorf("source %s: ZCode requires local base_url zcode://app-server/stdio", s.ID)
			}
		} else {
			if e != nil || u.Host == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" {
				return fmt.Errorf("source %s: invalid base_url", s.ID)
			}
			ip := net.ParseIP(u.Hostname())
			loop := u.Hostname() == "localhost" || (ip != nil && ip.IsLoopback())
			if u.Scheme != "https" && !(u.Scheme == "http" && loop) {
				return fmt.Errorf("source %s: HTTPS required outside loopback", s.ID)
			}
			if s.Local && !loop {
				return fmt.Errorf("source %s: local requires loopback endpoint", s.ID)
			}
		}
		if !s.Local && !s.Anonymous && s.KeyEnv == "" && c.SourceCredentialRef(s) == "" && s.Adapter != "chatgpt-web" && s.Adapter != "doubao" && s.Adapter != "gemini-web" && s.Adapter != "gigachat-web" && s.Adapter != "duckduckgo-web" && s.Adapter != "aistudio-build" {
			return fmt.Errorf("source %s: key_env required", s.ID)
		}
		if s.MaxInflight < 1 || s.MaxInflight > 10000 || s.QuotaDomain == "" || s.QuotaMaxInflight < 1 || s.QuotaMaxInflight > 10000 {
			return fmt.Errorf("source %s: explicit source and quota capacity required", s.ID)
		}
		if n, ok := domains[s.QuotaDomain]; ok && n != s.QuotaMaxInflight {
			return fmt.Errorf("inconsistent quota limit for %s", s.QuotaDomain)
		}
		domains[s.QuotaDomain] = s.QuotaMaxInflight
		models := map[string]bool{}
		for _, m := range s.Models {
			if m.ID == "" || len(m.ID) > 256 || strings.HasPrefix(m.ID, "auto") || models[m.ID] || m.Upstream == "" || len(m.Upstream) > 1024 || strings.ContainsAny(m.Upstream, "?#\\") {
				return fmt.Errorf("source %s: invalid model", s.ID)
			}
			models[m.ID] = true
			if !ValidPrice(m.InputUSDPerMillion) || !ValidPrice(m.OutputUSDPerMillion) {
				return fmt.Errorf("source %s: invalid model USD rate", s.ID)
			}
			if m.Tier != "unrated" && (Tier(m.Tier) < 0 || m.RatingBasis == "") {
				return fmt.Errorf("source %s: rated models require rating_basis", s.ID)
			}
			if m.MaxInputBytes < 1 || len(m.Protocols) == 0 {
				return fmt.Errorf("source %s: model limits and protocols required", s.ID)
			}
			if m.Tools != "native" && m.Tools != "none" && m.Tools != "unknown" {
				return fmt.Errorf("source %s: invalid tools capability", s.ID)
			}
			if descriptor.TextOnly && (m.Tools != "none" || m.Vision) {
				return fmt.Errorf("source %s: text adapter requires tools none and vision false", s.ID)
			}
			if !descriptor.ToolsAllowed && m.Tools != "none" {
				return fmt.Errorf("source %s: adapter requires tools none", s.ID)
			}
			for _, p := range m.Protocols {
				if !Supports(s.Adapter, p) {
					return fmt.Errorf("source %s: adapter does not support %s", s.ID, p)
				}
			}
		}
	}
	if browserCount > 0 || doubaoBrowserCount > 0 || geminiBrowserCount > 0 || gigaChatBrowserCount > 0 || buildBrowserCount > 0 {
		b := c.Browser
		u, e := url.Parse(b.CDPURL)
		if e != nil || u.Scheme != "http" || net.ParseIP(u.Hostname()) == nil || !net.ParseIP(u.Hostname()).IsLoopback() || u.User != nil || u.RawQuery != "" || u.Fragment != "" {
			return errors.New("browser cdp_url must be an explicit loopback HTTP endpoint")
		}
		if b.StateFile == "" || b.MaxSessions < 1 || b.MaxSessions > 4096 || b.SessionTTLSeconds < 60 || b.PollMS < 500 || b.PollMS > 10000 || b.MaxPromptBytes < 1 || b.MaxPromptBytes > 1<<20 || b.MaxResponseBytes < 1 || b.MaxResponseBytes > 4<<20 {
			return errors.New("invalid browser resource limits")
		}
	}
	gids := map[string]bool{}
	for _, g := range c.Groups {
		if !ValidPrice(g.MaxUSDPerMillion) {
			return fmt.Errorf("group %s: invalid maximum USD rate", g.ID)
		}
		seenPreferences := map[string]bool{}
		for _, preference := range g.Preferences {
			switch preference {
			case "lower_latency", "existing_subscription", "lower_cost", "official_api", "reverse_source":
			default:
				return fmt.Errorf("group %s: invalid preference", g.ID)
			}
			if seenPreferences[preference] || (g.Type != "auto" && g.Type != "latency" && g.Type != "load-balance") {
				return fmt.Errorf("group %s: preferences must be unique and require auto, latency or load-balance", g.ID)
			}
			seenPreferences[preference] = true
		}
		if g.MaxAttempts < 0 || g.MaxAttempts > 8 {
			return fmt.Errorf("group %s: max_attempts must be between 0 and 8", g.ID)
		}
		if !identifier.MatchString(g.ID) || gids[g.ID] {
			return errors.New("invalid or duplicate group id")
		}
		gids[g.ID] = true
		switch g.Type {
		case "auto", "select", "fallback", "latency", "load-balance", "weighted":
		default:
			return fmt.Errorf("group %s: invalid type", g.ID)
		}
		if Tier(g.MinTier) < 0 {
			return fmt.Errorf("group %s: invalid min_tier", g.ID)
		}
		for _, id := range g.Sources {
			if !ids[id] {
				return fmt.Errorf("group %s: unknown source %s", g.ID, id)
			}
		}
	}
	return nil
}
func Supports(adapter, p string) bool { return providerdef.Supports(adapter, p) }
