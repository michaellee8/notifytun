package cli

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/michaellee8/notifytun/internal/db"
	"github.com/michaellee8/notifytun/internal/proto"
	"github.com/michaellee8/notifytun/internal/socket"
)

func tempAttachDB(t *testing.T) *db.DB {
	t.Helper()

	d, err := db.Open(filepath.Join(t.TempDir(), "attach.db"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() {
		_ = d.Close()
	})
	return d
}

func TestReplayBacklogThreeOrFewer(t *testing.T) {
	d := tempAttachDB(t)
	for _, title := range []string{"One", "Two"} {
		if _, err := d.Insert(title, "Body", "tool"); err != nil {
			t.Fatalf("Insert: %v", err)
		}
	}

	var messages []any
	write := func(msg any) error {
		messages = append(messages, msg)
		return nil
	}

	if err := replayBacklog(d, write, func() time.Time { return time.Unix(0, 0).UTC() }); err != nil {
		t.Fatalf("replayBacklog: %v", err)
	}
	if len(messages) != 2 {
		t.Fatalf("expected 2 messages, got %d", len(messages))
	}
	for _, msg := range messages {
		notif := msg.(*proto.NotifMessage)
		if notif.Backlog || notif.Summary {
			t.Fatalf("expected normal replay, got %+v", notif)
		}
	}

	rows, err := d.QueryUndelivered()
	if err != nil {
		t.Fatalf("QueryUndelivered: %v", err)
	}
	if len(rows) != 0 {
		t.Fatalf("expected replayed rows to be delivered, got %d", len(rows))
	}
}

func TestReplayBacklogSummary(t *testing.T) {
	d := tempAttachDB(t)
	for i := 0; i < 5; i++ {
		if _, err := d.Insert("Title", "Body", "tool"); err != nil {
			t.Fatalf("Insert: %v", err)
		}
	}

	var messages []any
	write := func(msg any) error {
		messages = append(messages, msg)
		return nil
	}

	if err := replayBacklog(d, write, func() time.Time { return time.Unix(0, 0).UTC() }); err != nil {
		t.Fatalf("replayBacklog: %v", err)
	}
	if len(messages) != 6 {
		t.Fatalf("expected 6 messages, got %d", len(messages))
	}
	for i := 0; i < 5; i++ {
		notif := messages[i].(*proto.NotifMessage)
		if !notif.Backlog || notif.Summary {
			t.Fatalf("expected backlog message, got %+v", notif)
		}
	}
	summary := messages[5].(*proto.NotifMessage)
	if !summary.Summary || summary.Body != "5 notifications delivered while disconnected" {
		t.Fatalf("unexpected summary: %+v", summary)
	}
}

func TestStreamUndeliveredMarksDelivered(t *testing.T) {
	d := tempAttachDB(t)
	if _, err := d.Insert("Title", "Body", "tool"); err != nil {
		t.Fatalf("Insert: %v", err)
	}

	var messages []any
	if err := streamUndelivered(d, func(msg any) error {
		messages = append(messages, msg)
		return nil
	}); err != nil {
		t.Fatalf("streamUndelivered: %v", err)
	}
	if len(messages) != 1 {
		t.Fatalf("expected 1 message, got %d", len(messages))
	}

	rows, err := d.QueryUndelivered()
	if err != nil {
		t.Fatalf("QueryUndelivered: %v", err)
	}
	if len(rows) != 0 {
		t.Fatalf("expected row to be delivered, got %d", len(rows))
	}
}

func TestLiveLoopSendsHeartbeats(t *testing.T) {
	d := tempAttachDB(t)
	listener, err := socket.Listen(filepath.Join(t.TempDir(), "attach.sock"))
	if err != nil {
		t.Fatalf("Listen: %v", err)
	}
	defer listener.Close()

	var messages []any
	ctx, cancel := context.WithTimeout(context.Background(), 35*time.Millisecond)
	defer cancel()

	if err := liveLoop(ctx, d, listener, func(msg any) error {
		messages = append(messages, msg)
		return nil
	}, time.Now, 10*time.Millisecond); err != nil {
		t.Fatalf("liveLoop: %v", err)
	}

	foundHeartbeat := false
	for _, msg := range messages {
		if _, ok := msg.(*proto.HeartbeatMessage); ok {
			foundHeartbeat = true
			break
		}
	}
	if !foundHeartbeat {
		t.Fatalf("expected at least one heartbeat, got %v", messages)
	}
}
