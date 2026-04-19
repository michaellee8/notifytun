// Package ssh wraps the system `ssh` binary for notifytun's local -> remote
// tunnel. Identity, host-key, and jump-host behavior are delegated entirely
// to the user's ssh configuration (~/.ssh/config, ssh-agent, etc.).
package ssh

import (
	"context"
	"fmt"
	"io"
	"os/exec"
	"sync"
	"syscall"
	"time"
)

const (
	// waitDelay is the grace period between SIGTERM and SIGKILL on shutdown.
	waitDelay = 5 * time.Second
)

// Session wraps a running `ssh` subprocess and exposes its stdio pipes.
type Session struct {
	Stdout io.Reader
	Stderr io.Reader

	cmd    *exec.Cmd
	cancel context.CancelFunc

	waitOnce sync.Once
	waitErr  error
}

// Connect starts `ssh` with the configured options and returns once the
// subprocess is started. The remote command is passed as a single argv
// element; ssh concatenates it with spaces and hands it to the remote
// login shell. Cancelling ctx or calling Close terminates the subprocess.
func Connect(ctx context.Context, target, remoteCmd string) (*Session, error) {
	runCtx, cancel := context.WithCancel(ctx)

	args := []string{
		"-T",
		"-o", "BatchMode=yes",
		"-o", "ConnectTimeout=10",
		"-o", "ServerAliveInterval=15",
		"-o", "ServerAliveCountMax=3",
		"--",
		target,
		remoteCmd,
	}

	cmd := exec.CommandContext(runCtx, "ssh", args...)
	cmd.Cancel = func() error {
		if cmd.Process == nil {
			return nil
		}
		return cmd.Process.Signal(syscall.SIGTERM)
	}
	cmd.WaitDelay = waitDelay

	stdout, err := cmd.StdoutPipe()
	if err != nil {
		cancel()
		return nil, fmt.Errorf("stdout pipe: %w", err)
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		cancel()
		return nil, fmt.Errorf("stderr pipe: %w", err)
	}

	if err := cmd.Start(); err != nil {
		cancel()
		return nil, fmt.Errorf("start ssh: %w", err)
	}

	return &Session{
		Stdout: stdout,
		Stderr: stderr,
		cmd:    cmd,
		cancel: cancel,
	}, nil
}

// Wait blocks until the ssh subprocess exits and returns the exit status.
// Safe to call more than once; subsequent calls return the cached result.
func (s *Session) Wait() error {
	if s == nil {
		return nil
	}
	s.waitOnce.Do(func() {
		s.waitErr = s.cmd.Wait()
		s.cancel()
	})
	return s.waitErr
}

// Close cancels the run context (triggering SIGTERM on the subprocess) and
// waits for it to exit. If the process does not exit within waitDelay, the
// stdio pipes are closed and the process is killed.
func (s *Session) Close() error {
	if s == nil {
		return nil
	}
	s.cancel()
	return s.Wait()
}
