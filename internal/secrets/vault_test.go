package secrets

import (
	"os"
	"path/filepath"
	"testing"
)

func TestVaultRoundTripAndTampering(t *testing.T) {
	path := filepath.Join(t.TempDir(), "keys")
	a, e := LoadOrCreate(path)
	if e != nil {
		t.Fatal(e)
	}
	b, e := LoadOrCreate(path)
	if e != nil || a != b || a.API == a.Admin {
		t.Fatal("keys changed or are not isolated")
	}
	if e = os.WriteFile(path, []byte("corrupted"), 0600); e != nil {
		t.Fatal(e)
	}
	if _, e = LoadOrCreate(path); e == nil {
		t.Fatal("corrupt key store accepted")
	}
}
