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

	t.Cleanup(func() {
		_ = d.Close()
	})

	return d
}

func TestOpenCreatesDB(t *testing.T) {
	path := filepath.Join(t.TempDir(), "subdir", "test.db")

	d, err := db.Open(path)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer d.Close()

	if _, err := os.Stat(path); err != nil {
		t.Fatalf("expected db file to exist: %v", err)
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
	if rows[0].Title != "Test Title" || rows[0].Body != "Test Body" || rows[0].Tool != "claude-code" {
		t.Fatalf("unexpected row: %+v", rows[0])
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

	for _, title := range []string{"First", "Second", "Third"} {
		if _, err := d.Insert(title, "", ""); err != nil {
			t.Fatalf("Insert %q: %v", title, err)
		}
	}

	rows, err := d.QueryUndelivered()
	if err != nil {
		t.Fatalf("QueryUndelivered: %v", err)
	}
	if len(rows) != 3 {
		t.Fatalf("expected 3 rows, got %d", len(rows))
	}
	if rows[0].Title != "First" || rows[1].Title != "Second" || rows[2].Title != "Third" {
		t.Fatalf("rows not ordered by insertion: %+v", rows)
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

	if _, err := d.Insert("Title", "Body", "test"); err != nil {
		t.Fatalf("Insert: %v", err)
	}

	rows, err := d.QueryUndelivered()
	if err != nil {
		t.Fatalf("QueryUndelivered: %v", err)
	}
	if len(rows) != 1 || rows[0].CreatedAt == "" {
		t.Fatalf("expected created_at to be populated, got %+v", rows)
	}
}
