package routing

import (
	"context"
	"math"
	"testing"

	"clash-of-tokens/internal/protocol"
)

func TestExecutionUsageAggregatesAndEstimatesDeclaredCost(t *testing.T) {
	c := fixture(1)
	inputRate, outputRate := 1.5, 2.0
	c.Sources[0].Models[0].InputUSDPerMillion = &inputRate
	c.Sources[0].Models[0].OutputUSDPerMillion = &outputRate
	r := New(c)
	l, err := r.Acquire(context.Background(), query())
	if err != nil {
		t.Fatal(err)
	}
	l.RecordExecution(protocol.ExecutionResult{TransportOK: true, ProtocolComplete: true, UpstreamStatus: 200, InputTokens: 2_000_000, OutputTokens: 3_000_000, UsageKnown: true})
	l.RecordExecution(protocol.ExecutionResult{TransportOK: true, ProtocolComplete: true, UpstreamStatus: 200, InputTokens: 99, OutputTokens: 99, UsageKnown: true})
	l.Release(200, 0)
	status := r.Status()[0]
	if status.InputTokens != 2_000_000 || status.OutputTokens != 3_000_000 || status.TotalTokens != 5_000_000 || !status.CostKnown || math.Abs(status.EstimatedCostUSD-9) > 1e-9 {
		t.Fatalf("unexpected usage status: %+v", status)
	}
	if status.LastExecution == nil || status.LastExecution.InputTokens != 99 || !status.LastExecution.UsageKnown {
		t.Fatalf("last execution lost usage: %+v", status.LastExecution)
	}
	events := r.ExecutionEvents()
	if len(events) != 1 || events[0].Execution == nil || events[0].Execution.InputTokens != 99 {
		t.Fatalf("execution event lost declared usage: %+v", events)
	}
}

func TestExecutionUsageUnknownStillCountsTokensWithoutCost(t *testing.T) {
	c := fixture(1)
	inputRate, outputRate := 1.0, 1.0
	c.Sources[0].Models[0].InputUSDPerMillion = &inputRate
	c.Sources[0].Models[0].OutputUSDPerMillion = &outputRate
	r := New(c)
	l, err := r.Acquire(context.Background(), query())
	if err != nil {
		t.Fatal(err)
	}
	l.RecordExecution(protocol.ExecutionResult{TransportOK: true, ProtocolComplete: true, UpstreamStatus: 200, InputTokens: 4, OutputTokens: 6})
	l.Release(200, 0)
	status := r.Status()[0]
	if status.InputTokens != 4 || status.OutputTokens != 6 || status.TotalTokens != 10 || status.CostKnown || status.EstimatedCostUSD != 0 {
		t.Fatalf("unknown usage should not claim cost: %+v", status)
	}
}

func TestMixedKnownAndUnknownUsageWithholdsPartialCost(t *testing.T) {
	c := fixture(1)
	inputRate, outputRate := 1.0, 1.0
	c.Sources[0].Models[0].InputUSDPerMillion = &inputRate
	c.Sources[0].Models[0].OutputUSDPerMillion = &outputRate
	r := New(c)
	first, err := r.Acquire(context.Background(), query())
	if err != nil {
		t.Fatal(err)
	}
	first.RecordExecution(protocol.ExecutionResult{TransportOK: true, ProtocolComplete: true, UpstreamStatus: 200, InputTokens: 1, OutputTokens: 1, TotalTokens: 2, UsageKnown: true})
	first.Release(200, 0)
	second, err := r.Acquire(context.Background(), query())
	if err != nil {
		t.Fatal(err)
	}
	second.RecordExecution(protocol.ExecutionResult{TransportOK: true, ProtocolComplete: true, UpstreamStatus: 200, InputTokens: 2, OutputTokens: 2})
	second.Release(200, 0)
	status := r.Status()[0]
	if status.CostKnown || status.EstimatedCostUSD != 0 || status.InputTokens != 3 || status.OutputTokens != 3 {
		t.Fatalf("partial cost should be withheld: %+v", status)
	}
}
