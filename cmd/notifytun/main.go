package main

import (
	"fmt"
	"os"

	"github.com/michaellee8/notifytun/internal/cli"
	"github.com/spf13/cobra"
)

var rootCmd = &cobra.Command{
	Use:   "notifytun",
	Short: "Tunnel notifications from a remote VM to your local desktop over SSH",
}

func init() {
	rootCmd.AddCommand(cli.NewEmitCmd())
	rootCmd.AddCommand(cli.NewEmitHookCmd())
	rootCmd.AddCommand(cli.NewAttachCmd())
	rootCmd.AddCommand(cli.NewLocalCmd())
	rootCmd.AddCommand(cli.NewRemoteSetupCmd())
	rootCmd.AddCommand(cli.NewTestNotifyCmd())
}

func main() {
	if err := rootCmd.Execute(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
