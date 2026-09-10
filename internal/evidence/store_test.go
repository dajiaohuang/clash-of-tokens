package evidence

import (
	"os"
	"path/filepath"
	"testing"
)

func TestEvidenceRestartAndFailedCommit(t *testing.T) {
	path := filepath.Join(t.TempDir(), "history")
	s, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	if err = s.Append(Entry{Binding: "test-binding", Revision: 7, Kind: "validation", Resource: "source", Status: "verified"}); err != nil {
		t.Fatal(err)
	}
	reloaded, err := Open(path)
	if err != nil || len(reloaded.List()) != 1 || reloaded.List()[0].Revision != 7 || reloaded.List()[0].Binding != "test-binding" {
		t.Fatal("history not preserved", err)
	}
	copy := s.List()
	copy[0].Status = "changed"
	if s.List()[0].Status != "verified" {
		t.Fatal("caller mutated history")
	}
	s.path = filepath.Join(path, "child") // A file cannot become a directory.
	if s.Append(Entry{Status: "failed"}) == nil || len(s.List()) != 1 {
		t.Fatal("failed write changed history")
	}
	if _, err = os.Stat(path); err != nil {
		t.Fatal("prior history lost")
	}
}
