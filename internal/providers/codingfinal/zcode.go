package codingfinal

// ZCode is a local app-server integration.  The product owns authentication in
// its local profile; this adapter only speaks the documented stdio protocol
// and never reads, forwards, or persists a Z.ai credential.

import (
	"bufio"
	"bytes"
	"context"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

const (
	zcodeURL             = "zcode://app-server/stdio"
	zcodeDefaultProvider = "builtin:zai-coding-plan"
	zcodeHeaderSize      = 13
	zcodeRegularMessage  = 1
	zcodeInitialize      = 200
	zcodeResponse        = 201
	zcodeError           = 202
	zcodeCanceled        = 203
	zcodeMaxFrameBytes   = 32 << 20
	zcodeMaxHelloBytes   = 64 << 10
	zcodeMaxArgs         = 16
	zcodeWorkspacePrefix = "clash-tokens-zcode-"
)

var zcodeModels = map[string]struct{}{
	"glm-5.3-flash": {}, "glm-5.3": {}, "glm-5.3-max": {},
	"glm-5.2": {}, "glm-5.1": {}, "glm-5": {}, "glm-5-turbo": {},
	"glm-4.7-flash": {}, "glm-4.7": {}, "glm-4.6v": {}, "glm-4.6": {},
	"glm-4.5v": {}, "glm-4.5": {}, "glm-4.5-air": {},
}

type zcodeRuntime struct {
	cmd    *exec.Cmd
	stdin  io.WriteCloser
	reader *bufio.Reader
	nextID uint64
}

type zcodeFrame struct {
	kind    byte
	header  any
	payload any
}

func zcodeModel(model string) (string, error) {
	model = strings.TrimSpace(model)
	model = strings.TrimPrefix(model, "zcode/")
	if model == "" {
		return "glm-5.3-flash", nil
	}
	if strings.HasPrefix(model, "-") {
		return "", fmt.Errorf("%w: invalid ZCode model", ErrUnsupported)
	}
	if _, ok := zcodeModels[model]; !ok {
		return "", fmt.Errorf("%w: ZCode model %q is not in the pinned model set", ErrUnsupported, model)
	}
	return model, nil
}

func zcodePrompt(req chatRequest, model string, stream bool) (string, error) {
	if len(req.Messages) == 0 {
		return "", fmt.Errorf("%w: messages are required", ErrUnsupported)
	}
	if len(req.Tools) > 0 || req.ToolChoice != nil {
		return "", fmt.Errorf("%w: ZCode tool execution is not implemented", ErrUnsupported)
	}
	for key := range req.Raw {
		switch key {
		case "messages", "model", "stream":
		default:
			return "", fmt.Errorf("%w: ZCode request field %q is unsupported", ErrUnsupported, key)
		}
	}
	if raw, ok := req.Raw["model"]; ok {
		var requested string
		if json.Unmarshal(raw, &requested) != nil || (requested != "" && requested != model) {
			return "", fmt.Errorf("%w: request model conflicts with selected model", ErrUnsupported)
		}
	}
	if raw, ok := req.Raw["stream"]; ok {
		var requested bool
		if json.Unmarshal(raw, &requested) != nil || requested != stream {
			return "", fmt.Errorf("%w: stream flag conflicts with gateway selection", ErrUnsupported)
		}
	}
	parts := make([]string, 0, len(req.Messages))
	for _, message := range req.Messages {
		if len(message.ToolCalls) > 0 || message.Role == "tool" {
			return "", fmt.Errorf("%w: ZCode tool history is not implemented", ErrUnsupported)
		}
		text := strings.TrimSpace(message.Content)
		if text == "" {
			continue
		}
		label := "User"
		switch message.Role {
		case "system":
			label = "System"
		case "assistant":
			label = "Assistant"
		}
		parts = append(parts, "["+label+"]\n"+text)
	}
	if len(parts) == 0 {
		return "", fmt.Errorf("%w: message text is empty", ErrUnsupported)
	}
	return strings.Join(parts, "\n\n"), nil
}

func zcodeCommand() (string, []string, string, error) {
	runtimeRoot := strings.TrimSpace(os.Getenv("ZCODE_SERVER_RUNTIME_ROOT"))
	if runtimeRoot == "" {
		home, err := os.UserHomeDir()
		if err == nil {
			runtimeRoot = filepath.Join(home, ".zcode", "server")
		}
	}
	serverNode := strings.TrimSpace(os.Getenv("ZCODE_SERVER_NODE"))
	serverEntry := strings.TrimSpace(os.Getenv("ZCODE_SERVER_ENTRY"))
	if serverNode == "" && runtimeRoot != "" {
		serverNode = filepath.Join(runtimeRoot, "node")
	}
	if serverEntry == "" && runtimeRoot != "" {
		serverEntry = filepath.Join(runtimeRoot, "zcode-server.cjs")
	}
	if serverNode != "" && serverEntry != "" {
		if _, err := os.Stat(serverNode); err == nil {
			if _, err := os.Stat(serverEntry); err == nil {
				return serverNode, []string{serverEntry}, zcodeCWD(), nil
			}
		}
	}
	command := strings.TrimSpace(os.Getenv("ZCODE_BIN"))
	if command == "" {
		command = "zcode"
	}
	args := []string{"app-server"}
	if raw := strings.TrimSpace(os.Getenv("ZCODE_ARGS")); raw != "" {
		var values []string
		if err := json.Unmarshal([]byte(raw), &values); err != nil || len(values) > zcodeMaxArgs {
			return "", nil, "", fmt.Errorf("%w: ZCODE_ARGS must be a JSON array of at most 16 strings", ErrUnsupported)
		}
		for _, value := range values {
			if len(value) > 4096 {
				return "", nil, "", fmt.Errorf("%w: ZCODE_ARGS contains an oversized argument", ErrUnsupported)
			}
		}
		args = values
	}
	return command, args, zcodeCWD(), nil
}

func zcodeCWD() string {
	if cwd := strings.TrimSpace(os.Getenv("ZCODE_CWD")); cwd != "" {
		return cwd
	}
	return ""
}

func zcodeTimeout(name string, fallback, min, max int) time.Duration {
	value, err := strconv.Atoi(strings.TrimSpace(os.Getenv(name)))
	if err != nil || value < min || value > max {
		value = fallback
	}
	return time.Duration(value) * time.Millisecond
}

func startZcode(ctx context.Context, command string, args []string, cwd string, startupTimeout time.Duration) (*zcodeRuntime, error) {
	cmd := exec.CommandContext(ctx, command, args...)
	cmd.Env = zcodeEnvironment()
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, fmt.Errorf("%w: cannot open ZCode stdin", ErrUnsupported)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		_ = stdin.Close()
		return nil, fmt.Errorf("%w: cannot open ZCode stdout", ErrUnsupported)
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		_ = stdin.Close()
		_ = stdout.Close()
		return nil, fmt.Errorf("%w: cannot open ZCode stderr", ErrUnsupported)
	}
	if cwd != "" {
		cmd.Dir = cwd
	}
	if err := cmd.Start(); err != nil {
		_ = stdin.Close()
		_ = stdout.Close()
		_ = stderr.Close()
		return nil, fmt.Errorf("%w: cannot start ZCode app-server", ErrUnsupported)
	}
	// Diagnostics can contain local provider details or credentials. Drain them
	// so a noisy app-server cannot block on stderr, but never expose the bytes.
	go func() { _, _ = io.Copy(io.Discard, stderr) }()
	runtime := &zcodeRuntime{cmd: cmd, stdin: stdin, reader: bufio.NewReaderSize(stdout, 64<<10)}
	startupCtx, cancelStartup := context.WithTimeout(ctx, startupTimeout)
	defer cancelStartup()
	type helloResult struct {
		line []byte
		err  error
	}
	helloDone := make(chan helloResult, 1)
	go func() {
		line, err := readZcodeHello(runtime.reader)
		helloDone <- helloResult{line: line, err: err}
	}()
	var helloResultValue helloResult
	select {
	case helloResultValue = <-helloDone:
	case <-startupCtx.Done():
		_ = runtime.close()
		return nil, startupCtx.Err()
	}
	line, err := helloResultValue.line, helloResultValue.err
	if err != nil {
		_ = runtime.close()
		return nil, err
	}
	var hello map[string]any
	if json.Unmarshal(line, &hello) != nil || hello == nil || hello["type"] != "zcode-hello" {
		_ = runtime.close()
		return nil, fmt.Errorf("%w: invalid ZCode app-server hello", ErrTruncated)
	}
	ack, _ := json.Marshal(map[string]any{
		"type": "zcode-hello-ack", "version": "clash-of-tokens", "clientId": "clash-of-tokens",
	})
	if err := writeZcodeBytes(runtime.stdin, append(ack, '\n')); err != nil {
		_ = runtime.close()
		return nil, fmt.Errorf("%w: cannot acknowledge ZCode app-server", ErrTruncated)
	}
	frameDone := make(chan struct {
		frame zcodeFrame
		err   error
	}, 1)
	go func() {
		frame, err := runtime.readFrame()
		frameDone <- struct {
			frame zcodeFrame
			err   error
		}{frame: frame, err: err}
	}()
	var frameResult struct {
		frame zcodeFrame
		err   error
	}
	select {
	case frameResult = <-frameDone:
	case <-startupCtx.Done():
		_ = runtime.close()
		return nil, startupCtx.Err()
	}
	frame, err := frameResult.frame, frameResult.err
	if err != nil {
		_ = runtime.close()
		return nil, err
	}
	if zcodeMessageType(frame.header) != zcodeInitialize {
		_ = runtime.close()
		return nil, fmt.Errorf("%w: ZCode app-server did not initialize", ErrTruncated)
	}
	return runtime, nil
}

// zcodeEnvironment gives the product runtime only the conventional profile
// and process-discovery variables it needs. Gateway keys and unrelated
// provider credentials must never be inherited by a local app-server.
func zcodeEnvironment() []string {
	allowed := map[string]bool{
		"APPDATA": true, "HOME": true, "HOMEDRIVE": true, "HOMEPATH": true,
		"LANG": true, "LC_ALL": true, "LOCALAPPDATA": true, "NO_COLOR": true,
		"PATH": true, "PATHEXT": true, "SYSTEMROOT": true, "TEMP": true,
		"TERM": true, "TMP": true, "USERPROFILE": true, "WINDIR": true,
		"XDG_CACHE_HOME": true, "XDG_CONFIG_HOME": true, "XDG_DATA_HOME": true,
	}
	// Keep the test-only helper marker explicit. Do not pass arbitrary ZCODE_*
	// variables through: an operator may have placed credentials or gateway
	// settings in the parent environment, and the app-server does not need the
	// adapter's configuration variables after startup.
	allowed["ZCODE_HELPER"] = true
	values := make([]string, 0, len(allowed)+1)
	for _, entry := range os.Environ() {
		key, _, ok := strings.Cut(entry, "=")
		if !ok {
			continue
		}
		if allowed[key] {
			values = append(values, entry)
		}
	}
	return values
}

func readZcodeHello(reader *bufio.Reader) ([]byte, error) {
	var line []byte
	for {
		part, err := reader.ReadSlice('\n')
		if len(part) > zcodeMaxHelloBytes-len(line) {
			return nil, fmt.Errorf("%w: ZCode hello line is too large", ErrUnsupported)
		}
		line = append(line, part...)
		if err == bufio.ErrBufferFull {
			continue
		}
		if err != nil {
			return nil, fmt.Errorf("%w: ZCode hello was truncated", ErrTruncated)
		}
		return bytes.TrimSpace(line), nil
	}
}

func (r *zcodeRuntime) readFrame() (zcodeFrame, error) {
	var header [zcodeHeaderSize]byte
	if _, err := io.ReadFull(r.reader, header[:]); err != nil {
		return zcodeFrame{}, fmt.Errorf("%w: ZCode frame was truncated", ErrTruncated)
	}
	length := binary.BigEndian.Uint32(header[9:13])
	if length > zcodeMaxFrameBytes {
		return zcodeFrame{}, fmt.Errorf("%w: ZCode frame exceeds byte limit", ErrUnsupported)
	}
	body := make([]byte, int(length))
	if _, err := io.ReadFull(r.reader, body); err != nil {
		return zcodeFrame{}, fmt.Errorf("%w: ZCode frame payload was truncated", ErrTruncated)
	}
	first, offset, err := decodeZcodeValue(body, 0, 0)
	if err != nil {
		return zcodeFrame{}, err
	}
	second, offset, err := decodeZcodeValue(body, offset, 0)
	if err != nil {
		return zcodeFrame{}, err
	}
	if offset != len(body) {
		return zcodeFrame{}, fmt.Errorf("%w: ZCode frame contains trailing bytes", ErrTruncated)
	}
	return zcodeFrame{kind: header[0], header: first, payload: second}, nil
}

func (r *zcodeRuntime) call(ctx context.Context, channel, method string, args []any) (any, error) {
	id := r.nextID
	if id == 0 {
		id = 1
	}
	r.nextID++
	packet, err := encodeZcodeCall(id, channel, method, args)
	if err != nil {
		return nil, err
	}
	if err := writeZcodeBytes(r.stdin, packet); err != nil {
		return nil, fmt.Errorf("%w: ZCode request write failed", ErrTruncated)
	}
	for {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		frame, err := r.readFrame()
		if err != nil {
			return nil, err
		}
		if frame.kind != zcodeRegularMessage {
			continue
		}
		header, ok := frame.header.([]any)
		if !ok || len(header) == 0 {
			continue
		}
		typ := zcodeNumber(header[0])
		if typ == zcodeInitialize {
			continue
		}
		if typ != zcodeResponse && typ != zcodeError && typ != zcodeCanceled || len(header) < 2 || zcodeNumber(header[1]) != id {
			continue
		}
		if typ == zcodeResponse {
			return frame.payload, nil
		}
		return nil, fmt.Errorf("ZCode app-server request failed: %s", zcodeErrorText(frame.payload))
	}
}

func (r *zcodeRuntime) close() error {
	if r == nil || r.cmd == nil {
		return nil
	}
	_ = r.stdin.Close()
	done := make(chan error, 1)
	go func() { done <- r.cmd.Wait() }()
	select {
	case err := <-done:
		return err
	case <-time.After(1500 * time.Millisecond):
		_ = r.cmd.Process.Kill()
		return <-done
	}
}

func writeZcodeBytes(writer io.Writer, data []byte) error {
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

func encodeZcodeVQL(value uint64) []byte {
	var out [10]byte
	i := 0
	for {
		b := byte(value & 0x7f)
		value >>= 7
		if value != 0 {
			b |= 0x80
		}
		out[i] = b
		i++
		if value == 0 {
			return out[:i]
		}
	}
}

func encodeZcodeValue(value any) ([]byte, error) {
	switch value := value.(type) {
	case nil:
		return zcodeJSONValue([]byte("null")), nil
	case string:
		data := []byte(value)
		return append(append([]byte{1}, encodeZcodeVQL(uint64(len(data)))...), data...), nil
	case []byte:
		return append(append([]byte{2}, encodeZcodeVQL(uint64(len(value)))...), value...), nil
	case []any:
		out := append([]byte{4}, encodeZcodeVQL(uint64(len(value)))...)
		for _, item := range value {
			encoded, err := encodeZcodeValue(item)
			if err != nil {
				return nil, err
			}
			out = append(out, encoded...)
		}
		return out, nil
	case int:
		if value < 0 {
			return zcodeJSONNumber(int64(value))
		}
		return append([]byte{6}, encodeZcodeVQL(uint64(value))...), nil
	case int64:
		if value < 0 {
			return zcodeJSONNumber(value)
		}
		return append([]byte{6}, encodeZcodeVQL(uint64(value))...), nil
	case uint64:
		return append([]byte{6}, encodeZcodeVQL(value)...), nil
	case uint:
		return append([]byte{6}, encodeZcodeVQL(uint64(value))...), nil
	default:
		data, err := json.Marshal(value)
		if err != nil {
			return nil, fmt.Errorf("%w: cannot encode ZCode value", ErrUnsupported)
		}
		return zcodeJSONValue(data), nil
	}
}

func zcodeJSONValue(data []byte) []byte {
	return append(append([]byte{5}, encodeZcodeVQL(uint64(len(data)))...), data...)
}

func zcodeJSONNumber(value int64) ([]byte, error) {
	data, _ := json.Marshal(value)
	return zcodeJSONValue(data), nil
}

func encodeZcodeCall(id uint64, channel, method string, args []any) ([]byte, error) {
	header, err := encodeZcodeValue([]any{uint64(100), id, channel, method})
	if err != nil {
		return nil, err
	}
	payload, err := encodeZcodeValue(args)
	if err != nil {
		return nil, err
	}
	body := append(header, payload...)
	if len(body) > zcodeMaxFrameBytes {
		return nil, fmt.Errorf("%w: ZCode request exceeds byte limit", ErrUnsupported)
	}
	frame := make([]byte, zcodeHeaderSize+len(body))
	frame[0] = zcodeRegularMessage
	binary.BigEndian.PutUint32(frame[9:13], uint32(len(body)))
	copy(frame[zcodeHeaderSize:], body)
	return frame, nil
}

func decodeZcodeValue(data []byte, offset, depth int) (any, int, error) {
	if depth > 32 || offset >= len(data) {
		return nil, offset, fmt.Errorf("%w: malformed ZCode value", ErrTruncated)
	}
	typ := data[offset]
	offset++
	if typ == 0 {
		return nil, offset, nil
	}
	if typ == 1 || typ == 2 || typ == 5 {
		length, next, err := decodeZcodeVQL(data, offset)
		if err != nil || length > uint64(len(data)-next) {
			return nil, offset, fmt.Errorf("%w: truncated ZCode value", ErrTruncated)
		}
		end := next + int(length)
		if typ == 1 {
			return string(data[next:end]), end, nil
		}
		if typ == 2 {
			return append([]byte(nil), data[next:end]...), end, nil
		}
		var value any
		if err := json.Unmarshal(data[next:end], &value); err != nil {
			return nil, offset, fmt.Errorf("%w: malformed ZCode JSON value", ErrTruncated)
		}
		return value, end, nil
	}
	if typ == 4 {
		count, next, err := decodeZcodeVQL(data, offset)
		if err != nil || count > uint64(len(data)) {
			return nil, offset, fmt.Errorf("%w: malformed ZCode array", ErrTruncated)
		}
		values := make([]any, 0, int(count))
		for i := uint64(0); i < count; i++ {
			value, after, err := decodeZcodeValue(data, next, depth+1)
			if err != nil {
				return nil, offset, err
			}
			values = append(values, value)
			next = after
		}
		return values, next, nil
	}
	if typ == 6 {
		value, next, err := decodeZcodeVQL(data, offset)
		if err != nil {
			return nil, offset, err
		}
		return value, next, nil
	}
	return nil, offset, fmt.Errorf("%w: unknown ZCode value type", ErrTruncated)
}

func decodeZcodeVQL(data []byte, offset int) (uint64, int, error) {
	var value, multiplier uint64 = 0, 1
	for i := 0; i < 8; i++ {
		if offset >= len(data) {
			return 0, offset, fmt.Errorf("%w: truncated ZCode integer", ErrTruncated)
		}
		part := data[offset]
		offset++
		if uint64(part&0x7f) > (^uint64(0)-value)/multiplier {
			return 0, offset, fmt.Errorf("%w: invalid ZCode integer", ErrTruncated)
		}
		value += uint64(part&0x7f) * multiplier
		if part&0x80 == 0 {
			return value, offset, nil
		}
		if multiplier > ^uint64(0)/128 {
			return 0, offset, fmt.Errorf("%w: invalid ZCode integer", ErrTruncated)
		}
		multiplier *= 128
	}
	return 0, offset, fmt.Errorf("%w: invalid ZCode integer", ErrTruncated)
}

func zcodeNumber(value any) uint64 {
	switch value := value.(type) {
	case uint64:
		return value
	case float64:
		if value >= 0 && value == float64(uint64(value)) {
			return uint64(value)
		}
	case int:
		if value >= 0 {
			return uint64(value)
		}
	case int64:
		if value >= 0 {
			return uint64(value)
		}
	}
	return 0
}

func zcodeMessageType(value any) uint64 {
	if header, ok := value.([]any); ok && len(header) > 0 {
		return zcodeNumber(header[0])
	}
	return zcodeNumber(value)
}

func zcodeRecord(value any) map[string]any {
	if record, ok := value.(map[string]any); ok {
		return record
	}
	return nil
}

func zcodeSessionID(value any) string {
	root := zcodeRecord(value)
	nested := zcodeRecord(root["session"])
	for _, candidate := range []map[string]any{nested, root} {
		if id, ok := candidate["sessionId"].(string); ok && strings.TrimSpace(id) != "" {
			return id
		}
	}
	return ""
}

func zcodeStatus(value any) string {
	root := zcodeRecord(value)
	nested := zcodeRecord(root["session"])
	for _, candidate := range []map[string]any{nested, root} {
		if status, ok := candidate["status"].(string); ok {
			return status
		}
	}
	return ""
}

func zcodeMessageText(value any) (string, string) {
	message := zcodeRecord(value)
	info := zcodeRecord(message["info"])
	role, _ := info["role"].(string)
	var out strings.Builder
	if parts, ok := message["parts"].([]any); ok {
		for _, part := range parts {
			record := zcodeRecord(part)
			if record["type"] == "text" {
				if text, ok := record["text"].(string); ok {
					out.WriteString(text)
				}
			}
		}
	}
	return role, out.String()
}

func zcodeAssistantText(value any) string {
	root := zcodeRecord(value)
	if messages, ok := root["messages"].([]any); ok {
		for i := len(messages) - 1; i >= 0; i-- {
			role, text := zcodeMessageText(messages[i])
			if text != "" && role == "assistant" {
				return text
			}
		}
	}
	return ""
}

func zcodeErrorText(value any) string { return "ZCode runtime request failed" }

func runZcodeTurn(ctx context.Context, model, prompt string) (string, error) {
	if strings.TrimSpace(os.Getenv("COT_ZCODE_ALLOW_UNSAFE")) != "1" {
		return "", fmt.Errorf("%w: ZCode build sessions can execute local actions; set COT_ZCODE_ALLOW_UNSAFE=1 only after explicit operator approval", ErrUnsupported)
	}
	command, args, cwd, err := zcodeCommand()
	if err != nil {
		return "", err
	}
	// The pinned app-server exposes build/persistent sessions and has no
	// documented no-tools/read-only mode. Require the explicit opt-in above and
	// contain model-driven edits in a throwaway workspace for that opt-in path.
	workspace, err := os.MkdirTemp("", zcodeWorkspacePrefix)
	if err != nil {
		return "", fmt.Errorf("%w: cannot create isolated ZCode workspace", ErrUnsupported)
	}
	defer os.RemoveAll(workspace)
	_ = cwd // ZCODE_CWD is not used as an editable workspace.
	turnCtx, cancel := context.WithTimeout(ctx, zcodeTimeout("ZCODE_TURN_TIMEOUT_MS", 120000, 1, 900000))
	defer cancel()
	runtime, err := startZcode(turnCtx, command, args, workspace, zcodeTimeout("ZCODE_STARTUP_TIMEOUT_MS", 10000, 1, 120000))
	if err != nil {
		if turnCtx.Err() != nil {
			return "", turnCtx.Err()
		}
		return "", err
	}
	var sessionID string
	defer func() {
		if sessionID != "" && turnCtx.Err() == nil {
			closeCtx, cancel := context.WithTimeout(context.Background(), time.Second)
			_, _ = runtime.call(closeCtx, "zcode-agent", "closeSession", []any{map[string]any{
				"workspacePath": workspace, "workspaceIdentity": workspace, "sessionId": sessionID,
			}})
			cancel()
		}
		_ = runtime.close()
	}()
	workspaceValue := map[string]any{"workspacePath": workspace, "workspaceIdentity": workspace}
	initialized, err := runtime.call(turnCtx, "zcode-agent", "initialize", []any{workspaceValue})
	if err != nil {
		return "", err
	}
	initRecord := zcodeRecord(initialized)
	if available, ok := initRecord["available"].(bool); !ok || !available {
		return "", fmt.Errorf("%w: %s", ErrUnsupported, zcodeErrorText(initialized))
	}
	created, err := runtime.call(turnCtx, "zcode-agent", "createSession", []any{map[string]any{
		"workspacePath": workspace, "workspaceIdentity": workspace,
		"sessionTraceId": randomUUID(), "mode": "build", "persistence": "persistent",
	}})
	if err != nil {
		return "", err
	}
	sessionID = zcodeSessionID(created)
	if sessionID == "" {
		return "", fmt.Errorf("%w: ZCode createSession returned no sessionId", ErrTruncated)
	}
	provider := strings.TrimSpace(os.Getenv("ZCODE_PROVIDER_ID"))
	if provider == "" {
		provider = zcodeDefaultProvider
	}
	modelArg := map[string]any{"workspacePath": workspace, "workspaceIdentity": workspace, "sessionId": sessionID, "model": map[string]any{"providerId": provider, "modelId": model}}
	if _, err := runtime.call(turnCtx, "zcode-agent", "setModel", []any{modelArg}); err != nil {
		return "", err
	}
	state, err := runtime.call(turnCtx, "zcode-agent", "sendPrompt", []any{map[string]any{
		"workspacePath": workspace, "workspaceIdentity": workspace, "sessionId": sessionID,
		"inputId": randomUUID(), "content": prompt,
	}})
	if err != nil {
		return "", err
	}
	deadline := time.Now().Add(zcodeTimeout("ZCODE_TURN_TIMEOUT_MS", 120000, 1, 900000))
	for time.Now().Before(deadline) {
		if text := zcodeAssistantText(state); text != "" && zcodeStatus(state) == "completed" {
			return text, nil
		}
		if zcodeStatus(state) == "error" || zcodeStatus(state) == "failed" {
			return "", errors.New(zcodeErrorText(state))
		}
		poll := zcodeTimeout("ZCODE_POLL_INTERVAL_MS", 250, 0, 10000)
		timer := time.NewTimer(poll)
		select {
		case <-turnCtx.Done():
			timer.Stop()
			return "", turnCtx.Err()
		case <-timer.C:
		}
		state, err = runtime.call(turnCtx, "zcode-agent", "readSession", []any{map[string]any{
			"workspacePath": workspace, "workspaceIdentity": workspace, "sessionId": sessionID, "messageLimit": 200,
		}})
		if err != nil {
			return "", err
		}
	}
	return "", fmt.Errorf("%w: ZCode turn timed out before an assistant response was available", ErrTruncated)
}

func zcodeReadOutput(ctx context.Context, reader io.Reader) ([]byte, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	data, err := io.ReadAll(io.LimitReader(reader, maxOutputBytes+1))
	if err != nil {
		return nil, err
	}
	if int64(len(data)) > maxOutputBytes {
		return nil, fmt.Errorf("%w: ZCode output exceeds byte limit", ErrUnsupported)
	}
	if len(bytes.TrimSpace(data)) == 0 {
		return nil, ErrTruncated
	}
	return data, nil
}

func zcodeCompletion(model, prompt, content string) []byte {
	value := map[string]any{
		"id": "chatcmpl-zcode-" + randomUUID(), "object": "chat.completion", "created": time.Now().Unix(), "model": model,
		"choices": []any{map[string]any{"index": 0, "message": map[string]any{"role": "assistant", "content": content}, "finish_reason": "stop"}},
	}
	_ = prompt
	data, _ := json.Marshal(value)
	return data
}

func zcodeSSE(model, content string) []byte {
	var out bytes.Buffer
	id, created := "chatcmpl-zcode-"+randomUUID(), time.Now().Unix()
	appendSSE(&out, map[string]any{"id": id, "object": "chat.completion.chunk", "created": created, "model": model, "choices": []any{map[string]any{"index": 0, "delta": map[string]any{"role": "assistant", "content": ""}, "finish_reason": nil}}})
	appendSSE(&out, map[string]any{"id": id, "object": "chat.completion.chunk", "created": created, "model": model, "choices": []any{map[string]any{"index": 0, "delta": map[string]any{"content": content}, "finish_reason": nil}}})
	appendSSE(&out, map[string]any{"id": id, "object": "chat.completion.chunk", "created": created, "model": model, "choices": []any{map[string]any{"index": 0, "delta": map[string]any{}, "finish_reason": "stop"}}})
	out.WriteString("data: [DONE]\n\n")
	return out.Bytes()
}

func (c *Client) doZCode(ctx context.Context, model string, req chatRequest, stream bool) (*http.Response, error) {
	model, err := zcodeModel(model)
	if err != nil {
		return nil, err
	}
	prompt, err := zcodePrompt(req, model, stream)
	if err != nil {
		return nil, err
	}
	content, err := runZcodeTurn(ctx, model, prompt)
	if err != nil {
		return nil, err
	}
	data := zcodeCompletion(model, prompt, content)
	contentType := "application/json"
	if stream {
		data = zcodeSSE(model, content)
		contentType = "text/event-stream"
	}
	return &http.Response{StatusCode: http.StatusOK, Header: http.Header{"Content-Type": []string{contentType}}, Body: io.NopCloser(bytes.NewReader(data)), ContentLength: int64(len(data))}, nil
}
