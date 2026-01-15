package cmd

import (
	"bufio"
	"fmt"
	"io"
	"log/slog"
	"net"
	"os"
	"os/signal"
	"sort"
	"strings"
	"syscall"
	"time"

	"github.com/spf13/cobra"
	"overseer.olrik.dev/internal/core"
	"overseer.olrik.dev/internal/daemon"
)

// formatDaemonMessage formats a message with timestamp and [DAEMON] prefix
func formatDaemonMessage(msg string) string {
	timestamp := time.Now().Format("2006-01-02 15:04:05")
	return colorGray + timestamp + colorReset + " " + colorMagenta + "[DAEMON]" + colorReset + " " + msg + "\n"
}

// colorizeCompanionOutput adds colors to companion output
// Expected format: "2024-01-12 15:04:05 [stream] message"
func colorizeCompanionOutput(line string) string {
	trimmed := strings.TrimSpace(line)

	// Skip empty lines
	if trimmed == "" {
		return ""
	}

	// Detect ^C (Ctrl+C echo from PTY) and replace with cleaner message
	if strings.Contains(line, "^C") || strings.Contains(line, "\x03") {
		timestamp := time.Now().Format("2006-01-02 15:04:05")
		return colorGray + timestamp + colorReset + " " + colorMagenta + "[DAEMON]" + colorReset + " Sending interrupt signal...\n"
	}

	// Format daemon/client messages with timestamp and [DAEMON] prefix
	if strings.HasPrefix(trimmed, "Companion process terminated") ||
		strings.HasPrefix(trimmed, "Attached to companion") ||
		strings.HasPrefix(trimmed, "Connection lost") ||
		strings.HasPrefix(trimmed, "Reconnecting") {
		timestamp := time.Now().Format("2006-01-02 15:04:05")
		return colorGray + timestamp + colorReset + " " + colorMagenta + "[DAEMON]" + colorReset + " " + trimmed + "\n"
	}

	// Check if line has the expected format with timestamp
	// Format: "2024-01-12 15:04:05 [stream] message" (timestamp is 19 chars)
	if len(line) >= 29 && line[19] == ' ' && line[20] == '[' {
		timestamp := line[:19]
		rest := line[20:] // "[stream] message"

		var coloredPrefix string
		var message string

		switch {
		case strings.HasPrefix(rest, "[stdout]"):
			coloredPrefix = colorGreen + "[stdout]" + colorReset
			message = rest[8:]
		case strings.HasPrefix(rest, "[stderr]"):
			coloredPrefix = colorYellow + "[stderr]" + colorReset
			message = rest[8:]
		case strings.HasPrefix(rest, "[output]"):
			coloredPrefix = colorCyan + "[output]" + colorReset
			message = rest[8:]
		case strings.HasPrefix(rest, "[DAEMON]"):
			coloredPrefix = colorMagenta + "[DAEMON]" + colorReset
			message = rest[8:]
		default:
			return colorGray + timestamp + colorReset + " " + rest
		}

		return colorGray + timestamp + colorReset + " " + coloredPrefix + message
	}

	// Fallback for lines without timestamp (legacy format or other messages)
	switch {
	case strings.HasPrefix(line, "[stdout]"):
		return colorGreen + "[stdout]" + colorReset + line[8:]
	case strings.HasPrefix(line, "[stderr]"):
		return colorYellow + "[stderr]" + colorReset + line[8:]
	case strings.HasPrefix(line, "[output]"):
		return colorCyan + "[output]" + colorReset + line[8:]
	case strings.HasPrefix(line, "[DAEMON]"):
		return colorMagenta + "[DAEMON]" + colorReset + line[8:]
	default:
		return line
	}
}

func NewCompanionCommand() *cobra.Command {
	companionCmd := &cobra.Command{
		Use:   "companion",
		Short: "Manage companion scripts",
		Long:  `Manage companion scripts that run alongside tunnels.`,
	}

	companionCmd.AddCommand(
		newCompanionListCommand(),
		newCompanionAttachCommand(),
		newCompanionStartCommand(),
		newCompanionStopCommand(),
		newCompanionRestartCommand(),
		newCompanionStatusCommand(),
	)

	return companionCmd
}

func newCompanionListCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "list",
		Short: "List companions for a tunnel or all tunnels",
		Long:  `List all companions (running and dormant). If --tunnel is specified, shows only that tunnel's companions.`,
		Args:  cobra.NoArgs,
		Run: func(cmd *cobra.Command, args []string) {
			tunnel, _ := cmd.Flags().GetString("tunnel")

			// Get running companions from daemon
			allRunningCompanions := make(map[string]map[string]companionInfo)
			if response, err := daemon.SendCommand("COMPANION_STATUS"); err == nil {
				companionMap := getCompanionMap(response)
				for t, comps := range companionMap {
					allRunningCompanions[t] = make(map[string]companionInfo)
					for _, comp := range comps {
						allRunningCompanions[t][comp.Name] = comp
					}
				}
			}

			// Determine which tunnels to show
			var tunnelsToShow []string
			if tunnel != "" {
				// Specific tunnel requested
				if _, exists := core.Config.Tunnels[tunnel]; !exists {
					slog.Error(fmt.Sprintf("Tunnel '%s' not found in configuration", tunnel))
					os.Exit(1)
				}
				tunnelsToShow = []string{tunnel}
			} else {
				// Show all tunnels that have companions configured
				for alias, cfg := range core.Config.Tunnels {
					if len(cfg.Companions) > 0 {
						tunnelsToShow = append(tunnelsToShow, alias)
					}
				}
				sort.Strings(tunnelsToShow)
			}

			if len(tunnelsToShow) == 0 {
				fmt.Println("No companions configured.")
				return
			}

			for i, t := range tunnelsToShow {
				tunnelConfig := core.Config.Tunnels[t]
				runningCompanions := allRunningCompanions[t]
				if runningCompanions == nil {
					runningCompanions = make(map[string]companionInfo)
				}

				fmt.Printf("Tunnel '%s':\n", t)

				// Sort companion names for consistent output
				names := make([]string, 0, len(tunnelConfig.Companions))
				for _, comp := range tunnelConfig.Companions {
					names = append(names, comp.Name)
				}
				sort.Strings(names)

				for _, name := range names {
					if running, ok := runningCompanions[name]; ok {
						// Running companion
						var color, icon string
						switch running.State {
						case "running", "ready":
							color = colorGreen
							icon = "✓"
						case "waiting", "starting":
							color = colorYellow
							icon = "⟳"
						case "failed":
							color = colorRed
							icon = "✗"
						default:
							color = colorGray
							icon = "○"
						}
						fmt.Printf("  %s%s%s %s %s[%s]%s (PID: %d)\n",
							color, icon, colorReset,
							name,
							color, running.State, colorReset,
							running.PID)
					} else {
						// Dormant companion
						fmt.Printf("  %s○%s %s %s[dormant]%s\n",
							colorGray, colorReset,
							name,
							colorGray, colorReset)
					}
				}

				// Add blank line between tunnels (but not after the last one)
				if i < len(tunnelsToShow)-1 {
					fmt.Println()
				}
			}
		},
	}

	cmd.Flags().StringP("tunnel", "T", "", "Tunnel alias (optional, shows all if not specified)")
	cmd.RegisterFlagCompletionFunc("tunnel", tunnelCompletionFunc)

	return cmd
}

func newCompanionAttachCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "attach",
		Short: "Attach to a companion script's output",
		Long: `Stream the stdout/stderr of a running companion script.

Press Ctrl+C to detach.`,
		Args: cobra.NoArgs,
		Run: func(cmd *cobra.Command, args []string) {
			tunnel, _ := cmd.Flags().GetString("tunnel")
			name, _ := cmd.Flags().GetString("name")
			lines, _ := cmd.Flags().GetInt("lines")

			daemon.EnsureDaemonIsRunning()

			// Set up signal handler for Ctrl+C
			sigChan := make(chan os.Signal, 1)
			signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)

			// Reconnect loop
			isReconnect := false
			for {
				// Connect to daemon
				conn, err := net.Dial("unix", core.GetSocketPath())
				if err != nil {
					slog.Error(fmt.Sprintf("Failed to connect to daemon: %v", err))
					os.Exit(1)
				}

				// Send COMPANION_ATTACH command with lines count (0 + no_history on reconnect)
				var command string
				if isReconnect {
					command = fmt.Sprintf("COMPANION_ATTACH %s %s 0 no_history\n", tunnel, name)
				} else {
					command = fmt.Sprintf("COMPANION_ATTACH %s %s %d\n", tunnel, name, lines)
				}
				if _, err := conn.Write([]byte(command)); err != nil {
					conn.Close()
					slog.Error(fmt.Sprintf("Failed to send command: %v", err))
					os.Exit(1)
				}
				isReconnect = true // Next iteration is a reconnect

				// Channel to signal when reading is done
				done := make(chan bool)
				var lastMessage string

				// Start reading output in a goroutine
				go func() {
					reader := bufio.NewReader(conn)
					for {
						line, err := reader.ReadString('\n')
						if err != nil {
							if err != io.EOF {
								// Don't log error on normal disconnect
							}
							done <- true
							return
						}
						lastMessage = line
						if colored := colorizeCompanionOutput(line); colored != "" {
							fmt.Print(colored)
						}
					}
				}()

				// Wait for either Ctrl+C or connection close
				select {
				case <-sigChan:
					conn.Close()
					fmt.Print(formatDaemonMessage("Detached from companion."))
					return
				case <-done:
					conn.Close()

					// Check if companion/tunnel not found error
					if strings.Contains(lastMessage, "not found") {
						// Query tunnel status to determine if we should retry
						response, err := daemon.SendCommand("STATUS")
						if err != nil {
							fmt.Print(formatDaemonMessage("Daemon not available. Exiting."))
							return
						}

						// Check if tunnel exists and is reconnecting
						tunnelExists, isReconnecting := checkTunnelReconnecting(response, tunnel)
						if !tunnelExists {
							fmt.Print(formatDaemonMessage("Tunnel was closed. Exiting."))
							return
						}
						if !isReconnecting {
							// Tunnel exists but not reconnecting (connected state = config issue)
							fmt.Print(formatDaemonMessage("Tunnel is connected but companion not found. Exiting."))
							return
						}
						// Tunnel is reconnecting - continue to retry
						fmt.Print(formatDaemonMessage("Tunnel reconnecting. Waiting..."))
					} else {
						fmt.Print(formatDaemonMessage("Connection lost. Reconnecting..."))
					}

					time.Sleep(500 * time.Millisecond)

					// Wait for daemon to be available again (up to 5 seconds)
					reconnected := false
					for i := 0; i < 10; i++ {
						if _, err := daemon.SendCommand("STATUS"); err == nil {
							reconnected = true
							break
						}
						time.Sleep(500 * time.Millisecond)
					}

					if !reconnected {
						fmt.Print(formatDaemonMessage("Daemon not available. Exiting."))
						return
					}
					// Continue loop to reconnect
				}
			}
		},
	}

	cmd.Flags().StringP("tunnel", "T", "", "Tunnel alias")
	cmd.Flags().StringP("name", "N", "", "Companion name")
	cmd.Flags().IntP("lines", "L", 20, "Number of history lines to show on attach")
	cmd.MarkFlagRequired("tunnel")
	cmd.MarkFlagRequired("name")
	cmd.RegisterFlagCompletionFunc("tunnel", tunnelCompletionFunc)
	cmd.RegisterFlagCompletionFunc("name", companionCompletionFunc)

	return cmd
}

func newCompanionStartCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "start",
		Short: "Start a specific companion script",
		Long: `Start a specific companion script for a running tunnel.

This is useful for starting a dormant companion or restarting a failed one.`,
		Args: cobra.NoArgs,
		Run: func(cmd *cobra.Command, args []string) {
			tunnel, _ := cmd.Flags().GetString("tunnel")
			name, _ := cmd.Flags().GetString("name")

			daemon.EnsureDaemonIsRunning()
			daemon.CheckVersionMismatch()

			command := fmt.Sprintf("COMPANION_START %s %s", tunnel, name)
			response, err := daemon.SendCommand(command)
			if err != nil {
				slog.Error(err.Error())
				os.Exit(1)
			}
			response.LogMessages()
		},
	}

	cmd.Flags().StringP("tunnel", "T", "", "Tunnel alias")
	cmd.Flags().StringP("name", "N", "", "Companion name")
	cmd.MarkFlagRequired("tunnel")
	cmd.MarkFlagRequired("name")
	cmd.RegisterFlagCompletionFunc("tunnel", tunnelCompletionFunc)
	cmd.RegisterFlagCompletionFunc("name", companionCompletionFunc)

	return cmd
}

func newCompanionStopCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "stop",
		Short: "Stop a specific companion script",
		Long:  `Stop a specific companion script without affecting the tunnel.`,
		Args:  cobra.NoArgs,
		Run: func(cmd *cobra.Command, args []string) {
			tunnel, _ := cmd.Flags().GetString("tunnel")
			name, _ := cmd.Flags().GetString("name")

			daemon.EnsureDaemonIsRunning()
			daemon.CheckVersionMismatch()

			command := fmt.Sprintf("COMPANION_STOP %s %s", tunnel, name)
			response, err := daemon.SendCommand(command)
			if err != nil {
				slog.Error(err.Error())
				os.Exit(1)
			}
			response.LogMessages()
		},
	}

	cmd.Flags().StringP("tunnel", "T", "", "Tunnel alias")
	cmd.Flags().StringP("name", "N", "", "Companion name")
	cmd.MarkFlagRequired("tunnel")
	cmd.MarkFlagRequired("name")
	cmd.RegisterFlagCompletionFunc("tunnel", tunnelCompletionFunc)
	cmd.RegisterFlagCompletionFunc("name", companionCompletionFunc)

	return cmd
}

func newCompanionRestartCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "restart",
		Short: "Restart a specific companion script",
		Long:  `Stop and start a specific companion script.`,
		Args:  cobra.NoArgs,
		Run: func(cmd *cobra.Command, args []string) {
			tunnel, _ := cmd.Flags().GetString("tunnel")
			name, _ := cmd.Flags().GetString("name")

			daemon.EnsureDaemonIsRunning()
			daemon.CheckVersionMismatch()

			command := fmt.Sprintf("COMPANION_RESTART %s %s", tunnel, name)
			response, err := daemon.SendCommand(command)
			if err != nil {
				slog.Error(err.Error())
				os.Exit(1)
			}
			response.LogMessages()
		},
	}

	cmd.Flags().StringP("tunnel", "T", "", "Tunnel alias")
	cmd.Flags().StringP("name", "N", "", "Companion name")
	cmd.MarkFlagRequired("tunnel")
	cmd.MarkFlagRequired("name")
	cmd.RegisterFlagCompletionFunc("tunnel", tunnelCompletionFunc)
	cmd.RegisterFlagCompletionFunc("name", companionCompletionFunc)

	return cmd
}

func newCompanionStatusCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "status",
		Short: "Show status of companion scripts for a tunnel",
		Long:  `Display the status of running companion scripts for a specific tunnel.`,
		Args:  cobra.NoArgs,
		Run: func(cmd *cobra.Command, args []string) {
			tunnel, _ := cmd.Flags().GetString("tunnel")

			daemon.EnsureDaemonIsRunning()
			daemon.CheckVersionMismatch()

			response, err := daemon.SendCommand("COMPANION_STATUS")
			if err != nil {
				slog.Error(err.Error())
				os.Exit(1)
			}

			// Check if there are any companions
			if response.Data == nil {
				fmt.Printf("No companion scripts running for tunnel '%s'.\n", tunnel)
				return
			}

			dataMap, ok := response.Data.(map[string]interface{})
			if !ok {
				fmt.Printf("No companion scripts running for tunnel '%s'.\n", tunnel)
				return
			}

			companions, ok := dataMap["companions"]
			if !ok {
				fmt.Printf("No companion scripts running for tunnel '%s'.\n", tunnel)
				return
			}

			companionMap, ok := companions.(map[string]interface{})
			if !ok || len(companionMap) == 0 {
				fmt.Printf("No companion scripts running for tunnel '%s'.\n", tunnel)
				return
			}

			// Get companions for the specified tunnel
			comps, ok := companionMap[tunnel]
			if !ok {
				fmt.Printf("No companion scripts running for tunnel '%s'.\n", tunnel)
				return
			}

			compList, ok := comps.([]interface{})
			if !ok || len(compList) == 0 {
				fmt.Printf("No companion scripts running for tunnel '%s'.\n", tunnel)
				return
			}

			fmt.Printf("Companion status for tunnel '%s':\n", tunnel)
			for _, c := range compList {
				comp, ok := c.(map[string]interface{})
				if !ok {
					continue
				}
				name := comp["name"]
				state := comp["state"]
				pid := comp["pid"]
				command := comp["command"]

				fmt.Printf("  %s:\n", name)
				fmt.Printf("    State:   %v\n", state)
				fmt.Printf("    PID:     %v\n", pid)
				fmt.Printf("    Command: %v\n", command)

				if exitCode, ok := comp["exit_code"]; ok {
					fmt.Printf("    Exit:    %v\n", exitCode)
				}
				if exitErr, ok := comp["exit_error"]; ok && exitErr != "" {
					fmt.Printf("    Error:   %v\n", exitErr)
				}
			}
		},
	}

	cmd.Flags().StringP("tunnel", "T", "", "Tunnel alias")
	cmd.MarkFlagRequired("tunnel")
	cmd.RegisterFlagCompletionFunc("tunnel", tunnelCompletionFunc)

	return cmd
}

// Note: companionInfo and getCompanionMap are defined in status.go

// checkTunnelReconnecting checks if the tunnel exists and is in a reconnecting state
func checkTunnelReconnecting(response daemon.Response, tunnelAlias string) (exists bool, reconnecting bool) {
	if response.Data == nil {
		return false, false
	}
	dataMap, ok := response.Data.(map[string]interface{})
	if !ok {
		return false, false
	}
	tunnels, ok := dataMap["tunnels"].(map[string]interface{})
	if !ok {
		return false, false
	}
	tunnelData, ok := tunnels[tunnelAlias].(map[string]interface{})
	if !ok {
		return false, false // Tunnel not found = permanently closed
	}

	state, _ := tunnelData["state"].(string)
	autoReconnect, _ := tunnelData["auto_reconnect"].(bool)

	// Tunnel is reconnecting if: state is "reconnecting" OR (state is "disconnected" and auto_reconnect is true)
	isReconnecting := state == "reconnecting" || (state == "disconnected" && autoReconnect)
	return true, isReconnecting
}
