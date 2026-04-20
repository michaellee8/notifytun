package cli

import (
	"encoding/json"
	"errors"
	"os"
	"strings"

	"github.com/michaellee8/notifytun/internal/db"
	"github.com/michaellee8/notifytun/internal/socket"
	"github.com/spf13/cobra"
)

type codexNotifyPayload struct {
	Type                 string   `json:"type"`
	InputMessages        []string `json:"input-messages"`
	LastAssistantMessage string   `json:"last-assistant-message"`
}

// NewEmitCmd records a notification from a tool hook.
// Always exits 0; errors go to notifytun-errors.log next to the DB.
func NewEmitCmd() *cobra.Command {
	var (
		title      string
		body       string
		tool       string
		dbPath     string
		socketPath string
	)

	home, _ := os.UserHomeDir()
	defaultDB := home + "/.notifytun/notifytun.db"
	defaultSocket := home + "/.notifytun/notifytun.sock"

	cmd := &cobra.Command{
		Use:           "emit [codex-notify-json]",
		Short:         "Record a notification (called by tool hooks)",
		Args:          cobra.MaximumNArgs(1),
		SilenceUsage:  true,
		SilenceErrors: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			if len(args) == 1 && title == "" {
				var payload codexNotifyPayload
				if err := json.Unmarshal([]byte(args[0]), &payload); err == nil && payload.Type == "agent-turn-complete" {
					title = "Task complete"
					body = strings.TrimSpace(payload.LastAssistantMessage)
					if body == "" {
						body = strings.TrimSpace(strings.Join(payload.InputMessages, " "))
					}
				}
			}

			if title == "" {
				LogHookError(dbPath, "emit", "parse", errors.New("missing notification title"))
				return nil
			}

			d, err := db.Open(dbPath)
			if err != nil {
				LogHookError(dbPath, "emit", "db-open", err)
				return nil
			}
			defer d.Close()

			if _, err := d.Insert(title, body, tool); err != nil {
				LogHookError(dbPath, "emit", "db-insert", err)
				return nil
			}

			_ = socket.SendWakeup(socketPath)
			return nil
		},
	}

	cmd.Flags().StringVar(&title, "title", "", "Notification title (required unless derived from Codex payload)")
	cmd.Flags().StringVar(&body, "body", "", "Notification body")
	cmd.Flags().StringVar(&tool, "tool", "", "Source tool name")
	cmd.Flags().StringVar(&dbPath, "db", defaultDB, "SQLite database path")
	cmd.Flags().StringVar(&socketPath, "socket", defaultSocket, "Unix socket path")

	return cmd
}
