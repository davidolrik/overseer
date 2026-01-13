package cmd

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"sort"
	"strings"
	"time"

	"github.com/spf13/cobra"
	"overseer.olrik.dev/internal/daemon"
)

func NewStatusCommand() *cobra.Command {
	statusCmd := &cobra.Command{
		Use:     "status",
		Aliases: []string{"s", "st", "list", "ls", "context", "ctx"},
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

			// Get companion status for tree display
			companionResponse, _ := daemon.SendCommand("COMPANION_STATUS")
			companionMap := getCompanionMap(companionResponse)

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
					// ANSI color codes
					const (
						colorGreen  = "\033[32m"
						colorYellow = "\033[33m"
						colorRed    = "\033[31m"
						colorGray   = "\033[90m"
						colorReset  = "\033[0m"
					)

					// Build state indicator with colored icon and alias
					var icon, color, extraInfo, timeInfo string
					switch status.State {
					case "connecting":
						icon = "⟳"
						color = colorYellow
						startTime, _ := time.Parse(time.RFC3339, status.StartDate)
						elapsed := time.Since(startTime)
						timeInfo = fmt.Sprintf("Connecting: %s", elapsed.Round(time.Second).String())
					case "connected":
						icon = "✓"
						color = colorGreen
						// Use LastConnectedTime for age (resets to 0 on reconnection)
						lastConnected, _ := time.Parse(time.RFC3339, status.LastConnectedTime)
						age := time.Since(lastConnected)
						timeInfo = fmt.Sprintf("Age: %s", age.Round(time.Second).String())
					case "disconnected":
						icon = "✗"
						color = colorRed
						// Show how long it's been disconnected
						if status.DisconnectedTime != "" {
							disconnectedAt, _ := time.Parse(time.RFC3339, status.DisconnectedTime)
							disconnectedFor := time.Since(disconnectedAt)
							timeInfo = fmt.Sprintf("Disconnected: %s ago", disconnectedFor.Round(time.Second).String())
						} else {
							timeInfo = "Disconnected"
						}
					case "reconnecting":
						icon = "⟳"
						color = colorYellow
						// Show how long it's been disconnected
						if status.DisconnectedTime != "" {
							disconnectedAt, _ := time.Parse(time.RFC3339, status.DisconnectedTime)
							disconnectedFor := time.Since(disconnectedAt)
							timeInfo = fmt.Sprintf("Disconnected: %s ago", disconnectedFor.Round(time.Second).String())
						} else {
							timeInfo = "Reconnecting"
						}
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
						"  %s%s%s %s%s%s (PID: %d, %s%s)%s\n",
						color, icon, colorReset,
						color, status.Hostname, colorReset,
						status.Pid, timeInfo,
						reconnectInfo,
						extraInfo,
					)

					// Show companions for this tunnel in tree format
					if companions, ok := companionMap[status.Hostname]; ok && len(companions) > 0 {
						for i, comp := range companions {
							// Tree connector: └── for last item, ├── for others
							connector := "├──"
							if i == len(companions)-1 {
								connector = "└──"
							}

							// Choose color based on state
							var compColor, compIcon string
							switch comp.State {
							case "running", "ready":
								compColor = colorGreen
								compIcon = "✓"
							case "waiting", "starting":
								compColor = colorYellow
								compIcon = "⟳"
							case "stopped", "exited":
								compColor = colorGray
								compIcon = "○"
							case "failed":
								compColor = colorRed
								compIcon = "✗"
							default:
								compColor = colorReset
								compIcon = "?"
							}

							fmt.Printf("      %s %s%s%s %s %s[%s]%s\n",
								connector,
								compColor, compIcon, colorReset,
								comp.Name,
								compColor, comp.State, colorReset)
						}
					}
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
	statusCmd.Flags().IntP("events", "N", 20, "Number of recent events to show")

	return statusCmd
}

// companionInfo holds parsed companion information for display
type companionInfo struct {
	Name  string
	State string
	PID   int
}

// getCompanionMap parses companion response into a map of tunnel -> companions
func getCompanionMap(response daemon.Response) map[string][]companionInfo {
	result := make(map[string][]companionInfo)

	if response.Data == nil {
		return result
	}

	dataMap, ok := response.Data.(map[string]interface{})
	if !ok {
		return result
	}

	companions, ok := dataMap["companions"]
	if !ok {
		return result
	}

	companionMap, ok := companions.(map[string]interface{})
	if !ok {
		return result
	}

	for tunnel, comps := range companionMap {
		compList, ok := comps.([]interface{})
		if !ok {
			continue
		}

		for _, c := range compList {
			comp, ok := c.(map[string]interface{})
			if !ok {
				continue
			}

			name, _ := comp["name"].(string)
			state, _ := comp["state"].(string)
			pid, _ := comp["pid"].(float64) // JSON numbers are float64

			result[tunnel] = append(result[tunnel], companionInfo{
				Name:  name,
				State: state,
				PID:   int(pid),
			})
		}
	}

	return result
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

	// Show IPs if available
	var ips []string

	// Local IP
	if ip, ok := status.Sensors["local_ipv4"]; ok && ip != "" {
		ips = append(ips, fmt.Sprintf("LAN: %s", ip))
	}

	// Public IP (prefer IPv4, fallback to IPv6)
	if ip, ok := status.Sensors["public_ipv4"]; ok && ip != "" && ip != "169.254.0.0" {
		ips = append(ips, fmt.Sprintf("WAN: %s", ip))
	} else if ip, ok := status.Sensors["public_ipv6"]; ok && ip != "" && ip != "fe80::" {
		ips = append(ips, fmt.Sprintf("WAN: %s", ip))
	}

	if len(ips) > 0 {
		fmt.Printf(" (%s)", strings.Join(ips, ", "))
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
		colorOrange  = "\033[38;2;255;165;0m" // True 24-bit orange
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

		// Use orange for companion events, yellow for tunnel events
		aliasColor := colorYellow
		if strings.HasPrefix(te.EventType, "companion_") {
			aliasColor = colorOrange
		}
		msg = fmt.Sprintf("%s%s:%s %s", aliasColor, te.TunnelAlias, colorReset, eventDesc)

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

