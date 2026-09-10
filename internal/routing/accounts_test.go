package routing

import (
	"clash-of-tokens/internal/config"
	"context"
	"testing"
)

func accountFixture() config.Config {
	c := fixture(2)
	c.Providers = []config.Provider{{ID: "p", Enabled: true, AutoApproved: true}}
	c.Accounts = []config.Account{{ID: "a", ProviderID: "p", Enabled: true, AutoApproved: true, MaxInflight: 1, Weight: 1, QuotaDomain: "shared"}}
	for i := range c.Sources {
		c.Sources[i].Provider = "p"
		c.Sources[i].AccountID = "a"
		c.Sources[i].QuotaDomain = "shared"
		c.Sources[i].QuotaMaxInflight = 2
	}
	return c
}

func TestAccountCapacityAcrossSources(t *testing.T) {
	r := New(accountFixture())
	l, err := r.Acquire(context.Background(), query())
	if err != nil {
		t.Fatal(err)
	}
	for id, reason := range r.Explain(query()) {
		if reason != "account_capacity" {
			t.Fatalf("%s: %s", id, reason)
		}
	}
	l.Release(200, 0)
	for id, reason := range r.Explain(query()) {
		if reason != "eligible" {
			t.Fatalf("%s: %s", id, reason)
		}
	}
}

func TestFourLevelEnableAndApproval(t *testing.T) {
	for _, level := range []string{"provider", "account", "source", "provider_auto", "account_auto", "source_auto"} {
		t.Run(level, func(t *testing.T) {
			c := accountFixture()
			switch level {
			case "provider":
				c.Providers[0].Enabled = false
			case "account":
				c.Accounts[0].Enabled = false
			case "source":
				c.Sources[0].Enabled = false
			case "provider_auto":
				c.Providers[0].AutoApproved = false
			case "account_auto":
				c.Accounts[0].AutoApproved = false
			case "source_auto":
				c.Sources[0].AutoApproved = false
			}
			r := New(c)
			if reason := r.Explain(query())["s0/model"]; reason == "eligible" {
				t.Fatal("auto accepted disabled/unapproved source")
			}
			q := query()
			q.Model = "s0/model"
			reason := r.Explain(q)["s0/model"]
			isApproval := level == "provider_auto" || level == "account_auto" || level == "source_auto"
			if (reason == "eligible") != isApproval {
				t.Fatalf("explicit route: %s", reason)
			}
		})
	}
}
