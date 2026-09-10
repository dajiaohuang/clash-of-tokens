package routing

import (
	"context"
	"errors"
	"testing"

	"clash-of-tokens/internal/config"
)

func strategyFixture(group config.Group) config.Config {
	c := fixture(2)
	c.Groups = []config.Group{group}
	for i := range c.Sources {
		c.Sources[i].MaxInflight = 2
		c.Sources[i].QuotaMaxInflight = 2
		c.Sources[i].QuotaDomain = c.Sources[i].ID
	}
	return c
}

func TestStrategyMatrixPreservesPolicyBoundaries(t *testing.T) {
	t.Run("fallback advances only after a safe release", func(t *testing.T) {
		c := strategyFixture(config.Group{ID: "fallback", Type: "fallback", Sources: []string{"s0", "s1"}, MinTier: "silver"})
		r := New(c)
		q := Query{Model: "fallback", Protocol: "chat", Bytes: 100}
		first, err := r.Acquire(context.Background(), q)
		if err != nil || first.Target.Source != 0 {
			t.Fatalf("first selection = %#v, err=%v", first, err)
		}
		first.Release(500, 0) // ambiguous server failure keeps the source cooling down
		second, err := r.Acquire(context.Background(), q)
		if err != nil || second.Target.Source != 1 {
			t.Fatalf("ambiguous failure did not leave next request on second member: %#v, err=%v", second, err)
		}
		second.Release(200, 0)
		// A new router models the explicit safe rejection path without carrying
		// the cooldown from the prior attempt.
		r = New(c)
		first, err = r.Acquire(context.Background(), q)
		if err != nil {
			t.Fatal(err)
		}
		first.Release(429, 0)
		second, err = r.Acquire(context.Background(), q)
		if err != nil || second.Target.Source != 1 {
			t.Fatalf("safe rejection did not advance: %#v, err=%v", second, err)
		}
		second.Release(200, 0)
	})

	t.Run("select never falls through", func(t *testing.T) {
		c := strategyFixture(config.Group{ID: "select", Type: "select", Sources: []string{"s0", "s1"}, MinTier: "silver"})
		r := New(c)
		q := Query{Model: "select", Protocol: "chat", Bytes: 100}
		lease, err := r.Acquire(context.Background(), q)
		if err != nil || lease.Target.Source != 0 {
			t.Fatalf("select picked %#v, err=%v", lease, err)
		}
		lease.Release(200, 0)
		if got := r.Explain(q)["s1/model"]; got != "not_selected" {
			t.Fatalf("second select member reason=%q", got)
		}
		r.SetEnabled("s0", false)
		if _, err := r.Acquire(context.Background(), q); !errors.Is(err, ErrUnavailable) {
			t.Fatalf("select fell through after primary disabled: %v", err)
		}
	})

	for _, tc := range []struct {
		name, typ string
		latency   [2]float64
		want      int
	}{
		{"latency", "latency", [2]float64{250, 10}, 1},
		{"load-balance", "load-balance", [2]float64{}, 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c := strategyFixture(config.Group{ID: tc.name, Type: tc.typ, Sources: []string{"s0", "s1"}, MinTier: "silver"})
			r := New(c)
			r.state[0].latency, r.state[1].latency = tc.latency[0], tc.latency[1]
			if tc.typ == "load-balance" {
				held, err := r.Acquire(context.Background(), Query{Model: "s0/model", Protocol: "chat", Bytes: 100})
				if err != nil {
					t.Fatal(err)
				}
				defer held.Release(200, 0)
			}
			lease, err := r.Acquire(context.Background(), Query{Model: tc.name, Protocol: "chat", Bytes: 100})
			if err != nil || lease.Target.Source != tc.want {
				t.Fatalf("selected source=%v, err=%v", lease, err)
			}
			lease.Release(200, 0)
		})
	}
}

func TestMultipleMembershipAndSimulationSelection(t *testing.T) {
	c := strategyFixture(config.Group{ID: "auto", Type: "auto", Sources: []string{"s0", "s1"}, MinTier: "silver"})
	c.Groups = append(c.Groups, config.Group{ID: "reverse", Type: "fallback", Sources: []string{"s1", "s0"}, MinTier: "silver"})
	c.Sources[0].SourceKind = "browser_reverse"
	c.Sources[1].SourceKind = "vendor_api"
	r := New(c)
	q := Query{Model: "reverse", Protocol: "chat", Bytes: 100, Detail: true}
	sim := r.Simulate(q)
	if sim.Selected != "s1/model" || len(sim.Candidates) != 2 || sim.Candidates[0].Order != 1 || !sim.Candidates[0].Selected {
		t.Fatalf("fallback membership simulation = %#v", sim)
	}
	if got := r.Explain(Query{Model: "auto", Protocol: "chat", Bytes: 100})["s0/model"]; got != "eligible" {
		t.Fatalf("source shared by groups is not eligible in auto: %q", got)
	}
	r.SetEnabled("s1", false)
	sim = r.Simulate(q)
	if sim.Selected != "s0/model" || sim.Candidates[0].Reason != "disabled" {
		t.Fatalf("simulation did not expose disabled fallback member: %#v", sim)
	}
	// A simulated query never acquires capacity; both sources are immediately
	// available again after the read-only call.
	if _, err := r.Acquire(context.Background(), Query{Model: "auto", Protocol: "chat", Bytes: 100}); err != nil {
		t.Fatal(err)
	}
}
