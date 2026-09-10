package protocol

import (
	"bytes"
	"encoding/json"
)

type ExecutionResult struct {
	InputTokens      int64  `json:"input_tokens,omitempty"`
	OutputTokens     int64  `json:"output_tokens,omitempty"`
	TotalTokens      int64  `json:"total_tokens,omitempty"`
	UsageKnown       bool   `json:"usage_known"`
	UpstreamStatus   int    `json:"upstream_status"`
	TransportOK      bool   `json:"transport_ok"`
	ProtocolComplete bool   `json:"protocol_complete"`
	UpstreamError    string `json:"upstream_error,omitempty"`
	ClientCanceled   bool   `json:"client_canceled"`
}

func (r ExecutionResult) Successful() bool {
	return r.TransportOK && r.ProtocolComplete && r.UpstreamError == "" && !r.ClientCanceled
}

type CompletionObserver struct {
	InputTokens  int64
	OutputTokens int64
	TotalTokens  int64
	UsageSeen    bool
	Protocol     string
	Complete     bool
	Terminal     bool
	SawOutput    bool
	Error        string
	choices      map[int]bool
	inputSeen    bool
	outputSeen   bool
	totalSeen    bool
}

func (o *CompletionObserver) Observe(frame []byte) {
	event := ""
	for _, line := range bytes.Split(frame, []byte{'\n'}) {
		if bytes.HasPrefix(line, []byte("event:")) {
			event = string(bytes.TrimSpace(line[6:]))
		}
	}
	data := bytes.TrimSpace(SSEData(frame))
	if event == "error" {
		o.Error = "upstream_error"
		return
	}
	if len(data) == 0 {
		return
	}
	if bytes.Equal(data, []byte("[DONE]")) {
		if o.Protocol != "chat" {
			o.Error = "unexpected_completion_marker"
			return
		}
		o.Complete = true
		o.Terminal = true
		return
	}
	var v struct {
		Type    string          `json:"type"`
		Error   json.RawMessage `json:"error"`
		Delta   json.RawMessage `json:"delta"`
		Choices []struct {
			Index        int     `json:"index"`
			FinishReason *string `json:"finish_reason"`
			Delta        struct {
				Content      string            `json:"content"`
				Refusal      string            `json:"refusal"`
				ToolCalls    []json.RawMessage `json:"tool_calls"`
				FunctionCall json.RawMessage   `json:"function_call"`
			} `json:"delta"`
		} `json:"choices"`
		Candidates []struct {
			Index        int    `json:"index"`
			FinishReason string `json:"finishReason"`
			Content      struct {
				Parts []struct {
					Text         string          `json:"text"`
					FunctionCall json.RawMessage `json:"functionCall"`
				} `json:"parts"`
			} `json:"content"`
		} `json:"candidates"`
		PromptFeedback struct {
			BlockReason string `json:"blockReason"`
		} `json:"promptFeedback"`
	}
	if json.Unmarshal(data, &v) != nil {
		o.Error = "invalid_sse_json"
		return
	}
	if len(v.Error) > 0 && !bytes.Equal(v.Error, []byte("null")) {
		o.Error = "upstream_error"
		return
	}
	if v.Type != "" {
		event = v.Type
	}
	o.observeUsage(data)
	switch event {
	case "error", "response.failed", "response.incomplete":
		o.Error = "upstream_incomplete"
		return
	}
	switch o.Protocol {
	case "chat":
		for _, choice := range v.Choices {
			o.choice(choice.Index, choice.FinishReason != nil && *choice.FinishReason != "")
			if choice.Delta.Content != "" || choice.Delta.Refusal != "" || len(choice.Delta.ToolCalls) > 0 || len(choice.Delta.FunctionCall) > 0 {
				o.SawOutput = true
			}
		}
	case "messages":
		if event == "message_stop" {
			o.Complete = true
			o.Terminal = true
		}
		if event == "content_block_delta" {
			var delta struct {
				Text        string `json:"text"`
				PartialJSON string `json:"partial_json"`
			}
			if json.Unmarshal(v.Delta, &delta) == nil && (delta.Text != "" || delta.PartialJSON != "") {
				o.SawOutput = true
			}
		}
	case "responses":
		if event == "response.completed" {
			o.Complete = true
			o.Terminal = true
		}
		if event == "response.output_text.delta" || event == "response.function_call_arguments.delta" || event == "response.refusal.delta" {
			var delta string
			if json.Unmarshal(v.Delta, &delta) == nil && delta != "" {
				o.SawOutput = true
			}
		}
	case "gemini":
		if v.PromptFeedback.BlockReason != "" {
			o.Error = "upstream_blocked"
			return
		}
		for _, candidate := range v.Candidates {
			o.choice(candidate.Index, candidate.FinishReason != "")
			for _, part := range candidate.Content.Parts {
				if part.Text != "" || len(part.FunctionCall) > 0 {
					o.SawOutput = true
				}
			}
		}
	default:
		o.Error = "unknown_stream_protocol"
	}
}

// ObserveJSON records declared usage from a complete non-streaming JSON
// response. Completion state is deliberately left to the HTTP transport,
// because non-streaming adapters have no common completion marker.
func (o *CompletionObserver) ObserveJSON(data []byte) {
	o.observeUsage(bytes.TrimSpace(data))
}

type usageValue struct {
	input, output, total          int64
	inputSet, outputSet, totalSet bool
}

func (o *CompletionObserver) observeUsage(data []byte) {
	var envelope map[string]json.RawMessage
	if json.Unmarshal(data, &envelope) != nil {
		return
	}
	for _, key := range []string{"usage", "usageMetadata", "usage_metadata"} {
		if raw, ok := envelope[key]; ok {
			o.mergeUsage(parseUsageValue(raw))
		}
	}
	// Anthropic puts input usage in message_start.message. A few adapters also
	// nest usage under the final response object, so inspect that envelope.
	if raw, ok := envelope["message"]; ok {
		var nested map[string]json.RawMessage
		if json.Unmarshal(raw, &nested) == nil {
			for _, key := range []string{"usage", "usageMetadata", "usage_metadata"} {
				if value, exists := nested[key]; exists {
					o.mergeUsage(parseUsageValue(value))
				}
			}
		}
	}
}

func parseUsageValue(raw json.RawMessage) usageValue {
	var fields map[string]json.RawMessage
	if len(raw) == 0 || bytes.Equal(bytes.TrimSpace(raw), []byte("null")) || json.Unmarshal(raw, &fields) != nil {
		return usageValue{}
	}
	value := usageValue{}
	value.input, value.inputSet = usageField(fields, "prompt_tokens", "promptTokens", "input_tokens", "inputTokens", "promptTokenCount")
	value.output, value.outputSet = usageField(fields, "completion_tokens", "completionTokens", "output_tokens", "outputTokens", "candidatesTokenCount")
	value.total, value.totalSet = usageField(fields, "total_tokens", "totalTokens", "totalTokenCount")
	return value
}

func usageField(fields map[string]json.RawMessage, names ...string) (int64, bool) {
	for _, name := range names {
		raw, ok := fields[name]
		if !ok {
			continue
		}
		var value int64
		if json.Unmarshal(raw, &value) == nil && value >= 0 {
			return value, true
		}
	}
	return 0, false
}

func (o *CompletionObserver) mergeUsage(value usageValue) {
	if !value.inputSet && !value.outputSet && !value.totalSet {
		return
	}
	o.UsageSeen = true
	if value.inputSet {
		o.InputTokens, o.inputSeen = value.input, true
	}
	if value.outputSet {
		o.OutputTokens, o.outputSeen = value.output, true
	}
	if value.totalSet {
		o.TotalTokens, o.totalSeen = value.total, true
	}
	if !o.totalSeen && o.inputSeen && o.outputSeen {
		o.TotalTokens = o.InputTokens + o.OutputTokens
	}
}
func (o *CompletionObserver) choice(index int, finished bool) {
	if index < 0 || index > 1024 {
		o.Error = "invalid_choice_index"
		return
	}
	if o.choices == nil {
		o.choices = map[int]bool{}
	}
	o.choices[index] = o.choices[index] || finished
	o.Complete = len(o.choices) > 0
	for _, done := range o.choices {
		if !done {
			o.Complete = false
		}
	}
}
