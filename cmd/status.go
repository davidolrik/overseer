package cmd

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"sort"
	"time"

	"github.com/spf13/cobra"
	"overseer.olrik.dev/internal/daemon"
)

func NewStatusCommand() *cobra.Command {
	statusCmd := &cobra.Command{
		Use:     "status",
		Aliases: []string{"s", "list", "ls", "context", "ctx"},
		Short:   "Shows current security context, sensors, and active tunnels",
		Long: `Display comprehensive status including security context, sensor values, and active SSH tunnels.

The security context is determined by sensors (public IP, environment variables, online status, etc.)
and rules defined in your configuration. Context changes automatically connect or disconnect tunnels.`,
		Args: cobra.NoArgs,
		Run: func(cmd *cobra.Command, args []string) {
			daemon.CheckVersionMismatch()

			// Get tunnel status
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

			// Get context status with event limit
			eventLimit, _ := cmd.Flags().GetInt("events")
			contextResponse, err := daemon.SendCommand(fmt.Sprintf("CONTEXT_STATUS %d", eventLimit))

			format, _ := cmd.Flags().GetString("format")

			switch format {
			case "text":
				// Show comprehensive context information
				if err == nil && contextResponse.Data != nil {
					displayContextBanner(contextResponse.Data)
					displayContextInfo(contextResponse.Data)
				}

				fmt.Println("Active Tunnels:")
				if len(statuses) == 0 {
					fmt.Println("  (none)")
				}
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

				// Show recent events after tunnels in verbose mode
				if eventLimit > 0 && err == nil && contextResponse.Data != nil {
					displayRecentEvents(contextResponse.Data)
				}
			case "json":
				// Combine tunnel and context status for JSON output
				output := make(map[string]interface{})
				output["tunnels"] = statuses
				if err == nil && contextResponse.Data != nil {
					output["context"] = contextResponse.Data
				}
				jsonOutput, _ := json.MarshalIndent(output, "", "  ")
				fmt.Println(string(jsonOutput))
			default:
				slog.Error("unknown format")
				os.Exit(1)
			}
		},
	}
	statusCmd.Flags().StringP("format", "F", "text", "Format to use (text/json)")
	statusCmd.Flags().IntP("events", "n", 20, "Number of recent events to show")

	return statusCmd
}

// displayContextBanner shows a compact context banner at the top of status output
func displayContextBanner(data interface{}) {
	jsonData, err := json.Marshal(data)
	if err != nil {
		return
	}

	var status struct {
		Context  string            `json:"context"`
		Location string            `json:"location,omitempty"`
		Sensors  map[string]string `json:"sensors"`
	}

	if err := json.Unmarshal(jsonData, &status); err != nil {
		return
	}

	// ANSI color codes
	const (
		colorBold      = "\033[1m"
		colorBoldWhite = "\033[1m\033[37m"
		colorBoldCyan  = "\033[1m\033[36m"
		colorCyan      = "\033[36m"
		colorReset     = "\033[0m"
	)

	// Show location if available
	location := ""
	if status.Location != "" {
		location = fmt.Sprintf("%s%s%s @ ", colorBoldWhite, status.Location, colorReset)
	}

	// Display compact context info
	fmt.Printf("%s%s%s%s", location, colorBoldCyan, status.Context, colorReset)

	// Show public IP if available (prefer IPv4, fallback to IPv6)
	if ip, ok := status.Sensors["public_ipv4"]; ok && ip != "" && ip != "169.254.0.0" {
		fmt.Printf(" (IP: %s)", ip)
	} else if ip, ok := status.Sensors["public_ipv6"]; ok && ip != "" && ip != "fe80::" {
		fmt.Printf(" (IP: %s)", ip)
	}

	fmt.Println()
	fmt.Println()
}

// displayContextInfo shows context age and sensor values
func displayContextInfo(data interface{}) {
	jsonData, err := json.Marshal(data)
	if err != nil {
		return
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
		SensorChanges []struct {
			SensorName string `json:"sensor_name"`
			SensorType string `json:"sensor_type"`
			OldValue   string `json:"old_value"`
			NewValue   string `json:"new_value"`
			Timestamp  string `json:"timestamp"`
		} `json:"sensor_changes"`
		TunnelEvents []struct {
			TunnelAlias string `json:"tunnel_alias"`
			EventType   string `json:"event_type"`
			Details     string `json:"details,omitempty"`
			Timestamp   string `json:"timestamp"`
		} `json:"tunnel_events"`
	}

	if err := json.Unmarshal(jsonData, &status); err != nil {
		return
	}

	// ANSI color codes
	const (
		colorCyan    = "\033[36m"
		colorGray    = "\033[90m"
		colorReset   = "\033[0m"
		colorBold    = "\033[1m"
		colorYellow  = "\033[33m"
		colorMagenta = "\033[35m"
		colorBlue    = "\033[34m"
		colorGreen   = "\033[32m"
		colorRed     = "\033[31m"
	)

	// Display context age
	if status.Uptime != "" {
		fmt.Printf("%sContext Age:%s %s\n", colorGray, colorReset, status.Uptime)
	}

	// Display all sensors (sorted by name)
	if len(status.Sensors) > 0 {
		fmt.Printf("\n%sSensors:%s\n", colorBold, colorReset)
		sensorKeys := make([]string, 0, len(status.Sensors))
		for key := range status.Sensors {
			sensorKeys = append(sensorKeys, key)
		}
		sort.Strings(sensorKeys)
		for _, key := range sensorKeys {
			value := status.Sensors[key]
			// Format the display value
			displayValue := value
			if value == "" {
				// Environment sensors show "(empty)", others show "unknown"
				if len(key) > 4 && key[:4] == "env:" {
					displayValue = colorGray + "(empty)" + colorReset
				} else {
					displayValue = colorGray + "unknown" + colorReset
				}
			} else if value == "true" {
				displayValue = colorGreen + "true" + colorReset
			} else if value == "false" {
				displayValue = colorRed + "false" + colorReset
			}
			fmt.Printf("  %s%s:%s %s\n", colorCyan, key, colorReset, displayValue)
		}
	}

	fmt.Println()
}

// displayRecentEvents shows recent sensor changes and tunnel events
func displayRecentEvents(data interface{}) {
	jsonData, err := json.Marshal(data)
	if err != nil {
		return
	}

	var status struct {
		SensorChanges []struct {
			SensorName string `json:"sensor_name"`
			SensorType string `json:"sensor_type"`
			OldValue   string `json:"old_value"`
			NewValue   string `json:"new_value"`
			Timestamp  string `json:"timestamp"`
		} `json:"sensor_changes"`
		TunnelEvents []struct {
			TunnelAlias string `json:"tunnel_alias"`
			EventType   string `json:"event_type"`
			Details     string `json:"details,omitempty"`
			Timestamp   string `json:"timestamp"`
		} `json:"tunnel_events"`
		DaemonEvents []struct {
			EventType string `json:"event_type"`
			Details   string `json:"details,omitempty"`
			Timestamp string `json:"timestamp"`
		} `json:"daemon_events"`
	}

	if err := json.Unmarshal(jsonData, &status); err != nil {
		return
	}

	// ANSI color codes
	const (
		colorCyan    = "\033[36m"
		colorGray    = "\033[90m"
		colorReset   = "\033[0m"
		colorBold    = "\033[1m"
		colorYellow  = "\033[33m"
		colorMagenta = "\033[35m"
		colorBlue    = "\033[34m"
		colorGreen   = "\033[32m"
		colorRed     = "\033[31m"
	)

	// Collect all events into a single list for unified display
	type logEvent struct {
		timestamp time.Time
		message   string
	}
	var events []logEvent

	// Add sensor changes as individual log lines
	for _, sc := range status.SensorChanges {
		ts, err := time.Parse(time.RFC3339Nano, sc.Timestamp)
		if err != nil {
			continue
		}

		// Format old and new values
		oldValue := sc.OldValue
		newValue := sc.NewValue
		isEnvSensor := len(sc.SensorName) > 4 && sc.SensorName[:4] == "env:"

		if oldValue == "" {
			if isEnvSensor {
				oldValue = colorGray + "(empty)" + colorReset
			} else {
				oldValue = colorGray + "unknown" + colorReset
			}
		} else if oldValue == "true" {
			oldValue = colorGreen + "true" + colorReset
		} else if oldValue == "false" {
			oldValue = colorRed + "false" + colorReset
		}
		if newValue == "" {
			if isEnvSensor {
				newValue = colorGray + "(empty)" + colorReset
			} else {
				newValue = colorGray + "unknown" + colorReset
			}
		} else if newValue == "true" {
			newValue = colorGreen + "true" + colorReset
		} else if newValue == "false" {
			newValue = colorRed + "false" + colorReset
		}

		var msg string
		if sc.SensorName == "context" {
			msg = fmt.Sprintf("%scontext: %s → %s%s", colorMagenta, sc.OldValue, sc.NewValue, colorReset)
		} else if sc.SensorName == "location" {
			msg = fmt.Sprintf("%slocation: %s → %s%s", colorBlue, sc.OldValue, sc.NewValue, colorReset)
		} else {
			msg = fmt.Sprintf("%s%s:%s %s → %s", colorCyan, sc.SensorName, colorReset, oldValue, newValue)
		}

		events = append(events, logEvent{timestamp: ts, message: msg})
	}

	// Add tunnel events
	for _, te := range status.TunnelEvents {
		ts, err := time.Parse(time.RFC3339Nano, te.Timestamp)
		if err != nil {
			continue
		}

		var msg string
		eventDesc := te.EventType
		if te.Details != "" {
			eventDesc = fmt.Sprintf("%s - %s", te.EventType, te.Details)
		}
		msg = fmt.Sprintf("%s%s:%s %s", colorYellow, te.TunnelAlias, colorReset, eventDesc)

		events = append(events, logEvent{timestamp: ts, message: msg})
	}

	// Add daemon events
	for _, de := range status.DaemonEvents {
		ts, err := time.Parse(time.RFC3339Nano, de.Timestamp)
		if err != nil {
			continue
		}

		var msg string
		eventDesc := de.EventType
		if de.Details != "" {
			eventDesc = fmt.Sprintf("%s - %s", de.EventType, de.Details)
		}
		msg = fmt.Sprintf("%sdaemon:%s %s", colorGreen, colorReset, eventDesc)

		events = append(events, logEvent{timestamp: ts, message: msg})
	}

	// Sort events by timestamp (most recent first)
	sort.Slice(events, func(i, j int) bool {
		return events[i].timestamp.After(events[j].timestamp)
	})

	// Display recent events
	if len(events) > 0 {
		fmt.Printf("\n%sRecent Events:%s\n", colorBold, colorReset)
		for _, event := range events {
			// Format timestamp as HH:MM:SS
			timeStr := event.timestamp.Local().Format("2006-01-02 15:04:05")
			fmt.Printf("  %s%s%s %s\n", colorGray, timeStr, colorReset, event.message)
		}
	}

	fmt.Println()
}
