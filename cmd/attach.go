package cmd

import (
	"bufio"
	"fmt"
	"io"
	"log/slog"
	"net"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/spf13/cobra"
	"overseer.olrik.dev/internal/core"
	"overseer.olrik.dev/internal/daemon"
)

func NewAttachCommand() *cobra.Command {
	attachCmd := &cobra.Command{
		Use:   "attach",
		Short: "Attach to the daemon's log output",
		Long: `Attach to the daemon and stream its log output in real-time.

This shows the same output you would see if running the daemon manually
in the foreground. Useful for debugging SSH connections, sensor issues,
and other daemon-level problems.

Press Ctrl+C to detach.

For filtered event logs (sensors, state changes, etc.), use 'overseer logs' instead.`,
		Args: cobra.NoArgs,
		Run: func(cmd *cobra.Command, args []string) {
			// Check if daemon is running
			if _, err := daemon.SendCommand("STATUS"); err != nil {
				slog.Error("Daemon is not running. Start a tunnel first.")
				os.Exit(1)
			}

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

				// Send ATTACH command
				if _, err := conn.Write([]byte("ATTACH\n")); err != nil {
					conn.Close()
					slog.Error(fmt.Sprintf("Failed to send ATTACH command: %v", err))
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
						fmt.Print(line)
					}
				}()

				// Wait for either Ctrl+C or connection close
				select {
				case <-sigChan:
					conn.Close()
					fmt.Println("\nDetached from daemon.")
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

	return attachCmd
}
