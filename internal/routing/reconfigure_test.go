package routing

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestHotUpdatePreservesHeldAccountCapacity(t *testing.T) {
	c := accountFixture()
	old := New(c)
	held, err := old.Acquire(context.Background(), query())
	if err != nil {
		t.Fatal(err)
	}
	next := New(c)
	old.Adopt(next)
	if got := next.Explain(query())["s1/model"]; got != "account_capacity" {
		t.Fatal(got)
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	acquired := make(chan *Lease, 1)
	go func() { l, _ := next.Acquire(ctx, query()); acquired <- l }()
	awaitQueue(t, next, 1)
	held.Release(200, 0)
	l := <-acquired
	if l == nil {
		t.Fatal("old lease failed to wake new router")
	}
	l.Release(200, 0)
	if next.active != 0 {
		t.Fatal("leaked cross-generation capacity")
	}
}

func TestRetiredQueueCannotUseOldApproval(t *testing.T) {
	c := fixture(1)
	old := New(c)
	held, _ := old.Acquire(context.Background(), query())
	defer held.Release(200, 0)
	done := make(chan error, 1)
	go func() {
		l, e := old.Acquire(context.Background(), query())
		if l != nil {
			l.Release(200, 0)
		}
		done <- e
	}()
	awaitQueue(t, old, 1)
	c.Sources[0].Enabled = false
	next := New(c)
	old.Adopt(next)
	select {
	case err := <-done:
		if !errors.Is(err, ErrUnavailable) {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("retired waiter stuck")
	}
	if next.Status()[0].Enabled {
		t.Fatal("new source not disabled")
	}
}
