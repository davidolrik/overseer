package cmd

import (
	"fmt"
	"log/slog"

	"github.com/spf13/cobra"
	"go.olrik.dev/overseer/internal/daemon"
)

func NewStartCommand() *cobra.Command {
	var quiet bool
	var background bool

	cmd := &cobra.Command{
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
				// Daemon is running - exit silently with code 0 (success)
				if !quiet {
					// Get version if not in quiet mode
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
				}
				return
			}

			// Start the daemon
			if !quiet {
				slog.Info("Starting overseer daemon...")
			}
			daemonCmd, err := daemon.StartDaemon()
			if err != nil {
				if !quiet {
					slog.Error(fmt.Sprintf("Failed to start daemon: %v", err))
				}
				return
			}

			if background {
				if !quiet {
					slog.Info("Daemon starting in background")
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
				slog.Info("Daemon started successfully")
			}
		},
	}

	cmd.Flags().BoolVarP(&quiet, "quiet", "q", false, "Suppress output (useful for shell initialization)")
	cmd.Flags().BoolVarP(&background, "background", "B", false, "Start daemon in background without waiting for readiness")

	return cmd
}
