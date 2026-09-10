package api

import (
	"context"
	"io"
	"net/http"
	"time"

	"clash-of-tokens/internal/protocol"
	"clash-of-tokens/internal/routing"
)

func (s *Server) streamResponse(ctx context.Context, w http.ResponseWriter, body io.Reader, proto string, lease *routing.Lease) (protocol.ExecutionResult, bool) {
	observer := protocol.CompletionObserver{Protocol: proto}
	reader := protocol.NewSSEReader(body, int(min(s.cfg.Runtime.MaxOutputBytes, 1<<20)))
	rc := http.NewResponseController(w)
	defer rc.SetWriteDeadline(time.Time{})
	result := protocol.ExecutionResult{}
	sent, observedOutput := false, false
	var total int64
	for {
		frame, err := reader.Next()
		if err == io.EOF {
			result.TransportOK = true
			result.ProtocolComplete = observer.Complete
			if !observer.Complete {
				result.UpstreamError = "missing_completion"
			}
			return result, sent
		}
		if err != nil {
			result.UpstreamError = "truncated_stream"
			result.ClientCanceled = ctx.Err() != nil
			return result, sent
		}
		total += int64(len(frame))
		if total > s.cfg.Runtime.MaxOutputBytes {
			result.UpstreamError = "output_limit"
			return result, sent
		}
		observer.Observe(frame)
		if observer.Error != "" {
			result.UpstreamError = observer.Error
			return result, sent
		}
		if observer.SawOutput && !observedOutput {
			lease.FirstOutput()
			observedOutput = true
		}
		_ = rc.SetWriteDeadline(time.Now().Add(time.Duration(s.cfg.Runtime.WriteTimeoutMS) * time.Millisecond))
		n, err := w.Write(frame)
		sent = true
		s.outputBytes.Add(uint64(n))
		if err != nil {
			result.ClientCanceled = true
			return result, sent
		}
		if err = rc.Flush(); err != nil {
			result.ClientCanceled = true
			return result, sent
		}
		if observer.Terminal {
			result.TransportOK = true
			result.ProtocolComplete = observer.Complete
			return result, sent
		}
	}
}
