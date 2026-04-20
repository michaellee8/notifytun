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
