package routing

import (
	"clash-of-tokens/internal/protocol"
	"time"
)

func (l *Lease) RecordExecution(result protocol.ExecutionResult) {
	// Runtime status is an administration response. Never retain an arbitrary
	// adapter error string, which may contain a URL, credential or response body.
	result = result.Redacted()
	l.router.mu.Lock()
	defer l.router.mu.Unlock()
	if l.execution == nil {
		input := maxInt64(result.InputTokens, 0)
		output := maxInt64(result.OutputTokens, 0)
		total := maxInt64(result.TotalTokens, 0)
		if total == 0 && (input > 0 || output > 0) {
			total = input + output
		}
		l.state.inputTokens += uint64(input)
		l.state.outputTokens += uint64(output)
		l.state.totalTokens += uint64(total)
		l.state.executions++
		if result.UsageKnown && l.Target.Model.InputUSDPerMillion != nil && l.Target.Model.OutputUSDPerMillion != nil {
			l.state.pricedExecutions++
			l.state.estimatedCost += float64(input)/1e6*float64(*l.Target.Model.InputUSDPerMillion) + float64(output)/1e6*float64(*l.Target.Model.OutputUSDPerMillion)
		}
		l.state.costKnown = l.state.executions > 0 && l.state.pricedExecutions == l.state.executions
	}
	l.state.lastExecution = &result
	l.execution = &result
}

func maxInt64(v, zero int64) int64 {
	if v < zero {
		return zero
	}
	return v
}

func (l *Lease) FirstOutput() {
	l.firstOutput.Do(func() {
		l.router.mu.Lock()
		defer l.router.mu.Unlock()
		ms := float64(time.Since(l.started).Microseconds()) / 1000
		l.ttftMS = ms
		if l.state.ttftMS == 0 {
			l.state.ttftMS = ms
		} else {
			l.state.ttftMS = l.state.ttftMS*.8 + ms*.2
		}
	})
}
