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

func TestConolPinsModelBeforeSubmission(t *testing.T) {
	t.Setenv("CONOL_TEST", "own-cookie")
	step := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		step++
		if r.Header.Get("Cookie") != "__Secure-better-auth.session_token=own-cookie" {
			t.Error("wrong cookie")
		}
		var p map[string]any
		if r.Method == http.MethodPost {
			json.NewDecoder(r.Body).Decode(&p)
		}
		switch step {
		case 1:
			if r.URL.Path != "/api/sessions" || len(p["messages"].([]any)) != 0 {
				t.Error("must create empty session")
			}
			io.WriteString(w, `{"sessionId":"created-1"}`)
		case 2:
			if r.URL.Path != "/api/sessions/created-1/model" || p["modelPreset"] != "pro" {
				t.Error("preset missing")
			}
			io.WriteString(w, `{"ok":true}`)
		case 3:
			if p["agentModel"] != "requested-model" || p["agentEffort"] != nil {
				t.Error("model was not pinned")
			}
			io.WriteString(w, `{"ok":true}`)
		case 4:
			if r.URL.Path != "/api/sessions/created-1/messages" || p["timezone"] != "UTC" {
				t.Error("incorrect submission")
			}
			io.WriteString(w, `{}`)
		case 5:
			if r.Method != "GET" || r.URL.Query().Get("logDeltas") != "1" {
				t.Error("wrong stream route")
			}
			io.WriteString(w, "{\"type\":\"stream_event\",\"delta\":\"draft\"}\n{\"stages\":[{\"logs\":[{\"role\":\"assistant\",\"content\":\"final answer\"}]}]}\n{\"type\":\"done\"}\n")
		default:
			t.Fatal("unexpected call")
		}
	}))
	defer server.Close()
	c := New(config.Source{Adapter: "conol-web", KeyEnv: "CONOL_TEST", BaseURL: server.URL})
	defer c.Close()
	resp, err := c.Do(context.Background(), "chat", "requested-model", false, []byte(`{"messages":[{"role":"user","content":"hello"}]}`), nil)
	if err != nil {
		t.Fatal(err)
	}
	data, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if !strings.Contains(string(data), `"content":"final answer"`) || strings.Contains(string(data), "draft") {
		t.Fatalf("wrong final precedence %s", data)
	}
	if step != 5 {
		t.Fatal(step)
	}
}

func TestConolTerminalAndError(t *testing.T) {
	for _, raw := range []string{`{"type":"assistant","content":"unfinished"}` + "\n", `{"type":"error","error":"private"}` + "\n"} {
		_, err := collectConol(strings.NewReader(raw))
		if err == nil || strings.Contains(err.Error(), "private") {
			t.Fatal(err)
		}
	}
	text, err := collectConol(strings.NewReader("{\"type\":\"assistant\",\"content\":\"answer\"}\n{\"type\":\"done\"}"))
	if err != nil || text != "answer" {
		t.Fatalf("%s %v", text, err)
	}
}
