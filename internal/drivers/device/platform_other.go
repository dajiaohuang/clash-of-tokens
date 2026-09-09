//go:build !windows

package device

import (
	"errors"
	"os/exec"
)

func hideCommand(*exec.Cmd) {}
func ReadClipboard() (string, error) {
	return "", errors.New("device: vivo clipboard input requires Windows")
}
func WriteClipboard(string) error { return errors.New("device: vivo clipboard input requires Windows") }
