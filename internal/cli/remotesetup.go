package cli

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/michaellee8/notifytun/internal/setup"
	"github.com/spf13/cobra"
)

// NewRemoteSetupCmd detects supported AI tools and configures notifytun hooks for them.
func NewRemoteSetupCmd() *cobra.Command {
	var dryRun bool

	cmd := &cobra.Command{
		Use:           "remote-setup",
		Short:         "Detect AI tools and configure their hooks to call notifytun emit-hook",
		Args:          cobra.NoArgs,
		SilenceUsage:  true,
		SilenceErrors: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			tools := setup.DetectTools("")
			if len(tools) == 0 {
				fmt.Fprintln(cmd.OutOrStdout(), "No supported AI tools detected in PATH.")
				return nil
			}

			home, err := os.UserHomeDir()
			if err != nil {
				return fmt.Errorf("resolve home directory: %w", err)
			}

			markConfigured(home, tools)
			writePreview(cmd.OutOrStdout(), home, tools)

			if dryRun {
				fmt.Fprintln(cmd.OutOrStdout(), "(dry run — no changes applied)")
				return nil
			}

			if !hasConfigWork(tools) {
				fmt.Fprintln(cmd.OutOrStdout(), "Nothing to configure — all supported tools already set up.")
				return nil
			}

			if !confirmApply(cmd.InOrStdin(), cmd.OutOrStdout()) {
				fmt.Fprintln(cmd.OutOrStdout(), "Aborted.")
				return nil
			}

			applyToolConfig(cmd.OutOrStdout(), cmd.ErrOrStderr(), home, tools)
			return nil
		},
	}

	cmd.Flags().BoolVar(&dryRun, "dry-run", false, "Show what would be configured without applying")
	return cmd
}

func markConfigured(home string, tools []setup.Tool) {
	for i := range tools {
		if tools[i].Cfg == nil {
			continue
		}
		tools[i].Configured = tools[i].Cfg.IsConfigured(home)
	}
}

func writePreview(w io.Writer, home string, tools []setup.Tool) {
	var sb strings.Builder
	sb.WriteString("Detected tools:\n")
	for _, tool := range tools {
		switch {
		case tool.Configured:
			sb.WriteString(fmt.Sprintf("  * %s -- already configured\n", tool.Name))
		case tool.Cfg != nil:
			sb.WriteString(fmt.Sprintf("  * %s -- %s\n", tool.Name, tool.Cfg.PreviewAction(home)))
		default:
			sb.WriteString(fmt.Sprintf("  * %s -- detected but hook setup not supported in v1\n", tool.Name))
		}
	}
	fmt.Fprint(w, sb.String())
}

func hasConfigWork(tools []setup.Tool) bool {
	for _, tool := range tools {
		if tool.Cfg != nil && !tool.Configured {
			return true
		}
	}
	return false
}

func confirmApply(in io.Reader, out io.Writer) bool {
	fmt.Fprint(out, "Apply? [Y/n] ")
	answer, err := bufio.NewReader(in).ReadString('\n')
	answer = strings.TrimSpace(strings.ToLower(answer))
	if err != nil {
		if err == io.EOF {
			return answer == "y" || answer == "yes"
		}
		return false
	}
	return answer == "" || answer == "y" || answer == "yes"
}

func applyToolConfig(stdout, stderr io.Writer, home string, tools []setup.Tool) {
	for _, tool := range tools {
		if tool.Cfg == nil || tool.Configured {
			continue
		}
		if err := tool.Cfg.Apply(home); err != nil {
			fmt.Fprintf(stderr, "warning: failed to configure %s: %v\n", tool.Name, err)
			continue
		}
		fmt.Fprintf(stdout, "Configured %s at %s\n", tool.Name, tool.Cfg.ConfigPath(home))
	}
}
