package routing

import (
	"context"
	"testing"
	"time"
)

func rate(value float64) *float64 { return &value }

func TestGroupVisionAndPriceCeiling(t *testing.T) {
	c := fixture(1)
	c.Groups[0].RequireVision = true
	if got := New(c).Explain(query())["s0/model"]; got != "vision_required" {
		t.Fatal(got)
	}
	c.Sources[0].Models[0].Vision = true
	c.Groups[0].MaxUSDPerMillion = rate(2)
	if got := New(c).Explain(query())["s0/model"]; got != "cost_unknown" {
		t.Fatal(got)
	}
	c.Sources[0].Models[0].InputUSDPerMillion = rate(1)
	if got := New(c).Explain(query())["s0/model"]; got != "cost_unknown" {
		t.Fatal(got)
	}
	c.Sources[0].Models[0].OutputUSDPerMillion = rate(3)
	if got := New(c).Explain(query())["s0/model"]; got != "cost_ceiling" {
		t.Fatal(got)
	}
	c.Sources[0].Models[0].OutputUSDPerMillion = rate(2)
	r := New(c)
	lease, err := r.Acquire(context.Background(), query())
	if err != nil {
		t.Fatal(err)
	}
	lease.Release(200, 0)
	// A named-source request does not inherit a group's policy.
	c.Sources[0].Models[0].Vision = false
	q := query()
	q.Model = "s0/model"
	if got := New(c).Explain(q)["s0/model"]; got != "eligible" {
		t.Fatal(got)
	}
}

func TestHardRequestCostBudget(t *testing.T) {
	c := fixture(1)
	c.Sources[0].Models[0].InputUSDPerMillion = rate(1)
	c.Sources[0].Models[0].OutputUSDPerMillion = rate(2)
	c.Groups[0].MaxUSDPerRequest = rate(0.0002)
	q := query()
	q.Bytes = 100
	q.OutputTokens = 100
	if got := New(c).Explain(q)["s0/model"]; got != "request_cost_budget" {
		t.Fatalf("budget should reject estimated cost: %s", got)
	}
	c.Groups[0].MaxUSDPerRequest = rate(0.00031)
	if got := New(c).Explain(q)["s0/model"]; got != "eligible" {
		t.Fatalf("budget should admit within bound: %s", got)
	}
	q.OutputTokens = 0
	if got := New(c).Explain(q)["s0/model"]; got != "request_cost_unknown" {
		t.Fatalf("missing output bound should fail closed: %s", got)
	}
	q.OutputTokens = 100
	q.SpentUSD = 0.0002
	if got := New(c).Explain(q)["s0/model"]; got != "request_cost_budget" {
		t.Fatalf("spent retry budget was not enforced: %s", got)
	}
	q.Model = "s0/model"
	if got := New(c).Explain(q)["s0/model"]; got != "eligible" {
		t.Fatalf("named source unexpectedly inherited budget: %s", got)
	}
}

func TestOrderedPreferencesAndCapacity(t *testing.T) {
	c := fixture(3)
	c.Groups[0].Preferences = []string{"lower_cost", "official_api"}
	for i := range c.Sources {
		c.Sources[i].Models[0].InputUSDPerMillion = rate(1)
		c.Sources[i].Models[0].OutputUSDPerMillion = rate(2)
	}
	c.Sources[0].Models[0].OutputUSDPerMillion = nil // unknown is not free
	c.Sources[2].SourceKind = "vendor_api"
	r := New(c)
	first, err := r.Acquire(context.Background(), query())
	if err != nil || first.Target.Source != 2 {
		t.Fatalf("first: %v %v", first, err)
	}
	second, err := r.Acquire(context.Background(), query())
	if err != nil || second.Target.Source != 1 {
		t.Fatalf("second: %v %v", second, err)
	}
	first.Release(200, 0)
	second.Release(200, 0)
	// Primary preference beats a conflicting secondary preference.
	c.Sources[2].Models[0].OutputUSDPerMillion = rate(3)
	r = New(c)
	if got, _ := r.choose(query(), time.Now()); got != 1 {
		t.Fatal(got)
	}
	c.Groups[0].Preferences = []string{"official_api", "lower_cost"}
	r = New(c)
	if got, _ := r.choose(query(), time.Now()); got != 2 {
		t.Fatal(got)
	}
}

func TestSubscriptionReverseAndObservedLatencyPreferences(t *testing.T) {
	for _, preference := range []string{"existing_subscription", "reverse_source", "lower_latency"} {
		t.Run(preference, func(t *testing.T) {
			c := fixture(2)
			c.Groups[0].Preferences = []string{preference}
			c.Groups[0].AllowPaid = true
			c.Sources[1].BillingMode = "subscription"
			c.Sources[1].SourceKind = "browser_reverse"
			r := New(c)
			r.state[1].latency = 200 // unobserved zero must not win
			if got, _ := r.choose(query(), time.Now()); got != 1 {
				t.Fatal(got)
			}
		})
	}
}
