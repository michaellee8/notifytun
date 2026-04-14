package proto_test

import (
	"bytes"
	"testing"
	"time"

	"github.com/michaellee8/notifytun/internal/proto"
)

func TestEncodeNotifAppendsNewline(t *testing.T) {
	msg := &proto.NotifMessage{
		ID:        1,
		Title:     "Task complete",
		Body:      "Finished refactoring",
		Tool:      "claude-code",
		CreatedAt: "2026-04-14T10:30:00.000Z",
	}

	line, err := proto.Encode(msg)
	if err != nil {
		t.Fatalf("Encode: %v", err)
	}
	if len(line) == 0 || line[len(line)-1] != '\n' {
		t.Fatalf("Encode must append newline, got %q", string(line))
	}
	if !bytes.Contains(line, []byte(`"type":"notif"`)) {
		t.Fatalf("expected notif type in %q", string(line))
	}
}

func TestEncodeNotifBacklog(t *testing.T) {
	msg := &proto.NotifMessage{
		ID:        5,
		Title:     "Build passed",
		Tool:      "codex",
		CreatedAt: "2026-04-14T10:31:02.000Z",
		Backlog:   true,
	}

	line, err := proto.Encode(msg)
	if err != nil {
		t.Fatalf("Encode: %v", err)
	}
	if !bytes.Contains(line, []byte(`"backlog":true`)) {
		t.Fatalf("expected backlog:true in %q", string(line))
	}
}

func TestEncodeHeartbeat(t *testing.T) {
	msg := &proto.HeartbeatMessage{
		Ts: time.Now().UTC().Format(time.RFC3339Nano),
	}

	line, err := proto.Encode(msg)
	if err != nil {
		t.Fatalf("Encode: %v", err)
	}
	if !bytes.Contains(line, []byte(`"type":"heartbeat"`)) {
		t.Fatalf("expected heartbeat type in %q", string(line))
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
	if notif.ID != 1 || notif.Title != "Test" {
		t.Fatalf("unexpected notif: %+v", notif)
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
		t.Fatalf("unexpected heartbeat: %+v", hb)
	}
}

func TestDecodeUnknownType(t *testing.T) {
	msg, err := proto.Decode([]byte(`{"type":"future_type","data":"something"}`))
	if err != nil {
		t.Fatalf("unknown type should not error: %v", err)
	}
	if msg != nil {
		t.Fatalf("unknown type should return nil, got %T", msg)
	}
}

func TestDecodeMalformed(t *testing.T) {
	if _, err := proto.Decode([]byte(`not json at all`)); err == nil {
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
	}

	line, err := proto.Encode(original)
	if err != nil {
		t.Fatalf("Encode: %v", err)
	}

	msg, err := proto.Decode(bytes.TrimSuffix(line, []byte{'\n'}))
	if err != nil {
		t.Fatalf("Decode: %v", err)
	}

	decoded, ok := msg.(*proto.NotifMessage)
	if !ok {
		t.Fatalf("expected *NotifMessage, got %T", msg)
	}
	if decoded.ID != original.ID || decoded.Title != original.Title || decoded.Tool != original.Tool || decoded.Backlog != original.Backlog {
		t.Fatalf("round trip mismatch: want %+v got %+v", original, decoded)
	}
}
