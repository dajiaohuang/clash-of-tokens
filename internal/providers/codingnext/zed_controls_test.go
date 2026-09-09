package codingnext

import (
	"testing"
)

func TestZedRejectsLostMessageFields(t *testing.T) {
	for _, field := range []string{"name", "function_call", "tool_call_id"} {
		input := map[string]any{"messages": []any{map[string]any{"role": "user", "content": "hi", field: "value"}}}
		if _, err := zedProviderRequest(zedProviderAnthropic, "claude", input, true); err == nil {
			t.Fatalf("accepted lost field %s", field)
		}
	}
}

func TestZedRejectsInvalidTokenBudget(t *testing.T) {
	for _, v := range []any{-1.0, 0.0, 1.5, "100"} {
		input := map[string]any{"messages": []any{map[string]any{"role": "user", "content": "hi"}}, "max_tokens": v}
		if _, err := zedProviderRequest(zedProviderGoogle, "gemini", input, true); err == nil {
			t.Fatalf("accepted invalid budget %v", v)
		}
	}
}

func TestZedResponsesReasoningEnvelope(t *testing.T) {
	input := map[string]any{"messages": []any{map[string]any{"role": "user", "content": "hi"}}, "reasoning_effort": "high"}
	v, err := zedProviderRequest(zedProviderOpenAI, "gpt-test", input, true)
	if err != nil {
		t.Fatal(err)
	}
	p := v.(map[string]any)
	if p["reasoning_effort"] != nil || p["reasoning"].(map[string]any)["effort"] != "high" {
		t.Fatalf("wrong reasoning translation: %v", p)
	}
}
