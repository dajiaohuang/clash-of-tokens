package chatgptweb

import (
	"clash-of-tokens/internal/config"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"
)

func TestSessionMetadataAndAdministrativeChanges(t *testing.T) {
	c := config.Default().Browser
	c.StateFile = filepath.Join(t.TempDir(), "sessions.json")
	d := New(c, "source")
	if err := d.store.put(Session{ID: "one", Source: "source", Account: "private-account-digest", HistoryHash: "private-history-digest", Conversation: "conversation", Model: "auto"}); err != nil {
		t.Fatal(err)
	}
	items, err := ReadSessions(c, "source")
	if err != nil || len(items) != 1 || items[0].Created.IsZero() {
		t.Fatal(items, err)
	}
	data, _ := json.Marshal(items)
	if strings.Contains(string(data), "private-") {
		t.Fatal("internal identity or history leaked")
	}
	if err = d.ChangeSession("one", "expire"); err != nil {
		t.Fatal(err)
	}
	items, _ = ReadSessions(c, "source")
	if !items[0].Expired {
		t.Fatal("expiration not persistent")
	}
	if err = d.ChangeSession("one", "clear"); err != nil {
		t.Fatal(err)
	}
	items, _ = ReadSessions(c, "source")
	if len(items) != 0 {
		t.Fatal("clear not persistent")
	}
}
