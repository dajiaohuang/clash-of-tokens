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

func TestRaycastSignedContract(t *testing.T) {
	if got := raycastSignature("2026-09-09T01:02:03.004Z", "device-abc123", "own-secret", []byte("hello")); got != "1e0f12f198ba23e5f0c50319ca0b77e700beb9cceb226ddd923e5bb9c3082f4d" {
		t.Fatal("signature golden mismatch")
	}
	sent := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer own-token" || r.Header.Get("X-Raycast-DeviceId") != "own-device" {
			t.Error("wrong auth")
		}
		if r.URL.Path == "/api/v1/ai/models" {
			if r.Method != "GET" {
				t.Error("wrong lookup method")
			}
			io.WriteString(w, `{"models":[{"id":"model-id","provider":"provider","model":"internal-model"}]}`)
			return
		}
		if r.URL.Path != "/api/v1/ai/chat_completions" || r.Method != "POST" {
			t.Error("wrong path")
		}
		sent++
		b, _ := io.ReadAll(r.Body)
		if r.Header.Get("X-Raycast-Signature-v2") != raycastSignature(r.Header.Get("X-Raycast-Timestamp"), "own-device", "own-secret", b) {
			t.Error("signature not bound to body")
		}
		var p map[string]any
		json.Unmarshal(b, &p)
		if p["model"] != "internal-model" || p["provider"] != "provider" {
			t.Error("wrong selected model")
		}
		io.WriteString(w, "data: {\"text\":\"answer\",\"finish_reason\":null}\n\ndata: {\"text\":\"\",\"finish_reason\":\"length\"}\n\n")
	}))
	defer srv.Close()
	t.Setenv("COT_TEST_RAYCAST", `{"token":"own-token","device_id":"own-device","signature_secret":"own-secret"}`)
	c := New(config.Source{Adapter: "raycast", BaseURL: srv.URL, KeyEnv: "COT_TEST_RAYCAST"})
	defer c.Close()
	body := []byte(`{"messages":[{"role":"user","content":"hello"}]}`)
	r, e := c.Do(context.Background(), "chat", "model-id", false, body, nil)
	if e != nil {
		t.Fatal(e)
	}
	defer r.Body.Close()
	b, _ := io.ReadAll(r.Body)
	if !strings.Contains(string(b), `"finish_reason":"length"`) || !strings.Contains(string(b), "answer") {
		t.Fatal(string(b))
	}
	if _, e := c.Do(context.Background(), "chat", "unknown", false, body, nil); e == nil || sent != 1 {
		t.Fatal("unknown model silently defaulted")
	}
}

func TestRaycastRejectsTruncated(t *testing.T) {
	for _, raw := range []string{"data: {\"text\":\"partial\"}\n\n", "data: {\"error\":\"secret\"}\n\n", "data: {\"text\":\"x\",\"finish_reason\":\"error\"}\n\n"} {
		if _, _, e := readRaycast(strings.NewReader(raw)); e == nil {
			t.Fatal("accepted invalid stream")
		}
	}
}
