package codingnext

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"strings"
	"testing"
)

func TestAppendToolCallDeltasRejectsInvalidIndexesWithoutPanicking(t *testing.T) {
	for _, index := range []any{-1.0, 1.5, math.Inf(1), float64(maxToolCalls), int64(-1), int64(maxToolCalls)} {
		t.Run(formatTestValue(index), func(t *testing.T) {
			defer func() {
				if recovered := recover(); recovered != nil {
					t.Fatalf("appendToolCallDeltas panicked for index %v: %v", index, recovered)
				}
			}()
			_, err := appendToolCallDeltasChecked(nil, []any{map[string]any{
				"index":    index,
				"function": map[string]any{"arguments": "x"},
			}})
			if err == nil {
				t.Fatalf("accepted invalid index %v", index)
			}
			if got := appendToolCallDeltas(nil, []any{map[string]any{"index": index}}); got != nil {
				t.Fatalf("compatibility helper returned accumulated calls for invalid index: %v", got)
			}
		})
	}
}

func TestAppendToolCallDeltasAccumulatesArgumentsAndInput(t *testing.T) {
	calls, err := appendToolCallDeltasChecked(nil, []any{
		map[string]any{"index": 0.0, "id": "call-1", "function": map[string]any{
			"name": "lookup", "arguments": `{"city":"`, "input": "Paris"},
		},
		map[string]any{"index": 0.0, "function": map[string]any{"arguments": `"}`}},
	})
	if err != nil {
		t.Fatal(err)
	}
	call := calls[0].(map[string]any)
	function := call["function"].(map[string]any)
	if got, want := function["arguments"], `{"city":"Paris"}`; got != want {
		t.Fatalf("arguments=%q want %q", got, want)
	}
}

func TestStreamToOpenAIJSONBoundsCumulativeToolArguments(t *testing.T) {
	input := "data: {\"choices\":[{\"delta\":{\"tool_calls\":[{\"index\":0,\"function\":{\"arguments\":\"123456\"}}]}}]}\n\ndata: [DONE]\n\n"
	_, err := streamToOpenAIJSON(context.Background(), strings.NewReader(input), "model", 5)
	if err == nil {
		t.Fatal("accepted tool arguments after output limit")
	}
}

func TestOpenAIStreamErrorsNeverBecomeSuccess(t *testing.T) {
	for _, input := range []string{
		"data: {\"error\":{\"message\":\"quota exceeded\"}}\n\ndata: [DONE]\n\n",
		"data: {\"choices\":[{\"error\":{\"message\":\"provider failed\"}}]}\n\ndata: [DONE]\n\n",
	} {
		_, err := streamToOpenAIJSON(context.Background(), strings.NewReader(input), "model", maxResponseBytes)
		if err == nil || strings.Contains(err.Error(), "quota exceeded") || strings.Contains(err.Error(), "provider failed") {
			t.Fatalf("error event was accepted: %v", err)
		}
	}

	var sink bytes.Buffer
	err := copyValidatedOpenAISSE(context.Background(), strings.NewReader("data: {\"error\":\"upstream down\"}\n\ndata: [DONE]\n\n"), &sink)
	if err == nil || strings.Contains(err.Error(), "upstream down") {
		t.Fatalf("streaming error event was accepted: %v", err)
	}
	if sink.Len() != 0 {
		t.Fatalf("error event was forwarded before failing: %q", sink.String())
	}
}

func TestOpenAIEmptyTerminalFramesDoNotBecomeSuccess(t *testing.T) {
	for _, input := range []string{
		"data: [DONE]\n\n",
		"data: {\"choices\":[]}\n\ndata: [DONE]\n\n",
		"data: {\"status\":\"completed\"}\n\ndata: [DONE]\n\n",
	} {
		if _, err := streamToOpenAIJSON(context.Background(), strings.NewReader(input), "model", maxResponseBytes); !errors.Is(err, ErrTruncated) {
			t.Fatalf("empty terminal stream returned err=%v", err)
		}
	}

	var sink bytes.Buffer
	if err := copyValidatedOpenAISSE(context.Background(), strings.NewReader("data: [DONE]\n\n"), &sink); !errors.Is(err, ErrTruncated) {
		t.Fatalf("empty streaming terminal returned err=%v", err)
	}
	if sink.Len() != 0 {
		t.Fatalf("empty terminal was forwarded: %q", sink.String())
	}
}

func TestMalformedToolDeltaCannotBecomeSuccess(t *testing.T) {
	input := "data: {\"choices\":[{\"delta\":{\"tool_calls\":[{\"index\":0,\"function\":{\"arguments\":{}}}]}}]}\n\ndata: [DONE]\n\n"
	if _, err := streamToOpenAIJSON(context.Background(), strings.NewReader(input), "model", maxResponseBytes); err == nil {
		t.Fatal("accepted a non-text tool argument")
	}
}

func TestZedErrorsNeverBecomeSuccess(t *testing.T) {
	_, err := zedNDJSONToJSON(context.Background(), strings.NewReader("{\"event\":{\"error\":{\"message\":\"zed failed\"}}}\n{\"status\":\"stream_ended\"}\n"), "model", maxResponseBytes)
	if err == nil || strings.Contains(err.Error(), "zed failed") {
		t.Fatalf("Zed error event was accepted: %v", err)
	}
	if errors.Is(err, ErrTruncated) {
		t.Fatalf("Zed error was misclassified as truncation: %v", err)
	}
}

func TestZedEmptyTerminalAndToolDeltas(t *testing.T) {
	if _, err := zedNDJSONToJSON(context.Background(), strings.NewReader("{\"status\":\"stream_ended\"}\n"), "model", maxResponseBytes); !errors.Is(err, ErrTruncated) {
		t.Fatalf("empty Zed stream returned err=%v", err)
	}

	firstChoice := map[string]any{
		"delta": map[string]any{
			"tool_calls": []any{map[string]any{
				"index": 0, "function": map[string]any{"arguments": `{"x":`},
			}},
		},
	}
	secondChoice := map[string]any{
		"delta": map[string]any{
			"tool_calls": []any{map[string]any{
				"index": 0, "function": map[string]any{"arguments": `1}`},
			}},
		},
		"finish_reason": "tool_calls",
	}
	lines := []any{
		map[string]any{"event": map[string]any{"choices": []any{firstChoice}}},
		map[string]any{"event": map[string]any{"choices": []any{secondChoice}}},
		map[string]any{"status": "stream_ended"},
	}
	encoded := make([]string, 0, len(lines))
	for _, line := range lines {
		data, err := json.Marshal(line)
		if err != nil {
			t.Fatal(err)
		}
		encoded = append(encoded, string(data))
	}
	input := strings.Join(encoded, "\n") + "\n"
	data, err := zedNDJSONToJSON(context.Background(), strings.NewReader(input), "model", maxResponseBytes)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(data, []byte(`"arguments":"{\"x\":1}"`)) {
		t.Fatalf("Zed tool arguments were not accumulated: %s", data)
	}
}

func formatTestValue(value any) string {
	return strings.ReplaceAll(fmt.Sprint(value), " ", "_")
}
