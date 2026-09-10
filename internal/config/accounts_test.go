package config

import "testing"

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
	if c.ValidateAccounts() == nil {
		t.Fatal("cross-provider binding accepted")
	}
	c.Sources[0].Provider = "p"
	c.Sources[0].CredentialRef = "cred://different"
	if c.ValidateAccounts() == nil {
		t.Fatal("conflicting credential accepted")
	}
}
