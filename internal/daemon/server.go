package daemon

import (
	"bufio"
	"encoding/json"
	"fmt"
	"log"
	"net"
	"os"
	"os/exec"
	"os/signal"
	"strconv"
	"strings"
	"sync"
	"syscall"

	"olrik.dev/davidolrik/overseer/internal/core"
)

// Daemon manages the SSH tunnel processes.
type Daemon struct {
	tunnels      map[string]*exec.Cmd
	mu           sync.Mutex
	listener     net.Listener
	shutdownOnce sync.Once
}

func New() *Daemon {
	return &Daemon{tunnels: make(map[string]*exec.Cmd)}
}

// Run starts the daemon's main loop.
func (d *Daemon) Run() {
	// Setup PID and socket files and ensure they are cleaned up on exit.
	socketPath := core.GetSocketPath()
	os.WriteFile(core.GetPIDFilePath(), []byte(strconv.Itoa(os.Getpid())), 0o644)
	defer os.Remove(core.GetPIDFilePath())
	defer os.Remove(core.GetSocketPath())

	listener, err := net.Listen("unix", socketPath)
	if err != nil {
		log.Fatalf("Fatal: Could not create socket listener: %v", err)
	}
	d.listener = listener
	log.Println("Daemon listening on", socketPath)

	// Graceful shutdown on SIGTERM/SIGINT
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGTERM, syscall.SIGINT)
	go func() {
		<-sigChan
		log.Println("Shutdown signal received. Closing all tunnels.")
		d.shutdown()
		os.Exit(0)
	}()

	// Accept connections in a loop
	for {
		conn, err := d.listener.Accept()
		if err != nil {
			if !strings.Contains(err.Error(), "use of closed network connection") {
				log.Printf("Error accepting connection: %v", err)
			}
			break
		}
		go d.handleConnection(conn)
	}
}

func (d *Daemon) handleConnection(conn net.Conn) {
	defer conn.Close()
	scanner := bufio.NewScanner(conn)
	if !scanner.Scan() {
		return
	}

	parts := strings.Fields(scanner.Text())
	if len(parts) == 0 {
		return
	}
	command, args := parts[0], parts[1:]

	var response string
	switch command {
	case "START":
		if len(args) > 0 {
			response = d.startTunnel(args[0])
		}
	case "STOP":
		if len(args) > 0 {
			var shouldShutdown bool
			response, shouldShutdown = d.stopTunnel(args[0])

			// If a shutdown is imminent, append the notification to the client's response.
			if shouldShutdown {
				response += "\nINFO: Last tunnel closed. Daemon is shutting down."
			}

			// Send the final (potentially multi-line) response back to the client.
			conn.Write([]byte(response + "\n"))

			// Now, perform the shutdown on the daemon side.
			if shouldShutdown {
				// We can log here for the daemon's own records.
				log.Println("Last tunnel closed. Shutting down.")
				d.shutdown()
			}
			return // We've handled the entire response and action for this case.
		}
	case "STOPALL":
		response = ""
		for alias := range d.tunnels {
			stopResponse, shouldShutdown := d.stopTunnel(alias)
			response += stopResponse + "\n"

			if shouldShutdown {
				response += "INFO: Last tunnel closed. Daemon is shutting down."

				// We can log here for the daemon's own records.
				log.Println("Last tunnel closed. Shutting down.")
				d.shutdown()
				break
			}
		}
	case "STATUS":
		response = d.getStatus()
	default:
		response = "ERROR: Unknown command."
	}
	conn.Write([]byte(response + "\n"))
}

func (d *Daemon) startTunnel(alias string) string {
	d.mu.Lock()
	defer d.mu.Unlock()

	if _, exists := d.tunnels[alias]; exists {
		return fmt.Sprintf("ERROR: Tunnel '%s' is already running.", alias)
	}

	cmd := exec.Command("ssh", alias, "-N", "-o", "ExitOnForwardFailure=yes")
	err := cmd.Start()
	if err != nil {
		return fmt.Sprintf("ERROR: Failed to launch SSH process for '%s': %v", alias, err)
	}

	d.tunnels[alias] = cmd
	log.Printf("Attempting to start tunnel for '%s' (PID %d)", alias, cmd.Process.Pid)

	// This goroutine waits for the process to exit for any reason.
	go func() {
		waitErr := cmd.Wait()
		if waitErr != nil {
			log.Printf("Tunnel process for '%s' exited with an error: %v", alias, waitErr)
		} else {
			log.Printf("Tunnel process for '%s' exited successfully.", alias)
		}

		d.mu.Lock()
		delete(d.tunnels, alias)

		// Check if the map is empty after deletion.
		shouldShutdown := len(d.tunnels) == 0
		d.mu.Unlock()

		if shouldShutdown {
			log.Println("Last active tunnel process has exited. Triggering daemon shutdown.")
			// Instead of os.Exit(), we call our safe shutdown function.
			// This will close the listener and allow the main process to exit gracefully.
			d.shutdown()
		}
	}()

	return fmt.Sprintf("OK: Tunnel process for '%s' launched.", alias)
}

func (d *Daemon) stopTunnel(alias string) (string, bool) {
	d.mu.Lock()
	defer d.mu.Unlock()

	cmd, exists := d.tunnels[alias]
	if !exists {
		return fmt.Sprintf("ERROR: Tunnel for '%s' is not running.", alias), false
	}

	if err := cmd.Process.Kill(); err != nil {
		// Even if killing fails, we should clean up the map.
		delete(d.tunnels, alias)
		return fmt.Sprintf("ERROR: Failed to kill process for '%s': %v", alias, err), len(d.tunnels) == 0
	}

	delete(d.tunnels, alias)
	log.Printf("Stopped tunnel for '%s'.", alias)

	// Check if this was the last tunnel
	shouldShutdown := len(d.tunnels) == 0

	return fmt.Sprintf("OK: Tunnel for '%s' stopped.", alias), shouldShutdown
}

type DaemonStatus struct {
	Hostname string `json:"hostname"`
	Pid      int    `json:"pid"`
}

func (d *Daemon) getStatus() string {
	d.mu.Lock()
	defer d.mu.Unlock()
	if len(d.tunnels) == 0 {
		return "[]"
	}

	// Build status json
	status := []DaemonStatus{}
	for alias, cmd := range d.tunnels {
		status = append(status, DaemonStatus{
			Hostname: alias,
			Pid:      cmd.Process.Pid,
		})
	}

	bytes, err := json.Marshal(status)
	if err != nil {
		panic(err)
	}

	return string(bytes)
}

// This makes it safe to call multiple times from multiple goroutines.
func (d *Daemon) shutdown() {
	d.shutdownOnce.Do(func() {
		log.Println("Executing shutdown sequence...")

		// Close the listener first to prevent any new client connections.
		// This will unblock the main Accept() loop in the Run() function.
		if d.listener != nil {
			d.listener.Close()
		}

		d.mu.Lock()
		defer d.mu.Unlock()

		for alias, cmd := range d.tunnels {
			cmd.Process.Kill()
			log.Printf("Killed process for '%s'", alias)
		}
		d.tunnels = make(map[string]*exec.Cmd)
	})
}
