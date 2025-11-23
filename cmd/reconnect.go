package cmd

import (
	"log/slog"
	"os"

	"github.com/spf13/cobra"
	"overseer.olrik.dev/internal/daemon"
)

func NewReconnectCommand() *cobra.Command {
	reconnectCmd := &cobra.Command{
		Use:               "reconnect <alias>",
		Aliases:           []string{"r"},
		Short:             "Reconnect SSH tunnel (disconnect then connect)",
		Long:              `Reconnect SSH tunnel by disconnecting and then connecting again`,
		Args:              cobra.ExactArgs(1),
		ValidArgsFunction: activeHostCompletionFunc,
		Run: func(cmd *cobra.Command, args []string) {
			alias := args[0]
			daemon.EnsureDaemonIsRunning()
			daemon.CheckVersionMismatch()

			// First disconnect
			response, err := daemon.SendCommand("SSH_DISCONNECT " + alias)
			if err != nil {
				slog.Error("Failed to disconnect", "error", err)
				os.Exit(1)
			}
			response.LogMessages()

			// Then connect
			response, err = daemon.SendCommand("SSH_CONNECT " + alias)
			if err != nil {
				slog.Error("Failed to connect", "error", err)
				os.Exit(1)
			}
			response.LogMessages()
		},
	}

	return reconnectCmd
}
