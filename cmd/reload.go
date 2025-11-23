package cmd

import (
	"fmt"
	"log/slog"

	"github.com/spf13/cobra"
	"overseer.olrik.dev/internal/daemon"
)

func NewReloadCommand() *cobra.Command {
	var quiet bool

	cmd := &cobra.Command{
		Use:   "reload",
		Short: "Reload the overseer daemon without disconnecting tunnels (hot reload)",
		Long: `Reload the overseer daemon with zero downtime.

This command performs a hot reload by:
1. Saving the state of all active SSH tunnels
2. Gracefully stopping the current daemon
3. Starting a new daemon that adopts the existing tunnel processes

The SSH tunnel processes continue running throughout the reload, providing
zero downtime. This is ideal for upgrading overseer without interrupting
active connections.

If the hot reload fails (e.g., PIDs can't be validated), tunnels will be
reconnected automatically based on security context rules.`,
		Args: cobra.NoArgs,
		Run: func(cmd *cobra.Command, args []string) {
			// Check if daemon is running
			_, err := daemon.SendCommand("STATUS")
			if err != nil {
				if !quiet {
					slog.Error("Daemon is not running. Use 'overseer start' instead.")
				}
				return
			}

			if !quiet {
				slog.Info("Reloading daemon (hot reload - tunnels will be preserved)...")
			}

			// Send RELOAD command to trigger state save before shutdown
			_, err = daemon.SendCommand("RELOAD")
			if err != nil {
				if !quiet {
					slog.Error(fmt.Sprintf("Failed to send reload command: %v", err))
				}
				return
			}

			// Wait for daemon to fully stop
			if err := daemon.WaitForDaemonStop(); err != nil {
				if !quiet {
					slog.Warn(fmt.Sprintf("Daemon stop verification failed: %v", err))
				}
			}

			// Start new daemon (uses same logic as 'overseer start')
			if err := daemon.StartDaemon(); err != nil {
				if !quiet {
					slog.Error(fmt.Sprintf("Failed to start daemon: %v", err))
				}
				return
			}

			// Wait for daemon to be ready
			if err := daemon.WaitForDaemon(); err != nil {
				if !quiet {
					slog.Error(fmt.Sprintf("Daemon failed to start: %v", err))
				}
				return
			}

			if !quiet {
				slog.Info("Daemon reloaded successfully (tunnels preserved)")
			}
		},
	}

	cmd.Flags().BoolVarP(&quiet, "quiet", "q", false, "Suppress output")

	return cmd
}
