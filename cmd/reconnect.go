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

			// Use SSH_RECONNECT to preserve companion attach connections
			if err := daemon.SendCommandStreaming("SSH_RECONNECT " + alias); err != nil {
				slog.Error("Failed to reconnect", "error", err)
				os.Exit(1)
			}
		},
	}

	return reconnectCmd
}
