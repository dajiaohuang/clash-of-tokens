package routing

import (
	"context"
	"sync"
	"testing"
	"time"
)

func BenchmarkMultiAccountBurst(b *testing.B) {
	c := fixture(8)
	c.Runtime.MaxQueued = 1024
	r := New(c)
	b.ReportAllocs()
	for b.Loop() {
		held := make([]*Lease, 8)
		for i := range held {
			held[i], _ = r.Acquire(context.Background(), query())
		}
		var wg sync.WaitGroup
		for range 128 {
			wg.Go(func() {
				l, err := r.Acquire(context.Background(), query())
				if err != nil {
					b.Error(err)
					return
				}
				l.Release(200, 0)
			})
		}
		for {
			r.mu.Lock()
			n := r.waiting
			r.mu.Unlock()
			if n == 128 {
				break
			}
			time.Sleep(time.Microsecond)
		}
		for _, l := range held {
			l.Release(200, 0)
		}
		wg.Wait()
	}
}

func TestSelectionSkipsBusyAndRotatesEqualScores(t *testing.T) {
	r := New(fixture(4))
	r.quotas[0].active = 1
	r.state[1].cooldown = time.Now().Add(time.Minute)
	for _, tc := range []struct {
		cursor uint64
		want   int
	}{{0, 2}, {3, 3}, {4, 2}} {
		r.cursor = tc.cursor
		got, eligible := r.choose(query(), time.Now())
		if got != tc.want || !eligible {
			t.Fatalf("cursor %d: %d %v", tc.cursor, got, eligible)
		}
	}
	// A positive score must not hide a later lower latency score.
	g := r.groups["auto"]
	g.Type = "latency"
	r.groups["auto"] = g
	r.state[2].latency = 20
	r.state[3].latency = 10
	r.cursor = 2
	if got, _ := r.choose(query(), time.Now()); got != 3 {
		t.Fatal(got)
	}
}

func TestReleaseWakesQueuedRequest(t *testing.T) {
	r := New(fixture(1))
	first, err := r.Acquire(context.Background(), query())
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	result := make(chan error, 1)
	go func() {
		next, err := r.Acquire(ctx, query())
		if next != nil {
			next.Release(200, 0)
		}
		result <- err
	}()
	for {
		r.mu.Lock()
		waiting := r.waiting
		r.mu.Unlock()
		if waiting == 1 {
			break
		}
		if ctx.Err() != nil {
			t.Fatal("request did not queue")
		}
		time.Sleep(time.Millisecond)
	}
	first.Release(200, 0)
	if err := <-result; err != nil {
		t.Fatal(err)
	}
}

func BenchmarkChooseBusy(b *testing.B) {
	r := New(fixture(1000))
	for i := range r.state {
		r.state[i].latency = float64(1000 - i)
	}
	g := r.groups["auto"]
	g.Type = "latency"
	r.groups["auto"] = g
	q, now := query(), time.Now()
	b.ReportAllocs()
	for b.Loop() {
		r.choose(q, now)
	}
}
