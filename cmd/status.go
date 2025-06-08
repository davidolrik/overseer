package cmd

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"time"

	"github.com/spf13/cobra"
	"olrik.dev/davidolrik/overseer/internal/daemon"
)

func NewStatusCommand() *cobra.Command {
	statusCmd := &cobra.Command{
		Use:   "status",
		Short: "Shows a list of all currently active tunnels",
		Args:  cobra.NoArgs,
		Run: func(cmd *cobra.Command, args []string) {
			response, err := daemon.SendCommand("STATUS")
			if err != nil {
				slog.Warn("No active tunnels (daemon is not running).")
				return
			}

			jsonBytes, _ := json.Marshal(response.Data)
			statuses := []daemon.DaemonStatus{}
			json.Unmarshal(jsonBytes, &statuses)

			format, _ := cmd.Flags().GetString("format")
			switch format {
			case "text":
				fmt.Println("Active Tunnels:")
				for _, status := range statuses {
					startDate, _ := time.Parse(time.RFC3339, status.StartDate)
					age := time.Since(startDate)
					fmt.Printf(
						"  - %s (PID: %d, Age: %s)\n",
						status.Hostname, status.Pid, age.Round(time.Second).String(),
					)
				}
			case "json":
				fmt.Println(string(jsonBytes))
			default:
				slog.Error("unknown format")
				os.Exit(1)
			}
		},
	}
	statusCmd.Flags().StringP("format", "F", "text", "Format to use (text/json)")

	return statusCmd
}
