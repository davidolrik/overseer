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
	var force bool

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

			// When --force isn't explicitly set, default based on stdin TTY:
			// scripts/cron/CI get force (no one around to resolve a mux
			// conflict), interactive shells don't (the user's own ssh
			// session should not be killed out from under them).
			if !cmd.Flags().Changed("force") {
				force = !isStdinTerminal()
			}

			command := "SSH_CONNECT " + alias
			if force {
				command += " --force"
			}
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
	connectCmd.Flags().BoolVarP(&force, "force", "F", false,
		"Evict a conflicting SSH ControlMaster before connecting (default: auto — on when stdin is not a terminal)")

	return connectCmd
}
