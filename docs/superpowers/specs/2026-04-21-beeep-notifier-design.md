# notifytun Beeep Notifier Migration Design

**Date:** 2026-04-21
**Module:** `github.com/michaellee8/notifytun`
**Go version:** 1.25.5

> **Note:** This document supersedes the local notifier backend portions of
> [2026-04-14-notifytun-design.md](2026-04-14-notifytun-design.md). The SSH,
> remote queue, hook integration, and transport design remain unchanged.

## 1. Overview

Replace notifytun's current local notification implementation with
[`github.com/gen2brain/beeep`](https://github.com/gen2brain/beeep) so the
default backend is native and cross-platform without maintaining separate
macOS- and Linux-specific code paths in this repository.

After this change, notifytun exposes two local backend modes:

- `auto`: native notifications via `beeep`
- `generic`: user-provided command that receives the notification JSON on stdin

This is a simplification, not a feature expansion in the rest of the system.
The remote Linux queue, SSH transport, and CLI flow stay the same.

## 2. Goals

- Make the default local notification path cross-platform, including Windows.
- Stop maintaining custom local notifier implementations for macOS and Linux.
- Keep a fully user-controlled escape hatch through the existing `generic`
  backend.
- Update the docs so the platform support story matches the actual code.

## 3. Non-Goals

- No changes to remote-side behavior (`emit`, `attach`, SQLite, socket wakeup,
  SSH reconnects).
- No attempt to preserve unreleased `macos` or `linux` backend names.
- No additional notification customization such as icons, urgency, or click
  actions in this migration.
- No fallback from `auto` into `generic`; if native delivery fails, the command
  should return the native error and the user can explicitly choose `generic`.

## 4. User-Facing Behavior

### 4.1 Backend Contract

The backend surface becomes:

- `auto`
- `generic`

`macos` and `linux` are removed from CLI help, config examples, docs, and
backend selection logic.

### 4.2 `auto`

`auto` always initializes a native notifier backed by `beeep`.

Behavioral implications:

- The project no longer promises exact local implementation details such as
  `osascript` on macOS or `notify-send` on Linux.
- Platform-specific fallback behavior comes from `beeep`.
- Windows becomes part of the normal native support story instead of "use
  generic."

### 4.3 `generic`

`generic` keeps the current contract:

- `--notify-cmd` is required.
- notifytun marshals `{title, body, tool}` as JSON and writes it to stdin.
- stdout/stderr are surfaced by `test-notify` when the selected notifier is the
  generic command runner.

## 5. Code Design

### 5.1 Notifier Package

Introduce a single native notifier implementation in
`internal/notifier/beeep.go`.

Responsibilities:

- set `beeep.AppName = "notifytun"`
- implement `Notify(ctx context.Context, n Notification) error`
- call `beeep.Notify` with a bundled PNG byte payload so the native path does
  not rely on an empty-string icon value that `beeep` may interpret as a file
  path on some platforms

`ctx` remains part of the interface for consistency with the rest of the code,
even though `beeep.Notify` itself is not context-aware.

### 5.2 Backend Selection

`internal/notifier/notifier.go` becomes a two-way selector:

- `auto` -> native `beeep` notifier
- `generic` -> existing generic command notifier

Any other backend name returns an error.

### 5.3 Removed Code

Delete the explicit OS-specific native notifier implementations:

- `internal/notifier/macos.go`
- `internal/notifier/linux.go`

This also removes the old `newAuto` OS-detection and `notify-send` lookup logic.

### 5.4 Unchanged Integration Points

The rest of the application remains coupled only to `notifier.Notifier`.

No changes are required to:

- `notifytun local`
- notification queueing and dispatch
- `notifytun test-notify` command flow

The only special-case behavior retained in `test-notify` is generic backend
stdout/stderr passthrough via `CommandOutputConfigurer`.

## 6. Testing Strategy

### 6.1 Replace Old Native Tests

Remove tests that assert the exact command line for `osascript` and
`notify-send`. Those tests are implementation-specific to code that no longer
exists.

### 6.2 New Factory Tests

Add notifier factory coverage for:

- `New("auto", "")` returns the native beeep-backed notifier
- `New("generic", "")` returns an error
- `New("generic", "...")` succeeds
- unknown backend names return an error

### 6.3 Native Forwarding Test

Add a unit test for the native notifier that verifies it forwards title/body to
the `beeep` call without sending a real desktop notification.

Implementation approach:

- keep the direct dependency on `beeep`
- wrap `beeep.Notify` behind a package-level function variable
- swap that variable in tests to capture inputs and assert forwarding behavior

This keeps the test fast and deterministic while still covering notifytun's own
integration logic.

### 6.4 Existing Generic Tests

Keep the current `generic`-path tests in `internal/cli/testnotify_test.go`
because they still verify user-visible behavior:

- stdout passthrough
- stderr passthrough
- wrapped errors from the custom command

## 7. Documentation Changes

Update user-facing docs so the support story is accurate and minimal.

### 7.1 README

Revise:

- feature bullets
- requirements
- quickstart backend verification text
- config documentation
- backend descriptions
- troubleshooting guidance

New positioning:

- native cross-platform notifications via `beeep`
- use `generic` when you want complete control over the notification command

### 7.2 Config Example And CLI Help

Update:

- `config.example.toml`
- backend flag descriptions in CLI commands

Document only:

- `auto`
- `generic`

### 7.3 Historical Design Docs

Update retained design/spec docs only where they would clearly mislead future
maintenance about the current backend contract. Prefer short "superseded by"
notes over large retroactive rewrites.

## 8. Risks And Trade-Offs

### 8.1 Loss Of Exact Native Implementation Control

notifytun will no longer control the exact macOS/Linux notification command line
or dependency checks. This is intentional: the maintenance burden moves to a
well-used cross-platform library.

### 8.2 Native Failure Surface Changes

Some machines may now fail differently than before because `beeep` chooses
different platform mechanisms and fallbacks than notifytun's current custom
implementation. The mitigation is the retained `generic` backend.

### 8.3 Context Cancellation

The old native implementations used `exec.CommandContext`; `beeep` does not
accept a context. This is acceptable because local notification delivery is a
short-lived side effect and the notifier interface stability is more valuable
than trying to force cancellation into a library that does not expose it.

## 9. Rollout

Implementation should be delivered as one migration:

1. add the `beeep` dependency
2. replace the native notifier code path
3. update tests
4. update docs

There is no compatibility window for `macos` or `linux` backend names because
the project has not been released.
