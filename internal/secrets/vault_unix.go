//go:build !windows

package secrets

// On Unix, confidentiality is provided by the 0700 directory and 0600 file.
// Integrators needing encryption should supply keys through their secret manager.
func seal(b []byte) ([]byte, error)   { return b, nil }
func unseal(b []byte) ([]byte, error) { return b, nil }
