package catalog

import (
	"clash-of-tokens/internal/config"
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
		if p.Adapter == "gemini-cli" || p.Adapter == "antigravity" {
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
