package cli

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/michaellee8/notifytun/internal/notifier"
	"github.com/michaellee8/notifytun/internal/proto"
)

type recordingNotifier struct {
	calls []notifier.Notification
	err   error
}

func (r *recordingNotifier) Notify(_ context.Context, n notifier.Notification) error {
	r.calls = append(r.calls, n)
	return r.err
}

func TestLocalOptionsApplyConfigFallbacks(t *testing.T) {
	configPath := filepath.Join(t.TempDir(), "config.toml")
	content := `
[local]
target = "ops@example.com"
remote-bin = "/opt/bin/notifytun"
backend = "generic"
notify-cmd = "cat"
ssh-key = "/tmp/test-key"
`
	if err := os.WriteFile(configPath, []byte(content), 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	opts := localOptions{
		remoteBin:  "notifytun",
		backend:    "auto",
		configFile: configPath,
	}

	if err := opts.loadAndApplyConfig(); err != nil {
		t.Fatalf("loadAndApplyConfig: %v", err)
	}

	if opts.target != "ops@example.com" {
		t.Fatalf("expected target from config, got %q", opts.target)
	}
	if opts.remoteBin != "/opt/bin/notifytun" {
		t.Fatalf("expected remote bin from config, got %q", opts.remoteBin)
	}
	if opts.backend != "generic" {
		t.Fatalf("expected backend from config, got %q", opts.backend)
	}
	if opts.notifyCmd != "cat" {
		t.Fatalf("expected notify cmd from config, got %q", opts.notifyCmd)
	}
	if opts.sshKey != "/tmp/test-key" {
		t.Fatalf("expected ssh key from config, got %q", opts.sshKey)
	}
}

func TestLocalOptionsPreserveExplicitFlags(t *testing.T) {
	configPath := filepath.Join(t.TempDir(), "config.toml")
	content := `
[local]
target = "ops@example.com"
remote-bin = "/opt/bin/notifytun"
backend = "generic"
notify-cmd = "cat"
ssh-key = "/tmp/test-key"
`
	if err := os.WriteFile(configPath, []byte(content), 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	opts := localOptions{
		target:     "flag@example.com",
		remoteBin:  "notifytun-custom",
		backend:    "linux",
		notifyCmd:  "printf hi",
		sshKey:     "/tmp/flag-key",
		configFile: configPath,
	}

	if err := opts.loadAndApplyConfig(); err != nil {
		t.Fatalf("loadAndApplyConfig: %v", err)
	}

	if opts.target != "flag@example.com" {
		t.Fatalf("expected explicit target to win, got %q", opts.target)
	}
	if opts.remoteBin != "notifytun-custom" {
		t.Fatalf("expected explicit remote bin to win, got %q", opts.remoteBin)
	}
	if opts.backend != "linux" {
		t.Fatalf("expected explicit backend to win, got %q", opts.backend)
	}
	if opts.notifyCmd != "printf hi" {
		t.Fatalf("expected explicit notify cmd to win, got %q", opts.notifyCmd)
	}
	if opts.sshKey != "/tmp/flag-key" {
		t.Fatalf("expected explicit ssh key to win, got %q", opts.sshKey)
	}
}

func TestProcessStreamSkipsMalformedJSONAndReturnsEOF(t *testing.T) {
	heartbeat, err := proto.Encode(&proto.HeartbeatMessage{Ts: "2026-04-14T00:00:00Z"})
	if err != nil {
		t.Fatalf("Encode(heartbeat): %v", err)
	}
	notif, err := proto.Encode(&proto.NotifMessage{
		ID:        1,
		Title:     "Task complete",
		Body:      "Body",
		Tool:      "codex",
		CreatedAt: "2026-04-14T00:00:00Z",
	})
	if err != nil {
		t.Fatalf("Encode(notif): %v", err)
	}

	stream := strings.Join([]string{
		"{bad json",
		strings.TrimSpace(string(heartbeat)),
		strings.TrimSpace(string(notif)),
	}, "\n") + "\n"

	n := &recordingNotifier{}
	err = processStreamWithTimeout(context.Background(), strings.NewReader(stream), n, 100*time.Millisecond)
	if err == nil || !strings.Contains(err.Error(), "stream EOF") {
		t.Fatalf("expected EOF error, got %v", err)
	}
	if len(n.calls) != 1 {
		t.Fatalf("expected 1 delivered notification, got %d", len(n.calls))
	}
}

func TestProcessStreamHeartbeatResetAllowsLaterNotification(t *testing.T) {
	reader, writer := io.Pipe()
	defer reader.Close()

	go func() {
		defer writer.Close()

		time.Sleep(20 * time.Millisecond)
		heartbeat, err := proto.Encode(&proto.HeartbeatMessage{Ts: "2026-04-14T00:00:00Z"})
		if err != nil {
			writer.CloseWithError(err)
			return
		}
		if _, err := writer.Write(heartbeat); err != nil {
			return
		}

		time.Sleep(40 * time.Millisecond)
		notif, err := proto.Encode(&proto.NotifMessage{
			ID:        2,
			Title:     "Delayed",
			Body:      "Still alive",
			Tool:      "codex",
			CreatedAt: "2026-04-14T00:00:00Z",
		})
		if err != nil {
			writer.CloseWithError(err)
			return
		}
		_, _ = writer.Write(notif)
	}()

	n := &recordingNotifier{}
	err := processStreamWithTimeout(context.Background(), reader, n, 50*time.Millisecond)
	if err == nil || !strings.Contains(err.Error(), "stream EOF") {
		t.Fatalf("expected EOF error after processing stream, got %v", err)
	}
	if len(n.calls) != 1 || n.calls[0].Title != "Delayed" {
		t.Fatalf("expected delayed notification after heartbeat reset, got %+v", n.calls)
	}
}

func TestProcessStreamHeartbeatTimeout(t *testing.T) {
	reader, writer := io.Pipe()
	defer reader.Close()
	defer writer.Close()

	n := &recordingNotifier{}
	err := processStreamWithTimeout(context.Background(), reader, n, 20*time.Millisecond)
	if err == nil || !strings.Contains(err.Error(), "heartbeat timeout") {
		t.Fatalf("expected heartbeat timeout, got %v", err)
	}
}

func TestHandleNotifSuppressesBacklog(t *testing.T) {
	n := &recordingNotifier{}

	handleNotif(context.Background(), &proto.NotifMessage{
		Title:   "Queued",
		Body:    "Backlog",
		Tool:    "codex",
		Backlog: true,
	}, n)

	if len(n.calls) != 0 {
		t.Fatalf("expected backlog notification to be suppressed, got %+v", n.calls)
	}
}

func TestHandleNotifDeliversSummary(t *testing.T) {
	n := &recordingNotifier{}

	handleNotif(context.Background(), &proto.NotifMessage{
		Title:   "notifytun",
		Body:    "4 notifications delivered while disconnected",
		Summary: true,
	}, n)

	if len(n.calls) != 1 {
		t.Fatalf("expected summary notification to be delivered, got %+v", n.calls)
	}
	if n.calls[0].Title != "notifytun" {
		t.Fatalf("unexpected delivered notification: %+v", n.calls[0])
	}
}

func TestHandleNotifLogsDeliveryFailure(t *testing.T) {
	n := &recordingNotifier{err: errors.New("boom")}

	handleNotif(context.Background(), &proto.NotifMessage{
		Title: "Task complete",
		Body:  "Body",
		Tool:  "codex",
	}, n)

	if len(n.calls) != 1 {
		t.Fatalf("expected notifier to be called once, got %d", len(n.calls))
	}
}
