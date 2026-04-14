package notifier

import (
	"context"
	"fmt"
	"runtime"
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

// New builds a notifier for the selected backend.
func New(backend, notifyCmd string) (Notifier, error) {
	switch backend {
	case "auto":
		return newAuto(notifyCmd)
	case "macos":
		return NewMacOS(), nil
	case "linux":
		return NewLinux(), nil
	case "generic":
		if notifyCmd == "" {
			return nil, fmt.Errorf("--notify-cmd is required for generic backend")
		}
		return NewGeneric(notifyCmd)
	default:
		return nil, fmt.Errorf("unknown backend: %s", backend)
	}
}

func newAuto(notifyCmd string) (Notifier, error) {
	switch runtime.GOOS {
	case "darwin":
		return NewMacOS(), nil
	case "linux":
		return NewLinux(), nil
	default:
		if notifyCmd == "" {
			return nil, fmt.Errorf("auto backend is unsupported on %s without --notify-cmd", runtime.GOOS)
		}
		return NewGeneric(notifyCmd)
	}
}
