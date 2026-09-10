package routing

import (
	"clash-of-tokens/internal/config"
	"context"
	"errors"
	"fmt"
	"testing"
	"time"
)

func fixture(n int) config.Config {
	c := config.Default()
	c.Runtime.MaxInflight = n
	c.Runtime.MaxQueued = 2
	for i := 0; i < n; i++ {
		id := fmt.Sprint("s", i)
		c.Sources = append(c.Sources, config.Source{ID: id, Enabled: true, AutoApproved: true, MaxInflight: 1, QuotaDomain: id, QuotaMaxInflight: 1, Models: []config.Model{{ID: "model", Upstream: "model", Protocols: []string{"chat"}, Tier: "silver", RatingBasis: "manual", Tools: "native", MaxInputBytes: 1024}}})
		c.Groups[0].Sources = append(c.Groups[0].Sources, id)
		c.Sources[i].BillingMode = "free_allowance"
	}
	return c
}
func query() Query { return Query{Model: "auto/silver", Protocol: "chat", Bytes: 100} }
func TestAllCandidatesAndSharedQuota(t *testing.T) {
	c := fixture(3)
	c.Sources[1].QuotaDomain = c.Sources[0].QuotaDomain
	r := New(c)
	a, e := r.Acquire(context.Background(), query())
	if e != nil {
		t.Fatal(e)
	}
	defer a.Release(200, 0)
	b, e := r.Acquire(context.Background(), query())
	if e != nil {
		t.Fatal(e)
	}
	defer b.Release(200, 0)
	if b.Target.Source != 2 {
		t.Fatal("shared quota exceeded")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
	defer cancel()
	if _, e = r.Acquire(ctx, query()); !errors.Is(e, context.DeadlineExceeded) {
		t.Fatal(e)
	}
}
func TestPolicyAndDisable(t *testing.T) {
	c := fixture(2)
	c.Sources[0].Models[0].Tier = "bronze"
	c.Sources[1].Paid = true
	c.Sources[1].BillingMode = "metered"
	r := New(c)
	if _, e := r.Acquire(context.Background(), query()); !errors.Is(e, ErrUnavailable) {
		t.Fatal(e)
	}
	c.Sources[1].Paid = false
	c.Sources[1].BillingMode = "free_allowance"
	r = New(c)
	l, e := r.Acquire(context.Background(), query())
	if e != nil || l.Target.Source != 1 {
		t.Fatalf("%v %v", l, e)
	}
	r.SetEnabled("s1", false)
	l.Release(200, 0)
	if _, e = r.Acquire(context.Background(), query()); !errors.Is(e, ErrUnavailable) {
		t.Fatal(e)
	}
}
func TestCooldownAndAuthentication(t *testing.T) {
	r := New(fixture(1))
	l, _ := r.Acquire(context.Background(), query())
	l.Release(403, 0)
	if _, e := r.Acquire(context.Background(), query()); !errors.Is(e, ErrUnavailable) {
		t.Fatal(e)
	}
	r.SetEnabled("s0", true)
	l, _ = r.Acquire(context.Background(), query())
	l.Release(429, time.Minute)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
	defer cancel()
	if _, e := r.Acquire(ctx, query()); !errors.Is(e, context.DeadlineExceeded) {
		t.Fatal(e)
	}
}
func TestStatefulRequiresExplicitSource(t *testing.T) {
	r := New(fixture(1))
	q := query()
	q.Stateful = true
	if _, e := r.Acquire(context.Background(), q); !errors.Is(e, ErrUnavailable) {
		t.Fatal(e)
	}
	q.Model = "s0/model"
	l, e := r.Acquire(context.Background(), q)
	if e != nil {
		t.Fatal(e)
	}
	l.Release(200, 0)
}

func TestFallbackFollowsConfiguredMemberOrder(t *testing.T) {
	c := fixture(3)
	c.Groups = []config.Group{{ID: "fallback", Type: "fallback", Sources: []string{"s2", "s0", "s1"}, MinTier: "silver"}}
	r := New(c)
	l, err := r.Acquire(context.Background(), Query{Model: "fallback", Protocol: "chat", Bytes: 100})
	if err != nil {
		t.Fatal(err)
	}
	if got := c.Sources[l.Target.Source].ID; got != "s2" {
		t.Fatalf("fallback ignored configured order: got %s", got)
	}
	l.Release(429, 0)
	l, err = r.Acquire(context.Background(), Query{Model: "fallback", Protocol: "chat", Bytes: 100})
	if err != nil {
		t.Fatal(err)
	}
	if got := c.Sources[l.Target.Source].ID; got != "s0" {
		t.Fatalf("fallback did not advance after rejection: got %s", got)
	}
	l.Release(200, 0)
}

func BenchmarkChoose(b *testing.B) {
	for _, n := range []int{8, 100, 1000} {
		b.Run(fmt.Sprint(n), func(b *testing.B) {
			r := New(fixture(n))
			q := query()
			now := time.Now()
			b.ReportAllocs()
			for b.Loop() {
				r.choose(q, now)
			}
		})
	}
}

func BenchmarkAcquireRelease(b *testing.B) {
	r := New(fixture(8))
	q := query()
	ctx := context.Background()
	b.ReportAllocs()
	for b.Loop() {
		lease, err := r.Acquire(ctx, q)
		if err != nil {
			b.Fatal(err)
		}
		lease.Release(200, 0)
	}
}
