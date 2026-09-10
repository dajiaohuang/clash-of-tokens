package codingmore

import (
	"strings"
	"testing"

	"clash-of-tokens/internal/config"
)

func TestSessionMetadataLifecycleForAmazonQ(t *testing.T) {
	c := New(config.Source{ID: "amazon-source", Adapter: AdapterAmazonQ})
	s, err := c.session("private-session-key")
	if err != nil {
		t.Fatal(err)
	}
	s.touch("amazon-q", "chat")
	items, err := c.Sessions()
	if err != nil || len(items) != 1 {
		t.Fatalf("sessions=%+v err=%v", items, err)
	}
	if items[0].ID == "private-session-key" || !strings.HasPrefix(items[0].ID, "session-") || items[0].Conversation != s.conversation {
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
		t.Fatalf("expired session was not reset: %+v", items)
	}
	if err := c.ChangeSession(items[0].ID, "clear"); err != nil {
		t.Fatal(err)
	}
	items, _ = c.Sessions()
	if len(items) != 0 {
		t.Fatalf("clear left metadata: %+v", items)
	}
}
