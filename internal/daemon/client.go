package daemon

import (
	"fmt"
	"io"
	"log"
	"net"
	"os"
	"os/exec"
	"time"

	"olrik.dev/davidolrik/overseer/internal/core"
)

// SendCommand connects to the daemon, sends a command, and returns the response.
func SendCommand(command string) (string, error) {
	conn, err := net.Dial("unix", core.GetSocketPath())
	if err != nil {
		return "", err
	}
	defer conn.Close()

	if _, err := conn.Write([]byte(command + "\n")); err != nil {
		return "", fmt.Errorf("failed to send command to daemon: %w", err)
	}
	response, err := io.ReadAll(conn)
	if err != nil {
		return "", fmt.Errorf("failed to read response from daemon: %w", err)
	}
	return string(response), nil
}

// EnsureDaemonIsRunning handles the auto-start logic.
func EnsureDaemonIsRunning() {
	if _, err := SendCommand("STATUS"); err == nil {
		return // Daemon is running
	}

	log.Println("Daemon not running. Starting it now...")
	cmd := exec.Command(os.Args[0], "internal-daemon-start")
	if err := cmd.Start(); err != nil {
		log.Fatalf("Fatal: Could not fork daemon process: %v", err)
	}
	log.Printf("Daemon process launched with PID: %d", cmd.Process.Pid)

	// Wait for the daemon to create the socket
	for i := 0; i < 20; i++ {
		time.Sleep(100 * time.Millisecond)
		if _, err := os.Stat(core.GetSocketPath()); err == nil {
			log.Println("Daemon is ready.")
			return
		}
	}
	log.Fatal("Fatal: Daemon process was launched but socket was not created in time.")
}
