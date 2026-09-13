package credentials

import (
	"path/filepath"
	"testing"
)

func TestDeletedReferenceCannotBeRevivedAfterRestart(t *testing.T) {
	path := filepath.Join(t.TempDir(), "vault")
	seal := func(b []byte) ([]byte, error) { return append([]byte(nil), b...), nil }
	s, err := open(path, seal, seal)
	if err != nil {
		t.Fatal(err)
	}
	if err = s.Put("cred://revoked", "api_key", "fixture", "old-secret"); err != nil {
		t.Fatal(err)
	}
	if err = s.Delete("cred://revoked"); err != nil {
		t.Fatal(err)
	}
	s, err = open(path, seal, seal)
	if err != nil {
		t.Fatal(err)
	}
	if len(s.List()) != 0 || s.Resolve("cred://revoked") != "" {
		t.Fatal("deleted value visible")
	}
	if err = s.Put("cred://revoked", "api_key", "fixture", "new-secret"); err == nil {
		t.Fatal("revoked reference reused")
	}
	if err = s.Put("cred://replacement", "api_key", "fixture", "new-secret"); err != nil {
		t.Fatal(err)
	}
}
