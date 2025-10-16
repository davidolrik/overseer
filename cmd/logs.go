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

	"github.com/spf13/cobra"
	"olrik.dev/davidolrik/overseer/internal/core"
	"olrik.dev/davidolrik/overseer/internal/daemon"
)

func NewLogsCommand() *cobra.Command {
	logsCmd := &cobra.Command{
		Use:     "logs",
		Aliases: []string{"log"},
		Short:   "Stream daemon logs in real-time",
		Long:    `Stream daemon logs in real-time.

Press Ctrl+C to exit. By default, only shows INFO level and above. Use -v to see DEBUG logs.`,
		Args:    cobra.NoArgs,
		Run: func(cmd *cobra.Command, args []string) {
			// Check if daemon is running
			if _, err := daemon.SendCommand("STATUS"); err != nil {
				slog.Error("Daemon is not running. Start a tunnel first.")
				os.Exit(1)
			}

			// Get verbose flag
			verbose, _ := cmd.Flags().GetBool("verbose")

			// Connect to daemon
			conn, err := net.Dial("unix", core.GetSocketPath())
			if err != nil {
				slog.Error(fmt.Sprintf("Failed to connect to daemon: %v", err))
				os.Exit(1)
			}
			defer conn.Close()

			// Send LOGS command
			if _, err := conn.Write([]byte("LOGS\n")); err != nil {
				slog.Error(fmt.Sprintf("Failed to send LOGS command: %v", err))
				os.Exit(1)
			}

			// Set up signal handler for Ctrl+C
			sigChan := make(chan os.Signal, 1)
			signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)

			// Channel to signal when reading is done
			done := make(chan bool)

			// Start reading logs in a goroutine
			go func() {
				reader := bufio.NewReader(conn)
				for {
					line, err := reader.ReadString('\n')
					if err != nil {
						if err != io.EOF {
							slog.Error(fmt.Sprintf("Error reading logs: %v", err))
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
				fmt.Println("\nDisconnected from daemon logs.")
			case <-done:
				fmt.Println("Connection to daemon closed.")
			}
		},
	}

	logsCmd.Flags().BoolP("verbose", "v", false, "Show DEBUG level logs")

	return logsCmd
}
