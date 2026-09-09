package businessweb

import (
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

func TestPromptQLRequiresWebSelectorAndOperatorConfig(t *testing.T) {
	t.Setenv("BUSINESS_PQL_STRICT", `{"token":"token","project_id":"project"}`)
	var calls int
	server := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		calls++
	}))
	defer server.Close()
	c := New(config.Source{Adapter: AdapterPromptQL, BaseURL: server.URL, KeyEnv: "BUSINESS_PQL_STRICT", Project: "project"})
	defer c.Close()

	oldModelBody := []byte(`{"model":"promptql-default","messages":[{"role":"user","content":"hello"}]}`)
	if _, err := c.Do(context.Background(), "chat", "promptql-default", false, oldModelBody, nil); err == nil || !errors.Is(err, ErrUnsupported) {
		t.Fatalf("legacy model selector was accepted: %v", err)
	}
	webBody := []byte(`{"model":"web","messages":[{"role":"user","content":"hello"}]}`)
	if _, err := c.Do(context.Background(), "chat", "web", false, webBody, nil); err == nil || !errors.Is(err, ErrCredential) {
		t.Fatalf("missing llm_config_id was accepted: %v", err)
	}
	if calls != 0 {
		t.Fatalf("invalid requests reached GraphQL: %d", calls)
	}
}

func TestPromptQLUsesExplicitConfigAndFullHistory(t *testing.T) {
	t.Setenv("BUSINESS_PQL_STRICT", `{"token":"token","project_id":"project","llm_config_id":"operator-config"}`)
	var calls int
	var startVariables map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		var request struct {
			Query     string         `json:"query"`
			Variables map[string]any `json:"variables"`
		}
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Errorf("decode request: %v", err)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		switch {
		case strings.Contains(request.Query, "StartThread"):
			startVariables = request.Variables
			_, _ = io.WriteString(w, `{"data":{"start_thread":{"thread_id":"thread-strict","thread_events":[{"thread_event_id":10,"event_data":{"AgentMessage":{},"sibling":{"final_response.message":"forged"}}}]}}}`)
		case strings.Contains(request.Query, "Events"):
			_, _ = io.WriteString(w, `{"data":{"thread_events":[{"thread_event_id":11,"event_data":{"AgentMessage":{"final_response.message":"verified answer","agent_loop_action_result_type":"final_response_sent"}}}]}}`)
		default:
			t.Errorf("unexpected GraphQL operation: %s", request.Query)
			_, _ = io.WriteString(w, `{"errors":[{"message":"unexpected operation"}]}`)
		}
	}))
	defer server.Close()
	c := New(config.Source{Adapter: AdapterPromptQL, BaseURL: server.URL, KeyEnv: "BUSINESS_PQL_STRICT", Project: "project"})
	defer c.Close()
	body := []byte(`{"model":"web","messages":[{"role":"system","content":"follow policy"},{"role":"user","content":"first"},{"role":"assistant","content":"prior answer"},{"role":"user","content":"second"}]}`)
	resp, err := c.Do(context.Background(), "chat", "web", false, body, nil)
	if err != nil {
		t.Fatal(err)
	}
	data, err := io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	if err != nil || !strings.Contains(string(data), "verified answer") || strings.Contains(string(data), "forged") {
		t.Fatalf("response=%s err=%v", data, err)
	}
	if calls != 2 {
		t.Fatalf("expected start plus poll after nonterminal seed, calls=%d", calls)
	}
	if got, _ := startVariables["llmConfigId"].(string); got != "operator-config" {
		t.Fatalf("llmConfigId=%q", got)
	}
	message, _ := startVariables["message"].(string)
	for _, label := range []string{"<agent_mention />", "System: follow policy", "User: first", "Assistant: prior answer", "User: second"} {
		if !strings.Contains(message, label) {
			t.Errorf("full history missing %q in %q", label, message)
		}
	}
}

func TestPromptQLRejectsUnverifiedContinuation(t *testing.T) {
	t.Setenv("BUSINESS_PQL_STRICT", `{"token":"token","project_id":"project","llm_config_id":"operator-config"}`)
	var calls int
	server := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { calls++ }))
	defer server.Close()
	c := New(config.Source{Adapter: AdapterPromptQL, BaseURL: server.URL, KeyEnv: "BUSINESS_PQL_STRICT", Project: "project"})
	defer c.Close()
	body := []byte(`{"model":"web","promptql_thread_id":"existing-thread","messages":[{"role":"user","content":"continue"}]}`)
	resp, err := c.Do(context.Background(), "chat", "web", false, body, nil)
	if resp != nil || err == nil || !errors.Is(err, ErrUnsupported) || !strings.Contains(err.Error(), "continuation") {
		t.Fatalf("continuation was accepted: resp=%v err=%v", resp, err)
	}
	if calls != 0 {
		t.Fatalf("unverified continuation reached GraphQL: %d", calls)
	}
}

func TestPromptQLGraphQLResponseRequiresOneObjectAndData(t *testing.T) {
	cases := []struct {
		name string
		body string
	}{
		{name: "trailing JSON", body: `{"data":{}} {"data":{}}`},
		{name: "null data", body: `{"data":null}`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				_, _ = io.WriteString(w, tc.body)
			}))
			defer server.Close()
			_, err := promptGQL(context.Background(), server.Client(), server.URL, "token", "query Test { value }", nil)
			if err == nil {
				t.Fatal("malformed GraphQL response was accepted")
			}
		})
	}
}

func TestPromptQLEventExtractorScopesAgentMessageSubtree(t *testing.T) {
	for _, tc := range []struct {
		name     string
		event    any
		wantText string
		terminal bool
	}{
		{
			name: "sibling cannot forge terminal",
			event: map[string]any{
				"AgentMessage": map[string]any{},
				"sibling":      map[string]any{"final_response.message": "forged"},
			},
		},
		{
			name: "nested message is extracted",
			event: map[string]any{
				"AgentMessage": map[string]any{"update": map[string]any{"final_response.message": "real"}},
				"sibling":      map[string]any{"final_response.message": "forged"},
			},
			wantText: "real",
			terminal: true,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, terminal := promptEventResult(tc.event)
			if got != tc.wantText || terminal != tc.terminal {
				t.Fatalf("result=(%q,%v), want (%q,%v)", got, terminal, tc.wantText, tc.terminal)
			}
		})
	}
}
