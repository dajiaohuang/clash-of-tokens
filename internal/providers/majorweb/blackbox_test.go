package majorweb

import (
	"clash-of-tokens/internal/config"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestBlackboxAccountContract(t *testing.T) {
	t.Setenv("BLACKBOX_TEST", `{"cookie":"next-auth.session-token=own","validated":"actual-frontend-value"}`)
	calls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if r.Header.Get("Cookie") != "next-auth.session-token=own" || r.Header.Get("Authorization") != "" {
			t.Error("credentials crossed boundary")
		}
		switch r.URL.Path {
		case "/api/auth/session":
			io.WriteString(w, `{"user":{"email":"owner@example.test"}}`)
		case "/api/check-subscription":
			var p map[string]any
			json.NewDecoder(r.Body).Decode(&p)
			if p["email"] != "owner@example.test" {
				t.Error("wrong account")
			}
			io.WriteString(w, `{"hasActiveSubscription":false}`)
		case "/api/chat":
			var p map[string]any
			json.NewDecoder(r.Body).Decode(&p)
			if p["isPremium"] != false || p["validated"] != "actual-frontend-value" || p["userSelectedModel"] != "requested-model" || p["maxTokens"] != float64(1024) {
				t.Error("wrong payload")
			}
			io.WriteString(w, "answer with spaces  ")
		default:
			t.Error("unexpected endpoint")
			w.WriteHeader(404)
		}
	}))
	defer server.Close()
	c := New(config.Source{Adapter: "blackbox", KeyEnv: "BLACKBOX_TEST", BaseURL: server.URL})
	defer c.Close()
	for _, stream := range []bool{true, false} {
		resp, err := c.Do(context.Background(), "chat", "requested-model", stream, []byte(`{"messages":[{"role":"user","content":"hello"}]}`), http.Header{"Cookie": {"foreign"}})
		if err != nil {
			t.Fatal(err)
		}
		data, err := io.ReadAll(resp.Body)
		resp.Body.Close()
		if err != nil || !strings.Contains(string(data), "answer with spaces  ") {
			t.Fatalf("bad output %s %v", data, err)
		}
		if resp.Header.Get("X-COT-Delivery") != "buffered" {
			t.Error("false streaming claim")
		}
	}
	if calls != 6 {
		t.Fatal(calls)
	}
}

func TestBlackboxMissingValidationFailsBeforeNetwork(t *testing.T) {
	t.Setenv("BLACKBOX_TEST", "session")
	c := New(config.Source{Adapter: "blackbox", KeyEnv: "BLACKBOX_TEST", BaseURL: "https://example.invalid"})
	defer c.Close()
	_, err := c.Do(context.Background(), "chat", "model", false, []byte(`{"messages":[{"role":"user","content":"hello"}]}`), nil)
	if err == nil || !strings.Contains(err.Error(), "validated") {
		t.Fatal(err)
	}
}
