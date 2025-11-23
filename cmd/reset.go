package cmd

import (
	"log/slog"
	"os"

	"github.com/spf13/cobra"
	"overseer.olrik.dev/internal/daemon"
)

func NewResetCommand() *cobra.Command {
	resetCmd := &cobra.Command{
		Use:   "reset",
		Short: "Reset retry counters for all tunnels",
		Long: `Reset retry counters for all tunnels to zero.

This is useful after waking a laptop from sleep or recovering from network issues,
giving all reconnecting tunnels a fresh start with the initial backoff delay.

Only affects tunnels in the reconnecting state. Connected tunnels are unaffected.`,
		Args: cobra.NoArgs,
		Run: func(cmd *cobra.Command, args []string) {
			daemon.CheckVersionMismatch()
			response, err := daemon.SendCommand("RESET")
			if err != nil {
				slog.Error("Could not connect to daemon. Is overseer running?")
				os.Exit(1)
			}
			response.LogMessages()
		},
	}

	return resetCmd
}
