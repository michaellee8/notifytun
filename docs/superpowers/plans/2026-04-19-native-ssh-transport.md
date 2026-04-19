# Native-ssh Transport Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Replace the in-process `golang.org/x/crypto/ssh` client in `internal/ssh` with a thin wrapper around the system `ssh` binary, so identity selection and `ssh_config`-aware behavior match what the user gets at the shell.

**Architecture:** `internal/ssh.Connect(ctx, target, remoteCmd)` spawns `ssh -T -o BatchMode=yes -o ConnectTimeout=10 -o ServerAliveInterval=15 -o ServerAliveCountMax=3 -- <target> <remoteCmd>` via `os/exec`. `Session` exposes stdout/stderr pipes and `Wait`/`Close`. `Close` cancels a derived context that triggers a `SIGTERM` Cancel hook with `WaitDelay=5s` backstop. `Backoff` stays unchanged. Caller (`internal/cli/local.go`) calls `Connect` directly; `ResolveTarget`, `--ssh-key`, `local.ssh-key`, `ConnConfig`, and all identity/`known_hosts`/`ssh_config` helpers are deleted.

**Tech Stack:** Go 1.25.5, stdlib `os/exec`, `context`, `syscall`. Drop `github.com/kevinburke/ssh_config` and `golang.org/x/crypto/*`.

**Spec:** [docs/superpowers/specs/2026-04-19-native-ssh-transport-design.md](../specs/2026-04-19-native-ssh-transport-design.md)

---

## File plan

Created:
- `internal/ssh/backoff.go` — `Backoff` struct extracted from current `ssh.go` (no behavior change).
- `internal/ssh/backoff_test.go` — moves `TestBackoffSequence` / `TestBackoffReset` out of `ssh_test.go`.

Rewritten:
- `internal/ssh/ssh.go` — replaced wholesale with subprocess-based `Connect` / `Session`.
- `internal/ssh/ssh_test.go` — replaced wholesale with fake-`ssh`-via-PATH tests.

Modified:
- `internal/cli/local.go` — call-site update, drop `sshKey` field + flag + loader + `runLocal` param, rename `remote stderr:` log prefix to `ssh stderr:`.
- `internal/cli/local_test.go` — drop `sshKey` field references in `TestLocalOptionsApplyConfigFallbacks` and `TestLocalOptionsPreserveExplicitFlags`.
- `config.example.toml` — delete `ssh-key` commented line + surrounding comment block.
- `README.md` — remove `--ssh-key` from flag table and config key list; drop `local.ssh-key` from config keys.
- `go.mod`, `go.sum` — `go mod tidy` removes `kevinburke/ssh_config` and `golang.org/x/crypto`.
- `docs/superpowers/specs/2026-04-14-notifytun-design.md` — add one-line note at top marking the SSH transport section as superseded by the 2026-04-19 spec.

---

## Task 1: Extract `Backoff` into its own file

**Rationale:** `Backoff` is the only part of the current `ssh` package that survives. Splitting it out first lets Task 2 delete `ssh.go`/`ssh_test.go` wholesale without conflating refactor with rewrite.

**Files:**
- Create: `internal/ssh/backoff.go`
- Create: `internal/ssh/backoff_test.go`
- Modify: `internal/ssh/ssh.go` (remove `Backoff` struct + `NewBackoff`/`Next`/`Reset`, remove `initialBackoff`/`maxBackoff` constants)
- Modify: `internal/ssh/ssh_test.go` (remove `TestBackoffSequence` / `TestBackoffReset`)

- [ ] **Step 1: Create `internal/ssh/backoff.go`**

```go
package ssh

import "time"

const (
	initialBackoff = 1 * time.Second
	maxBackoff     = 30 * time.Second
)

// Backoff implements exponential reconnect backoff capped at 30 seconds.
type Backoff struct {
	current time.Duration
}

// NewBackoff creates a backoff starting at 1 second.
func NewBackoff() *Backoff {
	return &Backoff{current: initialBackoff}
}

// Next returns the current backoff and advances the sequence.
func (b *Backoff) Next() time.Duration {
	delay := b.current
	b.current *= 2
	if b.current > maxBackoff {
		b.current = maxBackoff
	}
	return delay
}

// Reset returns the backoff to its initial value.
func (b *Backoff) Reset() {
	b.current = initialBackoff
}
```

- [ ] **Step 2: Create `internal/ssh/backoff_test.go`**

```go
package ssh_test

import (
	"testing"

	tunnelssh "github.com/michaellee8/notifytun/internal/ssh"
)

func TestBackoffSequence(t *testing.T) {
	b := tunnelssh.NewBackoff()
	expected := []int{1, 2, 4, 8, 16, 30, 30, 30}

	for i, want := range expected {
		if got := int(b.Next().Seconds()); got != want {
			t.Fatalf("attempt %d: expected %ds, got %ds", i+1, want, got)
		}
	}
}

func TestBackoffReset(t *testing.T) {
	b := tunnelssh.NewBackoff()
	_ = b.Next()
	_ = b.Next()
	_ = b.Next()
	b.Reset()

	if got := int(b.Next().Seconds()); got != 1 {
		t.Fatalf("expected reset backoff to 1s, got %ds", got)
	}
}
```

- [ ] **Step 3: Remove duplicated `Backoff` code from `internal/ssh/ssh.go`**

Delete these blocks from `internal/ssh/ssh.go`:

- The `initialBackoff` and `maxBackoff` entries in the `const` block at the top (`ssh.go:24-25`). Keep `defaultSSHPort` and `connectTimeout` for now.
- The `// Backoff implements exponential reconnect backoff capped at 30 seconds.` struct + `NewBackoff` + `Next` + `Reset` methods (`ssh.go:208-237`).

- [ ] **Step 4: Remove duplicated backoff tests from `internal/ssh/ssh_test.go`**

Delete `TestBackoffSequence` and `TestBackoffReset` (lines 386-407 of current `ssh_test.go`).

- [ ] **Step 5: Run full test suite to verify the refactor**

Run: `go test ./...`
Expected: PASS (all existing tests, including the moved `TestBackoffSequence` / `TestBackoffReset`).

- [ ] **Step 6: Commit**

```bash
git add internal/ssh/backoff.go internal/ssh/backoff_test.go internal/ssh/ssh.go internal/ssh/ssh_test.go
git commit -m "$(cat <<'EOF'
refactor(ssh): extract Backoff into its own file

Co-Authored-By: Claude Opus 4.7 (1M context) <noreply@anthropic.com>
EOF
)"
```

---

## Task 2: Replace SSH transport with subprocess wrapper

**Rationale:** This is the atomic swap. The `Connect` signature changes (`ConnConfig` → `target string`), so the new impl, the new tests, and the `internal/cli/local.go` caller must land together to keep `go build ./...` green. TDD within the task: tests first, then impl, then caller updates.

**Files:**
- Rewrite: `internal/ssh/ssh.go`
- Rewrite: `internal/ssh/ssh_test.go`
- Modify: `internal/cli/local.go`
- Modify: `internal/cli/local_test.go`

- [ ] **Step 1: Rewrite `internal/ssh/ssh_test.go` with subprocess tests**

Replace the entire contents of `internal/ssh/ssh_test.go` with:

```go
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
```

- [ ] **Step 2: Run the new tests — expect build failure**

Run: `go test ./internal/ssh/...`
Expected: FAIL with compile errors — `ssh.Connect` signature doesn't match, `ssh.Session` fields not as tests expect.

- [ ] **Step 3: Rewrite `internal/ssh/ssh.go` with the subprocess impl**

Replace the entire contents of `internal/ssh/ssh.go` with:

```go
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
```

- [ ] **Step 4: Run the ssh package tests — expect pass**

Run: `go test ./internal/ssh/... -count=1`
Expected: PASS on `TestConnectHappyPath`, `TestConnectStreamsStderr`, `TestConnectNonZeroExit`, `TestConnectCtxCancelKillsProcess`, `TestConnectSSHNotFound`, `TestBackoffSequence`, `TestBackoffReset`.

- [ ] **Step 5: Run the whole module — expect failure in `internal/cli`**

Run: `go build ./...`
Expected: FAIL. `internal/cli/local.go` still calls `tunnelssh.ResolveTarget` (now gone) and the old `Connect(ctx, cfg, cmd)` signature.

- [ ] **Step 6: Update `internal/cli/local.go` — drop `sshKey` plumbing**

Apply these edits to `internal/cli/local.go`:

**6a.** Remove the `sshKey` and `sshKeySet` fields from `localOptions` (lines ~40 and ~47):

Old `localOptions` block:
```go
type localOptions struct {
	target     string
	remoteBin  string
	backend    string
	notifyCmd  string
	sshKey     string
	configFile string

	targetSet    bool
	remoteBinSet bool
	backendSet   bool
	notifyCmdSet bool
	sshKeySet    bool
}
```

New:
```go
type localOptions struct {
	target     string
	remoteBin  string
	backend    string
	notifyCmd  string
	configFile string

	targetSet    bool
	remoteBinSet bool
	backendSet   bool
	notifyCmdSet bool
}
```

**6b.** In `RunE` (lines ~83-95), remove the `sshKeySet` assignment and the `opts.sshKey` argument to `runLocal`:

Old:
```go
		RunE: func(cmd *cobra.Command, args []string) error {
			opts.targetSet = cmd.Flags().Changed("target")
			opts.remoteBinSet = cmd.Flags().Changed("remote-bin")
			opts.backendSet = cmd.Flags().Changed("backend")
			opts.notifyCmdSet = cmd.Flags().Changed("notify-cmd")
			opts.sshKeySet = cmd.Flags().Changed("ssh-key")

			if err := opts.loadAndApplyConfig(); err != nil {
				return err
			}
			if opts.target == "" {
				return fmt.Errorf("--target is required (or set local.target in config)")
			}
			return runLocal(cmd.Context(), opts.target, opts.remoteBin, opts.backend, opts.notifyCmd, opts.sshKey)
		},
```

New:
```go
		RunE: func(cmd *cobra.Command, args []string) error {
			opts.targetSet = cmd.Flags().Changed("target")
			opts.remoteBinSet = cmd.Flags().Changed("remote-bin")
			opts.backendSet = cmd.Flags().Changed("backend")
			opts.notifyCmdSet = cmd.Flags().Changed("notify-cmd")

			if err := opts.loadAndApplyConfig(); err != nil {
				return err
			}
			if opts.target == "" {
				return fmt.Errorf("--target is required (or set local.target in config)")
			}
			return runLocal(cmd.Context(), opts.target, opts.remoteBin, opts.backend, opts.notifyCmd)
		},
```

**6c.** Remove the `--ssh-key` flag registration (line ~103):

Delete the line:
```go
	cmd.Flags().StringVar(&opts.sshKey, "ssh-key", "", "Path to SSH private key")
```

**6d.** In `loadAndApplyConfig` (around line ~144-146), remove the `ssh-key` branch:

Delete:
```go
	if !o.sshKeySet {
		o.sshKey = cfg.GetString("local.ssh-key")
	}
```

**6e.** Change the `runLocal` signature and drop the `sshKey` parameter (line 167 and its `ResolveTarget`/`Connect` call at lines 191-192):

Old:
```go
func runLocal(ctx context.Context, target, remoteBin, backend, notifyCmd, sshKey string) error {
```
...
```go
		log.Printf("connecting to %s...", target)
		connCfg := tunnelssh.ResolveTarget(target, sshKey, "")
		sess, err := tunnelssh.Connect(ctx, connCfg, remoteCommand)
```

New:
```go
func runLocal(ctx context.Context, target, remoteBin, backend, notifyCmd string) error {
```
...
```go
		log.Printf("connecting to %s...", target)
		sess, err := tunnelssh.Connect(ctx, target, remoteCommand)
```

**6f.** Rename the log prefix in `logRemoteStderr` (line ~266):

Old:
```go
		log.Printf("remote stderr: %s", scanner.Text())
```

New:
```go
		log.Printf("ssh stderr: %s", scanner.Text())
```

And the warning a few lines below:

Old:
```go
		log.Printf("warning: remote stderr read failed: %v", err)
```

New:
```go
		log.Printf("warning: ssh stderr read failed: %v", err)
```

- [ ] **Step 7: Update `internal/cli/local_test.go` — drop `sshKey` assertions**

Two tests touch `sshKey`. Apply:

**7a.** In `TestLocalOptionsApplyConfigFallbacks` (starting at line 134):

- Remove the `ssh-key = "/tmp/test-key"` line from the inline TOML (line ~142).
- Remove the assertion block:

```go
	if opts.sshKey != "/tmp/test-key" {
		t.Fatalf("expected ssh key from config, got %q", opts.sshKey)
	}
```

**7b.** In `TestLocalOptionsPreserveExplicitFlags` (starting at line 175):

- Remove `ssh-key = "/tmp/test-key"` from the inline TOML.
- Remove `sshKey: "/tmp/flag-key",` and `sshKeySet: true,` from the `localOptions` literal (lines ~194 and ~200).
- Remove the assertion block:

```go
	if opts.sshKey != "/tmp/flag-key" {
		t.Fatalf("expected explicit ssh key to win, got %q", opts.sshKey)
	}
```

- [ ] **Step 8: Run the full test suite**

Run: `go test ./... -count=1`
Expected: PASS across all packages.

- [ ] **Step 9: Run the build to confirm no leftover references**

Run: `go build ./...`
Expected: PASS.

- [ ] **Step 10: Commit**

```bash
git add internal/ssh/ssh.go internal/ssh/ssh_test.go internal/cli/local.go internal/cli/local_test.go
git commit -m "$(cat <<'EOF'
feat(ssh): switch transport to system ssh binary

Delegate identity/host-key/jump-host behavior to the user's ssh config.
Drops --ssh-key and local.ssh-key; identity selection now uses
~/.ssh/config IdentityFile and the SSH agent.

Co-Authored-By: Claude Opus 4.7 (1M context) <noreply@anthropic.com>
EOF
)"
```

---

## Task 3: Update user-facing docs and config example

**Files:**
- Modify: `config.example.toml`
- Modify: `README.md`
- Modify: `docs/superpowers/specs/2026-04-14-notifytun-design.md`

- [ ] **Step 1: Remove `ssh-key` from `config.example.toml`**

Delete lines 18-20 (the three-line block):

```toml
# Path to SSH private key (optional)
# If unset, uses SSH agent or keys from ~/.ssh/config
# ssh-key = "~/.ssh/id_ed25519"
```

The resulting file has `backend = "auto"` at line ~16 and the `# Custom notification command...` block continues directly afterward.

- [ ] **Step 2: Remove `--ssh-key` from `README.md` flag table**

In `README.md`, delete this row from the `notifytun local` flag table (line ~145 area):

```
- `--ssh-key`: optional SSH key path
```

- [ ] **Step 3: Remove `local.ssh-key` from `README.md` config-keys list**

In `README.md`, delete this bullet from the "Supported keys" list (line ~119):

```
- `local.ssh-key`: optional SSH private key override
```

- [ ] **Step 4: Remove `ssh-key` from the `README.md` config example snippet**

In the config example block around line 105-112, delete this line:

```
# ssh-key = "~/.ssh/id_ed25519"
```

- [ ] **Step 5: Mark the 2026-04-14 spec as superseded for SSH transport**

Prepend to `docs/superpowers/specs/2026-04-14-notifytun-design.md`, just after the `**Go version:** 1.23+` line (line 5):

```markdown

> **Note (2026-04-19):** The SSH transport described here (section 9, "SSH connection") has been superseded by [2026-04-19-native-ssh-transport-design.md](2026-04-19-native-ssh-transport-design.md). The rest of this document still applies.
```

- [ ] **Step 6: Verify the README still reads cleanly**

Run: `sed -n '95,150p' README.md`
Expected: no stray references to `--ssh-key`, `local.ssh-key`, or `ssh-key = `. The flag table and config-keys list should be coherent with the lines removed.

- [ ] **Step 7: Commit**

```bash
git add config.example.toml README.md docs/superpowers/specs/2026-04-14-notifytun-design.md
git commit -m "$(cat <<'EOF'
docs: drop --ssh-key references after native ssh switch

Co-Authored-By: Claude Opus 4.7 (1M context) <noreply@anthropic.com>
EOF
)"
```

---

## Task 4: Drop unused module dependencies

**Rationale:** `github.com/kevinburke/ssh_config` and `golang.org/x/crypto` are no longer imported by any code. `go mod tidy` removes them from `go.mod` / `go.sum`.

**Files:**
- Modify: `go.mod`, `go.sum`

- [ ] **Step 1: Confirm no remaining imports of the deps**

Run: `grep -rn 'golang.org/x/crypto\|kevinburke/ssh_config' --include='*.go' .`
Expected: no matches.

- [ ] **Step 2: Run `go mod tidy`**

Run: `go mod tidy`
Expected: success. `go.mod` no longer has `github.com/kevinburke/ssh_config` in the `require` block. `golang.org/x/crypto` is removed from `require` (may remain transitively if pulled in by another dep; do not force-remove).

- [ ] **Step 3: Verify `go.mod` reflects the change**

Run: `grep -E 'kevinburke/ssh_config|golang.org/x/crypto' go.mod`
Expected: no matches (or `golang.org/x/crypto` only as `// indirect` if something transitively requires it — that's acceptable).

- [ ] **Step 4: Run the full test suite**

Run: `go test ./... -count=1`
Expected: PASS.

- [ ] **Step 5: Run `go vet`**

Run: `go vet ./...`
Expected: no warnings.

- [ ] **Step 6: Commit**

```bash
git add go.mod go.sum
git commit -m "$(cat <<'EOF'
chore: go mod tidy after dropping gossh deps

Co-Authored-By: Claude Opus 4.7 (1M context) <noreply@anthropic.com>
EOF
)"
```

---

## Acceptance

The implementation is complete when all of the following are true:

- `go build ./...` and `go test ./... -count=1` pass.
- `grep -rn 'golang.org/x/crypto\|kevinburke/ssh_config' --include='*.go' .` finds nothing.
- `grep -rn 'ssh-key\|sshKey\|--ssh-key' --include='*.go' --include='*.toml' --include='*.md' .` finds nothing outside of `docs/superpowers/specs/2026-04-14-notifytun-design.md` (historical) and `docs/rough-spec.md` (historical rough notes — no change planned).
- The `internal/ssh` package exports only `Session`, `Connect`, `Backoff`, `NewBackoff`.
- `notifytun local --target <alias>` connects when the user has `IdentityFile` set in `~/.ssh/config` (including non-default key names like `id_ecdsa`) without passing any flag related to keys.
- `ctrl-c` on `notifytun local` terminates the `ssh` subprocess within seconds (verified manually — not automatable here).
