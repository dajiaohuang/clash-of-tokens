package api

import (
	"clash-of-tokens/internal/config"
	"clash-of-tokens/internal/credentials"
	"testing"
)

func TestCredentialBindingUsesEffectiveReference(t *testing.T) {
	c := config.Config{Accounts: []config.Account{{ID: "account", CredentialRef: "cred://login"}}, Sources: []config.Source{{ID: "source", Adapter: "openai", AccountID: "account"}}}
	metadata := []credentials.Metadata{{ID: "cred://login", Kind: "username_password"}, {ID: "cred://key", Kind: "api_key"}}
	if validateCredentialBindings(c, metadata) == nil {
		t.Fatal("password accepted as API key")
	}
	c.Sources[0].CredentialRef = "cred://key"
	if err := validateCredentialBindings(c, metadata); err != nil {
		t.Fatal(err)
	}
	c.Sources[0].CredentialRef = "cred://missing"
	if validateCredentialBindings(c, metadata) == nil {
		t.Fatal("missing reference accepted")
	}
}
