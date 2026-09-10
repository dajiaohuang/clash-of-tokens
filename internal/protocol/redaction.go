package protocol

// Redacted retains only gateway-owned error categories for runtime observation.
// Unknown nonempty errors remain failures without retaining their text.
func (r ExecutionResult) Redacted() ExecutionResult {
	switch r.UpstreamError {
	case "", "upstream_error", "unexpected_completion_marker", "invalid_sse_json",
		"upstream_incomplete", "upstream_blocked", "unknown_stream_protocol",
		"invalid_choice_index", "missing_completion", "truncated_stream",
		"output_limit", "transport_or_adapter_error", "upstream_rejected", "expected_stream":
	default:
		r.UpstreamError = "upstream_error"
	}
	return r
}
