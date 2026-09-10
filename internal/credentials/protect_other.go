//go:build !windows

package credentials

import "errors"

// Fail closed until a platform keychain is connected; never write plaintext.
func protect([]byte) ([]byte, error) {
	return nil, errors.New("OS credential protection is not configured on this platform")
}
func unprotect([]byte) ([]byte, error) {
	return nil, errors.New("OS credential protection is not configured on this platform")
}
