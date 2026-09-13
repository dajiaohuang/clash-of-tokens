//go:build !windows

package api

import "os/exec"

func startOwnedBrowser(cmd *exec.Cmd) (func() error, func(), error) {
	if err := cmd.Start(); err != nil {
		return nil, nil, err
	}
	return cmd.Process.Kill, func() {}, nil
}
