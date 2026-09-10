package upstream

import (
	"strings"
	"testing"

	"clash-of-tokens/internal/config"
)

func TestSessionManagerDispatchKeepsUnsupportedAdaptersExplicit(t *testing.T) {
	stateful := NewConfigured(config.Source{ID: "yuanbao-source", Adapter: "yuanbao", BaseURL: "https://yuanbao.tencent.com", KeyEnv: "COT_TEST_TOKEN"}, config.Browser{}, config.Device{})
	defer stateful.Close()
	items, err := stateful.Sessions()
	if err != nil || len(items) != 0 {
		t.Fatalf("stateful session inventory=%+v err=%v", items, err)
	}
	stateless := NewConfigured(config.Source{ID: "openai-source", Adapter: "openai", BaseURL: "https://api.openai.com", KeyEnv: "COT_TEST_TOKEN"}, config.Browser{}, config.Device{})
	defer stateless.Close()
	if _, err := stateless.Sessions(); err == nil || !strings.Contains(err.Error(), "not implemented") {
		t.Fatalf("unsupported session capability was not explicit: %v", err)
	}
}
