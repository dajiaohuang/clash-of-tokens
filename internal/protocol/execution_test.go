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
