package chinafinal

import (
	"context"
	"io"
	"strings"
	"testing"
)

func TestTencentSSERejectsErrorsAndTruncation(t *testing.T) {
	for _, raw := range []string{"data: {\"error\":{\"message\":\"private\"}}\n\ndata: [DONE]\n\n", "data: {\"choices\":[{\"delta\":{\"content\":\"partial\"}}]}\n\n", "data: [DONE]\n\n"} {
		_, err := collectTencentSSE(strings.NewReader(raw), "model")
		if err == nil || strings.Contains(err.Error(), "private") {
			t.Fatal(err)
		}
		body := validatedTencentSSE(context.Background(), io.NopCloser(strings.NewReader(raw)))
		_, err = io.ReadAll(body)
		body.Close()
		if err == nil {
			t.Fatal("stream accepted invalid completion")
		}
	}
}

func TestTencentStreamingValidationPreservesReasoning(t *testing.T) {
	raw := "data: {\"choices\":[{\"index\":0,\"delta\":{\"reasoning_content\":\"reason\",\"content\":\"answer\"},\"finish_reason\":\"length\"}]}\n\ndata: [DONE]\n\n"
	body := validatedTencentSSE(context.Background(), io.NopCloser(strings.NewReader(raw)))
	data, err := io.ReadAll(body)
	body.Close()
	if err != nil || string(data) != raw {
		t.Fatalf("%q %v", data, err)
	}
	value, err := collectTencentSSE(strings.NewReader(raw), "model")
	if err != nil {
		t.Fatal(err)
	}
	choice := value["choices"].([]any)[0].(map[string]any)
	if choice["finish_reason"] != "length" || choice["message"].(map[string]any)["reasoning_content"] != "reason" {
		t.Fatal(value)
	}
}
