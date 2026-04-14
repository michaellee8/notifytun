package cli

import (
	"encoding/json"
	"fmt"

	"github.com/michaellee8/notifytun/internal/notifier"
	"github.com/spf13/cobra"
)

const (
	testNotifyTitle = "notifytun"
	testNotifyBody  = "Test notification - if you see this, your backend is working!"
	testNotifyTool  = "test"
)

// NewTestNotifyCmd fires a sample local notification to verify the configured backend.
func NewTestNotifyCmd() *cobra.Command {
	var (
		backend   string
		notifyCmd string
	)

	cmd := &cobra.Command{
		Use:           "test-notify",
		Short:         "Fire a test notification to verify the backend works",
		Args:          cobra.NoArgs,
		SilenceUsage:  true,
		SilenceErrors: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			n, err := notifier.New(backend, notifyCmd)
			if err != nil {
				return fmt.Errorf("init notifier: %w", err)
			}

			notification := notifier.Notification{
				Title: testNotifyTitle,
				Body:  testNotifyBody,
				Tool:  testNotifyTool,
			}

			if err := n.Notify(cmd.Context(), notification); err != nil {
				return fmt.Errorf("notification failed: %w", err)
			}

			if _, ok := n.(*notifier.Generic); ok {
				payload, err := json.Marshal(map[string]string{
					"title": notification.Title,
					"body":  notification.Body,
					"tool":  notification.Tool,
				})
				if err != nil {
					return fmt.Errorf("marshal notification payload: %w", err)
				}
				fmt.Fprintln(cmd.OutOrStdout(), string(payload))
			}

			fmt.Fprintln(cmd.OutOrStdout(), "Test notification sent successfully.")
			return nil
		},
	}

	cmd.Flags().StringVar(&backend, "backend", "auto", "Notifier backend: auto, macos, linux, generic")
	cmd.Flags().StringVar(&notifyCmd, "notify-cmd", "", "Custom command for generic backend")

	return cmd
}
