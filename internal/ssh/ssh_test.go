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

// requireBinSleep skips the test if /bin/sleep is not executable — fake-ssh
// scripts that need a long-running child rely on its absolute path since
// installFakeSSH replaces PATH with a dir containing only the fake ssh.
func requireBinSleep(t *testing.T) {
	t.Helper()
	info, err := os.Stat("/bin/sleep")
	if err != nil {
		t.Skipf("/bin/sleep not available: %v", err)
	}
	if info.Mode()&0o111 == 0 {
		t.Skipf("/bin/sleep not executable: mode %v", info.Mode())
	}
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

	// Pin each `-o key=value` as an actual flag pair, not just a loose token.
	expectOptionPair := func(value string) {
		for i := 0; i < len(args)-1; i++ {
			if args[i] == "-o" && args[i+1] == value {
				return
			}
		}
		t.Fatalf("expected `-o %s` pair in argv, got %v", value, args)
	}
	expectOptionPair("BatchMode=yes")
	expectOptionPair("ConnectTimeout=10")
	expectOptionPair("ServerAliveInterval=15")
	expectOptionPair("ServerAliveCountMax=3")

	contains := func(needle string) bool {
		for _, a := range args {
			if a == needle {
				return true
			}
		}
		return false
	}
	for _, want := range []string{"-T", "--", "example.com", "echo hi"} {
		if !contains(want) {
			t.Fatalf("expected argv to contain %q, got %v", want, args)
		}
	}

	indexOf := func(needle string) int {
		for i, a := range args {
			if a == needle {
				return i
			}
		}
		return -1
	}
	sepIdx := indexOf("--")
	targetIdx := indexOf("example.com")
	remoteIdx := indexOf("echo hi")
	if sepIdx < 0 || targetIdx < 0 || remoteIdx < 0 {
		t.Fatalf("missing separator, target, or remote in argv: %v", args)
	}
	// `--` must separate options from positional args, with target then remote after it.
	if !(sepIdx < targetIdx && targetIdx < remoteIdx) {
		t.Fatalf("expected order `--` < target < remote, got sep=%d target=%d remote=%d (%v)",
			sepIdx, targetIdx, remoteIdx, args)
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
	requireBinSleep(t)
	installFakeSSH(t, `#!/bin/sh
exec /bin/sleep 60
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
	if elapsed < 10*time.Millisecond {
		t.Fatalf("Wait returned suspiciously fast (%s); fake ssh may have self-exited before ctx cancel fired", elapsed)
	}
}

func TestConnectCtxCancelKillsProcessIgnoringSIGTERM(t *testing.T) {
	if testing.Short() {
		t.Skip("slow: exercises WaitDelay backstop (SIGTERM ignored → SIGKILL)")
	}

	requireBinSleep(t)
	installFakeSSH(t, `#!/bin/sh
trap '' TERM
exec /bin/sleep 60
`)

	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()

	sess, err := tunnelssh.Connect(ctx, "example.com", "true")
	if err != nil {
		t.Fatalf("Connect: %v", err)
	}
	defer sess.Close()

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
		t.Fatal("expected non-nil Wait error after ctx cancel with trapped SIGTERM")
	}
	// WaitDelay is 5s in the implementation. Allow generous slack for CI.
	if elapsed > 10*time.Second {
		t.Fatalf("expected Wait to return within ~WaitDelay after ctx cancel, took %s", elapsed)
	}
	// WaitDelay is 5s. With SIGTERM trapped, the process must survive until
	// SIGKILL fires after WaitDelay. Assert elapsed is at least 3s to prove
	// the backstop is load-bearing — if WaitDelay were removed, Wait would
	// hang past the ctx deadline until the full sleep completes.
	if elapsed < 3*time.Second {
		t.Fatalf("expected elapsed >= 3s (WaitDelay backstop), got %s", elapsed)
	}
}

func TestSessionCloseTerminatesRunningSubprocess(t *testing.T) {
	requireBinSleep(t)
	installFakeSSH(t, `#!/bin/sh
exec /bin/sleep 60
`)

	sess, err := tunnelssh.Connect(context.Background(), "example.com", "true")
	if err != nil {
		t.Fatalf("Connect: %v", err)
	}

	go func() {
		_, _ = io.Copy(io.Discard, sess.Stdout)
	}()
	go func() {
		_, _ = io.Copy(io.Discard, sess.Stderr)
	}()

	start := time.Now()
	_ = sess.Close()
	elapsed := time.Since(start)
	if elapsed > 1*time.Second {
		t.Fatalf("Close did not terminate subprocess promptly, took %s", elapsed)
	}
	// Lower bound helps detect if the subprocess self-exited due to PATH issues
	// (which would exit instantly), vs being properly killed by Close (which
	// takes at least the time to deliver and handle the signal).
	if elapsed < 100*time.Microsecond {
		t.Fatalf("Close returned suspiciously fast (%s); fake ssh may have self-exited before Close fired", elapsed)
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
