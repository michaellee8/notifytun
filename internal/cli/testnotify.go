package cli

import (
	"fmt"
	"io"

	"github.com/michaellee8/notifytun/internal/notifier"
	"github.com/spf13/cobra"
)

const (
	testNotifyTitle = "notifytun"
	testNotifyBody  = "Test notification - if you see this, your backend is working!"
	testNotifyTool  = "test"
)

type outputTracker struct {
	w              io.Writer
	wrote          bool
	endedWithNewln bool
}

func (o *outputTracker) Write(p []byte) (int, error) {
	if len(p) > 0 {
		o.wrote = true
		o.endedWithNewln = p[len(p)-1] == '\n'
	}
	return o.w.Write(p)
}

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
			stdout := cmd.OutOrStdout()
			var tracker *outputTracker

			n, err := notifier.New(backend, notifyCmd)
			if err != nil {
				return fmt.Errorf("init notifier: %w", err)
			}
			if outputConfigurer, ok := n.(notifier.CommandOutputConfigurer); ok {
				tracker = &outputTracker{w: stdout}
				outputConfigurer.SetCommandOutput(tracker, cmd.ErrOrStderr())
			}

			notification := notifier.Notification{
				Title: testNotifyTitle,
				Body:  testNotifyBody,
				Tool:  testNotifyTool,
			}

			if err := n.Notify(cmd.Context(), notification); err != nil {
				return fmt.Errorf("notification failed: %w", err)
			}

			if tracker != nil && tracker.wrote && !tracker.endedWithNewln {
				fmt.Fprintln(stdout)
			}

			fmt.Fprintln(stdout, "Test notification sent successfully.")
			return nil
		},
	}

	cmd.Flags().StringVar(&backend, "backend", "auto", "Notifier backend: auto, generic")
	cmd.Flags().StringVar(&notifyCmd, "notify-cmd", "", "Custom command for generic backend")

	return cmd
}
