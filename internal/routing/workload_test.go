package routing

import (
	"context"
	"testing"
	"time"
)

func TestWorkloadQueueAndRetiredActiveLease(t *testing.T) {
	r := New(fixture(1))
	lease, err := r.Acquire(context.Background(), query())
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() {
		other, err := r.Acquire(ctx, query())
		if other != nil {
			other.ReleaseAdministrative()
		}
		done <- err
	}()
	deadline := time.Now().Add(time.Second)
	for r.WorkloadStatus().Queued != 1 && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	status := r.WorkloadStatus()
	if status.Active != 1 || status.ActiveLimit != 1 || status.Queued != 1 || status.QueueLimit != 2 {
		t.Fatalf("bad workload: %+v", status)
	}
	cancel()
	<-done
	if r.WorkloadStatus().Queued != 0 {
		t.Fatal("canceled queue retained")
	}
	c := fixture(1)
	c.Sources = nil
	c.Groups[0].Sources = nil
	next := New(c)
	r.Adopt(next)
	if len(next.Status()) != 0 || next.WorkloadStatus().Active != 1 {
		t.Fatal("global active lost removed-source lease")
	}
	lease.Release(200, 0)
	if next.WorkloadStatus().Active != 0 {
		t.Fatal("released lease remained active")
	}
}
