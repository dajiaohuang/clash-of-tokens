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
	s.protect = func([]byte) ([]byte, error) { return nil, errors.New("locked") }
	if s.Put("cred://one", "api_key", "manual", "replacement") == nil {
		t.Fatal("write should fail")
	}
	if s.Resolve("cred://one") != "private-test-secret" {
		t.Fatal("failed transaction changed state")
	}
	if loaded.Delete("cred://one") != nil || loaded.Resolve("cred://one") != "" {
		t.Fatal("delete failed")
	}
}
