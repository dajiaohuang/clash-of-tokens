package credentials

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestKeyEnvelopeRoundTripAndTamper(t *testing.T) {
	keys := map[string][]byte{}
	writes := 0
	makeEnvelope := func() *keyEnvelope {
		return &keyEnvelope{
			get: func(id string) ([]byte, error) {
				key, ok := keys[id]
				if !ok {
					return nil, errors.New("missing")
				}
				return bytes.Clone(key), nil
			},
			put: func(id string, key []byte) error { writes++; keys[id] = bytes.Clone(key); return nil },
		}
	}
	p := makeEnvelope()
	plain := []byte(`{"secret":"synthetic-keychain-test"}`)
	first, err := p.seal(plain)
	if err != nil || bytes.Contains(first, plain) {
		t.Fatalf("seal failed: %v", err)
	}
	second, err := p.seal(plain)
	if err != nil || bytes.Equal(first, second) || writes != 1 {
		t.Fatalf("key reuse / nonce freshness failed: %v writes=%d", err, writes)
	}
	restored := makeEnvelope()
	decoded, err := restored.unseal(first)
	if err != nil || !bytes.Equal(decoded, plain) {
		t.Fatalf("reopen failed: %v", err)
	}
	if _, err = restored.seal(plain); err != nil || writes != 1 {
		t.Fatalf("reopened vault replaced key: %v", err)
	}
	for i := range first {
		damaged := bytes.Clone(first)
		damaged[i] ^= 1
		if _, err = makeEnvelope().unseal(damaged); err == nil {
			t.Fatalf("tamper accepted at %d", i)
		}
	}
	for i := 0; i < len(first); i++ {
		if _, err = makeEnvelope().unseal(first[:i]); err == nil {
			t.Fatalf("truncated data accepted at %d", i)
		}
	}
	clear(keys)
	if _, err = restored.unseal(first); err == nil {
		t.Fatal("missing key accepted")
	}
	if _, err = restored.seal(plain); err == nil || writes != 1 {
		t.Fatal("missing key silently replaced")
	}
}

func TestKeychainFailurePreservesVault(t *testing.T) {
	var savedKey []byte
	unavailable := false
	p := &keyEnvelope{
		get: func(string) ([]byte, error) {
			if unavailable {
				return nil, errors.New("private backend error")
			}
			return bytes.Clone(savedKey), nil
		},
		put: func(_ string, key []byte) error {
			if unavailable {
				return errors.New("private backend error")
			}
			savedKey = bytes.Clone(key)
			return nil
		},
	}
	path := filepath.Join(t.TempDir(), "vault")
	s, err := open(path, p.seal, p.unseal)
	if err != nil {
		t.Fatal(err)
	}
	unavailable = true
	if err = s.Put("cred://test", "api_key", "test", "first"); err == nil || bytes.Contains([]byte(err.Error()), []byte("private")) {
		t.Fatalf("unavailable keychain failure: %v", err)
	}
	if _, err = os.Stat(path); !os.IsNotExist(err) || len(s.List()) != 0 {
		t.Fatal("failed write changed store")
	}
	unavailable = false
	if err = s.Put("cred://test", "api_key", "test", "first"); err != nil {
		t.Fatal(err)
	}
	before, _ := os.ReadFile(path)
	unavailable = true
	if err = s.Put("cred://test", "api_key", "test", "second"); err == nil {
		t.Fatal("locked keychain accepted")
	}
	after, _ := os.ReadFile(path)
	if !bytes.Equal(before, after) || s.Resolve("cred://test") != "first" {
		t.Fatal("failed encryption modified existing vault")
	}
}
