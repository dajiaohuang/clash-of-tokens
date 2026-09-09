package webhttp

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

func TestPhindAIHomepageNonceAndBufferedAnswer(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "" {
			t.Error("caller credential leaked")
		}
		if r.URL.Path == "/" {
			http.SetCookie(w, &http.Cookie{Name: "site", Value: "own"})
			io.WriteString(w, `<script>const cfg={"nonce":"ab1234"}</script>`)
			return
		}
		if r.URL.Path != "/wp-admin/admin-ajax.php" || r.Method != "POST" {
			t.Errorf("wrong route %s", r.URL.Path)
		}
		if err := r.ParseForm(); err != nil {
			t.Error(err)
		}
		if r.Form.Get("nonce") != "ab1234" || r.Form.Get("action") != "phind_ai_send" || r.Form.Get("message") != "你好" {
			t.Errorf("wrong form %v", r.Form)
		}
		if r.Header.Get("Cookie") != "site=own" {
			t.Error("site cookie not retained")
		}
		io.WriteString(w, `{"success":true,"data":{"response":"answer"}}`)
	}))
	defer server.Close()
	c := New(config.Source{Adapter: "phindai", BaseURL: server.URL, Anonymous: true})
	defer c.Close()
	resp, err := c.Do(context.Background(), "chat", "default", false, []byte(`{"messages":[{"role":"user","content":"你好"}]}`), http.Header{"Authorization": {"Bearer caller"}})
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(resp.Body)
	if err != nil || !strings.Contains(string(data), `"content":"answer"`) || resp.Header.Get("X-COT-Delivery") != "buffered" {
		t.Fatalf("answer=%s err=%v", data, err)
	}
}

func TestWhiteRabbitRequestPreservesHistoryAndUsesOwnCookie(t *testing.T) {
	t.Setenv("RABBIT_COOKIE", "session=own")
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/chat" || r.Header.Get("Cookie") != "session=own" || r.Header.Get("Authorization") != "" {
			t.Error("wrong auth or route")
		}
		var payload map[string]any
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			t.Error(err)
		}
		if len(payload["messages"].([]any)) != 3 || payload["enhancePrompt"] != false || payload["useFunctions"] != false || payload["model"] != nil {
			t.Errorf("wrong payload %v", payload)
		}
		w.Header().Set("Content-Type", "text/plain;charset=UTF-8")
		io.WriteString(w, "你好 🌍")
	}))
	defer server.Close()
	c := New(config.Source{Adapter: "whiterabbitneo", BaseURL: server.URL, KeyEnv: "RABBIT_COOKIE"})
	defer c.Close()
	resp, err := c.Do(context.Background(), "chat", "default", true, []byte(`{"messages":[{"role":"user","content":"hello"},{"role":"assistant","content":"hi"},{"role":"user","content":"again"}]}`), http.Header{"Cookie": {"other=secret"}})
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(resp.Body)
	if err != nil || !strings.Contains(string(data), "你好 🌍") || !strings.Contains(string(data), "[DONE]") {
		t.Fatalf("answer=%s err=%v", data, err)
	}
}

type byteReader struct {
	data     []byte
	terminal error
}

func (r *byteReader) Read(p []byte) (int, error) {
	if len(r.data) == 0 {
		return 0, r.terminal
	}
	p[0] = r.data[0]
	r.data = r.data[1:]
	return 1, nil
}

func TestPlainTextUnicodeBoundariesAndTruncation(t *testing.T) {
	var b strings.Builder
	reason, err := readPlainText(&byteReader{[]byte("你好 🌍"), io.EOF}, func(s string) error { b.WriteString(s); return nil })
	if err != nil || reason != "stop" || b.String() != "你好 🌍" {
		t.Fatalf("text=%q reason=%s err=%v", b.String(), reason, err)
	}
	for _, data := range [][]byte{{0xe4, 0xbd}, {0xff}, {}} {
		if _, err := readPlainText(strings.NewReader(string(data)), func(string) error { return nil }); err == nil {
			t.Fatalf("accepted invalid text %v", data)
		}
	}
	if _, err := readPlainText(&byteReader{[]byte("partial"), io.ErrUnexpectedEOF}, func(string) error { return nil }); !errors.Is(err, io.ErrUnexpectedEOF) {
		t.Fatal(err)
	}
}

func TestProductDefaultModelEnforced(t *testing.T) {
	for _, adapter := range []string{"phindai", "whiterabbitneo"} {
		c := New(config.Source{Adapter: adapter})
		_, err := c.Do(context.Background(), "chat", "invented-model", false, []byte(`{"messages":[{"role":"user","content":"hi"}]}`), nil)
		c.Close()
		if err == nil {
			t.Fatalf("accepted unselectable model %s", adapter)
		}
	}
}
