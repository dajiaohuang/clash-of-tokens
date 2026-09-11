//go:build (linux || darwin) && !cot_test_keyring

package credentials

import (
	"errors"
	"github.com/99designs/keyring"
	"os"
	"runtime"
	"strings"
)

func nativeKeyringConfig() keyring.Config {
	backend := keyring.SecretServiceBackend
	if runtime.GOOS == "darwin" {
		backend = keyring.KeychainBackend
	}
	c := keyring.Config{AllowedBackends: []keyring.BackendType{backend}, ServiceName: "clash-of-tokens", LibSecretCollectionName: "login", KeychainAccessibleWhenUnlocked: true}
	if name := strings.TrimSpace(os.Getenv("COT_TEST_KEYCHAIN")); name != "" {
		// The hosted native test supplies a temporary keychain base name. The
		// keyring library appends its platform suffix when opening it.
		c.KeychainName = name
	}
	if os.Getenv("COT_TEST_KEYCHAIN_TRUST") == "1" {
		// The temporary CI keychain is isolated and synthetic; trust the test
		// process so macOS does not open an interactive authorization prompt.
		c.KeychainTrustApplication = true
	}
	return c
}

func platformProtection() (func([]byte) ([]byte, error), func([]byte) ([]byte, error)) {
	// Opening an empty store must not prompt. Connect on the first actual read
	// or write, and permit only the native OS backend (never file/pass fallbacks).
	var ring keyring.Keyring
	connect := func() error {
		if ring != nil {
			return nil
		}
		var err error
		ring, err = keyring.Open(nativeKeyringConfig())
		if err != nil {
			ring = nil
			return errors.New("native OS keychain unavailable; macOS requires a cgo build and Linux requires Secret Service")
		}
		return nil
	}
	p := &keyEnvelope{
		get: func(id string) ([]byte, error) {
			if err := connect(); err != nil {
				return nil, err
			}
			item, err := ring.Get("vault-" + id)
			return item.Data, err
		},
		put: func(id string, key []byte) error {
			if err := connect(); err != nil {
				return err
			}
			return ring.Set(keyring.Item{Key: "vault-" + id, Data: key, Label: "Clash of Tokens vault encryption key", KeychainNotSynchronizable: true})
		},
	}
	return p.seal, p.unseal
}
