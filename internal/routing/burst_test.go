package routing

import (
	"context"
	"errors"
	"testing"
	"time"
)

func awaitQueue(t *testing.T, r *Router, n int) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		r.mu.Lock()
		got := r.waiting
		r.mu.Unlock()
		if got == n {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatalf("queue did not reach %d", n)
}

func TestBurstFillsAllAccountsAndDrains(t *testing.T) {
	c := fixture(8)
	c.Runtime.MaxQueued = 256
	r := New(c)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	held := make([]*Lease, 8)
	for i := range held {
		held[i], _ = r.Acquire(ctx, query())
	}
	result := make(chan *Lease, 256)
	errs := make(chan error, 256)
	for range 256 {
		go func() {
			l, e := r.Acquire(ctx, query())
			if e != nil {
				errs <- e
			} else {
				result <- l
			}
		}()
	}
	awaitQueue(t, r, 256)
	for _, l := range held {
		l.Release(200, 0)
	}
	for wave := 0; wave < 32; wave++ {
		seen := map[int]bool{}
		for i := range held {
			select {
			case held[i] = <-result:
			case e := <-errs:
				t.Fatal(e)
			case <-ctx.Done():
				t.Fatal("free accounts failed to fill")
			}
			if seen[held[i].Target.Source] {
				t.Fatal("account concurrency exceeded")
			}
			seen[held[i].Target.Source] = true
		}
		for _, l := range held {
			l.Release(200, 0)
		}
	}
	awaitQueue(t, r, 0)
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.active != 0 {
		t.Fatal("leaked active leases")
	}
}

func TestWakeBypassesSaturatedAccountAndCancelledHead(t *testing.T) {
	c := fixture(2)
	c.Runtime.MaxQueued = 10
	r := New(c)
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	q0, q1 := query(), query()
	q0.Model = "s0/model"
	q1.Model = "s1/model"
	a, _ := r.Acquire(ctx, q0)
	defer a.Release(200, 0)
	b, _ := r.Acquire(ctx, q1)
	headCtx, headCancel := context.WithCancel(ctx)
	defer headCancel()
	head := make(chan error, 1)
	go func() {
		l, e := r.Acquire(headCtx, q0)
		if l != nil {
			l.Release(200, 0)
		}
		head <- e
	}()
	awaitQueue(t, r, 1)
	next := make(chan *Lease, 1)
	go func() { l, _ := r.Acquire(ctx, q1); next <- l }()
	awaitQueue(t, r, 2)
	b.Release(200, 0)
	select {
	case l := <-next:
		if l == nil {
			t.Fatal("unrelated account blocked")
		}
		l.Release(200, 0)
	case <-ctx.Done():
		t.Fatal("unrelated account blocked")
	}
	headCancel()
	if e := <-head; !errors.Is(e, context.Canceled) {
		t.Fatal(e)
	}
	awaitQueue(t, r, 0)
}

func TestDisableDrainsIneligibleWaiters(t *testing.T) {
	c := fixture(1)
	c.Runtime.MaxQueued = 64
	r := New(c)
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	held, _ := r.Acquire(ctx, query())
	defer held.Release(200, 0)
	result := make(chan error, 64)
	for range 64 {
		go func() {
			l, e := r.Acquire(ctx, query())
			if l != nil {
				l.Release(200, 0)
			}
			result <- e
		}()
	}
	awaitQueue(t, r, 64)
	r.SetEnabled("s0", false)
	for range 64 {
		if e := <-result; !errors.Is(e, ErrUnavailable) {
			t.Fatal(e)
		}
	}
	awaitQueue(t, r, 0)
}

func TestCooldownExpiresWithoutReleaseSignal(t *testing.T) {
	r := New(fixture(1))
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	l, _ := r.Acquire(ctx, query())
	l.Release(429, 20*time.Millisecond)
	l, err := r.Acquire(ctx, query())
	if err != nil {
		t.Fatal(err)
	}
	l.Release(200, 0)
}

func TestSignalTargetsOnlyFirstRunnableWaiter(t *testing.T) {
	r := New(fixture(1))
	first := &waiter{query: query(), ctx: context.Background(), ready: make(chan struct{}, 1)}
	second := &waiter{query: query(), ctx: context.Background(), ready: make(chan struct{}, 1)}
	first.next = second
	r.head, r.tail, r.waiting = first, second, 2
	r.signal()
	r.signal() // Coalesce repeated releases until the chosen waiter runs.
	if len(first.ready) != 1 || len(second.ready) != 0 {
		t.Fatal("unexpected broadcast or lost wake")
	}
	if r.turn(second) {
		t.Fatal("younger request bypassed runnable older request")
	}
	r.removeWaiter(first)
	r.signal()
	if len(second.ready) != 1 {
		t.Fatal("baton was not passed")
	}
}
