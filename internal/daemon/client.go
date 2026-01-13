package daemon

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net"
	"os"
	"os/exec"
	"sync"
	"time"

	"overseer.olrik.dev/internal/core"
)

var (
	versionCheckOnce sync.Once
	versionWarned    bool
)

// SendCommand connects to the daemon, sends a command, and returns the response.
func SendCommand(command string) (Response, error) {
	response := Response{}

	conn, err := net.Dial("unix", core.GetSocketPath())
	if err != nil {
		return response, err
	}
	defer conn.Close()

	if _, err := conn.Write([]byte(command + "\n")); err != nil {
		return response, fmt.Errorf("failed to send command to daemon: %w", err)
	}
	bytes, err := io.ReadAll(conn)
	if err != nil {
		return response, fmt.Errorf("failed to read response from daemon: %w", err)
	}

	if err := json.Unmarshal(bytes, &response); err != nil {
		return response, fmt.Errorf("failed to parse response from daemon: %w", err)
	}

	return response, nil
}

// SendCommandStreaming connects to the daemon, sends a command, and streams response messages.
// Each message is logged as it arrives, allowing real-time progress feedback.
// Returns an error if the connection or command fails.
func SendCommandStreaming(command string) error {
	conn, err := net.Dial("unix", core.GetSocketPath())
	if err != nil {
		return err
	}
	defer conn.Close()

	if _, err := conn.Write([]byte(command + "\n")); err != nil {
		return fmt.Errorf("failed to send command to daemon: %w", err)
	}

	// Read response line by line - each line is a JSON message
	reader := bufio.NewReader(conn)
	for {
		line, err := reader.ReadBytes('\n')
		if err != nil {
			if err == io.EOF {
				return nil // Normal end of stream
			}
			return fmt.Errorf("failed to read response from daemon: %w", err)
		}

		// Skip empty lines
		if len(line) <= 1 {
			continue
		}

		// Parse and log the message
		var msg ResponseMessage
		if err := json.Unmarshal(line, &msg); err != nil {
			// If it's not a valid ResponseMessage, might be the final empty response
			continue
		}

		// Log the message based on status
		switch msg.Status {
		case "INFO":
			slog.Info(msg.Message)
		case "WARN":
			slog.Warn(msg.Message)
		case "ERROR":
			slog.Error(msg.Message)
		default:
			slog.Info(msg.Message)
		}
	}
}

// EnsureDaemonIsRunning handles the auto-start logic.
func EnsureDaemonIsRunning() {
	if _, err := SendCommand("STATUS"); err == nil {
		return // Daemon is running
	}

	slog.Info("Daemon not running. Starting it now...")
	cmd := exec.Command(os.Args[0], "daemon")
	if err := cmd.Start(); err != nil {
		slog.Error(fmt.Sprintf("Fatal: Could not fork daemon process: %v", err))
		os.Exit(1)
	}
	slog.Info(fmt.Sprintf("Daemon process launched with PID: %d", cmd.Process.Pid))

	// Wait for the daemon to create the socket and be ready to accept connections
	for range 20 {
		time.Sleep(100 * time.Millisecond)
		// Try to connect to the socket to verify daemon is actually listening
		if _, err := SendCommand("STATUS"); err == nil {
			slog.Info("Daemon is ready.")
			return
		}
	}
	slog.Error("Fatal: Daemon process was launched but socket was not created in time.")
	os.Exit(1)
}

// CheckVersionMismatch checks if the client and daemon versions match and warns if they don't.
// This check is done only once per command execution.
func CheckVersionMismatch() {
	versionCheckOnce.Do(func() {
		response, err := SendCommand("VERSION")
		if err != nil {
			// Daemon not running, no need to check version
			return
		}

		if response.Data != nil {
			// Data comes back as map[string]interface{} from JSON unmarshaling
			if dataMap, ok := response.Data.(map[string]interface{}); ok {
				if daemonVersion, ok := dataMap["version"].(string); ok {
					clientVersion := core.Version

					if clientVersion != daemonVersion {
						// Use formatted versions in the warning
						clientFormatted := core.FormatVersion(clientVersion)
						daemonFormatted := core.FormatVersion(daemonVersion)
						slog.Warn(fmt.Sprintf("Version mismatch! Client %s and daemon %s versions differ.", clientFormatted, daemonFormatted))
						slog.Warn("The daemon may be running an outdated version. Run 'overseer stop' and try again.")
						versionWarned = true
					}
				}
			}
		}
	})
}

// StartDaemon starts the daemon process in the background
func StartDaemon() error {
	cmd := exec.Command(os.Args[0], "daemon")

	// Pass the parent PID (shell/SSH session) to the daemon
	// The daemon will monitor this PID instead of its own parent (which will be PID 1)
	// This is critical for remote mode: when you SSH in and run 'overseer start',
	// the daemon needs to monitor the SSH session, not init.
	parentPID := os.Getppid()
	cmd.Env = append(os.Environ(), fmt.Sprintf("OVERSEER_MONITOR_PID=%d", parentPID))

	if err := cmd.Start(); err != nil {
		return fmt.Errorf("could not fork daemon process: %w", err)
	}
	return nil
}

// WaitForDaemon waits for the daemon to be ready
func WaitForDaemon() error {
	for range 20 {
		time.Sleep(100 * time.Millisecond)
		if _, err := SendCommand("STATUS"); err == nil {
			return nil
		}
	}
	return fmt.Errorf("daemon was launched but socket was not created in time")
}

// WaitForDaemonStop waits for the daemon to stop
func WaitForDaemonStop() error {
	for range 20 {
		time.Sleep(100 * time.Millisecond)
		if _, err := SendCommand("STATUS"); err != nil {
			// Daemon stopped (connection failed)
			return nil
		}
	}
	return fmt.Errorf("daemon did not stop in time")
}
