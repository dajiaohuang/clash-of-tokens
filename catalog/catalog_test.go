package catalog

import (
	"clash-of-tokens/internal/config"
	"clash-of-tokens/internal/providerdef"
	"testing"
)

func TestCatalogPresetsAreExecutableConfigurations(t *testing.T) {
	seen := map[string]bool{}
	for _, entry := range All() {
		if seen[entry.ID] {
			t.Fatal("duplicate", entry.ID)
		}
		seen[entry.ID] = true
		if entry.Implementation == "not_implemented" {
			if _, e := Preset(entry.ID, "model"); e == nil {
				t.Fatal("research exposed as supported")
			}
			continue
		}
		var override []string
		if entry.ID == "augment" {
			if _, err := Preset(entry.ID, "default"); err == nil {
				t.Fatal("Augment accepted missing tenant URL")
			}
			override = []string{"https://tenant.example"}
		}
		if entry.ID == "aistudio-build" {
			if _, err := Preset(entry.ID, "gemini-model"); err == nil {
				t.Fatal("AI Studio Build accepted missing operator app URL")
			}
			override = []string{"https://ai.studio/apps/operator-owned-app"}
		}
		p, e := Preset(entry.ID, "test-model", override...)
		if e != nil {
			t.Fatal(e)
		}
		if p.Adapter == "gemini-cli" || p.Adapter == "antigravity" || p.Adapter == "weread-ai" {
			p.Project = "explicit-project"
		}
		c := config.Default()
		c.Sources = []config.Source{p}
		if e = c.Validate(); e != nil {
			t.Errorf("%s: %v", entry.ID, e)
		}
		if p.Enabled || p.AutoApproved {
			t.Fatal("preset sends traffic without approval")
		}
	}
}

func TestCredentialMatchingUsesExactOfficialAliases(t *testing.T) {
	matched := MatchCredentials("chat.openai.com", "api_key")
	if len(matched) == 0 || matched[0].Provider != "openai" || !matched[0].Compatible {
		t.Fatalf("expected OpenAI alias match, got %#v", matched)
	}
	if got := MatchCredentials("chat.openai.com.evil.test", "api_key"); len(got) != 0 {
		t.Fatalf("lookalike domain matched: %#v", got)
	}
}

func TestBrowserPresetsExposeBrowserReverseMetadata(t *testing.T) {
	for _, entry := range All() {
		d, ok := providerdef.Lookup(entry.Adapter)
		if !ok || entry.Kind != "web" || (!d.BrowserRequired && !d.BrowserAuthCheck) {
			continue
		}
		override := []string(nil)
		if entry.ID == "aistudio-build" {
			override = []string{"https://ai.studio/apps/operator-owned-app"}
		}
		source, err := Preset(entry.ID, "test-model", override...)
		if err != nil {
			t.Fatalf("%s: %v", entry.ID, err)
		}
		if source.SourceKind != "browser_reverse" {
			t.Errorf("%s: source kind = %q, want browser_reverse", entry.ID, source.SourceKind)
		}
		wantCredentialMode := "browser_session"
		if entry.Anonymous {
			wantCredentialMode = "anonymous"
		}
		if source.CredentialMode != wantCredentialMode {
			t.Errorf("%s: credential mode = %q, want %s", entry.ID, source.CredentialMode, wantCredentialMode)
		}
	}
}
