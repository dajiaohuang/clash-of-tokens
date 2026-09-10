package routing

import (
	"time"

	"clash-of-tokens/internal/protocol"
)

const MaxExecutionEvents = 1000

// ExecutionEvent contains only routing identifiers, measurements and fixed
// outcome categories. No prompt, response, URL, credential or arbitrary error.
type ExecutionEvent struct {
	Sequence   uint64                    `json:"sequence"`
	FinishedAt time.Time                 `json:"finished_at"`
	Source     string                    `json:"source"`
	Model      string                    `json:"model"`
	Protocol   string                    `json:"protocol"`
	Validation bool                      `json:"validation"`
	Status     int                       `json:"status"`
	Outcome    string                    `json:"outcome"`
	DurationMS float64                   `json:"duration_ms"`
	TTFTMS     float64                   `json:"ttft_ms"`
	Execution  *protocol.ExecutionResult `json:"execution,omitempty"`
}

// recordEvent runs under the shared capacity mutex at exactly-once release.
func (l *Lease) recordEvent(status int) {
	r := l.router
	r.eventSequence++
	event := ExecutionEvent{Sequence: r.eventSequence, FinishedAt: time.Now().UTC(), Source: r.cfg.Sources[l.Target.Source].ID, Model: l.Target.Model.ID, Protocol: l.protocol, Validation: l.probe, Status: status, Outcome: "failed", DurationMS: float64(time.Since(l.started).Microseconds()) / 1000, TTFTMS: l.ttftMS}
	if status >= 200 && status < 300 {
		event.Outcome = "succeeded"
	}
	if status == 499 {
		event.Outcome = "canceled"
	}
	if l.execution != nil {
		copy := l.execution.Redacted()
		event.Execution = &copy
		if !copy.Successful() {
			event.Outcome = "failed"
		}
		if copy.ClientCanceled {
			event.Outcome = "canceled"
		}
	}
	if len(r.events) < MaxExecutionEvents {
		r.events = append(r.events, event)
	} else {
		r.events[r.eventCursor] = event
		r.eventCursor = (r.eventCursor + 1) % MaxExecutionEvents
	}
}

// ExecutionEvents returns oldest to newest, with no mutable aliases. The shared
// ring survives hot adoption, including events from finishing retired leases.
func (r *Router) ExecutionEvents() []ExecutionEvent {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]ExecutionEvent, len(r.events))
	for i := range out {
		out[i] = r.events[(r.eventCursor+i)%len(r.events)]
		if out[i].Execution != nil {
			copy := *out[i].Execution
			out[i].Execution = &copy
		}
	}
	return out
}
