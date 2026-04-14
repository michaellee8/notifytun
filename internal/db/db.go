package db

import (
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	_ "modernc.org/sqlite"
)

const schema = `
CREATE TABLE IF NOT EXISTS notifications (
	id         INTEGER PRIMARY KEY AUTOINCREMENT,
	title      TEXT    NOT NULL,
	body       TEXT    NOT NULL DEFAULT '',
	tool       TEXT    NOT NULL DEFAULT '',
	created_at TEXT    NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now')),
	delivered  INTEGER NOT NULL DEFAULT 0
);

CREATE INDEX IF NOT EXISTS idx_undelivered
ON notifications (delivered, id)
WHERE delivered = 0;
`

// Notification is a stored notification row.
type Notification struct {
	ID        int64
	Title     string
	Body      string
	Tool      string
	CreatedAt string
	Delivered bool
}

// DB wraps a SQLite connection for notification storage.
type DB struct {
	db *sql.DB
}

// Open opens or creates the database, applies pragmas, and ensures the schema exists.
func Open(path string) (*DB, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, fmt.Errorf("create db directory: %w", err)
	}

	sqlDB, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, fmt.Errorf("open sqlite: %w", err)
	}

	pragmas := []string{
		"PRAGMA busy_timeout = 5000",
		"PRAGMA journal_mode = WAL",
	}
	for _, stmt := range pragmas {
		if err := execWithBusyRetry(sqlDB, stmt); err != nil {
			_ = sqlDB.Close()
			return nil, fmt.Errorf("apply pragma %q: %w", stmt, err)
		}
	}

	if err := execWithBusyRetry(sqlDB, schema); err != nil {
		_ = sqlDB.Close()
		return nil, fmt.Errorf("ensure schema: %w", err)
	}

	return &DB{db: sqlDB}, nil
}

// Insert stores a new notification and returns its row ID.
func (d *DB) Insert(title, body, tool string) (int64, error) {
	result, err := d.db.Exec(
		`INSERT INTO notifications (title, body, tool) VALUES (?, ?, ?)`,
		title, body, tool,
	)
	if err != nil {
		return 0, fmt.Errorf("insert notification: %w", err)
	}

	id, err := result.LastInsertId()
	if err != nil {
		return 0, fmt.Errorf("lookup inserted id: %w", err)
	}

	return id, nil
}

// QueryUndelivered returns undelivered notifications ordered by ascending ID.
func (d *DB) QueryUndelivered() ([]Notification, error) {
	rows, err := d.db.Query(
		`SELECT id, title, body, tool, created_at, delivered
		FROM notifications
		WHERE delivered = 0
		ORDER BY id`,
	)
	if err != nil {
		return nil, fmt.Errorf("query undelivered: %w", err)
	}
	defer rows.Close()

	var notifications []Notification
	for rows.Next() {
		var (
			n             Notification
			deliveredFlag int
		)
		if err := rows.Scan(&n.ID, &n.Title, &n.Body, &n.Tool, &n.CreatedAt, &deliveredFlag); err != nil {
			return nil, fmt.Errorf("scan notification: %w", err)
		}
		n.Delivered = deliveredFlag != 0
		notifications = append(notifications, n)
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate notifications: %w", err)
	}

	return notifications, nil
}

// MarkDelivered marks a notification as delivered.
func (d *DB) MarkDelivered(id int64) error {
	if _, err := d.db.Exec(`UPDATE notifications SET delivered = 1 WHERE id = ?`, id); err != nil {
		return fmt.Errorf("mark delivered: %w", err)
	}
	return nil
}

// Close closes the database connection.
func (d *DB) Close() error {
	return d.db.Close()
}

func execWithBusyRetry(db *sql.DB, stmt string) error {
	deadline := time.Now().Add(5 * time.Second)
	for {
		if _, err := db.Exec(stmt); err != nil {
			if !isBusyError(err) || time.Now().After(deadline) {
				return err
			}
			time.Sleep(10 * time.Millisecond)
			continue
		}
		return nil
	}
}

func isBusyError(err error) bool {
	return strings.Contains(err.Error(), "SQLITE_BUSY")
}
