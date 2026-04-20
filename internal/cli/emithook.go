package cli

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/michaellee8/notifytun/internal/db"
	"github.com/michaellee8/notifytun/internal/socket"
	"github.com/spf13/cobra"
)

// hookDispatch describes how to turn a payload into a notification for one (tool, event) pair.
type hookDispatch struct {
	toolDisplayName string
	titleSuffix     string
	// extractBody receives the unmarshaled payload and returns the body string.
	// Returning "" means "no body" — title-only notification. Not an error.
	extractBody func(map[string]any) string
}

var hookTable = map[string]map[string]hookDispatch{
	"claude-code": {
		"Stop": {
			toolDisplayName: "Claude Code",
			titleSuffix:     "Task complete",
			extractBody:     extractStringField("last_assistant_message"),
		},
		"Notification": {
			toolDisplayName: "Claude Code",
			titleSuffix:     "Needs attention",
			extractBody:     extractStringField("message"),
		},
	},
	"gemini": {
		"AfterAgent": {
			toolDisplayName: "Gemini CLI",
			titleSuffix:     "Task complete",
			extractBody:     extractStringField("prompt_response"),
		},
		"Notification": {
			toolDisplayName: "Gemini CLI",
			titleSuffix:     "Needs attention",
			extractBody:     extractStringField("message"),
		},
	},
	"codex": {
		"notify": {
			toolDisplayName: "Codex",
			titleSuffix:     "Task complete",
			extractBody:     extractCodexBody,
		},
	},
	"opencode": {
		"session.idle": {
			toolDisplayName: "OpenCode",
			titleSuffix:     "Task complete",
			extractBody:     extractStringField("body"),
		},
	},
}

func extractStringField(field string) func(map[string]any) string {
	return func(payload map[string]any) string {
		if payload == nil {
			return ""
		}
		v, _ := payload[field].(string)
		return strings.TrimSpace(v)
	}
}

func extractCodexBody(payload map[string]any) string {
	if payload == nil {
		return ""
	}
	if s, _ := payload["last-assistant-message"].(string); strings.TrimSpace(s) != "" {
		return strings.TrimSpace(s)
	}
	raw, ok := payload["input-messages"].([]any)
	if !ok {
		return ""
	}
	parts := make([]string, 0, len(raw))
	for _, item := range raw {
		if s, ok := item.(string); ok {
			parts = append(parts, s)
		}
	}
	return strings.TrimSpace(strings.Join(parts, " "))
}

// NewEmitHookCmd records a notification derived from an agent hook payload.
// Always exits 0; errors go to notifytun-errors.log next to the DB.
func NewEmitHookCmd() *cobra.Command {
	var (
		tool       string
		event      string
		dbPath     string
		socketPath string
	)

	home, _ := os.UserHomeDir()
	defaultDB := home + "/.notifytun/notifytun.db"
	defaultSocket := home + "/.notifytun/notifytun.sock"

	cmd := &cobra.Command{
		Use:           "emit-hook [payload-json]",
		Short:         "Record a notification derived from an agent hook payload",
		Args:          cobra.ArbitraryArgs,
		SilenceUsage:  true,
		SilenceErrors: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			if len(args) > 1 {
				LogHookError(dbPath, "emit-hook", "parse",
					fmt.Errorf("unexpected positional argument count: got %d, want 0 or 1", len(args)))
				args = args[:1]
			}

			dispatch, ok := lookupDispatch(tool, event)
			if !ok {
				LogHookError(dbPath, "emit-hook", "dispatch",
					fmt.Errorf("unknown tool/event: %s/%s", tool, event))
				return nil
			}

			payloadBytes, err := readPayload(args, cmd.InOrStdin())
			if err != nil {
				LogHookError(dbPath, "emit-hook", "parse", err)
			}

			var payload map[string]any
			if len(payloadBytes) > 0 {
				if err := json.Unmarshal(payloadBytes, &payload); err != nil {
					LogHookError(dbPath, "emit-hook", "parse", err)
					payload = nil
				}
			}

			title := dispatch.toolDisplayName + ": " + dispatch.titleSuffix
			body := truncateRunes(dispatch.extractBody(payload), 180)

			d, err := db.Open(dbPath)
			if err != nil {
				LogHookError(dbPath, "emit-hook", "db-open", err)
				return nil
			}
			defer d.Close()

			if _, err := d.Insert(title, body, tool); err != nil {
				LogHookError(dbPath, "emit-hook", "db-insert", err)
				return nil
			}

			_ = socket.SendWakeup(socketPath)
			return nil
		},
	}

	cmd.Flags().StringVar(&tool, "tool", "", "Source tool name (claude-code|gemini|codex|opencode)")
	cmd.Flags().StringVar(&event, "event", "", "Hook event name (Stop|Notification|AfterAgent|notify|session.idle)")
	cmd.Flags().StringVar(&dbPath, "db", defaultDB, "SQLite database path")
	cmd.Flags().StringVar(&socketPath, "socket", defaultSocket, "Unix socket path")

	return cmd
}

func lookupDispatch(tool, event string) (hookDispatch, bool) {
	byEvent, ok := hookTable[tool]
	if !ok {
		return hookDispatch{}, false
	}
	d, ok := byEvent[event]
	return d, ok
}

func readPayload(args []string, stdin io.Reader) ([]byte, error) {
	if len(args) == 1 {
		return []byte(args[0]), nil
	}
	if stdin == nil {
		return nil, nil
	}
	data, err := io.ReadAll(stdin)
	if err != nil {
		return nil, fmt.Errorf("read stdin: %w", err)
	}
	return data, nil
}

func truncateRunes(s string, max int) string {
	runes := []rune(s)
	if len(runes) <= max {
		return s
	}
	return string(runes[:max])
}
