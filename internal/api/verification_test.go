package api

import (
	"testing"
	"time"

	"clash-of-tokens/internal/config"
	"clash-of-tokens/internal/credentials"
)

func TestVerificationBindingTracksCredentialAndRuntime(t *testing.T) {
	p := &ControlPlane{runtimeID: "first-run"}
	c := config.Default()
	source := config.Source{ID: "source", Adapter: "openai", BaseURL: "https://example.com", CredentialRef: "cred://key"}
	metadata := credentials.Metadata{ID: "cred://key", Version: 1, CreatedAt: time.Unix(1, 0), UpdatedAt: time.Unix(1, 0)}
	original := p.bindingWithMetadata(c, source, metadata)
	p.runtimeID = "second-run"
	if original != p.bindingWithMetadata(c, source, metadata) {
		t.Fatal("protected binding changed just because of restart")
	}
	metadata.Version++
	if original == p.bindingWithMetadata(c, source, metadata) {
		t.Fatal("credential replacement did not invalidate binding")
	}
	metadata.Version--
	source.BaseURL = "https://another.example.com"
	if original == p.bindingWithMetadata(c, source, metadata) {
		t.Fatal("upstream change did not invalidate binding")
	}
	source.CredentialRef = ""
	source.KeyEnv = "COT_SYNTHETIC_KEY"
	envBinding := p.bindingWithMetadata(c, source, credentials.Metadata{})
	p.runtimeID = "third-run"
	if envBinding == p.bindingWithMetadata(c, source, credentials.Metadata{}) {
		t.Fatal("environment evidence survived a restart without a credential version")
	}
}
