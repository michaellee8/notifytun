package notifier

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
)

// Generic pipes notification JSON to a user-provided shell command.
type Generic struct {
	command string
}

// NewGeneric creates a generic notifier.
func NewGeneric(command string) (*Generic, error) {
	if command == "" {
		return nil, fmt.Errorf("notify-cmd must not be empty")
	}
	return &Generic{command: command}, nil
}

// Notify pipes the notification payload to the command on stdin.
func (g *Generic) Notify(ctx context.Context, n Notification) error {
	payload, err := json.Marshal(map[string]string{
		"title": n.Title,
		"body":  n.Body,
		"tool":  n.Tool,
	})
	if err != nil {
		return fmt.Errorf("marshal notification: %w", err)
	}

	cmd := exec.CommandContext(ctx, "sh", "-lc", g.command)
	cmd.Stdin = bytes.NewReader(payload)
	if output, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("notify command failed: %w (output: %s)", err, string(output))
	}

	return nil
}
