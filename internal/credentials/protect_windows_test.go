//go:build windows

package credentials

import (
	"bytes"
	"path/filepath"
	"testing"
)

func TestDPAPIVaultRoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "vault")
	s, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	if err = s.Put("cred://dpapi", "cookie", "manual", "test-cookie"); err != nil {
		t.Fatal(err)
	}
	s, err = Open(path)
	if err != nil || s.Resolve("cred://dpapi") != "test-cookie" {
		t.Fatal("DPAPI reload", err)
	}
	b, err := protect([]byte("secret"))
	if err != nil || bytes.Contains(b, []byte("secret")) {
		t.Fatal("encryption", err)
	}
	b[len(b)-1] ^= 1
	if _, err = unprotect(b); err == nil {
		t.Fatal("tampered vault accepted")
	}
}
