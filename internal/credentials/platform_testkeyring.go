//go:build cot_test_keyring

package credentials

import (
	"bytes"
	"errors"
	"sync"
)

// The test keyring is deliberately available only with the cot_test_keyring
// build tag. It keeps random vault keys in this test process so Linux CI can
// exercise the complete control plane without requiring a desktop Secret
// Service. Production builds always use the platform-native implementation.
var testKeyring = struct {
	sync.Mutex
	keys map[string][]byte
}{keys: map[string][]byte{}}

func platformProtection() (func([]byte) ([]byte, error), func([]byte) ([]byte, error)) {
	p := &keyEnvelope{
		get: func(id string) ([]byte, error) {
			testKeyring.Lock()
			defer testKeyring.Unlock()
			key, ok := testKeyring.keys[id]
			if !ok {
				return nil, errors.New("test key not found")
			}
			return bytes.Clone(key), nil
		},
		put: func(id string, key []byte) error {
			testKeyring.Lock()
			defer testKeyring.Unlock()
			testKeyring.keys[id] = bytes.Clone(key)
			return nil
		},
	}
	return p.seal, p.unseal
}
