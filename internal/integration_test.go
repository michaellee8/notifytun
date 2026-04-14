package internal_test

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/michaellee8/notifytun/internal/cli"
	"github.com/michaellee8/notifytun/internal/db"
	"github.com/michaellee8/notifytun/internal/proto"
	"github.com/michaellee8/notifytun/internal/socket"
)

func TestIntegrationEmitThenAttachReplay(t *testing.T) {
	dbPath, socketPath := tempIntegrationPaths(t)

	emitNotification(t, dbPath, socketPath, "Task complete", "Replay me", "codex")

	session := startAttach(t, dbPath, socketPath)
	notif := mustNotifMessage(t, session.ReadMessage(t, time.Second))
	if err := session.Close(); err != nil {
		t.Fatalf("Close(attach): %v", err)
	}

	if notif.Title != "Task complete" || notif.Body != "Replay me" || notif.Tool != "codex" {
		t.Fatalf("unexpected replayed notification: %+v", notif)
	}
	if notif.Backlog || notif.Summary {
		t.Fatalf("expected replay to remain a normal notification, got %+v", notif)
	}

	assertUndeliveredCount(t, dbPath, 0)
}

func TestIntegrationBacklogFloodControl(t *testing.T) {
	dbPath, socketPath := tempIntegrationPaths(t)

	for i := 1; i <= 4; i++ {
		emitNotification(t, dbPath, socketPath, fmt.Sprintf("Queued %d", i), "Disconnected backlog", "codex")
	}

	session := startAttach(t, dbPath, socketPath)
	var messages []any
	for i := 0; i < 5; i++ {
		messages = append(messages, session.ReadMessage(t, time.Second))
	}
	if err := session.Close(); err != nil {
		t.Fatalf("Close(attach): %v", err)
	}

	for i := 0; i < 4; i++ {
		notif := mustNotifMessage(t, messages[i])
		if !notif.Backlog || notif.Summary {
			t.Fatalf("expected backlog notification at index %d, got %+v", i, notif)
		}
	}

	summary := mustNotifMessage(t, messages[4])
	if !summary.Summary {
		t.Fatalf("expected backlog summary message, got %+v", summary)
	}
	if summary.Body != "4 notifications delivered while disconnected" {
		t.Fatalf("unexpected backlog summary body: %+v", summary)
	}

	assertUndeliveredCount(t, dbPath, 0)
}

func TestIntegrationThreeOrFewerRowsRemainNormal(t *testing.T) {
	dbPath, socketPath := tempIntegrationPaths(t)

	for i := 1; i <= 3; i++ {
		emitNotification(t, dbPath, socketPath, fmt.Sprintf("Normal %d", i), "Small backlog", "codex")
	}

	session := startAttach(t, dbPath, socketPath)
	for i := 1; i <= 3; i++ {
		notif := mustNotifMessage(t, session.ReadMessage(t, time.Second))
		if notif.Title != fmt.Sprintf("Normal %d", i) {
			t.Fatalf("unexpected notification order at index %d: %+v", i, notif)
		}
		if notif.Backlog || notif.Summary {
			t.Fatalf("expected normal replay for three-or-fewer rows, got %+v", notif)
		}
	}
	if err := session.Close(); err != nil {
		t.Fatalf("Close(attach): %v", err)
	}

	assertUndeliveredCount(t, dbPath, 0)
}

func TestIntegrationSocketWakeupReachesAttachListener(t *testing.T) {
	dbPath, socketPath := tempIntegrationPaths(t)

	session := startAttach(t, dbPath, socketPath)
	waitForSocketFile(t, socketPath, time.Second)

	emitNotification(t, dbPath, socketPath, "Live delivery", "Socket wakeup", "codex")

	notif := mustNotifMessage(t, session.ReadMessage(t, time.Second))
	if err := session.Close(); err != nil {
		t.Fatalf("Close(attach): %v", err)
	}

	if notif.Title != "Live delivery" || notif.Body != "Socket wakeup" || notif.Tool != "codex" {
		t.Fatalf("unexpected live notification: %+v", notif)
	}
	if notif.Backlog || notif.Summary {
		t.Fatalf("expected wakeup delivery to be a normal notification, got %+v", notif)
	}

	assertUndeliveredCount(t, dbPath, 0)
}

func TestIntegrationProtocolOverPipeJSONLFromAttach(t *testing.T) {
	dbPath, socketPath := tempIntegrationPaths(t)

	emitNotification(t, dbPath, socketPath, "Frame one", "Line 1\nLine 2", "codex")
	emitNotification(t, dbPath, socketPath, "Frame two", `Body with "quotes"`, "claude-code")

	session := startAttach(t, dbPath, socketPath)
	firstLine := session.ReadRawLine(t, time.Second)
	secondLine := session.ReadRawLine(t, time.Second)
	if err := session.Close(); err != nil {
		t.Fatalf("Close(attach): %v", err)
	}

	for i, line := range [][]byte{firstLine, secondLine} {
		if len(line) == 0 || line[len(line)-1] != '\n' {
			t.Fatalf("frame %d missing newline terminator: %q", i, string(line))
		}
		if bytes.Count(line, []byte{'\n'}) != 1 {
			t.Fatalf("frame %d should contain exactly one transport newline, got %q", i, string(line))
		}
	}

	firstNotif := mustNotifMessage(t, decodeJSONLFrame(t, firstLine))
	secondNotif := mustNotifMessage(t, decodeJSONLFrame(t, secondLine))

	if firstNotif.Title != "Frame one" || firstNotif.Body != "Line 1\nLine 2" || firstNotif.Tool != "codex" {
		t.Fatalf("unexpected first frame from attach: %+v", firstNotif)
	}
	if secondNotif.Title != "Frame two" || secondNotif.Body != `Body with "quotes"` || secondNotif.Tool != "claude-code" {
		t.Fatalf("unexpected second frame from attach: %+v", secondNotif)
	}

	assertUndeliveredCount(t, dbPath, 0)
}

type attachSession struct {
	cancel     context.CancelFunc
	commandErr chan error
	reader     *bufio.Reader
	readPipe   *os.File
	socketPath string
	writePipe  *os.File
	oldStdout  *os.File
	closeOnce  sync.Once
}

func startAttach(t *testing.T, dbPath, socketPath string) *attachSession {
	t.Helper()

	readPipe, writePipe, err := os.Pipe()
	if err != nil {
		t.Fatalf("os.Pipe: %v", err)
	}

	oldStdout := os.Stdout
	os.Stdout = writePipe

	ctx, cancel := context.WithCancel(context.Background())
	cmd := cli.NewAttachCmd()
	cmd.SetContext(ctx)
	cmd.SetArgs([]string{
		"--db", dbPath,
		"--socket", socketPath,
	})

	session := &attachSession{
		cancel:     cancel,
		commandErr: make(chan error, 1),
		reader:     bufio.NewReader(readPipe),
		readPipe:   readPipe,
		socketPath: socketPath,
		writePipe:  writePipe,
		oldStdout:  oldStdout,
	}

	go func() {
		session.commandErr <- cmd.Execute()
	}()

	t.Cleanup(func() {
		_ = session.Close()
	})

	return session
}

func (s *attachSession) ReadMessage(t *testing.T, timeout time.Duration) any {
	t.Helper()

	return readDecodedLine(t, s.reader, timeout)
}

func (s *attachSession) ReadRawLine(t *testing.T, timeout time.Duration) []byte {
	t.Helper()

	return readJSONLLine(t, s.reader, timeout)
}

func (s *attachSession) Close() error {
	var closeErr error

	s.closeOnce.Do(func() {
		s.cancel()
		_ = socket.SendWakeup(s.socketPath)

		select {
		case err := <-s.commandErr:
			if err != nil {
				closeErr = err
			}
		case <-time.After(2 * time.Second):
			closeErr = fmt.Errorf("timed out waiting for attach to exit")
		}

		os.Stdout = s.oldStdout

		if err := s.writePipe.Close(); err != nil && !errors.Is(err, os.ErrClosed) && closeErr == nil {
			closeErr = err
		}
		if err := s.readPipe.Close(); err != nil && !errors.Is(err, os.ErrClosed) && closeErr == nil {
			closeErr = err
		}
	})

	return closeErr
}

func tempIntegrationPaths(t *testing.T) (dbPath, socketPath string) {
	t.Helper()

	dir := t.TempDir()
	return filepath.Join(dir, "notifytun.db"), filepath.Join(dir, "notifytun.sock")
}

func emitNotification(t *testing.T, dbPath, socketPath, title, body, tool string) {
	t.Helper()

	cmd := cli.NewEmitCmd()
	cmd.SetArgs([]string{
		"--title", title,
		"--body", body,
		"--tool", tool,
		"--db", dbPath,
		"--socket", socketPath,
	})

	if err := cmd.Execute(); err != nil {
		t.Fatalf("Execute(emit): %v", err)
	}
}

func readDecodedLine(t *testing.T, reader *bufio.Reader, timeout time.Duration) any {
	t.Helper()

	return decodeJSONLFrame(t, readJSONLLine(t, reader, timeout))
}

func readJSONLLine(t *testing.T, reader *bufio.Reader, timeout time.Duration) []byte {
	t.Helper()

	type result struct {
		line []byte
		err  error
	}

	resultCh := make(chan result, 1)
	go func() {
		line, err := reader.ReadBytes('\n')
		resultCh <- result{line: line, err: err}
	}()

	select {
	case res := <-resultCh:
		if res.err != nil {
			t.Fatalf("read JSONL line: %v", res.err)
		}
		return res.line
	case <-time.After(timeout):
		t.Fatalf("timed out waiting for protocol line after %s", timeout)
		return nil
	}
}

func decodeJSONLFrame(t *testing.T, line []byte) any {
	t.Helper()

	msg, err := proto.Decode(bytes.TrimSuffix(line, []byte{'\n'}))
	if err != nil {
		t.Fatalf("decode JSONL frame: %v", err)
	}
	if msg == nil {
		t.Fatalf("decoded nil message from %q", string(line))
	}

	return msg
}

func mustNotifMessage(t *testing.T, msg any) *proto.NotifMessage {
	t.Helper()

	notif, ok := msg.(*proto.NotifMessage)
	if !ok {
		t.Fatalf("expected *proto.NotifMessage, got %T", msg)
	}

	return notif
}

func waitForSocketFile(t *testing.T, path string, timeout time.Duration) {
	t.Helper()

	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if _, err := os.Stat(path); err == nil {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}

	t.Fatalf("timed out waiting for socket file %s", path)
}

func assertUndeliveredCount(t *testing.T, dbPath string, want int) {
	t.Helper()

	d, err := db.Open(dbPath)
	if err != nil {
		t.Fatalf("Open(db): %v", err)
	}
	defer d.Close()

	rows, err := d.QueryUndelivered()
	if err != nil {
		t.Fatalf("QueryUndelivered: %v", err)
	}
	if len(rows) != want {
		t.Fatalf("expected %d undelivered rows, got %d", want, len(rows))
	}
}
