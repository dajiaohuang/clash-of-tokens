package upstream

import (
	"clash-of-tokens/catalog"
	"clash-of-tokens/internal/config"
	"testing"
)

// A valid preset must select its implementation, not accidentally fall through
// to generic HTTP routing. Config validation alone cannot prove this.
func TestRegisteredWebSourcesHaveProductDispatch(t *testing.T) {
	for _, entry := range catalog.All() {
		if entry.Kind != "web" || entry.Implementation == "not_implemented" {
			continue
		}
		c := New(config.Source{ID: entry.ID, Adapter: entry.Adapter, BaseURL: entry.BaseURL})
		if c.adapter == nil && c.web == nil {
			t.Errorf("%s falls through to generic HTTP", entry.ID)
		}
		c.Close()
	}
}
