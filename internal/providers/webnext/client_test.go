package webnext

import (
	"bufio"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"clash-of-tokens/internal/config"
	"github.com/gobwas/ws"
	"github.com/gobwas/ws/wsutil"
)

func TestSupports(t *testing.T) {
	for _, adapter := range []string{AdapterFlowith, AdapterLangFast, AdapterLiaoBots} {
		if !Supports(adapter) {
			t.Fatalf("Supports(%q) = false", adapter)
		}
	}
	for _, adapter := range []string{AdapterEaseMate, "unknown"} {
		if Supports(adapter) {
			t.Fatalf("unsupported adapter %q is supported", adapter)
		}
	}
}

func TestLiaoBotsExplicitCredentialsAndStream(t *testing.T) {
	var request map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/chat" || r.Header.Get("X-Auth-Code") != "auth-code" || r.Header.Get("Cookie") != "session-cookie" || r.Header.Get("Authorization") != "" {
			t.Errorf("request path/auth = %s/%q/%q", r.URL.Path, r.Header.Get("X-Auth-Code"), r.Header.Get("Cookie"))
		}
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Errorf("request JSON: %v", err)
		}
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w, "data: {\"content\":\"hello\"}\n\ndata: [DONE]\n\n")
	}))
	defer server.Close()
	t.Setenv("WEBNEXT_LIAO", `{"authCode":"auth-code","cookie":"session-cookie"}`)
	c := New(config.Source{Adapter: AdapterLiaoBots, BaseURL: server.URL, KeyEnv: "WEBNEXT_LIAO"})
	defer c.Close()
	resp, err := c.Do(context.Background(), "chat", "gpt-4o", true, []byte(`{"model":"gpt-4o","messages":[{"role":"user","content":"question"}],"stream":true}`), http.Header{"Authorization": []string{"Bearer caller-secret"}})
	if err != nil {
		t.Fatal(err)
	}
	data, err := io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	if err != nil || !strings.Contains(string(data), `"content":"hello"`) || !strings.Contains(string(data), "[DONE]") {
		t.Fatalf("LiaoBots output = %s, err=%v", data, err)
	}
	if request["prompt_id"] != "" || request["search"] != "false" {
		t.Fatalf("LiaoBots payload = %#v", request)
	}
	if strings.Contains(string(data), "auth-code") || strings.Contains(string(data), "session-cookie") {
		t.Fatal("LiaoBots credential leaked into output")
	}
}

func TestLiaoBotsRejectsPlainCredential(t *testing.T) {
	t.Setenv("WEBNEXT_LIAO_PLAIN", "cookie")
	c := New(config.Source{Adapter: AdapterLiaoBots, BaseURL: "http://127.0.0.1:1", KeyEnv: "WEBNEXT_LIAO_PLAIN"})
	defer c.Close()
	if _, err := c.Do(context.Background(), "chat", "gpt-4o", false, []byte(`{"messages":[{"role":"user","content":"question"}]}`), nil); err != ErrCredential {
		t.Fatalf("plain credential error = %v", err)
	}
}

func TestLiaoBotsRejectsTrailingCredentialData(t *testing.T) {
	t.Setenv("WEBNEXT_LIAO_TRAILING", `{"authCode":"auth-code","cookie":"session-cookie"} trailing`)
	c := New(config.Source{Adapter: AdapterLiaoBots, BaseURL: "http://127.0.0.1:1", KeyEnv: "WEBNEXT_LIAO_TRAILING"})
	defer c.Close()
	_, err := c.Do(context.Background(), "chat", "gpt-4o", false, []byte(`{"messages":[{"role":"user","content":"question"}]}`), nil)
	if err != ErrCredential {
		t.Fatalf("trailing credential error = %v", err)
	}
}

func TestFlowithPayloadAndCompletion(t *testing.T) {
	var request map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/ai/chat" || r.URL.Query().Get("mode") != "general" {
			t.Errorf("Flowith route = %s", r.URL.String())
		}
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Errorf("request JSON: %v", err)
		}
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w, "line one\n\nline two  ")
	}))
	defer server.Close()
	t.Setenv("WEBNEXT_FLOW", "cookie")
	c := New(config.Source{Adapter: AdapterFlowith, BaseURL: server.URL, KeyEnv: "WEBNEXT_FLOW"})
	defer c.Close()
	resp, err := c.Do(context.Background(), "chat", "flowith-gpt-4.1", false, []byte(`{"model":"flowith-gpt-4.1","messages":[{"role":"user","content":"question"}]}`), nil)
	if err != nil {
		t.Fatal(err)
	}
	data, _ := io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	if !strings.Contains(string(data), `"content":"line oneline two  "`) {
		t.Fatalf("Flowith output = %s", data)
	}
	if request["model"] != "gpt-4.1" || request["stream"] != true {
		t.Fatalf("Flowith payload = %#v", request)
	}
}

func TestFlowithForwardsRawLinesAndAcceptsCleanEOF(t *testing.T) {
	var output string
	err := parseFlowith(context.Background(), strings.NewReader("  raw JSON line  \n\nfinal line"), func(text string) error {
		output += text
		return nil
	})
	if err != nil || output != "  raw JSON line  final line" {
		t.Fatalf("raw Flowith output = %q, err=%v", output, err)
	}
}

type unexpectedEOFReader struct{}

func (unexpectedEOFReader) Read(p []byte) (int, error) {
	copy(p, "partial")
	return len("partial"), io.ErrUnexpectedEOF
}

func TestFlowithUnexpectedEOFRemainsError(t *testing.T) {
	err := parseFlowith(context.Background(), unexpectedEOFReader{}, func(string) error { return nil })
	if !errors.Is(err, io.ErrUnexpectedEOF) {
		t.Fatalf("unexpected EOF error = %v", err)
	}
}

func TestEaseMateIsUnsupportedWithoutGuessedPayload(t *testing.T) {
	c := New(config.Source{Adapter: AdapterEaseMate, BaseURL: "http://127.0.0.1:1", KeyEnv: "WEBNEXT_EASE"})
	defer c.Close()
	resp, err := c.Do(context.Background(), "chat", "gpt-4o-mini", false, []byte(`{"model":"gpt-4o-mini","messages":[{"role":"user","content":"question"}]}`), nil)
	if resp != nil || err != ErrUnsupported {
		t.Fatalf("EaseMate support result = resp=%v, err=%v", resp, err)
	}
}

func TestLangFastHTTPAndSocketProtocol(t *testing.T) {
	var initiate map[string]any
	initiated := make(chan struct{})
	var once sync.Once
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/socket.io/" {
			conn, rw, _, err := ws.UpgradeHTTP(r, w)
			if err != nil {
				t.Errorf("upgrade: %v", err)
				return
			}
			defer conn.Close()
			if err := wsutil.WriteServerText(conn, []byte(`0{"sid":"sid","pingInterval":25000}`)); err != nil {
				t.Errorf("open: %v", err)
				return
			}
			data, err := wsutil.ReadClientText(rw)
			if err != nil || !strings.HasPrefix(string(data), "40") {
				t.Errorf("auth packet = %q, err=%v", data, err)
				return
			}
			if err := wsutil.WriteServerText(conn, []byte(`40{"sid":"namespace"}`)); err != nil {
				t.Errorf("connect: %v", err)
				return
			}
			<-initiated
			_ = wsutil.WriteServerText(conn, []byte(`42["execution:chunk",{"content":"lang"}]`))
			_ = wsutil.WriteServerText(conn, []byte(`42["execution:chunk",{"content":"langfast","status":"completed"}]`))
			return
		}
		if r.URL.Path != "/functions/v1/initiate-prompt-run" {
			t.Errorf("HTTP route = %s", r.URL.Path)
			return
		}
		if err := json.NewDecoder(r.Body).Decode(&initiate); err != nil {
			t.Errorf("initiate JSON: %v", err)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"ok":true}`)
		once.Do(func() { close(initiated) })
	}))
	defer server.Close()
	wsURL := strings.Replace(server.URL, "http://", "ws://", 1)
	part := base64.RawURLEncoding.EncodeToString([]byte(`{"sub":"user-1"}`))
	t.Setenv("WEBNEXT_LANG", `{"access_token":"header.`+part+`.signature","socket_url":"`+wsURL+`","supabase_anon_key":"anon"}`)
	c := New(config.Source{Adapter: AdapterLangFast, BaseURL: server.URL, KeyEnv: "WEBNEXT_LANG", Project: "prompt-1"})
	defer c.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	resp, err := c.Do(ctx, "chat", "gpt-4o-mini", false, []byte(`{"model":"gpt-4o-mini","messages":[{"role":"user","content":"question"}]}`), nil)
	if err != nil {
		t.Fatal(err)
	}
	data, err := io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	if err != nil || !strings.Contains(string(data), `"content":"langfast"`) {
		t.Fatalf("LangFast output = %s, err=%v", data, err)
	}
	if initiate["created_by"] != "user-1" || initiate["prompt_id"] != "prompt-1" {
		t.Fatalf("LangFast payload = %#v", initiate)
	}
}

func TestLangFastRequiresExplicitProjectAndAnonKey(t *testing.T) {
	t.Setenv("WEBNEXT_LANG_REQUIREMENTS", `{"access_token":"token","supabase_anon_key":"anon","user_id":"user-1"}`)
	missingProject := New(config.Source{Adapter: AdapterLangFast, BaseURL: "http://127.0.0.1:1", KeyEnv: "WEBNEXT_LANG_REQUIREMENTS"})
	defer missingProject.Close()
	_, err := missingProject.Do(context.Background(), "chat", "model", false, []byte(`{"messages":[{"role":"user","content":"question"}]}`), nil)
	if err == nil || !strings.Contains(err.Error(), "project (prompt_id) is required") {
		t.Fatalf("missing project error = %v", err)
	}

	t.Setenv("WEBNEXT_LANG_REQUIREMENTS", `{"access_token":"token","user_id":"user-1"}`)
	missingAnonKey := New(config.Source{Adapter: AdapterLangFast, BaseURL: "http://127.0.0.1:1", KeyEnv: "WEBNEXT_LANG_REQUIREMENTS", Project: "prompt-1"})
	defer missingAnonKey.Close()
	_, err = missingAnonKey.Do(context.Background(), "chat", "model", false, []byte(`{"messages":[{"role":"user","content":"question"}]}`), nil)
	if err != ErrCredential {
		t.Fatalf("missing anon key error = %v", err)
	}
}

func TestLangFastRejectsInsecureRemoteSocket(t *testing.T) {
	for _, raw := range []string{"ws://example.com", "http://example.com"} {
		if _, err := socketEndpoint(raw); err == nil {
			t.Fatalf("insecure remote socket %q was accepted", raw)
		}
	}
}

func TestLiaoBotsCleanEOF(t *testing.T) {
	var output string
	err := parseLiaoBots(context.Background(), bufio.NewReader(strings.NewReader("data: {\"content\":\"x\"}\n\n")), func(text string) error {
		output += text
		return nil
	})
	if err != nil || output != "x" {
		t.Fatalf("clean LiaoBots EOF = output %q, err=%v", output, err)
	}
}

func TestLiaoBotsCompletedStatusStopsAndEmitsFinalText(t *testing.T) {
	var output string
	err := parseLiaoBots(context.Background(), strings.NewReader("data: {\"status\":\"completed\",\"content\":\"answer\"}\n\n"), func(text string) error {
		output += text
		return nil
	})
	if err != nil || output != "answer" {
		t.Fatalf("completed status = output %q, err=%v", output, err)
	}
}

func TestParseChatRequestRejectsUnknownMessageField(t *testing.T) {
	_, err := parseChatRequest([]byte(`{"messages":[{"role":"user","content":"question","name":"unexpected"}]}`), AdapterFlowith, "model", false)
	if err == nil || !strings.Contains(err.Error(), "messages must be a non-empty text array") {
		t.Fatalf("unknown message field error = %v", err)
	}
}

func TestDoRejectsNilContext(t *testing.T) {
	c := New(config.Source{Adapter: AdapterFlowith, KeyEnv: "WEBNEXT_NIL_CONTEXT"})
	defer c.Close()
	if _, err := c.Do(nil, "chat", "model", false, []byte(`{"messages":[{"role":"user","content":"question"}]}`), nil); err == nil || !strings.Contains(err.Error(), "nil request context") {
		t.Fatalf("nil context error = %v", err)
	}
}

func TestStatelessAdaptersRejectSessionHeader(t *testing.T) {
	t.Setenv("WEBNEXT_STATELESS", "cookie")
	c := New(config.Source{Adapter: AdapterFlowith, BaseURL: "http://127.0.0.1:1", KeyEnv: "WEBNEXT_STATELESS"})
	defer c.Close()
	headers := make(http.Header)
	headers.Set("X-COT-Session", "conversation-1")
	_, err := c.Do(context.Background(), "chat", "model", false, []byte(`{"messages":[{"role":"user","content":"question"}]}`), headers)
	if err == nil || !strings.Contains(err.Error(), "stateless adapters reject X-COT-Session") {
		t.Fatalf("session header error = %v", err)
	}
}

func TestLiaoBotsCancellationClosesUpstream(t *testing.T) {
	started := make(chan struct{})
	closed := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		close(started)
		w.Header().Set("Content-Type", "text/event-stream")
		if f, ok := w.(http.Flusher); ok {
			_, _ = io.WriteString(w, "data: {\"content\":\"partial\"}\n\n")
			f.Flush()
		}
		<-r.Context().Done()
		close(closed)
	}))
	defer server.Close()
	t.Setenv("WEBNEXT_LIAO_CANCEL", `{"authCode":"auth-code","cookie":"session-cookie"}`)
	c := New(config.Source{Adapter: AdapterLiaoBots, BaseURL: server.URL, KeyEnv: "WEBNEXT_LIAO_CANCEL"})
	defer c.Close()
	ctx, cancel := context.WithCancel(context.Background())
	resp, err := c.Do(ctx, "chat", "gpt-4o", true, []byte(`{"messages":[{"role":"user","content":"question"}]}`), nil)
	if err != nil {
		t.Fatal(err)
	}
	<-started
	cancel()
	_, _ = io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	select {
	case <-closed:
	case <-time.After(2 * time.Second):
		t.Fatal("upstream was not closed after cancellation")
	}
}
