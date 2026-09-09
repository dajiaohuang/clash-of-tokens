package codingmore

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"clash-of-tokens/internal/config"
)

func TestAugmentLineBoundBeforeAllocation(t *testing.T) {
	input := strings.NewReader(strings.Repeat("x", 2*maxAugmentLine))
	_, err := readAugmentLine(bufio.NewReaderSize(input, 8192))
	if err == nil || input.Len() < maxAugmentLine-8192 {
		t.Fatal("oversized line was consumed beyond the bounded prefix")
	}
}

func augmentTestSource(base string) config.Source {
	return config.Source{ID: "augment-test", Adapter: AdapterAugment, BaseURL: base, KeyEnv: "COT_AUGMENT_TEST_KEY", MaxInflight: 1}
}

func TestAugmentChatAndStream(t *testing.T) {
	t.Setenv("COT_AUGMENT_TEST_KEY", "augment-secret")
	var requests int
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		if r.URL.Path != "/chat-stream" {
			t.Errorf("path=%s", r.URL.Path)
		}
		if r.Header.Get("Authorization") != "Bearer augment-secret" || r.Header.Get("x-api-version") != "2" {
			t.Errorf("auth/version=%q/%q", r.Header.Get("Authorization"), r.Header.Get("x-api-version"))
		}
		if r.Header.Get("Cookie") != "" || r.Header.Get("X-Internal-Secret") != "" {
			t.Error("caller credential headers were forwarded")
		}
		if r.Header.Get("x-request-id") == "" || r.Header.Get("x-request-session-id") == "" {
			t.Error("Augment request identifiers are missing")
		}
		var payload map[string]any
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			t.Errorf("request JSON: %v", err)
		}
		if payload["mode"] != "CHAT" || payload["message"] != "current" {
			t.Errorf("product mode/message=%v/%v", payload["mode"], payload["message"])
		}
		history, ok := payload["chat_history"].([]any)
		if !ok || len(history) != 1 {
			t.Errorf("chat history=%#v", payload["chat_history"])
		}
		w.Header().Set("Content-Type", "application/x-ndjson")
		_, _ = io.WriteString(w, `{"text":"hello","done":false}`+"\n"+`{"text":" world","done":true}`+"\n")
	}))
	defer server.Close()

	c := New(augmentTestSource(server.URL))
	defer c.Close()
	body := []byte(`{"model":"default","messages":[{"role":"user","content":"past"},{"role":"assistant","content":"answer"},{"role":"user","content":"current"}]}`)
	resp, err := c.Do(context.Background(), "chat", "default", true, body, http.Header{
		"Cookie": []string{"caller-cookie"}, "X-Internal-Secret": []string{"caller-secret"}, "X-COT-Session": []string{"session-1"},
	})
	if err != nil {
		t.Fatal(err)
	}
	stream, err := io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	if err != nil {
		t.Fatalf("stream=%q err=%v", stream, err)
	}
	if !bytes.Contains(stream, []byte(`"content":"hello"`)) || !bytes.Contains(stream, []byte(`"content":" world"`)) || !bytes.Contains(stream, []byte(`"finish_reason":"stop"`)) || !bytes.Contains(stream, []byte("data: [DONE]")) {
		t.Fatalf("stream=%s", stream)
	}

	resp, err = c.Do(context.Background(), "chat", "default", false, body, http.Header{"X-COT-Session": []string{"session-1"}})
	if err != nil {
		t.Fatal(err)
	}
	result, err := io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(result, []byte(`"object":"chat.completion"`)) || !bytes.Contains(result, []byte(`"content":"hello world"`)) {
		t.Fatalf("non-stream=%s", result)
	}
	if requests != 2 {
		t.Fatalf("requests=%d", requests)
	}
}

func TestAugmentRejectsUnsupportedSemantics(t *testing.T) {
	t.Setenv("COT_AUGMENT_TEST_KEY", "augment-secret")
	server := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { t.Error("request should not be sent") }))
	defer server.Close()
	c := New(augmentTestSource(server.URL))
	defer c.Close()
	for name, body := range map[string]string{
		"system role": `{"messages":[{"role":"system","content":"rules"},{"role":"user","content":"hi"}]}`,
		"tool field":  `{"messages":[{"role":"user","content":"hi","tool_calls":[]}]}`,
		"agent mode":  `{"messages":[{"role":"user","content":"hi"}]}`,
	} {
		t.Run(name, func(t *testing.T) {
			model := "default"
			if name == "agent mode" {
				model = "claude-agent"
			}
			_, err := c.Do(context.Background(), "chat", model, false, []byte(body), nil)
			if err == nil || !errors.Is(err, ErrUnsupported) {
				t.Fatalf("err=%v", err)
			}
		})
	}
}

func TestAugmentRequiresCompletionMarker(t *testing.T) {
	_, err := collectAugment(context.Background(), io.NopCloser(strings.NewReader(`{"text":"partial","done":false}`+"\n")), "m")
	if !errors.Is(err, ErrTruncated) {
		t.Fatalf("err=%v", err)
	}
	_, err = collectAugment(context.Background(), io.NopCloser(strings.NewReader(`{"text":"blocked: Request blocked. Please reach out to support@augmentcode.com if you think this was a mistake.","done":true}`+"\n")), "m")
	if !errors.Is(err, errAugmentBlocked) {
		t.Fatalf("blocked err=%v", err)
	}
}
