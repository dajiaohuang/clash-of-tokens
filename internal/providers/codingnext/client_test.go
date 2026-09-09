package codingnext

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"clash-of-tokens/internal/config"
)

func testSource(adapter, base string) config.Source {
	return config.Source{ID: adapter, Adapter: adapter, BaseURL: base, KeyEnv: "COT_CODINGNEXT_TEST_KEY", MaxInflight: 2}
}

func TestFreebuffRequestLifecycle(t *testing.T) {
	t.Setenv("COT_CODINGNEXT_TEST_KEY", "free-secret")
	var started atomic.Int32
	finished := make(chan struct{}, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer free-secret" {
			t.Errorf("authorization=%q", r.Header.Get("Authorization"))
		}
		switch r.URL.Path {
		case "/freebuff/session":
			started.Add(1)
			if r.Header.Get("x-freebuff-model") != "deepseek/deepseek-v4-flash" {
				t.Errorf("freebuff model=%q", r.Header.Get("x-freebuff-model"))
			}
			_, _ = io.WriteString(w, `{"instanceId":"instance-1"}`)
		case "/agent-runs":
			body, _ := io.ReadAll(r.Body)
			if bytes.Contains(body, []byte(`"action":"START"`)) {
				_, _ = io.WriteString(w, `{"runId":"run-1"}`)
			} else {
				finished <- struct{}{}
			}
		case "/chat/completions":
			var body map[string]any
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				t.Error(err)
			}
			if body["model"] != "deepseek/deepseek-v4-flash" || body["stream"] != false {
				t.Errorf("Freebuff payload=%v", body)
			}
			messages := body["messages"].([]any)
			if messages[0].(map[string]any)["role"] != "system" {
				t.Errorf("Buffy system prompt missing: %v", messages)
			}
			_, _ = io.WriteString(w, `{"id":"cmpl-1","object":"chat.completion","choices":[{"message":{"role":"assistant","content":"ok"},"finish_reason":"stop"}]}`)
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	client := New(testSource(AdapterFreebuff, server.URL))
	defer client.Close()
	response, err := client.Do(context.Background(), "chat", "deepseek/deepseek-v4-flash", false, []byte(`{"messages":[{"role":"user","content":"hi"}],"tools":[{"type":"function"}]}`), nil)
	if err != nil {
		t.Fatal(err)
	}
	data, err := io.ReadAll(response.Body)
	response.Body.Close()
	if err != nil || !bytes.Contains(data, []byte(`"content":"ok"`)) {
		t.Fatalf("response=%s err=%v", data, err)
	}
	if started.Load() != 1 {
		t.Fatalf("session calls=%d", started.Load())
	}
	select {
	case <-finished:
	case <-time.After(time.Second):
		t.Fatal("Freebuff FINISH was not sent")
	}
}

func TestCodeBuddyForcesStreamAndAggregates(t *testing.T) {
	t.Setenv("COT_CODINGNEXT_TEST_KEY", "buddy-secret")
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v2/chat/completions" {
			t.Fatalf("path=%s", r.URL.Path)
		}
		for key, want := range map[string]string{
			"Authorization": "Bearer buddy-secret", "User-Agent": "CLI/2.108.1 CodeBuddy/2.108.1",
			"X-Product": "SaaS", "X-IDE-Type": "CLI", "X-IDE-Name": "CLI",
			"x-requested-with": "XMLHttpRequest", "x-codebuddy-request": "1",
		} {
			if got := r.Header.Get(key); got != want {
				t.Errorf("%s=%q want %q", key, got, want)
			}
		}
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Error(err)
		}
		if body["stream"] != true || body["reasoning_summary"] != "auto" {
			t.Errorf("forced fields=%v", body)
		}
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w, "data: {\"id\":\"m\",\"choices\":[{\"delta\":{\"role\":\"assistant\",\"content\":\"hello\"}}]}\n\n")
		_, _ = io.WriteString(w, "data: {\"choices\":[{\"delta\":{\"content\":\" world\"},\"finish_reason\":\"stop\"}]}\n\n")
		_, _ = io.WriteString(w, "data: [DONE]\n\n")
	}))
	defer server.Close()
	client := New(testSource(AdapterCodeBuddy, server.URL))
	defer client.Close()
	response, err := client.Do(context.Background(), "chat", "glm-5.2", false, []byte(`{"messages":[{"role":"user","content":"hi"}],"reasoning_effort":"high","tools":[{"type":"function","function":{"name":"x"}}]}`), nil)
	if err != nil {
		t.Fatal(err)
	}
	data, err := io.ReadAll(response.Body)
	response.Body.Close()
	if err != nil || !bytes.Contains(data, []byte(`"content":"hello world"`)) {
		t.Fatalf("aggregated=%s err=%v", data, err)
	}
}

func TestCodeBuddyStreamingRequiresDoneMarker(t *testing.T) {
	t.Setenv("COT_CODINGNEXT_TEST_KEY", "buddy-secret")
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w, "data: {\"choices\":[{\"delta\":{\"content\":\"partial\"}}]}\n\n")
	}))
	defer server.Close()
	client := New(testSource(AdapterCodeBuddy, server.URL))
	defer client.Close()
	response, err := client.Do(context.Background(), "chat", "glm-5.2", true, []byte(`{"messages":[{"role":"user","content":"hi"}]}`), nil)
	if err != nil {
		t.Fatal(err)
	}
	_, err = io.ReadAll(response.Body)
	response.Body.Close()
	if !errors.Is(err, ErrTruncated) {
		t.Fatalf("stream error=%v", err)
	}
}

func TestZedEnvelopeAndStreamCompletion(t *testing.T) {
	t.Setenv("COT_CODINGNEXT_TEST_KEY", "zed-llm-token")
	var thread string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer zed-llm-token" {
			t.Errorf("authorization=%q", r.Header.Get("Authorization"))
		}
		var envelope map[string]any
		if err := json.NewDecoder(r.Body).Decode(&envelope); err != nil {
			t.Fatal(err)
		}
		if envelope["provider"] != "anthropic" {
			t.Errorf("provider=%v", envelope["provider"])
		}
		current, _ := envelope["thread_id"].(string)
		if thread != "" && thread != current {
			t.Errorf("thread changed from %q to %q", thread, current)
		}
		thread = current
		w.Header().Set("Content-Type", "application/x-ndjson")
		_, _ = io.WriteString(w, `{"event":{"type":"message_start"}}`+"\n")
		_, _ = io.WriteString(w, `{"event":{"type":"content_block_delta","delta":{"text":"zed"}}}`+"\n")
		_, _ = io.WriteString(w, `{"status":"stream_ended"}`+"\n")
	}))
	defer server.Close()
	client := New(testSource(AdapterZedHosted, server.URL))
	defer client.Close()
	header := http.Header{}
	header.Set("X-COT-Session", "stable-session")
	for range 2 {
		response, err := client.Do(context.Background(), "chat", "claude-sonnet-5", false, []byte(`{"messages":[{"role":"user","content":"hi"}]}`), header)
		if err != nil {
			t.Fatal(err)
		}
		data, err := io.ReadAll(response.Body)
		response.Body.Close()
		if err != nil || !bytes.Contains(data, []byte(`"content":"zed"`)) {
			t.Fatalf("zed response=%s err=%v", data, err)
		}
	}
}

func TestCodingNextRejectsUnsupportedAndTruncated(t *testing.T) {
	t.Setenv("COT_CODINGNEXT_TEST_KEY", "secret")
	client := New(testSource(AdapterZedHosted, "http://127.0.0.1:1"))
	defer client.Close()
	_, err := client.Do(context.Background(), "responses", "gpt-5.5", false, []byte(`{"messages":[]}`), nil)
	if !errors.Is(err, ErrUnsupported) {
		t.Fatalf("protocol err=%v", err)
	}
	_, err = zedProviderRequest(zedProviderOpenAI, "gpt-5.5", map[string]any{
		"messages": []any{map[string]any{"role": "user", "content": "hi"}},
		"tools":    []any{map[string]any{"type": "function"}},
	}, false)
	if !errors.Is(err, ErrUnsupported) {
		t.Fatalf("tool err=%v", err)
	}
	_, err = streamToOpenAIJSON(context.Background(), strings.NewReader("data: {\"choices\":[]}\n\n"), "m", maxResponseBytes)
	if !errors.Is(err, ErrTruncated) {
		t.Fatalf("truncated err=%v", err)
	}
}
