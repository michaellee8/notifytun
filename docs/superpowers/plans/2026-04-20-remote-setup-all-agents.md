# `remote-setup` for all four coding agents — Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Extend `notifytun remote-setup` to fully configure Claude Code, Codex CLI, Gemini CLI, and OpenCode, and make every notification surface the agent's actual message; never let a hook failure block the agent.

**Architecture:** Add one new `emit-hook` adapter subcommand with a static `(tool, event) → (title, body-field)` dispatch table; refactor `remote-setup` tool handling into a `Configurator` interface with one registered implementation per tool; share the JSON-hooks file format between Claude and Gemini via a small helper; write OpenCode integration as a verbatim `.js` plugin file; swallow all emit errors into a dedicated log file next to the database so hooks always exit 0.

**Tech Stack:** Go 1.23+, `github.com/spf13/cobra`, `modernc.org/sqlite`, `github.com/pelletier/go-toml/v2`, standard library JSON.

**Spec:** [docs/superpowers/specs/2026-04-20-remote-setup-all-agents-design.md](../specs/2026-04-20-remote-setup-all-agents-design.md)

---

## File structure

New files:

- `internal/cli/errorlog.go` — shared error-log writer used by `emit` and `emit-hook`. Placed in `internal/cli` (not `internal/setup` as sketched in the spec) because it is CLI-runtime infrastructure, not configuration code, and co-locating keeps imports straight.
- `internal/cli/errorlog_test.go`
- `internal/cli/emithook.go` — `emit-hook` subcommand with dispatch table.
- `internal/cli/emithook_test.go`
- `internal/setup/configurator.go` — `Configurator` interface and `Registered` list.
- `internal/setup/jsonhooks.go` — shared Claude/Gemini JSON-hooks merger and matcher.
- `internal/setup/jsonhooks_test.go`
- `internal/setup/claude.go` — `ClaudeConfigurator`, extracted from `setup.go`, reuses `jsonhooks.go`.
- `internal/setup/claude_test.go` — Claude-specific tests extracted from `setup_test.go`.
- `internal/setup/codex.go` — `CodexConfigurator`, extracted from `setup.go`.
- `internal/setup/codex_test.go` — Codex-specific tests extracted from `setup_test.go`.
- `internal/setup/gemini.go` — `GeminiConfigurator`, new, reuses `jsonhooks.go`.
- `internal/setup/gemini_test.go`
- `internal/setup/opencode.go` — `OpenCodeConfigurator`, verbatim file writer.
- `internal/setup/opencode_test.go`

Modified files:

- `internal/cli/emit.go` — `RunE` no longer returns an error; instead it logs via `errorlog` and returns `nil`.
- `internal/cli/emit_test.go` — updated to assert log file contents instead of exit codes.
- `internal/cli/remotesetup.go` — iterates `setup.Registered` instead of switching on `tool.Name`.
- `internal/cli/remotesetup_test.go` — covers all four tools, asserts canonical `emit-hook` commands.
- `internal/setup/setup.go` — trimmed to detection (`DetectTools`) and the `Tool`/`DetectedTool` struct; per-tool code moves out.
- `internal/setup/setup_test.go` — shared detection tests remain; per-tool tests extracted.
- `README.md` — update remote-setup section to document all four tools and the error log path.
- `docs/rough-spec.md` — update §15–17 (Codex, Claude, hooks) to reference `emit-hook` and list Gemini/OpenCode.
- `docs/superpowers/specs/2026-04-14-notifytun-design.md` — add a one-line note at the top marking `remote-setup` scope as superseded.

Canonical hook commands after this plan lands:

- Claude Stop: `notifytun emit-hook --tool claude-code --event Stop`
- Claude Notification: `notifytun emit-hook --tool claude-code --event Notification`
- Gemini AfterAgent: `notifytun emit-hook --tool gemini --event AfterAgent`
- Gemini Notification: `notifytun emit-hook --tool gemini --event Notification`
- Codex `notify`: `["notifytun", "emit-hook", "--tool", "codex", "--event", "notify"]`
- OpenCode: plugin file pipes `{"body": "..."}` to `notifytun emit-hook --tool opencode --event session.idle`

---

## Task 1: Error log helper

**Files:**

- Create: `internal/cli/errorlog.go`
- Create: `internal/cli/errorlog_test.go`

The helper opens the log with `O_APPEND|O_CREATE|O_WRONLY`, writes one line, closes. Path is `<dir(dbPath)>/notifytun-errors.log`. Format: `<RFC3339>\t<subcommand>\t<stage>: <error>\n`.

- [ ] **Step 1: Write the failing test**

`internal/cli/errorlog_test.go`:

```go
package cli

import (
	"errors"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

func TestLogHookErrorWritesLineToFileNextToDB(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "notifytun.db")

	LogHookError(dbPath, "emit-hook", "db-insert", errors.New("boom"))

	data, err := os.ReadFile(filepath.Join(dir, "notifytun-errors.log"))
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	line := strings.TrimRight(string(data), "\n")
	matched, err := regexp.MatchString(`^\d{4}-\d{2}-\d{2}T\d{2}:\d{2}:\d{2}(?:\.\d+)?Z\temit-hook\tdb-insert: boom$`, line)
	if err != nil {
		t.Fatalf("regexp: %v", err)
	}
	if !matched {
		t.Fatalf("unexpected line: %q", line)
	}
}

func TestLogHookErrorAppendsMultipleLines(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "notifytun.db")

	LogHookError(dbPath, "emit", "db-open", errors.New("first"))
	LogHookError(dbPath, "emit-hook", "parse", errors.New("second"))

	data, err := os.ReadFile(filepath.Join(dir, "notifytun-errors.log"))
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	lines := strings.Split(strings.TrimRight(string(data), "\n"), "\n")
	if len(lines) != 2 {
		t.Fatalf("expected 2 lines, got %d: %q", len(lines), string(data))
	}
	if !strings.Contains(lines[0], "\temit\tdb-open: first") {
		t.Fatalf("line 0 wrong: %q", lines[0])
	}
	if !strings.Contains(lines[1], "\temit-hook\tparse: second") {
		t.Fatalf("line 1 wrong: %q", lines[1])
	}
}

func TestLogHookErrorSilentWhenDirUnwritable(t *testing.T) {
	// If the log file cannot be created, LogHookError must not panic or
	// return anything visible — it simply gives up.
	LogHookError("/nonexistent/path/that/does/not/exist/notifytun.db", "emit", "db-open", errors.New("boom"))
}

func TestLogHookErrorIgnoresNilError(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "notifytun.db")

	LogHookError(dbPath, "emit", "db-open", nil)

	if _, err := os.Stat(filepath.Join(dir, "notifytun-errors.log")); !os.IsNotExist(err) {
		t.Fatalf("expected no log file, stat err=%v", err)
	}
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./internal/cli/ -run TestLogHookError -v`
Expected: FAIL — undefined `LogHookError`.

- [ ] **Step 3: Implement the helper**

`internal/cli/errorlog.go`:

```go
package cli

import (
	"fmt"
	"os"
	"path/filepath"
	"time"
)

// LogHookError appends a single line to the error log next to dbPath.
// All I/O failures are silently swallowed — a notifytun logging failure
// must never propagate to the caller (which is typically an agent hook).
func LogHookError(dbPath, subcommand, stage string, err error) {
	if err == nil {
		return
	}
	logPath := filepath.Join(filepath.Dir(dbPath), "notifytun-errors.log")
	f, ferr := os.OpenFile(logPath, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if ferr != nil {
		return
	}
	defer f.Close()
	_, _ = fmt.Fprintf(
		f,
		"%s\t%s\t%s: %s\n",
		time.Now().UTC().Format(time.RFC3339),
		subcommand,
		stage,
		err.Error(),
	)
}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./internal/cli/ -run TestLogHookError -v`
Expected: PASS (4/4).

- [ ] **Step 5: Commit**

```bash
git add internal/cli/errorlog.go internal/cli/errorlog_test.go
git commit -m "$(cat <<'EOF'
feat(cli): add LogHookError helper for hook-path errors

Writes a single tab-separated line to notifytun-errors.log next to the DB.
All I/O failures are swallowed because hook commands must never fail their
caller. Used by emit and emit-hook in subsequent commits.

Co-Authored-By: Claude Opus 4.7 (1M context) <noreply@anthropic.com>
EOF
)"
```

---

## Task 2: `emit` always exits 0

Change the existing `emit` command so it never returns an error. Missing title, DB open/insert failures, and socket wakeup failures all go to the error log and the command exits 0. This eliminates the `silentExit` usage and the `ExitError` assertion in existing tests.

**Files:**

- Modify: `internal/cli/emit.go`
- Modify: `internal/cli/emit_test.go`

- [ ] **Step 1: Update the existing test expectations**

Replace `TestEmitWithoutTitleReturnsSilentExit` in `internal/cli/emit_test.go` with a test that asserts the log file is written instead of an exit error. Also add a test for DB open failure. Full replacement file body:

```go
package cli_test

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/michaellee8/notifytun/internal/cli"
	"github.com/michaellee8/notifytun/internal/db"
	"github.com/michaellee8/notifytun/internal/socket"
)

func TestEmitWritesNotificationAndSignalsSocket(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "notifytun.db")
	socketPath := filepath.Join(dir, "notifytun.sock")

	listener, err := socket.Listen(socketPath)
	if err != nil {
		t.Fatalf("Listen: %v", err)
	}
	defer listener.Close()

	waitResult := make(chan error, 1)
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		waitResult <- listener.Wait(ctx)
	}()

	cmd := cli.NewEmitCmd()
	cmd.SetArgs([]string{
		"--title", "Test",
		"--body", "Hello",
		"--tool", "test",
		"--db", dbPath,
		"--socket", socketPath,
	})

	if err := cmd.Execute(); err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if err := <-waitResult; err != nil {
		t.Fatalf("expected wakeup, got %v", err)
	}

	d, err := db.Open(dbPath)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer d.Close()

	rows, err := d.QueryUndelivered()
	if err != nil {
		t.Fatalf("QueryUndelivered: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("expected 1 row, got %d", len(rows))
	}
	if rows[0].Title != "Test" || rows[0].Body != "Hello" || rows[0].Tool != "test" {
		t.Fatalf("unexpected stored notification: %+v", rows[0])
	}
}

func TestEmitDerivesCodexPayload(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "notifytun.db")

	cmd := cli.NewEmitCmd()
	cmd.SetArgs([]string{
		"--tool", "codex",
		"--db", dbPath,
		`{"type":"agent-turn-complete","input-messages":["rename foo to bar"],"last-assistant-message":"Rename complete"}`,
	})

	if err := cmd.Execute(); err != nil {
		t.Fatalf("Execute: %v", err)
	}

	d, err := db.Open(dbPath)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer d.Close()

	rows, err := d.QueryUndelivered()
	if err != nil {
		t.Fatalf("QueryUndelivered: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("expected 1 row, got %d", len(rows))
	}
	if rows[0].Title != "Task complete" || rows[0].Body != "Rename complete" || rows[0].Tool != "codex" {
		t.Fatalf("unexpected derived notification: %+v", rows[0])
	}
}

func TestEmitWithoutTitleLogsAndExitsZero(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "notifytun.db")

	cmd := cli.NewEmitCmd()
	cmd.SetArgs([]string{"--db", dbPath})

	if err := cmd.Execute(); err != nil {
		t.Fatalf("Execute returned error, want nil: %v", err)
	}

	data, err := os.ReadFile(filepath.Join(dir, "notifytun-errors.log"))
	if err != nil {
		t.Fatalf("expected error log to exist: %v", err)
	}
	if !strings.Contains(string(data), "\temit\tparse: missing notification title") {
		t.Fatalf("unexpected log contents: %q", string(data))
	}
}

func TestEmitDBOpenFailureLogsAndExitsZero(t *testing.T) {
	dir := t.TempDir()
	// Make the DB path itself a directory. DB Open's MkdirAll(filepath.Dir(path))
	// succeeds because dir exists, but opening the SQLite file fails since
	// dbPath is a directory. The log lives in `dir`, which is writable.
	dbPath := filepath.Join(dir, "db-is-a-dir")
	if err := os.Mkdir(dbPath, 0o755); err != nil {
		t.Fatalf("Mkdir: %v", err)
	}

	cmd := cli.NewEmitCmd()
	cmd.SetArgs([]string{
		"--title", "Test",
		"--db", dbPath,
	})

	if err := cmd.Execute(); err != nil {
		t.Fatalf("Execute returned error, want nil: %v", err)
	}

	logPath := filepath.Join(dir, "notifytun-errors.log")
	data, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatalf("expected error log at %s: %v", logPath, err)
	}
	if !strings.Contains(string(data), "\temit\tdb-open:") {
		t.Fatalf("unexpected log contents: %q", string(data))
	}
}
```

- [ ] **Step 2: Run tests to confirm the new expectations fail**

Run: `go test ./internal/cli/ -run TestEmit -v`
Expected: FAIL on `TestEmitWithoutTitleLogsAndExitsZero` (returns `*ExitError`) and `TestEmitDBOpenFailureLogsAndExitsZero`.

- [ ] **Step 3: Rewrite `internal/cli/emit.go` to log and exit 0**

Full replacement for `internal/cli/emit.go`:

```go
package cli

import (
	"encoding/json"
	"errors"
	"os"
	"strings"

	"github.com/michaellee8/notifytun/internal/db"
	"github.com/michaellee8/notifytun/internal/socket"
	"github.com/spf13/cobra"
)

type codexNotifyPayload struct {
	Type                 string   `json:"type"`
	InputMessages        []string `json:"input-messages"`
	LastAssistantMessage string   `json:"last-assistant-message"`
}

// NewEmitCmd records a notification from a tool hook.
// Always exits 0; errors go to notifytun-errors.log next to the DB.
func NewEmitCmd() *cobra.Command {
	var (
		title      string
		body       string
		tool       string
		dbPath     string
		socketPath string
	)

	home, _ := os.UserHomeDir()
	defaultDB := home + "/.notifytun/notifytun.db"
	defaultSocket := home + "/.notifytun/notifytun.sock"

	cmd := &cobra.Command{
		Use:           "emit [codex-notify-json]",
		Short:         "Record a notification (called by tool hooks)",
		Args:          cobra.MaximumNArgs(1),
		SilenceUsage:  true,
		SilenceErrors: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			if len(args) == 1 && title == "" {
				var payload codexNotifyPayload
				if err := json.Unmarshal([]byte(args[0]), &payload); err == nil && payload.Type == "agent-turn-complete" {
					title = "Task complete"
					body = strings.TrimSpace(payload.LastAssistantMessage)
					if body == "" {
						body = strings.TrimSpace(strings.Join(payload.InputMessages, " "))
					}
				}
			}

			if title == "" {
				LogHookError(dbPath, "emit", "parse", errors.New("missing notification title"))
				return nil
			}

			d, err := db.Open(dbPath)
			if err != nil {
				LogHookError(dbPath, "emit", "db-open", err)
				return nil
			}
			defer d.Close()

			if _, err := d.Insert(title, body, tool); err != nil {
				LogHookError(dbPath, "emit", "db-insert", err)
				return nil
			}

			_ = socket.SendWakeup(socketPath)
			return nil
		},
	}

	cmd.Flags().StringVar(&title, "title", "", "Notification title (required unless derived from Codex payload)")
	cmd.Flags().StringVar(&body, "body", "", "Notification body")
	cmd.Flags().StringVar(&tool, "tool", "", "Source tool name")
	cmd.Flags().StringVar(&dbPath, "db", defaultDB, "SQLite database path")
	cmd.Flags().StringVar(&socketPath, "socket", defaultSocket, "Unix socket path")

	return cmd
}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./internal/cli/ -run TestEmit -v`
Expected: PASS on all four emit tests.

- [ ] **Step 5: Run the full package to confirm nothing else broke**

Run: `go test ./internal/cli/`
Expected: PASS.

- [ ] **Step 6: Commit**

```bash
git add internal/cli/emit.go internal/cli/emit_test.go
git commit -m "$(cat <<'EOF'
refactor(cli): emit always exits 0, logs errors to notifytun-errors.log

Hook commands must never fail their caller. Errors from argument parsing,
DB open, and DB insert are now written one line at a time to a log file
next to the SQLite DB. Socket wakeup was already silent.

Co-Authored-By: Claude Opus 4.7 (1M context) <noreply@anthropic.com>
EOF
)"
```

---

## Task 3: `emit-hook` subcommand with full dispatch matrix

New subcommand that reads a JSON payload (from stdin or a positional arg), looks up the (tool, event) pair in a static table, extracts a body field, and writes a row. Always exits 0 via the error log.

**Files:**

- Create: `internal/cli/emithook.go`
- Create: `internal/cli/emithook_test.go`
- Modify: `cmd/notifytun/main.go` — register the new subcommand.

- [ ] **Step 1: Write the failing test file**

`internal/cli/emithook_test.go`:

```go
package cli_test

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/michaellee8/notifytun/internal/cli"
	"github.com/michaellee8/notifytun/internal/db"
)

func runEmitHook(t *testing.T, dbPath, stdin string, args ...string) {
	t.Helper()
	cmd := cli.NewEmitHookCmd()
	cmd.SetArgs(append([]string{"--db", dbPath, "--socket", filepath.Join(filepath.Dir(dbPath), "nowhere.sock")}, args...))
	cmd.SetIn(bytes.NewBufferString(stdin))
	if err := cmd.Execute(); err != nil {
		t.Fatalf("Execute: %v (always-exit-0 was violated)", err)
	}
}

func readSingleRow(t *testing.T, dbPath string) db.Notification {
	t.Helper()
	d, err := db.Open(dbPath)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer d.Close()
	rows, err := d.QueryUndelivered()
	if err != nil {
		t.Fatalf("QueryUndelivered: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("expected 1 row, got %d", len(rows))
	}
	return rows[0]
}

func TestEmitHookClaudeStop(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "notifytun.db")
	runEmitHook(t, dbPath,
		`{"last_assistant_message":"Renamed foo to bar.","hook_event_name":"Stop"}`,
		"--tool", "claude-code", "--event", "Stop")

	row := readSingleRow(t, dbPath)
	if row.Title != "Claude Code: Task complete" {
		t.Fatalf("title: %q", row.Title)
	}
	if row.Body != "Renamed foo to bar." {
		t.Fatalf("body: %q", row.Body)
	}
	if row.Tool != "claude-code" {
		t.Fatalf("tool: %q", row.Tool)
	}
}

func TestEmitHookClaudeNotification(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "notifytun.db")
	runEmitHook(t, dbPath,
		`{"message":"Claude needs your permission to use Bash"}`,
		"--tool", "claude-code", "--event", "Notification")

	row := readSingleRow(t, dbPath)
	if row.Title != "Claude Code: Needs attention" {
		t.Fatalf("title: %q", row.Title)
	}
	if row.Body != "Claude needs your permission to use Bash" {
		t.Fatalf("body: %q", row.Body)
	}
}

func TestEmitHookCodexNotifyFromPositional(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "notifytun.db")
	runEmitHook(t, dbPath, "",
		"--tool", "codex", "--event", "notify",
		`{"type":"agent-turn-complete","last-assistant-message":"Rename complete","input-messages":["rename foo to bar"]}`)

	row := readSingleRow(t, dbPath)
	if row.Title != "Codex: Task complete" {
		t.Fatalf("title: %q", row.Title)
	}
	if row.Body != "Rename complete" {
		t.Fatalf("body: %q", row.Body)
	}
	if row.Tool != "codex" {
		t.Fatalf("tool: %q", row.Tool)
	}
}

func TestEmitHookCodexNotifyFallbackToInputMessages(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "notifytun.db")
	runEmitHook(t, dbPath, "",
		"--tool", "codex", "--event", "notify",
		`{"type":"agent-turn-complete","input-messages":["rename foo","to bar"]}`)

	row := readSingleRow(t, dbPath)
	if row.Body != "rename foo to bar" {
		t.Fatalf("body: %q", row.Body)
	}
}

func TestEmitHookGeminiAfterAgent(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "notifytun.db")
	runEmitHook(t, dbPath,
		`{"prompt_response":"Analysis complete.","hook_event_name":"AfterAgent"}`,
		"--tool", "gemini", "--event", "AfterAgent")

	row := readSingleRow(t, dbPath)
	if row.Title != "Gemini CLI: Task complete" {
		t.Fatalf("title: %q", row.Title)
	}
	if row.Body != "Analysis complete." {
		t.Fatalf("body: %q", row.Body)
	}
}

func TestEmitHookGeminiNotification(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "notifytun.db")
	runEmitHook(t, dbPath,
		`{"message":"Gemini is awaiting input"}`,
		"--tool", "gemini", "--event", "Notification")

	row := readSingleRow(t, dbPath)
	if row.Title != "Gemini CLI: Needs attention" {
		t.Fatalf("title: %q", row.Title)
	}
	if row.Body != "Gemini is awaiting input" {
		t.Fatalf("body: %q", row.Body)
	}
}

func TestEmitHookOpenCodeSessionIdle(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "notifytun.db")
	runEmitHook(t, dbPath,
		`{"body":"Plugin-supplied summary."}`,
		"--tool", "opencode", "--event", "session.idle")

	row := readSingleRow(t, dbPath)
	if row.Title != "OpenCode: Task complete" {
		t.Fatalf("title: %q", row.Title)
	}
	if row.Body != "Plugin-supplied summary." {
		t.Fatalf("body: %q", row.Body)
	}
	if row.Tool != "opencode" {
		t.Fatalf("tool: %q", row.Tool)
	}
}

func TestEmitHookTruncatesBodyTo180Runes(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "notifytun.db")
	long := strings.Repeat("x", 500)
	runEmitHook(t, dbPath,
		`{"last_assistant_message":"`+long+`"}`,
		"--tool", "claude-code", "--event", "Stop")

	row := readSingleRow(t, dbPath)
	if got := len([]rune(row.Body)); got != 180 {
		t.Fatalf("expected body truncated to 180 runes, got %d", got)
	}
}

func TestEmitHookEmptyPayloadProducesTitleOnlyRow(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "notifytun.db")
	runEmitHook(t, dbPath, "", "--tool", "claude-code", "--event", "Stop")

	row := readSingleRow(t, dbPath)
	if row.Title != "Claude Code: Task complete" {
		t.Fatalf("title: %q", row.Title)
	}
	if row.Body != "" {
		t.Fatalf("expected empty body, got %q", row.Body)
	}

	if _, err := os.Stat(filepath.Join(dir, "notifytun-errors.log")); !os.IsNotExist(err) {
		t.Fatalf("expected no error log for empty payload, stat err=%v", err)
	}
}

func TestEmitHookMalformedJSONProducesTitleOnlyRowAndLogsParseError(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "notifytun.db")
	runEmitHook(t, dbPath, `not-json`, "--tool", "claude-code", "--event", "Stop")

	row := readSingleRow(t, dbPath)
	if row.Title != "Claude Code: Task complete" {
		t.Fatalf("title: %q", row.Title)
	}
	if row.Body != "" {
		t.Fatalf("expected empty body, got %q", row.Body)
	}

	logData, err := os.ReadFile(filepath.Join(dir, "notifytun-errors.log"))
	if err != nil {
		t.Fatalf("expected error log: %v", err)
	}
	if !strings.Contains(string(logData), "\temit-hook\tparse:") {
		t.Fatalf("expected parse stage line, got %q", string(logData))
	}
}

func TestEmitHookUnknownToolLogsDispatchNoRow(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "notifytun.db")
	runEmitHook(t, dbPath, `{}`, "--tool", "unknown", "--event", "Stop")

	d, err := db.Open(dbPath)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer d.Close()
	rows, err := d.QueryUndelivered()
	if err != nil {
		t.Fatalf("QueryUndelivered: %v", err)
	}
	if len(rows) != 0 {
		t.Fatalf("expected no rows for unknown tool, got %d", len(rows))
	}

	logData, err := os.ReadFile(filepath.Join(dir, "notifytun-errors.log"))
	if err != nil {
		t.Fatalf("expected error log: %v", err)
	}
	if !strings.Contains(string(logData), "\temit-hook\tdispatch:") {
		t.Fatalf("expected dispatch stage line, got %q", string(logData))
	}
}

func TestEmitHookDBFailureLogsAndExitsZero(t *testing.T) {
	dir := t.TempDir()
	// Make the DB path itself a directory; the log dir (= `dir`) stays writable.
	dbPath := filepath.Join(dir, "db-is-a-dir")
	if err := os.Mkdir(dbPath, 0o755); err != nil {
		t.Fatalf("Mkdir: %v", err)
	}

	cmd := cli.NewEmitHookCmd()
	cmd.SetArgs([]string{"--db", dbPath, "--tool", "claude-code", "--event", "Stop"})
	cmd.SetIn(strings.NewReader(`{"last_assistant_message":"x"}`))
	if err := cmd.Execute(); err != nil {
		t.Fatalf("Execute: %v", err)
	}

	logPath := filepath.Join(dir, "notifytun-errors.log")
	data, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatalf("expected error log at %s: %v", logPath, err)
	}
	if !strings.Contains(string(data), "\temit-hook\tdb-open:") {
		t.Fatalf("unexpected log contents: %q", string(data))
	}
}
```

- [ ] **Step 2: Run tests to confirm they fail**

Run: `go test ./internal/cli/ -run TestEmitHook -v`
Expected: FAIL — `NewEmitHookCmd` is undefined.

- [ ] **Step 3: Implement `emit-hook`**

`internal/cli/emithook.go`:

```go
package cli

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/michaellee8/notifytun/internal/db"
	"github.com/michaellee8/notifytun/internal/socket"
	"github.com/spf13/cobra"
)

// hookDispatch describes how to turn a payload into a notification for one (tool, event) pair.
type hookDispatch struct {
	toolDisplayName string
	titleSuffix     string
	// extractBody receives the unmarshaled payload and returns the body string.
	// Returning "" means "no body" — title-only notification. Not an error.
	extractBody func(map[string]any) string
}

var hookTable = map[string]map[string]hookDispatch{
	"claude-code": {
		"Stop": {
			toolDisplayName: "Claude Code",
			titleSuffix:     "Task complete",
			extractBody:     extractStringField("last_assistant_message"),
		},
		"Notification": {
			toolDisplayName: "Claude Code",
			titleSuffix:     "Needs attention",
			extractBody:     extractStringField("message"),
		},
	},
	"gemini": {
		"AfterAgent": {
			toolDisplayName: "Gemini CLI",
			titleSuffix:     "Task complete",
			extractBody:     extractStringField("prompt_response"),
		},
		"Notification": {
			toolDisplayName: "Gemini CLI",
			titleSuffix:     "Needs attention",
			extractBody:     extractStringField("message"),
		},
	},
	"codex": {
		"notify": {
			toolDisplayName: "Codex",
			titleSuffix:     "Task complete",
			extractBody:     extractCodexBody,
		},
	},
	"opencode": {
		"session.idle": {
			toolDisplayName: "OpenCode",
			titleSuffix:     "Task complete",
			extractBody:     extractStringField("body"),
		},
	},
}

func extractStringField(field string) func(map[string]any) string {
	return func(payload map[string]any) string {
		if payload == nil {
			return ""
		}
		v, _ := payload[field].(string)
		return strings.TrimSpace(v)
	}
}

func extractCodexBody(payload map[string]any) string {
	if payload == nil {
		return ""
	}
	if s, _ := payload["last-assistant-message"].(string); strings.TrimSpace(s) != "" {
		return strings.TrimSpace(s)
	}
	raw, ok := payload["input-messages"].([]any)
	if !ok {
		return ""
	}
	parts := make([]string, 0, len(raw))
	for _, item := range raw {
		if s, ok := item.(string); ok {
			parts = append(parts, s)
		}
	}
	return strings.TrimSpace(strings.Join(parts, " "))
}

// NewEmitHookCmd records a notification derived from an agent hook payload.
// Always exits 0; errors go to notifytun-errors.log next to the DB.
func NewEmitHookCmd() *cobra.Command {
	var (
		tool       string
		event      string
		dbPath     string
		socketPath string
	)

	home, _ := os.UserHomeDir()
	defaultDB := home + "/.notifytun/notifytun.db"
	defaultSocket := home + "/.notifytun/notifytun.sock"

	cmd := &cobra.Command{
		Use:           "emit-hook [payload-json]",
		Short:         "Record a notification derived from an agent hook payload",
		Args:          cobra.MaximumNArgs(1),
		SilenceUsage:  true,
		SilenceErrors: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			dispatch, ok := lookupDispatch(tool, event)
			if !ok {
				LogHookError(dbPath, "emit-hook", "dispatch",
					fmt.Errorf("unknown tool/event: %s/%s", tool, event))
				return nil
			}

			payloadBytes, err := readPayload(args, cmd.InOrStdin())
			if err != nil {
				LogHookError(dbPath, "emit-hook", "parse", err)
			}

			var payload map[string]any
			if len(payloadBytes) > 0 {
				if err := json.Unmarshal(payloadBytes, &payload); err != nil {
					LogHookError(dbPath, "emit-hook", "parse", err)
					payload = nil
				}
			}

			title := dispatch.toolDisplayName + ": " + dispatch.titleSuffix
			body := truncateRunes(dispatch.extractBody(payload), 180)

			d, err := db.Open(dbPath)
			if err != nil {
				LogHookError(dbPath, "emit-hook", "db-open", err)
				return nil
			}
			defer d.Close()

			if _, err := d.Insert(title, body, tool); err != nil {
				LogHookError(dbPath, "emit-hook", "db-insert", err)
				return nil
			}

			_ = socket.SendWakeup(socketPath)
			return nil
		},
	}

	cmd.Flags().StringVar(&tool, "tool", "", "Source tool name (claude-code|gemini|codex|opencode)")
	cmd.Flags().StringVar(&event, "event", "", "Hook event name (Stop|Notification|AfterAgent|notify|session.idle)")
	cmd.Flags().StringVar(&dbPath, "db", defaultDB, "SQLite database path")
	cmd.Flags().StringVar(&socketPath, "socket", defaultSocket, "Unix socket path")

	return cmd
}

func lookupDispatch(tool, event string) (hookDispatch, bool) {
	byEvent, ok := hookTable[tool]
	if !ok {
		return hookDispatch{}, false
	}
	d, ok := byEvent[event]
	return d, ok
}

func readPayload(args []string, stdin io.Reader) ([]byte, error) {
	if len(args) == 1 {
		return []byte(args[0]), nil
	}
	if stdin == nil {
		return nil, nil
	}
	data, err := io.ReadAll(stdin)
	if err != nil {
		return nil, fmt.Errorf("read stdin: %w", err)
	}
	return data, nil
}

func truncateRunes(s string, max int) string {
	runes := []rune(s)
	if len(runes) <= max {
		return s
	}
	return string(runes[:max])
}

```

- [ ] **Step 4: Register the subcommand in the root command**

Edit `cmd/notifytun/main.go`. Insert one line inside `init()` immediately after the existing `rootCmd.AddCommand(cli.NewEmitCmd())` line:

```go
func init() {
	rootCmd.AddCommand(cli.NewEmitCmd())
	rootCmd.AddCommand(cli.NewEmitHookCmd())
	rootCmd.AddCommand(cli.NewAttachCmd())
	rootCmd.AddCommand(cli.NewLocalCmd())
	rootCmd.AddCommand(cli.NewRemoteSetupCmd())
	rootCmd.AddCommand(cli.NewTestNotifyCmd())
}
```

Only this one line is added; the other lines stay as-is.

- [ ] **Step 5: Run tests to verify they pass**

Run: `go test ./internal/cli/ -run TestEmitHook -v`
Expected: PASS (12/12).

- [ ] **Step 6: Build to verify main wires up**

Run: `go build ./...`
Expected: no output (success).

- [ ] **Step 7: Commit**

```bash
git add internal/cli/emithook.go internal/cli/emithook_test.go cmd/notifytun/main.go
git commit -m "$(cat <<'EOF'
feat(cli): add emit-hook subcommand with (tool, event) dispatch table

Reads hook payload JSON from a positional arg (Codex) or stdin (everyone
else), extracts a body field per (tool, event), and writes one row with a
'<Tool>: <suffix>' title. Always exits 0 — any failure logs one line to
notifytun-errors.log and returns nil.

Covers: claude-code/Stop, claude-code/Notification, codex/notify,
gemini/AfterAgent, gemini/Notification, opencode/session.idle.

Co-Authored-By: Claude Opus 4.7 (1M context) <noreply@anthropic.com>
EOF
)"
```

---

## Task 4: `Configurator` interface + extract Claude/Codex

Pure refactor with no behavior change. Introduce `Configurator` and extract Claude and Codex into their own files implementing it. `DetectTools` now iterates the registry. Existing tests (including `remotesetup_test.go`) must still pass unchanged.

**Files:**

- Create: `internal/setup/configurator.go`
- Create: `internal/setup/claude.go`
- Create: `internal/setup/claude_test.go`
- Create: `internal/setup/codex.go`
- Create: `internal/setup/codex_test.go`
- Modify: `internal/setup/setup.go` (trim per-tool code; keep `Tool` struct and public helpers so `remotesetup.go` compiles unchanged this task)
- Modify: `internal/setup/setup_test.go` (keep detection tests; remove per-tool tests that moved)

Key decision: **preserve the existing legacy command strings** in this task. `ClaudeConfigurator.Apply` still writes `notifytun emit --tool claude-code --title 'Task complete'`. Migration to `emit-hook` happens in Task 6. That keeps this a pure refactor and prevents a cascade of broken tests.

- [ ] **Step 1: Create `configurator.go`**

`internal/setup/configurator.go`:

```go
package setup

// Configurator knows how to detect, describe, and install a single tool's
// notifytun integration.
type Configurator interface {
	// Name is the human-readable tool name shown in previews ("Claude Code").
	Name() string
	// Binaries lists names to probe on PATH. First hit wins.
	Binaries() []string
	// ConfigPath returns the absolute path of the file this configurator
	// reads/writes, derived from the user's home directory.
	ConfigPath(home string) string
	// IsConfigured reports whether the canonical notifytun integration is
	// already present at ConfigPath(home).
	IsConfigured(home string) bool
	// PreviewAction returns a one-line description of what Apply would do.
	// Used for the dry-run preview and the pre-apply prompt.
	PreviewAction(home string) string
	// Apply writes the canonical notifytun integration, merging with any
	// existing unrelated configuration. Idempotent.
	Apply(home string) error
}

// Registered lists all configurators in preview order.
var Registered = []Configurator{
	&ClaudeConfigurator{},
	&CodexConfigurator{},
}
```

Gemini and OpenCode are appended to `Registered` in later tasks.

- [ ] **Step 2: Create `claude.go` by extracting from `setup.go`**

`internal/setup/claude.go`:

```go
package setup

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
)

const (
	claudeStopCommand         = "notifytun emit --tool claude-code --title 'Task complete'"
	claudeNotificationCommand = "notifytun emit --tool claude-code --title 'Needs attention'"
)

// ClaudeConfigurator manages ~/.claude/settings.json hooks.
type ClaudeConfigurator struct{}

func (*ClaudeConfigurator) Name() string           { return "Claude Code" }
func (*ClaudeConfigurator) Binaries() []string     { return []string{"claude", "claude-code"} }
func (*ClaudeConfigurator) ConfigPath(home string) string {
	return filepath.Join(home, ".claude", "settings.json")
}
func (*ClaudeConfigurator) IsConfigured(home string) bool {
	return IsClaudeConfigured((&ClaudeConfigurator{}).ConfigPath(home))
}
func (*ClaudeConfigurator) PreviewAction(home string) string {
	return "will add Stop + Notification hooks to ~/.claude/settings.json"
}
func (c *ClaudeConfigurator) Apply(home string) error {
	return ApplyClaudeHook(c.ConfigPath(home))
}

// IsClaudeConfigured reports whether both notifytun Claude hooks are already present.
func IsClaudeConfigured(settingsPath string) bool {
	data, err := os.ReadFile(settingsPath)
	if err != nil {
		return false
	}

	var settings map[string]any
	if err := json.Unmarshal(data, &settings); err != nil {
		return false
	}

	hooks, ok := settings["hooks"].(map[string]any)
	if !ok {
		return false
	}

	stopEntries, ok := hooks["Stop"].([]any)
	if !ok {
		return false
	}
	notificationEntries, ok := hooks["Notification"].([]any)
	if !ok {
		return false
	}

	return hasHookCommand(stopEntries, claudeStopCommand) &&
		hasHookCommand(notificationEntries, claudeNotificationCommand)
}

// ApplyClaudeHook merges notifytun Claude hooks into the given settings file.
func ApplyClaudeHook(settingsPath string) error {
	if IsClaudeConfigured(settingsPath) {
		return nil
	}

	settings, err := readSettings(settingsPath)
	if err != nil {
		return err
	}

	hooks, err := mapValue(settings["hooks"], "hooks")
	if err != nil {
		return err
	}

	stopEntries, err := sliceValue(hooks["Stop"], "hooks.Stop")
	if err != nil {
		return err
	}
	notificationEntries, err := sliceValue(hooks["Notification"], "hooks.Notification")
	if err != nil {
		return err
	}

	if !hasHookCommand(stopEntries, claudeStopCommand) {
		stopEntries = append(stopEntries, newClaudeEntry(claudeStopCommand))
	}
	if !hasHookCommand(notificationEntries, claudeNotificationCommand) {
		notificationEntries = append(notificationEntries, newClaudeEntry(claudeNotificationCommand))
	}

	hooks["Stop"] = stopEntries
	hooks["Notification"] = notificationEntries
	settings["hooks"] = hooks

	if err := os.MkdirAll(filepath.Dir(settingsPath), 0o755); err != nil {
		return fmt.Errorf("create Claude settings dir: %w", err)
	}

	data, err := json.MarshalIndent(settings, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal Claude settings: %w", err)
	}
	data = append(data, '\n')

	if err := os.WriteFile(settingsPath, data, 0o644); err != nil {
		return fmt.Errorf("write Claude settings: %w", err)
	}
	return nil
}

// GenerateClaudeHook returns the JSON snippet notifytun writes into Claude settings.
// Retained for test compatibility.
func GenerateClaudeHook() string {
	return `{
  "hooks": {
    "Stop": [
      {
        "matcher": "",
        "hooks": [
          {
            "type": "command",
            "command": "notifytun emit --tool claude-code --title 'Task complete'"
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
            "command": "notifytun emit --tool claude-code --title 'Needs attention'"
          }
        ]
      }
    ]
  }
}`
}

func readSettings(settingsPath string) (map[string]any, error) {
	data, err := os.ReadFile(settingsPath)
	if err != nil {
		if os.IsNotExist(err) {
			return map[string]any{}, nil
		}
		return nil, fmt.Errorf("read Claude settings: %w", err)
	}

	var settings map[string]any
	if err := json.Unmarshal(data, &settings); err != nil {
		return nil, fmt.Errorf("parse Claude settings: %w", err)
	}
	if settings == nil {
		settings = map[string]any{}
	}
	return settings, nil
}

func mapValue(value any, field string) (map[string]any, error) {
	if value == nil {
		return map[string]any{}, nil
	}
	m, ok := value.(map[string]any)
	if !ok {
		return nil, fmt.Errorf("unexpected %s format: want object", field)
	}
	return m, nil
}

func sliceValue(value any, field string) ([]any, error) {
	if value == nil {
		return nil, nil
	}
	s, ok := value.([]any)
	if !ok {
		return nil, fmt.Errorf("unexpected %s format: want array", field)
	}
	return s, nil
}

func newClaudeEntry(command string) map[string]any {
	return map[string]any{
		"matcher": "",
		"hooks": []any{
			map[string]any{
				"type":    "command",
				"command": command,
			},
		},
	}
}

func hasHookCommand(entries []any, want string) bool {
	for _, entry := range entries {
		entryMap, ok := entry.(map[string]any)
		if !ok {
			continue
		}
		hooks, ok := entryMap["hooks"].([]any)
		if !ok {
			continue
		}
		for _, hook := range hooks {
			hookMap, ok := hook.(map[string]any)
			if !ok {
				continue
			}
			if command, _ := hookMap["command"].(string); command == want {
				return true
			}
		}
	}
	return false
}
```

- [ ] **Step 3: Create `codex.go` by extracting from `setup.go`**

`internal/setup/codex.go`:

```go
package setup

import (
	"fmt"
	"os"
	"path/filepath"

	toml "github.com/pelletier/go-toml/v2"
)

const codexNotifyConfigLine = `notify = ["notifytun", "emit", "--tool", "codex"]`

var codexNotifyCommand = []string{"notifytun", "emit", "--tool", "codex"}

// CodexConfigurator manages ~/.codex/config.toml notify.
type CodexConfigurator struct{}

func (*CodexConfigurator) Name() string           { return "Codex CLI" }
func (*CodexConfigurator) Binaries() []string     { return []string{"codex"} }
func (*CodexConfigurator) ConfigPath(home string) string {
	return filepath.Join(home, ".codex", "config.toml")
}
func (*CodexConfigurator) IsConfigured(home string) bool {
	return IsCodexConfigured((&CodexConfigurator{}).ConfigPath(home))
}
func (*CodexConfigurator) PreviewAction(home string) string {
	return "will set notify in ~/.codex/config.toml"
}
func (c *CodexConfigurator) Apply(home string) error {
	return ApplyCodexNotifyConfig(c.ConfigPath(home))
}

// GenerateCodexNotifyConfig returns the notify config line for Codex CLI.
func GenerateCodexNotifyConfig() string {
	return codexNotifyConfigLine + "\n"
}

// IsCodexConfigured reports whether the notifytun notify hook is already present.
func IsCodexConfigured(configPath string) bool {
	cfg, err := readCodexConfig(configPath)
	if err != nil {
		return false
	}
	return codexNotifyConfigured(cfg)
}

// ApplyCodexNotifyConfig writes the notifytun notify config at the TOML root.
func ApplyCodexNotifyConfig(configPath string) error {
	cfg, err := readCodexConfig(configPath)
	if err != nil {
		if os.IsNotExist(err) {
			cfg = map[string]any{}
		} else {
			return err
		}
	}

	if codexNotifyConfigured(cfg) {
		return nil
	}
	cfg["notify"] = append([]string(nil), codexNotifyCommand...)

	if err := os.MkdirAll(filepath.Dir(configPath), 0o755); err != nil {
		return fmt.Errorf("create Codex config dir: %w", err)
	}

	updated, err := toml.Marshal(cfg)
	if err != nil {
		return fmt.Errorf("marshal Codex config: %w", err)
	}
	if err := os.WriteFile(configPath, []byte(updated), 0o644); err != nil {
		return fmt.Errorf("write Codex config: %w", err)
	}
	return nil
}

func readCodexConfig(configPath string) (map[string]any, error) {
	data, err := os.ReadFile(configPath)
	if err != nil {
		return nil, err
	}

	var cfg map[string]any
	if err := toml.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("parse Codex config: %w", err)
	}
	if cfg == nil {
		cfg = map[string]any{}
	}
	return cfg, nil
}

func codexNotifyConfigured(cfg map[string]any) bool {
	raw, ok := cfg["notify"]
	if !ok {
		return false
	}

	notifyArgs, ok := stringSlice(raw)
	if !ok {
		return false
	}
	return equalStringSlices(notifyArgs, codexNotifyCommand)
}

func stringSlice(value any) ([]string, bool) {
	switch typed := value.(type) {
	case []string:
		return append([]string(nil), typed...), true
	case []any:
		out := make([]string, 0, len(typed))
		for _, item := range typed {
			s, ok := item.(string)
			if !ok {
				return nil, false
			}
			out = append(out, s)
		}
		return out, true
	default:
		return nil, false
	}
}

func equalStringSlices(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
```

- [ ] **Step 4: Trim `setup.go` to detection + shared struct**

`internal/setup/setup.go` after this task:

```go
package setup

import (
	"fmt"
	"os/exec"
	"path/filepath"
	"strings"
)

// Tool represents a detected AI coding tool and whether notifytun can configure it.
type Tool struct {
	Name       string
	Binary     string
	Detected   bool
	Configured bool
	Supported  bool // true once a Configurator is registered for it
	Cfg        Configurator
}

// DetectTools scans the provided path list or the current PATH when pathEnv is empty.
// Returns one Tool per Registered configurator whose binary is found on PATH.
func DetectTools(pathEnv string) []Tool {
	var tools []Tool

	for _, cfg := range Registered {
		tool := Tool{
			Name:      cfg.Name(),
			Supported: true,
			Cfg:       cfg,
		}

		for _, binary := range cfg.Binaries() {
			if path := lookPath(binary, pathEnv); path != "" {
				tool.Binary = path
				tool.Detected = true
				break
			}
		}

		if tool.Detected {
			tools = append(tools, tool)
		}
	}

	return tools
}

func lookPath(binary, pathEnv string) string {
	if pathEnv == "" {
		path, _ := exec.LookPath(binary)
		return path
	}

	for _, dir := range filepath.SplitList(pathEnv) {
		if dir == "" {
			continue
		}
		candidate := filepath.Join(dir, binary)
		if path, err := exec.LookPath(candidate); err == nil {
			return path
		}
	}

	return ""
}

// Preview summarizes what remote-setup would do for detected tools.
func Preview(tools []Tool) string {
	var sb strings.Builder
	sb.WriteString("Detected tools:\n")
	for _, tool := range tools {
		switch {
		case tool.Configured:
			sb.WriteString(fmt.Sprintf("  * %s -- already configured\n", tool.Name))
		case tool.Cfg != nil:
			sb.WriteString(fmt.Sprintf("  * %s -- %s\n", tool.Name, tool.Cfg.PreviewAction("")))
		default:
			sb.WriteString(fmt.Sprintf("  * %s -- detected but hook setup not supported in v1\n", tool.Name))
		}
	}
	return sb.String()
}
```

Note `Supported` stays true for any tool with a registered `Configurator`. Because we are only registering Claude and Codex in this task, Gemini/OpenCode are still omitted from `Registered`, so their `TestRemoteSetupDryRunPrintsPreview`-style detection expectations must be temporarily adapted. But current detection tests in `setup_test.go` write fake binaries then call `DetectTools` and assert Gemini/OpenCode come back with `Supported == false`. Under this refactor, those tools would not be in `Registered` yet, so they would not be returned at all — test expectations change.

To avoid breaking that test, temporarily add `gemini` and `opencode` as "placeholder" entries in `Registered` that implement the interface as "always-unconfigured, unsupported preview". Simpler alternative: preserve the old detection behavior by listing all four tools in a separate `knownTools` slice used only for detection while `Registered` holds only the ones we know how to configure. Use this simpler alternative — `DetectTools` walks the union; `Tool.Cfg` is nil for unsupported tools; `Tool.Supported` is false for them.

Revised `DetectTools` (replace the body from above):

```go
var knownTools = []struct {
	Name     string
	Binaries []string
}{
	{Name: "Claude Code", Binaries: []string{"claude", "claude-code"}},
	{Name: "Codex CLI", Binaries: []string{"codex"}},
	{Name: "Gemini CLI", Binaries: []string{"gemini"}},
	{Name: "OpenCode", Binaries: []string{"opencode"}},
}

func DetectTools(pathEnv string) []Tool {
	configurators := map[string]Configurator{}
	for _, cfg := range Registered {
		configurators[cfg.Name()] = cfg
	}

	var tools []Tool
	for _, known := range knownTools {
		tool := Tool{Name: known.Name}
		for _, binary := range known.Binaries {
			if path := lookPath(binary, pathEnv); path != "" {
				tool.Binary = path
				tool.Detected = true
				break
			}
		}
		if !tool.Detected {
			continue
		}
		if cfg, ok := configurators[known.Name]; ok {
			tool.Cfg = cfg
			tool.Supported = true
		}
		tools = append(tools, tool)
	}
	return tools
}
```

Subsequent tasks grow `Registered` (Gemini task appends, OpenCode task appends) and can drop the `knownTools` list once both are registered. Task 10 handles that cleanup.

- [ ] **Step 5: Move per-tool tests out of `setup_test.go`**

Read the current `internal/setup/setup_test.go` (already seen above). Split it:

- `internal/setup/claude_test.go` takes `TestClaudeHookGeneration`, `TestClaudeHookIdempotent`, `TestDetectAlreadyConfigured`, `TestApplyClaudeHookPreservesExistingStopHooks`.
- `internal/setup/codex_test.go` takes `TestCodexNotifyGeneration`, `TestCodexNotifyIdempotent`, `TestIsCodexConfiguredIgnoresTableScopedNotify`, `TestApplyCodexNotifyConfigInsertsRootNotifyBeforeFirstTable`, `TestApplyCodexNotifyConfigReplacesExistingRootNotify`, `TestIsCodexConfiguredAcceptsMultilineRootNotify`, `TestApplyCodexNotifyConfigReplacesMultilineRootNotify`, plus the `decodeTOML`/`rootNotifyValue`/`equalStrings` helpers.
- `internal/setup/setup_test.go` keeps `TestDetectToolsFullMatrix`, `TestDetectToolsClaudeCodeAlias`, `TestDetectToolsInjectedPathRequiresExecutable`. Update the full-matrix test's Gemini/OpenCode assertion: they should return with `Supported: false` and `Cfg: nil` as before.

Each file keeps `package setup_test` and imports. No test logic changes — this is physical relocation plus the one `Supported == false` assertion sanity-check.

- [ ] **Step 6: Run all tests**

Run: `go test ./...`
Expected: PASS — behavior is unchanged.

- [ ] **Step 7: Commit**

```bash
git add internal/setup/configurator.go internal/setup/claude.go internal/setup/claude_test.go internal/setup/codex.go internal/setup/codex_test.go internal/setup/setup.go internal/setup/setup_test.go
git commit -m "$(cat <<'EOF'
refactor(setup): extract Claude and Codex into Configurator implementations

Introduces the Configurator interface and a Registered slice. DetectTools
now walks a knownTools list and decorates each detected tool with its
configurator (if any). Claude/Codex logic (and their tests) move to their
own files. No behavior change — all existing tests pass unchanged.

Co-Authored-By: Claude Opus 4.7 (1M context) <noreply@anthropic.com>
EOF
)"
```

---

## Task 5: Shared JSON hooks helper

Extract the Claude-specific JSON merger into `jsonhooks.go` so Gemini (Task 7) can reuse it without copy-paste. Still no observable behavior change.

**Files:**

- Create: `internal/setup/jsonhooks.go`
- Create: `internal/setup/jsonhooks_test.go`
- Modify: `internal/setup/claude.go` (call into the helper)

- [ ] **Step 1: Write the failing helper test**

`internal/setup/jsonhooks_test.go`:

```go
package setup

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestApplyJSONHooksCreatesFreshFile(t *testing.T) {
	dir := t.TempDir()
	settingsPath := filepath.Join(dir, "settings.json")

	events := []JSONHookEvent{
		{Event: "Stop", Command: "notifytun emit-hook --tool claude-code --event Stop"},
		{Event: "Notification", Command: "notifytun emit-hook --tool claude-code --event Notification"},
	}

	if err := ApplyJSONHooks(settingsPath, events, nil); err != nil {
		t.Fatalf("ApplyJSONHooks: %v", err)
	}

	data, err := os.ReadFile(settingsPath)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	for _, ev := range events {
		if !strings.Contains(string(data), ev.Command) {
			t.Fatalf("expected command %q in settings, got %q", ev.Command, string(data))
		}
	}
}

func TestApplyJSONHooksRemovesLegacyByPrefix(t *testing.T) {
	dir := t.TempDir()
	settingsPath := filepath.Join(dir, "settings.json")

	if err := os.WriteFile(settingsPath, []byte(`{
  "hooks": {
    "Stop": [
      {"matcher":"","hooks":[{"type":"command","command":"notifytun emit --tool claude-code --title 'Task complete'"}]},
      {"matcher":"","hooks":[{"type":"command","command":"echo unrelated"}]}
    ]
  }
}`), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	events := []JSONHookEvent{
		{Event: "Stop", Command: "notifytun emit-hook --tool claude-code --event Stop"},
	}
	prefixes := []string{"notifytun emit ", "notifytun emit-hook "}

	if err := ApplyJSONHooks(settingsPath, events, prefixes); err != nil {
		t.Fatalf("ApplyJSONHooks: %v", err)
	}

	data, err := os.ReadFile(settingsPath)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	content := string(data)
	if strings.Contains(content, "'Task complete'") {
		t.Fatalf("expected legacy notifytun emit entry to be removed, got %q", content)
	}
	if !strings.Contains(content, "echo unrelated") {
		t.Fatalf("expected unrelated hook to be preserved, got %q", content)
	}
	if strings.Count(content, "notifytun emit-hook --tool claude-code --event Stop") != 1 {
		t.Fatalf("expected exactly one canonical hook, got %q", content)
	}
}

func TestApplyJSONHooksIdempotent(t *testing.T) {
	dir := t.TempDir()
	settingsPath := filepath.Join(dir, "settings.json")

	events := []JSONHookEvent{
		{Event: "AfterAgent", Command: "notifytun emit-hook --tool gemini --event AfterAgent"},
	}

	if err := ApplyJSONHooks(settingsPath, events, []string{"notifytun emit-hook "}); err != nil {
		t.Fatalf("first: %v", err)
	}
	first, _ := os.ReadFile(settingsPath)
	if err := ApplyJSONHooks(settingsPath, events, []string{"notifytun emit-hook "}); err != nil {
		t.Fatalf("second: %v", err)
	}
	second, _ := os.ReadFile(settingsPath)
	if string(first) != string(second) {
		t.Fatalf("second apply changed the file:\nfirst=%q\nsecond=%q", first, second)
	}
}

func TestJSONHooksConfigured(t *testing.T) {
	dir := t.TempDir()
	settingsPath := filepath.Join(dir, "settings.json")

	events := []JSONHookEvent{
		{Event: "Stop", Command: "notifytun emit-hook --tool claude-code --event Stop"},
	}

	if JSONHooksConfigured(settingsPath, events) {
		t.Fatal("empty file should not be configured")
	}
	if err := ApplyJSONHooks(settingsPath, events, nil); err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if !JSONHooksConfigured(settingsPath, events) {
		t.Fatal("expected configured after apply")
	}
}
```

- [ ] **Step 2: Run tests to confirm they fail**

Run: `go test ./internal/setup/ -run TestApplyJSONHooks -v -count=1`
Expected: FAIL — `JSONHookEvent` / `ApplyJSONHooks` / `JSONHooksConfigured` undefined.

- [ ] **Step 3: Implement `jsonhooks.go`**

`internal/setup/jsonhooks.go`:

```go
package setup

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// JSONHookEvent describes one hook entry to install at hooks.<Event>.
// Matcher is always "" for notifytun — we hook every occurrence.
type JSONHookEvent struct {
	Event   string
	Command string
}

// JSONHooksConfigured reports whether every event's canonical command is
// already present at settingsPath (and no legacy entries would be removed).
func JSONHooksConfigured(settingsPath string, events []JSONHookEvent) bool {
	data, err := os.ReadFile(settingsPath)
	if err != nil {
		return false
	}
	var settings map[string]any
	if err := json.Unmarshal(data, &settings); err != nil {
		return false
	}
	hooks, ok := settings["hooks"].(map[string]any)
	if !ok {
		return false
	}
	for _, ev := range events {
		entries, ok := hooks[ev.Event].([]any)
		if !ok {
			return false
		}
		if !hasHookCommand(entries, ev.Command) {
			return false
		}
	}
	return true
}

// ApplyJSONHooks installs every event's canonical command and removes any
// existing entry whose command (after trimming leading whitespace) has one
// of stripPrefixes as a prefix. Non-matching entries are preserved.
func ApplyJSONHooks(settingsPath string, events []JSONHookEvent, stripPrefixes []string) error {
	settings, err := readSettings(settingsPath)
	if err != nil {
		return err
	}
	hooks, err := mapValue(settings["hooks"], "hooks")
	if err != nil {
		return err
	}

	for _, ev := range events {
		entries, err := sliceValue(hooks[ev.Event], "hooks."+ev.Event)
		if err != nil {
			return err
		}
		entries = removeEntriesByCommandPrefix(entries, stripPrefixes)
		if !hasHookCommand(entries, ev.Command) {
			entries = append(entries, newClaudeEntry(ev.Command))
		}
		hooks[ev.Event] = entries
	}
	settings["hooks"] = hooks

	if err := os.MkdirAll(filepath.Dir(settingsPath), 0o755); err != nil {
		return fmt.Errorf("create settings dir: %w", err)
	}
	data, err := json.MarshalIndent(settings, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal settings: %w", err)
	}
	data = append(data, '\n')
	if err := os.WriteFile(settingsPath, data, 0o644); err != nil {
		return fmt.Errorf("write settings: %w", err)
	}
	return nil
}

func removeEntriesByCommandPrefix(entries []any, prefixes []string) []any {
	if len(prefixes) == 0 {
		return entries
	}
	out := entries[:0:0]
	for _, entry := range entries {
		if entryHasCommandWithPrefix(entry, prefixes) {
			continue
		}
		out = append(out, entry)
	}
	return out
}

func entryHasCommandWithPrefix(entry any, prefixes []string) bool {
	entryMap, ok := entry.(map[string]any)
	if !ok {
		return false
	}
	hooks, ok := entryMap["hooks"].([]any)
	if !ok || len(hooks) == 0 {
		return false
	}
	for _, hook := range hooks {
		hookMap, ok := hook.(map[string]any)
		if !ok {
			continue
		}
		cmd, _ := hookMap["command"].(string)
		cmd = strings.TrimLeft(cmd, " \t")
		for _, p := range prefixes {
			if strings.HasPrefix(cmd, p) {
				return true
			}
		}
	}
	return false
}
```

- [ ] **Step 4: Run helper tests to verify pass**

Run: `go test ./internal/setup/ -run TestApplyJSONHooks -v -count=1`
Expected: PASS (4/4).

- [ ] **Step 5: Re-point Claude to the helper (no behavior change yet)**

Update `internal/setup/claude.go` so `IsClaudeConfigured` and `ApplyClaudeHook` delegate to `JSONHooksConfigured` / `ApplyJSONHooks` while still using the LEGACY commands. Replace:

```go
// IsClaudeConfigured reports whether both notifytun Claude hooks are already present.
func IsClaudeConfigured(settingsPath string) bool {
	return JSONHooksConfigured(settingsPath, claudeLegacyHookEvents())
}

// ApplyClaudeHook merges notifytun Claude hooks into the given settings file.
func ApplyClaudeHook(settingsPath string) error {
	if IsClaudeConfigured(settingsPath) {
		return nil
	}
	return ApplyJSONHooks(settingsPath, claudeLegacyHookEvents(), nil)
}

func claudeLegacyHookEvents() []JSONHookEvent {
	return []JSONHookEvent{
		{Event: "Stop", Command: claudeStopCommand},
		{Event: "Notification", Command: claudeNotificationCommand},
	}
}
```

Remove the now-dead inline body of `ApplyClaudeHook` and `IsClaudeConfigured`. Keep `GenerateClaudeHook` and the `claudeStopCommand`/`claudeNotificationCommand` constants.

- [ ] **Step 6: Run all setup tests**

Run: `go test ./internal/setup/ -count=1`
Expected: PASS (all existing Claude tests continue to pass).

- [ ] **Step 7: Run full tree**

Run: `go test ./...`
Expected: PASS.

- [ ] **Step 8: Commit**

```bash
git add internal/setup/jsonhooks.go internal/setup/jsonhooks_test.go internal/setup/claude.go
git commit -m "$(cat <<'EOF'
refactor(setup): factor Claude JSON hook handling into jsonhooks.go

Introduces JSONHookEvent, ApplyJSONHooks, and JSONHooksConfigured. Claude's
configurator now delegates to the shared helper while keeping its legacy
command strings unchanged. Gemini will reuse the same helper next.

Co-Authored-By: Claude Opus 4.7 (1M context) <noreply@anthropic.com>
EOF
)"
```

---

## Task 6: Claude migration to `emit-hook`

Flip Claude from the legacy `emit --title '...'` commands to the new `emit-hook --event ...` commands. On Apply, remove any legacy notifytun entries by prefix. Update `remotesetup_test.go` expectations.

**Files:**

- Modify: `internal/setup/claude.go`
- Modify: `internal/setup/claude_test.go`
- Modify: `internal/cli/remotesetup_test.go` (legacy-string assertions → new canonical strings)

- [ ] **Step 1: Update Claude constants and helper to use emit-hook**

In `internal/setup/claude.go`, replace the constants and helper:

```go
const (
	claudeStopCommand         = "notifytun emit-hook --tool claude-code --event Stop"
	claudeNotificationCommand = "notifytun emit-hook --tool claude-code --event Notification"
)

var claudeStripPrefixes = []string{"notifytun emit ", "notifytun emit-hook "}

func claudeHookEvents() []JSONHookEvent {
	return []JSONHookEvent{
		{Event: "Stop", Command: claudeStopCommand},
		{Event: "Notification", Command: claudeNotificationCommand},
	}
}
```

Replace `IsClaudeConfigured` and `ApplyClaudeHook`:

```go
func IsClaudeConfigured(settingsPath string) bool {
	return JSONHooksConfigured(settingsPath, claudeHookEvents())
}

func ApplyClaudeHook(settingsPath string) error {
	return ApplyJSONHooks(settingsPath, claudeHookEvents(), claudeStripPrefixes)
}
```

Update `GenerateClaudeHook` to emit the new canonical commands (it's part of the public surface; tests assert substrings):

```go
func GenerateClaudeHook() string {
	return `{
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
}`
}
```

- [ ] **Step 2: Update `claude_test.go` for new commands and add migration test**

`internal/setup/claude_test.go`:

```go
package setup_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/michaellee8/notifytun/internal/setup"
)

func TestClaudeHookGeneration(t *testing.T) {
	hook := setup.GenerateClaudeHook()
	if !strings.Contains(hook, `"Stop"`) {
		t.Fatal("expected Stop hook in generated config")
	}
	if !strings.Contains(hook, `"Notification"`) {
		t.Fatal("expected Notification hook in generated config")
	}
	if !strings.Contains(hook, "notifytun emit-hook --tool claude-code --event Stop") {
		t.Fatal("expected emit-hook Stop command")
	}
	if !strings.Contains(hook, "notifytun emit-hook --tool claude-code --event Notification") {
		t.Fatal("expected emit-hook Notification command")
	}
}

func TestClaudeHookIdempotent(t *testing.T) {
	dir := t.TempDir()
	settingsPath := filepath.Join(dir, "settings.json")

	if err := setup.ApplyClaudeHook(settingsPath); err != nil {
		t.Fatalf("first apply: %v", err)
	}
	first, err := os.ReadFile(settingsPath)
	if err != nil {
		t.Fatalf("ReadFile(first): %v", err)
	}

	if err := setup.ApplyClaudeHook(settingsPath); err != nil {
		t.Fatalf("second apply: %v", err)
	}
	second, err := os.ReadFile(settingsPath)
	if err != nil {
		t.Fatalf("ReadFile(second): %v", err)
	}
	if string(first) != string(second) {
		t.Fatal("second apply changed the file - not idempotent")
	}
}

func TestDetectAlreadyConfigured(t *testing.T) {
	dir := t.TempDir()
	settingsPath := filepath.Join(dir, "settings.json")
	if err := os.WriteFile(settingsPath, []byte(`{
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
}`), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	if !setup.IsClaudeConfigured(settingsPath) {
		t.Fatal("expected Claude to be detected as already configured")
	}
}

func TestApplyClaudeHookPreservesExistingStopHooks(t *testing.T) {
	dir := t.TempDir()
	settingsPath := filepath.Join(dir, "settings.json")
	if err := os.WriteFile(settingsPath, []byte(`{
  "hooks": {
    "Stop": [
      {
        "matcher": "existing",
        "hooks": [
          {
            "type": "command",
            "command": "echo existing"
          }
        ]
      }
    ]
  }
}`), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	if err := setup.ApplyClaudeHook(settingsPath); err != nil {
		t.Fatalf("ApplyClaudeHook: %v", err)
	}

	data, err := os.ReadFile(settingsPath)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	content := string(data)
	if !strings.Contains(content, "echo existing") {
		t.Fatal("expected existing Stop hook to be preserved")
	}
	if strings.Count(content, "notifytun emit-hook --tool claude-code --event Stop") != 1 {
		t.Fatal("expected exactly one notifytun Stop hook after apply")
	}
}

func TestApplyClaudeHookMigratesLegacyEmitEntries(t *testing.T) {
	dir := t.TempDir()
	settingsPath := filepath.Join(dir, "settings.json")
	if err := os.WriteFile(settingsPath, []byte(`{
  "hooks": {
    "Stop": [
      {
        "matcher": "",
        "hooks": [
          {
            "type": "command",
            "command": "notifytun emit --tool claude-code --title 'Task complete'"
          }
        ]
      },
      {
        "matcher": "preserve",
        "hooks": [
          {
            "type": "command",
            "command": "echo keep-me"
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
            "command": "notifytun emit --tool claude-code --title 'Needs attention'"
          }
        ]
      }
    ]
  }
}`), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	if err := setup.ApplyClaudeHook(settingsPath); err != nil {
		t.Fatalf("ApplyClaudeHook: %v", err)
	}

	data, err := os.ReadFile(settingsPath)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	content := string(data)
	if strings.Contains(content, "'Task complete'") || strings.Contains(content, "'Needs attention'") {
		t.Fatalf("expected legacy notifytun emit entries to be removed, got %q", content)
	}
	if !strings.Contains(content, "echo keep-me") {
		t.Fatal("expected unrelated hook to be preserved")
	}
	if strings.Count(content, "notifytun emit-hook --tool claude-code --event Stop") != 1 {
		t.Fatalf("expected exactly one emit-hook Stop entry, got %q", content)
	}
	if strings.Count(content, "notifytun emit-hook --tool claude-code --event Notification") != 1 {
		t.Fatalf("expected exactly one emit-hook Notification entry, got %q", content)
	}
}
```

- [ ] **Step 3: Update `internal/cli/remotesetup_test.go` to use canonical strings**

Change the substring `"notifytun emit --tool claude-code --title 'Task complete'"` in `TestRemoteSetupApplyConfiguresSupportedTools` and `TestRemoteSetupNothingToConfigureWhenAlreadySetUp` to `"notifytun emit-hook --tool claude-code --event Stop"`. Also add the Notification string check to both tests for completeness:

Replace the `TestRemoteSetupApplyConfiguresSupportedTools` body's Claude-settings assertion block with:

```go
claudeSettings, err := os.ReadFile(filepath.Join(home, ".claude", "settings.json"))
if err != nil {
	t.Fatalf("ReadFile(claude): %v", err)
}
if !strings.Contains(string(claudeSettings), "notifytun emit-hook --tool claude-code --event Stop") {
	t.Fatalf("expected Claude settings to contain notifytun Stop hook, got %q", string(claudeSettings))
}
if !strings.Contains(string(claudeSettings), "notifytun emit-hook --tool claude-code --event Notification") {
	t.Fatalf("expected Claude settings to contain notifytun Notification hook, got %q", string(claudeSettings))
}
```

Replace the pre-written settings in `TestRemoteSetupNothingToConfigureWhenAlreadySetUp` with the new canonical commands (change both commands inside the literal JSON). The Codex pre-written notify array stays on the legacy `["notifytun", "emit", "--tool", "codex"]` for now because Task 8 updates it; do NOT modify the Codex expectations in this task, but DO be aware the "already configured" branch for Codex still uses the legacy array until Task 8. If the test fails because Codex "already configured" no longer matches, temporarily loosen that assertion to only check Claude — Task 8 will restore it.

Also update the Claude seed block in `TestRemoteSetupNothingToConfigureWhenAlreadySetUp` to write the new canonical commands (both Stop and Notification), otherwise that test now fails because `IsClaudeConfigured` no longer accepts the legacy `emit --title '...'` strings. Concrete Claude seed block to use:

```go
if err := os.WriteFile(filepath.Join(home, ".claude", "settings.json"), []byte(`{
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
}`), 0o644); err != nil {
    t.Fatalf("WriteFile(claude settings): %v", err)
}
```

The Codex seed in that same test stays on the legacy `["notifytun", "emit", "--tool", "codex"]` array for now; Task 8 updates it.

- [ ] **Step 4: Run setup tests**

Run: `go test ./internal/setup/ -count=1 -v`
Expected: PASS, including the new `TestApplyClaudeHookMigratesLegacyEmitEntries`.

- [ ] **Step 5: Run CLI tests**

Run: `go test ./internal/cli/ -count=1 -v`
Expected: PASS.

- [ ] **Step 6: Commit**

```bash
git add internal/setup/claude.go internal/setup/claude_test.go internal/cli/remotesetup_test.go
git commit -m "$(cat <<'EOF'
feat(setup): migrate Claude hooks to emit-hook and drop legacy emit entries

Claude's canonical commands are now:
  notifytun emit-hook --tool claude-code --event Stop
  notifytun emit-hook --tool claude-code --event Notification

ApplyClaudeHook strips any existing 'notifytun emit ' or
'notifytun emit-hook ' prefixed entries from Stop/Notification before
writing, so re-running remote-setup cleanly migrates v1 installations.

Co-Authored-By: Claude Opus 4.7 (1M context) <noreply@anthropic.com>
EOF
)"
```

---

## Task 7: Gemini configurator

Add the third configurator, reusing `jsonhooks.go`. Hooks `AfterAgent` (Task complete) and `Notification` (Needs attention) in `~/.gemini/settings.json`.

**Files:**

- Create: `internal/setup/gemini.go`
- Create: `internal/setup/gemini_test.go`
- Modify: `internal/setup/configurator.go` (register Gemini)
- Modify: `internal/setup/setup.go` (leave `knownTools` — Gemini now fully supported)

- [ ] **Step 1: Write failing Gemini tests**

`internal/setup/gemini_test.go`:

```go
package setup_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/michaellee8/notifytun/internal/setup"
)

func TestGeminiHookIdempotent(t *testing.T) {
	dir := t.TempDir()
	settingsPath := filepath.Join(dir, "settings.json")

	if err := setup.ApplyGeminiHook(settingsPath); err != nil {
		t.Fatalf("first apply: %v", err)
	}
	first, err := os.ReadFile(settingsPath)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	if err := setup.ApplyGeminiHook(settingsPath); err != nil {
		t.Fatalf("second apply: %v", err)
	}
	second, err := os.ReadFile(settingsPath)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	if string(first) != string(second) {
		t.Fatal("not idempotent")
	}
}

func TestGeminiHookIncludesAfterAgentAndNotification(t *testing.T) {
	dir := t.TempDir()
	settingsPath := filepath.Join(dir, "settings.json")

	if err := setup.ApplyGeminiHook(settingsPath); err != nil {
		t.Fatalf("ApplyGeminiHook: %v", err)
	}

	data, err := os.ReadFile(settingsPath)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	content := string(data)
	if !strings.Contains(content, "notifytun emit-hook --tool gemini --event AfterAgent") {
		t.Fatalf("missing AfterAgent hook: %q", content)
	}
	if !strings.Contains(content, "notifytun emit-hook --tool gemini --event Notification") {
		t.Fatalf("missing Notification hook: %q", content)
	}
}

func TestIsGeminiConfiguredOnCanonicalFile(t *testing.T) {
	dir := t.TempDir()
	settingsPath := filepath.Join(dir, "settings.json")

	if setup.IsGeminiConfigured(settingsPath) {
		t.Fatal("empty file should not be configured")
	}
	if err := setup.ApplyGeminiHook(settingsPath); err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if !setup.IsGeminiConfigured(settingsPath) {
		t.Fatal("expected configured after Apply")
	}
}

func TestApplyGeminiHookPreservesUnrelatedEntries(t *testing.T) {
	dir := t.TempDir()
	settingsPath := filepath.Join(dir, "settings.json")
	if err := os.WriteFile(settingsPath, []byte(`{
  "hooks": {
    "AfterAgent": [
      {
        "matcher": "existing",
        "hooks": [
          {"type": "command", "command": "echo other"}
        ]
      }
    ]
  }
}`), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	if err := setup.ApplyGeminiHook(settingsPath); err != nil {
		t.Fatalf("Apply: %v", err)
	}
	data, _ := os.ReadFile(settingsPath)
	content := string(data)
	if !strings.Contains(content, "echo other") {
		t.Fatal("expected unrelated hook to be preserved")
	}
	if strings.Count(content, "notifytun emit-hook --tool gemini --event AfterAgent") != 1 {
		t.Fatalf("expected exactly one AfterAgent entry, got %q", content)
	}
}
```

- [ ] **Step 2: Run tests to confirm they fail**

Run: `go test ./internal/setup/ -run TestGemini -v -count=1`
Expected: FAIL — `ApplyGeminiHook` / `IsGeminiConfigured` undefined.

- [ ] **Step 3: Implement `gemini.go`**

`internal/setup/gemini.go`:

```go
package setup

import (
	"path/filepath"
)

const (
	geminiAfterAgentCommand   = "notifytun emit-hook --tool gemini --event AfterAgent"
	geminiNotificationCommand = "notifytun emit-hook --tool gemini --event Notification"
)

var geminiStripPrefixes = []string{"notifytun emit ", "notifytun emit-hook "}

// GeminiConfigurator manages ~/.gemini/settings.json hooks.
type GeminiConfigurator struct{}

func (*GeminiConfigurator) Name() string       { return "Gemini CLI" }
func (*GeminiConfigurator) Binaries() []string { return []string{"gemini"} }
func (*GeminiConfigurator) ConfigPath(home string) string {
	return filepath.Join(home, ".gemini", "settings.json")
}
func (*GeminiConfigurator) IsConfigured(home string) bool {
	return IsGeminiConfigured((&GeminiConfigurator{}).ConfigPath(home))
}
func (*GeminiConfigurator) PreviewAction(home string) string {
	return "will add AfterAgent + Notification hooks to ~/.gemini/settings.json"
}
func (c *GeminiConfigurator) Apply(home string) error {
	return ApplyGeminiHook(c.ConfigPath(home))
}

func geminiHookEvents() []JSONHookEvent {
	return []JSONHookEvent{
		{Event: "AfterAgent", Command: geminiAfterAgentCommand},
		{Event: "Notification", Command: geminiNotificationCommand},
	}
}

// IsGeminiConfigured reports whether notifytun Gemini hooks are already present.
func IsGeminiConfigured(settingsPath string) bool {
	return JSONHooksConfigured(settingsPath, geminiHookEvents())
}

// ApplyGeminiHook merges notifytun Gemini hooks into the given settings file.
func ApplyGeminiHook(settingsPath string) error {
	return ApplyJSONHooks(settingsPath, geminiHookEvents(), geminiStripPrefixes)
}
```

- [ ] **Step 4: Register Gemini**

In `internal/setup/configurator.go`, extend `Registered`:

```go
var Registered = []Configurator{
	&ClaudeConfigurator{},
	&CodexConfigurator{},
	&GeminiConfigurator{},
}
```

- [ ] **Step 5: Run tests**

Run: `go test ./internal/setup/ -v -count=1`
Expected: PASS. Note: `TestDetectToolsFullMatrix` in `setup_test.go` expects Gemini to be `Supported: false`. Update that assertion to `tool.Supported == true` for Gemini, since it is now registered.

Specifically, change in `setup_test.go`:

```go
if tool, ok := found["Gemini CLI"]; !ok {
	t.Fatal("expected Gemini CLI to be detected")
} else if !tool.Supported {
	t.Fatal("expected Gemini CLI to be detected as supported")
}
```

- [ ] **Step 6: Re-run tests**

Run: `go test ./internal/setup/ -v -count=1`
Expected: PASS.

- [ ] **Step 7: Commit**

```bash
git add internal/setup/gemini.go internal/setup/gemini_test.go internal/setup/configurator.go internal/setup/setup_test.go
git commit -m "$(cat <<'EOF'
feat(setup): add Gemini CLI configurator with AfterAgent + Notification

GeminiConfigurator writes ~/.gemini/settings.json hooks for AfterAgent
(Task complete) and Notification (Needs attention) using the shared
jsonhooks helper. Registered as the third Configurator.

Co-Authored-By: Claude Opus 4.7 (1M context) <noreply@anthropic.com>
EOF
)"
```

---

## Task 8: Codex → `emit-hook`

Switch Codex's notify array from the legacy `emit` invocation to the new `emit-hook --event notify` form.

**Files:**

- Modify: `internal/setup/codex.go`
- Modify: `internal/setup/codex_test.go`
- Modify: `internal/cli/remotesetup_test.go`

- [ ] **Step 1: Write failing Codex canonical-command test**

Replace `TestCodexNotifyGeneration` in `internal/setup/codex_test.go` with:

```go
func TestCodexNotifyGeneration(t *testing.T) {
	cfg := setup.GenerateCodexNotifyConfig()
	if !strings.Contains(cfg, `notify = ["notifytun", "emit-hook", "--tool", "codex", "--event", "notify"]`) {
		t.Fatalf("expected codex notify config to call emit-hook, got %q", cfg)
	}
}
```

Update the existing `TestCodexNotifyIdempotent` if it compares against a specific literal (it only does `string(first) == string(second)`, so no change needed — but verify).

Update `TestIsCodexConfiguredIgnoresTableScopedNotify` and `TestIsCodexConfiguredAcceptsMultilineRootNotify` if they embed the legacy array. They embed `["notifytun", "emit", "--tool", "codex"]`. Replace those with `["notifytun", "emit-hook", "--tool", "codex", "--event", "notify"]`.

Update `TestApplyCodexNotifyConfigInsertsRootNotifyBeforeFirstTable` and `TestApplyCodexNotifyConfigReplacesExistingRootNotify` similarly: their `equalStrings` expected values change to `{"notifytun", "emit-hook", "--tool", "codex", "--event", "notify"}`.

- [ ] **Step 2: Update Codex constants**

In `internal/setup/codex.go`:

```go
const codexNotifyConfigLine = `notify = ["notifytun", "emit-hook", "--tool", "codex", "--event", "notify"]`

var codexNotifyCommand = []string{"notifytun", "emit-hook", "--tool", "codex", "--event", "notify"}
```

- [ ] **Step 3: Update `remotesetup_test.go` Codex seed config**

In `TestRemoteSetupNothingToConfigureWhenAlreadySetUp`, change the pre-written Codex config to:

```go
if err := os.WriteFile(filepath.Join(home, ".codex", "config.toml"),
	[]byte(`notify = ["notifytun", "emit-hook", "--tool", "codex", "--event", "notify"]`+"\n"),
	0o644); err != nil {
```

- [ ] **Step 4: Run tests**

Run: `go test ./internal/setup/ -count=1 -v`
Expected: PASS.

Run: `go test ./internal/cli/ -count=1 -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/setup/codex.go internal/setup/codex_test.go internal/cli/remotesetup_test.go
git commit -m "$(cat <<'EOF'
feat(setup): Codex notify invokes emit-hook instead of emit

Canonical Codex root-notify array is now:
  ["notifytun", "emit-hook", "--tool", "codex", "--event", "notify"]

Matches the unified hook-adapter pattern used by the JSON-hook tools.

Co-Authored-By: Claude Opus 4.7 (1M context) <noreply@anthropic.com>
EOF
)"
```

---

## Task 9: OpenCode configurator + plugin file

Write a verbatim JS plugin at `~/.config/opencode/plugins/notifytun.js`. Detection is byte-for-byte equality to the canonical content (after normalizing a trailing newline).

**Files:**

- Create: `internal/setup/opencode.go`
- Create: `internal/setup/opencode_test.go`
- Modify: `internal/setup/configurator.go` (register OpenCode)
- Modify: `internal/setup/setup_test.go` (update OpenCode assertion)

- [ ] **Step 1: Write failing OpenCode tests**

`internal/setup/opencode_test.go`:

```go
package setup_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/michaellee8/notifytun/internal/setup"
)

func TestOpenCodePluginIdempotent(t *testing.T) {
	dir := t.TempDir()
	pluginPath := filepath.Join(dir, "notifytun.js")

	if err := setup.ApplyOpenCodePlugin(pluginPath); err != nil {
		t.Fatalf("first apply: %v", err)
	}
	first, err := os.ReadFile(pluginPath)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	if err := setup.ApplyOpenCodePlugin(pluginPath); err != nil {
		t.Fatalf("second apply: %v", err)
	}
	second, err := os.ReadFile(pluginPath)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	if string(first) != string(second) {
		t.Fatal("second apply changed content")
	}
}

func TestOpenCodePluginContentContainsKeyBits(t *testing.T) {
	dir := t.TempDir()
	pluginPath := filepath.Join(dir, "notifytun.js")

	if err := setup.ApplyOpenCodePlugin(pluginPath); err != nil {
		t.Fatalf("Apply: %v", err)
	}
	data, _ := os.ReadFile(pluginPath)
	content := string(data)

	for _, want := range []string{
		`export const NotifytunPlugin`,
		`"session.idle"`,
		`notifytun emit-hook --tool opencode --event session.idle`,
		`client.session.messages`,
	} {
		if !strings.Contains(content, want) {
			t.Fatalf("expected plugin to contain %q, got %q", want, content)
		}
	}
}

func TestIsOpenCodeConfiguredCanonical(t *testing.T) {
	dir := t.TempDir()
	pluginPath := filepath.Join(dir, "notifytun.js")

	if setup.IsOpenCodeConfigured(pluginPath) {
		t.Fatal("absent file should not be configured")
	}
	if err := setup.ApplyOpenCodePlugin(pluginPath); err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if !setup.IsOpenCodeConfigured(pluginPath) {
		t.Fatal("expected configured after Apply")
	}
}

func TestApplyOpenCodePluginOverwritesModifiedContent(t *testing.T) {
	dir := t.TempDir()
	pluginPath := filepath.Join(dir, "notifytun.js")
	if err := os.WriteFile(pluginPath, []byte("// user edits\n"), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	if err := setup.ApplyOpenCodePlugin(pluginPath); err != nil {
		t.Fatalf("Apply: %v", err)
	}
	data, _ := os.ReadFile(pluginPath)
	if strings.Contains(string(data), "user edits") {
		t.Fatal("expected user edits to be overwritten with canonical content")
	}
	if !setup.IsOpenCodeConfigured(pluginPath) {
		t.Fatal("expected configured after Apply-over-existing")
	}
}

func TestApplyOpenCodePluginCreatesParentDir(t *testing.T) {
	dir := t.TempDir()
	pluginPath := filepath.Join(dir, "deep", "nested", "notifytun.js")

	if err := setup.ApplyOpenCodePlugin(pluginPath); err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if _, err := os.Stat(pluginPath); err != nil {
		t.Fatalf("Stat: %v", err)
	}
}
```

- [ ] **Step 2: Run tests to confirm they fail**

Run: `go test ./internal/setup/ -run TestOpenCode -v -count=1`
Expected: FAIL — undefined symbols.

- [ ] **Step 3: Implement `opencode.go`**

`internal/setup/opencode.go`:

```go
package setup

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
)

// openCodePluginContent is the verbatim content notifytun writes.
// Any byte-level divergence causes IsOpenCodeConfigured to report false
// and Apply to overwrite.
const openCodePluginContent = `// Managed by ` + "`notifytun remote-setup`" + `. Edits will be overwritten.
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
        await $` + "`echo ${payload} | notifytun emit-hook --tool opencode --event session.idle`" + `;
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
`

// OpenCodeConfigurator writes a verbatim JS plugin.
type OpenCodeConfigurator struct{}

func (*OpenCodeConfigurator) Name() string       { return "OpenCode" }
func (*OpenCodeConfigurator) Binaries() []string { return []string{"opencode"} }
func (*OpenCodeConfigurator) ConfigPath(home string) string {
	return filepath.Join(home, ".config", "opencode", "plugins", "notifytun.js")
}
func (*OpenCodeConfigurator) IsConfigured(home string) bool {
	return IsOpenCodeConfigured((&OpenCodeConfigurator{}).ConfigPath(home))
}
func (*OpenCodeConfigurator) PreviewAction(home string) string {
	return "will write ~/.config/opencode/plugins/notifytun.js"
}
func (c *OpenCodeConfigurator) Apply(home string) error {
	return ApplyOpenCodePlugin(c.ConfigPath(home))
}

// GenerateOpenCodePlugin returns the verbatim plugin content.
func GenerateOpenCodePlugin() string {
	return openCodePluginContent
}

// IsOpenCodeConfigured reports whether the plugin file matches the canonical content.
func IsOpenCodeConfigured(pluginPath string) bool {
	data, err := os.ReadFile(pluginPath)
	if err != nil {
		return false
	}
	return bytes.Equal(data, []byte(openCodePluginContent))
}

// ApplyOpenCodePlugin writes the canonical plugin file, creating parents.
func ApplyOpenCodePlugin(pluginPath string) error {
	if IsOpenCodeConfigured(pluginPath) {
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(pluginPath), 0o755); err != nil {
		return fmt.Errorf("create OpenCode plugin dir: %w", err)
	}
	if err := os.WriteFile(pluginPath, []byte(openCodePluginContent), 0o644); err != nil {
		return fmt.Errorf("write OpenCode plugin: %w", err)
	}
	return nil
}
```

- [ ] **Step 4: Register OpenCode**

`internal/setup/configurator.go`:

```go
var Registered = []Configurator{
	&ClaudeConfigurator{},
	&CodexConfigurator{},
	&GeminiConfigurator{},
	&OpenCodeConfigurator{},
}
```

- [ ] **Step 5: Update `TestDetectToolsFullMatrix`**

In `internal/setup/setup_test.go`, update the OpenCode assertion:

```go
if tool, ok := found["OpenCode"]; !ok {
	t.Fatal("expected OpenCode to be detected")
} else if !tool.Supported {
	t.Fatal("expected OpenCode to be detected as supported")
}
```

- [ ] **Step 6: Clean up `knownTools` in `setup.go`**

Since all four configurators are now registered, the fallback `knownTools` slice in `setup.go` is no longer needed for "supported=false" handling. Simplify `DetectTools` to walk `Registered` directly:

```go
func DetectTools(pathEnv string) []Tool {
	var tools []Tool
	for _, cfg := range Registered {
		tool := Tool{
			Name:      cfg.Name(),
			Supported: true,
			Cfg:       cfg,
		}
		for _, binary := range cfg.Binaries() {
			if path := lookPath(binary, pathEnv); path != "" {
				tool.Binary = path
				tool.Detected = true
				break
			}
		}
		if tool.Detected {
			tools = append(tools, tool)
		}
	}
	return tools
}
```

Delete the `knownTools` slice.

- [ ] **Step 7: Run all tests**

Run: `go test ./... -count=1`
Expected: PASS.

- [ ] **Step 8: Commit**

```bash
git add internal/setup/opencode.go internal/setup/opencode_test.go internal/setup/configurator.go internal/setup/setup.go internal/setup/setup_test.go
git commit -m "$(cat <<'EOF'
feat(setup): add OpenCode configurator that writes a JS plugin file

Writes ~/.config/opencode/plugins/notifytun.js verbatim. The plugin
hooks session.idle, fetches the last assistant message via the
OpenCode client SDK, and pipes a {body: ...} payload to
notifytun emit-hook --tool opencode --event session.idle. All plugin
failures are swallowed so a notifytun outage cannot block an
OpenCode session.

With four configurators registered, DetectTools now walks Registered
directly and the separate knownTools list is removed.

Co-Authored-By: Claude Opus 4.7 (1M context) <noreply@anthropic.com>
EOF
)"
```

---

## Task 10: `remote-setup` loop refactor

Replace the three `switch tool.Name` blocks in `remotesetup.go` with loops that call `Configurator` methods. Preview sentences now come from `Cfg.PreviewAction(home)` rather than string literals in `Preview()`.

**Files:**

- Modify: `internal/cli/remotesetup.go`
- Modify: `internal/setup/setup.go` (`Preview` uses `PreviewAction`)
- Modify: `internal/cli/remotesetup_test.go` (add Gemini/OpenCode paths)

- [ ] **Step 1: Rewrite `internal/cli/remotesetup.go` to use configurators**

Full replacement:

```go
package cli

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/michaellee8/notifytun/internal/setup"
	"github.com/spf13/cobra"
)

// NewRemoteSetupCmd detects supported AI tools and configures notifytun hooks for them.
func NewRemoteSetupCmd() *cobra.Command {
	var dryRun bool

	cmd := &cobra.Command{
		Use:           "remote-setup",
		Short:         "Detect AI tools and configure their hooks to call notifytun emit",
		Args:          cobra.NoArgs,
		SilenceUsage:  true,
		SilenceErrors: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			tools := setup.DetectTools("")
			if len(tools) == 0 {
				fmt.Fprintln(cmd.OutOrStdout(), "No supported AI tools detected in PATH.")
				return nil
			}

			home, err := os.UserHomeDir()
			if err != nil {
				return fmt.Errorf("resolve home directory: %w", err)
			}

			markConfigured(home, tools)
			writePreview(cmd.OutOrStdout(), home, tools)

			if dryRun {
				fmt.Fprintln(cmd.OutOrStdout(), "(dry run — no changes applied)")
				return nil
			}

			if !hasConfigWork(tools) {
				fmt.Fprintln(cmd.OutOrStdout(), "Nothing to configure — all supported tools already set up.")
				return nil
			}

			if !confirmApply(cmd.InOrStdin(), cmd.OutOrStdout()) {
				fmt.Fprintln(cmd.OutOrStdout(), "Aborted.")
				return nil
			}

			applyToolConfig(cmd.OutOrStdout(), cmd.ErrOrStderr(), home, tools)
			return nil
		},
	}

	cmd.Flags().BoolVar(&dryRun, "dry-run", false, "Show what would be configured without applying")
	return cmd
}

func markConfigured(home string, tools []setup.Tool) {
	for i := range tools {
		if tools[i].Cfg == nil {
			continue
		}
		tools[i].Configured = tools[i].Cfg.IsConfigured(home)
	}
}

func writePreview(w io.Writer, home string, tools []setup.Tool) {
	var sb strings.Builder
	sb.WriteString("Detected tools:\n")
	for _, tool := range tools {
		switch {
		case tool.Configured:
			sb.WriteString(fmt.Sprintf("  * %s -- already configured\n", tool.Name))
		case tool.Cfg != nil:
			sb.WriteString(fmt.Sprintf("  * %s -- %s\n", tool.Name, tool.Cfg.PreviewAction(home)))
		default:
			sb.WriteString(fmt.Sprintf("  * %s -- detected but hook setup not supported in v1\n", tool.Name))
		}
	}
	fmt.Fprint(w, sb.String())
}

func hasConfigWork(tools []setup.Tool) bool {
	for _, tool := range tools {
		if tool.Cfg != nil && !tool.Configured {
			return true
		}
	}
	return false
}

func confirmApply(in io.Reader, out io.Writer) bool {
	fmt.Fprint(out, "Apply? [Y/n] ")
	answer, err := bufio.NewReader(in).ReadString('\n')
	answer = strings.TrimSpace(strings.ToLower(answer))
	if err != nil {
		if err == io.EOF {
			return answer == "y" || answer == "yes"
		}
		return false
	}
	return answer == "" || answer == "y" || answer == "yes"
}

func applyToolConfig(stdout, stderr io.Writer, home string, tools []setup.Tool) {
	for _, tool := range tools {
		if tool.Cfg == nil || tool.Configured {
			continue
		}
		if err := tool.Cfg.Apply(home); err != nil {
			fmt.Fprintf(stderr, "warning: failed to configure %s: %v\n", tool.Name, err)
			continue
		}
		fmt.Fprintf(stdout, "Configured %s at %s\n", tool.Name, tool.Cfg.ConfigPath(home))
	}
}
```

The success message goes from `"Configured %s hooks in %s\n"` / `"Configured %s notify in %s\n"` to a uniform `"Configured %s at %s\n"`. Update the tests accordingly.

- [ ] **Step 2: Remove the now-unused `Preview` function from `setup.go`**

In `internal/setup/setup.go`, delete the `Preview(tools []Tool) string` function — `writePreview` in `remotesetup.go` now handles it. Keep `DetectTools`, `Tool`, `lookPath`.

- [ ] **Step 3: Update `remotesetup_test.go` expectations**

Full replacement of the test expectations to use the new "Configured … at …" message and to cover all four tools. Replace `TestRemoteSetupDryRunPrintsPreview` and `TestRemoteSetupApplyConfiguresSupportedTools` bodies:

```go
func TestRemoteSetupDryRunPrintsPreview(t *testing.T) {
	t.Setenv("PATH", makeFakePath(t, "claude", "codex", "gemini", "opencode"))
	t.Setenv("HOME", t.TempDir())

	cmd := NewRemoteSetupCmd()

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	cmd.SetOut(&stdout)
	cmd.SetErr(&stderr)
	cmd.SetArgs([]string{"--dry-run"})

	if err := cmd.Execute(); err != nil {
		t.Fatalf("Execute: %v", err)
	}

	out := stdout.String()
	for _, want := range []string{
		"Detected tools:\n",
		"Claude Code -- will add Stop + Notification hooks to ~/.claude/settings.json",
		"Codex CLI -- will set notify in ~/.codex/config.toml",
		"Gemini CLI -- will add AfterAgent + Notification hooks to ~/.gemini/settings.json",
		"OpenCode -- will write ~/.config/opencode/plugins/notifytun.js",
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("expected preview to contain %q, got %q", want, out)
		}
	}
	if !strings.Contains(out, "(dry run — no changes applied)") {
		t.Fatalf("expected dry-run note, got %q", out)
	}
	if stderr.Len() != 0 {
		t.Fatalf("expected empty stderr, got %q", stderr.String())
	}
}

func TestRemoteSetupApplyConfiguresAllFourTools(t *testing.T) {
	t.Setenv("PATH", makeFakePath(t, "claude", "codex", "gemini", "opencode"))
	home := t.TempDir()
	t.Setenv("HOME", home)

	cmd := NewRemoteSetupCmd()

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	cmd.SetOut(&stdout)
	cmd.SetErr(&stderr)
	cmd.SetIn(strings.NewReader("y\n"))

	if err := cmd.Execute(); err != nil {
		t.Fatalf("Execute: %v", err)
	}

	out := stdout.String()
	for _, name := range []string{"Claude Code", "Codex CLI", "Gemini CLI", "OpenCode"} {
		if !strings.Contains(out, "Configured "+name+" at ") {
			t.Fatalf("expected 'Configured %s at ' in output, got %q", name, out)
		}
	}
	if stderr.Len() != 0 {
		t.Fatalf("expected empty stderr, got %q", stderr.String())
	}

	// Claude + emit-hook canonical commands present.
	claude, err := os.ReadFile(filepath.Join(home, ".claude", "settings.json"))
	if err != nil {
		t.Fatalf("ReadFile(claude): %v", err)
	}
	for _, want := range []string{
		"notifytun emit-hook --tool claude-code --event Stop",
		"notifytun emit-hook --tool claude-code --event Notification",
	} {
		if !strings.Contains(string(claude), want) {
			t.Fatalf("expected Claude settings to contain %q, got %q", want, string(claude))
		}
	}

	if !setup.IsCodexConfigured(filepath.Join(home, ".codex", "config.toml")) {
		t.Fatal("expected Codex config to be structurally configured")
	}

	gemini, err := os.ReadFile(filepath.Join(home, ".gemini", "settings.json"))
	if err != nil {
		t.Fatalf("ReadFile(gemini): %v", err)
	}
	for _, want := range []string{
		"notifytun emit-hook --tool gemini --event AfterAgent",
		"notifytun emit-hook --tool gemini --event Notification",
	} {
		if !strings.Contains(string(gemini), want) {
			t.Fatalf("expected Gemini settings to contain %q, got %q", want, string(gemini))
		}
	}

	pluginPath := filepath.Join(home, ".config", "opencode", "plugins", "notifytun.js")
	if _, err := os.Stat(pluginPath); err != nil {
		t.Fatalf("Stat(opencode plugin): %v", err)
	}
	if !setup.IsOpenCodeConfigured(pluginPath) {
		t.Fatal("expected OpenCode plugin to match canonical content")
	}
}
```

Delete the older `TestRemoteSetupApplyConfiguresSupportedTools` (superseded by the four-tool version).

Update `TestRemoteSetupNothingToConfigureWhenAlreadySetUp` to pre-seed all four tools' canonical configs. Full replacement:

```go
func TestRemoteSetupNothingToConfigureWhenAlreadySetUp(t *testing.T) {
	t.Setenv("PATH", makeFakePath(t, "claude", "codex", "gemini", "opencode"))
	home := t.TempDir()
	t.Setenv("HOME", home)

	mustWrite := func(path, body string) {
		t.Helper()
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatalf("MkdirAll(%s): %v", path, err)
		}
		if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
			t.Fatalf("WriteFile(%s): %v", path, err)
		}
	}

	mustWrite(filepath.Join(home, ".claude", "settings.json"), `{
  "hooks": {
    "Stop": [{"matcher":"","hooks":[{"type":"command","command":"notifytun emit-hook --tool claude-code --event Stop"}]}],
    "Notification": [{"matcher":"","hooks":[{"type":"command","command":"notifytun emit-hook --tool claude-code --event Notification"}]}]
  }
}`)

	mustWrite(filepath.Join(home, ".codex", "config.toml"),
		`notify = ["notifytun", "emit-hook", "--tool", "codex", "--event", "notify"]`+"\n")

	mustWrite(filepath.Join(home, ".gemini", "settings.json"), `{
  "hooks": {
    "AfterAgent": [{"matcher":"","hooks":[{"type":"command","command":"notifytun emit-hook --tool gemini --event AfterAgent"}]}],
    "Notification": [{"matcher":"","hooks":[{"type":"command","command":"notifytun emit-hook --tool gemini --event Notification"}]}]
  }
}`)

	mustWrite(filepath.Join(home, ".config", "opencode", "plugins", "notifytun.js"),
		setup.GenerateOpenCodePlugin())

	cmd := NewRemoteSetupCmd()
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	cmd.SetOut(&stdout)
	cmd.SetErr(&stderr)

	if err := cmd.Execute(); err != nil {
		t.Fatalf("Execute: %v", err)
	}

	out := stdout.String()
	for _, name := range []string{"Claude Code", "Codex CLI", "Gemini CLI", "OpenCode"} {
		if !strings.Contains(out, name+" -- already configured") {
			t.Fatalf("expected %q in preview, got %q", name+" -- already configured", out)
		}
	}
	if !strings.Contains(out, "Nothing to configure — all supported tools already set up.") {
		t.Fatalf("expected nothing-to-configure message, got %q", out)
	}
	if strings.Contains(out, "Apply? [Y/n] ") {
		t.Fatalf("did not expect prompt when nothing needs configuration, got %q", out)
	}
	if stderr.Len() != 0 {
		t.Fatalf("expected empty stderr, got %q", stderr.String())
	}
}
```

Leave `TestRemoteSetupContinuesAfterPerToolFailure`, `TestRemoteSetupAbortsOnNo`, and `TestRemoteSetupAbortsOnEOF` as-is — they still exercise just Claude + Codex and the messages they check still exist (the warning and "Aborted" lines). The only tweak: `TestRemoteSetupContinuesAfterPerToolFailure` checks `"Configured Codex CLI notify in"` — change to `"Configured Codex CLI at "`.

- [ ] **Step 4: Run all tests**

Run: `go test ./... -count=1`
Expected: PASS.

- [ ] **Step 5: Build the binary and smoke-test the help text**

Run: `go build ./... && ./notifytun emit-hook --help 2>&1 | head -n 20`
Expected: help text lists `--tool`, `--event`, `--db`, `--socket`.

Clean up: `rm -f ./notifytun`

- [ ] **Step 6: Commit**

```bash
git add internal/cli/remotesetup.go internal/setup/setup.go internal/cli/remotesetup_test.go
git commit -m "$(cat <<'EOF'
refactor(cli): remote-setup iterates Configurator registry

Eliminates the per-tool switch statements in markConfigured, Preview,
and applyToolConfig. The command now walks setup.Registered and calls
Configurator methods. Preview strings come from each configurator's
PreviewAction(home), and successful apply logs a uniform
"Configured <name> at <path>" line.

Tests cover all four tools end-to-end: detection, dry-run preview,
apply, and re-run idempotency.

Co-Authored-By: Claude Opus 4.7 (1M context) <noreply@anthropic.com>
EOF
)"
```

---

## Task 11: Documentation

**Files:**

- Modify: `README.md`
- Modify: `docs/rough-spec.md`
- Modify: `docs/superpowers/specs/2026-04-14-notifytun-design.md`

- [ ] **Step 1: Update `README.md` remote-setup section**

Read the current README to find the remote-setup section (likely under a heading like "Remote setup" or "Configuring agent hooks"). Replace any list of supported tools with all four. Add a sentence like:

```
`notifytun remote-setup` configures hooks for Claude Code, Codex CLI,
Gemini CLI, and OpenCode. For each detected tool it writes (or updates)
one config file:

- Claude Code: `~/.claude/settings.json` — `Stop` and `Notification` hooks
- Codex CLI: `~/.codex/config.toml` — root `notify` array
- Gemini CLI: `~/.gemini/settings.json` — `AfterAgent` and `Notification` hooks
- OpenCode: `~/.config/opencode/plugins/notifytun.js` — a plugin that
  forwards `session.idle` events

Hook commands always exit 0. Any DB or logging error is appended to
`notifytun-errors.log` next to the SQLite database (default
`~/.notifytun/notifytun-errors.log`).
```

Use `Grep` + `Edit` to find and replace the relevant README lines. Do not reword unrelated sections.

- [ ] **Step 2: Update `docs/rough-spec.md`**

In `docs/rough-spec.md`, find the sections describing Claude, Codex integration (§15–17 in the rough spec). Update the example commands so Claude hook examples use `notifytun emit-hook --tool claude-code --event Stop` (and the Notification equivalent) in their `command` fields. Update the Codex TOML example to `notify = ["notifytun", "emit-hook", "--tool", "codex", "--event", "notify"]`. Add a new subsection "§ Gemini integration" with the same pattern (`~/.gemini/settings.json`, `AfterAgent` + `Notification`) and "§ OpenCode integration" pointing at the plugin file.

Use `Grep` to locate the Claude `"command":` literals and `Edit` to replace each one.

- [ ] **Step 3: Add superseded note to the earlier design doc**

At the top of `docs/superpowers/specs/2026-04-14-notifytun-design.md`, directly after the heading/date block, insert:

```markdown
> **Note:** The `remote-setup` scope in this document is superseded for the
> hook-configuration portion by `docs/superpowers/specs/2026-04-20-remote-setup-all-agents-design.md`.
> Everything else in this document (SSH transport, SQLite protocol, notifier
> backends, etc.) still applies.
```

- [ ] **Step 4: Verify build/tests unchanged by docs**

Run: `go test ./... -count=1`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add README.md docs/rough-spec.md docs/superpowers/specs/2026-04-14-notifytun-design.md
git commit -m "$(cat <<'EOF'
docs: document four-agent remote-setup, emit-hook, and error log

README now lists Claude Code, Codex CLI, Gemini CLI, OpenCode and points
to ~/.notifytun/notifytun-errors.log. rough-spec's §15-17 example hook
commands are updated to emit-hook. The 2026-04-14 design doc marks its
remote-setup scope as superseded.

Co-Authored-By: Claude Opus 4.7 (1M context) <noreply@anthropic.com>
EOF
)"
```

---

## Self-review checklist (for the implementer, not a subagent)

Before opening the branch for final review, run:

- `go test ./... -count=1 -race` — all green.
- `go vet ./...` — clean.
- `git log --oneline` — 11 commits, one per task, messages readable.
- Install the binary locally and re-run `notifytun remote-setup` twice: first run writes four files; second run reports "already configured" for all four.
- `cat ~/.notifytun/notifytun-errors.log` after triggering a malformed payload end-to-end (`echo 'not-json' | notifytun emit-hook --tool claude-code --event Stop`) — one `parse` line appears.

Spec requirements map (§n of `2026-04-20-remote-setup-all-agents-design.md`):

- §4 per-tool title/body: Tasks 3 (emit-hook dispatch) + 6, 7, 8, 9 (per-tool configurators).
- §5 emit-hook adapter: Task 3.
- §6 OpenCode plugin: Task 9.
- §7 Configurator refactor + jsonhooks: Tasks 4 + 5.
- §8 always-exit-0 + error log: Tasks 1 + 2 + 3.
- §9 Claude migration: Task 6.
- §10 package layout: Tasks 1–10 cumulative.
- §11 testing: every code task includes the listed unit coverage.
- §12 docs: Task 11.
- §13 out-of-scope follow-ups: not implemented (by design).
- §14 acceptance: verified by the post-implementation smoke check above.
