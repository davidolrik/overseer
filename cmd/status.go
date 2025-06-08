package cmd

import (
	"encoding/json"
	"fmt"
	"log"

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
				log.Println("No active tunnels (daemon is not running).")
				return
			}

			statuses := []daemon.DaemonStatus{}
			err = json.Unmarshal([]byte(response), &statuses)
			if err != nil {
				panic(err)
			}

			format, _ := cmd.Flags().GetString("format")
			switch format {
			case "text":
				fmt.Println("Active Tunnels:")
				for _, status := range statuses {
					fmt.Printf("  - %s (PID: %d)\n", status.Hostname, status.Pid)
				}
			case "json":
				fmt.Print(response)
			default:
				panic("Unknown format")
			}
		},
	}
	statusCmd.Flags().StringP("format", "F", "text", "Format to use (text/json)")

	return statusCmd
}
