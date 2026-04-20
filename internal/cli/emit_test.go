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
