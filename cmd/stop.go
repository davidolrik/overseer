package cmd

import (
	"log/slog"
	"time"

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

			// Wait for daemon to fully shut down
			// Poll for up to 5 seconds to see if daemon is still responding
			maxWait := 5 * time.Second
			pollInterval := 100 * time.Millisecond
			elapsed := time.Duration(0)

			for elapsed < maxWait {
				time.Sleep(pollInterval)
				elapsed += pollInterval

				// Try to ping the daemon
				_, err := daemon.SendCommand("STATUS")
				if err != nil {
					// Daemon is no longer responding - shutdown complete
					slog.Debug("Daemon shutdown confirmed")
					return
				}
			}

			slog.Warn("Daemon did not shut down within timeout, but stop command was sent")
		},
	}
}
