package api

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"time"

	"clash-of-tokens/internal/config"
	"clash-of-tokens/internal/routing"
	"clash-of-tokens/internal/upstream"
)

func (s *Server) attemptLimit(q routing.Query) int {
	if q.Stateful {
		return 1
	}
	id := q.Model
	if strings.HasPrefix(id, "auto/") {
		id = "auto"
	}
	for _, g := range s.cfg.Groups {
		if g.ID != id || g.Type == "select" {
			continue
		}
		if g.MaxAttempts > 0 {
			return g.MaxAttempts
		}
		if g.Type == "fallback" {
			return max(1, min(8, len(g.Sources)))
		}
	}
	return 1
}

func rejectedBeforeGeneration(source config.Source, status int) bool {
	// Only native API adapters have this rejection contract. Reverse adapters
	// may return an error after a side effect and are never inferred retryable.
	if source.Adapter != "openai" && source.Adapter != "anthropic" && source.Adapter != "gemini" {
		return false
	}
	switch source.SourceKind {
	case "vendor_api", "cloud_api", "aggregator_api", "custom_api", "local_model":
		return status == 401 || status == 403 || status == 429
	}
	return false
}

// acquireResponse never writes downstream. Each attempt rewrites the original
// payload and releases its lease before selecting a different source.
func (s *Server) acquireResponse(ctx context.Context, q routing.Query, stream bool, headers http.Header, payload func(string) []byte) (*routing.Lease, *http.Response, error) {
	for attempt, limit := 0, s.attemptLimit(q); ; attempt++ {
		queueCtx, cancel := context.WithTimeout(ctx, time.Duration(s.cfg.Runtime.QueueTimeoutMS)*time.Millisecond)
		lease, err := s.Router.Acquire(queueCtx, q)
		cancel()
		if err != nil {
			return nil, nil, err
		}
		resp, err := s.client(lease.Target.Source).Do(ctx, q.Protocol, lease.Target.Model.Upstream, stream, payload(lease.Target.Model.Upstream), headers)
		safe := false
		status := 502
		var cooldown time.Duration
		if err != nil {
			var transport *upstream.TransportError
			safe = errors.As(err, &transport) && transport.BeforeSubmission
		} else {
			status = resp.StatusCode
			cooldown = retryAfter(resp.Header.Get("Retry-After"))
			safe = rejectedBeforeGeneration(s.cfg.Sources[lease.Target.Source], status)
		}
		if !safe || attempt+1 >= limit || ctx.Err() != nil {
			return lease, resp, err
		}
		if resp != nil {
			resp.Body.Close()
		}
		lease.Release(status, cooldown)
		q.ExcludedSources = append(q.ExcludedSources, s.cfg.Sources[lease.Target.Source].ID)
	}
}
