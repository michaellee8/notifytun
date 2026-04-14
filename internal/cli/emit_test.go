package cli_test

import (
	"context"
	"path/filepath"
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

func TestEmitWithoutTitleReturnsSilentExit(t *testing.T) {
	cmd := cli.NewEmitCmd()
	cmd.SetArgs(nil)

	err := cmd.Execute()
	if err == nil {
		t.Fatal("expected error")
	}

	exitErr, ok := err.(*cli.ExitError)
	if !ok {
		t.Fatalf("expected *cli.ExitError, got %T", err)
	}
	if exitErr.Code != 1 || !exitErr.Silent {
		t.Fatalf("unexpected exit error: %+v", exitErr)
	}
}
