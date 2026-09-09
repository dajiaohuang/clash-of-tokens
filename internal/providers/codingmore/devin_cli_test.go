package codingmore

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"

	"clash-of-tokens/internal/config"
)

func TestDevinCLICompanion(t *testing.T) {
	helper := buildDevinHelper(t)
	t.Setenv("CLI_DEVIN_BIN", helper)
	t.Setenv("COT_DEVIN_TEST_KEY", "devin-secret")
	t.Setenv("WINDSURF_API_KEY", "")
	c := New(config.Source{Adapter: AdapterDevinCLI, BaseURL: devinURL, KeyEnv: "COT_DEVIN_TEST_KEY", MaxInflight: 1})
	defer c.Close()
	body := []byte(`{"model":"swe-1-6-fast","messages":[{"role":"system","content":"Be concise."},{"role":"user","content":[{"type":"text","text":"Reply with OK."}]}]}`)
	resp, err := c.Do(context.Background(), "chat", "swe-1-6-fast", false, body, nil)
	if err != nil {
		t.Fatal(err)
	}
	data, err := io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	if err != nil {
		t.Fatal(err)
	}
	var completion map[string]any
	if err := json.Unmarshal(data, &completion); err != nil {
		t.Fatal(err)
	}
	if completion["object"] != "chat.completion" || completion["model"] != "swe-1-6-fast" || !strings.Contains(string(data), "devin companion response") {
		t.Fatalf("completion=%s", data)
	}

	resp, err = c.Do(context.Background(), "chat", "swe-1-6-fast", true, body, nil)
	if err != nil {
		t.Fatal(err)
	}
	data, err = io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	if err != nil || !strings.Contains(string(data), "devin companion response") || !strings.Contains(string(data), "data: [DONE]") {
		t.Fatalf("stream=%s err=%v", data, err)
	}
}

func TestDevinPromptPreservesTextBlocksAndRejectsLossyFields(t *testing.T) {
	prompt, err := devinPrompt([]byte(`{"messages":[{"role":"user","content":[{"type":"text","text":"hello"}]}]}`))
	if err != nil || !strings.Contains(prompt, "[User]\nhello") {
		t.Fatalf("prompt=%q err=%v", prompt, err)
	}
	if _, err := devinPrompt([]byte(`{"messages":[{"role":"user","content":"hello"}],"tools":[{"type":"function"}]}`)); err == nil {
		t.Fatal("tools were silently accepted")
	}
}

func TestDevinRejectsGatewaySession(t *testing.T) {
	t.Setenv("COT_DEVIN_TEST_KEY", "devin-secret")
	c := New(config.Source{Adapter: AdapterDevinCLI, BaseURL: devinURL, KeyEnv: "COT_DEVIN_TEST_KEY", MaxInflight: 1})
	defer c.Close()
	_, err := c.Do(context.Background(), "chat", "model", false, []byte(`{"messages":[{"role":"user","content":"hello"}]}`), mapHeader("X-COT-Session", "same"))
	if err == nil || !strings.Contains(err.Error(), "session continuation") {
		t.Fatalf("err=%v", err)
	}
}

func TestDevinCLIRejectsTruncatedAndNonTerminalTurns(t *testing.T) {
	tests := []struct {
		name string
		mode string
	}{
		{name: "eof after streamed text", mode: "eof-after-text"},
		{name: "mismatched update session", mode: "mismatched-update"},
		{name: "unknown stop reason", mode: "unknown-stop"},
		{name: "cancelled stop reason", mode: "cancel-stop"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			helper := buildDevinHelperMode(t, tt.mode)
			t.Setenv("CLI_DEVIN_BIN", helper)
			t.Setenv("COT_DEVIN_TEST_KEY", "devin-secret")
			t.Setenv("WINDSURF_API_KEY", "")
			c := New(config.Source{Adapter: AdapterDevinCLI, BaseURL: devinURL, KeyEnv: "COT_DEVIN_TEST_KEY", MaxInflight: 1})
			defer c.Close()

			resp, err := c.Do(context.Background(), "chat", "swe-1-6-fast", false, []byte(`{"model":"swe-1-6-fast","messages":[{"role":"user","content":"Reply with OK."}]}`), nil)
			if resp != nil {
				_ = resp.Body.Close()
			}
			if err == nil || !errors.Is(err, ErrTruncated) {
				t.Fatalf("expected truncated turn, resp=%v err=%v", resp, err)
			}
		})
	}
}

func mapHeader(key, value string) http.Header {
	return http.Header{key: []string{value}}
}

func buildDevinHelper(t *testing.T) string {
	return buildDevinHelperMode(t, "")
}

func buildDevinHelperMode(t *testing.T, mode string) string {
	t.Helper()
	dir := t.TempDir()
	source := filepath.Join(dir, "main.go")
	binary := filepath.Join(dir, "devin-helper")
	if runtime.GOOS == "windows" {
		binary += ".exe"
	}
	program := `package main
import ("bufio"; "encoding/json"; "fmt"; "os"; "strings")
const testMode = "__COT_DEVIN_TEST_MODE__"
func main() {
  if os.Getenv("WINDSURF_API_KEY") != "devin-secret" || os.Getenv("COT_DEVIN_TEST_KEY") != "" { os.Exit(31) }
  r := bufio.NewScanner(os.Stdin); r.Buffer(make([]byte, 1024), 1<<20)
  n := 0
  for r.Scan() {
    var msg map[string]any
    if json.Unmarshal(r.Bytes(), &msg) != nil { os.Exit(32) }
    method, _ := msg["method"].(string); id := int(msg["id"].(float64)); n++
    switch method {
    case "initialize": fmt.Printf("{"+"\"jsonrpc\":\"2.0\",\"id\":%d,\"result\":{\"protocolVersion\":\"0.3\"}}\n", id)
    case "session/new":
      p, _ := msg["params"].(map[string]any); if p["cwd"] == nil || p["mcpServers"] == nil || p["model"] != "swe-1-6-fast" { os.Exit(33) }
      fmt.Printf("{"+"\"jsonrpc\":\"2.0\",\"id\":%d,\"result\":{\"sessionId\":\"test-session\"}}\n", id)
    case "session/prompt":
      p, _ := msg["params"].(map[string]any); if p["sessionId"] != "test-session" { os.Exit(34) }
      if testMode == "eof-after-text" { fmt.Println("{\"jsonrpc\":\"2.0\",\"method\":\"session/update\",\"params\":{\"update\":{\"sessionUpdate\":\"agent_message_chunk\",\"content\":{\"type\":\"text\",\"text\":\"partial\"}}}}"); return }
      if testMode == "mismatched-update" { fmt.Println("{\"jsonrpc\":\"2.0\",\"method\":\"session/update\",\"params\":{\"sessionId\":\"other-session\",\"update\":{\"sessionUpdate\":\"agent_message_chunk\",\"content\":{\"type\":\"text\",\"text\":\"wrong\"}}}}"); return }
      if testMode == "unknown-stop" { fmt.Printf("{"+"\"jsonrpc\":\"2.0\",\"id\":%d,\"result\":{\"content\":\"not accepted\",\"stopReason\":\"paused\"}}\\n", id); return }
      if testMode == "cancel-stop" { fmt.Printf("{"+"\"jsonrpc\":\"2.0\",\"id\":%d,\"result\":{\"content\":\"not accepted\",\"stopReason\":\"cancelled\"}}\\n", id); return }
      fmt.Println("{\"jsonrpc\":\"2.0\",\"method\":\"session/update\",\"params\":{\"update\":{\"sessionUpdate\":\"agent_message_chunk\",\"content\":{\"type\":\"text\",\"text\":\"devin companion \"}}}}")
      fmt.Println("{\"jsonrpc\":\"2.0\",\"method\":\"session/update\",\"params\":{\"update\":{\"sessionUpdate\":\"agent_message_chunk\",\"content\":{\"type\":\"text\",\"text\":\"response\"}}}}")
      fmt.Printf("{"+"\"jsonrpc\":\"2.0\",\"id\":%d,\"result\":{\"stopReason\":\"end_turn\"}}\n", id)
    default: os.Exit(35)
    }
    if n >= 3 { /* keep stdin open until the adapter closes it */ }
  }
  if err := r.Err(); err != nil && !strings.Contains(err.Error(), "file already closed") { os.Exit(36) }
}`
	program = strings.Replace(program, `const testMode = "__COT_DEVIN_TEST_MODE__"`, `const testMode = `+strconv.Quote(mode), 1)
	if err := os.WriteFile(source, []byte(program), 0600); err != nil {
		t.Fatal(err)
	}
	build := exec.Command("go", "build", "-o", binary, source)
	if output, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build Devin helper: %v: %s", err, output)
	}
	return binary
}
