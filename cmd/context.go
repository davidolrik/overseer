package cmd

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"os"

	"github.com/spf13/cobra"
	"olrik.dev/davidolrik/overseer/internal/daemon"
)

func NewContextCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "context",
		Short: "Show current security context (alias for 'status --verbose')",
		Long: `Display the current security context including location, sensors, and recent changes.

NOTE: This command is now an alias for 'overseer status --verbose'. Use that instead.

The security context is determined by various sensors (like public IP address) and rules
defined in your configuration file. Context changes can automatically connect or disconnect
SSH tunnels based on your location or network.`,
		Aliases: []string{"ctx"},
		Deprecated: "use 'overseer status --verbose' instead",
		Run: func(cmd *cobra.Command, args []string) {
			daemon.EnsureDaemonIsRunning()
			daemon.CheckVersionMismatch()

			// Get context status
			contextResponse, err := daemon.SendCommand("CONTEXT_STATUS")
			if err != nil {
				slog.Error(fmt.Sprintf("Error communicating with daemon: %v", err))
				return
			}

			// Get tunnel status
			tunnelResponse, tunnelErr := daemon.SendCommand("STATUS")
			var statuses []daemon.DaemonStatus
			if tunnelErr == nil && tunnelResponse.Data != nil {
				jsonBytes, _ := json.Marshal(tunnelResponse.Data)
				json.Unmarshal(jsonBytes, &statuses)
			}

			// Handle response messages (only in text mode)
			format, _ := cmd.Flags().GetString("format")
			if format != "json" {
				for _, msg := range contextResponse.Messages {
					switch msg.Status {
					case "ERROR":
						slog.Error(msg.Message)
					case "WARN":
						slog.Warn(msg.Message)
					case "INFO":
						if msg.Message != "OK" {
							slog.Info(msg.Message)
						}
					}
				}
			}

			// Display context status
			if contextResponse.Data != nil {
				displayContextStatus(contextResponse.Data, statuses, format)
			}
		},
	}

	cmd.Flags().StringP("format", "F", "text", "Format to use (text/json)")

	return cmd
}

func displayContextStatus(data interface{}, statuses []daemon.DaemonStatus, format string) {
	// Parse the data as JSON
	jsonData, err := json.Marshal(data)
	if err != nil {
		slog.Error(fmt.Sprintf("Failed to parse context status: %v", err))
		return
	}

	// Handle JSON format
	if format == "json" {
		// Combine context and tunnels for JSON output
		output := make(map[string]interface{})
		output["context"] = data
		output["tunnels"] = statuses
		jsonOutput, _ := json.MarshalIndent(output, "", "  ")
		fmt.Println(string(jsonOutput))
		return
	}

	// Handle text format
	if format != "text" {
		slog.Error("unknown format")
		os.Exit(1)
	}

	var status struct {
		Context       string            `json:"context"`
		Location      string            `json:"location,omitempty"`
		LastChange    string            `json:"last_change"`
		Uptime        string            `json:"uptime"`
		Sensors       map[string]string `json:"sensors"`
		ChangeHistory []struct {
			From         string `json:"from"`
			To           string `json:"to"`
			FromLocation string `json:"from_location,omitempty"`
			ToLocation   string `json:"to_location,omitempty"`
			Timestamp    string `json:"timestamp"`
			Trigger      string `json:"trigger"`
		} `json:"change_history"`
	}

	if err := json.Unmarshal(jsonData, &status); err != nil {
		slog.Error(fmt.Sprintf("Failed to parse context status: %v", err))
		return
	}

	// Display current context
	const (
		colorCyan  = "\033[36m"
		colorReset = "\033[0m"
	)

	fmt.Printf("Current Context: %s%s%s\n", colorCyan, status.Context, colorReset)
	if status.Location != "" {
		fmt.Printf("Location:        %s\n", status.Location)
	}
	fmt.Printf("Context Age:     %s\n", status.Uptime)

	// Display sensors
	if len(status.Sensors) > 0 {
		fmt.Printf("\nSensors:\n")
		for key, value := range status.Sensors {
			fmt.Printf("  %s: %s\n", key, value)
		}
	}

	// Display recent changes
	if len(status.ChangeHistory) > 0 {
		fmt.Printf("\nRecent Changes:\n")
		for _, change := range status.ChangeHistory {
			// Build the change display string
			changeStr := fmt.Sprintf("%s → %s", change.From, change.To)

			// Add location information if available
			if change.FromLocation != "" || change.ToLocation != "" {
				changeStr += " ("
				if change.FromLocation != "" {
					changeStr += change.FromLocation
				} else {
					changeStr += "no location"
				}
				changeStr += " → "
				if change.ToLocation != "" {
					changeStr += change.ToLocation
				} else {
					changeStr += "no location"
				}
				changeStr += ")"
			}

			changeStr += fmt.Sprintf(" [%s]", change.Trigger)
			fmt.Printf("  %s\n", changeStr)
		}
	}

	// Display tunnels if any
	if len(statuses) > 0 {
		fmt.Printf("\nActive Tunnels:\n")
		for _, tunnel := range statuses {
			fmt.Printf("  %s (PID: %d, State: %s)\n", tunnel.Hostname, tunnel.Pid, tunnel.State)
		}
	}
}
