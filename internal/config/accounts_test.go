package config

import (
	"strings"
	"testing"
)

func TestAccountBindingValidation(t *testing.T) {
	c := Default()
	c.Providers = []Provider{{ID: "p"}}
	c.Accounts = []Account{{ID: "a", ProviderID: "p", CredentialRef: "cred://one", QuotaDomain: "quota", MaxInflight: 1, Weight: 1}}
	c.Sources = []Source{{ID: "s", Provider: "p", AccountID: "a", QuotaDomain: "quota"}}
	if err := c.ValidateAccounts(); err != nil {
		t.Fatal(err)
	}
	if c.SourceCredentialRef(c.Sources[0]) != "cred://one" {
		t.Fatal("credential not inherited")
	}
	c.Sources[0].Provider = "other"
	if err := c.ValidateAccounts(); err == nil || !strings.Contains(err.Error(), "migrate source first") {
		t.Fatal("cross-provider binding accepted")
	}
	c.Sources[0].Provider = "p"
	c.Sources[0].CredentialRef = "cred://different"
	if c.ValidateAccounts() == nil {
		t.Fatal("conflicting credential accepted")
	}
}

func TestAccountRoutingMetadataDefaultsApplyToBoundSource(t *testing.T) {
	c := Default()
	c.Providers = []Provider{{ID: "p"}}
	c.Accounts = []Account{{ID: "a", ProviderID: "p", BaseURL: "http://127.0.0.1:9000/v1", Organization: "org", Project: "project", QuotaDomain: "quota", MaxInflight: 1, Weight: 1}}
	c.Sources = []Source{{ID: "s", Provider: "p", Adapter: "openai", AccountID: "a", Local: true, MaxInflight: 1, QuotaDomain: "quota", QuotaMaxInflight: 1, Models: []Model{{ID: "m", Upstream: "m", Protocols: []string{"chat"}, Tier: "unrated", Tools: "none", MaxInputBytes: 1024}}}}
	if err := c.Validate(); err != nil {
		t.Fatal(err)
	}
	effective := c.EffectiveSource(c.Sources[0])
	if effective.BaseURL != c.Accounts[0].BaseURL || effective.Organization != "org" || effective.Project != "project" {
		t.Fatalf("account defaults were not applied: %+v", effective)
	}
	c.Sources[0].Project = "source-project"
	if got := c.EffectiveSource(c.Sources[0]).Project; got != "source-project" {
		t.Fatalf("source override was lost: %q", got)
	}
}

func TestAccountRoutingMetadataRejectsUnsafeValues(t *testing.T) {
	base := Config{Providers: []Provider{{ID: "p"}}, Accounts: []Account{{ID: "a", ProviderID: "p", QuotaDomain: "q", MaxInflight: 1, Weight: 1}}}
	for _, test := range []struct {
		name string
		edit func(*Account)
	}{
		{"base URL query", func(a *Account) { a.BaseURL = "https://example.test/v1?token=secret" }},
		{"organization newline", func(a *Account) { a.Organization = "org\nheader" }},
		{"project too long", func(a *Account) { a.Project = strings.Repeat("p", 257) }},
	} {
		t.Run(test.name, func(t *testing.T) {
			c := base
			c.Accounts = append([]Account(nil), base.Accounts...)
			test.edit(&c.Accounts[0])
			if err := c.ValidateAccounts(); err == nil {
				t.Fatal("unsafe account metadata accepted")
			}
		})
	}
}
