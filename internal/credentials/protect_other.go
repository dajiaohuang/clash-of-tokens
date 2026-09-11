//go:build !windows && !linux && !darwin && !cot_test_keyring

package credentials

import "errors"

func platformProtection() (func([]byte) ([]byte, error), func([]byte) ([]byte, error)) {
	return protect, unprotect
}

// Fail closed until a platform keychain is connected; never write plaintext.
func protect([]byte) ([]byte, error) {
	return nil, errors.New("OS credential protection is not configured on this platform")
}
func unprotect([]byte) ([]byte, error) {
	return nil, errors.New("OS credential protection is not configured on this platform")
}
