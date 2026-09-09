package majorweb

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"clash-of-tokens/internal/config"
	"github.com/gobwas/ws"
	"github.com/gobwas/ws/wsutil"
)

func TestParseInputsRejectLossyRequests(t *testing.T) {
	tests := []struct {
		name string
		body string
		fn   func([]byte) (chatInput, error)
	}{
		{"history", `{"messages":[{"role":"user","content":"one"},{"role":"user","content":"two"}]}`, func(body []byte) (chatInput, error) { return parseChatInput(body, "m") }},
		{"tools", `{"messages":[{"role":"user","content":"one"}],"tools":[]}`, func(body []byte) (chatInput, error) { return parseChatInput(body, "m") }},
		{"assistant", `{"messages":[{"role":"assistant","content":"one"}]}`, func(body []byte) (chatInput, error) { return parseChatInput(body, "m") }},
		{"response history", `{"input":[{"role":"user","content":"one"},{"role":"user","content":"two"}]}`, func(body []byte) (chatInput, error) { return parseResponsesInput(body, "m") }},
		{"response extra", `{"input":[{"role":"user","content":"one","type":"message"}]}`, func(body []byte) (chatInput, error) { return parseResponsesInput(body, "m") }},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if _, err := test.fn([]byte(test.body)); err == nil || !strings.Contains(err.Error(), "unsupported") && !strings.Contains(err.Error(), "one") && !strings.Contains(err.Error(), "single user") {
				t.Fatalf("expected strict input error, got %v", err)
			}
		})
	}
}

func TestClaudeConversationAndStreamConversion(t *testing.T) {
	var conversationBody map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Cookie") != "sessionKey=session-secret" {
			t.Fatalf("cookie = %q", r.Header.Get("Cookie"))
		}
		switch r.URL.Path {
		case "/api/organizations/org-1/chat_conversations":
			if err := json.NewDecoder(r.Body).Decode(&conversationBody); err != nil {
				t.Fatalf("conversation payload: %v", err)
			}
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusCreated)
			_, _ = io.WriteString(w, `{"uuid":"conversation-1"}`)
		case "/api/organizations/org-1/chat_conversations/conversation-1/completion":
			w.Header().Set("Content-Type", "text/event-stream")
			_, _ = io.WriteString(w, "data: {\"type\":\"content_block_delta\",\"delta\":{\"type\":\"thinking_delta\",\"thinking\":\"plan\"}}\n\n")
			_, _ = io.WriteString(w, "data: {\"type\":\"content_block_delta\",\"delta\":{\"type\":\"text_delta\",\"text\":\"hello\"}}\n\n")
			_, _ = io.WriteString(w, "event: message_stop\ndata: {\"type\":\"message_stop\"}\n\n")
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	t.Setenv("CLAUDE_MAJORWEB_TEST", "session-secret")
	c := New(config.Source{Adapter: AdapterClaude, BaseURL: server.URL, KeyEnv: "CLAUDE_MAJORWEB_TEST", AccountIDEnv: "CLAUDE_ORG_TEST"})
	defer c.Close()
	t.Setenv("CLAUDE_ORG_TEST", "org-1")
	resp, err := c.Do(context.Background(), "chat", "claude-sonnet", false, []byte(`{"model":"claude-sonnet","messages":[{"role":"user","content":"hello"}]}`), nil)
	if err != nil {
		t.Fatal(err)
	}
	data, _ := io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	var result struct {
		Choices []struct {
			Message struct {
				Content          string `json:"content"`
				ReasoningContent string `json:"reasoning_content"`
			} `json:"message"`
		} `json:"choices"`
	}
	if json.Unmarshal(data, &result) != nil || len(result.Choices) != 1 || result.Choices[0].Message.Content != "hello" || result.Choices[0].Message.ReasoningContent != "plan" {
		t.Fatalf("converted Claude result = %s", data)
	}
	if conversationBody["model"] != "claude-sonnet" || conversationBody["include_conversation_preferences"] != true {
		t.Fatalf("conversation payload = %#v", conversationBody)
	}
}

func TestClaudeDefaultSonnetOmitsModelOnWebRequests(t *testing.T) {
	var conversationBody, completionBody map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/organizations/org-1/chat_conversations":
			if err := json.NewDecoder(r.Body).Decode(&conversationBody); err != nil {
				t.Fatalf("conversation payload: %v", err)
			}
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusCreated)
			_, _ = io.WriteString(w, `{"uuid":"conversation-1"}`)
		case "/api/organizations/org-1/chat_conversations/conversation-1/completion":
			if err := json.NewDecoder(r.Body).Decode(&completionBody); err != nil {
				t.Fatalf("completion payload: %v", err)
			}
			w.Header().Set("Content-Type", "text/event-stream")
			_, _ = io.WriteString(w, "data: {\"type\":\"content_block_delta\",\"delta\":{\"type\":\"text_delta\",\"text\":\"answer\"}}\n\n")
			_, _ = io.WriteString(w, "event: message_stop\ndata: {\"type\":\"message_stop\"}\n\n")
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	t.Setenv("CLAUDE_DEFAULT_MAJORWEB_TEST", "session-secret")
	c := New(config.Source{Adapter: AdapterClaude, BaseURL: server.URL, KeyEnv: "CLAUDE_DEFAULT_MAJORWEB_TEST", AccountIDEnv: "CLAUDE_DEFAULT_ORG_TEST"})
	defer c.Close()
	t.Setenv("CLAUDE_DEFAULT_ORG_TEST", "org-1")
	resp, err := c.Do(context.Background(), "chat", claudeDefaultModel, false, []byte(`{"messages":[{"role":"user","content":"hello"}]}`), nil)
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()
	if _, ok := conversationBody["model"]; ok {
		t.Fatalf("conversation payload unexpectedly contains model: %#v", conversationBody)
	}
	if _, ok := completionBody["model"]; ok {
		t.Fatalf("completion payload unexpectedly contains model: %#v", completionBody)
	}
}

func TestGensparkPayloadAndStreamConversion(t *testing.T) {
	var payload map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/copilot/ask" || r.Header.Get("Cookie") != "GS_COOKIE=secret" {
			t.Fatalf("request path/cookie = %s/%q", r.URL.Path, r.Header.Get("Cookie"))
		}
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			t.Fatalf("payload: %v", err)
		}
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w, "data: {\"type\":\"message_field_delta\",\"field_name\":\"session_state.answerthink\",\"delta\":\"reason\"}\n\n")
		_, _ = io.WriteString(w, "data: {\"type\":\"message_result\",\"content\":\"answer\"}\n\n")
	}))
	defer server.Close()
	t.Setenv("GENSPARK_MAJORWEB_TEST", "GS_COOKIE=secret")
	c := New(config.Source{Adapter: AdapterGenspark, BaseURL: server.URL, KeyEnv: "GENSPARK_MAJORWEB_TEST"})
	defer c.Close()
	resp, err := c.Do(context.Background(), "chat", "grok-search", false, []byte(`{"messages":[{"role":"user","content":"question"}]}`), nil)
	if err != nil {
		t.Fatal(err)
	}
	data, _ := io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	if !strings.Contains(string(data), `"content":"answer"`) || !strings.Contains(string(data), `"reasoning_content":"reason"`) {
		t.Fatalf("converted Genspark result = %s", data)
	}
	extra, _ := payload["extra_data"].(map[string]any)
	if payload["type"] != gensparkChatType || extra["request_web_knowledge"] != true {
		t.Fatalf("Genspark payload = %#v", payload)
	}
}

func TestGrokResponsesPayloadAndConversion(t *testing.T) {
	var payload map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/responses" || r.Header.Get("Authorization") != "Bearer access-token" {
			t.Fatalf("request path/auth = %s/%q", r.URL.Path, r.Header.Get("Authorization"))
		}
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			t.Fatalf("payload: %v", err)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"object":"response","id":"resp-1","status":"completed","output":[{"type":"message","role":"assistant","content":[{"type":"output_text","text":"answer"}]}]}`)
	}))
	defer server.Close()
	t.Setenv("GROK_MAJORWEB_TEST", "access-token")
	c := New(config.Source{Adapter: AdapterGrokBuild, BaseURL: server.URL + "/v1", KeyEnv: "GROK_MAJORWEB_TEST"})
	defer c.Close()
	resp, err := c.Do(context.Background(), "responses", "grok-build-model", false, []byte(`{"model":"grok-build-model","input":"question"}`), nil)
	if err != nil {
		t.Fatal(err)
	}
	data, _ := io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	var result map[string]any
	if err := json.Unmarshal(data, &result); err != nil {
		t.Fatalf("native Grok result: %v", err)
	}
	if result["object"] != "response" || result["id"] != "resp-1" || payload["input"] != "question" {
		t.Fatalf("native Grok result/payload = %s/%#v", data, payload)
	}
}

func TestGrokResponsesRejectsLegacyShape(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"id":"resp-1","output_text":"answer"}`)
	}))
	defer server.Close()
	t.Setenv("GROK_MAJORWEB_TEST", "access-token")
	c := New(config.Source{Adapter: AdapterGrokBuild, BaseURL: server.URL + "/v1", KeyEnv: "GROK_MAJORWEB_TEST"})
	defer c.Close()
	if _, err := c.Do(context.Background(), "responses", "grok-build-model", false, []byte(`{"model":"grok-build-model","input":"question"}`), nil); err == nil || !strings.Contains(err.Error(), "non-Responses object") {
		t.Fatalf("legacy Grok Responses shape error = %v", err)
	}
}

func TestGrokResponsesEndpointVersionsBase(t *testing.T) {
	tests := []struct {
		name    string
		base    string
		console bool
		want    string
	}{
		{"console host", "https://console.x.ai", true, "https://console.x.ai/v1/responses"},
		{"console v1", "https://console.x.ai/v1", true, "https://console.x.ai/v1/responses"},
		{"build v1", "https://cli-chat-proxy.grok.com/v1", false, "https://cli-chat-proxy.grok.com/v1/responses"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := grokResponsesEndpoint(test.base, test.console); got != test.want {
				t.Fatalf("endpoint = %q, want %q", got, test.want)
			}
		})
	}
}

func TestGrokResponsesCompletedTextFallback(t *testing.T) {
	parse := responsesEventWithIdentity("id", "model")
	out, done, err := parse("response.output_text.done", `{"type":"response.output_text.done","text":"answer"}`)
	if err != nil || done || !strings.Contains(string(out), `"content":"answer"`) {
		t.Fatalf("completed text fallback = %q, done=%v, err=%v", out, done, err)
	}
	if _, done, err := parse("response.completed", `{"type":"response.completed"}`); err != nil || !done {
		t.Fatalf("completion marker = done=%v, err=%v", done, err)
	}
}

func TestGrokGatewayEndpoint(t *testing.T) {
	endpoint, err := grokGatewayEndpoint("https://grok.example", "user-1")
	if err != nil || endpoint != "wss://grok.example/ws/mgw/?uid=user-1" {
		t.Fatalf("endpoint = %q, err = %v", endpoint, err)
	}
}

func TestGrokCookieHeaderNormalizesSSOToken(t *testing.T) {
	tests := []struct {
		value string
		want  string
	}{
		{"token", "sso=token; sso-rw=token; x-userid=user-1"},
		{"sso=token", "sso=token; sso-rw=token; x-userid=user-1"},
		{"sso=token; cf_clearance=clear", "sso=token; sso-rw=token; cf_clearance=clear; x-userid=user-1"},
		{"sso=token; sso-rw=other", "sso=token; sso-rw=token; x-userid=user-1"},
	}
	for _, test := range tests {
		if got := grokCookieHeader(test.value, "user-1"); got != test.want {
			t.Errorf("cookie header for %q = %q, want %q", test.value, got, test.want)
		}
	}
}

func TestGrokGatewayDeltaShape(t *testing.T) {
	var root map[string]any
	if err := json.Unmarshal([]byte(`{"event":{"type":"response.chunk","chunk":{"text":{"channel":"CHANNEL_ASSISTANT_RESPONSE","text":"hello"}}}}`), &root); err != nil {
		t.Fatal(err)
	}
	event := root["event"].(map[string]any)
	if got := grokGatewayDelta(event["type"].(string), event, "id", "model"); len(got) == 0 {
		t.Fatalf("no delta from %#v", event)
	}
}

func TestGrokWebGatewaySessionAndConversion(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/ws/mgw/" || r.URL.Query().Get("uid") != "user-1" {
			t.Errorf("Gateway URL = %s", r.URL.String())
			return
		}
		conn, rw, _, err := ws.UpgradeHTTP(r, w)
		if err != nil {
			t.Errorf("upgrade: %v", err)
			return
		}
		defer conn.Close()
		readClient := func() map[string]any {
			data, err := wsutil.ReadClientText(rw)
			if err != nil {
				t.Errorf("read client frame: %v", err)
				return nil
			}
			var value map[string]any
			_ = json.Unmarshal(data, &value)
			return value
		}
		initial := readClient()
		if initial == nil {
			return
		}
		_ = wsutil.WriteServerText(conn, []byte(`{"event":{"type":"session.created"}}`))
		_ = wsutil.WriteServerText(conn, []byte(`{"event":{"type":"conversation.attached","conversation":{"id":"conversation-1"}}}`))
		item := readClient()
		response := readClient()
		if item["session_id"] != "conversation-1" || response["session_id"] != "conversation-1" {
			t.Errorf("Gateway session IDs = %#v/%#v", item["session_id"], response["session_id"])
		}
		_ = wsutil.WriteServerText(conn, []byte(`{"event":{"type":"response.chunk","chunk":{"text":{"channel":"CHANNEL_ASSISTANT_RESPONSE","text":"hello"}}}}`))
		_ = wsutil.WriteServerText(conn, []byte(`{"event":{"type":"response.done"}}`))
	}))
	defer server.Close()
	t.Setenv("GROK_WEB_MAJORWEB_TEST", `{"cookie":"sso=secret","user_id":"user-1"}`)
	c := New(config.Source{Adapter: AdapterGrokWeb, BaseURL: server.URL, KeyEnv: "GROK_WEB_MAJORWEB_TEST"})
	defer c.Close()
	resp, err := c.Do(context.Background(), "chat", "grok-4", false, []byte(`{"model":"grok-4","messages":[{"role":"user","content":"hello"}]}`), nil)
	if err != nil {
		t.Fatal(err)
	}
	data, _ := io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	if !strings.Contains(string(data), `"content":"hello"`) {
		t.Fatalf("converted Grok Web result = %s", data)
	}
}
