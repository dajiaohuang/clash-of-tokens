package china

import (
	"strings"
	"testing"

	"clash-of-tokens/internal/config"
)

func TestSessionMetadataUsesHashedIDsAndSupportsLifecycle(t *testing.T) {
	c := New(config.Source{ID: "kimi-source", Adapter: adapterKimi})
	state, err := c.session("private-session-key")
	if err != nil {
		t.Fatal(err)
	}
	state.touch("kimi", "chat")
	state.mu.Lock()
	state.kimiChatID = "provider-conversation"
	state.mu.Unlock()
	items, err := c.Sessions()
	if err != nil || len(items) != 1 {
		t.Fatalf("sessions=%+v err=%v", items, err)
	}
	if items[0].ID == "private-session-key" || !strings.HasPrefix(items[0].ID, "session-") {
		t.Fatalf("session key leaked or malformed id: %q", items[0].ID)
	}
	if items[0].Conversation != "provider-conversation" || items[0].Source != "kimi-source" {
		t.Fatalf("metadata=%+v", items[0])
	}
	if err := c.ChangeSession(items[0].ID, "expire"); err != nil {
		t.Fatal(err)
	}
	items, _ = c.Sessions()
	if len(items) != 1 || !items[0].Expired {
		t.Fatalf("expired session not visible: %+v", items)
	}
	if _, err := c.session("private-session-key"); err != nil {
		t.Fatal(err)
	}
	items, _ = c.Sessions()
	if len(items) != 1 || items[0].Expired {
		t.Fatalf("expired session was not reset on reuse: %+v", items)
	}
	if err := c.ChangeSession(items[0].ID, "clear"); err != nil {
		t.Fatal(err)
	}
	items, _ = c.Sessions()
	if len(items) != 0 {
		t.Fatalf("clear left session metadata: %+v", items)
	}
}
