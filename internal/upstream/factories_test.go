package upstream

import (
	"clash-of-tokens/catalog"
	"clash-of-tokens/internal/providerdef"
	"testing"
)

func TestEveryDescriptorHasAFactory(t *testing.T) {
	for _, d := range providerdef.All() {
		if factories[d.Factory] == nil {
			t.Errorf("%s: missing factory %s", d.ID, d.Factory)
		}
	}
	for _, entry := range catalog.All() {
		if entry.Implementation == "not_implemented" {
			continue
		}
		d, ok := providerdef.Lookup(entry.Adapter)
		if !ok {
			t.Errorf("%s: missing descriptor", entry.ID)
			continue
		}
		for _, p := range entry.Protocols {
			if !providerdef.Supports(d.ID, p) {
				t.Errorf("%s: unsupported catalog protocol %s", entry.ID, p)
			}
		}
	}
}
