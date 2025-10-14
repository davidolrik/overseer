package daemon

import (
	"bufio"
	"fmt"
	"io"
	"log/slog"
	"net"
	"os"
	"os/exec"
	"os/signal"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"olrik.dev/davidolrik/overseer/internal/core"
	"olrik.dev/davidolrik/overseer/internal/keyring"
)

// Daemon manages the SSH tunnel processes.
type Daemon struct {
	tunnels      map[string]Tunnel
	askpassTokens map[string]string // Maps token -> alias for validation
	mu           sync.Mutex
	listener     net.Listener
	shutdownOnce sync.Once
}

type Tunnel struct {
	Hostname     string
	Pid          int
	Cmd          *exec.Cmd
	StartDate    time.Time
	AskpassToken string // Token for this tunnel's askpass validation
}

func New() *Daemon {
	return &Daemon{
		tunnels:       make(map[string]Tunnel),
		askpassTokens: make(map[string]string),
	}
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
		slog.Error(fmt.Sprintf("Fatal: Could not create socket listener: %v", err))
		os.Exit(1)
	}
	d.listener = listener
	slog.Info(fmt.Sprintf("Daemon listening on %s", socketPath))

	// Graceful shutdown on SIGTERM/SIGINT
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGTERM, syscall.SIGINT)
	go func() {
		<-sigChan
		slog.Info("Shutdown signal received. Closing all tunnels.")
		d.shutdown()
		os.Exit(0)
	}()

	// Accept connections in a loop
	for {
		conn, err := d.listener.Accept()
		if err != nil {
			if !strings.Contains(err.Error(), "use of closed network connection") {
				slog.Info(fmt.Sprintf("Error accepting connection: %v", err))
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

	var response Response
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
				response.AddMessage("Last tunnel closed. Daemon is shutting down.", "INFO")
			}

			// Send the final (potentially multi-line) response back to the client.
			conn.Write([]byte(response.ToJSON()))

			// Now, perform the shutdown on the daemon side.
			if shouldShutdown {
				// We can log here for the daemon's own records.
				slog.Info("Last tunnel closed. Shutting down.")
				d.shutdown()
			}
			return // We've handled the entire response and action for this case.
		}
	case "STOPALL":
		for alias := range d.tunnels {
			stopResponse, shouldShutdown := d.stopTunnel(alias)
			response.AddMessage(stopResponse.Messages[0].Message, stopResponse.Messages[0].Status)

			if shouldShutdown {
				response.AddMessage("Last tunnel closed. Daemon is shutting down.", "INFO")

				// We can log here for the daemon's own records.
				slog.Info("Last tunnel closed. Shutting down.")
				d.shutdown()
				break
			}
		}
	case "STATUS":
		response = d.getStatus()
	case "ASKPASS":
		if len(args) >= 2 {
			response = d.handleAskpass(args[0], args[1])
		} else {
			response.AddMessage("Invalid ASKPASS command", "ERROR")
		}
	default:
		response.AddMessage("Unknown command.", "ERROR")
	}
	conn.Write([]byte(response.ToJSON()))
}

func (d *Daemon) startTunnel(alias string) Response {
	// Note: We cannot use defer d.mu.Unlock() here because we need to unlock
	// early (before waiting for connection verification) and the function continues
	// to execute afterward. Using defer would cause a double-unlock panic.
	d.mu.Lock()

	response := Response{}

	if _, exists := d.tunnels[alias]; exists {
		d.mu.Unlock()
		response.AddMessage(fmt.Sprintf("Tunnel '%s' is already running.", alias), "ERROR")
		return response
	}

	// Check if a password is stored for this alias
	hasPassword := keyring.HasPassword(alias)

	// Create SSH command with verbose mode to detect connection status
	cmd := exec.Command("ssh", alias, "-N", "-o", "ExitOnForwardFailure=yes", "-v")
	cmd.Env = os.Environ()

	// Capture stderr to monitor connection status
	stderrPipe, err := cmd.StderrPipe()
	if err != nil {
		d.mu.Unlock()
		response.AddMessage(fmt.Sprintf("Failed to create stderr pipe: %v", err), "ERROR")
		return response
	}

	var token string
	if hasPassword {
		// Configure SSH to use overseer binary as askpass helper
		token, err = keyring.ConfigureSSHAskpass(cmd, alias)
		if err != nil {
			d.mu.Unlock()
			response.AddMessage(fmt.Sprintf("Failed to configure askpass: %v", err), "ERROR")
			return response
		}

		// Store token for validation when askpass command calls back
		d.askpassTokens[token] = alias
	}

	err = cmd.Start()
	if err != nil {
		if token != "" {
			delete(d.askpassTokens, token)
		}
		d.mu.Unlock()
		response.AddMessage(fmt.Sprintf("Failed to launch SSH process for '%s': %v", alias, err), "ERROR")
		return response
	}

	d.tunnels[alias] = Tunnel{
		Hostname:     alias,
		Pid:          cmd.Process.Pid,
		Cmd:          cmd,
		StartDate:    time.Now(),
		AskpassToken: token,
	}
	slog.Info(fmt.Sprintf("Attempting to start tunnel for '%s' (PID %d)", alias, cmd.Process.Pid))

	// Unlock mutex before waiting for connection verification.
	// We cannot use defer because the function continues executing after this unlock.
	d.mu.Unlock()

	// Wait for connection verification (indefinitely until success or failure)
	connectionResult := make(chan error, 1)
	go d.verifyConnection(stderrPipe, alias, connectionResult)

	// Wait for either success or failure - no timeout
	err = <-connectionResult
	if err != nil {
		response.AddMessage(fmt.Sprintf("Tunnel '%s' failed to connect: %v", alias, err), "ERROR")
		// Clean up the failed tunnel
		d.mu.Lock()
		if tunnel, exists := d.tunnels[alias]; exists {
			tunnel.Cmd.Process.Kill()
			if tunnel.AskpassToken != "" {
				delete(d.askpassTokens, tunnel.AskpassToken)
			}
			delete(d.tunnels, alias)
		}
		d.mu.Unlock()
		return response
	}
	response.AddMessage(fmt.Sprintf("Tunnel '%s' connected successfully.", alias), "INFO")

	// This goroutine waits for the process to exit for any reason.
	go func() {
		waitErr := cmd.Wait()
		if waitErr != nil {
			slog.Info(fmt.Sprintf("Tunnel process for '%s' exited with an error: %v", alias, waitErr))
		} else {
			slog.Info(fmt.Sprintf("Tunnel process for '%s' exited successfully.", alias))
		}

		d.mu.Lock()
		// Clean up askpass token if it exists
		if tunnel, exists := d.tunnels[alias]; exists && tunnel.AskpassToken != "" {
			delete(d.askpassTokens, tunnel.AskpassToken)
		}
		delete(d.tunnels, alias)

		// Check if the map is empty after deletion.
		shouldShutdown := len(d.tunnels) == 0
		d.mu.Unlock()

		if shouldShutdown {
			slog.Info("Last active tunnel process has exited. Triggering daemon shutdown.")
			// Instead of os.Exit(), we call our safe shutdown function.
			// This will close the listener and allow the main process to exit gracefully.
			d.shutdown()
		}
	}()

	return response
}

// verifyConnection monitors SSH stderr output to detect connection success or failure
func (d *Daemon) verifyConnection(stderr io.ReadCloser, alias string, result chan<- error) {
	defer func() {
		// Ensure we always send a result, even if we exit unexpectedly
		select {
		case result <- fmt.Errorf("SSH process terminated unexpectedly"):
		default:
			// Channel already has a value, nothing to do
		}
	}()

	scanner := bufio.NewScanner(stderr)
	authenticated := false

	for scanner.Scan() {
		line := scanner.Text()
		slog.Debug(fmt.Sprintf("[%s] SSH: %s", alias, line))

		// Track authentication completion
		if strings.Contains(line, "Authentication succeeded") ||
			strings.Contains(line, "Authenticated to") {
			authenticated = true
			// Don't return yet - we need to wait for the session to be established
		}

		// Look for success indicators - session fully established
		// For -N (no command), look for "pledge: network" or "Entering interactive session"
		if authenticated && (strings.Contains(line, "Entering interactive session") ||
			strings.Contains(line, "pledge: network")) {
			result <- nil
			return
		}

		// Look for failure indicators
		if strings.Contains(line, "Permission denied") {
			result <- fmt.Errorf("authentication failed")
			return
		}
		if strings.Contains(line, "Connection refused") {
			result <- fmt.Errorf("connection refused")
			return
		}
		if strings.Contains(line, "No route to host") {
			result <- fmt.Errorf("no route to host")
			return
		}
		if strings.Contains(line, "Connection timed out") {
			result <- fmt.Errorf("connection timed out")
			return
		}
		if strings.Contains(line, "Could not resolve hostname") {
			result <- fmt.Errorf("could not resolve hostname")
			return
		}
		if strings.Contains(line, "Host key verification failed") {
			result <- fmt.Errorf("host key verification failed")
			return
		}
		if strings.Contains(line, "Too many authentication failures") {
			result <- fmt.Errorf("too many authentication failures")
			return
		}
	}

	// If we exit the loop without finding success/failure, check scanner error
	if err := scanner.Err(); err != nil {
		slog.Debug(fmt.Sprintf("[%s] Error reading SSH output: %v", alias, err))
		result <- fmt.Errorf("error reading SSH output: %v", err)
	}
}

func (d *Daemon) stopTunnel(alias string) (Response, bool) {
	d.mu.Lock()
	defer d.mu.Unlock()

	response := Response{}

	tunnel, exists := d.tunnels[alias]
	if !exists {
		response.AddMessage(fmt.Sprintf("Tunnel '%s' is not running.", alias), "ERROR")
		return response, false
	}

	if err := tunnel.Cmd.Process.Kill(); err != nil {
		// Even if killing fails, we should clean up the map and token
		if tunnel.AskpassToken != "" {
			delete(d.askpassTokens, tunnel.AskpassToken)
		}
		delete(d.tunnels, alias)
		response.AddMessage(fmt.Sprintf("Failed to kill process for '%s': %v", alias, err), "ERROR")
		return response, len(d.tunnels) == 0
	}

	// Clean up askpass token
	if tunnel.AskpassToken != "" {
		delete(d.askpassTokens, tunnel.AskpassToken)
	}
	delete(d.tunnels, alias)
	slog.Info(fmt.Sprintf("Stopped tunnel for '%s'.", alias))

	// Check if this was the last tunnel
	shouldShutdown := len(d.tunnels) == 0

	response.AddMessage(fmt.Sprintf("Tunnel process for '%s' stopped.", alias), "INFO")
	return response, shouldShutdown
}

type DaemonStatus struct {
	Hostname  string `json:"hostname"`
	Pid       int    `json:"pid"`
	StartDate string `json:"start_date"`
}

func (d *Daemon) getStatus() Response {
	d.mu.Lock()
	defer d.mu.Unlock()

	// Build statuses json
	statuses := []DaemonStatus{}
	response := Response{}

	// No tunnels
	if len(d.tunnels) == 0 {
		response.AddMessage("No tunnels found", "WARN")
		response.AddData(statuses)
		return response
	}

	// We have tunnels
	response.AddMessage("OK", "INFO")
	for alias, tunnel := range d.tunnels {
		statuses = append(statuses, DaemonStatus{
			Hostname:  alias,
			Pid:       tunnel.Cmd.Process.Pid,
			StartDate: tunnel.StartDate.Format(time.RFC3339),
		})
	}
	response.AddData(statuses)

	return response
}

// handleAskpass validates the token and returns the password
func (d *Daemon) handleAskpass(alias, token string) Response {
	d.mu.Lock()
	defer d.mu.Unlock()

	response := Response{}

	// Validate token matches the stored alias
	storedAlias, exists := d.askpassTokens[token]
	if !exists || storedAlias != alias {
		// Invalid token or alias mismatch
		response.AddMessage("", "ERROR")
		return response
	}

	// Token is valid, retrieve password from keyring
	password, err := keyring.GetPassword(alias)
	if err != nil || password == "" {
		response.AddMessage("", "ERROR")
		return response
	}

	// Return password in the message (it will be output to stdout by askpass command)
	response.AddMessage(password, "INFO")

	// Don't delete token yet - SSH might call askpass multiple times
	// Token will be cleaned up when tunnel stops

	return response
}

// This makes it safe to call multiple times from multiple goroutines.
func (d *Daemon) shutdown() {
	d.shutdownOnce.Do(func() {
		slog.Info("Executing shutdown sequence...")

		// Close the listener first to prevent any new client connections.
		// This will unblock the main Accept() loop in the Run() function.
		if d.listener != nil {
			d.listener.Close()
		}

		d.mu.Lock()
		defer d.mu.Unlock()

		for alias, tunnel := range d.tunnels {
			tunnel.Cmd.Process.Kill()
			slog.Info(fmt.Sprintf("Killed process for '%s'", alias))
		}
		d.tunnels = make(map[string]Tunnel)
	})
}
