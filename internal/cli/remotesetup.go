package cli

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/michaellee8/notifytun/internal/setup"
	"github.com/spf13/cobra"
)

// NewRemoteSetupCmd detects supported AI tools and configures notifytun hooks for them.
func NewRemoteSetupCmd() *cobra.Command {
	var dryRun bool

	cmd := &cobra.Command{
		Use:           "remote-setup",
		Short:         "Detect AI tools and configure their hooks to call notifytun emit",
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
			writePreview(cmd.OutOrStdout(), tools)

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
		switch tools[i].Name {
		case "Claude Code":
			tools[i].Configured = setup.IsClaudeConfigured(filepath.Join(home, ".claude", "settings.json"))
		case "Codex CLI":
			tools[i].Configured = setup.IsCodexConfigured(filepath.Join(home, ".codex", "config.toml"))
		}
	}
}

func writePreview(w io.Writer, tools []setup.Tool) {
	preview := setup.Preview(tools)
	if strings.HasSuffix(preview, "\n") {
		fmt.Fprint(w, preview)
		return
	}
	fmt.Fprintln(w, preview)
}

func hasConfigWork(tools []setup.Tool) bool {
	for _, tool := range tools {
		if tool.Supported && !tool.Configured {
			return true
		}
	}
	return false
}

func confirmApply(in io.Reader, out io.Writer) bool {
	fmt.Fprint(out, "Apply? [Y/n] ")
	answer, err := bufio.NewReader(in).ReadString('\n')
	if err != nil && err != io.EOF {
		return false
	}
	answer = strings.TrimSpace(strings.ToLower(answer))
	return answer == "" || answer == "y" || answer == "yes"
}

func applyToolConfig(stdout, stderr io.Writer, home string, tools []setup.Tool) {
	for _, tool := range tools {
		if !tool.Supported || tool.Configured {
			continue
		}

		switch tool.Name {
		case "Claude Code":
			settingsPath := filepath.Join(home, ".claude", "settings.json")
			if err := setup.ApplyClaudeHook(settingsPath); err != nil {
				fmt.Fprintf(stderr, "warning: failed to configure %s: %v\n", tool.Name, err)
				continue
			}
			fmt.Fprintf(stdout, "Configured %s hooks in %s\n", tool.Name, settingsPath)
		case "Codex CLI":
			configPath := filepath.Join(home, ".codex", "config.toml")
			if err := setup.ApplyCodexNotifyConfig(configPath); err != nil {
				fmt.Fprintf(stderr, "warning: failed to configure %s: %v\n", tool.Name, err)
				continue
			}
			fmt.Fprintf(stdout, "Configured %s notify in %s\n", tool.Name, configPath)
		}
	}
}
