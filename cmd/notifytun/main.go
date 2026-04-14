package main

import (
	"errors"
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
}

func main() {
	if err := rootCmd.Execute(); err != nil {
		var exitErr *cli.ExitError
		if errors.As(err, &exitErr) {
			if !exitErr.Silent && exitErr.Err != nil {
				fmt.Fprintln(os.Stderr, exitErr.Err)
			}
			os.Exit(exitErr.Code)
		}

		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
