# Native-ssh transport design

**Date:** 2026-04-19
**Scope:** Replace the `internal/ssh` package's `golang.org/x/crypto/ssh`-based client with a thin subprocess wrapper around the system `ssh` binary.

## 1. Motivation

`notifytun local` currently opens its SSH session through `x/crypto/ssh` (gossh) plus `kevinburke/ssh_config`. The in-process implementation re-implements pieces of SSH client behavior — identity file discovery, agent integration, `known_hosts` loading, `ssh_config` parsing — and does so less completely than the system `ssh` binary. The immediate trigger is identity selection: a user's `~/.ssh/id_ecdsa` is not picked up unless explicitly set via `--ssh-key` / `local.ssh-key`, because `ssh.ParsePrivateKey` fails silently on encrypted keys or non-default filenames and the fallback path has no agent-aware prompting.

The broader problem is that duplicating `ssh`'s feature surface (jump hosts, `Include`, `IdentitiesOnly`, `ControlMaster`, certificate auth, FIDO keys, hashed `known_hosts`) is a maintenance treadmill we do not want. `docs/rough-spec.md §8` originally called for using the system `ssh` binary; this spec reverts to that plan.

## 2. Goals

- Identity, host-key, and jump-host behavior matches `ssh <target>` at the shell.
- The `internal/ssh` public API surface shrinks to what `internal/cli/local.go` actually needs.
- No new CLI surface; `--ssh-key` and the `local.ssh-key` config entry are retired.
- Tests do not depend on running an in-process SSH server.

## 3. Non-goals

- No change to the JSONL protocol, the remote `attach` command, the notifier backends, or the reconnect/backoff policy in `internal/cli/local.go`.
- No `--ssh` override flag. If the user needs a specific `ssh` binary they can symlink it into `PATH`.
- No host-key-preflight replacement. `ssh`'s own `StrictHostKeyChecking` error surfaces via stderr.
- No interactive passphrase prompting from `notifytun local`. `BatchMode=yes` is set; users with passphrase-protected keys use an agent.

## 4. Package API

`internal/ssh` keeps its import path and retains `Backoff` unchanged. Connection surface becomes:

```go
package ssh

type Session struct {
    Stdout io.Reader
    Stderr io.Reader
    // unexported: *exec.Cmd, wait sync.Once, waitErr error
}

// Connect starts `ssh` with the configured options and returns once the
// subprocess is started. Cancelling ctx terminates the subprocess.
func Connect(ctx context.Context, target, remoteCmd string) (*Session, error)

func (s *Session) Wait() error
func (s *Session) Close() error

// Backoff is unchanged from the current implementation.
type Backoff struct { /* ... */ }
func NewBackoff() *Backoff
func (b *Backoff) Next() time.Duration
func (b *Backoff) Reset()
```

Removed symbols:

- `ConnConfig`, `ResolveTarget` — `target` is passed verbatim to `ssh`.
- All identity/agent/`known_hosts`/`ssh_config` helpers (`buildAuthMethods`, `loadKnownHosts`, `readSSHConfig`, `expandHomePath`, `loadSignerFromFile`, `loadAgentAuthMethod`, `defaultKeyPaths`, `splitTargetHostPort`, `runSSHOperationWithContext`).

Call-site change in `internal/cli/local.go`:

```go
// before
connCfg := tunnelssh.ResolveTarget(target, sshKey, "")
sess, err := tunnelssh.Connect(ctx, connCfg, remoteCommand)

// after
sess, err := tunnelssh.Connect(ctx, target, remoteCommand)
```

`sshKey` and all the plumbing that carried it from flag → options struct → config loader → `Connect` is deleted.

## 5. Command construction

`Connect` builds this argv:

```
ssh
  -T
  -o BatchMode=yes
  -o ConnectTimeout=10
  -o ServerAliveInterval=15
  -o ServerAliveCountMax=3
  --
  <target>
  <remoteCmd>
```

Rules:

- `ssh` is resolved via `PATH`. No override.
- `--` separates options from positional arguments so a target starting with `-` cannot be reinterpreted as a flag.
- `remoteCmd` is a single argv element. `ssh` concatenates trailing args with spaces and runs them through the remote login shell — this matches how `internal/cli/local.go:249-257` currently builds `sh -lc '<script>'`, which is preserved verbatim as the `remoteCmd` string.
- `cmd.Stdin` stays nil (subprocess reads `/dev/null`). Combined with `BatchMode=yes`, `ssh` cannot block on a prompt.

## 6. Process lifecycle

```go
cmd := exec.CommandContext(ctx, "ssh", args...)
cmd.Cancel = func() error { return cmd.Process.Signal(syscall.SIGTERM) }
cmd.WaitDelay = 5 * time.Second
```

This gives: `ctx.Done` → `SIGTERM` → up to 5s grace → `SIGKILL`. A well-behaved `ssh` cleans up; a wedged one is killed.

`Stdout` and `Stderr` are wired with `cmd.StdoutPipe()` / `cmd.StderrPipe()`. `cmd.Start` is called inside `Connect`; any error is wrapped as `start ssh: %w`.

`Session.Wait` wraps `cmd.Wait` in a `sync.Once` and caches the result, making it safe to call after `Close` or concurrently from multiple goroutines. This matters because `local.go` has `go logRemoteStderr(sess.Stderr)` racing with the main stdout loop; both can observe `Close`.

`Session.Close` triggers the `Cancel` path (via an internal cancel func captured at construction) and returns the combined `Wait` + signal error.

## 7. Error handling

Errors flow through the same call paths as today. `internal/cli/local.go` is not modified except for the `Connect` call-site change and a log prefix tweak (see §9).

**Start failures** (from `Connect`):
- `ssh` not on `PATH`: wrapped `exec` error; caller logs "connection failed" and backs off. Retries are futile but cost little and match current behavior when `known_hosts` was missing.
- Pipe setup failures: OS-level, rare.

**Runtime failures**:
- Remote command exits non-zero or `ssh` itself exits non-zero (auth failure, host-key mismatch, network drop): stdout reaches EOF, `processStream` returns `"stream EOF"`, the reconnect loop at `local.go:222-234` logs and backs off. Human-readable cause is in stderr, already streamed through `logRemoteStderr`.
- `ssh` auth permanently broken: backoff keeps trying forever — same as today.

**Cancellation**: `ctx.Done` triggers the lifecycle path in §6. `Wait` returns a ctx or signal error. `TestConnectCancelsDuringHandshake`'s timing invariant (return before the stalled server releases) is preserved because `exec.CommandContext` delivers the signal synchronously.

## 8. SSH keepalive vs. JSONL heartbeat

Two complementary liveness mechanisms:

- `ServerAliveInterval=15` / `ServerAliveCountMax=3` — ssh-level TCP keepalive. Detects dead sockets where the remote kernel is gone.
- 45s JSONL heartbeat timeout in `processStreamWithTimeout` (`local.go:277-352`). Detects a live TCP socket with a stuck-but-not-dead `attach` process on the other side.

Both stay. The JSONL heartbeat is protocol-level and independent of transport.

## 9. Observability

One small change: rename the `remote stderr:` prefix at `local.go:266` to `ssh stderr:`. After the switch, both ssh's own diagnostics ("Host key verification failed", "Permission denied (publickey)") and the remote-side stderr flow through the same stream; the new prefix is accurate for both.

The "connecting to %s..." log at `local.go:190` is unchanged. We lose the ability to print a resolved host/IP (since we no longer parse `ssh_config`). Users who want to inspect resolution can run `ssh -G <alias>` at the shell.

## 10. Dependencies

Removed from `go.mod`:
- `github.com/kevinburke/ssh_config`
- `golang.org/x/crypto` (if no other code in the repo imports it; to be confirmed when implementing)

Added: none. `os/exec`, `context`, `syscall` are stdlib.

## 11. Testing

Rewrite `internal/ssh/ssh_test.go`. The 636 lines of in-process gossh server scaffolding are replaced by subprocess tests that stub `ssh` via `PATH` injection.

Test hook: each test writes a small shell script named `ssh` into `t.TempDir()`, prepends that dir to `PATH` via `t.Setenv("PATH", ...)`. Production code stays flag-free.

Cases:

1. **Happy path** — fake `ssh` echoes its argv to a scratch file, emits one JSONL line on stdout, exits 0. Assert `Connect` returns nil, `Stdout` yields the line, `Wait` returns nil, and the argv file contains each of `-T`, `BatchMode=yes`, `ConnectTimeout=10`, `ServerAliveInterval=15`, `ServerAliveCountMax=3`, the target, and the remoteCmd. Positionally assert that the target precedes the remoteCmd and both appear after the last `-o`. Do not assert inter-`-o` ordering.
2. **Stderr streaming** — fake `ssh` writes to stderr and exits 0. Assert `Stderr` yields the bytes.
3. **Remote exit non-zero** — fake `ssh` exits 5. Assert `Wait` returns an `*exec.ExitError` with `ExitCode() == 5`.
4. **Ctx cancel kills the process** — fake `ssh` runs `sleep 60`. Cancel ctx after 50ms. Assert `Wait` returns within 1s and the error is a ctx error or signal error. Replaces today's `TestConnectCancelsDuringHandshake` and `TestConnectCancelsDuringRemoteCommandStart` — the subprocess model collapses both into one wait-on-subprocess case.
5. **`ssh` not on PATH** — `t.Setenv("PATH", "")`. Assert `Connect` returns an error whose message mentions `ssh`.
6. **`TestBackoffSequence` / `TestBackoffReset`** — unchanged from current `ssh_test.go`.

Dropped tests and why:

- `TestResolveTargetSimple`, `TestResolveTargetWithPort`, `TestResolveTargetFromSSHConfig`, `TestResolveTargetKeyOverride` — `ResolveTarget` is gone.
- `TestConnectFailsWithoutAuthMethods`, `TestConnectFailsWithoutKnownHosts` — these failure modes are `ssh`'s responsibility now.
- `TestConnectUsesSSHAgentAuth`, `TestConnectFallsBackToDefaultKeyWhenConfiguredIdentityFileIsMissing`, `TestConnectFailsWhenExplicitOverrideKeyIsMissing` — tested code that is being deleted.

`integration_test.go` at the repo root exercises the `emit`/`attach` pipeline on one box and does not touch the SSH path; expected to need no change.

## 12. Documentation changes

- `README.md:111` and `README.md:145`: remove `--ssh-key` from flag tables and the config example. Add a one-line note that identity selection uses `~/.ssh/config` and the SSH agent.
- `README.md:37`: "An SSH trust relationship already established in `~/.ssh/known_hosts`" stays — still required.
- `config.example.toml`: delete the `ssh-key` line.
- `docs/rough-spec.md`: already consistent with the new design; no change.
- `docs/superpowers/specs/2026-04-14-notifytun-design.md`: add a one-line note at the top marking it as superseded by this spec for the SSH transport section only. (Everything else in that spec still applies.)

## 13. Migration

One-shot replacement. No compatibility shim — `--ssh-key` is removed outright. Users with `local.ssh-key` in `config.toml` see their flag ignored after upgrade; they should either delete it or move the identity to `IdentityFile` in `~/.ssh/config`. This is documented in the README diff and called out in the commit/PR description.

## 14. Out-of-scope follow-ups

- `--ssh` override flag: add only if a real user needs it.
- Printing resolved host/IP via `ssh -G`: add only if lost diagnostic actually bites.
- ssh-origin vs. remote-origin stderr tagging: not worth the parsing effort.

## 15. Acceptance

This change is acceptable when:

- `go build ./...` and `go test ./...` pass on Linux and macOS.
- A user whose `~/.ssh/config` contains `IdentityFile ~/.ssh/id_ecdsa` for the target (or who relies on agent) can run `notifytun local --target <alias>` with no flag changes and have it connect.
- `ctrl-c` on `notifytun local` terminates the `ssh` subprocess within seconds, not after a 10s `ConnectTimeout`.
- Removing `--ssh-key` from an existing `config.toml` does not change behavior beyond what the README diff describes.
