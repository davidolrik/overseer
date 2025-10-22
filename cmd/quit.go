package cmd

import (
	"log/slog"
	"os"

	"github.com/spf13/cobra"
	"olrik.dev/davidolrik/overseer/internal/daemon"
)

func NewQuitCommand() *cobra.Command {
	quitCmd := &cobra.Command{
		Use:     "quit",
		Aliases: []string{"exit", "shutdown"},
		Short:   "Stop all tunnels and shutdown the daemon",
		Long:    `Stops all active SSH tunnels and shuts down the overseer daemon.`,
		Args:    cobra.NoArgs,
		Run: func(cmd *cobra.Command, args []string) {
			daemon.CheckVersionMismatch()
			response, err := daemon.SendCommand("SSH_DISCONNECT_ALL")
			if err != nil {
				slog.Error("Could not connect to daemon. Nothing to stop.")
				os.Exit(1)
			}
			response.LogMessages()
		},
	}

	return quitCmd
}
