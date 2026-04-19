package ssh_test

import (
	"context"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	tunnelssh "github.com/michaellee8/notifytun/internal/ssh"
)

// installFakeSSH writes a shell script named "ssh" into a fresh temp dir,
// makes it executable, and sets PATH to that dir. Returns the script dir
// (useful when a test wants to write sibling files, like an argv capture).
func installFakeSSH(t *testing.T, script string) string {
	t.Helper()

	dir := t.TempDir()
	sshPath := filepath.Join(dir, "ssh")
	if err := os.WriteFile(sshPath, []byte(script), 0o755); err != nil {
		t.Fatalf("WriteFile(fake ssh): %v", err)
	}
	t.Setenv("PATH", dir)
	return dir
}

func TestConnectHappyPath(t *testing.T) {
	dir := installFakeSSH(t, `#!/bin/sh
for a in "$@"; do
  printf '%s\n' "$a" >> "$ARGV_FILE"
done
printf '{"type":"heartbeat"}\n'
`)
	argvFile := filepath.Join(dir, "argv.txt")
	t.Setenv("ARGV_FILE", argvFile)

	sess, err := tunnelssh.Connect(context.Background(), "example.com", "echo hi")
	if err != nil {
		t.Fatalf("Connect: %v", err)
	}
	defer sess.Close()

	out, err := io.ReadAll(sess.Stdout)
	if err != nil {
		t.Fatalf("ReadAll(stdout): %v", err)
	}
	if err := sess.Wait(); err != nil {
		t.Fatalf("Wait: %v", err)
	}
	if got := strings.TrimSpace(string(out)); got != `{"type":"heartbeat"}` {
		t.Fatalf("unexpected stdout %q", got)
	}

	argvBytes, err := os.ReadFile(argvFile)
	if err != nil {
		t.Fatalf("ReadFile(argv): %v", err)
	}
	args := strings.Split(strings.TrimRight(string(argvBytes), "\n"), "\n")

	// Required flags/options must all appear.
	mustContain := []string{
		"-T",
		"BatchMode=yes",
		"ConnectTimeout=10",
		"ServerAliveInterval=15",
		"ServerAliveCountMax=3",
		"example.com",
		"echo hi",
	}
	for _, want := range mustContain {
		found := false
		for _, a := range args {
			if a == want || strings.Contains(a, want) {
				found = true
				break
			}
		}
		if !found {
			t.Fatalf("expected argv to contain %q, got %v", want, args)
		}
	}

	// Positional ordering: target must come before remoteCmd, and both
	// must come after the last `-o` group. We do not assert inter-`-o` order.
	indexOf := func(needle string) int {
		for i, a := range args {
			if a == needle {
				return i
			}
		}
		return -1
	}
	targetIdx := indexOf("example.com")
	remoteIdx := indexOf("echo hi")
	if targetIdx < 0 || remoteIdx < 0 {
		t.Fatalf("target or remote not in argv: %v", args)
	}
	if !(targetIdx < remoteIdx) {
		t.Fatalf("expected target before remoteCmd, got target=%d remote=%d", targetIdx, remoteIdx)
	}
	lastOpt := -1
	for i, a := range args {
		if strings.HasPrefix(a, "-") || i > 0 && args[i-1] == "-o" {
			if i > lastOpt {
				lastOpt = i
			}
		}
	}
	if !(targetIdx > lastOpt) {
		t.Fatalf("expected target after last option; argv=%v", args)
	}
}

func TestConnectStreamsStderr(t *testing.T) {
	installFakeSSH(t, `#!/bin/sh
printf 'diag line\n' >&2
`)

	sess, err := tunnelssh.Connect(context.Background(), "example.com", "true")
	if err != nil {
		t.Fatalf("Connect: %v", err)
	}
	defer sess.Close()

	stderr, err := io.ReadAll(sess.Stderr)
	if err != nil {
		t.Fatalf("ReadAll(stderr): %v", err)
	}
	if err := sess.Wait(); err != nil {
		t.Fatalf("Wait: %v", err)
	}
	if got := strings.TrimSpace(string(stderr)); got != "diag line" {
		t.Fatalf("unexpected stderr %q", got)
	}
}

func TestConnectNonZeroExit(t *testing.T) {
	installFakeSSH(t, `#!/bin/sh
exit 5
`)

	sess, err := tunnelssh.Connect(context.Background(), "example.com", "true")
	if err != nil {
		t.Fatalf("Connect: %v", err)
	}
	defer sess.Close()

	// Drain stdout so Wait can return.
	_, _ = io.Copy(io.Discard, sess.Stdout)

	err = sess.Wait()
	var exitErr *exec.ExitError
	if !errors.As(err, &exitErr) {
		t.Fatalf("expected *exec.ExitError, got %T: %v", err, err)
	}
	if exitErr.ExitCode() != 5 {
		t.Fatalf("expected exit 5, got %d", exitErr.ExitCode())
	}
}

func TestConnectCtxCancelKillsProcess(t *testing.T) {
	installFakeSSH(t, `#!/bin/sh
sleep 60
`)

	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()

	sess, err := tunnelssh.Connect(ctx, "example.com", "true")
	if err != nil {
		t.Fatalf("Connect: %v", err)
	}
	defer sess.Close()

	// Drain stdout/stderr concurrently so the pipes don't block Wait.
	go func() {
		_, _ = io.Copy(io.Discard, sess.Stdout)
	}()
	go func() {
		_, _ = io.Copy(io.Discard, sess.Stderr)
	}()

	start := time.Now()
	err = sess.Wait()
	elapsed := time.Since(start)
	if err == nil {
		t.Fatal("expected non-nil Wait error after ctx cancel")
	}
	if elapsed > 1*time.Second {
		t.Fatalf("expected Wait to return promptly after ctx cancel, took %s", elapsed)
	}
}

func TestConnectSSHNotFound(t *testing.T) {
	// Empty PATH with no ssh anywhere. Use a dir guaranteed not to contain ssh.
	emptyDir := t.TempDir()
	t.Setenv("PATH", emptyDir)

	_, err := tunnelssh.Connect(context.Background(), "example.com", "true")
	if err == nil {
		t.Fatal("expected error when ssh is not on PATH")
	}
	if !strings.Contains(err.Error(), "ssh") {
		t.Fatalf("expected error to mention ssh, got %v", err)
	}
}
