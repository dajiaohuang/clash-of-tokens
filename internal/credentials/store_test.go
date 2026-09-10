package credentials

import (
	"bytes"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestEncryptedPersistenceRedactionAndFailedTransaction(t *testing.T) {
	block, _ := aes.NewCipher(make([]byte, 32))
	gcm, _ := cipher.NewGCM(block)
	seal := func(b []byte) ([]byte, error) {
		nonce := make([]byte, gcm.NonceSize())
		rand.Read(nonce)
		return gcm.Seal(nonce, nonce, b, nil), nil
	}
	unseal := func(b []byte) ([]byte, error) { return gcm.Open(nil, b[:gcm.NonceSize()], b[gcm.NonceSize():], nil) }
	path := filepath.Join(t.TempDir(), "vault")
	s, err := open(path, seal, unseal)
	if err != nil {
		t.Fatal(err)
	}
	if err = s.Put("cred://one", "api_key", "manual", "private-test-secret"); err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(path)
	if bytes.Contains(b, []byte("private-test-secret")) {
		t.Fatal("plaintext on disk")
	}
	b, _ = json.Marshal(s.List())
	if bytes.Contains(b, []byte("private-test-secret")) {
		t.Fatal("secret in metadata")
	}
	loaded, err := open(path, seal, unseal)
	if err != nil || loaded.Resolve("cred://one") != "private-test-secret" {
		t.Fatal("reload failed", err)
	}
	if loaded.List()[0].LastUsedAt == nil {
		t.Fatal("credential use was not recorded")
	}
	used := loaded.List()[0].LastUsedAt
	if err := loaded.Put("cred://two", "api_key", "manual", "second-secret"); err != nil {
		t.Fatal("metadata update write failed", err)
	}
	reopened, err := open(path, seal, unseal)
	if err != nil || reopened.List()[0].LastUsedAt == nil || !reopened.List()[0].LastUsedAt.Equal(*used) {
		t.Fatalf("last-use metadata was not persisted with the next write: err=%v metadata=%+v", err, reopened.List())
	}
	s.protect = func([]byte) ([]byte, error) { return nil, errors.New("locked") }
	_ = s.Resolve("cred://one")
	if s.Put("cred://one", "api_key", "manual", "replacement") == nil {
		t.Fatal("write should fail")
	}
	if s.Resolve("cred://one") != "private-test-secret" {
		t.Fatal("failed transaction changed state")
	}
	if s.List()[0].Version != 1 {
		t.Fatal("failed transaction changed credential version")
	}
	if s.List()[0].LastUsedAt == nil {
		t.Fatal("failed transaction changed last-use state")
	}
	if err = loaded.Put("cred://one", "api_key", "manual", "rotated-secret"); err != nil || loaded.List()[0].Version != 2 {
		t.Fatal("successful replacement did not increment version", err)
	}
	rotated, err := open(path, seal, unseal)
	if err != nil || rotated.List()[0].Version != 2 {
		t.Fatal("credential version did not survive reopen", err)
	}
	if loaded.Delete("cred://one") != nil || loaded.Resolve("cred://one") != "" {
		t.Fatal("delete failed")
	}
}

func TestUsernamePasswordMaterialIsStructured(t *testing.T) {
	seal := func(b []byte) ([]byte, error) { return append([]byte("sealed:"), b...), nil }
	unseal := func(b []byte) ([]byte, error) { return bytes.TrimPrefix(b, []byte("sealed:")), nil }
	s, err := open(filepath.Join(t.TempDir(), "vault"), seal, unseal)
	if err != nil {
		t.Fatal(err)
	}
	for _, value := range []string{"secret", `{"username":"u"}`, `{"password":"p"}`, `{"username":"u","password":"p","extra":"x"}`} {
		if err := s.Put("cred://bad", "username_password", "test", value); err == nil {
			t.Fatalf("invalid username/password material accepted: %q", value)
		}
	}
	if err := s.Put("cred://login", "username_password", "test", `{"username":"u","password":"p"}`); err != nil {
		t.Fatal(err)
	}
	if got := s.Resolve("cred://login"); got != `{"username":"u","password":"p"}` {
		t.Fatalf("structured material changed: %q", got)
	}
}
