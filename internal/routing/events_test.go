package routing

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"clash-of-tokens/internal/protocol"
)

func TestExecutionEventRingAndRedaction(t *testing.T) {
	r := New(fixture(1))
	for i := 0; i < MaxExecutionEvents+3; i++ {
		lease, err := r.Acquire(context.Background(), query())
		if err != nil {
			t.Fatal(err)
		}
		lease.RecordExecution(protocol.ExecutionResult{TransportOK: true, ProtocolComplete: true, UpstreamStatus: 200})
		lease.FirstOutput()
		lease.Release(200, 0)
		lease.Release(200, 0)
	}
	events := r.ExecutionEvents()
	if len(events) != MaxExecutionEvents || events[0].Sequence != 4 || events[len(events)-1].Sequence != MaxExecutionEvents+3 {
		t.Fatal("ring order or exactly-once release failed")
	}
	for _, event := range events {
		if event.Source != "s0" || event.Model != "model" || event.Protocol != "chat" || event.Outcome != "succeeded" || event.DurationMS < event.TTFTMS {
			t.Fatalf("bad event: %+v", event)
		}
	}
	events[0].Execution.UpstreamError = "private-adapter-key"
	if r.ExecutionEvents()[0].Execution.UpstreamError != "" {
		t.Fatal("snapshot aliases execution")
	}
	lease, err := r.Acquire(context.Background(), query())
	if err != nil {
		t.Fatal(err)
	}
	lease.RecordExecution(protocol.ExecutionResult{UpstreamError: "private-adapter-key", ClientCanceled: true})
	lease.Release(499, 0)
	data, _ := json.Marshal(r.ExecutionEvents())
	if strings.Contains(string(data), "private-adapter-key") {
		t.Fatal("event leaked adapter text")
	}
	event := r.ExecutionEvents()[MaxExecutionEvents-1]
	if event.Outcome != "canceled" || event.Execution.UpstreamError != "upstream_error" {
		t.Fatal("canceled event lost classification")
	}
}

func TestExecutionEventsSurviveAdoptionAndOmitAdministrativeRelease(t *testing.T) {
	old := New(fixture(1))
	q := query()
	q.Probe = true
	q.Model = "s0/model"
	lease, err := old.Acquire(context.Background(), q)
	if err != nil {
		t.Fatal(err)
	}
	next := New(fixture(1))
	old.Adopt(next)
	lease.RecordExecution(protocol.ExecutionResult{UpstreamStatus: 200, UpstreamError: "missing_completion"})
	lease.Release(502, 0)
	events := next.ExecutionEvents()
	if len(events) != 1 || !events[0].Validation || events[0].Outcome != "failed" || events[0].Execution.UpstreamError != "missing_completion" {
		t.Fatal("retired lease event lost")
	}
	// Administrative leases do not represent an executed generation.
	other := New(fixture(1))
	admin, err := other.Acquire(context.Background(), query())
	if err != nil {
		t.Fatal(err)
	}
	admin.ReleaseAdministrative()
	if len(other.ExecutionEvents()) != 0 {
		t.Fatal("administrative release logged as generation")
	}
}
