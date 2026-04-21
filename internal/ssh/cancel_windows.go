//go:build windows

package ssh

import "os"

func cancelProcess(process *os.Process) error {
	if process == nil {
		return nil
	}

	return process.Kill()
}
