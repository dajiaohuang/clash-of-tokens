// Package catalog holds inert provider metadata. Listing it never creates a
// client, logs into an account, starts a browser or sends a probe.
package catalog

import (
	"clash-of-tokens/internal/config"
	"clash-of-tokens/internal/providerdef"
	_ "embed"
	"encoding/json"
	"fmt"
	"net/url"
	"strings"
)

//go:embed providers.json
var Data []byte

type Entry struct {
	Credentials    CredentialPolicy `json:"credentials"`
	ID             string           `json:"id"`
	Adapter        string           `json:"adapter"`
	BaseURL        string           `json:"base_url"`
	Protocols      []string         `json:"protocols"`
	Kind           string           `json:"kind"`
	Implementation string           `json:"implementation"`
	Reference      string           `json:"reference"`
	Notes          string           `json:"notes"`
	LiveVerified   bool             `json:"live_verified"`
	Anonymous      bool             `json:"anonymous,omitempty"`
	TextOnly       bool             `json:"text_only,omitempty"`
}

func All() []Entry {
	var out []Entry
	if e := json.Unmarshal(Data, &out); e != nil {
		panic(e)
	}
	for i := range out {
		d, _ := providerdef.Lookup(out[i].Adapter)
		out[i].Credentials.Accepted = d.CredentialModes
		if u, err := url.Parse(out[i].BaseURL); err == nil && u.Hostname() != "" && u.Scheme == "https" {
			out[i].Credentials.Domains = []string{strings.ToLower(u.Hostname())}
		}
	}
	return out
}
func Preset(id, model string, baseOverride ...string) (config.Source, error) {
	for _, p := range All() {
		if p.ID != id {
			continue
		}
		if p.Implementation == "not_implemented" {
			return config.Source{}, fmt.Errorf("%s is research-only: %s", id, p.Notes)
		}
		if model == "" {
			return config.Source{}, fmt.Errorf("-model must be the account's real upstream model id")
		}
		if len(baseOverride) > 0 && strings.TrimSpace(baseOverride[0]) != "" {
			p.BaseURL = strings.TrimSpace(baseOverride[0])
		}
		if p.ID == "augment" && p.BaseURL == "" {
			return config.Source{}, fmt.Errorf("Augment requires -base-url set to the tenant URL paired with your token")
		}
		if p.ID == "aistudio-build" && p.BaseURL == "" {
			return config.Source{}, fmt.Errorf("AI Studio Build requires -base-url set to your own https://ai.studio/apps/<app-id>")
		}
		s := config.Source{ID: id, Provider: id, Adapter: p.Adapter, BaseURL: p.BaseURL, KeyEnv: "COT_" + strings.ToUpper(strings.ReplaceAll(id, "-", "_")) + "_KEY", Local: p.Kind == "local", Paid: p.Kind != "local", MaxInflight: 1, QuotaDomain: id + "-account", QuotaMaxInflight: 1, Models: []config.Model{{ID: model, Upstream: model, Protocols: p.Protocols, Tier: "unrated", Tools: "unknown", MaxInputBytes: 1 << 20}}}
		s.Anonymous = p.Anonymous
		s.SourceKind = "product_reverse"
		s.ExecutionLocation = "local"
		s.InferenceLocation = "unknown"
		s.BillingMode = "unknown"
		s.CredentialMode = "api_key"
		switch p.Kind {
		case "api":
			s.SourceKind = "vendor_api"
			s.InferenceLocation = "remote"
			s.BillingMode = "metered"
		case "cloud":
			s.SourceKind = "cloud_api"
			s.InferenceLocation = "remote"
			s.BillingMode = "metered"
		case "aggregator":
			s.SourceKind = "aggregator_api"
			s.InferenceLocation = "remote"
			s.BillingMode = "metered"
		case "subscription":
			s.BillingMode = "subscription"
		case "device":
			s.SourceKind = "app_reverse"
			s.CredentialMode = "device_session"
		}
		if p.Kind == "local" && (p.Adapter == "openai" || p.Adapter == "anthropic" || p.Adapter == "gemini") {
			s.SourceKind = "local_model"
			s.InferenceLocation = "local"
			s.BillingMode = "local"
		}
		if p.Adapter == "devin-cli" || p.Adapter == "zcode" {
			s.SourceKind = "cli_reverse"
			s.CredentialMode = "cli_session"
		}
		if p.Anonymous {
			s.CredentialMode = "anonymous"
		}
		s.Paid = s.BillingMode == "metered" || s.BillingMode == "subscription"
		if p.Adapter == "app-device" {
			selectors := map[string]string{"meituan-xiaotuan": "meituan_xiaotuan", "wangzhe-lingbao": "wangzhe_lingbao", "douyin-xiaohuoren": "douyin_xiaohuoren"}
			s.Models[0].ID = selectors[p.ID]
			s.Models[0].Upstream = selectors[p.ID]
			s.Models[0].MaxInputBytes = 64 << 10
			s.QuotaDomain = "android-device"
		}
		if p.Anonymous {
			s.KeyEnv = ""
		}
		if p.TextOnly {
			s.Models[0].Tools = "none"
		}
		if p.Adapter == "aistudio-build" || p.Adapter == "zcode" || p.Adapter == "devin-cli" {
			s.KeyEnv = ""
		}
		if p.Adapter == "promptql" || p.Adapter == "copilot-m365" || p.Adapter == "weread-ai" {
			s.Models[0].ID = "web"
			s.Models[0].Upstream = "web"
		}
		if p.Adapter == "chatgpt-web" {
			s.KeyEnv = ""
			s.Models[0].ID = "web"
			s.Models[0].Tools = "none"
			s.Models[0].MaxInputBytes = 64 << 10
		}
		if p.Adapter == "doubao" {
			// Doubao authenticates through the user's configured Chrome profile.
			// KeyEnv is optional and, when explicitly supplied by a caller, is
			// interpreted as a cookie string only inside a fresh browser context.
			s.KeyEnv = ""
		}
		if p.Adapter == "gemini-web" || p.Adapter == "gigachat-web" || p.Adapter == "duckduckgo-web" {
			// These adapters use the model currently selected by the
			// browser page. Keep the gateway model contract at the sole
			// supported selector instead of echoing an arbitrary label.
			s.KeyEnv = ""
			s.Models[0].ID = "web"
			s.Models[0].Upstream = "web"
			s.Models[0].Tools = "none"
			s.Models[0].MaxInputBytes = 64 << 10
		}
		return s, nil
	}
	return config.Source{}, fmt.Errorf("unknown provider %s", id)
}
