package api

import (
	"io"
	"net/http"
	"strings"
	"testing"
	"time"
)

func TestTruncatedStreamIsNotSuccessful(t *testing.T) {
	s, g := setup(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w, "data: {\"choices\":[{\"delta\":{\"content\":\"partial\"}}]}\n\n")
	})
	res := request(t, g.URL, "/v1/chat/completions", "{\"model\":\"mock/model\",\"stream\":true}", testKey)
	_, err := io.ReadAll(res.Body)
	res.Body.Close()
	if err == nil {
		t.Fatal("truncated stream looked complete")
	}
	deadline := time.Now().Add(time.Second)
	for s.Router.Status()[0].Active > 0 && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	status := s.Router.Status()[0]
	if status.Completed != 0 || status.Failures != 1 || status.LastHTTPStatus != 502 {
		t.Fatal(status)
	}
}

func TestStreamingUsageRecordsDeclaredCounts(t *testing.T) {
	s, g := setup(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w, "data: {\"choices\":[{\"index\":0,\"delta\":{\"content\":\"OK\"}}]}\n\ndata: {\"choices\":[],\"usage\":{\"prompt_tokens\":8,\"completion_tokens\":2,\"total_tokens\":10}}\n\ndata: [DONE]\n\n")
	})
	res := request(t, g.URL, "/v1/chat/completions", `{"model":"mock/model","stream":true}`, testKey)
	_, _ = io.ReadAll(res.Body)
	res.Body.Close()
	state := s.Router.Status()[0]
	if res.StatusCode != 200 || state.InputTokens != 8 || state.OutputTokens != 2 || state.TotalTokens != 10 || state.LastExecution == nil || !state.LastExecution.UsageKnown {
		t.Fatalf("stream usage was not retained: status=%d state=%+v", res.StatusCode, state)
	}
}

func TestSSEErrorDoesNotLeakUpstreamMessage(t *testing.T) {
	_, g := setup(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w, "data: {\"error\":{\"message\":\"private-credential-value\"}}\n\n")
	})
	res := request(t, g.URL, "/v1/chat/completions", "{\"model\":\"mock/model\",\"stream\":true}", testKey)
	b, _ := io.ReadAll(res.Body)
	res.Body.Close()
	if res.StatusCode != 502 || strings.Contains(string(b), "private-credential-value") {
		t.Fatal(res.StatusCode, string(b))
	}
}
