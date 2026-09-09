package codingfinal

import (
	"bufio"
	"bytes"
	"context"
	"encoding/binary"
	"encoding/json"
	"errors"
	"io"
	"os"
	"strings"
	"testing"

	"clash-of-tokens/internal/config"
)

// TestZCodeHelperProcess is a small protocol peer used by the integration
// test below. It intentionally exercises split-free and binary-framed stdio,
// rather than mocking the app-server at the HTTP layer.
func TestZCodeHelperProcess(t *testing.T) {
	if os.Getenv("ZCODE_HELPER") != "1" {
		return
	}
	if os.Getenv("ZCODE_API_KEY") == "must-not-reach-zcode" {
		os.Exit(11)
	}
	reader := bufio.NewReader(os.Stdin)
	_, _ = io.WriteString(os.Stdout, `{"type":"zcode-hello","version":"test"}`+"\n")
	ack, err := reader.ReadBytes('\n')
	if err != nil || !bytes.Contains(ack, []byte("zcode-hello-ack")) {
		return
	}
	_, _ = os.Stdout.Write(zcodeTestFrame([]any{uint64(zcodeInitialize)}, nil))
	for {
		header, payload, err := zcodeTestReadFrame(reader)
		if err != nil {
			return
		}
		parts, ok := header.([]any)
		if !ok || len(parts) < 4 {
			continue
		}
		id := zcodeNumber(parts[1])
		method, _ := parts[3].(string)
		request := []any{}
		if values, ok := payload.([]any); ok {
			request = values
		}
		var response any = map[string]any{"ok": true}
		switch method {
		case "initialize":
			response = map[string]any{"available": true}
		case "createSession":
			response = map[string]any{"session": map[string]any{"sessionId": "test-session", "status": "idle"}}
		case "sendPrompt":
			response = map[string]any{"session": map[string]any{"sessionId": "test-session", "status": "running"}}
		case "readSession":
			response = map[string]any{"session": map[string]any{"sessionId": "test-session", "status": "completed"}, "messages": []any{
				map[string]any{"info": map[string]any{"role": "assistant"}, "parts": []any{map[string]any{"type": "text", "text": "zcode test response"}}},
			}}
		case "setModel":
			if len(request) == 0 {
				response = map[string]any{"error": map[string]any{"message": "missing model"}}
			}
		}
		_, _ = os.Stdout.Write(zcodeTestFrame([]any{uint64(zcodeResponse), id}, response))
	}
}

func TestZCodeAppServerTurn(t *testing.T) {
	t.Setenv("ZCODE_HELPER", "")
	t.Setenv("ZCODE_SERVER_RUNTIME_ROOT", t.TempDir())
	t.Setenv("ZCODE_SERVER_NODE", "")
	t.Setenv("ZCODE_SERVER_ENTRY", "")
	t.Setenv("ZCODE_API_KEY", "must-not-reach-zcode")
	t.Setenv("ZCODE_BIN", os.Args[0])
	t.Setenv("ZCODE_ARGS", ` ["-test.run=TestZCodeHelperProcess"] `)
	t.Setenv("ZCODE_CWD", "")
	t.Setenv("ZCODE_POLL_INTERVAL_MS", "1")
	t.Setenv("ZCODE_STARTUP_TIMEOUT_MS", "3000")
	t.Setenv("ZCODE_TURN_TIMEOUT_MS", "3000")

	oldBin := os.Getenv("ZCODE_BIN")
	defer os.Setenv("ZCODE_BIN", oldBin)
	// The helper marker is inherited by the app-server process. Keep it out of
	// the parent test process while the child is spawned by the adapter.
	if err := os.Setenv("ZCODE_HELPER", "1"); err != nil {
		t.Fatal(err)
	}
	defer os.Setenv("ZCODE_HELPER", "")
	t.Setenv("COT_ZCODE_ALLOW_UNSAFE", "1")

	client := New(config.Source{Adapter: AdapterZCode})
	defer client.Close()
	resp, err := client.Do(context.Background(), "chat", "glm-5.2", false, []byte(`{"messages":[{"role":"system","content":"You are a test assistant."},{"role":"user","content":"Reply with OK."}]}`), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var result map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		t.Fatal(err)
	}
	if result["object"] != "chat.completion" || result["model"] != "glm-5.2" {
		t.Fatalf("result=%v", result)
	}
	choices, _ := result["choices"].([]any)
	if len(choices) != 1 || !strings.Contains(string(mustJSON(t, choices[0])), "zcode test response") {
		t.Fatalf("choices=%v", choices)
	}
}

func TestZCodeRequiresExplicitUnsafeOptIn(t *testing.T) {
	t.Setenv("COT_ZCODE_ALLOW_UNSAFE", "")
	if _, err := runZcodeTurn(context.Background(), "glm-5.2", "[User]\nhello"); !errors.Is(err, ErrUnsupported) {
		t.Fatalf("err=%v", err)
	}
}

func mustJSON(t *testing.T, value any) []byte {
	t.Helper()
	data, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return data
}

func zcodeTestFrame(header, payload any) []byte {
	first, _ := encodeZcodeValue(header)
	second, _ := encodeZcodeValue(payload)
	body := append(first, second...)
	frame := make([]byte, zcodeHeaderSize+len(body))
	frame[0] = zcodeRegularMessage
	binary.BigEndian.PutUint32(frame[9:13], uint32(len(body)))
	copy(frame[zcodeHeaderSize:], body)
	return frame
}

func zcodeTestReadFrame(reader *bufio.Reader) (any, any, error) {
	var header [zcodeHeaderSize]byte
	if _, err := io.ReadFull(reader, header[:]); err != nil {
		return nil, nil, err
	}
	body := make([]byte, binary.BigEndian.Uint32(header[9:13]))
	if _, err := io.ReadFull(reader, body); err != nil {
		return nil, nil, err
	}
	first, offset, err := decodeZcodeValue(body, 0, 0)
	if err != nil {
		return nil, nil, err
	}
	second, _, err := decodeZcodeValue(body, offset, 0)
	return first, second, err
}
