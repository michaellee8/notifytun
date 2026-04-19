# notifytun Design Spec

**Date:** 2026-04-14
**Module:** `github.com/michaellee8/notifytun`
**Go version:** 1.23+

> **Note (2026-04-19):** The SSH transport described here (section 9, "SSH connection") has been superseded by [2026-04-19-native-ssh-transport-design.md](2026-04-19-native-ssh-transport-design.md). The rest of this document still applies.

## 1. Overview

notifytun is a single Go binary that tunnels notifications from a remote Linux VM to a local machine over SSH. No background daemons required — the `local` subcommand manages the SSH lifecycle directly using `x/crypto/ssh`.

Primary use case: receive desktop notifications when AI coding tools (Claude Code, Codex CLI, Gemini CLI, OpenCode) complete tasks on a remote VM.

## 2. Components

| Component | Runs on | Purpose |
|---|---|---|
| `notifytun local` | Local machine (macOS/Linux) | Connects to remote via SSH, receives notification stream, delivers to desktop |
| `notifytun attach` | Remote VM | Invoked over SSH. Streams undelivered notifications from SQLite as JSONL |
| `notifytun emit` | Remote VM | Called by tool hooks. Writes to SQLite + pokes Unix socket |
| `notifytun test-notify` | Local machine | Fires a test notification to verify backend works |
| `notifytun remote-setup` | Remote VM | Detects installed AI tools, configures their hooks to call `emit` |

### Data Flow

```
[Claude/Codex/Gemini/OpenCode hook]
        |
        v
   notifytun emit --> SQLite (always) + Unix socket (best-effort)
                                              |
                                              v
                                      notifytun attach
                                              |
                                         JSONL stdout
                                              |
                                      --- SSH tunnel ---
                                              |
                                              v
                                      notifytun local
                                              |
                                              v
                                    Desktop notification
```

## 3. CLI Contract

### `notifytun local`

The main long-running process on your machine.

```
notifytun local [flags]
```

| Flag | Default | Description |
|---|---|---|
| `--target` | (required, or from config) | SSH target, e.g. `user@host` or SSH config `Host` alias |
| `--remote-bin` | `notifytun` | Path to binary on remote. Wrapped in `sh -lc` |
| `--backend` | `auto` | Notifier backend: `auto`, `macos`, `linux`, `generic` |
| `--notify-cmd` | — | Custom command for `generic` backend. Receives JSON on stdin |
| `--ssh-key` | — | Path to SSH private key. If unset, uses SSH agent or keys from `~/.ssh/config` |
| `--config` | `~/.notifytun/config.toml` | Config file path |

Reconnect: on SSH drop, exponential backoff 1s -> 2s -> 4s -> ... capped at 30s. Resets after a connection lasts more than 60 seconds.

### `notifytun attach`

Invoked by `local` over SSH. Not run manually.

```
notifytun attach [flags]
```

| Flag | Default | Description |
|---|---|---|
| `--db` | `~/.notifytun/notifytun.db` | SQLite database path |
| `--socket` | `~/.notifytun/notifytun.sock` | Unix socket path for `emit` wakeup |

On startup: query SQLite for undelivered rows, stream as JSONL, then listen on socket for new ones. Mark rows delivered after sending.

### `notifytun emit`

Called by tool hooks.

```
notifytun emit [flags] [codex-notify-json]
```

| Flag | Default | Description |
|---|---|---|
| `--title` | derived from Codex payload when present, otherwise required | Notification title |
| `--body` | `""` | Notification body |
| `--tool` | `""` | Source tool name (`claude-code`, `codex`, `gemini`, `opencode`) |
| `--db` | `~/.notifytun/notifytun.db` | SQLite path |
| `--socket` | `~/.notifytun/notifytun.sock` | Socket path |

Normal hooks pass `--title`/`--body` explicitly. For Codex CLI integration, `notifytun emit` also accepts one trailing JSON argument in Codex `notify` format. When that payload is present and `--title` is unset, `emit` derives `title = "Task complete"` and uses `last-assistant-message` as the body, falling back to joined `input-messages` if needed.

Writes row to SQLite (always), then tries to send a wakeup byte to the socket (best-effort).

### `notifytun test-notify`

Local-only test command.

```
notifytun test-notify [--backend auto|macos|linux|generic] [--notify-cmd CMD]
```

Fires a sample notification to verify the backend works.

### `notifytun remote-setup`

Run on the remote VM.

```
notifytun remote-setup [--dry-run]
```

Detects which AI tools are installed, shows what hook config it will write, and applies (or previews with `--dry-run`). Supports Claude Code, Codex CLI, Gemini CLI, and OpenCode.

## 4. Data Model

### SQLite Schema (remote side)

Located at `~/.notifytun/notifytun.db`.

```sql
CREATE TABLE notifications (
    id         INTEGER PRIMARY KEY AUTOINCREMENT,
    title      TEXT    NOT NULL,
    body       TEXT    NOT NULL DEFAULT '',
    tool       TEXT    NOT NULL DEFAULT '',
    created_at TEXT    NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now')),
    delivered  INTEGER NOT NULL DEFAULT 0
);

CREATE INDEX idx_undelivered ON notifications (delivered, id)
    WHERE delivered = 0;
```

`emit` inserts rows with `delivered = 0`. `attach` queries `WHERE delivered = 0 ORDER BY id`, streams them, then sets `delivered = 1`. The partial index keeps the undelivered query fast.

### Concurrent Access

```sql
PRAGMA journal_mode = WAL;
PRAGMA busy_timeout = 5000;
```

- WAL mode: readers never block writers, writers never block readers. Multiple simultaneous `emit` writes serialize automatically.
- busy_timeout: if a write lock is held, retry for up to 5 seconds before failing. More than enough for microsecond-scale INSERTs.
- Short transactions: `emit` opens DB, INSERTs, closes. `attach` uses short read transactions: SELECT undelivered, UPDATE delivered, done.
- Database initialization: whichever process opens the DB first creates the table and sets PRAGMAs. Every subsequent open sets WAL + busy_timeout. Idempotent.

### Config File

Located at `~/.notifytun/config.toml`. Optional — everything works with flags alone. CLI flags override config values.

```toml
[local]
target = "user@myvm"
remote-bin = "notifytun"
backend = "auto"
ssh-key = "~/.ssh/id_ed25519"

# Optional: custom notification command for generic backend
# notify-cmd = "/usr/local/bin/my-notifier"
```

### Unix Socket

Located at `~/.notifytun/notifytun.sock`. A Unix datagram socket created by `attach` on startup, removed on exit. `emit` sends a single wakeup byte (`0x01`) to signal "new row available." If the socket doesn't exist or send fails, `emit` ignores it silently.

### No Local State

`local` is stateless. It connects, receives, notifies. The remote SQLite is the single source of truth.

## 5. JSONL Protocol

The wire protocol between `attach` (remote) and `local` is newline-delimited JSON over SSH stdout. One JSON object per line, remote-to-local only.

### Message Types

```jsonl
{"type":"notif","id":1,"title":"Task complete","body":"Finished refactoring auth module","tool":"claude-code","created_at":"2026-04-14T10:30:00.000Z"}
{"type":"notif","id":5,"title":"Build passed","body":"","tool":"codex","created_at":"2026-04-14T10:31:02.000Z","backlog":true}
{"type":"notif","id":0,"title":"notifytun","body":"12 notifications delivered while disconnected","tool":"","created_at":"...","summary":true}
{"type":"heartbeat","ts":"2026-04-14T10:31:30.000Z"}
```

| Type | Purpose | Fields |
|---|---|---|
| `notif` | A notification to deliver | `id`, `title`, `body`, `tool`, `created_at`, optional `backlog`, optional `summary` |
| `heartbeat` | Keep-alive for dead connection detection | `ts` |

Optional fields on `notif`:
- `backlog` (bool): set to `true` for replayed notifications during reconnect (suppressed from desktop, see Section 8)
- `summary` (bool): set to `true` for the synthetic summary notification after backlog replay

### Heartbeat

`attach` sends a heartbeat every 15 seconds. `local` expects one within 45 seconds (3x interval). If missed, `local` treats the connection as dead, tears down the SSH session, and begins reconnect backoff.

TCP keepalive alone is unreliable for detecting dead SSH sessions through NAT/firewalls. An application-level heartbeat gives fast, deterministic detection.

### Delivery Flow

1. `attach` starts, queries `WHERE delivered = 0 ORDER BY id`, streams each as a `notif` line
2. Marks each row `delivered = 1` individually after its line is successfully written to stdout (not batched — if stdout breaks mid-stream, unwritten rows remain undelivered)
3. Listens on Unix socket for wakeup, queries again, streams new rows
4. Every 15s, sends a `heartbeat` line

### `local` Processing

1. Reads lines from SSH stdout
2. `notif` -> pass to notifier backend
3. `heartbeat` -> reset the 45s dead-connection timer
4. Malformed JSONL line -> log warning and continue; EOF -> connection dead, begin reconnect

## 6. SSH Transport

### Connection Management

Uses `golang.org/x/crypto/ssh` as the SSH client library, with `github.com/kevinburke/ssh_config` for parsing `~/.ssh/config`.

1. `local` reads `~/.ssh/config` to resolve `--target` into host, port, user, and identity file
2. Authentication priority: explicit `--ssh-key` flag -> keys from SSH config -> SSH agent (`SSH_AUTH_SOCK`)
3. Opens an SSH session, runs `sh -lc "notifytun attach"` as a remote command
4. Grabs the session's stdout pipe as the JSONL reader
5. On connection drop (EOF, heartbeat timeout, SSH channel error) -> close session -> reconnect with backoff

### Reconnect Strategy

```
attempt 1: wait 1s
attempt 2: wait 2s
attempt 3: wait 4s
attempt 4: wait 8s
attempt 5: wait 16s
attempt 6+: wait 30s (cap)
```

Reset to 1s after a successful connection that lasts more than 60 seconds.

### SSH Config Support

Directives supported via `kevinburke/ssh_config`:
- `HostName`, `Port`, `User`, `IdentityFile` — core resolution

`ProxyJump` and `ProxyCommand` are out of scope for v1. Users who need jump-host support must handle that separately until notifytun adds native proxy support.

### Known Hosts

Respect `~/.ssh/known_hosts` for host key verification. Reject unknown hosts by default. Users must have connected via regular `ssh` at least once before using notifytun.

### Graceful Shutdown

`local` catches SIGINT/SIGTERM, closes the SSH session cleanly, exits 0. No orphaned remote `attach` processes — when the SSH channel closes, `attach`'s stdout pipe breaks and it exits.

## 7. Notifier Backends

Three backends, selected by `--backend` flag (default `auto` which detects OS).

### Backend Interface

```go
type Notifier interface {
    Notify(ctx context.Context, n Notification) error
}
```

### macOS Backend

Uses `osascript` to trigger native notifications:

```bash
osascript -e 'display notification "body" with title "title" subtitle "notifytun"'
```

Fire-and-forget via `os/exec`. If `osascript` fails, log warning to stderr, don't crash.

### Linux Backend

Uses `notify-send` (libnotify):

```bash
notify-send -a "notifytun" "title" "body"
```

Requires `notify-send` installed. If binary not found, log error and suggest `--backend generic`.

### Generic Backend

Pipes JSON to a user-supplied command via `--notify-cmd`:

```bash
echo '{"title":"...","body":"...","tool":"..."}' | notify-cmd
```

Enables integration with anything: Slack webhooks, Telegram bots, custom scripts, ntfy.sh.

### Auto-Detection

1. `runtime.GOOS == "darwin"` -> `macos`
2. `runtime.GOOS == "linux"` -> check if `notify-send` exists in PATH -> `linux`, else fall back to `generic` (requires `--notify-cmd` or error)

## 8. Flood Control

When `local` reconnects after downtime, `attach` may replay many queued notifications.

### Coalesce Strategy

1. `attach` queries all undelivered rows on startup
2. If 3 or fewer rows: stream individually as separate `notif` messages
3. If more than 3 rows: stream all individually with `backlog: true` flag, then send a synthetic summary:

```jsonl
{"type":"notif","id":0,"title":"notifytun","body":"12 notifications delivered while disconnected","tool":"","created_at":"...","summary":true}
```

### `local` Side Handling

- `backlog: true` notifications: suppress desktop popup, log only
- `summary: true` notification: deliver as a single desktop notification
- Regular notifications: deliver normally

### Live Rate Limiting

No coalescing during normal operation. Notifications arrive infrequently (task completions). If multiple `emit` calls fire within 1 second, each is delivered separately.

## 9. `remote-setup` Subcommand

Auto-configures AI tool hooks on the remote VM.

### Tool Support

| Tool | Detection | Hook Mechanism |
|---|---|---|
| Claude Code | `claude` or `claude-code` in PATH | `~/.claude/settings.json` `Stop` and `Notification` hooks arrays |
| Codex CLI | `codex` in PATH | `~/.codex/config.toml` `notify` argv array |
| Gemini CLI | `gemini` in PATH | Detection only in v1; preview as detected-but-unsupported, no file changes |
| OpenCode | `opencode` in PATH | Detection only in v1; preview as detected-but-unsupported, no file changes |

Hook entries written by v1:
- Claude Code `Stop` hook -> `notifytun emit --tool claude-code --title "Task complete"`
- Claude Code `Notification` hook -> `notifytun emit --tool claude-code --title "Needs attention"`
- Codex CLI `notify` config -> `notify = ["notifytun", "emit", "--tool", "codex"]` (Codex appends one JSON argument at runtime)

### Behavior

1. Scan PATH for known tool binaries
2. For each detected tool with supported hook setup, check if config is already present
3. Show preview:
   ```
   Detected tools:
     * Claude Code -- will add Stop + Notification hooks to ~/.claude/settings.json
     * Codex CLI -- will set notify in ~/.codex/config.toml

   Apply? [Y/n]
   ```
4. `--dry-run` shows preview without applying
5. Idempotent: checks for existing `notifytun` references before adding
6. Gracefully skip detected tools whose hook mechanism is not implemented in v1

## 10. Package Layout

```
github.com/michaellee8/notifytun/
  cmd/notifytun/
    main.go                   # cobra root command setup
  internal/
    cli/                      # subcommand implementations
      local.go
      attach.go
      emit.go
      testnotify.go
      remotesetup.go
    db/                       # SQLite operations
      db.go                   # Open, Insert, QueryUndelivered, MarkDelivered
    proto/                    # JSONL message types & serialization
      proto.go                # Notification, Heartbeat, Encode, Decode
    ssh/                      # SSH client, config parsing, reconnect
      ssh.go
    notifier/                 # Notification delivery backends
      notifier.go             # Notifier interface
      macos.go
      linux.go
      generic.go
    socket/                   # Unix datagram socket
      socket.go               # Create, SendWakeup, Listen
    setup/                    # remote-setup tool detection & hook writing
      setup.go
  docs/
    superpowers/
      specs/
      plans/
  go.mod
  go.sum
  config.example.toml
```

Each package has one clear responsibility and a narrow public API. `internal/` prevents external imports. `internal/cli/` is thin glue: parse flags, call into the other packages.

## 11. Error Handling & Edge Cases

### `emit` Must Never Fail Loudly

Called from tool hooks. Loud failures could disrupt the AI tool's workflow.

- SQLite write fails -> exit 1, no stderr output. Notification is lost.
- Socket send fails -> ignore silently. Row is in SQLite.
- DB doesn't exist yet -> create it, set PRAGMAs, create table, then insert.

### `attach` Edge Cases

- Socket already exists from stale process -> remove on startup
- Two `attach` processes racing -> second fails to bind socket, exits. First one wins.
- stdout pipe breaks (SSH dies) -> exit cleanly

### `local` Edge Cases

- SSH auth fails -> log error, retry with backoff
- Remote binary not found -> log stderr from remote, suggest `--remote-bin` with absolute path
- Heartbeat timeout -> dead connection, reconnect
- JSONL parse error on a line -> log warning, skip line, don't kill connection
- Notifier backend fails -> log warning, continue receiving
- Config file missing -> fine, use flag defaults. Config file malformed -> error on startup.

### `remote-setup` Edge Cases

- Tool config file doesn't exist yet -> create it with just the hook entry
- Tool config file has unexpected format -> skip that tool, warn user
- No tools detected -> print message, exit 0

### Signal Handling

- `local`: SIGINT/SIGTERM -> close SSH session, exit 0
- `attach`: stdin EOF or stdout broken -> exit 0 (normal SSH teardown)
- `emit`: no signal handling needed, short-lived

## 12. Testing Strategy

### Unit Tests

| Package | What to Test | How |
|---|---|---|
| `internal/db` | Insert, QueryUndelivered, MarkDelivered, concurrent access, WAL mode | In-memory SQLite, parallel goroutines |
| `internal/proto` | Encode/Decode round-trip, malformed JSON, unknown fields | Pure functions, table-driven tests |
| `internal/notifier` | Each backend formats command correctly | Mock `os/exec`, verify args |
| `internal/socket` | Wakeup send/receive, cleanup, send-when-no-listener | Real Unix sockets in `t.TempDir()` |
| `internal/ssh` | Config parsing, auth method selection | Mock SSH config files |
| `internal/setup` | Tool detection, hook config generation, idempotency | Fake filesystem with sample configs |

### Integration Tests

| Scenario | Coverage |
|---|---|
| emit -> SQLite -> attach -> JSONL output | Full remote-side pipeline |
| Backlog replay | Insert 10 rows, verify attach streams all 10 + summary |
| Flood control thresholds | Insert 3 vs 4 rows, verify backlog/summary behavior |
| Socket wakeup | Start attach, emit in another goroutine, verify streaming |

### SSH Transport Testing

No real SSH in CI. Test by:
1. Unit testing config parsing and auth method selection
2. Integration testing JSONL protocol over `io.Pipe()` to simulate the SSH stdio channel

### Manual Testing Checklist

- `notifytun local --target <real-vm>` connects and receives
- `notifytun test-notify` pops a notification on macOS and Linux
- `remote-setup --dry-run` on a VM with Claude Code installed
- Kill SSH, verify reconnect with backoff

## 13. Key Dependencies

| Dependency | Purpose |
|---|---|
| `github.com/spf13/cobra` | CLI framework, subcommand dispatch |
| `github.com/spf13/viper` | Config file parsing (TOML) |
| `modernc.org/sqlite` | Pure-Go SQLite driver, no CGo |
| `golang.org/x/crypto/ssh` | Native Go SSH client |
| `github.com/kevinburke/ssh_config` | Parse `~/.ssh/config` |

## 14. Key Decisions Log

| Decision | Choice | Rationale |
|---|---|---|
| Project name | `notifytun` | Short, evokes "notification tunnel" |
| Binary name | `notifytun` | Same as project, used everywhere |
| SSH client | `x/crypto/ssh` + config parser | Native Go, programmatic reconnect, respects `~/.ssh/config` |
| SQLite driver | `modernc.org/sqlite` | Pure Go, cross-compiles to all targets without CGo |
| CLI framework | cobra + viper | Industry standard, handles subcommands and config |
| Config format | TOML | Human-editable, idiomatic for Go CLI tools |
| State directory | `~/.notifytun/` | Simple, predictable, all platforms |
| Protocol | JSONL over SSH stdio | Simple, debuggable, testable with pipes |
| Remote command wrapping | `sh -lc` | Ensures PATH is loaded on remote |
| Backlog strategy | SQLite-backed, replay on reconnect | Persistent across process restarts |
| Flood control | Suppress > 3, show summary | Respects user attention on reconnect |
| Notifier backends | macOS + Linux + generic | Full platform coverage from v1 |
