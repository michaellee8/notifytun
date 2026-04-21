//go:build !windows

package ssh

import (
	"os"
	"syscall"
)

func cancelProcess(process *os.Process) error {
	if process == nil {
		return nil
	}

	return process.Signal(syscall.SIGTERM)
}
