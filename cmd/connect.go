package cmd

import (
	"log/slog"
	"os"

	"github.com/spf13/cobra"
	"olrik.dev/davidolrik/overseer/internal/daemon"
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
			response, err := daemon.SendCommand("SSH_CONNECT " + alias)
			if err != nil {
				slog.Error(err.Error())
				os.Exit(1)
			}
			response.LogMessages()
		},
	}

	return connectCmd
}
