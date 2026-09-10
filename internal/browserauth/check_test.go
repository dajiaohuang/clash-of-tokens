package browserauth

import (
	"context"
	"testing"

	"clash-of-tokens/internal/providerdef"
)

func TestDeclaredBrowserChecks(t *testing.T) {
	for _, d := range providerdef.All() {
		_, implemented := spec(d.ID)
		if d.ID == "chatgpt-web" {
			implemented = true
		}
		if implemented != d.BrowserAuthCheck {
			t.Fatalf("descriptor and browser check disagree for %s", d.ID)
		}
	}
	result := Check(context.Background(), "http://127.0.0.1:1", "https://example.com", "unknown")
	if result.Status != "unsupported" || result.GenerationVerified {
		t.Fatalf("unsupported adapter was probed: %+v", result)
	}
	result = Check(context.Background(), "http://127.0.0.1:1", "file:///private", "claude-web")
	if result.Status != "unknown" {
		t.Fatalf("invalid origin accepted: %+v", result)
	}
}
