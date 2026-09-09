package webhttp

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"clash-of-tokens/internal/config"
)

func TestAIFreeForever(t *testing.T) {
	t.Setenv("COT_AIFF_TEST", "session=test")
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/chat" || r.Header.Get("Cookie") != "session=test" {
			t.Error("unexpected endpoint or auth")
		}
		var v struct {
			Model    string `json:"modelId"`
			Trigger  string `json:"trigger"`
			Messages []struct {
				Role  string `json:"role"`
				Parts []struct {
					Text string `json:"text"`
				} `json:"parts"`
			} `json:"messages"`
		}
		if json.NewDecoder(r.Body).Decode(&v) != nil || v.Model != "model-test" || v.Trigger != "submit-message" || len(v.Messages) != 3 {
			t.Error("bad payload")
		}
		if len(v.Messages) == 3 && (v.Messages[1].Role != "assistant" || v.Messages[2].Parts[0].Text != "next") {
			t.Error("lost history")
		}
		w.Header().Set("Content-Type", "text/event-stream")
		io.WriteString(w, "data: {\"type\":\"text-delta\",\"delta\":\" hello \"}\n\ndata: {\"type\":\"finish\"}\n\n")
	}))
	defer srv.Close()
	c := New(config.Source{Adapter: "aifreeforever", BaseURL: srv.URL, KeyEnv: "COT_AIFF_TEST"})
	defer c.Close()
	r, err := c.Do(context.Background(), "chat", "model-test", false, []byte(`{"messages":[{"role":"user","content":"first"},{"role":"assistant","content":"answer"},{"role":"user","content":"next"}]}`), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer r.Body.Close()
	b, err := io.ReadAll(r.Body)
	if err != nil || !strings.Contains(string(b), `"content":" hello "`) {
		t.Fatalf("bad output %s %v", b, err)
	}
}

func TestAIFreeForeverTruncatedAndUnsupported(t *testing.T) {
	for _, data := range []string{"", "data: {\"type\":\"text-delta\",\"delta\":\"partial\"}\n\n", "data: {\"type\":\"tool-input-start\"}\n\n", "data: {\"type\":\"error\",\"errorText\":\"secret\"}\n\n"} {
		_, err := decode(strings.NewReader(data), "aifreeforever", func(string) error { return nil })
		if err == nil {
			t.Fatal("invalid stream accepted")
		}
		if strings.Contains(err.Error(), "secret") {
			t.Fatal("raw upstream error leaked")
		}
	}
}
