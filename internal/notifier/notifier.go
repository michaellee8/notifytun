package notifier

import (
	"context"
	"fmt"
	"io"
)

// Notification is the payload delivered to the local notification backend.
type Notification struct {
	Title string
	Body  string
	Tool  string
}

// Notifier delivers a notification to the local machine.
type Notifier interface {
	Notify(ctx context.Context, n Notification) error
}

// CommandOutputConfigurer lets notifiers that shell out expose the underlying
// command's stdout and stderr to caller-provided writers.
type CommandOutputConfigurer interface {
	SetCommandOutput(stdout, stderr io.Writer)
}

// New builds a notifier for the selected backend.
func New(backend, notifyCmd string) (Notifier, error) {
	switch backend {
	case "auto":
		return NewBeeep(), nil
	case "generic":
		if notifyCmd == "" {
			return nil, fmt.Errorf("--notify-cmd is required for generic backend")
		}
		return NewGeneric(notifyCmd)
	default:
		return nil, fmt.Errorf("unknown backend: %s", backend)
	}
}
