package routing

import (
	"clash-of-tokens/internal/config"
	"context"
	"testing"
)

func poolFixture(strategy string) config.Config {
	c := fixture(2)
	c.Providers = []config.Provider{{ID: "p", Enabled: true, AutoApproved: true, PoolStrategy: strategy}}
	c.Accounts = []config.Account{
		{ID: "a", ProviderID: "p", Enabled: true, AutoApproved: true, MaxInflight: 2, Weight: 3, QuotaDomain: "qa"},
		{ID: "b", ProviderID: "p", Enabled: true, AutoApproved: true, MaxInflight: 2, Weight: 1, QuotaDomain: "qb"},
	}
	for i := range c.Sources {
		c.Sources[i].Provider = "p"
		c.Sources[i].AccountID = c.Accounts[i].ID
		c.Sources[i].QuotaDomain = c.Accounts[i].QuotaDomain
		c.Sources[i].MaxInflight = 2
		c.Sources[i].QuotaMaxInflight = 2
	}
	return c
}

func TestWeightedAccountPoolDistribution(t *testing.T) {
	c := poolFixture("weighted")
	// Multiple targets on one account must not multiply its configured weight.
	c.Sources[0].Models = append(c.Sources[0].Models, c.Sources[0].Models[0])
	c.Sources[0].Models[1].ID = "other-model"
	r := New(c)
	count := [2]int{}
	for range 40 {
		l, e := r.Acquire(context.Background(), query())
		if e != nil {
			t.Fatal(e)
		}
		count[l.Target.Source]++
		l.Release(200, 0)
	}
	if count[0] < 29 || count[0] > 31 || count[1] < 9 || count[1] > 11 {
		t.Fatal(count)
	}
}
func TestRoundRobinAccountPool(t *testing.T) {
	r := New(poolFixture("round-robin"))
	previous := -1
	for range 10 {
		l, e := r.Acquire(context.Background(), query())
		if e != nil {
			t.Fatal(e)
		}
		if l.Target.Source == previous {
			t.Fatal("account did not rotate")
		}
		previous = l.Target.Source
		l.Release(200, 0)
	}
}
func TestStickyAccountPool(t *testing.T) {
	r := New(poolFixture("sticky"))
	for _, affinity := range []string{"project-a", "project-b", "project-c"} {
		q := query()
		q.Affinity = affinity
		expected := -1
		for range 5 {
			l, e := r.Acquire(context.Background(), q)
			if e != nil {
				t.Fatal(e)
			}
			if expected >= 0 && l.Target.Source != expected {
				t.Fatal("affinity changed account")
			}
			expected = l.Target.Source
			l.Release(200, 0)
		}
	}
}
func TestLeastLoadAccountPool(t *testing.T) {
	r := New(poolFixture("least-load"))
	q := query()
	q.Model = "s0/model"
	held, e := r.Acquire(context.Background(), q)
	if e != nil {
		t.Fatal(e)
	}
	defer held.Release(200, 0)
	l, e := r.Acquire(context.Background(), query())
	if e != nil {
		t.Fatal(e)
	}
	defer l.Release(200, 0)
	if l.Target.Source != 1 {
		t.Fatal("busy account preferred")
	}
}
func TestWeightedSourceGroup(t *testing.T) {
	c := fixture(2)
	c.Groups[0].Type = "weighted"
	c.Sources[0].Weight = 3
	c.Sources[1].Weight = 1
	r := New(c)
	count := [2]int{}
	for range 40 {
		l, e := r.Acquire(context.Background(), query())
		if e != nil {
			t.Fatal(e)
		}
		count[l.Target.Source]++
		l.Release(200, 0)
	}
	if count[0] < 29 || count[0] > 31 {
		t.Fatal(count)
	}
}
