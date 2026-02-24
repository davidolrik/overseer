package cmd

import (
	"fmt"
	"log/slog"
	"os"
	"strings"

	"github.com/spf13/cobra"
	"go.olrik.dev/overseer/internal/daemon"
)

func NewConnectCommand() *cobra.Command {
	var envVars []string

	connectCmd := &cobra.Command{
		Use:               "connect",
		Aliases:           []string{"c"},
		Short:             "Connect SSH tunnel",
		Long:              `Connect SSH tunnel`,
		Args:              cobra.ExactArgs(1),
		ValidArgsFunction: sshHostCompletionFunc,
		Run: func(cmd *cobra.Command, args []string) {
			alias := args[0]

			// Validate env var format
			for _, e := range envVars {
				idx := strings.Index(e, "=")
				if idx <= 0 {
					fmt.Fprintf(os.Stderr, "Error: invalid env var %q (expected KEY=VALUE)\n", e)
					os.Exit(1)
				}
			}

			daemon.EnsureDaemonIsRunning()
			daemon.CheckVersionMismatch()

			// Build command with optional env vars
			command := "SSH_CONNECT " + alias
			for _, e := range envVars {
				command += " --env=" + e
			}

			// Use streaming to show companion startup progress in real-time
			if err := daemon.SendCommandStreaming(command); err != nil {
				slog.Error(err.Error())
				os.Exit(1)
			}
		},
	}

	connectCmd.Flags().StringArrayVarP(&envVars, "env", "E", nil,
		"Set environment variable on the SSH process (repeatable, format: KEY=VALUE)")

	return connectCmd
}
