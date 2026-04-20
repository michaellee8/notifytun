# `remote-setup` for all four coding agents

**Date:** 2026-04-20
**Scope:** Make `notifytun remote-setup` fully configure every tool it already detects — Claude Code, Codex CLI, Gemini CLI, OpenCode — and surface the agent's actual message in each notification.

## 1. Motivation

`remote-setup` today only writes hook configs for Claude Code and Codex CLI. Gemini CLI and OpenCode are detected and then dismissed as "not supported in v1". When a notification does fire, the body is empty: the hook commands use static `--title 'Task complete'` arguments and throw away the agent's actual output.

Both problems are now addressable:

- Gemini's hook schema (`~/.gemini/settings.json`) is structurally identical to Claude's and exposes `AfterAgent.prompt_response` on stdin.
- Claude's `Stop` hook payload has a first-class `last_assistant_message` field (previously missed).
- OpenCode ships a plugin system with `session.idle` events and a `client.session.messages()` accessor that a small JS plugin can use to fetch the last assistant message.

Additionally, the code path is becoming noisy — three parallel `switch tool.Name` blocks in `remotesetup.go`. Adding two more tools without refactoring would triple the per-tool edit surface.

## 2. Goals

- All four detected tools receive a working hook configuration after `notifytun remote-setup`.
- Every notification includes the tool name in the title and the agent's last message in the body, truncated to a notification-friendly length.
- Hook execution never blocks the agent, even when notifytun's DB or socket fails. Errors go to a dedicated log next to the DB.
- Per-tool configuration logic lives in one place per tool (no cross-file switch statements keyed on tool name).

## 3. Non-goals

- No new top-level `notifytun` subcommands beyond a single `emit-hook` adapter.
- No auto-migration across host machines — `remote-setup` is a per-host operation.
- No log rotation or structured log format in v1. A plain append-only text file is sufficient.
- No interactive per-event opt-in. `remote-setup` installs the canonical event set for each tool.
- No native message-quality improvements (markdown stripping, citation trimming, etc.) — truncation-to-180 is the only transform.

## 4. Per-tool behavior

| Tool | Config path | Events | Title | Body source |
|---|---|---|---|---|
| Claude Code | `~/.claude/settings.json` JSON hooks | `Stop` | `Claude Code: Task complete` | stdin `.last_assistant_message` |
| | | `Notification` | `Claude Code: Needs attention` | stdin `.message` |
| Codex CLI | `~/.codex/config.toml` root `notify` | `notify` | `Codex: Task complete` | payload `.last-assistant-message`, fallback `input-messages` joined |
| Gemini CLI | `~/.gemini/settings.json` JSON hooks | `AfterAgent` | `Gemini CLI: Task complete` | stdin `.prompt_response` |
| | | `Notification` | `Gemini CLI: Needs attention` | stdin `.message` |
| OpenCode | `~/.config/opencode/plugins/notifytun.js` | plugin `event` with `event.type === "session.idle"` | `OpenCode: Task complete` | plugin reads `client.session.messages(...)` for last assistant text, pipes it to `emit-hook` stdin |

Rules common to all:

- Body is trimmed, then truncated to 180 UTF-8 chars (existing Codex convention).
- Empty or missing body → notification is title-only (no placeholder body, no error).
- Unknown `(tool, event)` combinations sent to `emit-hook` are logged to the error file and the command still exits 0 (see §8).

## 5. New subcommand: `emit-hook`

```
notifytun emit-hook --tool <name> --event <name>
                    [--db PATH] [--socket PATH]
                    [<payload-json>]
```

Flags:

- `--tool` — one of `claude-code`, `gemini`, `codex`, `opencode`.
- `--event` — event identifier. See table in §4.
- `--db`, `--socket` — same defaults as today (`~/.notifytun/notifytun.db`, `~/.notifytun/notifytun.sock`).
- Positional payload JSON — optional. Used by Codex (which passes the payload as a single argv element). If absent, stdin is read.

Behavior:

1. Resolve payload bytes: positional arg if present, else read stdin until EOF. Missing/empty payload is a recoverable "no body" case, not an error.
2. `json.Unmarshal` into `map[string]any`. Parse errors are logged and treated as "no body" — the notification still fires with title only.
3. Look up `(tool, event)` in a static dispatch table. Each entry contains:
   - `titleSuffix` — e.g., `"Task complete"` or `"Needs attention"`.
   - `bodyFields` — ordered list of JSON field paths to try for the body (first non-empty string wins).
4. Title = `"<Tool display name>: <titleSuffix>"`. Tool display names: `Claude Code`, `Codex`, `Gemini CLI`, `OpenCode`.
5. Body = `truncate(trim(first_non_empty(bodyFields)), 180)`.
6. Call existing `db.Insert(title, body, tool)` and `socket.SendWakeup(socketPath)`.
7. Exit 0 (see §8).

Dispatch table (literal):

| `--tool` | `--event` | Title suffix | Body fields (in order) |
|---|---|---|---|
| `claude-code` | `Stop` | `Task complete` | `last_assistant_message` |
| `claude-code` | `Notification` | `Needs attention` | `message` |
| `codex` | `notify` | `Task complete` | `last-assistant-message`, `input-messages` (joined with space) |
| `gemini` | `AfterAgent` | `Task complete` | `prompt_response` |
| `gemini` | `Notification` | `Needs attention` | `message` |
| `opencode` | `session.idle` | `Task complete` | `body` (see §6 — plugin crafts a `{"body": "..."}` payload) |

The existing `emit` subcommand remains for manual use, local test scripts, and backwards-compat. `remote-setup` stops installing `emit` hooks and always installs `emit-hook` going forward.

## 6. OpenCode plugin contents

`~/.config/opencode/plugins/notifytun.js` is written verbatim:

```javascript
// Managed by `notifytun remote-setup`. Edits will be overwritten.
export const NotifytunPlugin = async ({ client, $ }) => {
  return {
    event: async ({ event }) => {
      if (event.type !== "session.idle") return;
      let body = "";
      try {
        const sessionID =
          event.properties?.sessionID ?? event.properties?.session_id;
        if (sessionID) {
          const msgs = await client.session.messages({ path: { id: sessionID } });
          const last = Array.isArray(msgs) ? msgs[msgs.length - 1] : null;
          body = extractText(last);
        }
      } catch (_) {
        // Never let a notification failure block the session.
      }
      const payload = JSON.stringify({ body });
      try {
        await $`echo ${payload} | notifytun emit-hook --tool opencode --event session.idle`;
      } catch (_) {
        // notifytun missing or failing must not block the session.
      }
    },
  };
};

function extractText(msg) {
  if (!msg) return "";
  const parts = msg.parts ?? msg.content ?? [];
  if (typeof parts === "string") return parts;
  if (!Array.isArray(parts)) return "";
  return parts
    .map((p) => (typeof p === "string" ? p : p?.text ?? ""))
    .filter(Boolean)
    .join("\n");
}
```

Rationale:

- The file is a single, self-contained ESM module. No build step.
- The `extractText` helper is defensive because OpenCode's message part shape is not stably documented; this handles both string-parts and `{text: string}` parts and silently returns empty if neither matches.
- `echo` + pipe is used rather than the Bun shell's stdin helper to keep the plugin portable across Bun/Node-execution contexts.
- All failures are swallowed inside the plugin. notifytun's own error logging handles the other side.

Idempotency rule (see §7): `IsConfigured` for OpenCode is a byte-for-byte comparison against this canonical content (after normalizing trailing newline). Any divergence → `Apply` overwrites.

## 7. `remote-setup` refactor: `Configurator` interface

New file `internal/setup/configurator.go`:

```go
type Configurator interface {
    Name() string                              // "Claude Code"
    Binaries() []string                        // ["claude", "claude-code"]
    ConfigPath(home string) string             // for preview/error messages
    IsConfigured(home string) (bool, error)
    PreviewAction(home string) string          // one line preview
    Apply(home string) error
}

var Registered = []Configurator{
    &ClaudeConfigurator{},
    &CodexConfigurator{},
    &GeminiConfigurator{},
    &OpenCodeConfigurator{},
}
```

- `DetectTools` iterates `Registered`, does the existing `lookPath` dance against each configurator's `Binaries()`, and returns the subset whose binary was found.
- `remotesetup.go` becomes a loop over detected configurators — preview, prompt, apply. Zero `switch tool.Name`.
- Shared helper `internal/setup/jsonhooks.go` implements the Claude/Gemini JSON hook file format. Takes `(path, []hookEvent)` where `hookEvent = {event, matcher, command}`. `ClaudeConfigurator` and `GeminiConfigurator` each construct their own event list and call into the helper for read/parse/match/write/idempotency.
- Codex keeps its TOML-specific handling in `codex.go`; OpenCode gets a new `opencode.go` with the verbatim-file pattern.

The legacy `Tool` struct and its `Supported`/`Configured` fields are replaced by `DetectedTool{Name, Binary, Cfg Configurator, Configured bool}` produced by detection. This shrinks `remotesetup.go` to roughly the same size as today while adding two tools.

## 8. Error handling and logging — "never block the agent"

`emit-hook` (and `emit`, for consistency — both are hook targets) must always exit 0. A notification write failure must never break the agent's turn.

Error log file:

- Path: `<dir(dbPath)>/notifytun-errors.log`. With defaults: `~/.notifytun/notifytun-errors.log`. If `--db` is overridden, the log follows into the same directory.
- Format: one line per event, `<RFC3339 timestamp>\t<subcommand>\t<stage>: <error>\n`, where `<stage>` is one of `parse`, `dispatch`, `db-open`, `db-insert`, `log-open`. Example: `2026-04-20T12:34:56Z\temit-hook\tdb-insert: unable to open database file: permission denied`.
- Mode: `os.OpenFile` with `O_APPEND|O_CREATE|O_WRONLY` and perms `0o644`. POSIX `O_APPEND` gives atomic short writes across concurrent `emit-hook` invocations.
- If opening or writing the log itself fails (e.g., disk full), the command still exits 0 silently. There is nowhere safer to report this.

What gets logged:

- DB open failure, insert failure.
- JSON unmarshal failure on the payload.
- Unknown `(tool, event)` dispatch.
- Unexpected positional argument count.

What does NOT get logged (expected, not an error):

- Socket wakeup failure (normal when `attach` is not running — matches today's `_ = socket.SendWakeup(...)`).
- Empty payload → title-only notification.
- Missing body field → title-only notification.

Exit code:

- `emit-hook`: always 0.
- `emit`: also always 0 under this change. The only caller that cared about non-zero was `remote-setup`'s tests, which will be updated.
- `remote-setup` itself keeps its existing non-zero-on-setup-failure behavior (setup failures are visible to the human user running the command, not to an agent).

## 9. Migration of existing Claude hooks

Users who ran the previous `remote-setup` have this installed:

```
notifytun emit --tool claude-code --title 'Task complete'
notifytun emit --tool claude-code --title 'Needs attention'
```

`ClaudeConfigurator.Apply` performs a bounded cleanup before writing:

- For each of `hooks.Stop` and `hooks.Notification`, scan every entry's `hooks[].command` field.
- Remove any entry whose `command` string, after leading whitespace is trimmed, has either `notifytun emit ` or `notifytun emit-hook ` as a prefix (with the trailing space — `notifytun emit ` must not also match `notifytun emit-hook ` because of the space, but both prefixes are treated as notifytun-owned and removed).
- Append one fresh entry with the canonical `emit-hook` command.

Non-notifytun hook entries (user's own scripts) are untouched. `IsConfigured` returns true only when exactly the canonical `emit-hook` command is present and no legacy `emit` commands remain.

Codex has no migration concern — `ApplyCodexNotifyConfig` already replaces the entire root `notify` array.

Gemini and OpenCode are new — no legacy to clean up.

## 10. Package layout

```
internal/
  cli/
    emit.go          # unchanged signature; exit-0 + error-log
    emithook.go      # new — emit-hook command
    emithook_test.go # new
    remotesetup.go   # refactored to loop over Registered configurators
  setup/
    configurator.go  # new — interface + Registered list
    claude.go        # extracted; uses jsonhooks
    codex.go         # extracted
    gemini.go        # new; uses jsonhooks
    opencode.go      # new; verbatim file writer
    jsonhooks.go     # new — Claude+Gemini shared JSON hook file handling
    errorlog.go      # new — shared logger used by emit and emit-hook
    setup.go         # trimmed to just DetectTools + Tool struct
```

`setup.go` becomes much smaller. The individual per-tool files each hold one configurator and its tests.

## 11. Testing

**`emit-hook` unit tests (`emithook_test.go`)**

Table-driven across the full dispatch matrix. Each case provides:

- Input payload bytes (positional or stdin).
- Expected row fields (`title`, `body`, `tool`) in the temp SQLite DB.
- Expected error-log state (empty or contains expected fragment).

Cases:

1. `claude-code/Stop` — happy path with `last_assistant_message`.
2. `claude-code/Notification` — happy path with `message`.
3. `codex/notify` with positional arg — payload has `last-assistant-message`.
4. `codex/notify` fallback — payload has only `input-messages`.
5. `gemini/AfterAgent` — happy path with `prompt_response`.
6. `gemini/Notification` — happy path with `message`.
7. `opencode/session.idle` — payload `{"body": "..."}`.
8. Body truncation — 500-char body truncated to 180 UTF-8 chars.
9. Empty payload — title-only row, no error log entry.
10. Malformed JSON — title-only row, error log entry written with a `parse` fragment.
11. Unknown tool — no DB row, error log entry with `unknown tool`, exit 0.
12. DB write failure (pass a non-writable path) — error log entry with stage `db-open` or `db-insert`, exit 0.
13. `emit` DB write failure (same hostile path) — same behavior as case 12: error log entry, exit 0. This verifies the exit-0 change applies to both subcommands, not just `emit-hook`.

**`configurator` unit tests (per-tool)**

For each configurator: `IsConfigured` on empty/absent/wrong/canonical files, `Apply` writes canonical content, second `Apply` is a no-op, `Apply` preserves unrelated user entries (where applicable), `Apply` removes legacy notifytun entries (Claude migration case).

OpenCode-specific: `IsConfigured` reports false when the file exists but content differs; `Apply` overwrites to canonical content; `Apply` creates parent dirs if absent.

**`remote-setup` command tests (`remotesetup_test.go`)**

Keep the existing tests updated to the `Configurator` registry API. Add one test that registers a fake configurator returning controlled values and asserts the preview/prompt/apply loop calls each method once per tool.

**Manual verification (documented in acceptance, not automated)**

- Run `notifytun remote-setup` with all four tools on PATH on a real host, confirm each tool's config file ends up in the expected state.
- Fire one notification via each tool's hook in isolation and confirm the body contains the agent's actual message.

## 12. Documentation changes

- `README.md`: update the remote-setup section to list all four supported tools; remove any "detected but not supported" caveats for Gemini/OpenCode; mention that the error log lives at `<db-dir>/notifytun-errors.log`.
- `config.example.toml`: no change (DB/socket defaults unchanged).
- `docs/rough-spec.md`: §8 says `ssh` binary use is already consistent; §15–17 are updated to reflect `emit-hook` as the canonical hook command and to list Gemini/OpenCode alongside Claude/Codex.
- `docs/superpowers/specs/2026-04-14-notifytun-design.md`: add a one-line note at the top marking the Claude/Codex-only scope as superseded for `remote-setup`; the rest of that spec still applies.

## 13. Out-of-scope follow-ups

- Log rotation / size cap for `notifytun-errors.log`.
- Markdown / code-fence stripping from `last_assistant_message` before truncation.
- Per-event `--enable`/`--disable` flags on `remote-setup`.
- Native OpenCode SDK-based plugin (if OpenCode publishes a typed plugin SDK, the current `.js` can be re-expressed as `.ts`).
- Importing the Anthropic/Google hook payload types from their respective SDKs, once those stabilize.

## 14. Acceptance

This change is acceptable when:

- `go build ./...` and `go test ./...` pass on Linux and macOS.
- `notifytun remote-setup` on a host with all four CLIs on PATH writes:
  - `~/.claude/settings.json` with `emit-hook` commands under `Stop` and `Notification`, legacy `emit` entries removed.
  - `~/.codex/config.toml` with `notify = ["notifytun", "emit-hook", "--tool", "codex", "--event", "notify"]`.
  - `~/.gemini/settings.json` with `emit-hook` commands under `AfterAgent` and `Notification`.
  - `~/.config/opencode/plugins/notifytun.js` with the verbatim canonical content from §6.
- Running `remote-setup` a second time is a no-op for all four tools.
- A hook firing with a payload containing the agent's message produces a notification whose title is `"<Tool>: <event>"` and whose body is the agent's truncated message.
- A hook firing when the DB path is unwritable exits 0, writes one line to `notifytun-errors.log`, and does not propagate failure to the agent.
