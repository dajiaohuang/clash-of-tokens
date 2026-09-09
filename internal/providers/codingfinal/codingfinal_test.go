package codingfinal

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/hex"
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

func finalSource(adapter, base string) config.Source {
	return config.Source{ID: adapter + "-test", Adapter: adapter, BaseURL: base, KeyEnv: "COT_CODINGFINAL_TEST_KEY", MaxInflight: 1}
}
func chatBody() []byte { return []byte(`{"messages":[{"role":"user","content":"hello"}]}`) }

func TestCursorDirectConnect(t *testing.T) {
	t.Setenv("COT_CODINGFINAL_TEST_KEY", "cursor-secret")
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != cursorRunPath || r.Header.Get("Authorization") != "Bearer cursor-secret" {
			t.Errorf("request=%s auth=%q", r.URL, r.Header.Get("Authorization"))
		}
		if r.Header.Get("Cookie") != "" || r.Header.Get("X-Internal-Secret") != "" {
			t.Error("caller credentials forwarded")
		}
		if r.Header.Get("Content-Type") != "application/connect+proto" || r.Header.Get("Connect-Protocol-Version") != "1" {
			t.Error("Connect headers missing")
		}
		if _, _, err := (&connectReader{reader: r.Body}).next(); err != nil {
			t.Errorf("request frame=%v", err)
		}
		text := encodeMessageField(1, encodeStringField(1, "hello"))
		update := encodeMessageField(1, text)
		terminal := encodeVarintField(14, 1)
		payload := encodeMessageField(1, update)
		payload = append(payload, encodeMessageField(1, terminal)...)
		payload = append(payload, encodeMessageField(4, []byte("checkpoint"))...)
		frame, _ := connectFrame(payload, 0)
		end, _ := connectFrame([]byte(`{}`), connectFlagEndStream)
		w.Header().Set("Content-Type", "application/connect+proto")
		_, _ = w.Write(append(frame, end...))
	}))
	defer server.Close()
	c := New(finalSource(AdapterCursor, server.URL))
	defer c.Close()
	resp, err := c.Do(context.Background(), "chat", "composer-2.5", true, chatBody(), http.Header{"Cookie": []string{"caller-secret"}})
	if err != nil {
		t.Fatal(err)
	}
	data, err := io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	if err != nil {
		t.Fatalf("stream=%q err=%v", data, err)
	}
	if !bytes.Contains(data, []byte(`"content":"hello"`)) || !bytes.Contains(data, []byte("data: [DONE]")) {
		t.Fatalf("stream=%s", data)
	}
	resp, err = c.Do(context.Background(), "chat", "composer-2.5", false, chatBody(), nil)
	if err != nil {
		t.Fatal(err)
	}
	data, err = io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	if err != nil {
		t.Fatal(err)
	}
	var completion map[string]any
	if json.Unmarshal(data, &completion) != nil || completion["object"] != "chat.completion" {
		t.Fatalf("non-stream=%s", data)
	}
}

func TestWarpDirectProtobufAndSSE(t *testing.T) {
	t.Setenv("COT_CODINGFINAL_TEST_KEY", "warp-jwt")
	serverDone := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer close(serverDone)
		if r.URL.Path != "/ai/multi-agent" || r.Header.Get("Authorization") != "Bearer warp-jwt" || r.Header.Get("Content-Type") != "application/x-protobuf" {
			t.Errorf("Warp request=%s auth=%q content-type=%q", r.URL, r.Header.Get("Authorization"), r.Header.Get("Content-Type"))
		}
		request, _ := io.ReadAll(r.Body)
		fields, err := decodeProtoFields(request)
		if err != nil {
			t.Errorf("Warp request protobuf=%v", err)
		}
		if _, ok := firstField(fields, 2, 2); !ok {
			t.Error("Warp request missing input")
		}
		agent := encodeMessageField(3, encodeStringField(1, "warp hello"))
		appendAction := encodeMessageField(1, agent)
		clientActions := encodeMessageField(1, encodeMessageField(5, appendAction))
		first := encodeMessageField(2, clientActions)
		var debugOut bytes.Buffer
		debugRole := false
		if err := warpClientActions(&debugOut, clientActions, "id", 1, "m", &debugRole); err != nil {
			t.Errorf("Warp action parser=%v", err)
		}
		if !debugRole {
			t.Errorf("Warp action parser did not find assistant output: %x", first)
		}
		usage := encodeMessageField(8, encodeVarintField(2, 2), encodeVarintField(3, 3))
		finished := encodeMessageField(3, usage, encodeMessageField(2))
		w.Header().Set("Content-Type", "text/event-stream")
		enc := base64.RawStdEncoding
		_, _ = io.WriteString(w, "data: "+enc.EncodeToString(first)+"\n\n")
		_, _ = io.WriteString(w, "data: "+enc.EncodeToString(finished)+"\n\n")
		if f, ok := w.(http.Flusher); ok {
			f.Flush()
		}
		<-r.Context().Done()
	}))
	defer server.Close()
	c := New(finalSource(AdapterWarp, server.URL))
	defer c.Close()
	resp, err := c.Do(context.Background(), "chat", "gpt-5", true, chatBody(), nil)
	if err != nil {
		t.Fatal(err)
	}
	data, err := io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	if err != nil || !bytes.Contains(data, []byte(`"content":"warp hello"`)) || !bytes.Contains(data, []byte(`"total_tokens":5`)) {
		t.Fatalf("Warp stream=%s err=%v", data, err)
	}
	select {
	case <-serverDone:
	case <-time.After(time.Second):
		t.Fatal("Warp upstream body was not closed")
	}
}

func TestWarpRequestWireGolden(t *testing.T) {
	req, err := decodeChatRequest(chatBody())
	if err != nil {
		t.Fatal(err)
	}
	got := hex.EncodeToString(mustWarpRequest(t, req, "gpt-5", "conversation-1"))
	want := "120d320b0a090a070a0568656c6c6f1a150a110a056770742d3512026f331a046175746f580122100a0e636f6e766572736174696f6e2d31"
	if got != want {
		t.Fatalf("Warp request wire changed:\ngot  %s\nwant %s", got, want)
	}
}

func TestWarpRejectsUnpinnedModelAndHistory(t *testing.T) {
	req, err := decodeChatRequest(chatBody())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := EncodeWarpRequest(req, "provider/private-model", "conversation-1"); !errors.Is(err, ErrUnsupported) {
		t.Fatalf("unknown model err=%v", err)
	}
	req.Messages = append(req.Messages, chatMessage{Role: "assistant", Content: "old"})
	if _, err := EncodeWarpRequest(req, "gpt-5", "conversation-1"); !errors.Is(err, ErrUnsupported) {
		t.Fatalf("history err=%v", err)
	}
}

func mustWarpRequest(t *testing.T, req chatRequest, model, conversation string) []byte {
	t.Helper()
	data, err := EncodeWarpRequest(req, model, conversation)
	if err != nil {
		t.Fatal(err)
	}
	return data
}

func TestWindsurfAuthAndConnect(t *testing.T) {
	t.Setenv("COT_CODINGFINAL_TEST_KEY", "windsurf-secret")
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		switch r.URL.Path {
		case windsurfAuthPath:
			if r.Header.Get("Content-Type") != "application/proto" {
				t.Error("auth content type")
			}
			_, _ = w.Write(encodeStringField(1, "jwt-secret"))
		case windsurfChatPath:
			if r.Header.Get("Content-Type") != "application/connect+proto" {
				t.Error("chat content type")
			}
			if _, _, err := (&connectReader{reader: r.Body}).next(); err != nil {
				t.Errorf("request frame=%v", err)
			}
			text := encodeStringField(3, "wind hello")
			usage := encodeMessageField(7, encodeVarintField(2, 2), encodeVarintField(3, 1))
			frame, _ := connectFrame(append(text, usage...), 0)
			end, _ := connectFrame([]byte(`{}`), connectFlagEndStream)
			w.Header().Set("Content-Type", "application/connect+proto")
			_, _ = w.Write(append(frame, end...))
		default:
			t.Errorf("unexpected path %s", r.URL.Path)
		}
	}))
	defer server.Close()
	c := New(finalSource(AdapterWindsurf, server.URL))
	defer c.Close()
	resp, err := c.Do(context.Background(), "chat", "m", true, chatBody(), nil)
	if err != nil {
		t.Fatal(err)
	}
	data, err := io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	if err != nil {
		t.Fatalf("stream=%q err=%v", data, err)
	}
	if !bytes.Contains(data, []byte(`"content":"wind hello"`)) || !bytes.Contains(data, []byte(`"total_tokens":3`)) {
		t.Fatalf("stream=%s", data)
	}
	if calls.Load() != 2 {
		t.Fatalf("calls=%d", calls.Load())
	}
}

func TestWindsurfAuthWireGolden(t *testing.T) {
	got := hex.EncodeToString(EncodeWindsurfAuthRequest("token", "session"))
	want := "0a3b0a0877696e64737572661206312e34382e321a05746f6b656e2205656e2d55533a06332e362e3237520773657373696f6e620877696e6473757266"
	if got != want {
		t.Fatalf("Windsurf auth wire changed:\ngot  %s\nwant %s", got, want)
	}
}

func TestCursorRequestUsesPinnedAgentFields(t *testing.T) {
	framed, err := BuildCursorRequest(chatBody(), "composer-2.5", "conversation-1")
	if err != nil {
		t.Fatal(err)
	}
	reader := &connectReader{reader: bytes.NewReader(framed)}
	flags, payload, err := reader.next()
	if err != nil || flags != 0 {
		t.Fatalf("Cursor frame flags=%d err=%v", flags, err)
	}
	root, err := decodeProtoFields(payload)
	if err != nil {
		t.Fatal(err)
	}
	run, ok := firstField(root, 1, 2)
	if !ok {
		t.Fatal("missing AgentService.Run request field")
	}
	runFields, err := decodeProtoFields(run.bytes)
	if err != nil {
		t.Fatal(err)
	}
	if field, ok := firstField(runFields, 5, 2); !ok || string(field.bytes) != "conversation-1" {
		t.Fatalf("conversation field=%v", field)
	}
	requested, ok := firstField(runFields, 9, 2)
	if !ok {
		t.Fatal("missing requested model field")
	}
	requestedFields, err := decodeProtoFields(requested.bytes)
	if err != nil {
		t.Fatal(err)
	}
	modelField, ok := firstField(requestedFields, 1, 2)
	if !ok || string(modelField.bytes) != "composer-2.5" {
		t.Fatalf("requested model=%q", modelField.bytes)
	}
	action, ok := firstField(runFields, 2, 2)
	if !ok {
		t.Fatal("missing action field")
	}
	actionFields, err := decodeProtoFields(action.bytes)
	if err != nil {
		t.Fatal(err)
	}
	user, ok := firstField(actionFields, 1, 2)
	if !ok {
		t.Fatal("missing user action")
	}
	userFields, err := decodeProtoFields(user.bytes)
	if err != nil {
		t.Fatal(err)
	}
	message, ok := firstField(userFields, 1, 2)
	if !ok {
		t.Fatal("missing user message")
	}
	messageFields, err := decodeProtoFields(message.bytes)
	if err != nil {
		t.Fatal(err)
	}
	textField, ok := firstField(messageFields, 1, 2)
	if !ok || string(textField.bytes) != "hello" {
		t.Fatalf("user text=%q", textField.bytes)
	}
}

func TestCursorFlattenPreservesRoleLabels(t *testing.T) {
	got := flattenCursorMessages([]chatMessage{
		{Role: "user", Content: "ask"},
		{Role: "system", Content: "rules"},
		{Role: "assistant", Content: "answer", ToolCalls: []toolCall{{ID: "call-1", Name: "lookup", Arguments: `{"q":"x"}`}}},
		{Role: "tool", ToolCallID: "call-1", Content: "result"},
	})
	want := "rules\n\nUser: ask\n\nAssistant: answer\n\nAssistant called tool lookup (call-1) with arguments: {\"q\":\"x\"}\n\nTool result (call-1): result"
	if got != want {
		t.Fatalf("flattened Cursor messages=%q, want %q", got, want)
	}
}

func TestQoderDirectHTTPAndErrorEnvelope(t *testing.T) {
	t.Setenv("COT_CODINGFINAL_TEST_KEY", "qoder-secret")
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != qoderDefaultEndpoint || r.Header.Get("Authorization") != "Bearer qoder-secret" {
			t.Errorf("url/auth=%s/%q", r.URL, r.Header.Get("Authorization"))
		}
		if r.Header.Get("X-Dashscope-Authtype") != "qwen-oauth" || r.Header.Get("X-Stainless-Lang") != "js" {
			t.Error("Qoder headers missing")
		}
		var payload map[string]any
		if json.NewDecoder(r.Body).Decode(&payload) != nil {
			t.Error("invalid JSON")
		}
		if payload["model"] != "coder-model" || payload["stream"] != true {
			t.Errorf("payload=%v", payload)
		}
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w, "data: {\"id\":\"q\",\"choices\":[{\"delta\":{\"content\":\"qoder\"}}]}\n\ndata: {\"choices\":[{\"delta\":{},\"finish_reason\":\"stop\"}]}\n\ndata: [DONE]\n\n")
	}))
	defer server.Close()
	c := New(finalSource(AdapterQoder, server.URL))
	defer c.Close()
	resp, err := c.Do(context.Background(), "chat", "qwen3.5-plus", true, chatBody(), nil)
	if err != nil {
		t.Fatal(err)
	}
	data, err := io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	if err != nil {
		t.Fatalf("stream=%q err=%v", data, err)
	}
	if !bytes.Contains(data, []byte(`"content":"qoder"`)) {
		t.Fatalf("stream=%s", data)
	}
}

func TestTraeDirectSessionAndSSE(t *testing.T) {
	t.Setenv("COT_CODINGFINAL_TEST_KEY", "trae-jwt")
	var createSeen, eventsSeen atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/chat_sessions":
			createSeen.Add(1)
			if r.Header.Get("Authorization") != "Cloud-IDE-JWT trae-jwt" || r.Header.Get("X-Trae-Client-Type") != "web" {
				t.Errorf("Trae auth headers=%v", r.Header)
			}
			var body map[string]any
			if json.NewDecoder(r.Body).Decode(&body) != nil {
				t.Fatal("invalid Trae create body")
			}
			initial, ok := body["initial_message"].(map[string]any)
			if !ok || initial["agent_type"] != "solo_agent_remote" || initial["model_selection_strategy"] != "manual" {
				t.Fatalf("Trae initial message=%v", initial)
			}
			_, _ = io.WriteString(w, `{"code":0,"data":{"chat_session_id":"s1","message_id":"m1"}}`)
		case "/chat_sessions/s1/events":
			eventsSeen.Add(1)
			if r.URL.Query().Get("reply_to_message_id") != "m1" {
				t.Errorf("reply id=%q", r.URL.Query().Get("reply_to_message_id"))
			}
			w.Header().Set("Content-Type", "text/event-stream")
			_, _ = io.WriteString(w, "event: plan_item\ndata: {\"id\":\"p\",\"thought\":\"he\"}\n\n")
			_, _ = io.WriteString(w, "event: plan_item\ndata: {\"id\":\"p\",\"thought\":\"hello\"}\n\n")
			_, _ = io.WriteString(w, "event: token_usage\ndata: {\"prompt_tokens\":2,\"completion_tokens\":3,\"total_tokens\":5}\n\n")
			_, _ = io.WriteString(w, "event: done\ndata: {}\n\n")
		default:
			t.Errorf("unexpected Trae path %s", r.URL.Path)
		}
	}))
	defer server.Close()
	c := New(finalSource(AdapterTrae, server.URL))
	defer c.Close()
	body := []byte(`{"model":"gpt-5.2","messages":[{"role":"user","content":"hello"}]}`)
	resp, err := c.Do(context.Background(), "chat", "gpt-5.2", true, body, nil)
	if err != nil {
		t.Fatal(err)
	}
	data, err := io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	if err != nil {
		t.Fatalf("Trae stream=%q err=%v", data, err)
	}
	if createSeen.Load() != 1 || eventsSeen.Load() != 1 || !bytes.Contains(data, []byte(`"content":"he"`)) || !bytes.Contains(data, []byte(`"content":"llo"`)) || !bytes.Contains(data, []byte(`"total_tokens":5`)) {
		t.Fatalf("Trae calls=%d/%d stream=%s", createSeen.Load(), eventsSeen.Load(), data)
	}
}

func TestV0DirectCookieAndSSE(t *testing.T) {
	t.Setenv("COT_CODINGFINAL_TEST_KEY", "session-cookie=abc")
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != v0ChatPath || r.Header.Get("Cookie") != "session-cookie=abc" || r.Header.Get("Authorization") != "" {
			t.Errorf("v0 request path/cookies=%s/%q/%q", r.URL.Path, r.Header.Get("Cookie"), r.Header.Get("Authorization"))
		}
		var payload map[string]any
		if json.NewDecoder(r.Body).Decode(&payload) != nil || payload["model"] != "v0-default" || payload["stream"] != true {
			t.Errorf("v0 payload=%v", payload)
		}
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w, "data: {\"id\":\"v0\",\"choices\":[{\"delta\":{\"content\":\"v0\"},\"finish_reason\":null}]}\n\n")
		_, _ = io.WriteString(w, "data: {\"choices\":[{\"delta\":{},\"finish_reason\":\"stop\"}]}\n\ndata: [DONE]\n\n")
	}))
	defer server.Close()
	c := New(finalSource(AdapterV0, server.URL))
	defer c.Close()
	resp, err := c.Do(context.Background(), "chat", "v0-default", true, chatBody(), http.Header{"Cookie": []string{"caller-cookie"}})
	if err != nil {
		t.Fatal(err)
	}
	data, err := io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	if err != nil || !bytes.Contains(data, []byte(`"content":"v0"`)) || !bytes.Contains(data, []byte("data: [DONE]")) {
		t.Fatalf("v0 stream=%s err=%v", data, err)
	}
}

func TestV0StreamStopsAtDoneWithoutEOF(t *testing.T) {
	t.Setenv("COT_CODINGFINAL_TEST_KEY", "session-cookie=abc")
	serverDone := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer close(serverDone)
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w, "data: {\"id\":\"v0\",\"choices\":[{\"delta\":{\"content\":\"done\"},\"finish_reason\":null}]}\n\n")
		_, _ = io.WriteString(w, "data: {\"choices\":[{\"delta\":{},\"finish_reason\":\"stop\"}]}\n\ndata: [DONE]\n\n")
		if f, ok := w.(http.Flusher); ok {
			f.Flush()
		}
		<-r.Context().Done()
	}))
	defer server.Close()
	c := New(finalSource(AdapterV0, server.URL))
	defer c.Close()
	resp, err := c.Do(context.Background(), "chat", "v0-default", true, chatBody(), nil)
	if err != nil {
		t.Fatal(err)
	}
	readDone := make(chan struct{})
	var data []byte
	var readErr error
	go func() {
		data, readErr = io.ReadAll(resp.Body)
		close(readDone)
	}()
	select {
	case <-readDone:
	case <-time.After(time.Second):
		t.Fatal("v0 converter waited for upstream EOF after [DONE]")
	}
	_ = resp.Body.Close()
	if readErr != nil || !bytes.Contains(data, []byte(`"content":"done"`)) {
		t.Fatalf("stream=%s err=%v", data, readErr)
	}
	select {
	case <-serverDone:
	case <-time.After(time.Second):
		t.Fatal("v0 upstream body was not closed")
	}
}

func TestAdaptersRejectUnverifiedSessionContinuation(t *testing.T) {
	for _, adapter := range []string{AdapterCursor, AdapterWindsurf, AdapterDevinDesktop, AdapterTrae, AdapterV0, AdapterWarp} {
		t.Run(adapter, func(t *testing.T) {
			t.Setenv("COT_CODINGFINAL_TEST_KEY", "credential")
			c := New(finalSource(adapter, "https://example.test"))
			defer c.Close()
			_, err := c.Do(context.Background(), "chat", "m", true, chatBody(), http.Header{"X-COT-Session": []string{"same"}})
			if !errors.Is(err, ErrUnsupported) {
				t.Fatalf("err=%v", err)
			}
		})
	}
}

func TestStreamErrorsDoNotExposeRawPayloads(t *testing.T) {
	secret := "Bearer super-secret-token"
	err := parseStreamError(map[string]any{"error": map[string]any{"raw": secret}})
	if err == nil || strings.Contains(err.Error(), secret) {
		t.Fatalf("raw error leaked: %v", err)
	}
	err = parseStreamError(map[string]any{"statusCodeValue": float64(401), "body": `{"access_token":"super-secret-token"}`})
	if err == nil || strings.Contains(err.Error(), "super-secret-token") {
		t.Fatalf("status body leaked: %v", err)
	}
}

func TestCursorTerminalDoesNotWaitForEOF(t *testing.T) {
	t.Setenv("COT_CODINGFINAL_TEST_KEY", "cursor-secret")
	serverDone := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer close(serverDone)
		if _, _, err := (&connectReader{reader: r.Body}).next(); err != nil {
			t.Errorf("request frame=%v", err)
			return
		}
		text := encodeMessageField(1, encodeStringField(1, "done"))
		update := encodeMessageField(1, text)
		payload := encodeMessageField(1, update)
		frame, _ := connectFrame(payload, 0)
		end, _ := connectFrame([]byte(`{}`), connectFlagEndStream)
		w.Header().Set("Content-Type", "application/connect+proto")
		if f, ok := w.(http.Flusher); ok {
			_, _ = w.Write(append(frame, end...))
			f.Flush()
		} else {
			_, _ = w.Write(append(frame, end...))
		}
		<-r.Context().Done()
	}))
	defer server.Close()
	c := New(finalSource(AdapterCursor, server.URL))
	defer c.Close()
	resp, err := c.Do(context.Background(), "chat", "m", true, chatBody(), nil)
	if err != nil {
		t.Fatal(err)
	}
	readDone := make(chan struct{})
	var data []byte
	var readErr error
	go func() {
		data, readErr = io.ReadAll(resp.Body)
		close(readDone)
	}()
	select {
	case <-readDone:
	case <-time.After(time.Second):
		t.Fatal("Cursor converter waited for upstream EOF after terminal frame")
	}
	_ = resp.Body.Close()
	if readErr != nil || !bytes.Contains(data, []byte(`"content":"done"`)) {
		t.Fatalf("stream=%s err=%v", data, readErr)
	}
	select {
	case <-serverDone:
	case <-time.After(time.Second):
		t.Fatal("Cursor upstream body was not closed")
	}
}

func TestFinalRejectsUnsupportedAndRequiresCompletion(t *testing.T) {
	if !Supports(AdapterWarp) || !Supports(AdapterZCode) || Supports("unknown") {
		t.Fatal("Warp/ZCode were not reported as native, or unknown was reported as native")
	}
	c := New(config.Source{Adapter: AdapterZCode})
	_, err := c.Do(context.Background(), "chat", "m", false, chatBody(), nil)
	if !errors.Is(err, ErrUnsupported) {
		t.Fatalf("err=%v", err)
	}
	_, err = cursorToSSE(context.Background(), bytes.NewReader(func() []byte { f, _ := connectFrame([]byte{}, 0); return f }()), "m")
	if !errors.Is(err, ErrTruncated) {
		t.Fatalf("truncated err=%v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err = cursorToSSE(ctx, strings.NewReader(""), "m")
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("cancel err=%v", err)
	}
}

func TestFinalSessionContinuationRejected(t *testing.T) {
	t.Setenv("COT_CODINGFINAL_TEST_KEY", "qoder-secret")
	c := New(finalSource(AdapterQoder, "https://example.test"))
	defer c.Close()
	_, err := c.Do(context.Background(), "chat", "m", true, chatBody(), http.Header{"X-COT-Session": []string{"same"}})
	if err == nil || !errors.Is(err, ErrUnsupported) {
		t.Fatalf("session continuation err=%v", err)
	}
}
