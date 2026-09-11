//go:build (linux || darwin) && !cot_test_keyring

package credentials

import (
	"bytes"
	"encoding/hex"
	"os"
	"path/filepath"
	"testing"

	"github.com/99designs/keyring"
)

func TestNativeKeyringRoundTrip(t *testing.T) {
	if os.Getenv("COT_TEST_NATIVE_KEYRING") != "1" {
		t.Skip("requires an explicitly enabled isolated native keychain test environment")
	}
	path := filepath.Join(t.TempDir(), "vault")
	s, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	if err = s.Put("cred://native-test", "api_key", "synthetic", "native-keyring-synthetic-secret"); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	ring, err := keyring.Open(nativeKeyringConfig())
	if err != nil {
		t.Fatal(err)
	}
	keyID := "vault-" + hex.EncodeToString(data[len(envelopeMagic):len(envelopeMagic)+keyIDSize])
	defer func() {
		if err := ring.Remove(keyID); err != nil {
			t.Errorf("cannot remove test-created key: %v", err)
		}
	}()
	if bytes.Contains(data, []byte("synthetic-secret")) {
		t.Fatal("plaintext persisted")
	}
	restored, err := Open(path)
	if err != nil || restored.Resolve("cred://native-test") != "native-keyring-synthetic-secret" {
		t.Fatalf("native reopen failed: %v", err)
	}
	if err = restored.Put("cred://native-test", "api_key", "synthetic", "rotated"); err != nil {
		t.Fatal(err)
	}
	again, err := Open(path)
	if err != nil || again.Resolve("cred://native-test") != "rotated" {
		t.Fatalf("native update failed: %v", err)
	}
}

func TestNativeKeyringUnavailable(t *testing.T) {
	if os.Getenv("COT_TEST_KEYRING_UNAVAILABLE") != "1" {
		t.Skip("requires an explicitly unavailable native keychain environment")
	}
	path := filepath.Join(t.TempDir(), "vault")
	s, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	if err = s.Put("cred://native-test", "api_key", "synthetic", "unavailable-test-secret"); err == nil {
		t.Fatal("unavailable keychain accepted a write")
	}
	if _, err = os.Stat(path); !os.IsNotExist(err) || len(s.List()) != 0 {
		t.Fatal("failed native write changed the store")
	}
}
