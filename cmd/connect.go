package cmd

import (
	"log/slog"
	"os"

	"github.com/spf13/cobra"
	"overseer.olrik.dev/internal/daemon"
)

func NewConnectCommand() *cobra.Command {
	connectCmd := &cobra.Command{
		Use:     "connect",
		Aliases: []string{"c"},
		Short:   "Connect SSH tunnel",
		Long:    `Connect SSH tunnel`,
		Args:    cobra.ExactArgs(1),
		ValidArgsFunction: sshHostCompletionFunc,
		Run: func(cmd *cobra.Command, args []string) {
			alias := args[0]
			daemon.EnsureDaemonIsRunning()
			daemon.CheckVersionMismatch()
			// Use streaming to show companion startup progress in real-time
			if err := daemon.SendCommandStreaming("SSH_CONNECT " + alias); err != nil {
				slog.Error(err.Error())
				os.Exit(1)
			}
		},
	}

	return connectCmd
}
