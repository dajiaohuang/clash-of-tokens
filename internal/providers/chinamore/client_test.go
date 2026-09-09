package chinamore

import (
	"bytes"
	"context"
	"encoding/binary"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"clash-of-tokens/internal/config"
)

const moreRequest = `{"model":"wrong-body-model","messages":[{"role":"user","content":"hello"}]}`

func moreSource(adapter, base string) config.Source {
	return config.Source{ID: adapter, Adapter: adapter, BaseURL: base, KeyEnv: "COT_CHINAMORE_TEST_KEY", MaxInflight: 2}
}

func writeMoreSSE(w http.ResponseWriter, events ...string) {
	w.Header().Set("Content-Type", "text/event-stream")
	for _, event := range events {
		_, _ = io.WriteString(w, event+"\n\n")
		if f, ok := w.(http.Flusher); ok {
			f.Flush()
		}
	}
}

func TestMiniMaxWebContractAuthSigningPollingAndModelAuthority(t *testing.T) {
	t.Setenv("COT_CHINAMORE_TEST_KEY", `{"token":"jwt-token","realUserID":"user-1"}`)
	var mu sync.Mutex
	var paths []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		paths = append(paths, r.URL.Path)
		mu.Unlock()
		if r.Header.Get("Cookie") != "" || r.Header.Get("token") != "jwt-token" || r.Header.Get("x-signature") == "" || r.Header.Get("yy") == "" {
			t.Fatalf("credential/signature forwarding=%v", r.Header)
		}
		body, _ := io.ReadAll(r.Body)
		switch r.URL.Path {
		case minimaxRegisterPath:
			var v map[string]any
			if json.Unmarshal(body, &v) != nil || v["uuid"] == "" || r.URL.Query().Get("user_id") != "user-1" {
				t.Fatalf("register request=%s url=%s", body, r.URL.String())
			}
			_, _ = io.WriteString(w, `{"statusInfo":{"code":0},"data":{"deviceIDStr":"device-1","realUserID":"user-1"}}`)
		case minimaxSendPath:
			var v map[string]any
			if json.Unmarshal(body, &v) != nil || v["text"] != "user:hello\n" {
				t.Fatalf("send request=%s", body)
			}
			_, _ = io.WriteString(w, `{"base_resp":{"status_code":0},"chat_id":"chat-1","msg_id":"msg-1"}`)
		case minimaxDetailPath:
			_, _ = io.WriteString(w, `{"base_resp":{"status_code":0},"messages":[{"msg_type":2,"msg_id":"msg-1","msg_content":"hello from minimax","extra_info":{"thinking_content":"private reasoning"}}],"chat":{"chat_status":2}}`)
		default:
			t.Fatalf("unexpected MiniMax path %s", r.URL.Path)
		}
	}))
	defer server.Close()
	c := New(moreSource(AdapterMiniMax, server.URL))
	defer c.Close()
	resp, err := c.Do(context.Background(), "chat", "default", true, []byte(moreRequest), http.Header{"Cookie": []string{"caller-cookie"}})
	if err != nil {
		t.Fatal(err)
	}
	b, err := io.ReadAll(resp.Body)
	resp.Body.Close()
	if err != nil || !bytes.Contains(b, []byte(`"content":"hello from minimax"`)) || !bytes.Contains(b, []byte(`"reasoning_content":"private reasoning"`)) || !bytes.Contains(b, []byte("[DONE]")) {
		t.Fatalf("stream=%s err=%v", b, err)
	}
	resp, err = c.Do(context.Background(), "chat", "default", false, []byte(moreRequest), nil)
	if err != nil {
		t.Fatal(err)
	}
	b, err = io.ReadAll(resp.Body)
	resp.Body.Close()
	if err != nil || !bytes.Contains(b, []byte(`"object":"chat.completion"`)) {
		t.Fatalf("nonstream=%s err=%v", b, err)
	}
	mu.Lock()
	defer mu.Unlock()
	if len(paths) != 5 || paths[0] != minimaxRegisterPath || paths[1] != minimaxSendPath || paths[2] != minimaxDetailPath || paths[3] != minimaxSendPath || paths[4] != minimaxDetailPath {
		t.Fatalf("paths=%v", paths)
	}
}

func TestMimoWebContractCookieIsolationAndSSEConversion(t *testing.T) {
	t.Setenv("COT_CHINAMORE_TEST_KEY", `{"service_token":"service","user_id":"user","ph_token":"ph"}`)
	var sawQuery bool
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Cookie") != "serviceToken=service; userId=user; xiaomichatbot_ph=ph" || strings.Contains(r.Header.Get("Cookie"), "caller") {
			t.Fatalf("Mimo cookie forwarding=%q", r.Header.Get("Cookie"))
		}
		if r.URL.Query().Get("xiaomichatbot_ph") == "ph" {
			sawQuery = true
		}
		body, _ := io.ReadAll(r.Body)
		if r.URL.Path == "/open-apis/chat/conversation/save" {
			var v map[string]any
			if json.Unmarshal(body, &v) != nil || v["conversationId"] == "" {
				t.Fatalf("save payload=%s", body)
			}
			_, _ = io.WriteString(w, `{"code":0}`)
			return
		}
		if r.URL.Path != mimoChatPath {
			t.Fatalf("unexpected Mimo path %s", r.URL.Path)
		}
		var v map[string]any
		if json.Unmarshal(body, &v) != nil || v["query"] != "hello" || v["modelConfig"].(map[string]any)["model"] != "mimo-v2.5-pro" {
			t.Fatalf("chat payload=%s", body)
		}
		writeMoreSSE(w, "event: message\ndata: {\"content\":\"mimo answer\"}", "event: finish\ndata: {\"done\":true}")
	}))
	defer server.Close()
	c := New(moreSource(AdapterMimo, server.URL))
	defer c.Close()
	resp, err := c.Do(context.Background(), "chat", "MiMo-V2.5-Pro", false, []byte(moreRequest), http.Header{"Cookie": []string{"caller-cookie"}})
	if err != nil {
		t.Fatal(err)
	}
	b, err := io.ReadAll(resp.Body)
	resp.Body.Close()
	if err != nil || !bytes.Contains(b, []byte(`"content":"mimo answer"`)) {
		t.Fatalf("output=%s err=%v", b, err)
	}
	if !sawQuery {
		t.Fatal("Mimo ph token query was not sent")
	}
}

func TestStepChatConnectContractTokenExchangeAndFramedSSE(t *testing.T) {
	t.Setenv("COT_CHINAMORE_TEST_KEY", "refresh-token")
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Cookie") == "" || strings.Contains(r.Header.Get("Cookie"), "caller") {
			t.Fatalf("StepChat credential forwarding=%q", r.Header.Get("Cookie"))
		}
		body, _ := io.ReadAll(r.Body)
		switch r.URL.Path {
		case stepRegisterPath:
			_, _ = io.WriteString(w, `{"accessToken":{"raw":"access"},"refreshToken":{"raw":"refresh"},"device":{"deviceID":"device"}}`)
		case stepCreatePath:
			var v map[string]any
			if json.Unmarshal(body, &v) != nil || v["chatName"] != "新会话" {
				t.Fatalf("create body=%s", body)
			}
			_, _ = io.WriteString(w, `{"chatId":"chat-1"}`)
		case stepSendPath:
			payload, err := nextConnectFrame(bytes.NewReader(body))
			if err != nil {
				t.Fatal(err)
			}
			var v map[string]any
			if json.Unmarshal(payload, &v) != nil || v["chatId"] != "chat-1" || !strings.Contains(v["messageInfo"].(map[string]any)["text"].(string), "user:hello") {
				t.Fatalf("send frame=%s", payload)
			}
			w.Header().Set("Content-Type", "application/connect+json")
			_, _ = w.Write(connectFrame([]byte(`{"textEvent":{"text":"step answer"}}`)))
			_, _ = w.Write(connectFrame([]byte(`{"doneEvent":{}}`)))
		case stepDeletePath:
			_, _ = io.WriteString(w, `{}`)
		default:
			t.Fatalf("unexpected StepChat path %s", r.URL.Path)
		}
	}))
	defer server.Close()
	c := New(moreSource(AdapterStep, server.URL))
	defer c.Close()
	resp, err := c.Do(context.Background(), "chat", "default", true, []byte(moreRequest), http.Header{"Cookie": []string{"caller-cookie"}})
	if err != nil {
		t.Fatal(err)
	}
	b, err := io.ReadAll(resp.Body)
	resp.Body.Close()
	if err != nil || !bytes.Contains(b, []byte(`"content":"step answer"`)) || !bytes.Contains(b, []byte("[DONE]")) {
		t.Fatalf("stream=%s err=%v", b, err)
	}
}

func TestChinaMoreRejectsUnsupportedSemanticsAndCredentialShapes(t *testing.T) {
	t.Setenv("COT_CHINAMORE_TEST_KEY", `{"token":"jwt","realUserID":"u"}`)
	c := New(moreSource(AdapterMiniMax, "http://127.0.0.1:1"))
	defer c.Close()
	for _, body := range []string{
		`{"model":"m","messages":[{"role":"user","content":"x"}],"temperature":0}`,
		`{"model":"m","messages":[{"role":"user","content":"x"}],"web_search":true}`,
		`{"model":"m","messages":[{"role":"user","content":"x"}],"enable_thinking":true}`,
		`{"model":"m","messages":[{"role":"user","content":"x"}],"tools":[]}`,
		`{"model":"m","messages":[{"role":"user","content":"x"},{"role":"assistant","content":"y"}]}`,
		`{"model":"m","messages":[{"role":"user","content":[{"type":"image_url"}]}]}`,
	} {
		if _, err := c.Do(context.Background(), "chat", "m", false, []byte(body), nil); !errors.Is(err, ErrUnsupported) {
			t.Fatalf("body=%s err=%v", body, err)
		}
	}
	t.Setenv("COT_CHINAMORE_TEST_KEY", "refresh-a,refresh-b")
	if _, err := parseStepCredential("refresh-a,refresh-b"); !errors.Is(err, ErrCredential) {
		t.Fatalf("step multi-token err=%v", err)
	}
	if _, err := parseMimoCredential("raw-cookie"); !errors.Is(err, ErrCredential) {
		t.Fatalf("mimo raw credential err=%v", err)
	}
	if _, err := parseMiniCredential(`{"token":"not-a-jwt"}`); !errors.Is(err, ErrCredential) {
		t.Fatalf("minimax missing user err=%v", err)
	}
}

func TestChinaMoreFrameAndBaseURLGuards(t *testing.T) {
	if _, err := baseURL(config.Source{BaseURL: "http://example.com"}, defaultStepBase); err == nil {
		t.Fatal("external HTTP base URL accepted")
	}
	if got, err := baseURL(config.Source{BaseURL: "http://127.0.0.1:8080"}, defaultStepBase); err != nil || got != "http://127.0.0.1:8080" {
		t.Fatalf("loopback base=%q err=%v", got, err)
	}
	if got, err := baseURL(config.Source{BaseURL: "https://example.com"}, defaultStepBase); err != nil || got != "https://example.com" {
		t.Fatalf("HTTPS base=%q err=%v", got, err)
	}
	for _, bad := range [][]byte{{1, 0, 0, 0, 1, '{'}, {0, 0, 0, 0, 3, '{'}} {
		if _, err := nextConnectFrame(bytes.NewReader(bad)); err == nil {
			t.Fatalf("invalid frame accepted: %v", bad)
		}
	}
	if got := connectFrame([]byte(`{"x":1}`)); len(got) != 12 || binary.BigEndian.Uint32(got[1:5]) != 7 {
		t.Fatalf("frame=%v", got)
	}
}
