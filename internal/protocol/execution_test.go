package protocol

import "testing"

func TestCompletionMarkers(t *testing.T) {
	for _, tc := range []struct {
		protocol, wire             string
		complete, terminal, output bool
	}{
		{"chat", "data: [DONE]\n\n", true, true, false},
		{"chat", "data: {\"choices\":[{\"index\":0,\"delta\":{\"content\":\"hi\"}}]}\n\n", false, false, true},
		{"chat", "data: {\"choices\":[{\"index\":0,\"finish_reason\":\"stop\"}]}\n\n", true, false, false},
		{"messages", "event: message_stop\ndata: {\"type\":\"message_stop\"}\n\n", true, true, false},
		{"responses", "data: {\"type\":\"response.completed\"}\n\n", true, true, false},
		{"gemini", "data: {\"candidates\":[{\"index\":0,\"finishReason\":\"STOP\",\"content\":{\"parts\":[{\"text\":\"hello\"}]}}]}\n\n", true, false, true},
	} {
		t.Run(tc.protocol+tc.wire, func(t *testing.T) {
			observer := CompletionObserver{Protocol: tc.protocol}
			observer.Observe([]byte(tc.wire))
			if observer.Complete != tc.complete || observer.Terminal != tc.terminal || observer.SawOutput != tc.output || observer.Error != "" {
				t.Fatalf("%+v", observer)
			}
		})
	}
}
func TestErrorsAndIncompleteChoices(t *testing.T) {
	for _, wire := range []string{
		"event: error\ndata: {\"message\":\"secret\"}\n\n",
		"data: {\"error\":{\"message\":\"secret\"}}\n\n",
		"data: {\"type\":\"response.failed\"}\n\n",
		"data: invalid-json\n\n",
	} {
		o := CompletionObserver{Protocol: "chat"}
		o.Observe([]byte(wire))
		if o.Error == "" || o.Complete {
			t.Fatal("error accepted", wire)
		}
	}
	o := CompletionObserver{Protocol: "chat"}
	o.Observe([]byte("data: {\"choices\":[{\"index\":0},{\"index\":1}]}\n\n"))
	o.Observe([]byte("data: {\"choices\":[{\"index\":0,\"finish_reason\":\"stop\"}]}\n\n"))
	if o.Complete {
		t.Fatal("unfinished choice accepted")
	}
	o.Observe([]byte("data: {\"choices\":[{\"index\":1,\"finish_reason\":\"length\"}]}\n\n"))
	if !o.Complete {
		t.Fatal("all choices complete")
	}
}

func TestCompletionObserverUsageVariantsAndAccumulation(t *testing.T) {
	tests := []struct {
		name       string
		protocol   string
		frames     []string
		input      int64
		output     int64
		total      int64
		usageKnown bool
	}{
		{name: "openai", protocol: "chat", frames: []string{"data: {\"choices\":[{\"index\":0,\"delta\":{\"content\":\"hi\"}}]}\n\n", "data: {\"choices\":[],\"usage\":{\"prompt_tokens\":7,\"completion_tokens\":3,\"total_tokens\":10}}\n\n"}, input: 7, output: 3, total: 10, usageKnown: true},
		{name: "anthropic split", protocol: "messages", frames: []string{"event: message_start\ndata: {\"type\":\"message_start\",\"message\":{\"usage\":{\"input_tokens\":11}}}\n\n", "event: message_delta\ndata: {\"type\":\"message_delta\",\"usage\":{\"output_tokens\":5}}\n\n"}, input: 11, output: 5, total: 16, usageKnown: true},
		{name: "gemini", protocol: "gemini", frames: []string{"data: {\"usageMetadata\":{\"promptTokenCount\":4,\"candidatesTokenCount\":6,\"totalTokenCount\":10}}\n\n"}, input: 4, output: 6, total: 10, usageKnown: true},
		{name: "camel case json", protocol: "responses", frames: []string{"data: {\"type\":\"response.output_text.delta\",\"delta\":\"ok\",\"usage\":{\"inputTokens\":2,\"outputTokens\":8}}\n\n"}, input: 2, output: 8, total: 10, usageKnown: true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			o := CompletionObserver{Protocol: test.protocol}
			for _, frame := range test.frames {
				o.Observe([]byte(frame))
			}
			if o.InputTokens != test.input || o.OutputTokens != test.output || o.TotalTokens != test.total || o.UsageSeen != test.usageKnown {
				t.Fatalf("usage=%+v", o)
			}
		})
	}
	var o CompletionObserver
	o.ObserveJSON([]byte(`{"usage":{"prompt_tokens":0,"completion_tokens":0,"total_tokens":0}}`))
	if !o.UsageSeen || o.InputTokens != 0 || o.OutputTokens != 0 || o.TotalTokens != 0 {
		t.Fatalf("explicit zero usage should remain known: %+v", o)
	}
}
