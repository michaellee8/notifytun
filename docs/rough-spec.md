This spec is built around documented SSH remote-command behavior (`ssh` runs a remote command instead of a login shell when one is provided, and `-T` disables PTY allocation), SSH liveness detection via `ServerAliveInterval` / `ServerAliveCountMax`, Claude Code hooks including `Notification`, `TaskCompleted`, `Stop`, and `StopFailure`, Codex’s stable `notify` command hook, macOS notifications via AppleScript `display notification`, Linux notifications via `notify-send` with `-p` / `-r`, freedesktop notification replacement via `replaces_id`, and SQLite WAL plus `busy_timeout` for same-host concurrent read/write access. ([OpenBSD Manual Pages][1])

Paste this into Codex:

````markdown
# agentnotify v1 — implementation spec

## 1. Summary

Build a single Go binary named `agentnotify` that delivers Claude Code / Codex notifications from a remote Linux VM to a local laptop over a dedicated SSH stdio session.

### Hard requirements

- No third-party notification service.
- No SSH socket forwarding.
- No long-running background daemon on the VM.
- Durable notifications while the SSH session is down.
- When reconnecting after downtime:
  - if there is exactly 1 unread notification, show it normally
  - if there are 2+ unread notifications, show exactly **one** summary notification
- `tmux` must be irrelevant to delivery.
- The same Go binary is used locally and remotely.
- Local laptop targets:
  - macOS: native notifications via command-line tool
  - Linux: native notifications via command-line tool
  - Windows: binary should still build; v1 uses a generic custom-command backend rather than a native Windows notification backend

## 2. Architecture

### High-level flow

```text
Claude hook / Codex notify on VM
    -> agentnotify emit
        -> INSERT row into SQLite
        -> best-effort wakeup over remote unixgram socket (only if attach is connected)

Laptop
    -> agentnotify local
        -> starts dedicated ssh -T session
        -> runs remote: agentnotify attach --after-seq <last_acked_seq>
        -> reads JSONL from remote stdout
        -> shows native local notification
        -> persists last_acked_seq locally
````

### Key design decision

There is **no persistent daemon** on the VM.

Instead:

* `emit` is a one-shot process invoked by Claude/Codex hooks.
* `attach` is a session-scoped process that exists only while the dedicated SSH session exists.
* Durability comes from SQLite, not from a background broker.

## 3. Components

### 3.1 Remote subcommands

#### `agentnotify emit`

One-shot hook target.

Responsibilities:

* open or create SQLite DB
* insert one notification row
* best-effort send a wake-up packet to the unixgram socket if `attach` is currently running
* exit immediately

#### `agentnotify emit-codex`

Adapter for Codex `notify`.

Responsibilities:

* accept the Codex JSON payload
* map it to internal notification fields
* store the raw payload JSON in SQLite
* derive a conservative `title` / `body`
* perform the same logic as `emit`

#### `agentnotify attach`

Remote session-scoped streamer, launched over SSH by the laptop.

Responsibilities:

* open or create SQLite DB
* bind a unixgram socket for wake-up packets
* emit unread rows after `--after-seq` over stdout as JSONL
* continue streaming new rows until the SSH session exits
* clean up socket file on exit

### 3.2 Local subcommand

#### `agentnotify local`

Long-running local controller.

Responsibilities:

* spawn and supervise the dedicated SSH session
* reconnect with backoff when SSH dies
* keep local cursor / ack state
* collapse backlog into exactly one summary notification when unread count > 1
* call OS-specific local notifier backend

### 3.3 Local notifier backends

#### macOS backend

Use `osascript`.

#### Linux backend

Use `notify-send`.

#### Generic command backend

Use a user-provided command template for any unsupported platform, including Windows.

## 4. Non-goals

* No terminal bell / OSC notification path.
* No port forwarding.
* No websocket server.
* No public listening port on laptop or VM.
* No remote daemon / systemd service.
* No native Windows toast implementation in v1.
* No GUI app; CLI only.

## 5. Binary layout

Single binary with subcommands:

```text
agentnotify local
agentnotify attach
agentnotify emit
agentnotify emit-codex
agentnotify test-notify
```

## 6. Defaults

### Remote defaults

* SQLite DB path:

  * `$HOME/.local/state/agentnotify/notifications.db`
* Socket path:

  * `${XDG_RUNTIME_DIR}/agentnotify.sock` if `XDG_RUNTIME_DIR` exists
  * otherwise `/tmp/agentnotify-<uid>.sock`

### Local defaults

* state file:

  * `os.UserConfigDir()/agentnotify/state.json`
* backend:

  * `auto`
* reconnect backoff:

  * initial `1s`, max `30s`
* SSH liveness:

  * `ServerAliveInterval=15`
  * `ServerAliveCountMax=3`

## 7. Data model

## 7.1 SQLite

Use SQLite in WAL mode.

Schema:

```sql
PRAGMA journal_mode=WAL;
PRAGMA busy_timeout=5000;

CREATE TABLE IF NOT EXISTS notifications (
  id            INTEGER PRIMARY KEY AUTOINCREMENT,
  created_at    INTEGER NOT NULL DEFAULT (unixepoch()),
  app           TEXT NOT NULL,   -- "claude" | "codex" | ...
  kind          TEXT NOT NULL,   -- "needs_input" | "stop" | "failure" | ...
  title         TEXT NOT NULL,
  body          TEXT NOT NULL,
  payload_json  TEXT NOT NULL DEFAULT '{}'
);

CREATE INDEX IF NOT EXISTS idx_notifications_created_at
  ON notifications(created_at);
```

### Notes

* `id` is the durable, monotonic sequence number.
* Local ack state is **not** stored in the remote DB in v1.
* Ack state lives only on the laptop.
* `payload_json` stores raw structured context for later debugging.

## 7.2 Local state file

JSON file, per remote target / profile.

Example:

```json
{
  "profiles": {
    "user@vm.example.com": {
      "last_acked_seq": 184,
      "linux_replace_ids": {
        "summary:vm.example.com": 912
      }
    }
  }
}
```

### Semantics

* `last_acked_seq` means: the highest remote notification sequence successfully shown locally.
* `linux_replace_ids` is only used by Linux local backend to replace an existing summary notification instead of stacking a new one.

## 8. SSH transport

Use the system `ssh` binary, not a Go SSH client library.

### Rationale

* reuse the user’s existing SSH config, keys, jump hosts, and auth agent behavior
* simplest way to keep behavior aligned with normal shell usage
* easy to supervise / restart as a subprocess

### Required SSH invocation shape

```bash
ssh -T \
  -o ServerAliveInterval=15 \
  -o ServerAliveCountMax=3 \
  <target> \
  'sh -lc '"'"'<remote-command>'"'"''
```

### Remote command shape

Remote command must be POSIX-shell-escaped by the local process.

Example logical command:

```bash
agentnotify attach --after-seq 184
```

### Important rules

* Use a **dedicated** SSH session for notifications.
* Do **not** piggyback on the interactive `tmux` session.
* Do **not** allocate a PTY.
* Do **not** use socket forwarding.
* Do **not** require stdin interaction after the process starts.

## 9. Stdio protocol

Remote `attach` writes **JSONL** to stdout.

Stdin is unused in v1.

### Message types

#### `hello`

Sent once after startup.

```json
{
  "type": "hello",
  "version": 1,
  "hostname": "vm.example.com",
  "after_seq": 184,
  "socket_path": "/run/user/1000/agentnotify.sock"
}
```

#### `snapshot`

Sent once if there are unread rows at startup.

```json
{
  "type": "snapshot",
  "count": 7,
  "from_seq_exclusive": 184,
  "to_seq_inclusive": 191
}
```

#### `event`

Sent for both backlog rows and live rows.

```json
{
  "type": "event",
  "seq": 185,
  "ts_unix": 1776123456,
  "app": "claude",
  "kind": "needs_input",
  "title": "Claude Code",
  "body": "Claude Code needs your attention"
}
```

#### `snapshot_done`

Marks the end of the startup backlog replay.

```json
{
  "type": "snapshot_done",
  "to_seq_inclusive": 191
}
```

#### `log`

Optional informational line for stderr-like diagnostics that should not be shown as a notification.

```json
{
  "type": "log",
  "level": "warn",
  "message": "socket wakeup failed; polling fallback still active"
}
```

### Protocol rules

* One JSON object per line.
* No pretty printing.
* No binary framing.
* Unknown fields must be ignored by the receiver.
* Unknown message types must be logged and ignored.

## 10. Backlog / flood-control semantics

This is the most important behavior.

### On reconnect

Local `agentnotify local` starts `attach` with `--after-seq <last_acked_seq>`.

Then:

#### Case A: `snapshot.count == 0`

* show nothing
* enter live mode

#### Case B: `snapshot.count == 1`

* accept the single backlog `event`
* show it normally
* update `last_acked_seq` to that event seq
* enter live mode after `snapshot_done`

#### Case C: `snapshot.count >= 2`

* **do not** show each backlog event individually
* buffer / discard the per-row backlog events locally
* when `snapshot_done` arrives, show exactly one summary notification:

  * title: `agentnotify`
  * body: `"<count> unread notifications from <hostname>"`
* update `last_acked_seq` to `snapshot.to_seq_inclusive`
* enter live mode

### Important

* The underlying rows remain in SQLite for debug / audit purposes.
* Notification Center must not be flooded after reconnect.
* Backlog collapse happens on the **local** side, not the remote side.

## 11. Live delivery semantics

After snapshot handling, `attach` enters live mode.

For each new event:

* local shows one normal notification
* on successful local display:

  * persist `last_acked_seq = event.seq`
* on display failure:

  * do **not** advance `last_acked_seq`

This ensures failed local notifications are retried after a reconnect.

## 12. Remote `attach` behavior

### Startup sequence

1. resolve DB path and socket path
2. initialize DB
3. safely remove stale socket file if:

   * it exists
   * it is a socket
   * it is owned by current user
4. bind unixgram socket
5. emit `hello`
6. query unread rows where `id > after_seq`
7. if unread count > 0:

   * emit `snapshot`
   * emit each unread row as `event` in ascending `id`
   * emit `snapshot_done`
8. enter live loop

### Live loop

Implementation strategy:

* primary wake-up: unixgram socket
* fallback safety poll: every `2s`

On wake or poll:

* query rows `WHERE id > last_emitted_seq ORDER BY id`
* emit each as `event`
* update `last_emitted_seq`

### Exit behavior

On SIGINT / SIGTERM / stdout pipe break:

* close DB
* close socket
* unlink socket file if it still points to current process instance
* exit non-zero only for true startup / runtime errors

## 13. Remote `emit` behavior

### Steps

1. resolve defaults
2. initialize DB
3. insert one row
4. opportunistically prune old rows
5. best-effort send wake-up datagram to socket
6. exit 0 even if socket wake-up fails, as long as DB insert succeeded

### Insert contract

`emit` is successful if and only if the row is durably inserted into SQLite.

Socket delivery is an optimization only.

### Wake-up packet

Use unixgram and send a small JSON payload:

```json
{"seq": 185}
```

`attach` may ignore the payload contents and treat any datagram as a generic wake-up.

## 14. Retention policy

Avoid unbounded DB growth.

v1 retention policy:

* keep last `10000` rows
* also delete rows older than `30` days
* prune opportunistically from `emit` every `100` inserts

Implementation can choose either:

* count-based prune first, then age-based prune
* or age-based prune first, then count-based prune

Either is acceptable.

## 15. Codex payload adapter

### Goal

Codex only guarantees that the configured `notify` command receives a JSON payload. The adapter must therefore be conservative.

### `emit-codex` behavior

Accept payload from:

1. first non-flag positional argument, or
2. stdin if no positional JSON argument exists

Then:

* store the raw JSON in `payload_json`
* set:

  * `app = "codex"`
  * `kind`:

    * use payload `type` if it is a non-empty string
    * else `"notify"`
* derive `title = "Codex"`

### Body derivation priority

Try in this order:

1. top-level string field `message`
2. top-level string field `last_assistant_message`
3. top-level string field `summary`
4. fallback:

   * `"Codex needs your attention"` if payload suggests an input/approval pause
   * otherwise `"Codex completed a turn"`

### Safety rules

* truncate body to a reasonable notification length, e.g. 180 UTF-8 chars
* never fail because an expected field is missing
* malformed JSON should fail fast and return non-zero

## 16. Claude integration

Claude hooks call `agentnotify emit`.

### Default mapping

#### needs input / permission

* event: `Notification`
* app: `claude`
* kind: `needs_input`
* title: `Claude Code`
* body: `Claude Code needs your attention`

#### normal stop

* event: `Stop`
* kind: `stop`
* body: `Claude finished a turn`

#### task completion

* event: `TaskCompleted`
* kind: `task_completed`
* body: `Claude marked a task complete`

#### failure

* event: `StopFailure`
* kind: `failure`
* body: `Claude turn failed`

### Example Claude settings

```json
{
  "hooks": {
    "Stop": [
      {
        "matcher": "",
        "hooks": [
          {
            "type": "command",
            "command": "notifytun emit-hook --tool claude-code --event Stop"
          }
        ]
      }
    ],
    "Notification": [
      {
        "matcher": "",
        "hooks": [
          {
            "type": "command",
            "command": "notifytun emit-hook --tool claude-code --event Notification"
          }
        ]
      }
    ]
  }
}
```

Hook commands always exit 0. Any DB or logging failure is written to `~/.notifytun/notifytun-errors.log`.

## 17. Codex integration

Use the stable `notify` command hook in `~/.codex/config.toml`.

### Example

```toml
notify = ["notifytun", "emit-hook", "--tool", "codex", "--event", "notify"]
```

### Notes

* `emit-hook` accepts the Codex JSON payload as a trailing positional argument and derives title/body from it.
* Do not assume shell expansion in TOML array args.
* Do not use experimental Codex hooks in v1.
* Hook always exits 0; errors go to `~/.notifytun/notifytun-errors.log`.

## 17a. Gemini CLI integration

Use `AfterAgent` and `Notification` hooks in `~/.gemini/settings.json`.

### Example

```json
{
  "hooks": {
    "AfterAgent": [
      {
        "command": "notifytun emit-hook --tool gemini --event AfterAgent"
      }
    ],
    "Notification": [
      {
        "command": "notifytun emit-hook --tool gemini --event Notification"
      }
    ]
  }
}
```

### Notes

* `AfterAgent` fires after each agent turn completes; the hook payload carries the last assistant message.
* `Notification` fires when Gemini needs user attention.
* Hook always exits 0; errors go to `~/.notifytun/notifytun-errors.log`.

## 17b. OpenCode integration

Uses a plugin file at `~/.config/opencode/plugins/notifytun.js`.

### Example plugin (installed by `remote-setup`)

```js
// notifytun.js — installed by notifytun remote-setup
import { subscribe } from "@opencode-ai/sdk";
import { execFileSync } from "child_process";

subscribe("turn-complete", (event) => {
  const body = event?.lastAssistantMessage ?? "";
  execFileSync("notifytun", [
    "emit-hook", "--tool", "opencode", "--event", "turn-complete",
    "--body", body,
  ], { stdio: "ignore" });
});
```

### Notes

* The plugin reads the last assistant message via the OpenCode SDK.
* `notifytun emit-hook` always exits 0; any error is appended to `~/.notifytun/notifytun-errors.log`.

## 18. Local notifier backends

## 18.1 Interface

```go
type Notifier interface {
    Show(ctx context.Context, title string, body string, key string) error
}
```

### `key`

Used for replacement / dedupe hints.

Examples:

* live event:

  * `event:185`
* reconnect summary:

  * `summary:vm.example.com`

## 18.2 macOS backend

Use `osascript` via `exec.CommandContext`, not a shell.

### Required approach

Pass arguments safely, do not interpolate title/body directly into shell-quoted AppleScript.

Use a `run argv` script, for example conceptually:

```applescript
on run argv
  set theTitle to item 1 of argv
  set theBody to item 2 of argv
  display notification theBody with title theTitle
end run
```

Then invoke:

```text
osascript -e <script> -- "<title>" "<body>"
```

### Behavior

* no replacement semantics in v1
* one notification per live event
* one summary notification per reconnect backlog

## 18.3 Linux backend

Use `notify-send` via `exec.CommandContext`.

### Required behavior

* normal notification:

  * `notify-send -a agentnotify "<title>" "<body>"`
* summary notification:

  * first time:

    * `notify-send -p -a agentnotify "<title>" "<body>"`
    * parse returned ID and save it in local state
  * subsequent summary update for same `key`:

    * `notify-send -p -r <saved-id> -a agentnotify "<title>" "<body>"`

### Notes

* ignore replace-id errors by falling back to a normal non-replacing notification
* if `notify-send` is missing, backend init should fail with a clear error

## 18.4 Generic command backend

For unsupported platforms, including Windows in v1.

Flags:

```text
--backend=command
--notify-cmd=<template>
```

Template variables:

* `{{title}}`
* `{{body}}`
* `{{key}}`

Example placeholder only:

```text
--notify-cmd='my-notify-tool --title "{{title}}" --body "{{body}}"'
```

Implementation requirements:

* token replacement only
* no shell injection
* parse template into executable + args with explicit placeholder substitution
* document that user is responsible for platform-specific notifier tool choice

## 19. Local controller behavior

## 19.1 State machine

```text
Idle
  -> StartingSSH
  -> Connected
  -> Snapshot
  -> Live
  -> Disconnected
  -> Backoff
  -> StartingSSH
```

### Detailed behavior

#### StartingSSH

* load local state
* compute `after_seq`
* spawn SSH subprocess
* capture stdout / stderr separately

#### Connected

* wait for `hello`
* store `hostname` for summary text and keys

#### Snapshot

* if `snapshot.count == 0`, immediately transition to Live
* if `snapshot.count == 1`, show the single event normally
* if `snapshot.count >= 2`, discard individual backlog rows and show one summary after `snapshot_done`

#### Live

* show live events individually
* update local state after successful display

#### Disconnected

Triggered by:

* SSH exit
* stdout EOF
* parse fatal error
* context cancellation

#### Backoff

* exponential backoff with jitter
* initial `1s`
* double until `30s` max
* reset backoff after any successful `hello`

## 19.2 Local persistence

State file writes must be atomic:

* write temp file
* fsync temp file
* rename over existing file

## 20. Error handling

## 20.1 Remote `emit`

### DB insert fails

* return non-zero
* print clear error to stderr

### socket wake fails

* log to stderr
* still return 0 if DB insert succeeded

## 20.2 Remote `attach`

### socket bind fails

* return non-zero at startup

### stale socket file exists

* unlink only if it is a socket and owned by current user
* otherwise fail safe

### DB query fails mid-stream

* log to stderr
* continue retrying on next poll unless DB is unrecoverable

## 20.3 Local `local`

### notification display fails

* log error
* do not advance `last_acked_seq`

### malformed protocol message

* log and continue when possible
* if stream is unrecoverably corrupted, restart SSH session

## 21. Security / privacy requirements

* No third-party network service.
* No public listener.
* No forwarded socket from VM to laptop.
* No dependence on terminal escape sequences.
* No requirement for `ssh -A` agent forwarding.
* Remote socket lives in user-only runtime dir or user-owned `/tmp` path.
* Only the local laptop initiates the SSH connection.
* Local tool uses the user’s existing SSH trust / key config.

## 22. Implementation details

## 22.1 Language / dependencies

* Go 1.23+ acceptable
* prefer standard library where possible
* SQLite driver should be pure Go if practical to simplify cross-platform builds

## 22.2 Package layout

Suggested:

```text
/cmd/agentnotify/main.go
/internal/cli/
/internal/db/
/internal/proto/
/internal/remote/
/internal/local/
/internal/notifier/
/internal/state/
/internal/sshproc/
```

## 22.3 Suggested internal types

```go
type NotificationRow struct {
    ID         int64
    CreatedAt  int64
    App        string
    Kind       string
    Title      string
    Body       string
    PayloadJSON string
}

type HelloMsg struct {
    Type       string `json:"type"`
    Version    int    `json:"version"`
    Hostname   string `json:"hostname"`
    AfterSeq   int64  `json:"after_seq"`
    SocketPath string `json:"socket_path"`
}

type SnapshotMsg struct {
    Type             string `json:"type"`
    Count            int64  `json:"count"`
    FromSeqExclusive int64  `json:"from_seq_exclusive"`
    ToSeqInclusive   int64  `json:"to_seq_inclusive"`
}

type EventMsg struct {
    Type   string `json:"type"`
    Seq    int64  `json:"seq"`
    TsUnix int64  `json:"ts_unix"`
    App    string `json:"app"`
    Kind   string `json:"kind"`
    Title  string `json:"title"`
    Body   string `json:"body"`
}
```

## 23. CLI contract

## 23.1 `local`

```text
agentnotify local \
  --target user@vm.example.com \
  --remote-bin /home/ubuntu/bin/agentnotify \
  [--backend auto|macos|linux|command|none] \
  [--notify-cmd '...'] \
  [--state /path/to/state.json] \
  [--ssh /usr/bin/ssh]
```

### Required flags

* `--target`

### Optional flags

* `--remote-bin`
* `--backend`
* `--notify-cmd`
* `--state`
* `--ssh`

## 23.2 `attach`

```text
agentnotify attach [--after-seq N] [--db PATH] [--sock PATH]
```

## 23.3 `emit`

```text
agentnotify emit \
  --app claude \
  --kind needs_input \
  --title "Claude Code" \
  --body "Claude Code needs your attention" \
  [--payload-json '{}'] \
  [--db PATH] \
  [--sock PATH]
```

## 23.4 `emit-codex`

```text
agentnotify emit-codex [payload-json] [--db PATH] [--sock PATH]
```

## 23.5 `test-notify`

Local-only helper to test chosen backend.

```text
agentnotify test-notify --title "agentnotify" --body "test"
```

## 24. Exact acceptance criteria

A build is acceptable only if all of the following pass:

1. **Connected live notification**

   * with `local` running and SSH attached, one `emit` call on VM causes one local notification

2. **Disconnected durability**

   * stop the SSH session
   * run `emit` 5 times on VM
   * restart `local`
   * exactly one summary notification is shown locally
   * `last_acked_seq` advances to the highest unread seq

3. **Single unread behavior**

   * stop the SSH session
   * run `emit` once on VM
   * restart `local`
   * exactly one normal notification is shown, not a summary

4. **No VM daemon**

   * after disconnect, no `agentnotify` process remains running on VM except one-shot `emit` invocations

5. **Socket optionality**

   * if `attach` is not running, `emit` still succeeds by writing SQLite only

6. **Reconnect works**

   * kill SSH while `local` runs
   * `local` reconnects automatically
   * later live `emit` notifications resume without manual action

7. **Linux replace-id**

   * on Linux local backend, repeated reconnect summaries replace the prior summary instead of stacking many

8. **State durability**

   * restart local tool
   * it resumes from persisted `last_acked_seq`

9. **Codex adapter resilience**

   * `emit-codex` stores raw payload JSON and does not panic on missing optional fields

10. **Cross-platform build**

    * binary builds for:

      * linux/amd64
      * linux/arm64
      * darwin/arm64
      * darwin/amd64
      * windows/amd64

## 25. Testing plan

## 25.1 Unit tests

* SQLite init
* insert / query unread rows
* retention pruning
* state file round-trip
* protocol encode / decode
* codex payload parsing
* shell escaping for remote command construction
* notifier command arg construction

## 25.2 Integration tests

* local + fake ssh command that launches `attach` locally
* snapshot count 0 / 1 / many
* reconnect loop with forced process exit
* unixgram wake-up + fallback polling

## 25.3 Manual tests

### macOS

* `test-notify`
* live notification
* backlog summary

### Linux

* `test-notify`
* verify `notify-send -p` returns an ID
* verify `-r` replaces prior summary

## 26. Nice-to-have, not required for v1

* clickable “open SSH session” actions
* sound support
* message dedupe fingerprints
* richer Codex payload mapping
* Windows native toast backend
* telemetry / metrics
* subcommands for listing / clearing stored remote notifications

## 27. Implementation order

1. DB init + `emit`
2. `attach` snapshot replay
3. `local` SSH supervisor + JSONL parser
4. local state persistence
5. backlog collapse logic
6. macOS notifier
7. Linux notifier with replace-id support
8. Codex adapter
9. retention pruning
10. tests + docs

## 28. Final v1 behavior in one sentence

A one-shot hook writes each remote notification into SQLite, a reconnecting SSH stdio session replays unread notifications from that DB, and the local side shows either one live notification or one reconnect summary without ever needing a background VM daemon.

```

The concrete interfaces used above come from the docs for SSH remote commands and `-T`, SSH keepalive settings, Claude hook events and examples, Codex `notify`, AppleScript notifications, Linux `notify-send`, freedesktop replacement IDs, and SQLite WAL / busy-timeout behavior. :contentReference[oaicite:1]{index=1}
::contentReference[oaicite:2]{index=2}
```

[1]: https://man.openbsd.org/ssh.1 "https://man.openbsd.org/ssh.1"

