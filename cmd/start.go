package cmd

import (
	"fmt"
	"log/slog"

	"github.com/spf13/cobra"
	"olrik.dev/davidolrik/overseer/internal/daemon"
)

func NewStartCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "start",
		Short: "Start the overseer daemon",
		Long: `Start the overseer daemon in the background.

The daemon manages SSH tunnels and security context monitoring. It will continue
running until explicitly stopped with 'overseer stop'.

If the daemon is already running, this command will report its status.`,
		Aliases: []string{"startup", "boot"},
		Args:    cobra.NoArgs,
		Run: func(cmd *cobra.Command, args []string) {
			// Check if daemon is already running
			_, err := daemon.SendCommand("STATUS")
			if err == nil {
				// Daemon is running, get version
				response, _ := daemon.SendCommand("VERSION")
				if response.Data != nil {
					if versionData, ok := response.Data.(map[string]interface{}); ok {
						if version, ok := versionData["version"].(string); ok {
							slog.Info(fmt.Sprintf("Daemon is already running (version %s)", version))
							return
						}
					}
				}
				slog.Info("Daemon is already running")
				return
			}

			// Start the daemon
			slog.Info("Starting overseer daemon...")
			if err := daemon.StartDaemon(); err != nil {
				slog.Error(fmt.Sprintf("Failed to start daemon: %v", err))
				return
			}

			// Wait for daemon to be ready
			if err := daemon.WaitForDaemon(); err != nil {
				slog.Error(fmt.Sprintf("Daemon failed to start: %v", err))
				return
			}

			slog.Info("Daemon started successfully")
		},
	}
}
