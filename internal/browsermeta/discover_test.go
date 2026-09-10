package browsermeta

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestDiscoverReadsMetadataOnly(t *testing.T) {
	root := t.TempDir()
	os.WriteFile(filepath.Join(root, "Local State"), []byte(`{"profile":{"info_cache":{"Default":{"name":"Personal","user_name":"must-not-return"}}},"os_crypt":{"encrypted_key":"must-not-return"}}`), 0600)
	os.WriteFile(filepath.Join(root, "profiles.ini"), []byte("[Profile0]\nName=Work\nPath=Profiles/work\nIsRelative=1\n"), 0600)
	rows := Discover([]Root{{"chrome", root}, {"firefox", root}})
	if len(rows) != 2 || rows[0].Authentication != "not_checked" {
		t.Fatal(rows)
	}
	data, _ := json.Marshal(rows)
	if strings.Contains(string(data), "must-not-return") {
		t.Fatal("non-profile metadata escaped")
	}
}
