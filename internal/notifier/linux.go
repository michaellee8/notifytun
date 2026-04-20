package notifier

import (
	"context"
	"os/exec"
)

// Linux delivers notifications with notify-send.
type Linux struct{}

// NewLinux creates a Linux notifier.
func NewLinux() *Linux {
	return &Linux{}
}

// BuildCommand returns the command used to deliver the notification. The
// returned *exec.Cmd is bound to ctx so cancellation propagates to the
// child process.
func (l *Linux) BuildCommand(ctx context.Context, title, body string) *exec.Cmd {
	return exec.CommandContext(ctx, "notify-send", "-a", "notifytun", title, body)
}

// Notify sends the notification.
func (l *Linux) Notify(ctx context.Context, n Notification) error {
	return l.BuildCommand(ctx, n.Title, n.Body).Run()
}
