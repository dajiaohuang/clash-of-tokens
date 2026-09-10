package protocol

import (
	"bytes"
	"encoding/json"
)

type ExecutionResult struct {
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
	Protocol  string
	Complete  bool
	Terminal  bool
	SawOutput bool
	Error     string
	choices   map[int]bool
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
