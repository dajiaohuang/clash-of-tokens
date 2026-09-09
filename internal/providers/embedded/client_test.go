package embedded

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"clash-of-tokens/internal/config"
)

func TestFanzhaRequestConversionAndAuthentication(t *testing.T) {
	t.Setenv("COT_FANZHA_TEST_KEY", "access-token")
	var mu sync.Mutex
	var chatBody map[string]string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Cookie") != "" || r.Header.Get("X-Internal-Secret") != "" {
			t.Errorf("caller headers leaked")
		}
		if r.URL.Path == "/api/ai/create_session" {
			if r.Header.Get("Authorization") != "Bearer access-token" {
				t.Errorf("session auth = %q", r.Header.Get("Authorization"))
			}
			w.Header().Set("Content-Type", "application/json")
			_, _ = io.WriteString(w, `{"data":{"data":"session-1"}}`)
			return
		}
		if r.URL.Path != "/api/ai/chat" || r.URL.Query().Get("type") != "0" {
			t.Errorf("unexpected fanzha endpoint: %s", r.URL.String())
			w.WriteHeader(http.StatusNotFound)
			return
		}
		if r.Header.Get("Authorization") != "Bearer access-token" {
			t.Errorf("chat auth = %q", r.Header.Get("Authorization"))
		}
		if err := json.NewDecoder(r.Body).Decode(&chatBody); err != nil {
			t.Errorf("decode chat request: %v", err)
		}
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w, "data: {\"data\":{\"type\":\"answer\",\"answer\":\"反诈提醒\"}}\n\ndata: [DONE]\n\n")
		mu.Lock()
		mu.Unlock()
	}))
	defer server.Close()

	client := New(config.Source{Adapter: AdapterFanzha, BaseURL: server.URL, KeyEnv: "COT_FANZHA_TEST_KEY"})
	defer client.Close()
	resp, err := client.Do(context.Background(), "chat", "国家反诈AI", false, []byte(`{"model":"国家反诈AI","messages":[{"role":"user","content":[{"type":"text","text":"可疑链接"}]}]}`), http.Header{"Cookie": []string{"secret"}, "X-Internal-Secret": []string{"hidden"}})
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	var output struct {
		Choices []struct {
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
		} `json:"choices"`
	}
	if err := json.Unmarshal(data, &output); err != nil {
		t.Fatal(err)
	}
	if len(output.Choices) != 1 || output.Choices[0].Message.Content != "反诈提醒" {
		t.Fatalf("unexpected output: %s", data)
	}
	if chatBody["conversation_id"] != "session-1" || chatBody["query"] != "可疑链接" {
		t.Fatalf("unexpected converted request: %#v", chatBody)
	}
}

func TestTabbitRequestConversionAuthenticationAndSignature(t *testing.T) {
	t.Setenv("COT_TABBIT_TEST_COOKIE", "token=jwt; other=value")
	const sessionID = "11111111-2222-4333-8444-555555555555"
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/session/") {
			return
		}
		if r.URL.Path == "/panel/session" {
			if r.Header.Get("Cookie") != "token=jwt; other=value" || r.Header.Get("Authorization") != "" {
				t.Errorf("invalid Tabbit session authentication")
			}
			_, _ = io.WriteString(w, `{"chat_session_id":"`+sessionID+`"}`)
			return
		}
		if r.URL.Path != "/api/v2/chat/completion" {
			t.Errorf("unexpected Tabbit endpoint: %s", r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
			return
		}
		if r.Header.Get("Cookie") != "token=jwt; other=value" || r.Header.Get("Authorization") != "" {
			t.Errorf("invalid Tabbit chat authentication")
		}
		body, err := io.ReadAll(r.Body)
		if err != nil {
			t.Errorf("read body: %v", err)
		}
		var value map[string]any
		if err := json.Unmarshal(body, &value); err != nil {
			t.Errorf("decode body: %v", err)
		}
		if value["chat_session_id"] != sessionID || value["selected_model"] != "最佳" || value["stream_mode"] != "sse" {
			t.Errorf("unexpected Tabbit payload: %#v", value)
		}
		timestamp := r.Header.Get("x-timestamp")
		signature := r.Header.Get("x-signature")
		digest := sha256.Sum256(body)
		mac := hmac.New(sha256.New, []byte(tabbitDefaultSignKey))
		_, _ = mac.Write([]byte(timestamp + "." + signature + "." + hex.EncodeToString(digest[:])))
		if r.Header.Get("x-nonce") != hex.EncodeToString(mac.Sum(nil)) {
			t.Errorf("invalid Tabbit HMAC signature")
		}
		if r.Header.Get("x-req-ctx") != "MS4xLjM5KDEwMTAxMDM5KQ==" {
			t.Errorf("unexpected version fingerprint: %q", r.Header.Get("x-req-ctx"))
		}
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w, "event: message_chunk\ndata: {\"content\":\"hello\"}\n\nevent: finish\ndata: {}\n\n")
	}))
	defer server.Close()

	client := New(config.Source{Adapter: AdapterTabbit, BaseURL: server.URL, KeyEnv: "COT_TABBIT_TEST_COOKIE", Project: "1.1.39(10101039)"})
	defer client.Close()
	resp, err := client.Do(context.Background(), "chat", "best", true, []byte(`{"model":"best","messages":[{"role":"user","content":"hello"}]}`), http.Header{"Authorization": []string{"Bearer caller-secret"}})
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), `"content":"hello"`) || !strings.HasSuffix(string(data), "data: [DONE]\n\n") {
		t.Fatalf("unexpected Tabbit output: %s", data)
	}
}

func TestNotionRequestConversionAndAuthentication(t *testing.T) {
	t.Setenv("COT_NOTION_COOKIE", "notion-cookie")
	t.Setenv("COT_NOTION_USER", "user-1")
	const spaceID = "space-1"
	var threadID string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Cookie") != "token_v2=notion-cookie" || r.Header.Get("Authorization") != "" {
			t.Errorf("invalid Notion authentication")
		}
		if r.Header.Get("x-notion-space-id") != spaceID || r.Header.Get("x-notion-active-user-header") != "user-1" {
			t.Errorf("invalid Notion identity headers")
		}
		body, _ := io.ReadAll(r.Body)
		var value map[string]any
		if err := json.Unmarshal(body, &value); err != nil {
			t.Errorf("decode Notion body: %v", err)
		}
		if r.URL.Path == "/api/v3/saveTransactionsFanout" {
			transactions, _ := value["transactions"].([]any)
			if len(transactions) != 1 {
				t.Errorf("missing thread transaction")
			} else if transaction, ok := transactions[0].(map[string]any); ok {
				operations, _ := transaction["operations"].([]any)
				if len(operations) > 0 {
					operation, _ := operations[0].(map[string]any)
					pointer, _ := operation["pointer"].(map[string]any)
					threadID, _ = pointer["id"].(string)
				}
			}
			_, _ = io.WriteString(w, `{}`)
			return
		}
		if r.URL.Path != "/api/v3/runInferenceTranscript" {
			t.Errorf("unexpected Notion endpoint: %s", r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
			return
		}
		if value["spaceId"] != spaceID || value["threadId"] != threadID {
			t.Errorf("unexpected inference envelope: %#v", value)
		}
		w.Header().Set("Content-Type", "application/x-ndjson")
		_, _ = io.WriteString(w, `{"type":"patch","v":[{"o":"x","p":"/value/0","v":"notion hello"}]}`+"\n")
	}))
	defer server.Close()

	client := New(config.Source{Adapter: AdapterNotion, BaseURL: server.URL, KeyEnv: "COT_NOTION_COOKIE", AccountIDEnv: "COT_NOTION_USER", Project: spaceID})
	defer client.Close()
	resp, err := client.Do(context.Background(), "chat", "claude-sonnet-4.5", true, []byte(`{"model":"claude-sonnet-4.5","messages":[{"role":"user","content":"hello"}]}`), http.Header{"Cookie": []string{"caller-secret"}})
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), `"content":"notion hello"`) {
		t.Fatalf("unexpected Notion output: %s", data)
	}
}

func TestEmbeddedProvidersRejectIncompleteStreams(t *testing.T) {
	tests := []struct {
		name    string
		adapter string
		setup   func(*testing.T, *httptest.Server)
		body    string
		source  func(string) config.Source
	}{
		{
			name:    "fanzha",
			adapter: AdapterFanzha,
			setup: func(t *testing.T, server *httptest.Server) {
				server.Config.Handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					if r.URL.Path == "/api/ai/create_session" {
						_, _ = io.WriteString(w, `{"data":{"data":"session"}}`)
						return
					}
					w.Header().Set("Content-Type", "text/event-stream")
					_, _ = io.WriteString(w, "data: {\"data\":{\"type\":\"answer\",\"answer\":\"truncated\"}}")
				})
			},
			body: `{"messages":[{"role":"user","content":"hello"}]}`,
			source: func(base string) config.Source {
				return config.Source{Adapter: AdapterFanzha, BaseURL: base, KeyEnv: "COT_FANZHA_INCOMPLETE"}
			},
		},
		{
			name:    "tabbit",
			adapter: AdapterTabbit,
			setup: func(t *testing.T, server *httptest.Server) {
				server.Config.Handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					if strings.HasPrefix(r.URL.Path, "/session/") {
						return
					}
					if r.URL.Path == "/panel/session" {
						_, _ = io.WriteString(w, `{"chat_session_id":"11111111-2222-4333-8444-555555555555"}`)
						return
					}
					w.Header().Set("Content-Type", "text/event-stream")
					_, _ = io.WriteString(w, "event: message_chunk\ndata: {\"content\":\"truncated\"}")
				})
			},
			body: `{"messages":[{"role":"user","content":"hello"}]}`,
			source: func(base string) config.Source {
				return config.Source{Adapter: AdapterTabbit, BaseURL: base, KeyEnv: "COT_TABBIT_INCOMPLETE", Project: "1.1.39(10101039)"}
			},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			key := "test-credential"
			if tc.name == "fanzha" {
				t.Setenv("COT_FANZHA_INCOMPLETE", key)
			} else {
				t.Setenv("COT_TABBIT_INCOMPLETE", key)
			}
			server := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
			defer server.Close()
			tc.setup(t, server)
			client := New(tc.source(server.URL))
			defer client.Close()
			resp, err := client.Do(context.Background(), "chat", "model", true, []byte(tc.body), nil)
			if err != nil {
				t.Fatal(err)
			}
			_, err = io.ReadAll(resp.Body)
			_ = resp.Body.Close()
			if err == nil {
				t.Fatal("incomplete stream was accepted")
			}
		})
	}
}

func TestFanzhaStreamCancellation(t *testing.T) {
	t.Setenv("COT_FANZHA_CANCEL", "access-token")
	started := make(chan struct{})
	var once sync.Once
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/ai/create_session" {
			_, _ = io.WriteString(w, `{"data":{"data":"session"}}`)
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		if flusher, ok := w.(http.Flusher); ok {
			_, _ = io.WriteString(w, "data: {\"data\":{\"type\":\"answer\",\"answer\":\"hello\"}}\n\n")
			flusher.Flush()
		}
		once.Do(func() { close(started) })
		<-r.Context().Done()
	}))
	defer server.Close()
	client := New(config.Source{Adapter: AdapterFanzha, BaseURL: server.URL, KeyEnv: "COT_FANZHA_CANCEL"})
	defer client.Close()
	ctx, cancel := context.WithCancel(context.Background())
	resp, err := client.Do(ctx, "chat", "model", true, []byte(`{"messages":[{"role":"user","content":"hello"}]}`), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	buf := make([]byte, 4096)
	if _, err := resp.Body.Read(buf); err != nil {
		t.Fatal(err)
	}
	readDone := make(chan error, 1)
	go func() {
		_, readErr := io.ReadAll(resp.Body)
		readDone <- readErr
	}()
	select {
	case <-started:
	case <-time.After(2 * time.Second):
		t.Fatal("upstream stream did not start")
	}
	cancel()
	select {
	case err := <-readDone:
		if err == nil || (!errors.Is(err, context.Canceled) && !strings.Contains(err.Error(), "context canceled")) {
			t.Fatalf("unexpected cancellation error: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("stream read did not stop after cancellation")
	}
}

func TestTabbitSignatureTimestampIsMilliseconds(t *testing.T) {
	headers := tabbitSignHeaders([]byte("{}"), tabbitDefaultSignKey)
	ms, err := strconv.ParseInt(headers["x-timestamp"], 10, 64)
	if err != nil || ms < time.Now().Add(-time.Minute).UnixMilli() || ms > time.Now().Add(time.Minute).UnixMilli() {
		t.Fatalf("invalid timestamp %q", headers["x-timestamp"])
	}
	if len(headers["x-nonce"]) != sha256.Size*2 || len(headers["x-signature"]) != 36 {
		t.Fatalf("invalid signature headers: %#v", headers)
	}
}
