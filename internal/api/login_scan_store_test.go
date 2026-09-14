package api

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLoginScanRecoveryAndRedaction(t *testing.T) {
	path := filepath.Join(t.TempDir(), "scan")
	q := loginBatchQueue{path: path, ticket: "secret-ticket", view: loginBatchView{ID: "scan-1", State: "running", Connection: loginConnection{Endpoint: "ws://127.0.0.1:1234/devtools/browser/secret"}, Sites: []loginSite{{Provider: "one", State: "candidate_saved", Account: "saved"}, {Provider: "two", State: "queued"}}}}
	if err := q.save(); err != nil {
		t.Fatal(err)
	}
	data, _ := os.ReadFile(path)
	if strings.Contains(string(data), "secret") || strings.Contains(string(data), "Endpoint") {
		t.Fatal("connection authorization leaked to history")
	}
	var restored loginBatchQueue
	if err := restored.load(path); err != nil {
		t.Fatal(err)
	}
	if restored.view.State != "interrupted" || restored.view.Sites[0].Account != "saved" || restored.view.Sites[1].State != "not_attempted" || restored.cancel != nil || restored.ticket != "" {
		t.Fatal("unsafe recovery", restored.view)
	}
	var again loginBatchQueue
	if err := again.load(path); err != nil || again.view.State != "interrupted" {
		t.Fatal("recovery not persisted", err)
	}
}

func TestLoginScanRejectsInvalidHistory(t *testing.T) {
	for _, data := range []string{`{"state":"running","unexpected":true}`, strings.Repeat("x", (1<<20)+1)} {
		path := filepath.Join(t.TempDir(), "scan")
		if err := os.WriteFile(path, []byte(data), 0600); err != nil {
			t.Fatal(err)
		}
		var q loginBatchQueue
		if q.load(path) == nil {
			t.Fatal("invalid history accepted")
		}
	}
}
