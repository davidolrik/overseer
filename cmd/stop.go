package cmd

import (
	"log/slog"

	"github.com/spf13/cobra"
	"olrik.dev/davidolrik/overseer/internal/daemon"
)

func NewStopCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "stop",
		Short: "Stop the overseer daemon",
		Long: `Stop the overseer daemon, disconnecting all SSH tunnels and stopping security context monitoring.

This will gracefully shutdown the daemon, cleaning up all resources.`,
		Aliases: []string{"shutdown", "quit"},
		Args:    cobra.NoArgs,
		Run: func(cmd *cobra.Command, args []string) {
			daemon.CheckVersionMismatch()

			response, err := daemon.SendCommand("STOP")
			if err != nil {
				slog.Warn("Daemon is not running")
				return
			}

			// Print all response messages
			for _, msg := range response.Messages {
				switch msg.Status {
				case "ERROR":
					slog.Error(msg.Message)
				case "WARN":
					slog.Warn(msg.Message)
				case "INFO":
					slog.Info(msg.Message)
				}
			}
		},
	}
}
