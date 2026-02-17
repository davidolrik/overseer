package cmd

import (
	"fmt"
	"log/slog"
	"os"

	"github.com/spf13/cobra"
	"go.olrik.dev/overseer/internal/core"
	"go.olrik.dev/overseer/internal/daemon"
)

func NewVersionCommand() *cobra.Command {
	versionCmd := &cobra.Command{
		Use:     "version",
		Aliases: []string{},
		Short:   "Show version",
		Long:    `Show version of both client and daemon (if running)`,
		Run: func(cmd *cobra.Command, args []string) {
			clientVersion := core.Version
			clientFormatted := core.FormatVersion(clientVersion)
			fmt.Fprintf(os.Stderr, "Client version: %s\n", clientFormatted)

			// Try to get daemon version
			response, err := daemon.SendCommand("VERSION")
			if err != nil {
				fmt.Fprintln(os.Stderr, "Daemon: not running")
				return
			}

			// Parse daemon version from response
			if response.Data != nil {
				// Data comes back as map[string]interface{} from JSON unmarshaling
				if dataMap, ok := response.Data.(map[string]interface{}); ok {
					if version, ok := dataMap["version"].(string); ok {
						daemonFormatted := core.FormatVersion(version)
						fmt.Fprintf(os.Stderr, "Daemon version: %s\n", daemonFormatted)

						// Check for version mismatch
						if clientVersion != version {
							slog.Warn(fmt.Sprintf("Version mismatch! Client %s and daemon %s versions differ. Consider restarting the daemon.", clientFormatted, daemonFormatted))
						}
					}
				}
			}
		},
	}

	return versionCmd
}
