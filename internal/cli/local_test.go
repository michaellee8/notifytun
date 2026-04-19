package cli

import (
	"context"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/michaellee8/notifytun/internal/notifier"
	"github.com/michaellee8/notifytun/internal/proto"
)

type recordingNotifier struct {
	mu    sync.Mutex
	calls []notifier.Notification
	err   error
}

func (r *recordingNotifier) Notify(_ context.Context, n notifier.Notification) error {
	r.mu.Lock()
	r.calls = append(r.calls, n)
	r.mu.Unlock()
	return r.err
}

func (r *recordingNotifier) snapshot() []notifier.Notification {
	r.mu.Lock()
	defer r.mu.Unlock()

	out := make([]notifier.Notification, len(r.calls))
	copy(out, r.calls)
	return out
}

type blockingNotifier struct {
	started chan struct{}
	release chan struct{}

	mu    sync.Mutex
	calls []notifier.Notification
}

func (b *blockingNotifier) Notify(ctx context.Context, n notifier.Notification) error {
	b.mu.Lock()
	b.calls = append(b.calls, n)
	b.mu.Unlock()

	select {
	case <-b.started:
	default:
		close(b.started)
	}

	select {
	case <-b.release:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (b *blockingNotifier) snapshot() []notifier.Notification {
	b.mu.Lock()
	defer b.mu.Unlock()

	out := make([]notifier.Notification, len(b.calls))
	copy(out, b.calls)
	return out
}

func newTestDispatcher(t *testing.T, ctx context.Context, n notifier.Notifier) *notifDispatcher {
	t.Helper()

	dispatcher := newNotifDispatcher(ctx, n, defaultNotifQueueCapacity)
	t.Cleanup(func() {
		if err := dispatcher.Close(); err != nil {
			t.Fatalf("Close: %v", err)
		}
	})
	return dispatcher
}

func newTestDispatcherWithCapacity(t *testing.T, ctx context.Context, n notifier.Notifier, capacity int) *notifDispatcher {
	t.Helper()

	dispatcher := newNotifDispatcher(ctx, n, capacity)
	t.Cleanup(func() {
		if err := dispatcher.Close(); err != nil {
			t.Fatalf("Close: %v", err)
		}
	})
	return dispatcher
}

func waitForCalls(t *testing.T, n *recordingNotifier, want int, timeout time.Duration) []notifier.Notification {
	t.Helper()

	deadline := time.Now().Add(timeout)
	for {
		calls := n.snapshot()
		if len(calls) >= want {
			return calls
		}
		if time.Now().After(deadline) {
			t.Fatalf("expected at least %d delivered notifications, got %d", want, len(calls))
		}
		time.Sleep(5 * time.Millisecond)
	}
}

func waitForBlockingCalls(t *testing.T, n *blockingNotifier, want int, timeout time.Duration) []notifier.Notification {
	t.Helper()

	deadline := time.Now().Add(timeout)
	for {
		calls := n.snapshot()
		if len(calls) >= want {
			return calls
		}
		if time.Now().After(deadline) {
			t.Fatalf("expected at least %d delivered notifications, got %d", want, len(calls))
		}
		time.Sleep(5 * time.Millisecond)
	}
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
		target:       "flag@example.com",
		remoteBin:    "notifytun-custom",
		backend:      "linux",
		notifyCmd:    "printf hi",
		sshKey:       "/tmp/flag-key",
		configFile:   configPath,
		targetSet:    true,
		remoteBinSet: true,
		backendSet:   true,
		notifyCmdSet: true,
		sshKeySet:    true,
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

func TestLocalCmdExplicitDefaultBackendFlagBeatsConfig(t *testing.T) {
	configPath := filepath.Join(t.TempDir(), "config.toml")
	content := `
[local]
target = "127.0.0.1:1"
backend = "bogus"
`
	if err := os.WriteFile(configPath, []byte(content), 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()

	cmd := NewLocalCmd()
	cmd.SetArgs([]string{
		"--config", configPath,
		"--backend", "auto",
	})

	err := cmd.ExecuteContext(ctx)
	if err != nil && strings.Contains(err.Error(), "unknown backend: bogus") {
		t.Fatalf("expected explicit --backend auto to win over config, got %v", err)
	}
}

func TestLocalOptionsPreserveExplicitDefaultValuedFlags(t *testing.T) {
	configPath := filepath.Join(t.TempDir(), "config.toml")
	content := `
[local]
remote-bin = "/opt/bin/notifytun"
backend = "generic"
`
	if err := os.WriteFile(configPath, []byte(content), 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	opts := localOptions{
		remoteBin:    "notifytun",
		backend:      "auto",
		configFile:   configPath,
		remoteBinSet: true,
		backendSet:   true,
	}

	if err := opts.loadAndApplyConfig(); err != nil {
		t.Fatalf("loadAndApplyConfig: %v", err)
	}

	if opts.remoteBin != "notifytun" {
		t.Fatalf("expected explicit default remote bin to win, got %q", opts.remoteBin)
	}
	if opts.backend != "auto" {
		t.Fatalf("expected explicit default backend to win, got %q", opts.backend)
	}
}

func TestBuildRemoteAttachCommandSupportsSpacesInRemoteBin(t *testing.T) {
	remoteDir := filepath.Join(t.TempDir(), "bin dir")
	if err := os.MkdirAll(remoteDir, 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}

	remoteBin := filepath.Join(remoteDir, "notify tun")
	script := "#!/bin/sh\nprintf '%s\\n' \"$1\"\n"
	if err := os.WriteFile(remoteBin, []byte(script), 0o755); err != nil {
		t.Fatalf("WriteFile(remoteBin): %v", err)
	}

	out, err := exec.Command("sh", "-lc", buildRemoteAttachCommand(remoteBin)).CombinedOutput()
	if err != nil {
		t.Fatalf("CombinedOutput: %v (output: %s)", err, string(out))
	}
	if got := string(out); got != "attach\n" {
		t.Fatalf("expected attach arg, got %q", got)
	}
}

func TestBuildRemoteAttachCommandUsesFallbackForDefault(t *testing.T) {
	cmd := buildRemoteAttachCommand(defaultRemoteBin)
	if !strings.Contains(cmd, "command -v notifytun") {
		t.Fatalf("expected PATH probe, got %q", cmd)
	}
	if !strings.Contains(cmd, "$HOME/go/bin/notifytun") {
		t.Fatalf("expected go/bin fallback, got %q", cmd)
	}
	if !strings.Contains(cmd, "not found in PATH or ~/go/bin") {
		t.Fatalf("expected error message, got %q", cmd)
	}
}

func extractDefaultAttachScript(t *testing.T) string {
	t.Helper()
	cmd := buildRemoteAttachCommand(defaultRemoteBin)
	quoted := strings.TrimPrefix(cmd, "sh -lc ")
	if quoted == cmd {
		t.Fatalf("expected sh -lc prefix, got %q", cmd)
	}
	script, err := strconv.Unquote(quoted)
	if err != nil {
		t.Fatalf("Unquote(%q): %v", quoted, err)
	}
	return script
}

func TestBuildRemoteAttachCommandFallbackPrefersPath(t *testing.T) {
	binDir := t.TempDir()
	binPath := filepath.Join(binDir, "notifytun")
	stub := "#!/bin/sh\nprintf 'path:%s\\n' \"$1\"\n"
	if err := os.WriteFile(binPath, []byte(stub), 0o755); err != nil {
		t.Fatalf("WriteFile(binPath): %v", err)
	}

	homeDir := t.TempDir()

	cmd := exec.Command("sh", "-c", extractDefaultAttachScript(t))
	cmd.Env = []string{"PATH=" + binDir, "HOME=" + homeDir}
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("CombinedOutput: %v (output: %s)", err, string(out))
	}
	if got := string(out); got != "path:attach\n" {
		t.Fatalf("expected PATH copy to run, got %q", got)
	}
}

func TestBuildRemoteAttachCommandFallbackUsesGoBin(t *testing.T) {
	homeDir := t.TempDir()
	goBinDir := filepath.Join(homeDir, "go", "bin")
	if err := os.MkdirAll(goBinDir, 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	stub := "#!/bin/sh\nprintf 'gobin:%s\\n' \"$1\"\n"
	if err := os.WriteFile(filepath.Join(goBinDir, "notifytun"), []byte(stub), 0o755); err != nil {
		t.Fatalf("WriteFile(go/bin/notifytun): %v", err)
	}

	emptyBin := t.TempDir()

	cmd := exec.Command("sh", "-c", extractDefaultAttachScript(t))
	cmd.Env = []string{"PATH=" + emptyBin, "HOME=" + homeDir}
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("CombinedOutput: %v (output: %s)", err, string(out))
	}
	if got := string(out); got != "gobin:attach\n" {
		t.Fatalf("expected go/bin copy to run, got %q", got)
	}
}

func TestBuildRemoteAttachCommandFallbackFailsCleanly(t *testing.T) {
	emptyBin := t.TempDir()
	homeDir := t.TempDir()

	cmd := exec.Command("sh", "-c", extractDefaultAttachScript(t))
	cmd.Env = []string{"PATH=" + emptyBin, "HOME=" + homeDir}
	out, err := cmd.CombinedOutput()
	if err == nil {
		t.Fatalf("expected non-zero exit, got success (out: %s)", string(out))
	}
	if !strings.Contains(string(out), "notifytun: not found in PATH or ~/go/bin") {
		t.Fatalf("expected error message on stderr, got %q", string(out))
	}
	var exitErr *exec.ExitError
	if !errors.As(err, &exitErr) {
		t.Fatalf("expected ExitError, got %T: %v", err, err)
	}
	if exitErr.ExitCode() != 127 {
		t.Fatalf("expected exit 127, got %d", exitErr.ExitCode())
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
	dispatcher := newTestDispatcher(t, context.Background(), n)
	err = processStreamWithTimeout(context.Background(), strings.NewReader(stream), dispatcher, 100*time.Millisecond)
	if err == nil || !strings.Contains(err.Error(), "stream EOF") {
		t.Fatalf("expected EOF error, got %v", err)
	}
	if calls := waitForCalls(t, n, 1, time.Second); len(calls) != 1 {
		t.Fatalf("expected 1 delivered notification, got %d", len(calls))
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
	dispatcher := newTestDispatcher(t, context.Background(), n)
	err := processStreamWithTimeout(context.Background(), reader, dispatcher, 50*time.Millisecond)
	if err == nil || !strings.Contains(err.Error(), "stream EOF") {
		t.Fatalf("expected EOF error after processing stream, got %v", err)
	}
	calls := waitForCalls(t, n, 1, time.Second)
	if len(calls) != 1 || calls[0].Title != "Delayed" {
		t.Fatalf("expected delayed notification after heartbeat reset, got %+v", calls)
	}
}

func TestProcessStreamAcceptsLargeJSONLFrames(t *testing.T) {
	body := strings.Repeat("a", 2*1024*1024)
	notif, err := proto.Encode(&proto.NotifMessage{
		ID:        3,
		Title:     "Large",
		Body:      body,
		Tool:      "codex",
		CreatedAt: "2026-04-14T00:00:00Z",
	})
	if err != nil {
		t.Fatalf("Encode(notif): %v", err)
	}

	n := &recordingNotifier{}
	dispatcher := newTestDispatcher(t, context.Background(), n)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	err = processStream(ctx, strings.NewReader(string(notif)), dispatcher)
	if err == nil || !strings.Contains(err.Error(), "stream EOF") {
		t.Fatalf("expected EOF error, got %v", err)
	}

	calls := waitForCalls(t, n, 1, 2*time.Second)
	if len(calls) != 1 {
		t.Fatalf("expected 1 delivered notification, got %d", len(calls))
	}
	if calls[0].Body != body {
		t.Fatalf("expected large body to be delivered intact, got %d bytes", len(calls[0].Body))
	}
}

func TestProcessStreamDoesNotBackpressureHeartbeatsBehindSlowNotifier(t *testing.T) {
	reader, writer := io.Pipe()
	defer reader.Close()

	notif, err := proto.Encode(&proto.NotifMessage{
		ID:        4,
		Title:     "Slow",
		Body:      "Notifier",
		Tool:      "codex",
		CreatedAt: "2026-04-14T00:00:00Z",
	})
	if err != nil {
		t.Fatalf("Encode(notif): %v", err)
	}
	heartbeat, err := proto.Encode(&proto.HeartbeatMessage{Ts: "2026-04-14T00:00:00Z"})
	if err != nil {
		t.Fatalf("Encode(heartbeat): %v", err)
	}

	n := &blockingNotifier{
		started: make(chan struct{}),
		release: make(chan struct{}),
	}
	dispatcher := newTestDispatcherWithCapacity(t, context.Background(), n, 1)

	errCh := make(chan error, 1)
	go func() {
		errCh <- processStreamWithTimeout(context.Background(), reader, dispatcher, 250*time.Millisecond)
	}()

	if _, err := writer.Write(notif); err != nil {
		t.Fatalf("Write(notif): %v", err)
	}
	select {
	case <-n.started:
	case <-time.After(time.Second):
		t.Fatal("expected notifier delivery to start")
	}

	if _, err := writer.Write(heartbeat); err != nil {
		t.Fatalf("Write(first heartbeat): %v", err)
	}
	if _, err := writer.Write(notif); err != nil {
		t.Fatalf("Write(second notification): %v", err)
	}
	if _, err := writer.Write(notif); err != nil {
		t.Fatalf("Write(third notification): %v", err)
	}

	writeDone := make(chan error, 1)
	go func() {
		_, err := writer.Write(heartbeat)
		writeDone <- err
	}()

	select {
	case err := <-writeDone:
		if err != nil {
			t.Fatalf("Write(second heartbeat): %v", err)
		}
	case <-time.After(50 * time.Millisecond):
		t.Fatal("expected heartbeat writes to continue while notifier is blocked")
	}

	if err := writer.Close(); err != nil {
		t.Fatalf("Close(writer): %v", err)
	}

	select {
	case err := <-errCh:
		if err == nil || !strings.Contains(err.Error(), "stream EOF") {
			t.Fatalf("expected EOF after writer close, got %v", err)
		}
	case <-time.After(200 * time.Millisecond):
		t.Fatal("expected processStream to return even while notifier is blocked")
	}

	close(n.release)
	calls := waitForBlockingCalls(t, n, 3, time.Second)
	if calls[0].Title != "Slow" || calls[1].Title != "Slow" {
		t.Fatalf("expected queued notifications to preserve order, got %+v", calls)
	}
	if calls[2].Title != "notifytun" || !strings.Contains(calls[2].Body, "1 notifications skipped while local delivery was saturated") {
		t.Fatalf("expected saturation summary after dropped notifications, got %+v", calls[2])
	}
}

func TestProcessStreamHeartbeatTimeout(t *testing.T) {
	reader, writer := io.Pipe()
	defer reader.Close()
	defer writer.Close()

	n := &recordingNotifier{}
	dispatcher := newTestDispatcher(t, context.Background(), n)
	err := processStreamWithTimeout(context.Background(), reader, dispatcher, 20*time.Millisecond)
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

	if calls := n.snapshot(); len(calls) != 0 {
		t.Fatalf("expected backlog notification to be suppressed, got %+v", calls)
	}
}

func TestHandleNotifDeliversSummary(t *testing.T) {
	n := &recordingNotifier{}

	handleNotif(context.Background(), &proto.NotifMessage{
		Title:   "notifytun",
		Body:    "4 notifications delivered while disconnected",
		Summary: true,
	}, n)

	calls := n.snapshot()
	if len(calls) != 1 {
		t.Fatalf("expected summary notification to be delivered, got %+v", calls)
	}
	if calls[0].Title != "notifytun" {
		t.Fatalf("unexpected delivered notification: %+v", calls[0])
	}
}

func TestHandleNotifLogsDeliveryFailure(t *testing.T) {
	n := &recordingNotifier{err: errors.New("boom")}

	handleNotif(context.Background(), &proto.NotifMessage{
		Title: "Task complete",
		Body:  "Body",
		Tool:  "codex",
	}, n)

	if calls := n.snapshot(); len(calls) != 1 {
		t.Fatalf("expected notifier to be called once, got %d", len(calls))
	}
}
