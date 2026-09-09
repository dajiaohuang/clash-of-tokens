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

func TestPiConversationCookieAndText(t *testing.T) {
	t.Setenv("PI_TEST", "session=existing")
	calls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if r.URL.Path == "/api/chat/start" {
			if r.Header.Get("X-Api-Version") != "3" {
				t.Error("missing version")
			}
			http.SetCookie(w, &http.Cookie{Name: "turn", Value: "issued", Path: "/"})
			io.WriteString(w, `{"conversations":[{"sid":"own-created"}]}`)
			return
		}
		cookie, err := r.Cookie("turn")
		if err != nil || cookie.Value != "issued" {
			t.Error("issued cookie was lost")
		}
		var p map[string]any
		json.NewDecoder(r.Body).Decode(&p)
		if p["conversation"] != "own-created" || p["text"] != "hi" || p["mode"] != "BASE" {
			t.Error("wrong Pi body")
		}
		io.WriteString(w, "data: {\"text\":\"hello\"}\n\ndata: {\"text\":\" world\"}")
	}))
	defer server.Close()
	c := New(config.Source{Adapter: "pi", KeyEnv: "PI_TEST", BaseURL: server.URL})
	defer c.Close()
	resp, err := c.Do(context.Background(), "chat", "pi", false, []byte(`{"messages":[{"role":"user","content":"hi"}]}`), nil)
	if err != nil {
		t.Fatal(err)
	}
	data, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if !strings.Contains(string(data), "hello world") || calls != 2 {
		t.Fatalf("%s calls=%d", data, calls)
	}
}

func TestRekaCumulativeText(t *testing.T) {
	t.Setenv("REKA_TEST", "existing-bearer")
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer existing-bearer" || r.Header.Get("Cookie") != "" {
			t.Error("wrong authentication")
		}
		var p map[string]any
		json.NewDecoder(r.Body).Decode(&p)
		if p["model_name"] != "reka-core" {
			t.Error("wrong model")
		}
		io.WriteString(w, "data: {\"text\":\"hello\"}\n\ndata: {\"text\":\"hello again\"}\n\n")
	}))
	defer server.Close()
	c := New(config.Source{Adapter: "reka-web", KeyEnv: "REKA_TEST", BaseURL: server.URL})
	defer c.Close()
	resp, err := c.Do(context.Background(), "chat", "reka-core", false, []byte(`{"messages":[{"role":"user","content":"hi"}]}`), nil)
	if err != nil {
		t.Fatal(err)
	}
	data, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if !strings.Contains(string(data), `"content":"hello again"`) {
		t.Fatalf("%s", data)
	}
}

type interruptedText struct{}

func (interruptedText) Read([]byte) (int, error) { return 0, io.ErrUnexpectedEOF }
func (interruptedText) Close() error             { return nil }
func TestLineStreamDoesNotSucceedOnNetworkTruncation(t *testing.T) {
	body := textLineStream(interruptedText{}, "id", "model", func(s string) (string, error) { return s, nil })
	data, err := io.ReadAll(body)
	body.Close()
	if err == nil || strings.Contains(string(data), "[DONE]") {
		t.Fatalf("%s %v", data, err)
	}
}
