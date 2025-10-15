package daemon

import (
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net"
	"os"
	"os/exec"
	"sync"
	"time"

	"olrik.dev/davidolrik/overseer/internal/core"
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

	// Wait for the daemon to create the socket
	for i := 0; i < 20; i++ {
		time.Sleep(100 * time.Millisecond)
		if _, err := os.Stat(core.GetSocketPath()); err == nil {
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
			jsonBytes, _ := json.Marshal(response.Data)
			var versionData map[string]string
			if json.Unmarshal(jsonBytes, &versionData) == nil {
				daemonVersion := versionData["version"]
				clientVersion := core.Version

				if clientVersion != daemonVersion {
					// Use formatted versions in the warning
					clientFormatted := core.FormatVersion(clientVersion)
					daemonFormatted := core.FormatVersion(daemonVersion)
					slog.Warn(fmt.Sprintf("Version mismatch! Client (%s) and daemon (%s) versions differ.", clientFormatted, daemonFormatted))
					slog.Warn("The daemon may be running an outdated version. Run 'overseer quit' and try again.")
					versionWarned = true
				}
			}
		}
	})
}
