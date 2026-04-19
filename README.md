# notifytun

`notifytun` is a single Go CLI that tunnels notifications from a remote Linux VM to your local desktop over SSH. It is built for the common "AI agent running on a remote box, human waiting on a laptop" workflow: tool hooks write notifications remotely, and `notifytun local` delivers them on your machine without a remote daemon or third-party notification service.

## Why use it

- One binary for both local and remote use
- Durable remote queue backed by SQLite
- SSH-based transport with automatic reconnects
- Native local notifications on macOS and Linux
- Generic command backend for unsupported platforms
- Helper command to wire up Claude Code and Codex hooks on the remote machine

## How it works

```text
tool hook
  -> notifytun emit
  -> remote SQLite + best-effort socket wakeup
  -> notifytun attach (started over SSH)
  -> notifytun local
  -> local desktop notification
```

`notifytun emit` always records notifications in the remote SQLite database. If `notifytun local` is currently connected, `emit` also pokes a Unix socket so the remote `attach` process can stream the new notification immediately.

If the SSH session drops, notifications continue queueing remotely. When the connection comes back, `attach` replays the backlog. If more than 3 notifications accumulated while disconnected, `notifytun` sends one summary notification instead of replaying each item individually.

## Requirements

- Go `1.25.5` or newer to build from source
- A remote Linux host reachable over SSH
- A local machine running:
  - macOS for the `macos` backend
  - Linux with `notify-send` available for the `linux` backend
  - any platform with `--backend generic --notify-cmd ...`
- An SSH trust relationship already established in `~/.ssh/known_hosts`

## Build and install

Quick one-liner:

```bash
go install github.com/michaellee8/notifytun/cmd/notifytun@latest
```

Build the binary from the repo root:

```bash
go build -o notifytun ./cmd/notifytun
```

The same binary needs to be available on both machines. A simple approach is to build locally, copy it to the remote host, and place it in `PATH` on both ends.

Example:

```bash
go build -o notifytun ./cmd/notifytun
install -m 0755 notifytun ~/.local/bin/notifytun
scp notifytun myvm:~/notifytun
ssh myvm 'install -m 0755 ~/notifytun ~/.local/bin/notifytun'
```

If the remote binary is not in `PATH`, `notifytun local` also checks `~/go/bin/notifytun` automatically, so a plain `go install` on the remote works out of the box. For any other install location, pass it with `--remote-bin`.

## Quickstart

1. Build `notifytun` and install it locally and on the remote VM.
2. On the remote VM, preview supported hook setup:

```bash
notifytun remote-setup --dry-run
```

3. If the preview looks right, apply it:

```bash
notifytun remote-setup
```

`remote-setup` is interactive and will prompt with `Apply? [Y/n]`.

4. On your local machine, start the long-running tunnel:

```bash
notifytun local --target myvm
```

You can use an SSH config host alias or a full `user@host` target.

5. Verify your local notification backend:

```bash
notifytun test-notify
```

At this point, supported remote tool hooks can call `notifytun emit`, and notifications should appear on your local desktop.

## Configuration

The local command optionally reads `~/.notifytun/config.toml`. CLI flags override config values.

Start from the included example:

```toml
[local]
# target = "user@myvm"
# remote-bin = "notifytun"
# backend = "auto"
# notify-cmd = "/usr/local/bin/my-notifier"
```

Supported keys:

- `local.target`: SSH target for `notifytun local`
- `local.remote-bin`: remote binary path or command name
- `local.backend`: `auto`, `macos`, `linux`, or `generic`
- `local.notify-cmd`: command used by the `generic` backend

Remote defaults:

- SQLite database: `~/.notifytun/notifytun.db`
- Unix socket: `~/.notifytun/notifytun.sock`

The generic backend sends JSON on stdin to the configured command with this shape:

```json
{"title":"...","body":"...","tool":"..."}
```

## Commands

### `notifytun local`

Connects to the remote host over SSH, starts `notifytun attach`, reads the JSONL stream, and delivers notifications locally. It reconnects with exponential backoff if the connection drops.

Common flags:

- `--target`: required unless set in config
- `--remote-bin`: remote `notifytun` path or command name
- `--backend`: `auto`, `macos`, `linux`, `generic`
- `--notify-cmd`: required for `generic`, and also used when `auto` cannot select a native backend
- `--config`: explicit config file path

### `notifytun attach`

Internal remote streaming command. `notifytun local` starts this over SSH. It replays undelivered rows from SQLite, streams new notifications, and emits heartbeat messages to keep the connection healthy.

### `notifytun emit`

Writes a notification to the remote SQLite queue and best-effort wakes the remote socket listener.

Example:

```bash
notifytun emit --tool claude-code --title "Task complete" --body "Finished refactor"
```

It also accepts one trailing Codex `notify` JSON payload and can derive a title/body automatically when the payload type is `agent-turn-complete`.

### `notifytun remote-setup`

Detects supported AI tools on the remote host, shows what will be configured, and applies config updates after confirmation.

Current behavior:

- Claude Code: adds `Stop` and `Notification` hooks in `~/.claude/settings.json`
- Codex CLI: sets `notify = ["notifytun", "emit", "--tool", "codex"]` in `~/.codex/config.toml`
- Gemini CLI: detection only, no automatic configuration in v1
- OpenCode: detection only, no automatic configuration in v1

### `notifytun test-notify`

Sends a local test notification using the selected backend.

Example:

```bash
notifytun test-notify --backend auto
```

## AI tool integration

`remote-setup` is the easiest way to wire supported tools into `notifytun`, but the integration model is simple: remote tool hooks call `notifytun emit`.

### Claude Code

`remote-setup` adds these commands:

- `notifytun emit --tool claude-code --title 'Task complete'`
- `notifytun emit --tool claude-code --title 'Needs attention'`

### Codex CLI

`remote-setup` writes this root-level config entry:

```toml
notify = ["notifytun", "emit", "--tool", "codex"]
```

When Codex passes its JSON notify payload, `notifytun emit` will derive a conservative title/body if you do not supply `--title`.

## Troubleshooting

### `--target is required`

Set `--target` explicitly or add `local.target` to `~/.notifytun/config.toml`.

### SSH connection or host verification fails

`notifytun` respects your SSH config and `known_hosts`. Make sure:

- the target is reachable with normal `ssh`
- the host key is already trusted
- authentication works with your SSH agent or SSH config

### Linux notifications do not appear

The `linux` backend uses `notify-send`. Install it or switch to `--backend generic --notify-cmd ...`.

### `--notify-cmd` is required

That happens when:

- you selected `--backend generic` without a command
- `--backend auto` could not find a native notifier on the current platform

### Remote binary cannot be found

By default `notifytun local` tries `notifytun` on the remote `PATH` and then falls back to `~/go/bin/notifytun`. If neither exists, install `notifytun` into the remote `PATH` or pass the correct remote location with `--remote-bin`.

### Nothing shows up locally

Check the full path:

- remote hooks are actually calling `notifytun emit`
- `notifytun local` is running on your machine
- the SSH session is connected

If the tunnel was down and more than 3 notifications queued up, you should get one summary notification instead of one per queued item.

## Development

Run the test suite from the repo root:

```bash
go test ./...
```

This repo currently has test coverage across the main implementation areas:

- CLI behavior
- SSH transport and reconnect handling
- SQLite-backed storage
- local notifier backends
- remote setup helpers

Useful files when working on the project:

- [`cmd/notifytun/main.go`](cmd/notifytun/main.go)
- [`config.example.toml`](config.example.toml)
- [`internal/cli`](internal/cli)
- [`internal/ssh`](internal/ssh)
- [`internal/db`](internal/db)
- [`internal/notifier`](internal/notifier)
