package cmd

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"sort"
	"time"

	"github.com/spf13/cobra"
	"olrik.dev/davidolrik/overseer/internal/daemon"
)

func NewStatusCommand() *cobra.Command {
	statusCmd := &cobra.Command{
		Use:     "status",
		Aliases: []string{"list", "ls"},
		Short: "Shows a list of all currently active tunnels",
		Args:  cobra.NoArgs,
		Run: func(cmd *cobra.Command, args []string) {
			daemon.CheckVersionMismatch()
			response, err := daemon.SendCommand("STATUS")
			if err != nil {
				slog.Warn("No active tunnels (daemon is not running).")
				return
			}

			jsonBytes, _ := json.Marshal(response.Data)
			statuses := []daemon.DaemonStatus{}
			json.Unmarshal(jsonBytes, &statuses)

			// Sort tunnels by hostname for consistent output
			sort.Slice(statuses, func(i, j int) bool {
				return statuses[i].Hostname < statuses[j].Hostname
			})

			format, _ := cmd.Flags().GetString("format")
			switch format {
			case "text":
				// Show security context if enabled
			// Show security context (always active)
			contextResponse, err := daemon.SendCommand("CONTEXT_STATUS")
			if err == nil && contextResponse.Data != nil {
				displayContextBanner(contextResponse.Data)
			}

				fmt.Println("Active Tunnels:")
				for _, status := range statuses {
					// Use LastConnectedTime for age (resets to 0 on reconnection)
					lastConnected, _ := time.Parse(time.RFC3339, status.LastConnectedTime)
					age := time.Since(lastConnected)

					// ANSI color codes
					const (
						colorGreen  = "\033[32m"
						colorYellow = "\033[33m"
						colorRed    = "\033[31m"
						colorReset  = "\033[0m"
					)

					// Build state indicator with colored icon and alias
					var icon, color, extraInfo string
					switch status.State {
					case "connected":
						icon = "✓"
						color = colorGreen
					case "disconnected":
						icon = "✗"
						color = colorRed
					case "reconnecting":
						icon = "⟳"
						color = colorYellow
						if status.NextRetry != "" {
							nextRetry, err := time.Parse(time.RFC3339, status.NextRetry)
							if err == nil {
								timeUntil := time.Until(nextRetry)
								if timeUntil > 0 {
									extraInfo = fmt.Sprintf(" (next attempt in %s)", timeUntil.Round(time.Second))
								} else {
									extraInfo = " (attempting now)"
								}
							}
						}
						if status.RetryCount > 0 {
							extraInfo += fmt.Sprintf(" [attempt %d]", status.RetryCount)
						}
					}

					// Build reconnect count info
					reconnectInfo := ""
					if status.TotalReconnects > 0 {
						reconnectInfo = fmt.Sprintf(", Reconnects: %d", status.TotalReconnects)
					}

					fmt.Printf(
						"  %s%s%s %s%s%s (PID: %d, Age: %s%s)%s\n",
						color, icon, colorReset,
						color, status.Hostname, colorReset,
						status.Pid, age.Round(time.Second).String(),
						reconnectInfo,
						extraInfo,
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

// displayContextBanner shows a compact context banner at the top of status output
func displayContextBanner(data interface{}) {
	jsonData, err := json.Marshal(data)
	if err != nil {
		return
	}

	var status struct {
		Context string            `json:"context"`
		Sensors map[string]string `json:"sensors"`
	}

	if err := json.Unmarshal(jsonData, &status); err != nil {
		return
	}

	// ANSI color codes
	const (
		colorCyan  = "\033[36m"
		colorReset = "\033[0m"
	)

	// Display compact context info
	fmt.Printf("%sContext:%s %s", colorCyan, colorReset, status.Context)

	// Show public IP if available
	if ip, ok := status.Sensors["public_ip"]; ok && ip != "" {
		fmt.Printf(" (Public IP: %s)", ip)
	}

	fmt.Println()
	fmt.Println()
}
