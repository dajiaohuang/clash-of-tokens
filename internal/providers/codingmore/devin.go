package codingmore

// Devin CLI is an official local companion transport. The adapter speaks the
// pinned ACP JSON-RPC protocol over a directly spawned process and runs the
// summarizer agent, whose contract has no file-system tools. It never invokes
// a shell and never forwards caller headers.

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

const (
	devinURL             = "devin://acp/stdio"
	devinMaxLine         = 1 << 20
	devinShutdownTimeout = 2 * time.Second
	devinMaxErrorText    = 512
	devinPromptMaxBytes  = maxRequestBytes
)

type devinRPCError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

type devinRPCMessage struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params"`
	Result  json.RawMessage `json:"result"`
	Error   *devinRPCError  `json:"error"`
}

type devinRuntime struct {
	cmd    *exec.Cmd
	stdin  io.WriteCloser
	stdout io.ReadCloser
	reader *devinReader
}

type devinReader struct {
	reader *bufio.Reader
	total  int64
}

func resolveDevinBin() string {
	if value := strings.TrimSpace(os.Getenv("CLI_DEVIN_BIN")); value != "" {
		return value
	}
	if runtimeGOOSWindows() {
		localAppData := strings.TrimSpace(os.Getenv("LOCALAPPDATA"))
		if localAppData == "" {
			if home, err := os.UserHomeDir(); err == nil {
				localAppData = filepath.Join(home, "AppData", "Local")
			}
		}
		candidate := filepath.Join(localAppData, "devin", "cli", "bin", "devin.exe")
		if isRegularFile(candidate) {
			return candidate
		}
		return "devin.exe"
	}
	if home, err := os.UserHomeDir(); err == nil {
		for _, candidate := range []string{
			filepath.Join(home, ".local", "share", "devin", "bin", "devin"),
			filepath.Join(home, ".devin", "bin", "devin"),
		} {
			if isRegularFile(candidate) {
				return candidate
			}
		}
	}
	return "devin"
}

func isRegularFile(path string) bool {
	info, err := os.Stat(path)
	return err == nil && info.Mode().IsRegular()
}

// Kept as a variable-level helper so tests can exercise path discovery on
// either host without changing the process runtime. Production uses the real
// GOOS value through this function.
var runtimeGOOSWindows = func() bool { return os.PathSeparator == '\\' }

func devinPrompt(body []byte) (string, error) {
	var root map[string]json.RawMessage
	dec := json.NewDecoder(bytes.NewReader(body))
	if err := dec.Decode(&root); err != nil || root == nil {
		return "", fmt.Errorf("%w: Devin request must be a JSON object", ErrUnsupported)
	}
	if err := dec.Decode(new(any)); err != io.EOF {
		return "", fmt.Errorf("%w: Devin request must contain one JSON object", ErrUnsupported)
	}
	for key := range root {
		switch key {
		case "model", "messages", "stream":
		default:
			return "", fmt.Errorf("%w: Devin field %q is unsupported", ErrUnsupported, key)
		}
	}
	if raw := root["model"]; len(raw) != 0 {
		var model string
		if json.Unmarshal(raw, &model) != nil || strings.TrimSpace(model) == "" {
			return "", fmt.Errorf("%w: Devin model must be a non-empty string", ErrUnsupported)
		}
	}
	if raw := root["stream"]; len(raw) != 0 {
		var stream bool
		if json.Unmarshal(raw, &stream) != nil {
			return "", fmt.Errorf("%w: Devin stream must be boolean", ErrUnsupported)
		}
	}
	var rawMessages []json.RawMessage
	if err := json.Unmarshal(root["messages"], &rawMessages); err != nil || len(rawMessages) == 0 {
		return "", fmt.Errorf("%w: Devin messages must be a non-empty array", ErrUnsupported)
	}
	parts := make([]string, 0, len(rawMessages))
	for i, raw := range rawMessages {
		var message map[string]json.RawMessage
		if err := json.Unmarshal(raw, &message); err != nil || message == nil {
			return "", fmt.Errorf("%w: Devin message %d must be an object", ErrUnsupported, i)
		}
		for key := range message {
			switch key {
			case "role", "content", "tool_calls", "tool_call_id", "name":
			default:
				return "", fmt.Errorf("%w: Devin message %d field %q is unsupported", ErrUnsupported, i, key)
			}
		}
		var role string
		if json.Unmarshal(message["role"], &role) != nil || strings.TrimSpace(role) == "" {
			return "", fmt.Errorf("%w: Devin message %d role is required", ErrUnsupported, i)
		}
		text, err := devinTextContent(message["content"])
		if err != nil {
			return "", fmt.Errorf("%w: Devin message %d %v", ErrUnsupported, i, err)
		}
		if rawCalls := message["tool_calls"]; len(rawCalls) != 0 {
			var calls []json.RawMessage
			if json.Unmarshal(rawCalls, &calls) != nil {
				return "", fmt.Errorf("%w: Devin message %d tool_calls must be an array", ErrUnsupported, i)
			}
			for _, call := range calls {
				text += "\n[Tool call]\n" + string(bytes.TrimSpace(call))
			}
		}
		if rawID := message["tool_call_id"]; len(rawID) != 0 {
			var id string
			if json.Unmarshal(rawID, &id) != nil {
				return "", fmt.Errorf("%w: Devin message %d tool_call_id must be text", ErrUnsupported, i)
			}
			if id != "" {
				text = "[Tool result " + id + "]\n" + text
			}
		}
		if strings.TrimSpace(text) == "" {
			continue
		}
		label := "User"
		if role == "system" {
			label = "System"
		} else if role == "assistant" {
			label = "Assistant"
		}
		parts = append(parts, "["+label+"]\n"+text)
	}
	prompt := strings.Join(parts, "\n\n")
	if prompt == "" {
		prompt = "(empty)"
	}
	if int64(len(prompt)) > devinPromptMaxBytes {
		return "", fmt.Errorf("%w: Devin prompt exceeds byte limit", ErrUnsupported)
	}
	return prompt, nil
}

func devinTextContent(raw json.RawMessage) (string, error) {
	var text string
	if json.Unmarshal(raw, &text) == nil {
		return text, nil
	}
	var blocks []json.RawMessage
	if json.Unmarshal(raw, &blocks) != nil {
		return "", errors.New("content must be text")
	}
	var out strings.Builder
	for _, rawBlock := range blocks {
		var block map[string]json.RawMessage
		if json.Unmarshal(rawBlock, &block) != nil || block == nil {
			return "", errors.New("content block must be an object")
		}
		var kind string
		_ = json.Unmarshal(block["type"], &kind)
		if kind != "text" && kind != "input_text" && kind != "output_text" {
			return "", errors.New("content block must be text")
		}
		var value string
		if json.Unmarshal(block["text"], &value) != nil {
			return "", errors.New("content block text is required")
		}
		out.WriteString(value)
	}
	return out.String(), nil
}

func devinCredential(sourceKeyEnv string) (string, error) {
	keyEnv := strings.TrimSpace(sourceKeyEnv)
	value := ""
	if keyEnv != "" {
		value = strings.TrimSpace(os.Getenv(keyEnv))
	}
	if value == "" {
		value = strings.TrimSpace(os.Getenv("WINDSURF_API_KEY"))
	}
	if len(value) > maxHeaderValue || strings.ContainsAny(value, "\r\n") {
		return "", ErrCredential
	}
	return value, nil
}

func devinEnvironment(token string) []string {
	allowed := map[string]bool{
		"APPDATA": true, "HOME": true, "HOMEDRIVE": true, "HOMEPATH": true,
		"LANG": true, "LC_ALL": true, "LOCALAPPDATA": true, "NO_COLOR": true,
		"PATH": true, "PATHEXT": true, "SYSTEMROOT": true, "TEMP": true,
		"TERM": true, "TMP": true, "USERPROFILE": true, "WINDIR": true,
		"XDG_CACHE_HOME": true, "XDG_CONFIG_HOME": true, "XDG_DATA_HOME": true,
	}
	values := make([]string, 0, len(allowed)+1)
	for _, entry := range os.Environ() {
		key, _, ok := strings.Cut(entry, "=")
		if ok && allowed[strings.ToUpper(key)] {
			values = append(values, entry)
		}
	}
	if token != "" {
		values = append(values, "WINDSURF_API_KEY="+token)
	}
	return values
}

func startDevin(ctx context.Context, command, cwd, token string) (*devinRuntime, error) {
	cmd := exec.CommandContext(ctx, command, "acp", "--agent-type", "summarizer")
	cmd.WaitDelay = devinShutdownTimeout
	cmd.Env = devinEnvironment(token)
	if cwd != "" {
		cmd.Dir = cwd
	}
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, fmt.Errorf("%w: cannot open Devin stdin", ErrUnsupported)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		_ = stdin.Close()
		return nil, fmt.Errorf("%w: cannot open Devin stdout", ErrUnsupported)
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		_ = stdin.Close()
		_ = stdout.Close()
		return nil, fmt.Errorf("%w: cannot open Devin stderr", ErrUnsupported)
	}
	if err := cmd.Start(); err != nil {
		_ = stdin.Close()
		_ = stdout.Close()
		_ = stderr.Close()
		return nil, &HTTPError{Status: 502, What: "Devin CLI could not start"}
	}
	go func() { _, _ = io.Copy(io.Discard, stderr) }()
	return &devinRuntime{cmd: cmd, stdin: stdin, stdout: stdout, reader: &devinReader{reader: bufio.NewReaderSize(stdout, 64<<10)}}, nil
}

func (r *devinRuntime) close() error {
	if r == nil || r.cmd == nil {
		return nil
	}
	_ = r.stdin.Close()
	_ = r.stdout.Close()
	done := make(chan error, 1)
	go func() { done <- r.cmd.Wait() }()
	select {
	case err := <-done:
		return err
	case <-time.After(devinShutdownTimeout):
		_ = r.cmd.Process.Kill()
		return <-done
	}
}

func writeDevinRPC(writer io.Writer, id uint64, method string, params any) error {
	message := map[string]any{"jsonrpc": "2.0", "id": id, "method": method, "params": params}
	data, err := json.Marshal(message)
	if err != nil {
		return err
	}
	data = append(data, '\n')
	for len(data) > 0 {
		n, err := writer.Write(data)
		if n > 0 {
			data = data[n:]
		}
		if err != nil {
			return err
		}
		if n == 0 {
			return io.ErrShortWrite
		}
	}
	return nil
}

func (r *devinReader) next() (devinRPCMessage, error) {
	var line []byte
	for {
		part, err := r.reader.ReadSlice('\n')
		if len(part) > devinMaxLine-len(line) {
			return devinRPCMessage{}, fmt.Errorf("%w: Devin ACP line exceeds byte limit", ErrUnsupported)
		}
		line = append(line, part...)
		r.total += int64(len(part))
		if r.total > maxEventBytes {
			return devinRPCMessage{}, fmt.Errorf("%w: Devin ACP output exceeds byte limit", ErrUnsupported)
		}
		if err == bufio.ErrBufferFull {
			continue
		}
		if err != nil {
			if err == io.EOF && len(line) == 0 {
				return devinRPCMessage{}, io.EOF
			}
			return devinRPCMessage{}, ErrTruncated
		}
		trimmed := bytes.TrimSpace(line)
		if len(trimmed) == 0 {
			line = nil
			continue
		}
		var message devinRPCMessage
		if json.Unmarshal(trimmed, &message) != nil || message.JSONRPC != "2.0" {
			// Devin may print a short startup banner. The official executor ignores
			// such lines while retaining a hard total output bound.
			line = nil
			continue
		}
		return message, nil
	}
}

func devinIDMatches(raw json.RawMessage, id uint64) bool {
	var got uint64
	return json.Unmarshal(raw, &got) == nil && got == id
}

func readDevinResult(ctx context.Context, reader *devinReader, id uint64) (json.RawMessage, error) {
	for {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		message, err := reader.next()
		if err != nil {
			return nil, err
		}
		if !devinIDMatches(message.ID, id) {
			continue
		}
		if message.Error != nil {
			return nil, fmt.Errorf("Devin ACP request failed (%d)", message.Error.Code)
		}
		if len(message.Result) == 0 {
			return nil, ErrTruncated
		}
		return message.Result, nil
	}
}

func devinResultSessionID(raw json.RawMessage) string {
	var result map[string]any
	if json.Unmarshal(raw, &result) != nil || result == nil {
		return ""
	}
	value, _ := result["sessionId"].(string)
	return strings.TrimSpace(value)
}

func devinUpdate(raw json.RawMessage) (text string, terminal bool, failure error) {
	var params map[string]any
	if json.Unmarshal(raw, &params) != nil || params == nil {
		return "", false, fmt.Errorf("%w: Devin ACP update is malformed", ErrTruncated)
	}
	update, _ := params["update"].(map[string]any)
	kind := ""
	if update != nil {
		kind, _ = update["sessionUpdate"].(string)
	}
	if kind == "" {
		kind, _ = params["type"].(string)
	}
	if kind == "agent_message_chunk" {
		if update == nil {
			return "", false, fmt.Errorf("%w: Devin ACP agent message is malformed", ErrTruncated)
		}
		return devinContentText(update["content"]), false, nil
	}
	if kind == "message_delta" || kind == "text_delta" || kind == "content_delta" {
		for _, key := range []string{"content", "delta", "text"} {
			if value, ok := params[key].(string); ok {
				return value, false, nil
			}
		}
		return "", false, nil
	}
	if kind == "message_stop" || kind == "stop" || kind == "done" {
		return "", true, nil
	}
	if kind == "error" {
		return "", false, errors.New("Devin ACP reported an error")
	}
	return "", false, nil
}

func devinContentText(value any) string {
	if text, ok := value.(string); ok {
		return text
	}
	if values, ok := value.([]any); ok {
		var out strings.Builder
		for _, item := range values {
			out.WriteString(devinContentText(item))
		}
		return out.String()
	}
	if object, ok := value.(map[string]any); ok {
		text, _ := object["text"].(string)
		return text
	}
	return ""
}

func devinResultText(raw json.RawMessage) string {
	var result map[string]any
	if json.Unmarshal(raw, &result) != nil || result == nil {
		return ""
	}
	for _, key := range []string{"content", "text"} {
		if text, ok := result[key].(string); ok && strings.TrimSpace(text) != "" {
			return text
		}
	}
	if message, ok := result["message"].(map[string]any); ok {
		if text, ok := message["content"].(string); ok {
			return text
		}
	}
	if values, ok := result["messages"].([]any); ok {
		for i := len(values) - 1; i >= 0; i-- {
			message, _ := values[i].(map[string]any)
			if message["role"] != "assistant" {
				continue
			}
			if text, ok := message["content"].(string); ok {
				return text
			}
		}
	}
	return ""
}

func devinStopReason(raw json.RawMessage) string {
	var result map[string]any
	if json.Unmarshal(raw, &result) != nil || result == nil {
		return ""
	}
	value, _ := result["stopReason"].(string)
	return strings.ToLower(strings.TrimSpace(value))
}

func runDevinTurn(ctx context.Context, model, prompt, token string) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, 120*time.Second)
	defer cancel()
	if err := ctx.Err(); err != nil {
		return "", err
	}
	cwd, err := os.Getwd()
	if err != nil {
		return "", fmt.Errorf("%w: Devin working directory is unavailable", ErrUnsupported)
	}
	runtime, err := startDevin(ctx, resolveDevinBin(), cwd, token)
	if err != nil {
		return "", err
	}
	defer runtime.close()
	if err := writeDevinRPC(runtime.stdin, 1, "initialize", map[string]any{
		"protocolVersion": "0.3",
		"clientInfo":      map[string]string{"name": "omniroute", "version": "1.0"},
		"capabilities":    map[string]any{},
	}); err != nil {
		return "", fmt.Errorf("%w: Devin initialize write failed", ErrTruncated)
	}
	if _, err := readDevinResult(ctx, runtime.reader, 1); err != nil {
		return "", err
	}
	if err := writeDevinRPC(runtime.stdin, 2, "session/new", map[string]any{
		"cwd":        cwd,
		"mcpServers": []any{},
		"model":      model,
	}); err != nil {
		return "", fmt.Errorf("%w: Devin session write failed", ErrTruncated)
	}
	session, err := readDevinResult(ctx, runtime.reader, 2)
	if err != nil {
		return "", err
	}
	sessionID := devinResultSessionID(session)
	if sessionID == "" {
		return "", fmt.Errorf("%w: Devin session/new returned no sessionId", ErrTruncated)
	}
	if err := writeDevinRPC(runtime.stdin, 3, "session/prompt", map[string]any{
		"sessionId": sessionID,
		"prompt":    []map[string]string{{"type": "text", "text": prompt}},
	}); err != nil {
		return "", fmt.Errorf("%w: Devin prompt write failed", ErrTruncated)
	}
	var text strings.Builder
	for {
		if err := ctx.Err(); err != nil {
			return "", err
		}
		message, err := runtime.reader.next()
		if err != nil {
			if err == io.EOF {
				return "", ErrTruncated
			}
			return "", err
		}
		if message.Error != nil {
			return "", fmt.Errorf("Devin ACP request failed (%d)", message.Error.Code)
		}
		if message.Method == "session/update" || message.Method == "$/update" {
			var correlation struct {
				SessionID string `json:"sessionId"`
			}
			if json.Unmarshal(message.Params, &correlation) != nil || (correlation.SessionID != "" && correlation.SessionID != sessionID) {
				return "", fmt.Errorf("%w: Devin session mismatch", ErrTruncated)
			}
			delta, terminal, failure := devinUpdate(message.Params)
			if failure != nil {
				return "", failure
			}
			text.WriteString(delta)
			if terminal {
				if strings.TrimSpace(text.String()) == "" {
					return "", fmt.Errorf("%w: Devin completed without assistant text", ErrTruncated)
				}
				return text.String(), nil
			}
			continue
		}
		if devinIDMatches(message.ID, 3) && len(message.Result) != 0 {
			if text.Len() == 0 {
				text.WriteString(devinResultText(message.Result))
			}
			reason := devinStopReason(message.Result)
			if reason != "end_turn" {
				return "", fmt.Errorf("%w: Devin turn did not complete normally", ErrTruncated)
			}
			if reason != "" || strings.TrimSpace(text.String()) != "" {
				if strings.TrimSpace(text.String()) == "" {
					return "", fmt.Errorf("%w: Devin completed without assistant text", ErrTruncated)
				}
				return text.String(), nil
			}
		}
	}
}

func devinCompletion(model, content string) []byte {
	value := map[string]any{
		"id": "chatcmpl-devin-" + randomUUID(), "object": "chat.completion", "created": time.Now().Unix(), "model": model,
		"choices": []any{map[string]any{"index": 0, "message": map[string]any{"role": "assistant", "content": content}, "finish_reason": "stop"}},
	}
	data, _ := json.Marshal(value)
	return data
}

func devinSSE(model, content string) []byte {
	var out bytes.Buffer
	id, created := "chatcmpl-devin-"+randomUUID(), time.Now().Unix()
	appendDevinSSE := func(value any) {
		encoded, _ := json.Marshal(value)
		out.WriteString("data: ")
		out.Write(encoded)
		out.WriteString("\n\n")
	}
	appendDevinSSE(map[string]any{"id": id, "object": "chat.completion.chunk", "created": created, "model": model, "choices": []any{map[string]any{"index": 0, "delta": map[string]any{"role": "assistant", "content": ""}, "finish_reason": nil}}})
	appendDevinSSE(map[string]any{"id": id, "object": "chat.completion.chunk", "created": created, "model": model, "choices": []any{map[string]any{"index": 0, "delta": map[string]any{"content": content}, "finish_reason": nil}}})
	appendDevinSSE(map[string]any{"id": id, "object": "chat.completion.chunk", "created": created, "model": model, "choices": []any{map[string]any{"index": 0, "delta": map[string]any{}, "finish_reason": "stop"}}})
	out.WriteString("data: [DONE]\n\n")
	return out.Bytes()
}

func (c *Client) doDevin(ctx context.Context, model string, stream bool, body []byte) (*http.Response, error) {
	prompt, err := devinPrompt(body)
	if err != nil {
		return nil, err
	}
	token, err := devinCredential(c.source.KeyEnv)
	if err != nil {
		return nil, err
	}
	content, err := runDevinTurn(ctx, model, prompt, token)
	if err != nil {
		return nil, err
	}
	data := devinCompletion(model, content)
	contentType := "application/json"
	if stream {
		data = devinSSE(model, content)
		contentType = "text/event-stream"
	}
	return &http.Response{StatusCode: http.StatusOK, Header: http.Header{"Content-Type": []string{contentType}}, Body: io.NopCloser(bytes.NewReader(data)), ContentLength: int64(len(data))}, nil
}
