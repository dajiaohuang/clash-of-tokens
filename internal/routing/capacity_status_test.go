package routing

import (
	"context"
	"testing"
)

func TestCapacityStatusSharedAndRetiredLeases(t *testing.T) {
	c := accountFixture()
	r := New(c)
	lease, err := r.Acquire(context.Background(), query())
	if err != nil {
		t.Fatal(err)
	}
	accounts, domains := r.CapacityStatus()
	if len(accounts) != 1 || accounts[0].Active != 1 || accounts[0].Limit != 1 || len(domains) != 1 || domains[0].Active != 1 || domains[0].Limit != 2 || len(domains[0].Sources) != 2 {
		t.Fatal(accounts, domains)
	}
	domains[0].Sources[0] = "mutated"
	_, again := r.CapacityStatus()
	if again[0].Sources[0] == "mutated" {
		t.Fatal("mutable status alias")
	}
	c.Sources = nil
	c.Groups = nil
	c.Accounts = nil
	next := New(c)
	r.Adopt(next)
	accounts, domains = next.CapacityStatus()
	if len(accounts) != 1 || !accounts[0].Retired || accounts[0].Active != 1 || len(domains) != 1 || !domains[0].Retired || domains[0].Active != 1 {
		t.Fatal("retired lease disappeared", accounts, domains)
	}
	lease.Release(200, 0)
	accounts, domains = next.CapacityStatus()
	if len(accounts) != 0 || len(domains) != 0 {
		t.Fatal("released retired capacity retained", accounts, domains)
	}
}

func TestCapacityStatusAfterLoweringSharedLimit(t *testing.T) {
	c := accountFixture()
	c.Accounts[0].MaxInflight = 3
	r := New(c)
	first, err := r.Acquire(context.Background(), query())
	if err != nil {
		t.Fatal(err)
	}
	second, err := r.Acquire(context.Background(), query())
	if err != nil {
		t.Fatal(err)
	}
	for i := range c.Sources {
		c.Sources[i].QuotaMaxInflight = 1
	}
	next := New(c)
	r.Adopt(next)
	_, domains := next.CapacityStatus()
	if len(domains) != 1 || domains[0].Active != 2 || domains[0].Limit != 1 {
		t.Fatal(domains)
	}
	first.Release(200, 0)
	for _, reason := range next.Explain(query()) {
		if reason == "eligible" {
			t.Fatal("lowered quota admitted work before held capacity drained")
		}
	}
	second.Release(200, 0)
	for _, reason := range next.Explain(query()) {
		if reason != "eligible" {
			t.Fatal("drained quota did not restore eligibility", reason)
		}
	}
}
