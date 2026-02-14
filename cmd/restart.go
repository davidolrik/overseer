package cmd

import (
	"fmt"
	"log/slog"

	"github.com/spf13/cobra"
	"overseer.olrik.dev/internal/daemon"
)

func NewRestartCommand() *cobra.Command {
	var quiet bool

	cmd := &cobra.Command{
		Use:   "restart",
		Short: "Restart the overseer daemon",
		Long: `Restart the overseer daemon (cold restart).

This command stops the current daemon and starts a new one. The new daemon will
automatically detect the execution environment (local vs remote SSH session) and
monitor the appropriate parent process.

Active SSH tunnels will be disconnected and reconnected by the new daemon
based on the current security context.

For zero-downtime upgrades that preserve tunnel connections, use 'overseer reload' instead.`,
		Args:    cobra.NoArgs,
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
				slog.Info("Restarting daemon...")
			}

			// Stop the current daemon
			_, err = daemon.SendCommand("STOP")
			if err != nil {
				if !quiet {
					slog.Error(fmt.Sprintf("Failed to stop daemon: %v", err))
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
			daemonCmd, err := daemon.StartDaemon()
			if err != nil {
				if !quiet {
					slog.Error(fmt.Sprintf("Failed to start daemon: %v", err))
				}
				return
			}

			// Wait for daemon to be ready
			if err := daemon.WaitForDaemon(daemonCmd); err != nil {
				if !quiet {
					slog.Error(fmt.Sprintf("Daemon failed to start: %v", err))
				}
				return
			}

			if !quiet {
				slog.Info("Daemon restarted successfully")
			}
		},
	}

	cmd.Flags().BoolVarP(&quiet, "quiet", "q", false, "Suppress output")

	return cmd
}
