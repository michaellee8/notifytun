package notifier

import (
	"context"
	"os/exec"
)

// MacOS delivers notifications with osascript.
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

// BuildCommand returns the command used to deliver the notification. The
// returned *exec.Cmd is bound to ctx so cancellation propagates to the
// child process.
func (m *MacOS) BuildCommand(ctx context.Context, title, body string) *exec.Cmd {
	return exec.CommandContext(ctx, "osascript", "-e", appleScript, "--", title, body)
}

// Notify sends the notification.
func (m *MacOS) Notify(ctx context.Context, n Notification) error {
	return m.BuildCommand(ctx, n.Title, n.Body).Run()
}
