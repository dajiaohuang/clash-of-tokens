package routing

import (
	"clash-of-tokens/internal/protocol"
	"time"
)

func (l *Lease) RecordExecution(result protocol.ExecutionResult) {
	l.router.mu.Lock()
	defer l.router.mu.Unlock()
	l.state.lastExecution = &result
}

func (l *Lease) FirstOutput() {
	l.firstOutput.Do(func() {
		l.router.mu.Lock()
		defer l.router.mu.Unlock()
		ms := float64(time.Since(l.started).Microseconds()) / 1000
		if l.state.ttftMS == 0 {
			l.state.ttftMS = ms
		} else {
			l.state.ttftMS = l.state.ttftMS*.8 + ms*.2
		}
	})
}
