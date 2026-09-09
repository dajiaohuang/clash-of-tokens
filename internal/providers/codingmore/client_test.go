package codingmore

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
	"sync/atomic"
	"testing"
	"time"

	"clash-of-tokens/internal/config"
)

func testSource(base string) config.Source {
	return config.Source{ID: "amazon-q-test", Adapter: string(AdapterAmazonQ), BaseURL: base, KeyEnv: "COT_CODINGMORE_TEST_KEY", MaxInflight: 1}
}

func TestAmazonQChatAndStream(t *testing.T) {
	t.Setenv("COT_CODINGMORE_TEST_KEY", "q-secret")
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		if r.URL.Path != "/generateAssistantResponse" || r.Header.Get("Authorization") != "Bearer q-secret" {
			t.Errorf("unexpected endpoint/auth: %s %s", r.URL, r.Header.Get("Authorization"))
		}
		if r.Header.Get("Cookie") != "" || r.Header.Get("X-Internal-Secret") != "" {
			t.Error("caller credential headers were forwarded")
		}
		var payload map[string]any
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			t.Errorf("request JSON: %v", err)
		}
		state, ok := payload["conversationState"].(map[string]any)
		if !ok || state["chatTriggerType"] != "MANUAL" {
			t.Errorf("missing CodeWhisperer conversation state: %#v", payload)
		}
		current := state["currentMessage"].(map[string]any)["userInputMessage"].(map[string]any)
		if current["modelId"] != "claude-sonnet-4.5" {
			t.Errorf("model=%v", current["modelId"])
		}
		w.Header().Set("Content-Type", "application/vnd.amazon.eventstream")
		_, _ = w.Write(makeEventFrame(t, ":event-type", "metadataEvent", `{"usage":{"inputTokens":3,"outputTokens":2}}`))
		_, _ = w.Write(makeEventFrame(t, ":event-type", "assistantResponseEvent", `{"content":"hello","messageId":"q-msg","messageStatus":"IN_PROGRESS"}`))
		_, _ = w.Write(makeEventFrame(t, ":event-type", "assistantResponseEvent", `{"content":" world","messageStatus":"COMPLETED"}`))
		_, _ = w.Write(makeEventFrame(t, ":event-type", "messageStopEvent", `{}`))
	}))
	defer server.Close()

	c := New(testSource(server.URL))
	defer c.Close()
	body := []byte(`{"model":"ignored","messages":[{"role":"system","content":"Be concise"},{"role":"user","content":"hello"}]}`)
	resp, err := c.Do(context.Background(), "chat", "claude-sonnet-4.5", true, body, http.Header{"Cookie": []string{"secret"}, "X-COT-Session": []string{"same"}})
	if err != nil {
		t.Fatal(err)
	}
	stream, err := io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	if err != nil {
		t.Fatalf("stream=%q err=%v", stream, err)
	}
	text := string(stream)
	if !strings.Contains(text, `"content":"hello"`) || !strings.Contains(text, `"content":" world"`) || !strings.Contains(text, "data: [DONE]") {
		t.Fatalf("stream=%s", text)
	}
	if !strings.Contains(text, `"finish_reason":"stop"`) {
		t.Fatalf("missing finish marker: %s", text)
	}
	if !strings.Contains(text, `"total_tokens":5`) {
		t.Fatalf("missing streamed usage: %s", text)
	}

	resp, err = c.Do(context.Background(), "chat", "claude-sonnet-4.5", false, body, http.Header{"X-COT-Session": []string{"same"}})
	if err != nil {
		t.Fatal(err)
	}
	result, err := io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	if err != nil {
		t.Fatal(err)
	}
	var decoded map[string]any
	if err := json.Unmarshal(result, &decoded); err != nil || decoded["object"] != "chat.completion" {
		t.Fatalf("non-stream=%s err=%v", result, err)
	}
	if decoded["usage"].(map[string]any)["total_tokens"] != float64(5) {
		t.Fatalf("usage=%v", decoded["usage"])
	}
	if requests.Load() != 2 {
		t.Fatalf("requests=%d", requests.Load())
	}
}

func TestAmazonQRejectsUnsupportedAndMissingCredential(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { t.Error("request should not be sent") }))
	defer server.Close()
	c := New(testSource(server.URL))
	defer c.Close()
	for _, tc := range []struct {
		name     string
		protocol string
		body     string
		want     error
	}{
		{name: "protocol", protocol: "messages", body: `{"messages":[{"role":"user","content":"hi"}]}`, want: ErrUnsupported},
		{name: "tools", protocol: "chat", body: `{"messages":[{"role":"user","content":"hi"}],"tools":[]}`, want: ErrUnsupported},
		{name: "credential", protocol: "chat", body: `{"messages":[{"role":"user","content":"hi"}]}`, want: ErrCredential},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if tc.name == "credential" {
				t.Setenv("COT_CODINGMORE_TEST_KEY", "")
			} else {
				t.Setenv("COT_CODINGMORE_TEST_KEY", "q-secret")
			}
			_, err := c.Do(context.Background(), tc.protocol, "m", false, []byte(tc.body), nil)
			if !errors.Is(err, tc.want) {
				t.Fatalf("err=%v want=%v", err, tc.want)
			}
		})
	}
	if !Supports(string(AdapterAugment)) || !Supports(string(AdapterDevinCLI)) || Supports("unknown") {
		t.Fatal("native adapter support was reported incorrectly")
	}
}

func TestAmazonQCRCAndTruncation(t *testing.T) {
	valid := makeEventFrame(t, ":event-type", "assistantResponseEvent", `{"content":"ok","status":"COMPLETED"}`)
	if _, err := collectAmazonQ(context.Background(), io.NopCloser(bytes.NewReader(valid[:len(valid)-1])), "m"); err == nil {
		t.Fatal("truncated frame was accepted")
	}
	bad := makeEventFrame(t, ":event-type", "assistantResponseEvent", `{"content":"ok","status":"COMPLETED"}`)
	bad[len(bad)-1] ^= 1
	if _, err := collectAmazonQ(context.Background(), io.NopCloser(bytes.NewReader(bad)), "m"); err == nil {
		t.Fatal("CRC failure was accepted")
	}
}

func TestAmazonQSessionGateCancellation(t *testing.T) {
	t.Setenv("COT_CODINGMORE_TEST_KEY", "q-secret")
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write(makeEventFrame(t, ":event-type", "assistantResponseEvent", `{"content":"ok","status":"COMPLETED"}`))
	}))
	defer server.Close()
	c := New(testSource(server.URL))
	defer c.Close()
	first, err := c.Do(context.Background(), "chat", "m", true, []byte(`{"messages":[{"role":"user","content":"one"}]}`), http.Header{"X-COT-Session": []string{"same"}})
	if err != nil {
		t.Fatal(err)
	}
	defer first.Body.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	_, err = c.Do(ctx, "chat", "m", true, []byte(`{"messages":[{"role":"user","content":"two"}]}`), http.Header{"X-COT-Session": []string{"same"}})
	if err == nil || err != context.DeadlineExceeded {
		t.Fatalf("gate err=%v", err)
	}
}

func makeEventFrame(t *testing.T, headerName, headerValue, payload string) []byte {
	t.Helper()
	name := []byte(headerName)
	value := []byte(headerValue)
	headers := append([]byte{byte(len(name))}, name...)
	headers = append(headers, 7, byte(len(value)>>8), byte(len(value)))
	headers = append(headers, value...)
	body := []byte(payload)
	total := uint32(12 + len(headers) + len(body) + 4)
	frame := make([]byte, total)
	binary.BigEndian.PutUint32(frame[0:4], total)
	binary.BigEndian.PutUint32(frame[4:8], uint32(len(headers)))
	binary.BigEndian.PutUint32(frame[8:12], crc32.ChecksumIEEE(frame[:8]))
	copy(frame[12:], headers)
	copy(frame[12+len(headers):], body)
	binary.BigEndian.PutUint32(frame[len(frame)-4:], crc32.ChecksumIEEE(frame[:len(frame)-4]))
	return frame
}
