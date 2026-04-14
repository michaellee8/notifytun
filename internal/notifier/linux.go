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

// BuildCommand returns the command used to deliver the notification.
func (l *Linux) BuildCommand(title, body string) *exec.Cmd {
	return exec.Command("notify-send", "-a", "notifytun", title, body)
}

// Notify sends the notification.
func (l *Linux) Notify(ctx context.Context, n Notification) error {
	return exec.CommandContext(ctx, "notify-send", "-a", "notifytun", n.Title, n.Body).Run()
}
