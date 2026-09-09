package majorweb

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"clash-of-tokens/internal/config"
)

func TestYouContract(t *testing.T) {
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		if r.Method != "GET" || r.URL.Path != "/api/streamingSearch" || q.Get("q") != "hello & world" || q.Get("selectedAiModel") != "claude_3.5_sonnet" || q.Get("selectedChatMode") != "custom" || q.Get("chatId") == "" || r.Header.Get("Cookie") != "afUserId=own" {
			t.Error("wrong You request")
		}
		w.Header().Set("Content-Type", "text/event-stream")
		io.WriteString(w, "event: youChatToken\ndata: {\"youChatToken\":\"hello \"}\n\nevent: youChatUpdate\ndata: {\"t\":\"world\"}\n\n")
	}))
	defer s.Close()
	t.Setenv("COT_TEST_YOU", "afUserId=own")
	c := New(config.Source{Adapter: "you", BaseURL: s.URL, KeyEnv: "COT_TEST_YOU"})
	defer c.Close()
	r, err := c.Do(context.Background(), "chat", "claude-3.5-sonnet", true, []byte(`{"messages":[{"role":"user","content":"hello & world"}]}`), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer r.Body.Close()
	b, _ := io.ReadAll(r.Body)
	if !strings.Contains(string(b), "hello world") || !strings.Contains(string(b), "[DONE]") {
		t.Fatal(string(b))
	}
}

type youBrokenReader struct{ sent bool }

func (r *youBrokenReader) Read(p []byte) (int, error) {
	if r.sent {
		return 0, io.ErrUnexpectedEOF
	}
	r.sent = true
	return copy(p, "event: youChatToken\ndata: {\"youChatToken\":\"partial\"}\n\n"), nil
}

func TestYouRejectsBadResponses(t *testing.T) {
	if _, err := readYou(&youBrokenReader{}); err == nil {
		t.Fatal("accepted transport truncation")
	}
	for _, raw := range []string{
		"event: error\ndata: secret\n\n",
		"event: youChatToken\ndata: {\"youChatToken\":3}\n\n",
		"event: youChatToken\ndata: nope\n\n",
		"event: irrelevant\ndata: {}\n\n",
	} {
		if _, err := readYou(strings.NewReader(raw)); err == nil {
			t.Fatal("accepted invalid response")
		} else if strings.Contains(err.Error(), "secret") {
			t.Fatal("leaked upstream error")
		}
	}
	_, err := readYou(strings.NewReader("event: youChatToken\ndata: {\"youChatToken\":\"#### You've hit your free quota for the Model Agent.\"}\n\n"))
	var status *HTTPError
	if !errors.As(err, &status) || status.Status != 429 {
		t.Fatalf("%v", err)
	}
}
