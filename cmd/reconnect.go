package cmd

import (
	"log/slog"
	"os"

	"github.com/spf13/cobra"
	"go.olrik.dev/overseer/internal/daemon"
)

func NewReconnectCommand() *cobra.Command {
	var force bool

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

			if !cmd.Flags().Changed("force") {
				force = !isStdinTerminal()
			}

			command := "SSH_RECONNECT " + alias
			if force {
				command += " --force"
			}

			// Use SSH_RECONNECT to preserve companion attach connections
			if err := daemon.SendCommandStreaming(command); err != nil {
				slog.Error("Failed to reconnect", "error", err)
				os.Exit(1)
			}
		},
	}

	reconnectCmd.Flags().BoolVarP(&force, "force", "F", false,
		"Evict a conflicting SSH ControlMaster before connecting (default: auto — on when stdin is not a terminal)")

	return reconnectCmd
}
