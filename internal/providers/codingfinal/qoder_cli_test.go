package codingfinal

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestQoderCLICompanion(t *testing.T) {
	helper := buildQoderHelper(t, "")
	t.Setenv("CLI_QODER_BIN", helper)
	t.Setenv("QODER_CLI_CONFIG_DIR", t.TempDir())
	t.Setenv("COT_CODINGFINAL_TEST_KEY", "pt-qoder-test")
	t.Setenv("COT_GATEWAY_SECRET", "must-not-reach-qoder")
	t.Setenv("QODER_PERSONAL_ACCESS_TOKEN", "stale-parent-token")
	client := New(finalSource(AdapterQoder, "https://example.test"))
	defer client.Close()
	body := []byte(`{"model":"qwen3.8-max-preview","messages":[{"role":"user","content":"Reply with OK."}]}`)
	resp, err := client.Do(context.Background(), "chat", "qwen3.8-max-preview", false, body, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var completion map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&completion); err != nil {
		t.Fatal(err)
	}
	if completion["object"] != "chat.completion" || completion["model"] != "qwen3.8-max-preview" {
		t.Fatalf("completion=%v", completion)
	}
	choices, _ := completion["choices"].([]any)
	if len(choices) != 1 {
		t.Fatalf("choices=%v", choices)
	}
	encoded, _ := json.Marshal(choices[0])
	if string(encoded) == "" || !containsBytes(encoded, []byte("qoder companion response")) {
		t.Fatalf("choices=%s", encoded)
	}

	resp, err = client.Do(context.Background(), "chat", "qwen3.8-max-preview", true, body, nil)
	if err != nil {
		t.Fatal(err)
	}
	stream, err := io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	if err != nil || !containsBytes(stream, []byte("qoder companion response")) || !containsBytes(stream, []byte("data: [DONE]")) {
		t.Fatalf("stream=%s err=%v", stream, err)
	}
}

func TestQoderCLIRejectsUnknownModelsAndUnsupportedRequestFields(t *testing.T) {
	t.Setenv("COT_CODINGFINAL_TEST_KEY", "pt-qoder-test")
	c := New(finalSource(AdapterQoder, "https://example.test"))
	defer c.Close()
	body := `{"model":"qwen3.8-max-preview","messages":[{"role":"user","content":"hello"}]}`
	if _, err := c.Do(context.Background(), "chat", "private-model", false, []byte(body), nil); !errors.Is(err, ErrUnsupported) {
		t.Fatalf("unknown model err=%v", err)
	}
	for _, request := range []string{
		`{"model":"qwen3.8-max-preview","messages":[{"role":"user","content":"hello"}],"temperature":0.2}`,
		`{"model":"qwen3.8-max-preview","messages":[{"role":"user","content":"hello"}],"response_format":{"type":"json_object"}}`,
		`{"model":"qwen3.8-max-preview","messages":[{"role":"user","content":"hello"}],"tools":[{"type":"function","function":{"name":"lookup","parameters":{"type":"object"}}}]}`,
		`{"model":"qwen3.8-max-preview","messages":[{"role":"user","content":"hello"}],"metadata":{"trace":"drop-me"}}`,
	} {
		if _, err := c.Do(context.Background(), "chat", "qwen3.8-max-preview", false, []byte(request), nil); !errors.Is(err, ErrUnsupported) {
			t.Fatalf("unsupported request %s err=%v", request, err)
		}
	}
}

func TestQoderCLIRequiresTerminalResultAndSanitizesFailures(t *testing.T) {
	for _, tc := range []struct {
		name       string
		stdout     string
		wantText   string
		wantFailed bool
	}{
		{name: "success", stdout: `{"type":"result","is_error":false,"result":"ok"}`, wantText: "ok"},
		{name: "error", stdout: `{"type":"result","is_error":true,"result":"bad"}`, wantText: "bad", wantFailed: true},
		{name: "wrong type", stdout: `{"type":"message","is_error":false,"result":"ok"}`, wantFailed: true},
		{name: "missing terminal flag", stdout: `{"type":"result","result":"ok"}`, wantFailed: true},
		{name: "banner then result", stdout: "startup banner\n{\"type\":\"result\",\"is_error\":false,\"result\":\"ok\"}", wantText: "ok"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			text, failed := qoderCLIResult(tc.stdout)
			if text != tc.wantText || failed != tc.wantFailed {
				t.Fatalf("text=%q failed=%v", text, failed)
			}
		})
	}

	t.Setenv("CLI_QODER_BIN", buildQoderHelper(t, "fail"))
	t.Setenv("QODER_CLI_CONFIG_DIR", t.TempDir())
	t.Setenv("COT_CODINGFINAL_TEST_KEY", "pt-qoder-test")
	c := New(finalSource(AdapterQoder, "https://example.test"))
	defer c.Close()
	body := []byte(`{"model":"qwen3.8-max-preview","messages":[{"role":"user","content":"hello"}]}`)
	_, err := c.Do(context.Background(), "chat", "qwen3.8-max-preview", false, body, nil)
	if err == nil || strings.Contains(err.Error(), "qoder-private-secret") {
		t.Fatalf("stderr leaked or error missing: %v", err)
	}
	var httpErr *HTTPError
	if !errors.As(err, &httpErr) || httpErr.Status != 502 || httpErr.What != "qodercli request failed" {
		t.Fatalf("failure=%T %v", err, err)
	}
}

func TestQoderCLITimesOutAndKillsChild(t *testing.T) {
	t.Setenv("CLI_QODER_BIN", buildQoderHelper(t, "hang"))
	t.Setenv("QODER_CLI_CONFIG_DIR", t.TempDir())
	t.Setenv("QODER_CLI_TIMEOUT_MS", "50")
	t.Setenv("COT_CODINGFINAL_TEST_KEY", "pt-qoder-test")
	c := New(finalSource(AdapterQoder, "https://example.test"))
	defer c.Close()
	body := []byte(`{"model":"qwen3.8-max-preview","messages":[{"role":"user","content":"hello"}]}`)
	_, err := c.Do(context.Background(), "chat", "qwen3.8-max-preview", false, body, nil)
	var httpErr *HTTPError
	if !errors.As(err, &httpErr) || httpErr.Status != 504 {
		t.Fatalf("timeout=%T %v", err, err)
	}
}

func containsBytes(haystack, needle []byte) bool {
	for i := 0; i+len(needle) <= len(haystack); i++ {
		match := true
		for j := range needle {
			if haystack[i+j] != needle[j] {
				match = false
				break
			}
		}
		if match {
			return true
		}
	}
	return false
}

func buildQoderHelper(t *testing.T, mode string) string {
	t.Helper()
	dir := t.TempDir()
	source := filepath.Join(dir, "main.go")
	binary := filepath.Join(dir, "qoder-helper")
	if runtime.GOOS == "windows" {
		binary += ".exe"
	}
	program := `package main
import ("fmt"; "io"; "os"; "strings"; "time")
const helperMode = "` + mode + `"
func main() {
  _, _ = io.ReadAll(os.Stdin)
  if helperMode == "hang" { time.Sleep(time.Hour) }
  if helperMode == "fail" { fmt.Fprintln(os.Stderr, "qoder-private-secret"); os.Exit(3) }
  args := strings.Join(os.Args[1:], " ")
  if os.Getenv("QODER_PERSONAL_ACCESS_TOKEN") != "pt-qoder-test" || os.Getenv("COT_GATEWAY_SECRET") != "" || !strings.Contains(args, "--print") || !strings.Contains(args, "--output-format") || !strings.Contains(args, "--model") || !strings.Contains(args, "qmodel_preview") || !strings.Contains(args, "--tools") || !strings.Contains(args, "--config-dir") {
    fmt.Println("{\"type\":\"result\",\"is_error\":true,\"result\":\"qodercli invocation missing official flags\"}")
    os.Exit(0)
  }
  fmt.Println("{\"type\":\"result\",\"is_error\":false,\"result\":\"qoder companion response\"}")
}`
	if err := os.WriteFile(source, []byte(program), 0600); err != nil {
		t.Fatal(err)
	}
	build := exec.Command("go", "build", "-o", binary, source)
	if output, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build qoder helper: %v: %s", err, output)
	}
	return binary
}
