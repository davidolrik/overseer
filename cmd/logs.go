package cmd

import (
	"bufio"
	"fmt"
	"io"
	"log/slog"
	"net"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/spf13/cobra"
	"go.olrik.dev/overseer/internal/core"
	"go.olrik.dev/overseer/internal/daemon"
)

func NewLogsCommand() *cobra.Command {
	var lines int

	logsCmd := &cobra.Command{
		Use:     "logs",
		Aliases: []string{"log"},
		Short:   "Stream daemon logs in real-time",
		Long: `Stream daemon logs in real-time.

Press Ctrl+C to exit. By default, only shows INFO level and above.

Filter categories:
  sensor  - Sensor readings (TCP, IPv4, IPv6 probes)
  state   - State transitions (context, location, online changes)
  effect  - Side effects (env file writes, callbacks)
  system  - System events (daemon start/stop, config reload)
  tunnel  - Tunnel-related events

Examples:
  overseer logs            # Stream INFO and above
  overseer logs -v         # Include DEBUG logs
  overseer logs -F sensor  # Filter to sensor readings
  overseer logs -F state   # Filter to state changes
  overseer logs -F effect  # Filter to env file writes
  overseer logs -F online  # Filter by keyword
  overseer logs -L 50      # Show 50 history lines on connect

Automatically reconnects if the daemon is reloaded.`,
		Args: cobra.NoArgs,
		Run: func(cmd *cobra.Command, args []string) {
			// Check if daemon is running
			if _, err := daemon.SendCommand("STATUS"); err != nil {
				slog.Error("Daemon is not running. Use 'overseer start' to start it.")
				os.Exit(1)
			}

			// Get flags
			verbose, _ := cmd.Flags().GetBool("verbose")
			filter, _ := cmd.Flags().GetString("filter")
			noColor, _ := cmd.Flags().GetBool("no-color")

			// Set up signal handler for Ctrl+C
			sigChan := make(chan os.Signal, 1)
			signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)

			// Track reconnection state to suppress history on reconnect
			isReconnect := false

			// Reconnect loop
			for {
				// Connect to daemon
				conn, err := net.Dial("unix", core.GetSocketPath())
				if err != nil {
					slog.Error(fmt.Sprintf("Failed to connect to daemon: %v", err))
					os.Exit(1)
				}

				// Build LOGS command with optional lines count and no_history flag
				logsCmd := fmt.Sprintf("LOGS %d", lines)
				if isReconnect {
					logsCmd += " no_history"
				}
				logsCmd += "\n"

				// Send LOGS command
				if _, err := conn.Write([]byte(logsCmd)); err != nil {
					conn.Close()
					slog.Error(fmt.Sprintf("Failed to send LOGS command: %v", err))
					os.Exit(1)
				}

				// Channel to signal when reading is done
				done := make(chan bool)

				// Start reading logs in a goroutine
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

						// Filter logs based on verbose flag
						// Skip DEBUG logs if not verbose
						// Check both plain "DBG" and ANSI-colored version
						if !verbose && isDebugLog(line) {
							continue
						}

						// Apply filter if specified
						if filter != "" && !matchesFilter(line, filter) {
							continue
						}

						// Strip color codes if --no-color
						if noColor {
							line = stripANSI(line)
						}

						fmt.Print(line)
					}
				}()

				// Wait for either Ctrl+C or connection close
				select {
				case <-sigChan:
					conn.Close()
					fmt.Println("\nDisconnected from daemon logs.")
					return
				case <-done:
					conn.Close()
					// Try to reconnect after a short delay
					fmt.Println("Connection lost. Reconnecting...")
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
						fmt.Println("Daemon not available. Exiting.")
						return
					}
					// Mark as reconnect to suppress history on next connection
					isReconnect = true
					// Continue loop to reconnect
				}
			}
		},
	}

	logsCmd.Flags().BoolP("verbose", "v", false, "Show DEBUG level logs")
	logsCmd.Flags().StringP("filter", "F", "", "Filter logs by keyword (e.g., sensor, state, tunnel, context)")
	logsCmd.Flags().Bool("no-color", false, "Disable colored output")
	logsCmd.Flags().IntVarP(&lines, "lines", "L", 20, "Number of history lines to show on connect")

	return logsCmd
}

// isDebugLog checks if a log line is a DEBUG level log
func isDebugLog(line string) bool {
	// Check for plain DBG
	if strings.Contains(line, " DBG ") || strings.Contains(line, "\tDBG\t") {
		return true
	}
	// Check for ANSI-colored DBG (gray color: \033[90mDBG\033[0m)
	if strings.Contains(line, "\033[90mDBG\033[0m") {
		return true
	}
	// Strip ANSI and check again
	stripped := stripANSI(line)
	return strings.Contains(stripped, " DBG ") || strings.Contains(stripped, "\tDBG\t")
}

// matchesFilter checks if a log line matches the filter criteria
func matchesFilter(line, filter string) bool {
	filter = strings.ToLower(filter)
	lineLower := strings.ToLower(line)

	// Check for exact category matches
	switch filter {
	case "sensor":
		// Match sensor category icon (~) or sensor-related keywords
		return strings.Contains(line, " ~ ") ||
			strings.Contains(lineLower, "sensor") ||
			strings.Contains(lineLower, "tcp") ||
			strings.Contains(lineLower, "public_ip") ||
			strings.Contains(lineLower, "ipv4") ||
			strings.Contains(lineLower, "ipv6")
	case "state":
		// Match state category icon (*) or state-related keywords
		return strings.Contains(line, " * ") ||
			strings.Contains(lineLower, "context") ||
			strings.Contains(lineLower, "location") ||
			strings.Contains(lineLower, "online")
	case "effect":
		// Match effect category icon (>) or effect-related keywords
		return strings.Contains(line, " > ") ||
			strings.Contains(lineLower, "effect") ||
			strings.Contains(lineLower, "env_write") ||
			strings.Contains(lineLower, "dotenv")
	case "system":
		// Match system category icon (#) or system-related keywords
		return strings.Contains(line, " # ") ||
			strings.Contains(lineLower, "orchestrator") ||
			strings.Contains(lineLower, "daemon")
	case "tunnel":
		return strings.Contains(lineLower, "tunnel") ||
			strings.Contains(lineLower, "ssh")
	default:
		// General substring match
		return strings.Contains(lineLower, filter)
	}
}

// stripANSI removes ANSI escape codes from a string
func stripANSI(s string) string {
	var result strings.Builder
	inEscape := false

	for i := 0; i < len(s); i++ {
		if s[i] == '\033' {
			inEscape = true
			continue
		}
		if inEscape {
			if s[i] == 'm' {
				inEscape = false
			}
			continue
		}
		result.WriteByte(s[i])
	}

	return result.String()
}
