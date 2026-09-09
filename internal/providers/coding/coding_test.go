package coding

import (
	"bytes"
	"context"
	"encoding/binary"
	"encoding/json"
	"errors"
	"hash/crc32"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"clash-of-tokens/internal/config"
)

func TestCodingProviderRequestsAndAuth(t *testing.T) {
	tests := []struct {
		name     string
		adapter  string
		protocol string
		body     string
		check    func(t *testing.T, request []byte)
		response string
		want     string
	}{
		{
			name:     "kiro messages",
			adapter:  "kiro",
			protocol: "messages",
			body:     `{"model":"client-model","messages":[{"role":"user","content":"hello"}]}`,
			check: func(t *testing.T, request []byte) {
				var root map[string]json.RawMessage
				if json.Unmarshal(request, &root) != nil || len(root["conversationState"]) == 0 {
					t.Fatal("Kiro native conversationState envelope missing")
				}
				if !bytes.Contains(request, []byte(`"modelId":"kiro-model"`)) {
					t.Fatal("Kiro model was not selected from the gateway model")
				}
			},
			response: string(makeEventFrame(t, map[string]string{":event-type": "assistantResponseEvent"}, `{"assistantResponseEvent":{"content":"hello from kiro","messageId":"kiro-msg","status":"completed"}}`)),
			want:     `"type":"message"`,
		},
		{
			name:     "iflow chat",
			adapter:  "iflow",
			protocol: "chat",
			body:     `{"model":"client-model","messages":[{"role":"user","content":"hello"}]}`,
			check: func(t *testing.T, request []byte) {
				var root map[string]json.RawMessage
				if json.Unmarshal(request, &root) != nil {
					t.Fatal("iFlow request is not JSON")
				}
				if string(root["model"]) != `"iflow-model"` || string(root["stream"]) != "false" {
					t.Fatalf("iFlow model/stream mismatch: model=%s stream=%s", root["model"], root["stream"])
				}
			},
			response: `{"id":"iflow-msg","model":"iflow-model","choices":[{"index":0,"message":{"role":"assistant","content":"hello from iflow"},"finish_reason":"stop"}]}`,
			want:     `"hello from iflow"`,
		},
		{
			name:     "antigravity gemini",
			adapter:  "antigravity",
			protocol: "gemini",
			body:     `{"contents":[{"role":"user","parts":[{"text":"hello"}]}]}`,
			check: func(t *testing.T, request []byte) {
				var root map[string]json.RawMessage
				if json.Unmarshal(request, &root) != nil || len(root["request"]) == 0 {
					t.Fatal("Antigravity request envelope missing")
				}
				if string(root["project"]) != `"project-1"` || string(root["model"]) != `"antigravity-model"` {
					t.Fatalf("Antigravity routing fields missing: %s", request)
				}
			},
			response: `{"response":{"candidates":[{"content":{"parts":[{"text":"hello from antigravity"}]}}]}}`,
			want:     `"hello from antigravity"`,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("CODING_TEST_KEY", "provider-secret")
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				wantPath := map[string]string{
					"kiro":        "/generateAssistantResponse",
					"iflow":       "/chat/completions",
					"antigravity": "/v1internal:generateContent",
				}[tc.adapter]
				if r.URL.Path != wantPath {
					t.Errorf("path=%s, want %s", r.URL.Path, wantPath)
				}
				if r.Header.Get("Authorization") != "Bearer provider-secret" {
					t.Errorf("authorization=%q", r.Header.Get("Authorization"))
				}
				if r.Header.Get("Cookie") != "" || r.Header.Get("X-Internal-Secret") != "" {
					t.Error("caller credential headers leaked upstream")
				}
				request, err := io.ReadAll(r.Body)
				if err != nil {
					t.Error(err)
				}
				r.Body.Close()
				tc.check(t, request)
				if tc.adapter == "kiro" {
					w.Header().Set("Content-Type", "application/vnd.amazon.eventstream")
				} else {
					w.Header().Set("Content-Type", "application/json")
				}
				_, _ = io.WriteString(w, tc.response)
			}))
			defer server.Close()

			source := config.Source{ID: tc.adapter, Adapter: tc.adapter, BaseURL: server.URL, KeyEnv: "CODING_TEST_KEY", Project: "project-1", MaxInflight: 1}
			client := New(source)
			defer client.Close()
			model := map[string]string{"kiro": "kiro-model", "iflow": "iflow-model", "antigravity": "antigravity-model"}[tc.adapter]
			response, err := client.Do(context.Background(), tc.protocol, model, false, []byte(tc.body), http.Header{
				"Cookie":            []string{"caller-cookie"},
				"X-Internal-Secret": []string{"caller-secret"},
			})
			if err != nil {
				t.Fatal(err)
			}
			defer response.Body.Close()
			out, err := io.ReadAll(response.Body)
			if err != nil {
				t.Fatal(err)
			}
			if !bytes.Contains(out, []byte(tc.want)) {
				t.Fatalf("response=%s, missing %s", out, tc.want)
			}
		})
	}
}

func TestCodingProviderStreams(t *testing.T) {
	tests := []struct {
		name     string
		adapter  string
		protocol string
		body     string
		response string
		want     []string
	}{
		{
			name:     "kiro messages",
			adapter:  "kiro",
			protocol: "messages",
			body:     `{"messages":[{"role":"user","content":"hello"}]}`,
			response: string(makeEventFrame(t, map[string]string{":event-type": "assistantResponseEvent"}, `{"assistantResponseEvent":{"content":"hello","messageId":"m1","status":"in_progress"}}`)) + string(makeEventFrame(t, map[string]string{":event-type": "assistantResponseEvent"}, `{"assistantResponseEvent":{"content":" world","messageId":"m1","status":"completed"}}`)),
			want:     []string{"event: message_start", "hello", " world", "event: message_stop"},
		},
		{
			name:     "iflow messages",
			adapter:  "iflow",
			protocol: "messages",
			body:     `{"system":"Be concise","messages":[{"role":"user","content":"hello"}]}`,
			response: "data: {\"id\":\"m2\",\"model\":\"iflow-model\",\"choices\":[{\"delta\":{\"role\":\"assistant\",\"content\":\"hello\"}}]}\n\ndata: {\"id\":\"m2\",\"choices\":[{\"delta\":{\"content\":\" world\"}}]}\n\ndata: [DONE]\n\n",
			want:     []string{"event: message_start", "hello", " world", "event: message_stop"},
		},
		{
			name:     "antigravity gemini",
			adapter:  "antigravity",
			protocol: "gemini",
			body:     `{"contents":[]}`,
			response: "data: {\"response\":{\"candidates\":[{\"content\":{\"parts\":[{\"text\":\"hello\"}]}}]}}\n\ndata: [DONE]\n\n",
			want:     []string{"data: {\"candidates\":[", "hello", "data: [DONE]"},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("CODING_TEST_KEY", "stream-secret")
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "text/event-stream")
				_, _ = io.WriteString(w, tc.response)
			}))
			defer server.Close()
			client := New(config.Source{Adapter: tc.adapter, BaseURL: server.URL, KeyEnv: "CODING_TEST_KEY", Project: "project-1", MaxInflight: 1})
			defer client.Close()
			response, err := client.Do(context.Background(), tc.protocol, "iflow-model", true, []byte(tc.body), nil)
			if err != nil {
				t.Fatal(err)
			}
			out, err := io.ReadAll(response.Body)
			response.Body.Close()
			if err != nil {
				t.Fatal(err)
			}
			for _, want := range tc.want {
				if !bytes.Contains(out, []byte(want)) {
					t.Errorf("stream=%s, missing %q", out, want)
				}
			}
		})
	}
}

func TestCodingProviderErrorsAndCancellation(t *testing.T) {
	client := New(config.Source{Adapter: "iflow", BaseURL: "http://127.0.0.1:1", KeyEnv: "MISSING_CODING_TEST_KEY", MaxInflight: 1})
	defer client.Close()
	if _, err := client.Do(context.Background(), "chat", "model", false, []byte(`{"messages":[{"role":"user","content":"hi"}]}`), nil); !errors.Is(err, ErrCredential) {
		t.Fatalf("missing credential error=%v", err)
	}
	t.Setenv("CODING_TEST_KEY", "secret")
	client = New(config.Source{Adapter: "iflow", BaseURL: "http://127.0.0.1:1", KeyEnv: "CODING_TEST_KEY", MaxInflight: 1})
	defer client.Close()
	if _, err := client.Do(context.Background(), "gemini", "model", false, []byte(`{"messages":[]}`), nil); !errors.Is(err, ErrProtocol) {
		t.Fatalf("unsupported protocol error=%v", err)
	}

	started := make(chan struct{})
	release := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		close(started)
		select {
		case <-r.Context().Done():
		case <-release:
		}
	}))
	defer func() {
		close(release)
		server.Close()
	}()
	cancelClient := New(config.Source{Adapter: "iflow", BaseURL: server.URL, KeyEnv: "CODING_TEST_KEY", MaxInflight: 1})
	defer cancelClient.Close()
	ctx, cancel := context.WithCancel(context.Background())
	result := make(chan error, 1)
	go func() {
		response, err := cancelClient.Do(ctx, "chat", "model", false, []byte(`{"messages":[{"role":"user","content":"hi"}]}`), nil)
		if response != nil {
			response.Body.Close()
		}
		result <- err
	}()
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("request did not reach test server")
	}
	cancel()
	select {
	case err := <-result:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("cancellation error=%v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("canceled request did not return")
	}
}

func TestCodingRejectsUnsupportedRequestShapes(t *testing.T) {
	tests := []struct {
		name string
		call func() error
	}{
		{
			name: "Kiro unknown top-level field",
			call: func() error {
				_, _, err := kiroRequest("messages", "model", []byte(`{"messages":[{"role":"user","content":"hi"}],"unknown":true}`), "")
				return err
			},
		},
		{
			name: "Kiro unsupported role",
			call: func() error {
				_, _, err := kiroRequest("messages", "model", []byte(`{"messages":[{"role":"developer","content":"hi"}]}`), "")
				return err
			},
		},
		{
			name: "Kiro chat system role",
			call: func() error {
				_, _, err := kiroRequest("chat", "model", []byte(`{"messages":[{"role":"system","content":"be concise"},{"role":"user","content":"hi"}]}`), "")
				return err
			},
		},
		{
			name: "Kiro unsupported message field",
			call: func() error {
				_, _, err := kiroRequest("chat", "model", []byte(`{"messages":[{"role":"user","content":"hi","name":"caller"}]}`), "")
				return err
			},
		},
		{
			name: "Kiro unsupported generation field",
			call: func() error {
				_, _, err := kiroRequest("messages", "model", []byte(`{"messages":[{"role":"user","content":"hi"}],"max_tokens":32}`), "")
				return err
			},
		},
		{
			name: "Kiro unsupported system field",
			call: func() error {
				_, _, err := kiroRequest("messages", "model", []byte(`{"system":"be concise","messages":[{"role":"user","content":"hi"}]}`), "")
				return err
			},
		},
		{
			name: "Kiro unsupported tool definition",
			call: func() error {
				_, _, err := kiroRequest("messages", "model", []byte(`{"messages":[{"role":"user","content":"hi"}],"tools":[{"name":"lookup","input_schema":{"type":"object"}}]}`), "")
				return err
			},
		},
		{
			name: "Kiro unsupported tool result",
			call: func() error {
				_, _, err := kiroRequest("messages", "model", []byte(`{"messages":[{"role":"user","content":[{"type":"tool_result","tool_use_id":"x","content":"done"}]}]}`), "")
				return err
			},
		},
		{
			name: "iFlow unknown top-level field",
			call: func() error {
				_, err := iFlowRequest("chat", "model", false, []byte(`{"messages":[{"role":"user","content":"hi"}],"unknown":true}`))
				return err
			},
		},
		{
			name: "iFlow unsupported messages role",
			call: func() error {
				_, err := iFlowRequest("messages", "model", false, []byte(`{"messages":[{"role":"system","content":"hi"}]}`))
				return err
			},
		},
		{
			name: "iFlow unsupported content block",
			call: func() error {
				_, err := iFlowRequest("messages", "model", false, []byte(`{"messages":[{"role":"user","content":[{"type":"tool_result","tool_use_id":"x","content":"hi"}]}]}`))
				return err
			},
		},
		{
			name: "Antigravity unknown top-level field",
			call: func() error {
				_, err := antigravityRequest("model", []byte(`{"contents":[],"unknown":true}`), "project", "")
				return err
			},
		},
		{
			name: "Antigravity unknown request field",
			call: func() error {
				_, err := antigravityRequest("model", []byte(`{"request":{"contents":[],"unknown":true}}`), "project", "")
				return err
			},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if err := tc.call(); err == nil {
				t.Fatal("unsupported request shape was accepted")
			}
		})
	}
}

func TestKiroPreservesRepresentableFinalImage(t *testing.T) {
	body := []byte(`{"messages":[{"role":"user","content":[{"type":"text","text":"inspect"},{"type":"image","source":{"type":"base64","media_type":"image/png","data":"aGVsbG8="}}]}]}`)
	encoded, _, err := kiroRequest("messages", "model", body, "session-1")
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(encoded, []byte(`"format":"png"`)) || !bytes.Contains(encoded, []byte(`"bytes":"aGVsbG8="`)) {
		t.Fatalf("Kiro image was not preserved: %s", encoded)
	}
	if !bytes.Contains(encoded, []byte(`"content":"inspect"`)) {
		t.Fatalf("Kiro text was not preserved: %s", encoded)
	}
}

func TestKiroRejectsEmptyFinalPrompt(t *testing.T) {
	_, _, err := kiroRequest("messages", "model", []byte(`{"messages":[{"role":"user","content":""}]}`), "")
	if err == nil || !strings.Contains(err.Error(), "text or an image") {
		t.Fatalf("empty Kiro prompt error=%v", err)
	}
}

func TestCodingPreservesUpstreamStatusAndRejectsCorruptEventStream(t *testing.T) {
	t.Setenv("CODING_TEST_KEY", "secret")
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusTooManyRequests)
		_, _ = io.WriteString(w, `{"error":"quota"}`)
	}))
	client := New(config.Source{Adapter: "iflow", BaseURL: server.URL, KeyEnv: "CODING_TEST_KEY", MaxInflight: 1})
	response, err := client.Do(context.Background(), "chat", "model", false, []byte(`{"messages":[{"role":"user","content":"hi"}]}`), nil)
	if err != nil {
		t.Fatal(err)
	}
	if response.StatusCode != http.StatusTooManyRequests {
		t.Fatalf("status=%d, want %d", response.StatusCode, http.StatusTooManyRequests)
	}
	response.Body.Close()
	client.Close()

	frame := makeEventFrame(t, map[string]string{":event-type": "assistantResponseEvent"}, `{"assistantResponseEvent":{"content":"hello"}}`)
	frame[len(frame)-1] ^= 1
	reader := newEventReader(bytes.NewReader(frame))
	if _, err := reader.Next(); err == nil || !strings.Contains(err.Error(), "CRC") {
		t.Fatalf("corrupt frame error=%v", err)
	}
}

func makeEventFrame(t *testing.T, headers map[string]string, payload string) []byte {
	t.Helper()
	var encodedHeaders bytes.Buffer
	for name, value := range headers {
		if len(name) > 255 {
			t.Fatal("event header name too long")
		}
		encodedHeaders.WriteByte(byte(len(name)))
		encodedHeaders.WriteString(name)
		encodedHeaders.WriteByte(7) // AWS Event Stream string header.
		if len(value) > 65535 {
			t.Fatal("event header value too long")
		}
		var length [2]byte
		binary.BigEndian.PutUint16(length[:], uint16(len(value)))
		encodedHeaders.Write(length[:])
		encodedHeaders.WriteString(value)
	}
	headerBytes := encodedHeaders.Bytes()
	payloadBytes := []byte(payload)
	total := 12 + len(headerBytes) + len(payloadBytes) + 4
	frame := make([]byte, total)
	binary.BigEndian.PutUint32(frame[0:4], uint32(total))
	binary.BigEndian.PutUint32(frame[4:8], uint32(len(headerBytes)))
	binary.BigEndian.PutUint32(frame[8:12], crc32.ChecksumIEEE(frame[:8]))
	copy(frame[12:], headerBytes)
	copy(frame[12+len(headerBytes):], payloadBytes)
	binary.BigEndian.PutUint32(frame[total-4:], crc32.ChecksumIEEE(frame[:total-4]))
	return frame
}
