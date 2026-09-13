package upstream

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"clash-of-tokens/internal/config"
)

func TestFactoryCredentialReferenceOnlyClaude(t *testing.T) {
	var calls atomic.Int32
	var resolutions atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		if r.Header.Get("Cookie") != "sessionKey=synthetic-session" {
			t.Error("resolved session was not used")
			w.WriteHeader(401)
			return
		}
		switch r.URL.Path {
		case "/api/organizations/org-fixture/chat_conversations":
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(201)
			io.WriteString(w, `{"uuid":"conversation-fixture"}`)
		case "/api/organizations/org-fixture/chat_conversations/conversation-fixture/completion":
			w.Header().Set("Content-Type", "text/event-stream")
			io.WriteString(w, "data: {\"type\":\"content_block_delta\",\"delta\":{\"type\":\"text_delta\",\"text\":\"OK\"}}\n\nevent: message_stop\ndata: {\"type\":\"message_stop\"}\n\n")
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	source := config.Source{Adapter: "claude-web", BaseURL: server.URL, CredentialRef: "cred://fixture", MaxInflight: 1,
		CredentialResolverContext: func(ctx context.Context, ref string) (string, error) {
			resolutions.Add(1)
			if ref != "cred://fixture" {
				t.Error("unexpected reference")
			}
			return `{"sessionKey":"synthetic-session","org_id":"org-fixture"}`, nil
		}}
	client := New(source, config.Browser{})
	defer client.Close()
	response, err := client.Do(context.Background(), "chat", "claude-sonnet", false, []byte(`{"messages":[{"role":"user","content":"hello"}]}`), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	data, err := io.ReadAll(response.Body)
	if err != nil || !strings.Contains(string(data), "OK") || calls.Load() != 2 {
		t.Fatalf("factory invocation failed: calls=%d err=%v", calls.Load(), err)
	}
	if resolutions.Load() != 1 {
		t.Fatal("credential was not resolved exactly once per request")
	}
	source.CredentialResolverContext = nil
	source.CredentialResolver = func(string) string { return "" }
	source.KeyEnv = "COT_FACTORY_LEGACY"
	t.Setenv(source.KeyEnv, "must-not-fallback")
	missing := New(source, config.Browser{})
	defer missing.Close()
	if _, err := missing.Do(context.Background(), "chat", "claude-sonnet", false, []byte(`{"messages":[{"role":"user","content":"hello"}]}`), nil); err == nil {
		t.Fatal("missing reference accepted")
	}
	if calls.Load() != 2 {
		t.Fatal("unresolved credential reached upstream")
	}
}

func TestFactoryCredentialResolutionCancelsBeforeSubmission(t *testing.T) {
	for _, adapter := range []string{"openai", "claude-web"} {
		t.Run(adapter, func(t *testing.T) {
			var calls atomic.Int32
			up := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { calls.Add(1) }))
			defer up.Close()
			source := config.Source{Adapter: adapter, BaseURL: up.URL, MaxInflight: 1, CredentialRef: "cred://slow", CredentialResolverContext: func(ctx context.Context, _ string) (string, error) { <-ctx.Done(); return "", ctx.Err() }}
			client := New(source)
			defer client.Close()
			ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
			defer cancel()
			started := time.Now()
			if _, err := client.Do(ctx, "chat", "m", false, []byte(`{"messages":[{"role":"user","content":"hi"}]}`), nil); err == nil {
				t.Fatal("canceled resolver succeeded")
			}
			if time.Since(started) > 500*time.Millisecond || calls.Load() != 0 {
				t.Fatal("credential cancellation did not prevent submission")
			}
		})
	}
}
