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
	"overseer.olrik.dev/internal/core"
	"overseer.olrik.dev/internal/daemon"
)

func NewLogsCommand() *cobra.Command {
	logsCmd := &cobra.Command{
		Use:     "logs",
		Aliases: []string{"log"},
		Short:   "Stream daemon logs in real-time",
		Long:    `Stream daemon logs in real-time.

Press Ctrl+C to exit. By default, only shows INFO level and above. Use -v to see DEBUG logs.
Automatically reconnects if the daemon is reloaded.`,
		Args: cobra.NoArgs,
		Run: func(cmd *cobra.Command, args []string) {
			// Check if daemon is running
			if _, err := daemon.SendCommand("STATUS"); err != nil {
				slog.Error("Daemon is not running. Start a tunnel first.")
				os.Exit(1)
			}

			// Get verbose flag
			verbose, _ := cmd.Flags().GetBool("verbose")

			// Set up signal handler for Ctrl+C
			sigChan := make(chan os.Signal, 1)
			signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)

			// Reconnect loop
			for {
				// Connect to daemon
				conn, err := net.Dial("unix", core.GetSocketPath())
				if err != nil {
					slog.Error(fmt.Sprintf("Failed to connect to daemon: %v", err))
					os.Exit(1)
				}

				// Send LOGS command
				if _, err := conn.Write([]byte("LOGS\n")); err != nil {
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
						// Log format: timestamp [LEVEL] message
						// If not verbose, skip DEBUG logs
						if !verbose && strings.Contains(line, "DBG") {
							continue
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
					// Continue loop to reconnect
				}
			}
		},
	}

	logsCmd.Flags().BoolP("verbose", "v", false, "Show DEBUG level logs")

	return logsCmd
}
