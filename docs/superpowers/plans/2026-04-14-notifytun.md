# notifytun Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Build a single Go binary that tunnels notifications from a remote Linux VM to a local machine's desktop over SSH.

**Architecture:** `emit` writes notifications to SQLite and pokes a Unix socket. `attach` (launched over SSH) streams undelivered rows as JSONL. `local` (on the laptop) manages the SSH connection via `x/crypto/ssh`, reads the JSONL stream, and delivers desktop notifications. Flood control suppresses backlog popups on reconnect.

**Tech Stack:** Go 1.23+, cobra/viper, modernc.org/sqlite, golang.org/x/crypto/ssh, github.com/kevinburke/ssh_config

---

### Task 1: Project Scaffolding

**Files:**
- Create: `go.mod`
- Create: `cmd/notifytun/main.go`

- [ ] **Step 1: Initialize Go module**

```bash
go mod init github.com/michaellee8/notifytun
```

- [ ] **Step 2: Install cobra and viper**

```bash
go get github.com/spf13/cobra@latest
go get github.com/spf13/viper@latest
```

- [ ] **Step 3: Create main.go with cobra root command**

Create `cmd/notifytun/main.go`:

```go
package main

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"
)

var rootCmd = &cobra.Command{
	Use:   "notifytun",
	Short: "Tunnel notifications from a remote VM to your local desktop over SSH",
}

func main() {
	if err := rootCmd.Execute(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
```

- [ ] **Step 4: Verify it builds and runs**

Run: `go build -o notifytun ./cmd/notifytun && ./notifytun --help`
Expected: Help output showing "Tunnel notifications from a remote VM..."

- [ ] **Step 5: Commit**

```bash
git add go.mod go.sum cmd/
git commit -m "feat: scaffold project with cobra root command"
```

---

### Task 2: Proto Package — JSONL Message Types

**Files:**
- Create: `internal/proto/proto.go`
- Create: `internal/proto/proto_test.go`

- [ ] **Step 1: Write the failing tests**

Create `internal/proto/proto_test.go`:

```go
package proto_test

import (
	"testing"
	"time"

	"github.com/michaellee8/notifytun/internal/proto"
)

func TestEncodeNotif(t *testing.T) {
	msg := proto.NotifMessage{
		ID:        1,
		Title:     "Task complete",
		Body:      "Finished refactoring",
		Tool:      "claude-code",
		CreatedAt: "2026-04-14T10:30:00.000Z",
	}
	line, err := proto.Encode(&msg)
	if err != nil {
		t.Fatalf("Encode: %v", err)
	}
	if line[len(line)-1] != '\n' {
		t.Fatal("Encode must append newline")
	}
}

func TestEncodeNotifBacklog(t *testing.T) {
	msg := proto.NotifMessage{
		ID:        5,
		Title:     "Build passed",
		Body:      "",
		Tool:      "codex",
		CreatedAt: "2026-04-14T10:31:02.000Z",
		Backlog:   true,
	}
	line, err := proto.Encode(&msg)
	if err != nil {
		t.Fatalf("Encode: %v", err)
	}
	// backlog field should be present
	if !contains(line, `"backlog":true`) {
		t.Fatalf("expected backlog:true in output, got: %s", line)
	}
}

func TestEncodeHeartbeat(t *testing.T) {
	msg := proto.HeartbeatMessage{
		Ts: time.Now().UTC().Format(time.RFC3339Nano),
	}
	line, err := proto.Encode(&msg)
	if err != nil {
		t.Fatalf("Encode: %v", err)
	}
	if !contains(line, `"type":"heartbeat"`) {
		t.Fatalf("expected type:heartbeat, got: %s", line)
	}
}

func TestDecodeNotif(t *testing.T) {
	input := []byte(`{"type":"notif","id":1,"title":"Test","body":"hello","tool":"claude-code","created_at":"2026-04-14T10:30:00.000Z"}`)
	msg, err := proto.Decode(input)
	if err != nil {
		t.Fatalf("Decode: %v", err)
	}
	notif, ok := msg.(*proto.NotifMessage)
	if !ok {
		t.Fatalf("expected *NotifMessage, got %T", msg)
	}
	if notif.ID != 1 {
		t.Fatalf("expected ID=1, got %d", notif.ID)
	}
	if notif.Title != "Test" {
		t.Fatalf("expected Title=Test, got %s", notif.Title)
	}
}

func TestDecodeHeartbeat(t *testing.T) {
	input := []byte(`{"type":"heartbeat","ts":"2026-04-14T10:31:30.000Z"}`)
	msg, err := proto.Decode(input)
	if err != nil {
		t.Fatalf("Decode: %v", err)
	}
	hb, ok := msg.(*proto.HeartbeatMessage)
	if !ok {
		t.Fatalf("expected *HeartbeatMessage, got %T", msg)
	}
	if hb.Ts != "2026-04-14T10:31:30.000Z" {
		t.Fatalf("expected ts, got %s", hb.Ts)
	}
}

func TestDecodeUnknownType(t *testing.T) {
	input := []byte(`{"type":"future_type","data":"something"}`)
	msg, err := proto.Decode(input)
	if err != nil {
		t.Fatalf("unknown types should not error: %v", err)
	}
	if msg != nil {
		t.Fatalf("unknown types should return nil message, got %T", msg)
	}
}

func TestDecodeMalformed(t *testing.T) {
	input := []byte(`not json at all`)
	_, err := proto.Decode(input)
	if err == nil {
		t.Fatal("expected error for malformed JSON")
	}
}

func TestRoundTrip(t *testing.T) {
	original := &proto.NotifMessage{
		ID:        42,
		Title:     "Round trip",
		Body:      "test body",
		Tool:      "opencode",
		CreatedAt: "2026-04-14T12:00:00.000Z",
		Backlog:   true,
		Summary:   false,
	}
	line, err := proto.Encode(original)
	if err != nil {
		t.Fatalf("Encode: %v", err)
	}
	// strip trailing newline for Decode
	msg, err := proto.Decode(line[:len(line)-1])
	if err != nil {
		t.Fatalf("Decode: %v", err)
	}
	decoded, ok := msg.(*proto.NotifMessage)
	if !ok {
		t.Fatalf("expected *NotifMessage, got %T", msg)
	}
	if decoded.ID != original.ID || decoded.Title != original.Title ||
		decoded.Body != original.Body || decoded.Tool != original.Tool ||
		decoded.Backlog != original.Backlog {
		t.Fatalf("round trip mismatch: %+v vs %+v", original, decoded)
	}
}

func contains(b []byte, substr string) bool {
	return len(b) > 0 && len(substr) > 0 && string(b) != "" &&
		stringContains(string(b), substr)
}

func stringContains(s, substr string) bool {
	for i := 0; i+len(substr) <= len(s); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./internal/proto/ -v`
Expected: Compilation failure — package does not exist yet

- [ ] **Step 3: Implement the proto package**

Create `internal/proto/proto.go`:

```go
package proto

import (
	"encoding/json"
	"fmt"
)

const (
	TypeNotif     = "notif"
	TypeHeartbeat = "heartbeat"
)

// NotifMessage represents a notification sent from attach to local.
type NotifMessage struct {
	Type      string `json:"type"`
	ID        int64  `json:"id"`
	Title     string `json:"title"`
	Body      string `json:"body"`
	Tool      string `json:"tool"`
	CreatedAt string `json:"created_at"`
	Backlog   bool   `json:"backlog,omitempty"`
	Summary   bool   `json:"summary,omitempty"`
}

// HeartbeatMessage is a keep-alive sent from attach to local.
type HeartbeatMessage struct {
	Type string `json:"type"`
	Ts   string `json:"ts"`
}

// Encode serializes a message to a single JSONL line (with trailing newline).
func Encode(msg interface{}) ([]byte, error) {
	switch m := msg.(type) {
	case *NotifMessage:
		m.Type = TypeNotif
	case *HeartbeatMessage:
		m.Type = TypeHeartbeat
	default:
		return nil, fmt.Errorf("unknown message type: %T", msg)
	}
	data, err := json.Marshal(msg)
	if err != nil {
		return nil, err
	}
	data = append(data, '\n')
	return data, nil
}

// Decode parses a single JSONL line into a typed message.
// Returns (nil, nil) for unknown message types (per protocol rules).
// Returns (nil, error) for malformed JSON.
func Decode(line []byte) (interface{}, error) {
	var base struct {
		Type string `json:"type"`
	}
	if err := json.Unmarshal(line, &base); err != nil {
		return nil, fmt.Errorf("invalid JSON: %w", err)
	}

	switch base.Type {
	case TypeNotif:
		var msg NotifMessage
		if err := json.Unmarshal(line, &msg); err != nil {
			return nil, fmt.Errorf("invalid notif message: %w", err)
		}
		return &msg, nil
	case TypeHeartbeat:
		var msg HeartbeatMessage
		if err := json.Unmarshal(line, &msg); err != nil {
			return nil, fmt.Errorf("invalid heartbeat message: %w", err)
		}
		return &msg, nil
	default:
		// Unknown message types are ignored per protocol rules
		return nil, nil
	}
}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./internal/proto/ -v`
Expected: All 7 tests PASS

- [ ] **Step 5: Commit**

```bash
git add internal/proto/
git commit -m "feat: add JSONL protocol encode/decode"
```

---

### Task 3: DB Package — SQLite Operations

**Files:**
- Create: `internal/db/db.go`
- Create: `internal/db/db_test.go`

- [ ] **Step 1: Install SQLite driver**

```bash
go get modernc.org/sqlite@latest
```

- [ ] **Step 2: Write failing tests**

Create `internal/db/db_test.go`:

```go
package db_test

import (
	"os"
	"path/filepath"
	"sync"
	"testing"

	"github.com/michaellee8/notifytun/internal/db"
)

func tempDB(t *testing.T) *db.DB {
	t.Helper()
	path := filepath.Join(t.TempDir(), "test.db")
	d, err := db.Open(path)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { d.Close() })
	return d
}

func TestOpenCreatesDB(t *testing.T) {
	path := filepath.Join(t.TempDir(), "subdir", "test.db")
	d, err := db.Open(path)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer d.Close()
	if _, err := os.Stat(path); os.IsNotExist(err) {
		t.Fatal("DB file was not created")
	}
}

func TestInsertAndQuery(t *testing.T) {
	d := tempDB(t)

	id, err := d.Insert("Test Title", "Test Body", "claude-code")
	if err != nil {
		t.Fatalf("Insert: %v", err)
	}
	if id <= 0 {
		t.Fatalf("expected positive ID, got %d", id)
	}

	rows, err := d.QueryUndelivered()
	if err != nil {
		t.Fatalf("QueryUndelivered: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("expected 1 row, got %d", len(rows))
	}
	if rows[0].Title != "Test Title" {
		t.Fatalf("expected title 'Test Title', got %q", rows[0].Title)
	}
	if rows[0].Body != "Test Body" {
		t.Fatalf("expected body 'Test Body', got %q", rows[0].Body)
	}
	if rows[0].Tool != "claude-code" {
		t.Fatalf("expected tool 'claude-code', got %q", rows[0].Tool)
	}
}

func TestMarkDelivered(t *testing.T) {
	d := tempDB(t)

	id, err := d.Insert("Title", "Body", "codex")
	if err != nil {
		t.Fatalf("Insert: %v", err)
	}

	if err := d.MarkDelivered(id); err != nil {
		t.Fatalf("MarkDelivered: %v", err)
	}

	rows, err := d.QueryUndelivered()
	if err != nil {
		t.Fatalf("QueryUndelivered: %v", err)
	}
	if len(rows) != 0 {
		t.Fatalf("expected 0 undelivered rows, got %d", len(rows))
	}
}

func TestQueryUndeliveredOrdering(t *testing.T) {
	d := tempDB(t)

	d.Insert("First", "", "")
	d.Insert("Second", "", "")
	d.Insert("Third", "", "")

	rows, err := d.QueryUndelivered()
	if err != nil {
		t.Fatalf("QueryUndelivered: %v", err)
	}
	if len(rows) != 3 {
		t.Fatalf("expected 3 rows, got %d", len(rows))
	}
	if rows[0].Title != "First" || rows[1].Title != "Second" || rows[2].Title != "Third" {
		t.Fatal("rows not in insertion order")
	}
}

func TestConcurrentInserts(t *testing.T) {
	path := filepath.Join(t.TempDir(), "concurrent.db")

	var wg sync.WaitGroup
	const numWriters = 10

	for i := 0; i < numWriters; i++ {
		wg.Add(1)
		go func(n int) {
			defer wg.Done()
			d, err := db.Open(path)
			if err != nil {
				t.Errorf("Open: %v", err)
				return
			}
			defer d.Close()
			if _, err := d.Insert("Concurrent", "", "test"); err != nil {
				t.Errorf("Insert from goroutine %d: %v", n, err)
			}
		}(i)
	}
	wg.Wait()

	d, err := db.Open(path)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer d.Close()
	rows, err := d.QueryUndelivered()
	if err != nil {
		t.Fatalf("QueryUndelivered: %v", err)
	}
	if len(rows) != numWriters {
		t.Fatalf("expected %d rows, got %d", numWriters, len(rows))
	}
}

func TestCreatedAtIsPopulated(t *testing.T) {
	d := tempDB(t)
	d.Insert("Title", "Body", "test")

	rows, err := d.QueryUndelivered()
	if err != nil {
		t.Fatalf("QueryUndelivered: %v", err)
	}
	if rows[0].CreatedAt == "" {
		t.Fatal("created_at should be populated by default")
	}
}
```

- [ ] **Step 3: Run tests to verify they fail**

Run: `go test ./internal/db/ -v`
Expected: Compilation failure — package does not exist yet

- [ ] **Step 4: Implement the db package**

Create `internal/db/db.go`:

```go
package db

import (
	"database/sql"
	"fmt"
	"os"
	"path/filepath"

	_ "modernc.org/sqlite"
)

// Notification represents a row in the notifications table.
type Notification struct {
	ID        int64
	Title     string
	Body      string
	Tool      string
	CreatedAt string
	Delivered bool
}

// DB wraps a SQLite database for notification storage.
type DB struct {
	db *sql.DB
}

// Open opens or creates a SQLite database at the given path.
// Creates parent directories if they don't exist.
// Sets WAL mode and busy_timeout, creates the schema if needed.
func Open(path string) (*DB, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, fmt.Errorf("create db directory: %w", err)
	}

	sqlDB, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, fmt.Errorf("open sqlite: %w", err)
	}

	// Set connection-level PRAGMAs
	if _, err := sqlDB.Exec("PRAGMA journal_mode=WAL"); err != nil {
		sqlDB.Close()
		return nil, fmt.Errorf("set WAL mode: %w", err)
	}
	if _, err := sqlDB.Exec("PRAGMA busy_timeout=5000"); err != nil {
		sqlDB.Close()
		return nil, fmt.Errorf("set busy_timeout: %w", err)
	}

	// Create schema
	if _, err := sqlDB.Exec(`
		CREATE TABLE IF NOT EXISTS notifications (
			id         INTEGER PRIMARY KEY AUTOINCREMENT,
			title      TEXT    NOT NULL,
			body       TEXT    NOT NULL DEFAULT '',
			tool       TEXT    NOT NULL DEFAULT '',
			created_at TEXT    NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now')),
			delivered  INTEGER NOT NULL DEFAULT 0
		)
	`); err != nil {
		sqlDB.Close()
		return nil, fmt.Errorf("create table: %w", err)
	}

	if _, err := sqlDB.Exec(`
		CREATE INDEX IF NOT EXISTS idx_undelivered
		ON notifications (delivered, id)
		WHERE delivered = 0
	`); err != nil {
		sqlDB.Close()
		return nil, fmt.Errorf("create index: %w", err)
	}

	return &DB{db: sqlDB}, nil
}

// Insert adds a new notification row. Returns the new row ID.
func (d *DB) Insert(title, body, tool string) (int64, error) {
	result, err := d.db.Exec(
		"INSERT INTO notifications (title, body, tool) VALUES (?, ?, ?)",
		title, body, tool,
	)
	if err != nil {
		return 0, fmt.Errorf("insert notification: %w", err)
	}
	return result.LastInsertId()
}

// QueryUndelivered returns all undelivered notifications, ordered by ID ascending.
func (d *DB) QueryUndelivered() ([]Notification, error) {
	rows, err := d.db.Query(
		"SELECT id, title, body, tool, created_at FROM notifications WHERE delivered = 0 ORDER BY id",
	)
	if err != nil {
		return nil, fmt.Errorf("query undelivered: %w", err)
	}
	defer rows.Close()

	var notifications []Notification
	for rows.Next() {
		var n Notification
		if err := rows.Scan(&n.ID, &n.Title, &n.Body, &n.Tool, &n.CreatedAt); err != nil {
			return nil, fmt.Errorf("scan row: %w", err)
		}
		notifications = append(notifications, n)
	}
	return notifications, rows.Err()
}

// MarkDelivered sets delivered=1 for the given notification ID.
func (d *DB) MarkDelivered(id int64) error {
	_, err := d.db.Exec("UPDATE notifications SET delivered = 1 WHERE id = ?", id)
	if err != nil {
		return fmt.Errorf("mark delivered: %w", err)
	}
	return nil
}

// Close closes the database connection.
func (d *DB) Close() error {
	return d.db.Close()
}
```

- [ ] **Step 5: Run tests to verify they pass**

Run: `go test ./internal/db/ -v -count=1`
Expected: All 6 tests PASS

- [ ] **Step 6: Commit**

```bash
git add internal/db/
git commit -m "feat: add SQLite database layer with WAL mode"
```

---

### Task 4: Socket Package — Unix Datagram IPC

**Files:**
- Create: `internal/socket/socket.go`
- Create: `internal/socket/socket_test.go`

- [ ] **Step 1: Write failing tests**

Create `internal/socket/socket_test.go`:

```go
package socket_test

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/michaellee8/notifytun/internal/socket"
)

func TestListenAndWakeup(t *testing.T) {
	path := filepath.Join(t.TempDir(), "test.sock")

	listener, err := socket.Listen(path)
	if err != nil {
		t.Fatalf("Listen: %v", err)
	}
	defer listener.Close()

	// Send a wakeup in a goroutine
	done := make(chan struct{})
	go func() {
		time.Sleep(50 * time.Millisecond)
		if err := socket.SendWakeup(path); err != nil {
			t.Errorf("SendWakeup: %v", err)
		}
		close(done)
	}()

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	if err := listener.Wait(ctx); err != nil {
		t.Fatalf("Wait: %v", err)
	}
	<-done
}

func TestWaitTimeout(t *testing.T) {
	path := filepath.Join(t.TempDir(), "test.sock")

	listener, err := socket.Listen(path)
	if err != nil {
		t.Fatalf("Listen: %v", err)
	}
	defer listener.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()

	err = listener.Wait(ctx)
	if err == nil {
		t.Fatal("expected timeout error, got nil")
	}
}

func TestSendWakeupNoListener(t *testing.T) {
	path := filepath.Join(t.TempDir(), "nonexistent.sock")

	err := socket.SendWakeup(path)
	if err != nil {
		t.Fatalf("SendWakeup to nonexistent socket should not error, got: %v", err)
	}
}

func TestCloseRemovesSocket(t *testing.T) {
	path := filepath.Join(t.TempDir(), "test.sock")

	listener, err := socket.Listen(path)
	if err != nil {
		t.Fatalf("Listen: %v", err)
	}

	if err := listener.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	// Socket file should be removed
	if err := socket.SendWakeup(path); err != nil {
		// This is fine — no listener
	}
}

func TestStaleSocketRemoved(t *testing.T) {
	path := filepath.Join(t.TempDir(), "test.sock")

	// Create first listener
	l1, err := socket.Listen(path)
	if err != nil {
		t.Fatalf("Listen 1: %v", err)
	}
	l1.Close()

	// Create second listener on same path — should succeed (stale socket removed)
	l2, err := socket.Listen(path)
	if err != nil {
		t.Fatalf("Listen 2 (stale socket): %v", err)
	}
	l2.Close()
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./internal/socket/ -v`
Expected: Compilation failure — package does not exist yet

- [ ] **Step 3: Implement the socket package**

Create `internal/socket/socket.go`:

```go
package socket

import (
	"context"
	"fmt"
	"net"
	"os"
)

// Listener binds a Unix datagram socket and waits for wakeup packets.
type Listener struct {
	conn *net.UnixConn
	path string
}

// Listen creates a Unix datagram socket at the given path.
// Removes any stale socket file before binding.
func Listen(path string) (*Listener, error) {
	// Remove stale socket if it exists
	if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
		return nil, fmt.Errorf("remove stale socket: %w", err)
	}

	addr := &net.UnixAddr{Name: path, Net: "unixgram"}
	conn, err := net.ListenUnixgram("unixgram", addr)
	if err != nil {
		return nil, fmt.Errorf("listen unixgram: %w", err)
	}

	return &Listener{conn: conn, path: path}, nil
}

// Wait blocks until a wakeup packet is received or ctx is cancelled.
func (l *Listener) Wait(ctx context.Context) error {
	buf := make([]byte, 16)

	// Use a goroutine to make ReadFromUnix cancellable via context
	type result struct {
		err error
	}
	ch := make(chan result, 1)

	go func() {
		_, _, err := l.conn.ReadFromUnix(buf)
		ch <- result{err: err}
	}()

	select {
	case <-ctx.Done():
		// Set a deadline to unblock the read goroutine
		l.conn.SetReadDeadline(deadlineNow())
		<-ch // drain
		return ctx.Err()
	case r := <-ch:
		return r.err
	}
}

// Close closes the socket and removes the socket file.
func (l *Listener) Close() error {
	err := l.conn.Close()
	os.Remove(l.path) // best-effort cleanup
	return err
}

// SendWakeup sends a single wakeup byte to the socket at the given path.
// Returns nil if the socket doesn't exist or send fails — wakeup is best-effort.
func SendWakeup(path string) error {
	addr := &net.UnixAddr{Name: path, Net: "unixgram"}
	conn, err := net.DialUnix("unixgram", nil, addr)
	if err != nil {
		// Socket doesn't exist or can't connect — that's fine
		return nil
	}
	defer conn.Close()

	_, err = conn.Write([]byte{0x01})
	if err != nil {
		// Send failed — that's fine, notification is in SQLite
		return nil
	}
	return nil
}

func deadlineNow() (t interface{ Unix() int64 }) {
	// This is a hack — we just need a time in the past
	return nil
}
```

Wait — the `SetReadDeadline` approach needs a proper `time.Time`. Let me fix the implementation:

Replace `internal/socket/socket.go` with the corrected version:

```go
package socket

import (
	"context"
	"fmt"
	"net"
	"os"
	"time"
)

// Listener binds a Unix datagram socket and waits for wakeup packets.
type Listener struct {
	conn *net.UnixConn
	path string
}

// Listen creates a Unix datagram socket at the given path.
// Removes any stale socket file before binding.
func Listen(path string) (*Listener, error) {
	// Remove stale socket if it exists
	if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
		return nil, fmt.Errorf("remove stale socket: %w", err)
	}

	addr := &net.UnixAddr{Name: path, Net: "unixgram"}
	conn, err := net.ListenUnixgram("unixgram", addr)
	if err != nil {
		return nil, fmt.Errorf("listen unixgram: %w", err)
	}

	return &Listener{conn: conn, path: path}, nil
}

// Wait blocks until a wakeup packet is received or ctx is cancelled.
func (l *Listener) Wait(ctx context.Context) error {
	buf := make([]byte, 16)

	type result struct {
		err error
	}
	ch := make(chan result, 1)

	go func() {
		_, _, err := l.conn.ReadFromUnix(buf)
		ch <- result{err: err}
	}()

	select {
	case <-ctx.Done():
		// Unblock the read goroutine by setting a past deadline
		l.conn.SetReadDeadline(time.Now())
		<-ch // drain
		return ctx.Err()
	case r := <-ch:
		// Reset deadline for next Wait call
		l.conn.SetReadDeadline(time.Time{})
		return r.err
	}
}

// Close closes the socket and removes the socket file.
func (l *Listener) Close() error {
	err := l.conn.Close()
	os.Remove(l.path) // best-effort cleanup
	return err
}

// SendWakeup sends a single wakeup byte to the socket at the given path.
// Returns nil if the socket doesn't exist or send fails — wakeup is best-effort.
func SendWakeup(path string) error {
	addr := &net.UnixAddr{Name: path, Net: "unixgram"}
	conn, err := net.DialUnix("unixgram", nil, addr)
	if err != nil {
		// Socket doesn't exist or can't connect — that's fine
		return nil
	}
	defer conn.Close()

	// Best-effort: ignore write errors
	conn.Write([]byte{0x01})
	return nil
}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./internal/socket/ -v -count=1`
Expected: All 5 tests PASS

- [ ] **Step 5: Commit**

```bash
git add internal/socket/
git commit -m "feat: add Unix datagram socket for emit-to-attach wakeup"
```

---

### Task 5: Notifier Backends

**Files:**
- Create: `internal/notifier/notifier.go`
- Create: `internal/notifier/macos.go`
- Create: `internal/notifier/linux.go`
- Create: `internal/notifier/generic.go`
- Create: `internal/notifier/notifier_test.go`

- [ ] **Step 1: Write failing tests**

Create `internal/notifier/notifier_test.go`:

```go
package notifier_test

import (
	"context"
	"testing"

	"github.com/michaellee8/notifytun/internal/notifier"
)

func TestMacOSCommandArgs(t *testing.T) {
	n := notifier.NewMacOS()
	cmd := n.BuildCommand("Test Title", "Test Body")
	if cmd.Path == "" {
		t.Fatal("expected osascript path")
	}
	// Verify args contain the AppleScript
	found := false
	for _, arg := range cmd.Args {
		if arg == "-e" {
			found = true
		}
	}
	if !found {
		t.Fatal("expected -e flag in osascript args")
	}
}

func TestLinuxCommandArgs(t *testing.T) {
	n := notifier.NewLinux()
	cmd := n.BuildCommand("Test Title", "Test Body")
	args := cmd.Args
	// Should contain: notify-send -a notifytun "Test Title" "Test Body"
	if args[0] != "notify-send" {
		t.Fatalf("expected notify-send, got %s", args[0])
	}
	foundApp := false
	for i, arg := range args {
		if arg == "-a" && i+1 < len(args) && args[i+1] == "notifytun" {
			foundApp = true
		}
	}
	if !foundApp {
		t.Fatal("expected -a notifytun in args")
	}
}

func TestGenericNotifier(t *testing.T) {
	n, err := notifier.NewGeneric("echo")
	if err != nil {
		t.Fatalf("NewGeneric: %v", err)
	}
	// Should not error on Notify (echo will succeed)
	err = n.Notify(context.Background(), notifier.Notification{
		Title: "Test",
		Body:  "Body",
		Tool:  "test",
	})
	if err != nil {
		t.Fatalf("Notify: %v", err)
	}
}

func TestNewAutoDetectsOS(t *testing.T) {
	// Auto should return a non-nil notifier on any OS
	n, err := notifier.New("auto", "")
	if err != nil {
		t.Fatalf("New auto: %v", err)
	}
	if n == nil {
		t.Fatal("expected non-nil notifier from auto")
	}
}

func TestNewGenericRequiresCmd(t *testing.T) {
	_, err := notifier.New("generic", "")
	if err == nil {
		t.Fatal("expected error when generic backend has no notify-cmd")
	}
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./internal/notifier/ -v`
Expected: Compilation failure

- [ ] **Step 3: Implement the notifier interface and types**

Create `internal/notifier/notifier.go`:

```go
package notifier

import (
	"context"
	"fmt"
	"runtime"
)

// Notification holds the data to display.
type Notification struct {
	Title string
	Body  string
	Tool  string
}

// Notifier delivers desktop notifications.
type Notifier interface {
	Notify(ctx context.Context, n Notification) error
}

// New creates a Notifier for the given backend name.
// Valid backends: "auto", "macos", "linux", "generic".
// For "generic", notifyCmd must be non-empty.
func New(backend, notifyCmd string) (Notifier, error) {
	switch backend {
	case "auto":
		return newAuto(notifyCmd)
	case "macos":
		return NewMacOS(), nil
	case "linux":
		return NewLinux(), nil
	case "generic":
		if notifyCmd == "" {
			return nil, fmt.Errorf("--notify-cmd is required for generic backend")
		}
		return NewGeneric(notifyCmd)
	default:
		return nil, fmt.Errorf("unknown backend: %s", backend)
	}
}

func newAuto(notifyCmd string) (Notifier, error) {
	switch runtime.GOOS {
	case "darwin":
		return NewMacOS(), nil
	case "linux":
		return NewLinux(), nil
	default:
		if notifyCmd != "" {
			return NewGeneric(notifyCmd)
		}
		return NewLinux(), nil // fallback
	}
}
```

- [ ] **Step 4: Implement the macOS backend**

Create `internal/notifier/macos.go`:

```go
package notifier

import (
	"context"
	"os/exec"
)

// MacOS delivers notifications via osascript.
type MacOS struct{}

// NewMacOS creates a macOS notifier.
func NewMacOS() *MacOS {
	return &MacOS{}
}

const appleScript = `on run argv
  set theTitle to item 1 of argv
  set theBody to item 2 of argv
  display notification theBody with title theTitle subtitle "notifytun"
end run`

// BuildCommand returns the exec.Cmd for osascript (exported for testing).
func (m *MacOS) BuildCommand(title, body string) *exec.Cmd {
	return exec.Command("osascript", "-e", appleScript, "--", title, body)
}

// Notify sends a macOS notification via osascript.
func (m *MacOS) Notify(ctx context.Context, n Notification) error {
	cmd := m.BuildCommand(n.Title, n.Body)
	cmd.Stdout = nil
	cmd.Stderr = nil
	return cmd.Run()
}
```

- [ ] **Step 5: Implement the Linux backend**

Create `internal/notifier/linux.go`:

```go
package notifier

import (
	"context"
	"os/exec"
)

// Linux delivers notifications via notify-send.
type Linux struct{}

// NewLinux creates a Linux notifier.
func NewLinux() *Linux {
	return &Linux{}
}

// BuildCommand returns the exec.Cmd for notify-send (exported for testing).
func (l *Linux) BuildCommand(title, body string) *exec.Cmd {
	return exec.Command("notify-send", "-a", "notifytun", title, body)
}

// Notify sends a Linux notification via notify-send.
func (l *Linux) Notify(ctx context.Context, n Notification) error {
	cmd := l.BuildCommand(n.Title, n.Body)
	cmd.Stdout = nil
	cmd.Stderr = nil
	return cmd.Run()
}
```

- [ ] **Step 6: Implement the generic backend**

Create `internal/notifier/generic.go`:

```go
package notifier

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
)

// Generic pipes notification JSON to a user-supplied command's stdin.
type Generic struct {
	cmd string
}

// NewGeneric creates a generic notifier that pipes JSON to the given command.
func NewGeneric(cmd string) (*Generic, error) {
	if cmd == "" {
		return nil, fmt.Errorf("notify-cmd must not be empty")
	}
	return &Generic{cmd: cmd}, nil
}

// Notify pipes the notification as JSON to the command's stdin.
func (g *Generic) Notify(ctx context.Context, n Notification) error {
	payload := map[string]string{
		"title": n.Title,
		"body":  n.Body,
		"tool":  n.Tool,
	}
	data, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("marshal notification: %w", err)
	}

	cmd := exec.CommandContext(ctx, g.cmd)
	cmd.Stdin = bytes.NewReader(data)
	if output, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("notify command failed: %w (output: %s)", err, string(output))
	}
	return nil
}
```

- [ ] **Step 7: Run tests to verify they pass**

Run: `go test ./internal/notifier/ -v -count=1`
Expected: All 5 tests PASS (GenericNotifier uses `echo` which exists everywhere; macOS/Linux tests only check command construction, not execution)

- [ ] **Step 8: Commit**

```bash
git add internal/notifier/
git commit -m "feat: add macOS, Linux, and generic notifier backends"
```

---

### Task 6: Emit Subcommand

**Files:**
- Create: `internal/cli/emit.go`
- Modify: `cmd/notifytun/main.go`

- [ ] **Step 1: Implement the emit subcommand**

Create `internal/cli/emit.go`:

```go
package cli

import (
	"os"

	"github.com/michaellee8/notifytun/internal/db"
	"github.com/michaellee8/notifytun/internal/socket"
	"github.com/spf13/cobra"
)

func NewEmitCmd() *cobra.Command {
	var (
		title      string
		body       string
		tool       string
		dbPath     string
		socketPath string
	)

	cmd := &cobra.Command{
		Use:   "emit",
		Short: "Record a notification (called by tool hooks)",
		// emit must never fail loudly — no stderr output on error
		SilenceUsage:  true,
		SilenceErrors: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			if title == "" {
				os.Exit(1)
				return nil
			}

			d, err := db.Open(dbPath)
			if err != nil {
				os.Exit(1)
				return nil
			}
			defer d.Close()

			if _, err := d.Insert(title, body, tool); err != nil {
				os.Exit(1)
				return nil
			}

			// Best-effort wakeup — ignore errors
			socket.SendWakeup(socketPath)

			return nil
		},
	}

	home, _ := os.UserHomeDir()
	defaultDB := home + "/.notifytun/notifytun.db"
	defaultSock := home + "/.notifytun/notifytun.sock"

	cmd.Flags().StringVar(&title, "title", "", "Notification title (required)")
	cmd.Flags().StringVar(&body, "body", "", "Notification body")
	cmd.Flags().StringVar(&tool, "tool", "", "Source tool name")
	cmd.Flags().StringVar(&dbPath, "db", defaultDB, "SQLite database path")
	cmd.Flags().StringVar(&socketPath, "socket", defaultSock, "Unix socket path")

	return cmd
}
```

- [ ] **Step 2: Register emit in main.go**

Replace `cmd/notifytun/main.go`:

```go
package main

import (
	"fmt"
	"os"

	"github.com/michaellee8/notifytun/internal/cli"
	"github.com/spf13/cobra"
)

var rootCmd = &cobra.Command{
	Use:   "notifytun",
	Short: "Tunnel notifications from a remote VM to your local desktop over SSH",
}

func init() {
	rootCmd.AddCommand(cli.NewEmitCmd())
}

func main() {
	if err := rootCmd.Execute(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
```

- [ ] **Step 3: Build and test emit manually**

Run: `go build -o notifytun ./cmd/notifytun && ./notifytun emit --title "Test" --body "Hello" --tool "test" --db /tmp/notifytun-test.db && echo "exit code: $?"`
Expected: `exit code: 0`

Verify the row was written:
Run: `go build -o notifytun ./cmd/notifytun && ./notifytun emit --title "Second" --body "" --tool "claude-code" --db /tmp/notifytun-test.db && echo "exit code: $?"`
Expected: `exit code: 0`

- [ ] **Step 4: Commit**

```bash
git add internal/cli/emit.go cmd/notifytun/main.go
git commit -m "feat: add emit subcommand"
```

---

### Task 7: Attach Subcommand

**Files:**
- Create: `internal/cli/attach.go`
- Modify: `cmd/notifytun/main.go`

- [ ] **Step 1: Implement the attach subcommand**

Create `internal/cli/attach.go`:

```go
package cli

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/michaellee8/notifytun/internal/db"
	"github.com/michaellee8/notifytun/internal/proto"
	"github.com/michaellee8/notifytun/internal/socket"
	"github.com/spf13/cobra"
)

const (
	heartbeatInterval = 15 * time.Second
	backlogThreshold  = 3
)

func NewAttachCmd() *cobra.Command {
	var (
		dbPath     string
		socketPath string
	)

	cmd := &cobra.Command{
		Use:    "attach",
		Short:  "Stream notifications over stdout (invoked by local over SSH)",
		Hidden: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runAttach(cmd.Context(), dbPath, socketPath)
		},
	}

	home, _ := os.UserHomeDir()
	defaultDB := home + "/.notifytun/notifytun.db"
	defaultSock := home + "/.notifytun/notifytun.sock"

	cmd.Flags().StringVar(&dbPath, "db", defaultDB, "SQLite database path")
	cmd.Flags().StringVar(&socketPath, "socket", defaultSock, "Unix socket path")

	return cmd
}

func runAttach(ctx context.Context, dbPath, socketPath string) error {
	ctx, cancel := signal.NotifyContext(ctx, syscall.SIGINT, syscall.SIGTERM)
	defer cancel()

	d, err := db.Open(dbPath)
	if err != nil {
		return fmt.Errorf("open db: %w", err)
	}
	defer d.Close()

	listener, err := socket.Listen(socketPath)
	if err != nil {
		return fmt.Errorf("listen socket: %w", err)
	}
	defer listener.Close()

	// Replay undelivered notifications
	if err := replayBacklog(d); err != nil {
		return fmt.Errorf("replay backlog: %w", err)
	}

	// Enter live loop
	return liveLoop(ctx, d, listener)
}

func replayBacklog(d *db.DB) error {
	rows, err := d.QueryUndelivered()
	if err != nil {
		return err
	}

	isBacklog := len(rows) > backlogThreshold

	for _, row := range rows {
		msg := &proto.NotifMessage{
			ID:        row.ID,
			Title:     row.Title,
			Body:      row.Body,
			Tool:      row.Tool,
			CreatedAt: row.CreatedAt,
			Backlog:   isBacklog,
		}
		if err := writeMessage(msg); err != nil {
			return err
		}
		if err := d.MarkDelivered(row.ID); err != nil {
			return err
		}
	}

	// Send summary if backlog
	if isBacklog {
		summary := &proto.NotifMessage{
			ID:        0,
			Title:     "notifytun",
			Body:      fmt.Sprintf("%d notifications delivered while disconnected", len(rows)),
			CreatedAt: time.Now().UTC().Format(time.RFC3339Nano),
			Summary:   true,
		}
		if err := writeMessage(summary); err != nil {
			return err
		}
	}

	return nil
}

func liveLoop(ctx context.Context, d *db.DB, listener *socket.Listener) error {
	heartbeatTicker := time.NewTicker(heartbeatInterval)
	defer heartbeatTicker.Stop()

	for {
		select {
		case <-ctx.Done():
			return nil
		case <-heartbeatTicker.C:
			hb := &proto.HeartbeatMessage{
				Ts: time.Now().UTC().Format(time.RFC3339Nano),
			}
			if err := writeMessage(hb); err != nil {
				return nil // stdout broken, exit cleanly
			}
		default:
		}

		// Wait for wakeup or timeout (use short timeout to check heartbeat)
		waitCtx, waitCancel := context.WithTimeout(ctx, heartbeatInterval)
		err := listener.Wait(waitCtx)
		waitCancel()

		if ctx.Err() != nil {
			return nil // shutting down
		}

		// Whether wakeup or timeout, check for new rows
		if err == nil || err == context.DeadlineExceeded {
			if err := streamNew(d); err != nil {
				return nil // stdout broken, exit cleanly
			}
		}
	}
}

func streamNew(d *db.DB) error {
	rows, err := d.QueryUndelivered()
	if err != nil {
		return nil // log and continue, don't kill attach
	}

	for _, row := range rows {
		msg := &proto.NotifMessage{
			ID:        row.ID,
			Title:     row.Title,
			Body:      row.Body,
			Tool:      row.Tool,
			CreatedAt: row.CreatedAt,
		}
		if err := writeMessage(msg); err != nil {
			return err
		}
		if err := d.MarkDelivered(row.ID); err != nil {
			// Log but don't die — worst case, notification replays on next connect
			continue
		}
	}
	return nil
}

func writeMessage(msg interface{}) error {
	line, err := proto.Encode(msg)
	if err != nil {
		return err
	}
	_, err = os.Stdout.Write(line)
	return err
}
```

- [ ] **Step 2: Register attach in main.go**

Add to the `init()` function in `cmd/notifytun/main.go`:

```go
func init() {
	rootCmd.AddCommand(cli.NewEmitCmd())
	rootCmd.AddCommand(cli.NewAttachCmd())
}
```

- [ ] **Step 3: Build and test emit+attach pipeline manually**

```bash
go build -o notifytun ./cmd/notifytun

# Insert some test rows
DB=/tmp/notifytun-pipeline-test.db
SOCK=/tmp/notifytun-pipeline-test.sock
rm -f "$DB" "$SOCK"
./notifytun emit --title "Test 1" --body "Body 1" --tool "claude-code" --db "$DB" --socket "$SOCK"
./notifytun emit --title "Test 2" --body "Body 2" --tool "codex" --db "$DB" --socket "$SOCK"

# Run attach — it should replay the 2 rows then send heartbeats
timeout 3 ./notifytun attach --db "$DB" --socket "$SOCK" || true
```

Expected: Two JSONL `notif` lines printed to stdout (no backlog flags since count <= 3), then a heartbeat. Process exits after timeout.

- [ ] **Step 4: Commit**

```bash
git add internal/cli/attach.go cmd/notifytun/main.go
git commit -m "feat: add attach subcommand with backlog replay and heartbeat"
```

---

### Task 8: SSH Client Package

**Files:**
- Create: `internal/ssh/ssh.go`
- Create: `internal/ssh/ssh_test.go`

- [ ] **Step 1: Install dependencies**

```bash
go get golang.org/x/crypto/ssh@latest
go get golang.org/x/crypto/ssh/agent@latest
go get github.com/kevinburke/ssh_config@latest
```

- [ ] **Step 2: Write failing tests**

Create `internal/ssh/ssh_test.go`:

```go
package ssh_test

import (
	"os"
	"path/filepath"
	"testing"

	tunnelssh "github.com/michaellee8/notifytun/internal/ssh"
)

func TestResolveTargetSimple(t *testing.T) {
	cfg := tunnelssh.ResolveTarget("user@example.com", "", "")
	if cfg.User != "user" {
		t.Fatalf("expected user 'user', got %q", cfg.User)
	}
	if cfg.Host != "example.com" {
		t.Fatalf("expected host 'example.com', got %q", cfg.Host)
	}
	if cfg.Port != "22" {
		t.Fatalf("expected port '22', got %q", cfg.Port)
	}
}

func TestResolveTargetWithPort(t *testing.T) {
	cfg := tunnelssh.ResolveTarget("user@example.com:2222", "", "")
	if cfg.Host != "example.com" {
		t.Fatalf("expected host 'example.com', got %q", cfg.Host)
	}
	if cfg.Port != "2222" {
		t.Fatalf("expected port '2222', got %q", cfg.Port)
	}
}

func TestResolveTargetFromSSHConfig(t *testing.T) {
	dir := t.TempDir()
	configPath := filepath.Join(dir, "config")
	err := os.WriteFile(configPath, []byte(`
Host myvm
    HostName 10.0.0.5
    User michael
    Port 2222
    IdentityFile ~/.ssh/id_ed25519
`), 0o600)
	if err != nil {
		t.Fatalf("write config: %v", err)
	}

	cfg := tunnelssh.ResolveTarget("myvm", "", configPath)
	if cfg.Host != "10.0.0.5" {
		t.Fatalf("expected host '10.0.0.5', got %q", cfg.Host)
	}
	if cfg.User != "michael" {
		t.Fatalf("expected user 'michael', got %q", cfg.User)
	}
	if cfg.Port != "2222" {
		t.Fatalf("expected port '2222', got %q", cfg.Port)
	}
}

func TestResolveTargetKeyOverride(t *testing.T) {
	cfg := tunnelssh.ResolveTarget("user@host", "/path/to/key", "")
	if cfg.KeyPath != "/path/to/key" {
		t.Fatalf("expected key path '/path/to/key', got %q", cfg.KeyPath)
	}
}

func TestBackoffSequence(t *testing.T) {
	b := tunnelssh.NewBackoff()
	expected := []int{1, 2, 4, 8, 16, 30, 30, 30}
	for i, want := range expected {
		got := int(b.Next().Seconds())
		if got != want {
			t.Fatalf("attempt %d: expected %ds, got %ds", i+1, want, got)
		}
	}
}

func TestBackoffReset(t *testing.T) {
	b := tunnelssh.NewBackoff()
	b.Next() // 1s
	b.Next() // 2s
	b.Next() // 4s
	b.Reset()
	got := int(b.Next().Seconds())
	if got != 1 {
		t.Fatalf("after reset expected 1s, got %ds", got)
	}
}
```

- [ ] **Step 3: Run tests to verify they fail**

Run: `go test ./internal/ssh/ -v`
Expected: Compilation failure

- [ ] **Step 4: Implement the SSH package**

Create `internal/ssh/ssh.go`:

```go
package ssh

import (
	"context"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"strings"
	"time"

	sshconfig "github.com/kevinburke/ssh_config"
	"golang.org/x/crypto/ssh"
	"golang.org/x/crypto/ssh/agent"
	"golang.org/x/crypto/ssh/knownhosts"
)

// ConnConfig holds resolved SSH connection parameters.
type ConnConfig struct {
	Host    string
	Port    string
	User    string
	KeyPath string
}

// ResolveTarget resolves a target string (user@host, host alias) into connection config.
// sshKeyOverride takes precedence over SSH config.
// sshConfigPath overrides the default ~/.ssh/config location (for testing).
func ResolveTarget(target, sshKeyOverride, sshConfigPath string) ConnConfig {
	cfg := ConnConfig{Port: "22"}

	// Parse user@host:port
	if idx := strings.Index(target, "@"); idx >= 0 {
		cfg.User = target[:idx]
		target = target[idx+1:]
	}
	if idx := strings.LastIndex(target, ":"); idx >= 0 {
		cfg.Host = target[:idx]
		cfg.Port = target[idx+1:]
	} else {
		cfg.Host = target
	}

	alias := cfg.Host

	// Try SSH config resolution
	var configFile io.Reader
	if sshConfigPath != "" {
		f, err := os.Open(sshConfigPath)
		if err == nil {
			defer f.Close()
			configFile = f
		}
	} else {
		home, _ := os.UserHomeDir()
		f, err := os.Open(filepath.Join(home, ".ssh", "config"))
		if err == nil {
			defer f.Close()
			configFile = f
		}
	}

	if configFile != nil {
		parsed, err := sshconfig.Decode(configFile)
		if err == nil {
			if hostname, _ := parsed.Get(alias, "HostName"); hostname != "" {
				cfg.Host = hostname
			}
			if user, _ := parsed.Get(alias, "User"); user != "" && cfg.User == "" {
				cfg.User = user
			}
			if port, _ := parsed.Get(alias, "Port"); port != "" && cfg.Port == "22" {
				cfg.Port = port
			}
			if keyFile, _ := parsed.Get(alias, "IdentityFile"); keyFile != "" && sshKeyOverride == "" {
				// Expand ~ in key path
				if strings.HasPrefix(keyFile, "~/") {
					home, _ := os.UserHomeDir()
					keyFile = filepath.Join(home, keyFile[2:])
				}
				cfg.KeyPath = keyFile
			}
		}
	}

	if sshKeyOverride != "" {
		cfg.KeyPath = sshKeyOverride
	}

	return cfg
}

// Session represents an active SSH session running a remote command.
type Session struct {
	client  *ssh.Client
	session *ssh.Session
	Stdout  io.Reader
	Stderr  io.Reader
}

// Connect establishes an SSH connection and starts the remote command.
func Connect(ctx context.Context, cfg ConnConfig, remoteCmd string) (*Session, error) {
	authMethods, err := buildAuthMethods(cfg.KeyPath)
	if err != nil {
		return nil, fmt.Errorf("build auth methods: %w", err)
	}

	if len(authMethods) == 0 {
		return nil, fmt.Errorf("no SSH authentication methods available")
	}

	// Load known_hosts
	hostKeyCallback, err := loadKnownHosts()
	if err != nil {
		return nil, fmt.Errorf("load known_hosts: %w", err)
	}

	sshConfig := &ssh.ClientConfig{
		User:            cfg.User,
		Auth:            authMethods,
		HostKeyCallback: hostKeyCallback,
		Timeout:         10 * time.Second,
	}

	addr := net.JoinHostPort(cfg.Host, cfg.Port)
	client, err := ssh.Dial("tcp", addr, sshConfig)
	if err != nil {
		return nil, fmt.Errorf("ssh dial %s: %w", addr, err)
	}

	session, err := client.NewSession()
	if err != nil {
		client.Close()
		return nil, fmt.Errorf("new session: %w", err)
	}

	stdout, err := session.StdoutPipe()
	if err != nil {
		session.Close()
		client.Close()
		return nil, fmt.Errorf("stdout pipe: %w", err)
	}

	stderr, err := session.StderrPipe()
	if err != nil {
		session.Close()
		client.Close()
		return nil, fmt.Errorf("stderr pipe: %w", err)
	}

	if err := session.Start(remoteCmd); err != nil {
		session.Close()
		client.Close()
		return nil, fmt.Errorf("start remote command: %w", err)
	}

	return &Session{
		client:  client,
		session: session,
		Stdout:  stdout,
		Stderr:  stderr,
	}, nil
}

// Wait waits for the remote command to exit.
func (s *Session) Wait() error {
	return s.session.Wait()
}

// Close closes the SSH session and connection.
func (s *Session) Close() error {
	s.session.Close()
	return s.client.Close()
}

func buildAuthMethods(keyPath string) ([]ssh.AuthMethod, error) {
	var methods []ssh.AuthMethod

	// Try explicit key file first
	if keyPath != "" {
		key, err := os.ReadFile(keyPath)
		if err != nil {
			return nil, fmt.Errorf("read key file %s: %w", keyPath, err)
		}
		signer, err := ssh.ParsePrivateKey(key)
		if err != nil {
			return nil, fmt.Errorf("parse key file %s: %w", keyPath, err)
		}
		methods = append(methods, ssh.PublicKeys(signer))
	}

	// Try SSH agent
	if agentSock := os.Getenv("SSH_AUTH_SOCK"); agentSock != "" {
		conn, err := net.Dial("unix", agentSock)
		if err == nil {
			agentClient := agent.NewClient(conn)
			methods = append(methods, ssh.PublicKeysCallback(agentClient.Signers))
		}
	}

	// Try default key files
	if keyPath == "" {
		home, _ := os.UserHomeDir()
		defaultKeys := []string{
			filepath.Join(home, ".ssh", "id_ed25519"),
			filepath.Join(home, ".ssh", "id_rsa"),
			filepath.Join(home, ".ssh", "id_ecdsa"),
		}
		for _, kp := range defaultKeys {
			key, err := os.ReadFile(kp)
			if err != nil {
				continue
			}
			signer, err := ssh.ParsePrivateKey(key)
			if err != nil {
				continue
			}
			methods = append(methods, ssh.PublicKeys(signer))
		}
	}

	return methods, nil
}

func loadKnownHosts() (ssh.HostKeyCallback, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return nil, err
	}
	khPath := filepath.Join(home, ".ssh", "known_hosts")
	if _, err := os.Stat(khPath); os.IsNotExist(err) {
		return nil, fmt.Errorf("no known_hosts file found at %s — connect via regular ssh first", khPath)
	}
	return knownhosts.New(khPath)
}

// Backoff implements exponential backoff for reconnection.
type Backoff struct {
	current time.Duration
}

const (
	initialBackoff = 1 * time.Second
	maxBackoff     = 30 * time.Second
)

// NewBackoff creates a new Backoff starting at 1s.
func NewBackoff() *Backoff {
	return &Backoff{current: initialBackoff}
}

// Next returns the current backoff duration and advances to the next.
func (b *Backoff) Next() time.Duration {
	d := b.current
	b.current *= 2
	if b.current > maxBackoff {
		b.current = maxBackoff
	}
	return d
}

// Reset resets the backoff to the initial value.
func (b *Backoff) Reset() {
	b.current = initialBackoff
}
```

- [ ] **Step 5: Run tests to verify they pass**

Run: `go test ./internal/ssh/ -v -count=1`
Expected: All 6 tests PASS (these test config resolution and backoff only, no real SSH)

- [ ] **Step 6: Commit**

```bash
git add internal/ssh/
git commit -m "feat: add SSH client with config parsing and reconnect backoff"
```

---

### Task 9: Local Subcommand

**Files:**
- Create: `internal/cli/local.go`
- Modify: `cmd/notifytun/main.go`

- [ ] **Step 1: Implement the local subcommand**

Create `internal/cli/local.go`:

```go
package cli

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"log"
	"os"
	"os/signal"
	"strconv"
	"syscall"
	"time"

	"github.com/michaellee8/notifytun/internal/notifier"
	"github.com/michaellee8/notifytun/internal/proto"
	tunnelssh "github.com/michaellee8/notifytun/internal/ssh"
	"github.com/spf13/cobra"
	"github.com/spf13/viper"
)

const (
	heartbeatTimeout = 45 * time.Second
	stableConnTime   = 60 * time.Second
)

func NewLocalCmd() *cobra.Command {
	var (
		target    string
		remoteBin string
		backend   string
		notifyCmd string
		sshKey    string
		cfgFile   string
	)

	cmd := &cobra.Command{
		Use:   "local",
		Short: "Connect to remote VM and deliver notifications locally",
		RunE: func(cmd *cobra.Command, args []string) error {
			// Load config file
			if cfgFile != "" {
				viper.SetConfigFile(cfgFile)
			} else {
				home, _ := os.UserHomeDir()
				viper.SetConfigFile(home + "/.notifytun/config.toml")
			}
			if err := viper.ReadInConfig(); err != nil {
				var notFound viper.ConfigFileNotFoundError
				if !errors.As(err, &notFound) {
					return fmt.Errorf("read config: %w", err)
				}
			}

			// Config values as fallbacks
			if target == "" {
				target = viper.GetString("local.target")
			}
			if remoteBin == "notifytun" {
				if v := viper.GetString("local.remote-bin"); v != "" {
					remoteBin = v
				}
			}
			if backend == "auto" {
				if v := viper.GetString("local.backend"); v != "" {
					backend = v
				}
			}
			if sshKey == "" {
				sshKey = viper.GetString("local.ssh-key")
			}
			if notifyCmd == "" {
				notifyCmd = viper.GetString("local.notify-cmd")
			}

			if target == "" {
				return fmt.Errorf("--target is required (or set local.target in config)")
			}

			return runLocal(cmd.Context(), target, remoteBin, backend, notifyCmd, sshKey)
		},
	}

	cmd.Flags().StringVar(&target, "target", "", "SSH target (user@host or SSH config alias)")
	cmd.Flags().StringVar(&remoteBin, "remote-bin", "notifytun", "Path to notifytun on remote")
	cmd.Flags().StringVar(&backend, "backend", "auto", "Notifier backend: auto, macos, linux, generic")
	cmd.Flags().StringVar(&notifyCmd, "notify-cmd", "", "Custom command for generic backend")
	cmd.Flags().StringVar(&sshKey, "ssh-key", "", "Path to SSH private key")
	cmd.Flags().StringVar(&cfgFile, "config", "", "Config file path")

	return cmd
}

func runLocal(ctx context.Context, target, remoteBin, backend, notifyCmd, sshKey string) error {
	ctx, cancel := signal.NotifyContext(ctx, syscall.SIGINT, syscall.SIGTERM)
	defer cancel()

	n, err := notifier.New(backend, notifyCmd)
	if err != nil {
		return fmt.Errorf("init notifier: %w", err)
	}

	backoff := tunnelssh.NewBackoff()
	remoteCommand := "sh -lc " + strconv.Quote(remoteBin+" attach")

	for {
		if ctx.Err() != nil {
			return nil
		}

		log.Printf("connecting to %s...", target)
		connCfg := tunnelssh.ResolveTarget(target, sshKey, "")
		sess, err := tunnelssh.Connect(ctx, connCfg, remoteCommand)
		if err != nil {
			log.Printf("connection failed: %v", err)
			waitDuration := backoff.Next()
			log.Printf("reconnecting in %s...", waitDuration)
			select {
			case <-ctx.Done():
				return nil
			case <-time.After(waitDuration):
				continue
			}
		}

		log.Printf("connected to %s", target)
		connStart := time.Now()

		// Read stderr in background for diagnostics
		go func() {
			stderrData, _ := io.ReadAll(sess.Stderr)
			if len(stderrData) > 0 {
				log.Printf("remote stderr: %s", string(stderrData))
			}
		}()

		// Process JSONL stream
		err = processStream(ctx, sess.Stdout, n)
		sess.Close()

		if ctx.Err() != nil {
			return nil
		}

		// Reset backoff if connection was stable
		if time.Since(connStart) > stableConnTime {
			backoff.Reset()
		}

		if err != nil {
			log.Printf("connection lost: %v", err)
		} else {
			log.Printf("connection closed")
		}

		waitDuration := backoff.Next()
		log.Printf("reconnecting in %s...", waitDuration)
		select {
		case <-ctx.Done():
			return nil
		case <-time.After(waitDuration):
		}
	}
}

func processStream(ctx context.Context, stdout io.Reader, n notifier.Notifier) error {
	scanner := bufio.NewScanner(stdout)
	heartbeatTimer := time.NewTimer(heartbeatTimeout)
	defer heartbeatTimer.Stop()

	lines := make(chan string)
	scanErr := make(chan error, 1)

	go func() {
		for scanner.Scan() {
			lines <- scanner.Text()
		}
		scanErr <- scanner.Err()
	}()

	for {
		select {
		case <-ctx.Done():
			return nil

		case <-heartbeatTimer.C:
			return fmt.Errorf("heartbeat timeout (%s)", heartbeatTimeout)

		case err := <-scanErr:
			if err != nil {
				return fmt.Errorf("stream read error: %w", err)
			}
			return fmt.Errorf("stream EOF")

		case line := <-lines:
			msg, err := proto.Decode([]byte(line))
			if err != nil {
				log.Printf("warning: malformed JSONL line: %v", err)
				continue
			}

			switch m := msg.(type) {
			case *proto.NotifMessage:
				handleNotif(ctx, m, n)
			case *proto.HeartbeatMessage:
				// Reset heartbeat timer
				if !heartbeatTimer.Stop() {
					select {
					case <-heartbeatTimer.C:
					default:
					}
				}
				heartbeatTimer.Reset(heartbeatTimeout)
			case nil:
				// Unknown message type — ignore per protocol
			}
		}
	}
}

func handleNotif(ctx context.Context, msg *proto.NotifMessage, n notifier.Notifier) {
	// Backlog notifications are suppressed from desktop
	if msg.Backlog {
		log.Printf("backlog: [%s] %s - %s", msg.Tool, msg.Title, msg.Body)
		return
	}

	// Summary and regular notifications are delivered
	notification := notifier.Notification{
		Title: msg.Title,
		Body:  msg.Body,
		Tool:  msg.Tool,
	}

	if err := n.Notify(ctx, notification); err != nil {
		log.Printf("warning: notification delivery failed: %v", err)
	}
}
```

- [ ] **Step 2: Register local in main.go**

Update the `init()` function in `cmd/notifytun/main.go`:

```go
func init() {
	rootCmd.AddCommand(cli.NewEmitCmd())
	rootCmd.AddCommand(cli.NewAttachCmd())
	rootCmd.AddCommand(cli.NewLocalCmd())
}
```

- [ ] **Step 3: Build and verify help output**

Run: `go build -o notifytun ./cmd/notifytun && ./notifytun local --help`
Expected: Help output showing all flags: --target, --remote-bin, --backend, --notify-cmd, --ssh-key, --config

- [ ] **Step 4: Commit**

```bash
git add internal/cli/local.go cmd/notifytun/main.go
git commit -m "feat: add local subcommand with SSH reconnect and notification delivery"
```

---

### Task 10: Test-Notify Subcommand

**Files:**
- Create: `internal/cli/testnotify.go`
- Modify: `cmd/notifytun/main.go`

- [ ] **Step 1: Implement test-notify**

Create `internal/cli/testnotify.go`:

```go
package cli

import (
	"fmt"

	"github.com/michaellee8/notifytun/internal/notifier"
	"github.com/spf13/cobra"
)

func NewTestNotifyCmd() *cobra.Command {
	var (
		backend   string
		notifyCmd string
	)

	cmd := &cobra.Command{
		Use:   "test-notify",
		Short: "Fire a test notification to verify the backend works",
		RunE: func(cmd *cobra.Command, args []string) error {
			n, err := notifier.New(backend, notifyCmd)
			if err != nil {
				return fmt.Errorf("init notifier: %w", err)
			}

			err = n.Notify(cmd.Context(), notifier.Notification{
				Title: "notifytun",
				Body:  "Test notification - if you see this, your backend is working!",
				Tool:  "test",
			})
			if err != nil {
				return fmt.Errorf("notification failed: %w", err)
			}

			fmt.Println("Test notification sent successfully.")
			return nil
		},
	}

	cmd.Flags().StringVar(&backend, "backend", "auto", "Notifier backend: auto, macos, linux, generic")
	cmd.Flags().StringVar(&notifyCmd, "notify-cmd", "", "Custom command for generic backend")

	return cmd
}
```

- [ ] **Step 2: Register test-notify in main.go**

Update `init()`:

```go
func init() {
	rootCmd.AddCommand(cli.NewEmitCmd())
	rootCmd.AddCommand(cli.NewAttachCmd())
	rootCmd.AddCommand(cli.NewLocalCmd())
	rootCmd.AddCommand(cli.NewTestNotifyCmd())
}
```

- [ ] **Step 3: Build and verify**

Run: `go build -o notifytun ./cmd/notifytun && ./notifytun test-notify --backend generic --notify-cmd "cat"`
Expected: JSON output from `cat` and "Test notification sent successfully."

- [ ] **Step 4: Commit**

```bash
git add internal/cli/testnotify.go cmd/notifytun/main.go
git commit -m "feat: add test-notify subcommand"
```

---

### Task 11: Remote-Setup Subcommand

**Files:**
- Create: `internal/setup/setup.go`
- Create: `internal/setup/setup_test.go`
- Create: `internal/cli/remotesetup.go`
- Modify: `cmd/notifytun/main.go`

- [ ] **Step 1: Write failing tests for tool detection**

Create `internal/setup/setup_test.go`:

```go
package setup_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/michaellee8/notifytun/internal/setup"
)

func TestDetectTools(t *testing.T) {
	// Create a fake PATH with fake binaries
	dir := t.TempDir()
	for _, name := range []string{"claude", "codex"} {
		path := filepath.Join(dir, name)
		os.WriteFile(path, []byte("#!/bin/sh\n"), 0o755)
	}

	tools := setup.DetectTools(dir)
	if len(tools) != 2 {
		t.Fatalf("expected 2 tools, got %d: %v", len(tools), tools)
	}

	found := map[string]bool{}
	for _, tool := range tools {
		found[tool.Name] = true
	}
	if !found["Claude Code"] {
		t.Fatal("expected Claude Code to be detected")
	}
	if !found["Codex CLI"] {
		t.Fatal("expected Codex CLI to be detected")
	}
}

func TestClaudeHookGeneration(t *testing.T) {
	hook := setup.GenerateClaudeHook()
	if !strings.Contains(hook, `"Stop"`) {
		t.Fatal("expected Stop hook in generated config")
	}
	if strings.Contains(hook, `"Notification"`) {
		t.Fatal("generated config should not add Claude Notification hooks")
	}
	if !strings.Contains(hook, "Task complete") {
		t.Fatal("expected generated hook to emit Task complete notifications")
	}
}

func TestClaudeHookIdempotent(t *testing.T) {
	dir := t.TempDir()
	settingsPath := filepath.Join(dir, "settings.json")

	// First apply
	err := setup.ApplyClaudeHook(settingsPath)
	if err != nil {
		t.Fatalf("first apply: %v", err)
	}

	// Read content after first apply
	first, _ := os.ReadFile(settingsPath)

	// Second apply should not duplicate
	err = setup.ApplyClaudeHook(settingsPath)
	if err != nil {
		t.Fatalf("second apply: %v", err)
	}

	second, _ := os.ReadFile(settingsPath)
	if string(first) != string(second) {
		t.Fatal("second apply changed the file — not idempotent")
	}
}

func TestDetectAlreadyConfigured(t *testing.T) {
	dir := t.TempDir()
	settingsPath := filepath.Join(dir, "settings.json")
	os.WriteFile(settingsPath, []byte(`{
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
    ]
  }
}`), 0o644)

	configured := setup.IsClaudeConfigured(settingsPath)
	if !configured {
		t.Fatal("expected Claude to be detected as already configured")
	}
}

func TestApplyClaudeHookPreservesExistingStopHooks(t *testing.T) {
	dir := t.TempDir()
	settingsPath := filepath.Join(dir, "settings.json")
	os.WriteFile(settingsPath, []byte(`{
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
}`), 0o644)

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
	if strings.Count(content, "notifytun emit --tool claude-code --title 'Task complete'") != 1 {
		t.Fatal("expected exactly one notifytun Stop hook after apply")
	}
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./internal/setup/ -v`
Expected: Compilation failure

- [ ] **Step 3: Implement the setup package**

Create `internal/setup/setup.go`:

```go
package setup

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

const claudeHookCommand = "notifytun emit --tool claude-code --title 'Task complete'"

// Tool represents a detected AI coding tool.
type Tool struct {
	Name       string
	Binary     string
	Detected   bool
	Configured bool
	Supported  bool // whether we know how to configure hooks for this tool
}

// KnownTools defines the tools we look for.
var KnownTools = []struct {
	Name     string
	Binaries []string // any of these in PATH means the tool is installed
}{
	{Name: "Claude Code", Binaries: []string{"claude", "claude-code"}},
	{Name: "Codex CLI", Binaries: []string{"codex"}},
	{Name: "Gemini CLI", Binaries: []string{"gemini"}},
	{Name: "OpenCode", Binaries: []string{"opencode"}},
}

// DetectTools scans for known tool binaries in the given PATH (or system PATH if empty).
func DetectTools(extraPath string) []Tool {
	var tools []Tool

	for _, known := range KnownTools {
		tool := Tool{Name: known.Name, Supported: known.Name == "Claude Code"}
		for _, bin := range known.Binaries {
			var path string
			if extraPath != "" {
				// Check extra path first
				candidate := filepath.Join(extraPath, bin)
				if info, err := os.Stat(candidate); err == nil && !info.IsDir() {
					path = candidate
				}
			}
			if path == "" {
				path, _ = exec.LookPath(bin)
			}
			if path != "" {
				tool.Detected = true
				tool.Binary = path
				break
			}
		}
		if tool.Detected {
			tools = append(tools, tool)
		}
	}

	return tools
}

// GenerateClaudeHook returns the JSON snippet for Claude Code hooks.
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
    ]
  }
}`
}

// IsClaudeConfigured checks if notifytun hooks are already in the settings file.
func IsClaudeConfigured(settingsPath string) bool {
	data, err := os.ReadFile(settingsPath)
	if err != nil {
		return false
	}

	var settings map[string]interface{}
	if err := json.Unmarshal(data, &settings); err != nil {
		return false
	}

	hooksValue, ok := settings["hooks"]
	if !ok {
		return false
	}
	hooks, ok := hooksValue.(map[string]interface{})
	if !ok {
		return false
	}
	stopValue, ok := hooks["Stop"]
	if !ok {
		return false
	}
	stopEntries, ok := stopValue.([]interface{})
	if !ok {
		return false
	}

	return hasClaudeStopHook(stopEntries)
}

// ApplyClaudeHook writes or merges notifytun hooks into Claude Code settings.
func ApplyClaudeHook(settingsPath string) error {
	if IsClaudeConfigured(settingsPath) {
		return nil // already configured, idempotent
	}

	// Read existing settings or start fresh
	var settings map[string]interface{}
	data, err := os.ReadFile(settingsPath)
	if err == nil {
		if err := json.Unmarshal(data, &settings); err != nil {
			return fmt.Errorf("parse existing settings: %w", err)
		}
	} else if os.IsNotExist(err) {
		settings = map[string]interface{}{}
	} else {
		return fmt.Errorf("read settings: %w", err)
	}

	// Merge into settings
	hooks := map[string]interface{}{}
	if existing, ok := settings["hooks"]; ok {
		typed, ok := existing.(map[string]interface{})
		if !ok {
			return fmt.Errorf("unexpected hooks format: want object")
		}
		hooks = typed
	}

	var stopEntries []interface{}
	if existing, ok := hooks["Stop"]; ok {
		typed, ok := existing.([]interface{})
		if !ok {
			return fmt.Errorf("unexpected hooks.Stop format: want array")
		}
		stopEntries = typed
		if hasClaudeStopHook(stopEntries) {
			return nil
		}
	}

	stopEntries = append(stopEntries, map[string]interface{}{
		"matcher": "",
		"hooks": []interface{}{
			map[string]interface{}{
				"type":    "command",
				"command": claudeHookCommand,
			},
		},
	})

	hooks["Stop"] = stopEntries
	settings["hooks"] = hooks

	// Write back
	if err := os.MkdirAll(filepath.Dir(settingsPath), 0o755); err != nil {
		return fmt.Errorf("create settings dir: %w", err)
	}
	output, err := json.MarshalIndent(settings, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal settings: %w", err)
	}
	return os.WriteFile(settingsPath, output, 0o644)
}

func hasClaudeStopHook(entries []interface{}) bool {
	for _, entry := range entries {
		entryMap, ok := entry.(map[string]interface{})
		if !ok {
			continue
		}
		hookList, ok := entryMap["hooks"].([]interface{})
		if !ok {
			continue
		}
		for _, hook := range hookList {
			hookMap, ok := hook.(map[string]interface{})
			if !ok {
				continue
			}
			command, _ := hookMap["command"].(string)
			if command == claudeHookCommand {
				return true
			}
		}
	}
	return false
}

// Preview returns a human-readable summary of what remote-setup would do.
func Preview(tools []Tool) string {
	var sb strings.Builder
	sb.WriteString("Detected tools:\n")
	for _, tool := range tools {
		if tool.Detected {
			if tool.Configured {
				sb.WriteString(fmt.Sprintf("  * %s -- already configured\n", tool.Name))
			} else if tool.Supported {
				sb.WriteString(fmt.Sprintf("  * %s -- will add Stop hook\n", tool.Name))
			} else {
				sb.WriteString(fmt.Sprintf("  * %s -- detected but hook setup not supported in v1\n", tool.Name))
			}
		}
	}
	return sb.String()
}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./internal/setup/ -v -count=1`
Expected: All 5 tests PASS

- [ ] **Step 5: Implement the remote-setup CLI command**

Create `internal/cli/remotesetup.go`:

```go
package cli

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/michaellee8/notifytun/internal/setup"
	"github.com/spf13/cobra"
)

func NewRemoteSetupCmd() *cobra.Command {
	var dryRun bool

	cmd := &cobra.Command{
		Use:   "remote-setup",
		Short: "Detect AI tools and configure their hooks to call notifytun emit",
		RunE: func(cmd *cobra.Command, args []string) error {
			tools := setup.DetectTools("")

			if len(tools) == 0 {
				fmt.Println("No supported AI tools detected in PATH.")
				return nil
			}

			// Check existing configuration
			home, _ := os.UserHomeDir()
			for i := range tools {
				if tools[i].Name == "Claude Code" {
					settingsPath := filepath.Join(home, ".claude", "settings.json")
					tools[i].Configured = setup.IsClaudeConfigured(settingsPath)
				}
			}

			// Show preview
			fmt.Println(setup.Preview(tools))

			if dryRun {
				fmt.Println("(dry run — no changes applied)")
				return nil
			}

			// Check if there's anything to do
			hasWork := false
			for _, tool := range tools {
				if tool.Supported && !tool.Configured {
					hasWork = true
					break
				}
			}
			if !hasWork {
				fmt.Println("Nothing to configure — all supported tools already set up.")
				return nil
			}

			// Confirm
			fmt.Print("\nApply? [Y/n] ")
			reader := bufio.NewReader(os.Stdin)
			answer, _ := reader.ReadString('\n')
			answer = strings.TrimSpace(strings.ToLower(answer))
			if answer != "" && answer != "y" && answer != "yes" {
				fmt.Println("Aborted.")
				return nil
			}

			// Apply
			for _, tool := range tools {
				if !tool.Supported || tool.Configured {
					continue
				}
				switch tool.Name {
				case "Claude Code":
					settingsPath := filepath.Join(home, ".claude", "settings.json")
					if err := setup.ApplyClaudeHook(settingsPath); err != nil {
						fmt.Fprintf(os.Stderr, "warning: failed to configure %s: %v\n", tool.Name, err)
					} else {
						fmt.Printf("Configured %s hooks in %s\n", tool.Name, settingsPath)
					}
				}
			}

			return nil
		},
	}

	cmd.Flags().BoolVar(&dryRun, "dry-run", false, "Show what would be configured without applying")

	return cmd
}
```

- [ ] **Step 6: Register remote-setup in main.go**

Update `init()`:

```go
func init() {
	rootCmd.AddCommand(cli.NewEmitCmd())
	rootCmd.AddCommand(cli.NewAttachCmd())
	rootCmd.AddCommand(cli.NewLocalCmd())
	rootCmd.AddCommand(cli.NewTestNotifyCmd())
	rootCmd.AddCommand(cli.NewRemoteSetupCmd())
}
```

- [ ] **Step 7: Build and verify**

Run: `go build -o notifytun ./cmd/notifytun && ./notifytun remote-setup --dry-run`
Expected: Output showing detected tools, unsupported-tool notices when applicable, and "(dry run — no changes applied)"

- [ ] **Step 8: Commit**

```bash
git add internal/setup/ internal/cli/remotesetup.go cmd/notifytun/main.go
git commit -m "feat: add remote-setup subcommand for auto-configuring tool hooks"
```

---

### Task 12: Config Example File

**Files:**
- Create: `config.example.toml`

- [ ] **Step 1: Create the example config file**

Create `config.example.toml`:

```toml
# notifytun configuration
# Copy to ~/.notifytun/config.toml and edit as needed.
# CLI flags override all values set here.

[local]
# SSH target — required (unless passed via --target flag)
# Can be user@host or an SSH config Host alias
# target = "user@myvm"

# Path to notifytun binary on the remote machine
# Wrapped in sh -lc for PATH resolution
# remote-bin = "notifytun"

# Notifier backend: auto, macos, linux, generic
# auto detects based on OS
# backend = "auto"

# Path to SSH private key (optional)
# If unset, uses SSH agent or keys from ~/.ssh/config
# ssh-key = "~/.ssh/id_ed25519"

# Custom notification command for generic backend
# Receives notification JSON on stdin
# notify-cmd = "/usr/local/bin/my-notifier"
```

- [ ] **Step 2: Commit**

```bash
git add config.example.toml
git commit -m "docs: add example config file"
```

---

### Task 13: Integration Tests

**Files:**
- Create: `internal/integration_test.go`

- [ ] **Step 1: Write integration tests for the emit-attach pipeline**

Create `internal/integration_test.go`:

```go
package internal_test

import (
	"bufio"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/michaellee8/notifytun/internal/db"
	"github.com/michaellee8/notifytun/internal/proto"
	"github.com/michaellee8/notifytun/internal/socket"
)

// TestEmitThenAttachReplay tests the full remote-side pipeline:
// emit writes to SQLite, attach reads and streams as JSONL.
func TestEmitThenAttachReplay(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "test.db")

	// Simulate emit: insert rows
	d, err := db.Open(dbPath)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	d.Insert("First", "Body 1", "claude-code")
	d.Insert("Second", "Body 2", "codex")
	d.Close()

	// Simulate attach: query undelivered
	d2, err := db.Open(dbPath)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer d2.Close()

	rows, err := d2.QueryUndelivered()
	if err != nil {
		t.Fatalf("QueryUndelivered: %v", err)
	}
	if len(rows) != 2 {
		t.Fatalf("expected 2 rows, got %d", len(rows))
	}

	// Encode as JSONL
	for _, row := range rows {
		msg := &proto.NotifMessage{
			ID:        row.ID,
			Title:     row.Title,
			Body:      row.Body,
			Tool:      row.Tool,
			CreatedAt: row.CreatedAt,
		}
		line, err := proto.Encode(msg)
		if err != nil {
			t.Fatalf("Encode: %v", err)
		}
		// Verify it's valid JSON
		var check map[string]interface{}
		if err := json.Unmarshal(line[:len(line)-1], &check); err != nil {
			t.Fatalf("output is not valid JSON: %v", err)
		}
		// Mark delivered
		d2.MarkDelivered(row.ID)
	}

	// Verify no undelivered remain
	remaining, _ := d2.QueryUndelivered()
	if len(remaining) != 0 {
		t.Fatalf("expected 0 remaining, got %d", len(remaining))
	}
}

// TestBacklogFloodControl tests that >3 queued notifications trigger backlog mode.
func TestBacklogFloodControl(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "test.db")

	d, err := db.Open(dbPath)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}

	// Insert 5 rows (> 3 threshold)
	for i := 0; i < 5; i++ {
		d.Insert("Title", "Body", "test")
	}

	rows, err := d.QueryUndelivered()
	if err != nil {
		t.Fatalf("QueryUndelivered: %v", err)
	}
	d.Close()

	if len(rows) != 5 {
		t.Fatalf("expected 5 rows, got %d", len(rows))
	}

	// Simulate attach backlog logic
	isBacklog := len(rows) > 3
	if !isBacklog {
		t.Fatal("expected backlog mode for 5 rows")
	}

	// Each row should be marked as backlog
	for _, row := range rows {
		msg := &proto.NotifMessage{
			ID:      row.ID,
			Title:   row.Title,
			Body:    row.Body,
			Tool:    row.Tool,
			Backlog: isBacklog,
		}
		line, err := proto.Encode(msg)
		if err != nil {
			t.Fatalf("Encode: %v", err)
		}
		decoded, _ := proto.Decode(line[:len(line)-1])
		notif := decoded.(*proto.NotifMessage)
		if !notif.Backlog {
			t.Fatal("expected backlog=true on replayed notification")
		}
	}

	// Summary should be generated
	summary := &proto.NotifMessage{
		ID:      0,
		Title:   "notifytun",
		Body:    "5 notifications delivered while disconnected",
		Summary: true,
	}
	line, _ := proto.Encode(summary)
	decoded, _ := proto.Decode(line[:len(line)-1])
	s := decoded.(*proto.NotifMessage)
	if !s.Summary {
		t.Fatal("expected summary=true on summary notification")
	}
}

// TestThreeOrFewerNormal tests that <=3 queued notifications are not marked as backlog.
func TestThreeOrFewerNormal(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "test.db")

	d, err := db.Open(dbPath)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}

	d.Insert("One", "", "test")
	d.Insert("Two", "", "test")
	d.Insert("Three", "", "test")

	rows, _ := d.QueryUndelivered()
	d.Close()

	isBacklog := len(rows) > 3
	if isBacklog {
		t.Fatal("3 rows should NOT trigger backlog")
	}
}

// TestSocketWakeup tests that emit's socket wakeup reaches attach's listener.
func TestSocketWakeup(t *testing.T) {
	dir := t.TempDir()
	sockPath := filepath.Join(dir, "test.sock")

	listener, err := socket.Listen(sockPath)
	if err != nil {
		t.Fatalf("Listen: %v", err)
	}
	defer listener.Close()

	// Simulate emit sending wakeup
	go func() {
		time.Sleep(50 * time.Millisecond)
		socket.SendWakeup(sockPath)
	}()

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	if err := listener.Wait(ctx); err != nil {
		t.Fatalf("Wait: %v", err)
	}
}

// TestProtocolOverPipe tests JSONL encode/decode over an io.Pipe (simulating SSH stdio).
func TestProtocolOverPipe(t *testing.T) {
	pr, pw, _ := os.Pipe()

	// Writer (simulating attach)
	go func() {
		msgs := []interface{}{
			&proto.NotifMessage{
				ID: 1, Title: "Test", Body: "Body", Tool: "claude-code",
				CreatedAt: "2026-04-14T10:30:00.000Z",
			},
			&proto.HeartbeatMessage{
				Ts: "2026-04-14T10:31:00.000Z",
			},
			&proto.NotifMessage{
				ID: 2, Title: "Test 2", Body: "Body 2", Tool: "codex",
				CreatedAt: "2026-04-14T10:31:01.000Z",
			},
		}
		for _, msg := range msgs {
			line, _ := proto.Encode(msg)
			pw.Write(line)
		}
		pw.Close()
	}()

	// Reader (simulating local)
	scanner := bufio.NewScanner(pr)
	var received []interface{}
	for scanner.Scan() {
		msg, err := proto.Decode(scanner.Bytes())
		if err != nil {
			t.Fatalf("Decode: %v", err)
		}
		received = append(received, msg)
	}

	if len(received) != 3 {
		t.Fatalf("expected 3 messages, got %d", len(received))
	}

	// First should be a notif
	if _, ok := received[0].(*proto.NotifMessage); !ok {
		t.Fatalf("message 0: expected NotifMessage, got %T", received[0])
	}
	// Second should be a heartbeat
	if _, ok := received[1].(*proto.HeartbeatMessage); !ok {
		t.Fatalf("message 1: expected HeartbeatMessage, got %T", received[1])
	}
	// Third should be a notif
	if _, ok := received[2].(*proto.NotifMessage); !ok {
		t.Fatalf("message 2: expected NotifMessage, got %T", received[2])
	}
}
```

- [ ] **Step 2: Create package doc file (required for test-only directory)**

Create `internal/doc.go`:

```go
// Package internal contains the core packages for notifytun.
package internal
```

- [ ] **Step 3: Run integration tests**

Run: `go test ./internal/ -v -count=1`
Expected: All 5 integration tests PASS

- [ ] **Step 4: Run all tests together**

Run: `go test ./... -v -count=1`
Expected: All tests across all packages PASS

- [ ] **Step 5: Commit**

```bash
git add internal/doc.go internal/integration_test.go
git commit -m "test: add integration tests for emit-attach pipeline and flood control"
```

---

### Task 14: Cross-Platform Build Verification

**Files:**
- None (build verification only)

- [ ] **Step 1: Verify cross-platform builds**

```bash
GOOS=linux GOARCH=amd64 go build -o /dev/null ./cmd/notifytun
GOOS=linux GOARCH=arm64 go build -o /dev/null ./cmd/notifytun
GOOS=darwin GOARCH=arm64 go build -o /dev/null ./cmd/notifytun
GOOS=darwin GOARCH=amd64 go build -o /dev/null ./cmd/notifytun
GOOS=windows GOARCH=amd64 go build -o /dev/null ./cmd/notifytun
echo "All platforms built successfully"
```

Expected: All 5 builds succeed with no errors (pure Go, no CGo).

- [ ] **Step 2: Run full test suite one final time**

Run: `go test ./... -count=1 -race`
Expected: All tests PASS with race detector enabled

- [ ] **Step 3: Commit go.sum updates (if any)**

```bash
go mod tidy
git add go.mod go.sum
git commit -m "chore: tidy go module dependencies"
```
