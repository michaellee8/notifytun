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
