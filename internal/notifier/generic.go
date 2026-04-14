package notifier

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os/exec"
)

// Generic pipes notification JSON to a user-provided shell command.
type Generic struct {
	command string
	stdout  io.Writer
	stderr  io.Writer
}

// NewGeneric creates a generic notifier.
func NewGeneric(command string) (*Generic, error) {
	if command == "" {
		return nil, fmt.Errorf("notify-cmd must not be empty")
	}
	return &Generic{command: command}, nil
}

// SetCommandOutput configures where the notify command's stdout and stderr are written.
func (g *Generic) SetCommandOutput(stdout, stderr io.Writer) {
	g.stdout = stdout
	g.stderr = stderr
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
	if g.stdout != nil || g.stderr != nil {
		cmd.Stdout = g.stdout
		cmd.Stderr = g.stderr
		if err := cmd.Run(); err != nil {
			return fmt.Errorf("notify command failed: %w", err)
		}
		return nil
	}

	if output, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("notify command failed: %w (output: %s)", err, string(output))
	}

	return nil
}
