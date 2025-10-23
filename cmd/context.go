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
		Short: "Show current security context",
		Long: `Display the current security context including location, sensors, and recent changes.

The security context is determined by various sensors (like public IP address) and rules
defined in your configuration file. Context changes can automatically connect or disconnect
SSH tunnels based on your location or network.`,
		Aliases: []string{"ctx"},
		Run: func(cmd *cobra.Command, args []string) {
			daemon.EnsureDaemonIsRunning()
			daemon.CheckVersionMismatch()

			response, err := daemon.SendCommand("CONTEXT_STATUS")
			if err != nil {
				slog.Error(fmt.Sprintf("Error communicating with daemon: %v", err))
				return
			}

			// Handle response messages (only in text mode)
			format, _ := cmd.Flags().GetString("format")
			if format != "json" {
				for _, msg := range response.Messages {
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
			if response.Data != nil {
				displayContextStatus(response.Data, format)
			}
		},
	}

	cmd.Flags().StringP("format", "F", "text", "Format to use (text/json)")

	return cmd
}

func displayContextStatus(data interface{}, format string) {
	// Parse the data as JSON
	jsonData, err := json.Marshal(data)
	if err != nil {
		slog.Error(fmt.Sprintf("Failed to parse context status: %v", err))
		return
	}

	// Handle JSON format
	if format == "json" {
		fmt.Println(string(jsonData))
		return
	}

	// Handle text format
	if format != "text" {
		slog.Error("unknown format")
		os.Exit(1)
	}

	var status struct {
		Context       string            `json:"context"`
		LastChange    string            `json:"last_change"`
		Uptime        string            `json:"uptime"`
		Sensors       map[string]string `json:"sensors"`
		ChangeHistory []struct {
			From      string `json:"from"`
			To        string `json:"to"`
			Timestamp string `json:"timestamp"`
			Trigger   string `json:"trigger"`
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
			fmt.Printf("  %s → %s (%s)\n", change.From, change.To, change.Trigger)
		}
	}
}
