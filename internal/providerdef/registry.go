// Package providerdef is the inert contract shared by validation, factories and
// the control plane. Looking up a descriptor never constructs an adapter.
package providerdef

import (
	"sort"
	"strings"
)

type CredentialField struct {
	Name     string `json:"name"`
	Label    string `json:"label"`
	Secret   bool   `json:"secret"`
	Required bool   `json:"required"`
}
type Descriptor struct {
	ID                 string            `json:"id"`
	Factory            string            `json:"factory"`
	Protocols          []string          `json:"protocols"`
	TextOnly           bool              `json:"text_only"`
	ToolsAllowed       bool              `json:"tools_allowed"`
	BrowserRequired    bool              `json:"browser_required"`
	CredentialModes    []string          `json:"credential_modes"`
	CredentialFields   []CredentialField `json:"credential_fields"`
	DefaultMaxInflight int               `json:"default_max_inflight"`
}

var registry = build()

func build() map[string]Descriptor {
	out := map[string]Descriptor{}
	add := func(factory string, textOnly bool, ids string) {
		for _, id := range strings.Fields(ids) {
			if _, exists := out[id]; exists {
				panic("duplicate provider descriptor: " + id)
			}
			out[id] = Descriptor{ID: id, Factory: factory, Protocols: []string{"chat"}, TextOnly: textOnly, ToolsAllowed: !textOnly, CredentialModes: []string{"api_key", "oauth", "cookie", "browser_session"}, CredentialFields: []CredentialField{{Name: "value", Label: "Credential value", Secret: true, Required: true}}, DefaultMaxInflight: 1}
		}
	}
	add("http", false, "openai anthropic gemini copilot codex claude-code gemini-cli qwen-code kimi-code")
	add("chatgptweb", true, "chatgpt-web")
	add("appdevice", true, "app-device")
	add("playground", true, "cloudflare-playground")
	add("chinaapps", true, "tencent-ima weread-ai")
	add("chinafinal", true, "tencent-aistudio-web")
	add("webnext", true, "flowith langfast liaobots")
	add("codingnext", false, "freebuff codebuddy-cn")
	add("codingnext", true, "zed-hosted")
	add("chinamore", true, "minimax-web mimo stepchat")
	add("codingmore", true, "amazon-q augment devin-cli")
	add("codingfinal", true, "cursor windsurf trae v0-web warp zcode qoder")
	add("businessweb", true, "gemini-business aistudio-playground aistudio-build copilot-m365 promptql")
	add("enterpriseweb", true, "maxai notion-web opera-aria google-ai-mode")
	add("chinaremaining", true, "emohaa spark-web qwen-web-cn metaso")
	add("chinanext", true, "dola-web yuanbao deepseek-web doubao")
	add("china", true, "kimi-web qwen-web-intl glm-web zai-web")
	add("coding", false, "kiro antigravity")
	add("embedded", true, "tabbit fanzha chataigpt chatgptfree")
	add("webhttp", true, "venice-web inkeep gptanon perfectassistant aifreeforever toolbaz phindai whiterabbitneo")
	add("majorweb", true, "claude-web grok-web grok-console grok-build genspark zenmux-web blackbox conol-web adapta-web pi reka-web huggingchat hyperagent inner-ai uc-web easemate gemini-web gigachat-web copilot-web perplexity-web t3-web you poe-web meta-ai arena tinycms-web merlin sider monica raycast duckduckgo-web")
	protocols := map[string][]string{
		"openai": {"chat", "responses"}, "chatgpt-web": {"chat", "responses"},
		"grok-console": {"chat", "responses"}, "grok-build": {"chat", "responses"},
		"anthropic": {"messages"}, "claude-code": {"messages"}, "gemini": {"gemini"},
		"gemini-cli": {"gemini"}, "antigravity": {"gemini"}, "copilot": {"chat", "responses", "messages"},
		"codex": {"responses"}, "kiro": {"chat", "messages"}, "kimi-code": {"chat", "messages"},
	}
	for id, ps := range protocols {
		d := out[id]
		d.Protocols = ps
		out[id] = d
	}
	for _, id := range []string{"chatgpt-web", "doubao", "gemini-web", "gigachat-web", "aistudio-build", "cloudflare-playground"} {
		d := out[id]
		d.BrowserRequired = true
		d.CredentialModes = []string{"browser_profile", "browser_session", "cookie"}
		out[id] = d
	}
	for _, id := range []string{"openai", "anthropic", "gemini"} {
		d := out[id]
		d.CredentialModes = []string{"api_key"}
		d.CredentialFields[0].Label = "API key"
		out[id] = d
	}
	for _, id := range []string{"devin-cli", "zcode", "codex", "claude-code", "gemini-cli", "qwen-code", "kimi-code", "kiro", "antigravity", "amazon-q"} {
		d := out[id]
		d.CredentialModes = []string{"oauth", "cli_session", "api_key"}
		out[id] = d
	}
	d := out["kiro"]
	d.ToolsAllowed = false
	out["kiro"] = d
	d = out["app-device"]
	d.CredentialModes = []string{"device_session"}
	d.CredentialFields = nil
	out["app-device"] = d
	return out
}
func Lookup(id string) (Descriptor, bool) {
	d, ok := registry[id]
	if !ok {
		return Descriptor{}, false
	}
	d.Protocols = append([]string(nil), d.Protocols...)
	d.CredentialModes = append([]string(nil), d.CredentialModes...)
	d.CredentialFields = append([]CredentialField(nil), d.CredentialFields...)
	return d, true
}
func All() []Descriptor {
	out := make([]Descriptor, 0, len(registry))
	for id := range registry {
		d, _ := Lookup(id)
		out = append(out, d)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}
func Supports(id, protocol string) bool {
	d, ok := registry[id]
	if !ok {
		return false
	}
	for _, p := range d.Protocols {
		if p == protocol {
			return true
		}
	}
	return false
}
