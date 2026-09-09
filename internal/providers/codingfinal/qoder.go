package codingfinal

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"clash-of-tokens/internal/config"
)

const (
	qoderDefaultEndpoint   = "/compatible-mode/v1/chat/completions"
	qoderCLIUserAgent      = "Qoder-Cli"
	qwenCLIUserAgent       = "QwenCode/0.19.3 (windows; amd64)"
	qoderCLIDefaultTimeout = 45 * time.Second
	qoderCLIWaitDelay      = 2 * time.Second
	qoderCLIWriteTimeout   = 5 * time.Second
)

func qoderEndpoint(base string) (string, error) {
	root, err := endpointBase(config.Source{BaseURL: base})
	if err != nil {
		return "", err
	}
	if strings.HasSuffix(root, "/chat/completions") {
		return root, nil
	}
	return root + qoderDefaultEndpoint, nil
}

func qoderModel(model string) string {
	switch model {
	case "qwen3.5-plus", "qwen3.6-plus":
		return "coder-model"
	case "vision-model":
		return "qwen3-vl-plus"
	default:
		return model
	}
}

func (c *Client) doQoder(ctx context.Context, token, model string, stream bool, body []byte, _ *session) (*http.Response, error) {
	if strings.HasPrefix(token, "pt-") {
		return c.doQoderCLI(ctx, token, model, stream, body)
	}
	endpoint, err := qoderEndpoint(c.source.BaseURL)
	if err != nil {
		return nil, err
	}
	payload := cloneMap(mapFromJSON(body))
	payload["model"] = qoderModel(model)
	payload["stream"] = stream
	encoded, err := json.Marshal(payload)
	if err != nil {
		return nil, fmt.Errorf("%w: cannot encode Qoder request", ErrUnsupported)
	}
	h := http.Header{"Accept": []string{"text/event-stream"}, "Authorization": []string{"Bearer " + token}, "Content-Type": []string{"application/json"}, "User-Agent": []string{qwenCLIUserAgent}, "X-Dashscope-Authtype": []string{"qwen-oauth"}, "X-Dashscope-Cachecontrol": []string{"enable"}, "X-Dashscope-Useragent": []string{qwenCLIUserAgent}, "X-Stainless-Arch": []string{"x64"}, "X-Stainless-Lang": []string{"js"}, "X-Stainless-Os": []string{"Windows"}}
	return post(ctx, c.http, endpoint, encoded, h)
}

func mapFromJSON(body []byte) map[string]json.RawMessage {
	var m map[string]json.RawMessage
	_ = json.Unmarshal(body, &m)
	if m == nil {
		m = map[string]json.RawMessage{}
	}
	return m
}

func qoderToJSON(ctx context.Context, r io.Reader, model string) ([]byte, error) {
	data, err := io.ReadAll(io.LimitReader(r, maxEventBytes+1))
	if err != nil {
		return nil, err
	}
	if int64(len(data)) > maxEventBytes {
		return nil, fmt.Errorf("%w: Qoder response exceeds byte limit", ErrUnsupported)
	}
	trim := bytes.TrimSpace(data)
	if len(trim) == 0 {
		return nil, ErrTruncated
	}
	if trim[0] == '{' {
		var root map[string]any
		if json.Unmarshal(trim, &root) == nil && root != nil {
			if err := parseStreamError(root); err != nil {
				return nil, err
			}
			if _, ok := root["choices"]; ok {
				if _, ok := root["object"]; !ok {
					root["object"] = "chat.completion"
				}
				if _, ok := root["model"]; !ok {
					root["model"] = model
				}
				return json.Marshal(root)
			}
		}
	}
	return collectOpenAISSE(ctx, bytes.NewReader(data), model)
}

func validateQoderSSE(ctx context.Context, r io.Reader) ([]byte, error) { return validateSSE(ctx, r) }

// qoderCLIPrompt mirrors the pinned qodercli transport. The official CLI is
// deliberately used as a plain language model and never receives caller-side
// tools to execute; validateQoderCLIRequest rejects those fields before this
// prompt is built.
func qoderCLIPrompt(req chatRequest) string {
	lines := []string{
		"You are answering an OpenAI-compatible request through the Qoder CLI transport.",
		"Respond as a plain language model only.",
		"Do not use your own tools, do not inspect files, and do not run commands.",
		"Do not mention the adapter unless the user explicitly asks.",
	}
	lines = append(lines, "Conversation transcript:")
	for _, message := range req.Messages {
		text := message.Content
		role := strings.ToUpper(strings.TrimSpace(message.Role))
		if role == "" {
			role = "UNKNOWN"
		}
		line := role + ":\n" + text
		if strings.TrimSpace(text) != "" {
			lines = append(lines, strings.TrimSpace(line))
		}
	}
	lines = append(lines, "Reply now with the assistant response only.")
	return strings.Join(lines, "\n\n")
}

// validateQoderCLIRequest keeps the local qodercli bridge honest about the
// subset it can represent. Tool definitions, tool calls, response formats,
// sampling controls, and unknown fields would otherwise be silently turned
// into prompt text or dropped by the CLI.
func validateQoderCLIRequest(req chatRequest, model string, stream bool) error {
	for key := range req.Raw {
		switch key {
		case "messages", "model", "stream":
		default:
			return fmt.Errorf("%w: Qoder CLI request field %q is unsupported", ErrUnsupported, key)
		}
	}
	if raw, ok := req.Raw["model"]; ok {
		var requested string
		if json.Unmarshal(raw, &requested) != nil || strings.TrimSpace(requested) == "" {
			return fmt.Errorf("%w: Qoder CLI request model must be a non-empty string", ErrUnsupported)
		}
		canonical := strings.TrimPrefix(strings.ToLower(strings.TrimSpace(model)), "qoder/")
		if strings.TrimPrefix(strings.ToLower(strings.TrimSpace(requested)), "qoder/") != canonical {
			return fmt.Errorf("%w: request model conflicts with selected model", ErrUnsupported)
		}
	}
	if raw, ok := req.Raw["stream"]; ok {
		var requested bool
		if json.Unmarshal(raw, &requested) != nil || string(bytes.TrimSpace(raw)) == "null" || requested != stream {
			return fmt.Errorf("%w: Qoder CLI stream flag conflicts with gateway selection", ErrUnsupported)
		}
	}
	if len(req.Tools) > 0 || req.ToolChoice != nil || req.Parallel != nil {
		return fmt.Errorf("%w: Qoder CLI tool execution is not implemented", ErrUnsupported)
	}
	for i, message := range req.Messages {
		switch message.Role {
		case "system", "developer", "user", "assistant":
		default:
			return fmt.Errorf("%w: Qoder CLI message %d role %q is unsupported", ErrUnsupported, i, message.Role)
		}
		if message.ToolCallID != "" || len(message.ToolCalls) > 0 {
			return fmt.Errorf("%w: Qoder CLI tool history is not implemented", ErrUnsupported)
		}
	}
	return nil
}

func qoderCLIConfigDir() (string, error) {
	dir := strings.TrimSpace(os.Getenv("QODER_CLI_CONFIG_DIR"))
	if dir == "" {
		base := strings.TrimSpace(os.Getenv("DATA_DIR"))
		if base == "" {
			home, err := os.UserHomeDir()
			if err != nil {
				return "", fmt.Errorf("%w: Qoder CLI home is unavailable", ErrUnsupported)
			}
			base = filepath.Join(home, ".omniroute")
		}
		dir = filepath.Join(base, "qoder-cli")
	}
	if len(dir) > 4096 {
		return "", fmt.Errorf("%w: Qoder CLI config path is too long", ErrUnsupported)
	}
	if err := os.MkdirAll(dir, 0700); err != nil {
		return "", fmt.Errorf("%w: Qoder CLI config directory cannot be created", ErrUnsupported)
	}
	return dir, nil
}

var qoderCLIModelLevels = map[string]string{
	"qwen3.8-max-preview": "qmodel_preview", "qwen3.7-max": "qmodel_latest", "qwen3.7-plus": "qmodel",
	"kimi-k3": "kmodel_latest", "kimi-k2.7-code": "kmodel", "glm-5.2": "gm51model",
	"deepseek-v4-pro": "dmodel", "deepseek-v4-flash": "dfmodel", "minimax-m3": "mmodel",
}

var qoderCLIModelLevelsByID = map[string]bool{
	"auto": true, "qmodel_preview": true, "qmodel_latest": true, "qmodel": true,
	"kmodel_latest": true, "kmodel": true, "gm51model": true, "dmodel": true,
	"dfmodel": true, "mmodel": true,
}

func qoderCLILevel(model string) (string, error) {
	model = strings.ToLower(strings.TrimSpace(strings.TrimPrefix(model, "qoder/")))
	if model == "" || strings.HasPrefix(model, "-") {
		return "", fmt.Errorf("%w: invalid Qoder CLI model", ErrUnsupported)
	}
	if level := qoderCLIModelLevels[model]; level != "" {
		return level, nil
	}
	if qoderCLIModelLevelsByID[model] {
		return model, nil
	}
	return "", fmt.Errorf("%w: Qoder CLI model %q is not in the pinned model set", ErrUnsupported, model)
}

func qoderCLITimeout() time.Duration {
	raw := strings.TrimSpace(os.Getenv("QODER_CLI_TIMEOUT_MS"))
	if raw == "" {
		return qoderCLIDefaultTimeout
	}
	value, err := strconv.Atoi(raw)
	if err != nil || value < 1 || value > 900000 {
		return qoderCLIDefaultTimeout
	}
	return time.Duration(value) * time.Millisecond
}

func qoderCLIEnvironment(token string) []string {
	allowed := map[string]bool{
		"APPDATA": true, "COMSPEC": true, "HOME": true, "HOMEDRIVE": true, "HOMEPATH": true,
		"LANG": true, "LC_ALL": true, "LOCALAPPDATA": true, "NO_COLOR": true,
		"PATH": true, "PATHEXT": true, "SYSTEMROOT": true, "TEMP": true,
		"SYSTEMDRIVE": true, "TERM": true, "TMP": true, "USERDOMAIN": true, "USERNAME": true,
		"USERPROFILE": true, "WINDIR": true,
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
		values = append(values, "QODER_PERSONAL_ACCESS_TOKEN="+token)
	}
	return values
}

type qoderLimitedBuffer struct {
	bytes.Buffer
	limit int64
}

func (b *qoderLimitedBuffer) Write(data []byte) (int, error) {
	if int64(b.Len()+len(data)) > b.limit {
		return 0, fmt.Errorf("qodercli output exceeds byte limit")
	}
	return b.Buffer.Write(data)
}

func qoderCLIResult(stdout string) (string, bool) {
	trimmed := strings.TrimSpace(stdout)
	if trimmed == "" {
		return "", true
	}
	lines := strings.Split(trimmed, "\n")
	parse := func(data []byte) (string, bool, bool) {
		var envelope struct {
			Type    string          `json:"type"`
			Result  json.RawMessage `json:"result"`
			Error   *bool           `json:"is_error"`
			Subtype json.RawMessage `json:"subtype"`
		}
		if json.Unmarshal(data, &envelope) != nil || envelope.Type != "result" || envelope.Error == nil || len(envelope.Result) == 0 {
			return "", false, false
		}
		var text string
		if json.Unmarshal(envelope.Result, &text) != nil {
			return "", false, false
		}
		subtype := ""
		if len(envelope.Subtype) != 0 {
			if json.Unmarshal(envelope.Subtype, &subtype) != nil {
				return "", false, false
			}
		}
		return text, *envelope.Error || strings.EqualFold(subtype, "error"), true
	}
	if text, failed, ok := parse([]byte(trimmed)); ok {
		return text, failed
	}
	for i := len(lines) - 1; i >= 0; i-- {
		if text, failed, ok := parse([]byte(strings.TrimSpace(lines[i]))); ok {
			return text, failed
		}
	}
	return "", true
}

func (c *Client) doQoderCLI(ctx context.Context, token, model string, stream bool, body []byte) (*http.Response, error) {
	req, err := decodeChatRequest(body)
	if err != nil {
		return nil, err
	}
	if err := validateQoderCLIRequest(req, model, stream); err != nil {
		return nil, err
	}
	level, err := qoderCLILevel(model)
	if err != nil {
		return nil, err
	}
	configDir, err := qoderCLIConfigDir()
	if err != nil {
		return nil, err
	}
	command := strings.TrimSpace(os.Getenv("CLI_QODER_BIN"))
	if command == "" {
		command = "qodercli"
	}
	prompt := qoderCLIPrompt(req)
	if int64(len(prompt)) > maxRequestBytes {
		return nil, fmt.Errorf("%w: Qoder CLI prompt exceeds byte limit", ErrUnsupported)
	}
	args := []string{"--print", "--output-format", "json", "--model", level, "--tools", "", "--config-dir", configDir}
	runCtx, cancel := context.WithTimeout(ctx, qoderCLITimeout())
	defer cancel()
	child := exec.CommandContext(runCtx, command, args...)
	child.WaitDelay = qoderCLIWaitDelay
	child.Dir = configDir
	child.Env = qoderCLIEnvironment(token)
	stdin, err := child.StdinPipe()
	if err != nil {
		return nil, fmt.Errorf("%w: cannot open qodercli stdin", ErrUnsupported)
	}
	var stdout, stderr qoderLimitedBuffer
	stdout.limit, stderr.limit = maxEventBytes, 1<<20
	child.Stdout, child.Stderr = &stdout, &stderr
	if err := child.Start(); err != nil {
		_ = stdin.Close()
		return nil, fmt.Errorf("%w: qodercli was not found or could not start", ErrUnsupported)
	}
	writeDone := make(chan error, 1)
	go func() {
		_, writeErr := io.WriteString(stdin, prompt)
		closeErr := stdin.Close()
		if writeErr != nil {
			writeDone <- writeErr
		} else {
			writeDone <- closeErr
		}
	}()
	runDone := make(chan error, 1)
	go func() { runDone <- child.Wait() }()
	runFinished, writeFinished := false, false
	var runErr, writeErr error
	writeTimer := time.NewTimer(qoderCLIWriteTimeout)
	defer writeTimer.Stop()
	for !runFinished || !writeFinished {
		select {
		case runErr = <-runDone:
			runFinished = true
			if !writeFinished {
				_ = stdin.Close()
				select {
				case writeErr = <-writeDone:
				default:
					writeErr = io.ErrClosedPipe
				}
				writeFinished = true
			}
		case writeErr = <-writeDone:
			writeFinished = true
			writeTimer.Stop()
		case <-writeTimer.C:
			if !writeFinished {
				_ = stdin.Close()
				_ = child.Process.Kill()
				writeErr = io.ErrClosedPipe
				writeFinished = true
			}
		}
	}
	if ctx.Err() != nil {
		return nil, ctx.Err()
	}
	if runCtx.Err() == context.DeadlineExceeded {
		return nil, &HTTPError{Status: http.StatusGatewayTimeout, What: "qodercli timed out"}
	}
	if writeErr != nil {
		return nil, &HTTPError{Status: http.StatusBadGateway, What: "qodercli request failed"}
	}
	if runErr != nil {
		return nil, &HTTPError{Status: http.StatusBadGateway, What: "qodercli request failed"}
	}
	text, failed := qoderCLIResult(stdout.String())
	if failed {
		return nil, &HTTPError{Status: http.StatusBadGateway, What: "qodercli request failed"}
	}
	if strings.TrimSpace(text) == "" {
		return nil, ErrTruncated
	}
	if stream {
		data := qoderCLISSE(model, text)
		return &http.Response{StatusCode: http.StatusOK, Header: http.Header{"Content-Type": []string{"text/event-stream"}}, Body: io.NopCloser(bytes.NewReader(data)), ContentLength: int64(len(data))}, nil
	}
	data := qoderCLICompletion(model, text)
	return &http.Response{StatusCode: http.StatusOK, Header: http.Header{"Content-Type": []string{"application/json"}}, Body: io.NopCloser(bytes.NewReader(data)), ContentLength: int64(len(data))}, nil
}

func qoderCLICompletion(model, text string) []byte {
	data, _ := json.Marshal(map[string]any{"id": "chatcmpl-qoder-" + randomUUID(), "object": "chat.completion", "created": time.Now().Unix(), "model": model, "choices": []any{map[string]any{"index": 0, "message": map[string]any{"role": "assistant", "content": text}, "finish_reason": "stop"}}})
	return data
}

func qoderCLISSE(model, text string) []byte {
	var out bytes.Buffer
	id, created := "chatcmpl-qoder-"+randomUUID(), time.Now().Unix()
	appendSSE(&out, map[string]any{"id": id, "object": "chat.completion.chunk", "created": created, "model": model, "choices": []any{map[string]any{"index": 0, "delta": map[string]any{"role": "assistant", "content": ""}, "finish_reason": nil}}})
	appendSSE(&out, map[string]any{"id": id, "object": "chat.completion.chunk", "created": created, "model": model, "choices": []any{map[string]any{"index": 0, "delta": map[string]any{"content": text}, "finish_reason": nil}}})
	appendSSE(&out, map[string]any{"id": id, "object": "chat.completion.chunk", "created": created, "model": model, "choices": []any{map[string]any{"index": 0, "delta": map[string]any{}, "finish_reason": "stop"}}})
	out.WriteString("data: [DONE]\n\n")
	return out.Bytes()
}
