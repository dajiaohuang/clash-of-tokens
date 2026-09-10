package api

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"time"

	"clash-of-tokens/internal/config"
	audit "clash-of-tokens/internal/evidence"
	"clash-of-tokens/internal/protocol"
	"clash-of-tokens/internal/routing"
	"clash-of-tokens/internal/upstream"
)

type ValidationEvidence struct {
	Binding         string                   `json:"binding"`
	Account         string                   `json:"account,omitempty"`
	HistoryRecorded bool                     `json:"history_recorded"`
	Source          string                   `json:"source"`
	Model           string                   `json:"model"`
	Protocol        string                   `json:"protocol"`
	CheckedAt       time.Time                `json:"checked_at"`
	Method          string                   `json:"method"`
	OutputObserved  bool                     `json:"output_observed"`
	Result          protocol.ExecutionResult `json:"result"`
	Verified        bool                     `json:"verified"`
	Checks          ValidationChecks         `json:"checks"`
}

type ValidationChecks struct {
	Connection string  `json:"connection"`
	Auth       string  `json:"auth"`
	Request    string  `json:"request"`
	Streaming  string  `json:"streaming"`
	Completion string  `json:"completion"`
	DurationMS float64 `json:"duration_ms"`
}

func (p *ControlPlane) sourceCheckAdmin(w http.ResponseWriter, r *http.Request, s *Server) bool {
	if !strings.HasPrefix(r.URL.Path, "/admin/sources/") {
		return false
	}
	parts := strings.Split(strings.TrimPrefix(r.URL.Path, "/admin/sources/"), "/")
	if len(parts) != 2 || (parts[1] != "discover" && parts[1] != "validate") {
		return false
	}
	if r.Method != "POST" {
		fail(w, 405, "method not allowed")
		return true
	}
	index := -1
	for i, source := range s.cfg.Sources {
		if source.ID == parts[0] {
			index = i
		}
	}
	if index < 0 {
		fail(w, 404, "unknown source")
		return true
	}
	ctx, cancel := context.WithTimeout(r.Context(), time.Duration(min(s.cfg.Runtime.RequestTimeoutMS, 60000))*time.Millisecond)
	defer cancel()
	budget := int64(8 << 20)
	if !s.reserve(budget) {
		fail(w, 503, "check memory budget exhausted")
		return true
	}
	defer s.buffered.Add(-budget)
	if parts[1] == "discover" {
		adapter := s.cfg.Sources[index].Adapter
		if adapter != "openai" && adapter != "anthropic" && adapter != "gemini" {
			fail(w, 400, "model discovery is not implemented for this adapter")
			return true
		}
		result, err := s.client(index).Discover(ctx)
		status := "partial"
		if result.Complete {
			status = "complete"
		}
		if err != nil {
			status = "failed"
		}
		recorded := p.evidence.Append(audit.Entry{Revision: p.service.Current().Revision, Kind: "discovery", Resource: parts[0], CheckedAt: result.CheckedAt, Method: result.Method, Status: status, Count: len(result.Models)}) == nil
		if err != nil {
			fail(w, 502, err.Error())
			return true
		}
		reply(w, struct {
			upstream.Discovery
			HistoryRecorded bool `json:"history_recorded"`
		}{result, recorded})
		return true
	}
	var input struct {
		Model    string `json:"model"`
		Protocol string `json:"protocol"`
	}
	if decodeInput(w, r, &input, 4096) != nil {
		fail(w, 400, "model and protocol required")
		return true
	}
	source := s.cfg.Sources[index]
	var model config.Model
	for _, m := range source.Models {
		if m.ID == input.Model {
			model = m
		}
	}
	if model.ID == "" {
		fail(w, 400, "unknown configured model")
		return true
	}
	allowed := false
	for _, v := range model.Protocols {
		if v == input.Protocol {
			allowed = true
		}
	}
	if !allowed {
		fail(w, 400, "protocol not configured for model")
		return true
	}
	body := validationPayload(input.Protocol, model.Upstream)
	queue, cancelQueue := context.WithTimeout(ctx, time.Duration(s.cfg.Runtime.QueueTimeoutMS)*time.Millisecond)
	lease, err := s.Router.Acquire(queue, routing.Query{Model: source.ID + "/" + model.ID, Protocol: input.Protocol, Bytes: int64(len(body)), Probe: true})
	cancelQueue()
	if err != nil {
		fail(w, 503, "source capacity unavailable for validation")
		return true
	}
	status := 502
	defer func() { lease.Release(status, 0) }()
	started := time.Now()
	evidence := ValidationEvidence{Binding: p.sourceBinding(s.cfg, source), Account: source.AccountID, Source: source.ID, Model: model.ID, Protocol: input.Protocol, CheckedAt: started.UTC(), Method: "explicit_stream_generation", Checks: ValidationChecks{Connection: "pending", Auth: "pending", Request: "pending", Streaming: "pending", Completion: "pending"}}
	resp, err := s.client(index).Do(ctx, input.Protocol, model.Upstream, true, body, nil)
	if err != nil {
		evidence.Checks.Connection = "failed"
		evidence.Result.UpstreamError = "transport_or_adapter_error"
	} else {
		evidence.Checks.Connection = "pass"
		defer resp.Body.Close()
		evidence.Result.UpstreamStatus = resp.StatusCode
		if resp.StatusCode < 200 || resp.StatusCode >= 300 {
			status = resp.StatusCode
			evidence.Checks.Auth = map[bool]string{true: "rejected", false: "unknown"}[resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden]
			evidence.Checks.Request = "rejected"
			evidence.Result.UpstreamError = "upstream_rejected"
		} else if !strings.HasPrefix(resp.Header.Get("Content-Type"), "text/event-stream") {
			evidence.Checks.Auth = "pass"
			evidence.Checks.Request = "pass"
			evidence.Checks.Streaming = "failed"
			evidence.Result.UpstreamError = "expected_stream"
		} else {
			evidence.Checks.Auth = "pass"
			evidence.Checks.Request = "pass"
			evidence.Checks.Streaming = "pass"
			evidence.Result, evidence.OutputObserved = observeValidation(ctx, resp.Body, input.Protocol, min(s.cfg.Runtime.MaxOutputBytes, 1<<20), lease)
			evidence.Result.UpstreamStatus = resp.StatusCode
			if evidence.Result.ProtocolComplete {
				evidence.Checks.Completion = "pass"
			} else {
				evidence.Checks.Completion = "failed"
			}
		}
	}
	evidence.Checks.DurationMS = float64(time.Since(started).Microseconds()) / 1000
	if evidence.Checks.DurationMS <= 0 {
		evidence.Checks.DurationMS = 0.001
	}
	evidence.Result.ClientCanceled = ctx.Err() != nil
	evidence.Result = evidence.Result.Redacted()
	evidence.Verified = evidence.Result.Successful() && evidence.OutputObserved
	if evidence.Verified {
		status = 200
	}
	lease.RecordExecution(evidence.Result)
	checkStatus := "failed"
	if evidence.Verified {
		checkStatus = "verified"
	}
	evidence.HistoryRecorded = p.evidence.Append(audit.Entry{Binding: evidence.Binding, Revision: p.service.Current().Revision, Kind: "validation", Resource: source.ID, Model: model.ID, Protocol: input.Protocol, CheckedAt: evidence.CheckedAt, Method: evidence.Method, Status: checkStatus, UpstreamStatus: evidence.Result.UpstreamStatus, ProtocolComplete: evidence.Result.ProtocolComplete, OutputObserved: evidence.OutputObserved}) == nil
	reply(w, evidence)
	return true
}

func validationPayload(proto, model string) []byte {
	v := map[string]any{"model": model, "stream": true}
	switch proto {
	case "chat", "messages":
		v["messages"] = []any{map[string]string{"role": "user", "content": "Reply with OK."}}
		if proto == "messages" {
			v["max_tokens"] = 32
		}
	case "responses":
		v["input"] = "Reply with OK."
	case "gemini":
		v = map[string]any{"contents": []any{map[string]any{"role": "user", "parts": []any{map[string]string{"text": "Reply with OK."}}}}}
	}
	b, _ := json.Marshal(v)
	return b
}

func observeValidation(ctx context.Context, body io.Reader, proto string, limit int64, lease *routing.Lease) (protocol.ExecutionResult, bool) {
	o := protocol.CompletionObserver{Protocol: proto}
	reader := protocol.NewSSEReader(body, int(limit))
	result := protocol.ExecutionResult{}
	total := int64(0)
	for {
		frame, err := reader.Next()
		if err == io.EOF {
			result.TransportOK = true
			break
		}
		if err != nil {
			result.UpstreamError = "truncated_stream"
			break
		}
		total += int64(len(frame))
		if total > limit {
			result.UpstreamError = "output_limit"
			break
		}
		o.Observe(frame)
		if o.Error != "" {
			result.UpstreamError = o.Error
			break
		}
		if o.SawOutput {
			lease.FirstOutput()
		}
		if o.Terminal {
			result.TransportOK = true
			break
		}
	}
	result.ProtocolComplete = o.Complete
	result.ClientCanceled = ctx.Err() != nil
	if !o.Complete && result.UpstreamError == "" {
		result.UpstreamError = "missing_completion"
	}
	return result, o.SawOutput
}
