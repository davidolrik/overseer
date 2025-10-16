package cmd

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"os"

	"github.com/spf13/cobra"
	"olrik.dev/davidolrik/overseer/internal/core"
	"olrik.dev/davidolrik/overseer/internal/daemon"
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
				jsonBytes, _ := json.Marshal(response.Data)
				var versionData map[string]string
				if json.Unmarshal(jsonBytes, &versionData) == nil {
					daemonVersion := versionData["version"]
					daemonFormatted := core.FormatVersion(daemonVersion)
					fmt.Fprintf(os.Stderr, "Daemon version: %s\n", daemonFormatted)

					// Check for version mismatch
					if clientVersion != daemonVersion {
						slog.Warn(fmt.Sprintf("Version mismatch! Client %s and daemon %s versions differ. Consider restarting the daemon.", clientFormatted, daemonFormatted))
					}
				}
			}
		},
	}

	return versionCmd
}
