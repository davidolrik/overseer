package daemon

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"log/slog"
	"net"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/fsnotify/fsnotify"
	"olrik.dev/davidolrik/overseer/internal/core"
	"olrik.dev/davidolrik/overseer/internal/db"
	"olrik.dev/davidolrik/overseer/internal/keyring"
	"olrik.dev/davidolrik/overseer/internal/security"
)

// Daemon manages the SSH tunnel processes and security context.
type Daemon struct {
	tunnels         map[string]Tunnel
	askpassTokens   map[string]string // Maps token -> alias for validation
	mu              sync.Mutex
	listener        net.Listener
	shutdownOnce    sync.Once
	logBroadcast    *LogBroadcaster    // For streaming logs to clients
	securityManager *security.Manager  // Security context manager
	database        *db.DB             // Database for logging
	isRemote        bool               // Running on remote server (via SSH)
	parentMonitor   *ParentMonitor     // Monitors parent process in remote mode
	ctx             context.Context    // Context for lifecycle management
	cancelFunc      context.CancelFunc // Cancel function for context
}

type TunnelState string

const (
	StateConnected    TunnelState = "connected"
	StateDisconnected TunnelState = "disconnected"
	StateReconnecting TunnelState = "reconnecting"
)

type Tunnel struct {
	Hostname          string
	Pid               int
	Cmd               *exec.Cmd
	StartDate         time.Time // Original tunnel creation time
	LastConnectedTime time.Time // Time of last successful connection (for age display)
	AskpassToken      string    // Token for this tunnel's askpass validation
	RetryCount        int       // Current reconnection attempt number
	TotalReconnects   int       // Total successful reconnections (stability indicator)
	LastRetryTime     time.Time
	AutoReconnect     bool        // Whether to auto-reconnect on failure
	State             TunnelState // Current connection state
	NextRetryTime     time.Time   // When the next retry will occur
}

func New() *Daemon {
	ctx, cancel := context.WithCancel(context.Background())
	return &Daemon{
		tunnels:       make(map[string]Tunnel),
		askpassTokens: make(map[string]string),
		logBroadcast:  NewLogBroadcaster(),
		ctx:           ctx,
		cancelFunc:    cancel,
	}
}

// mergeEnvironment merges user environment variables into default environment
// User variables take precedence over defaults
func mergeEnvironment(defaultEnv, userEnv map[string]string) map[string]string {
	merged := make(map[string]string)

	// Copy defaults
	for k, v := range defaultEnv {
		merged[k] = v
	}

	// Override/add user values
	for k, v := range userEnv {
		merged[k] = v
	}

	return merged
}

// mergeLocation merges a user-defined location with default location settings
// Preserves user customizations while applying defaults for missing fields
func mergeLocation(defaultLoc, userLoc security.Location) security.Location {
	merged := defaultLoc

	// User can override display name
	if userLoc.DisplayName != "" {
		merged.DisplayName = userLoc.DisplayName
	}

	// Merge environment variables (user vars override/extend defaults)
	if len(userLoc.Environment) > 0 {
		merged.Environment = mergeEnvironment(defaultLoc.Environment, userLoc.Environment)
	}

	// Note: We keep the default Conditions/Condition to ensure core matching logic stays correct
	// If user really wants to override, they can define a completely different location

	return merged
}

// mergeRule merges a user-defined context rule with default rule settings
// Preserves user customizations while applying defaults for missing fields
func mergeRule(defaultRule, userRule security.Rule) security.Rule {
	merged := defaultRule

	// User can override display name
	if userRule.DisplayName != "" {
		merged.DisplayName = userRule.DisplayName
	}

	// Merge environment variables (user vars override/extend defaults)
	if len(userRule.Environment) > 0 {
		merged.Environment = mergeEnvironment(defaultRule.Environment, userRule.Environment)
	}

	// User can override actions
	if len(userRule.Actions.Connect) > 0 || len(userRule.Actions.Disconnect) > 0 {
		merged.Actions = userRule.Actions
	}

	// Note: We keep the default Conditions/Condition to ensure core matching logic stays correct

	return merged
}

// calculateBackoff calculates the exponential backoff duration
func calculateBackoff(retryCount int) time.Duration {
	// Parse config values
	initialBackoffStr := core.Config.SSH.InitialBackoff
	maxBackoffStr := core.Config.SSH.MaxBackoff
	backoffFactor := core.Config.SSH.BackoffFactor

	initialBackoff, err := time.ParseDuration(initialBackoffStr)
	if err != nil {
		slog.Error(fmt.Sprintf("Invalid initial_backoff config: %v, using default 1s", err))
		initialBackoff = 1 * time.Second
	}

	maxBackoff, err := time.ParseDuration(maxBackoffStr)
	if err != nil {
		slog.Error(fmt.Sprintf("Invalid max_backoff config: %v, using default 5m", err))
		maxBackoff = 5 * time.Minute
	}

	if retryCount <= 0 {
		return initialBackoff
	}

	// Calculate exponential backoff: initialBackoff * (backoffFactor ^ retryCount)
	backoff := initialBackoff
	for i := 0; i < retryCount && backoff < maxBackoff; i++ {
		backoff *= time.Duration(backoffFactor)
	}

	// Cap at maxBackoff
	if backoff > maxBackoff {
		backoff = maxBackoff
	}

	return backoff
}

// Run starts the daemon's main loop.
func (d *Daemon) Run() {
	// Setup custom logger that broadcasts to connected clients
	d.setupLogging()

	// Check if running in remote mode (via SSH)
	d.isRemote = os.Getenv("SSH_CONNECTION") != ""
	if d.isRemote {
		slog.Info("Running in remote mode - will exit on SSH disconnect")

		// Start parent process monitoring for robust disconnect detection
		// This provides multi-layer protection:
		// - Layer 1: SIGHUP (handled below)
		// - Layer 2: Platform-specific parent death signal (Linux: prctl)
		// - Layer 3: PPID polling (all platforms)
		d.parentMonitor = NewParentMonitor(d)
		d.parentMonitor.Start(d.ctx)
	}

	// Initialize database
	dbPath := filepath.Join(core.Config.ConfigPath, "overseer.db")
	database, err := db.Open(dbPath)
	if err != nil {
		slog.Error("Failed to open database", "error", err, "path", dbPath)
	} else {
		d.database = database
		defer d.database.Close()
		slog.Info("Database opened", "path", dbPath)

		// Log daemon start event
		if err := d.database.LogDaemonEvent("start", fmt.Sprintf("daemon started (PID: %d, remote: %v)", os.Getpid(), d.isRemote)); err != nil {
			slog.Error("Failed to log daemon start", "error", err)
		}
	}

	// Setup PID and socket files and ensure they are cleaned up on exit.
	socketPath := core.GetSocketPath()
	pidFilePath := core.GetPIDFilePath()

	// Try to create the socket listener
	listener, err := net.Listen("unix", socketPath)
	if err != nil {
		// Socket creation failed - this could be due to a stale socket file
		if _, statErr := os.Stat(socketPath); statErr == nil {
			// Socket file exists, try to connect to it to see if daemon is actually running
			conn, dialErr := net.Dial("unix", socketPath)
			if dialErr == nil {
				// Successfully connected, daemon is running
				conn.Close()
				slog.Error("Fatal: Daemon is already running")
				os.Exit(1)
			}
			// Connection failed, socket file is stale - remove it
			slog.Info(fmt.Sprintf("Removing stale socket file: %s", socketPath))
			if removeErr := os.Remove(socketPath); removeErr != nil {
				slog.Error(fmt.Sprintf("Fatal: Could not remove stale socket: %v", removeErr))
				os.Exit(1)
			}
			// Try to create listener again
			listener, err = net.Listen("unix", socketPath)
		}
		if err != nil {
			slog.Error(fmt.Sprintf("Fatal: Could not create socket listener: %v", err))
			os.Exit(1)
		}
	}

	os.WriteFile(pidFilePath, []byte(strconv.Itoa(os.Getpid())), 0o644)
	defer os.Remove(pidFilePath)
	defer os.Remove(socketPath)

	d.listener = listener
	slog.Info(fmt.Sprintf("Daemon listening on %s", socketPath))

	// Initialize security context manager (always active)
	// Database logging is enabled inside initSecurityManager before starting
	if err := d.initSecurityManager(); err != nil {
		slog.Error("Failed to initialize security manager", "error", err)
	} else {
		slog.Info("Security context monitoring started")
	}

	// Watch config file for changes
	d.watchConfig()

	// Handle signals
	shutdownChan := make(chan os.Signal, 1)
	hupChan := make(chan os.Signal, 1)
	signal.Notify(shutdownChan, syscall.SIGTERM, syscall.SIGINT)
	signal.Notify(hupChan, syscall.SIGHUP)

	// Graceful shutdown on SIGTERM/SIGINT
	go func() {
		<-shutdownChan
		slog.Info("Shutdown signal received. Closing all tunnels.")
		d.shutdown()
		os.Exit(0)
	}()

	// Handle SIGHUP (SSH disconnect) in remote mode
	go func() {
		<-hupChan
		if d.isRemote {
			slog.Info("SIGHUP received in remote mode - SSH session disconnected. Shutting down.")
			if d.database != nil {
				d.database.LogDaemonEvent("ssh_disconnect", "SSH session ended, shutting down")
			}
			d.shutdown()
			os.Exit(0)
		} else {
			slog.Info("SIGHUP received (ignored - not in remote mode)")
		}
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

	// Log the command execution (skip VERSION and ASKPASS as they're internal)
	// VERSION: automatic version check, ASKPASS: contains sensitive auth token
	if command != "VERSION" && command != "ASKPASS" {
		if len(args) > 0 {
			slog.Info(fmt.Sprintf("Executing command: %s %v", command, args))
		} else {
			slog.Info(fmt.Sprintf("Executing command: %s", command))
		}
	}

	var response Response
	switch command {
	case "SSH_CONNECT":
		if len(args) > 0 {
			response = d.startTunnel(args[0])
		}
	case "SSH_DISCONNECT":
		if len(args) > 0 {
			response = d.stopTunnel(args[0])
		}
	case "SSH_DISCONNECT_ALL":
		for alias := range d.tunnels {
			stopResponse := d.stopTunnel(alias)
			response.AddMessage(stopResponse.Messages[0].Message, stopResponse.Messages[0].Status)
		}
	case "STOP":
		response = d.stopDaemon()
		// Send response before shutting down
		conn.Write([]byte(response.ToJSON()))
		// Shutdown the daemon
		slog.Info("Stop command received. Shutting down daemon.")
		d.shutdown()
		return // Don't send response again
	case "STATUS":
		response = d.getStatus()
	case "VERSION":
		response = d.getVersion()
	case "ASKPASS":
		if len(args) >= 2 {
			response = d.handleAskpass(args[0], args[1])
		} else {
			response.AddMessage("Invalid ASKPASS command", "ERROR")
		}
	case "RESET":
		response = d.resetRetries()
	case "LOGS":
		// Handle log streaming - don't send JSON response, just stream logs
		d.handleLogs(conn)
		return // Don't send JSON response
	case "CONTEXT_STATUS":
		response = d.getContextStatus()
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
	// Build SSH options from config
	sshArgs := []string{alias, "-N", "-o", "ExitOnForwardFailure=yes", "-v"}

	// Add ServerAliveInterval if configured (0 means disabled)
	if core.Config.SSH.ServerAliveInterval > 0 {
		sshArgs = append(sshArgs,
			"-o", fmt.Sprintf("ServerAliveInterval=%d", core.Config.SSH.ServerAliveInterval),
			"-o", fmt.Sprintf("ServerAliveCountMax=%d", core.Config.SSH.ServerAliveCountMax))
	}

	cmd := exec.Command("ssh", sshArgs...)
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

	now := time.Now()
	d.tunnels[alias] = Tunnel{
		Hostname:          alias,
		Pid:               cmd.Process.Pid,
		Cmd:               cmd,
		StartDate:         now,
		LastConnectedTime: now,
		AskpassToken:      token,
		RetryCount:        0,
		AutoReconnect:     core.Config.SSH.ReconnectEnabled, // Use config value
		State:             StateConnected,             // Initial state is connected
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

		// Log to database
		if d.database != nil {
			details := fmt.Sprintf("Failed: %v", err)
			if dbErr := d.database.LogTunnelEvent(alias, "connect_failed", details); dbErr != nil {
				slog.Error("Failed to log tunnel connect failure", "error", dbErr)
			}
		}

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

	// Log success in daemon
	slog.Info(fmt.Sprintf("Tunnel '%s' connected successfully (PID %d)", alias, cmd.Process.Pid))

	// Log to database
	if d.database != nil {
		details := fmt.Sprintf("PID: %d", cmd.Process.Pid)
		if err := d.database.LogTunnelEvent(alias, "connect", details); err != nil {
			slog.Error("Failed to log tunnel connect event", "error", err)
		}
	}

	// Trigger context check after successful SSH connection
	// This ensures the public_ip sensor is checked and the online sensor is updated
	if d.securityManager != nil {
		if err := d.securityManager.TriggerCheckWithReason("ssh_connect"); err != nil {
			slog.Warn("Failed to trigger context check after SSH connect", "error", err)
		}
	}

	// Send success message to client
	response.AddMessage(fmt.Sprintf("Tunnel '%s' connected successfully.", alias), "INFO")

	// This goroutine monitors the tunnel process and handles reconnection
	go d.monitorTunnel(alias)

	return response
}

// monitorTunnel watches a tunnel process and handles reconnection with exponential backoff
func (d *Daemon) monitorTunnel(alias string) {
	for {
		// Wait for the current process to exit
		d.mu.Lock()
		tunnel, exists := d.tunnels[alias]
		if !exists {
			d.mu.Unlock()
			return // Tunnel was manually stopped
		}
		cmd := tunnel.Cmd
		d.mu.Unlock()

		waitErr := cmd.Wait()

		d.mu.Lock()
		tunnel, exists = d.tunnels[alias]
		if !exists {
			d.mu.Unlock()
			return // Tunnel was manually stopped while we were waiting
		}

		// Log the exit
		if waitErr != nil {
			slog.Info(fmt.Sprintf("Tunnel process for '%s' exited with an error: %v", alias, waitErr))
		} else {
			slog.Info(fmt.Sprintf("Tunnel process for '%s' exited successfully.", alias))
		}

		// Log to database
		exitDetails := ""
		if waitErr != nil {
			exitDetails = fmt.Sprintf("Error: %v", waitErr)
		}
		if d.database != nil {
			if err := d.database.LogTunnelEvent(alias, "disconnect", exitDetails); err != nil {
				slog.Error("Failed to log tunnel disconnect", "error", err)
			}
		}

		// Update state to disconnected
		tunnel.State = StateDisconnected
		d.tunnels[alias] = tunnel

		// Get max retries from config
		maxRetries := core.Config.SSH.MaxRetries

		// Check if auto-reconnect is enabled and we haven't exceeded max retries
		if !tunnel.AutoReconnect || tunnel.RetryCount >= maxRetries {
			// Clean up and don't reconnect
			if tunnel.AskpassToken != "" {
				delete(d.askpassTokens, tunnel.AskpassToken)
			}
			delete(d.tunnels, alias)

			if tunnel.RetryCount >= maxRetries {
				slog.Info(fmt.Sprintf("Tunnel '%s' exceeded max retry attempts (%d). Giving up.", alias, maxRetries))

				// Log to database
				if d.database != nil {
					details := fmt.Sprintf("Max retries (%d) exceeded", maxRetries)
					if err := d.database.LogTunnelEvent(alias, "max_retries_exceeded", details); err != nil {
						slog.Error("Failed to log max retries exceeded", "error", err)
					}
				}
			} else {
				slog.Info(fmt.Sprintf("Tunnel '%s' auto-reconnect disabled. Not reconnecting.", alias))
			}

			d.mu.Unlock()
			return
		}

		// Check if we're online before attempting reconnection
		// If offline, just skip reconnect but don't change tunnel state
		// The SSH connection might still be alive despite brief offline periods
		isOnline := d.checkOnlineStatus()
		if !isOnline {
			slog.Info(fmt.Sprintf("Tunnel '%s' not reconnecting - currently offline (will retry when back online)", alias))
			d.mu.Unlock()
			return
		}

		// Calculate backoff delay
		backoff := calculateBackoff(tunnel.RetryCount)
		tunnel.RetryCount++
		tunnel.LastRetryTime = time.Now()
		tunnel.State = StateReconnecting
		tunnel.NextRetryTime = time.Now().Add(backoff)

		slog.Info(fmt.Sprintf("Tunnel '%s' will reconnect in %v (attempt %d/%d)",
			alias, backoff, tunnel.RetryCount, maxRetries))

		// Clean up old askpass token
		if tunnel.AskpassToken != "" {
			delete(d.askpassTokens, tunnel.AskpassToken)
			tunnel.AskpassToken = ""
		}

		// Update tunnel with new retry count and state
		d.tunnels[alias] = tunnel
		d.mu.Unlock()

		// Wait for backoff period (outside the lock)
		time.Sleep(backoff)

		// Attempt to reconnect
		slog.Info(fmt.Sprintf("Attempting to reconnect tunnel '%s' (attempt %d/%d)",
			alias, tunnel.RetryCount, maxRetries))

		d.mu.Lock()
		// Check again if tunnel still exists (might have been manually stopped during backoff)
		tunnel, exists = d.tunnels[alias]
		if !exists {
			d.mu.Unlock()
			return
		}

		// Check again if we're still online (might have gone offline during backoff)
		// If offline, just skip this reconnect attempt but don't change state
		if !d.checkOnlineStatus() {
			slog.Info(fmt.Sprintf("Tunnel '%s' reconnection cancelled - went offline during backoff", alias))
			d.mu.Unlock()
			return
		}

		// Check if a password is stored for this alias
		hasPassword := keyring.HasPassword(alias)

		// Create new SSH command
		// Build SSH options from config
		sshArgs := []string{alias, "-N", "-o", "ExitOnForwardFailure=yes", "-v"}

		// Add ServerAliveInterval if configured (0 means disabled)
		if core.Config.SSH.ServerAliveInterval > 0 {
			sshArgs = append(sshArgs,
				"-o", fmt.Sprintf("ServerAliveInterval=%d", core.Config.SSH.ServerAliveInterval),
				"-o", fmt.Sprintf("ServerAliveCountMax=%d", core.Config.SSH.ServerAliveCountMax))
		}

		newCmd := exec.Command("ssh", sshArgs...)
		newCmd.Env = os.Environ()

		// Capture stderr to monitor connection status
		stderrPipe, err := newCmd.StderrPipe()
		if err != nil {
			slog.Error(fmt.Sprintf("Failed to create stderr pipe for reconnection: %v", err))
			delete(d.tunnels, alias)
			d.mu.Unlock()
			return
		}

		var token string
		if hasPassword {
			token, err = keyring.ConfigureSSHAskpass(newCmd, alias)
			if err != nil {
				slog.Error(fmt.Sprintf("Failed to configure askpass for reconnection: %v", err))
				delete(d.tunnels, alias)
				d.mu.Unlock()
				return
			}
			d.askpassTokens[token] = alias
		}

		err = newCmd.Start()
		if err != nil {
			if token != "" {
				delete(d.askpassTokens, token)
			}
			slog.Error(fmt.Sprintf("Failed to launch SSH process for reconnection: %v", err))
			// Continue the loop to retry again
			tunnel.Cmd = nil // Mark as failed
			d.tunnels[alias] = tunnel
			d.mu.Unlock()
			continue
		}

		// Update tunnel info
		tunnel.Pid = newCmd.Process.Pid
		tunnel.Cmd = newCmd
		tunnel.AskpassToken = token
		tunnel.State = StateReconnecting // Still reconnecting until verified
		d.tunnels[alias] = tunnel

		slog.Info(fmt.Sprintf("Reconnection attempt started for '%s' (PID %d)", alias, newCmd.Process.Pid))
		d.mu.Unlock()

		// Wait for connection verification
		connectionResult := make(chan error, 1)
		go d.verifyConnection(stderrPipe, alias, connectionResult)

		err = <-connectionResult
		if err != nil {
			slog.Warn(fmt.Sprintf("Reconnection failed for '%s': %v", alias, err))

			// Log to database
			if d.database != nil {
				details := fmt.Sprintf("Attempt %d failed: %v", tunnel.RetryCount, err)
				if dbErr := d.database.LogTunnelEvent(alias, "reconnect_failed", details); dbErr != nil {
					slog.Error("Failed to log reconnection failure", "error", dbErr)
				}
			}

			// Kill the failed process and continue the loop to retry
			d.mu.Lock()
			if tunnel, exists := d.tunnels[alias]; exists {
				if tunnel.Cmd != nil && tunnel.Cmd.Process != nil {
					tunnel.Cmd.Process.Kill()
				}
			}
			d.mu.Unlock()
			continue
		}

		// Success! Reset retry count, update state, reset connection time, and increment total reconnects
		slog.Info(fmt.Sprintf("Tunnel '%s' reconnected successfully.", alias))

		// Log to database
		d.mu.Lock()
		currentTunnel := d.tunnels[alias]
		if d.database != nil {
			details := fmt.Sprintf("PID: %d, Total reconnects: %d", newCmd.Process.Pid, currentTunnel.TotalReconnects+1)
			if err := d.database.LogTunnelEvent(alias, "reconnect", details); err != nil {
				slog.Error("Failed to log tunnel reconnect event", "error", err)
			}
		}

		if tunnel, exists := d.tunnels[alias]; exists {
			tunnel.RetryCount = 0
			tunnel.State = StateConnected
			tunnel.NextRetryTime = time.Time{}    // Clear next retry time
			tunnel.LastConnectedTime = time.Now() // Reset age to 0
			tunnel.TotalReconnects++              // Increment stability counter
			d.tunnels[alias] = tunnel
		}
		d.mu.Unlock()

		// Trigger context check after successful SSH reconnection
		// This ensures the public_ip sensor is checked and the online sensor is updated
		if d.securityManager != nil {
			if err := d.securityManager.TriggerCheckWithReason("ssh_reconnect"); err != nil {
				slog.Warn("Failed to trigger context check after SSH reconnect", "error", err)
			}
		}

		// Continue monitoring this tunnel (loop back to Wait())
	}
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

func (d *Daemon) stopTunnel(alias string) Response {
	d.mu.Lock()
	defer d.mu.Unlock()

	response := Response{}

	tunnel, exists := d.tunnels[alias]
	if !exists {
		response.AddMessage(fmt.Sprintf("Tunnel '%s' is not running.", alias), "ERROR")
		return response
	}

	if err := tunnel.Cmd.Process.Kill(); err != nil {
		// Even if killing fails, we should clean up the map and token
		if tunnel.AskpassToken != "" {
			delete(d.askpassTokens, tunnel.AskpassToken)
		}
		delete(d.tunnels, alias)
		response.AddMessage(fmt.Sprintf("Failed to kill process for '%s': %v", alias, err), "ERROR")
		return response
	}

	// Clean up askpass token
	if tunnel.AskpassToken != "" {
		delete(d.askpassTokens, tunnel.AskpassToken)
	}
	delete(d.tunnels, alias)
	slog.Info(fmt.Sprintf("Stopped tunnel for '%s'.", alias))

	// Log to database
	if d.database != nil {
		if err := d.database.LogTunnelEvent(alias, "manual_stop", ""); err != nil {
			slog.Error("Failed to log tunnel manual stop", "error", err)
		}
	}

	response.AddMessage(fmt.Sprintf("Tunnel process for '%s' stopped.", alias), "INFO")
	return response
}

type DaemonStatus struct {
	Hostname          string      `json:"hostname"`
	Pid               int         `json:"pid"`
	StartDate         string      `json:"start_date"`          // Original tunnel creation time
	LastConnectedTime string      `json:"last_connected_time"` // Time of last successful connection
	RetryCount        int         `json:"retry_count,omitempty"`
	TotalReconnects   int         `json:"total_reconnects"` // Total successful reconnections
	AutoReconnect     bool        `json:"auto_reconnect"`
	State             TunnelState `json:"state"`
	NextRetry         string      `json:"next_retry,omitempty"` // ISO 8601 format
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
		status := DaemonStatus{
			Hostname:          alias,
			Pid:               tunnel.Pid,
			StartDate:         tunnel.StartDate.Format(time.RFC3339),
			LastConnectedTime: tunnel.LastConnectedTime.Format(time.RFC3339),
			RetryCount:        tunnel.RetryCount,
			TotalReconnects:   tunnel.TotalReconnects,
			AutoReconnect:     tunnel.AutoReconnect,
			State:             tunnel.State,
		}

		// Add next retry time if tunnel is in reconnecting state
		if tunnel.State == StateReconnecting && !tunnel.NextRetryTime.IsZero() {
			status.NextRetry = tunnel.NextRetryTime.Format(time.RFC3339)
		}

		statuses = append(statuses, status)
	}
	response.AddData(statuses)

	return response
}

func (d *Daemon) getVersion() Response {
	response := Response{}

	// Return the daemon version
	response.AddMessage("OK", "INFO")
	response.AddData(map[string]string{
		"version": core.Version,
	})

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

// resetRetries resets retry counters for all tunnels to zero
func (d *Daemon) resetRetries() Response {
	d.mu.Lock()
	defer d.mu.Unlock()

	response := Response{}

	if len(d.tunnels) == 0 {
		response.AddMessage("No tunnels to reset.", "WARN")
		return response
	}

	resetCount := 0
	for alias, tunnel := range d.tunnels {
		// Reset tunnels that have any retry activity (current or historical)
		if tunnel.RetryCount > 0 || tunnel.TotalReconnects > 0 || tunnel.State == StateReconnecting {
			tunnel.RetryCount = 0
			tunnel.TotalReconnects = 0
			tunnel.NextRetryTime = time.Time{} // Clear next retry time
			// Note: We keep the tunnel in its current state (connected/disconnected/reconnecting)
			// but reset all retry/reconnect counters to give it a fresh start
			d.tunnels[alias] = tunnel
			resetCount++
			slog.Info(fmt.Sprintf("Reset retry counters for tunnel '%s'", alias))
		}
	}

	if resetCount == 0 {
		response.AddMessage("No tunnels needed resetting.", "INFO")
	} else if resetCount == 1 {
		response.AddMessage("Reset 1 tunnel's retry counters.", "INFO")
	} else {
		response.AddMessage(fmt.Sprintf("Reset %d tunnels' retry counters.", resetCount), "INFO")
	}

	return response
}

// This makes it safe to call multiple times from multiple goroutines.
func (d *Daemon) shutdown() {
	d.shutdownOnce.Do(func() {
		slog.Info("Executing shutdown sequence...")

		// Stop security manager if running
		if d.securityManager != nil {
			d.securityManager.Stop()
		}

		// Cancel context to stop all background tasks
		if d.cancelFunc != nil {
			d.cancelFunc()
		}

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

// initSecurityManager initializes the security context manager
func (d *Daemon) initSecurityManager() error {
	return d.initSecurityManagerInternal(core.Config.CheckOnStartup)
}

// initSecurityManagerWithoutStartupCheck initializes the security manager without startup check
func (d *Daemon) initSecurityManagerWithoutStartupCheck() error {
	return d.initSecurityManagerInternal(false)
}

// initSecurityManagerInternal is the internal implementation for initializing the security manager
func (d *Daemon) initSecurityManagerInternal(checkOnStartup bool) error {
	// Convert location definitions from config
	locations := make(map[string]security.Location)
	for name, loc := range core.Config.Locations {
		secLoc := security.Location{
			Name:        loc.Name,
			DisplayName: loc.DisplayName,
			Conditions:  loc.Conditions,
			Environment: loc.Environment,
		}
		// Convert structured condition if present
		if loc.Condition != nil {
			if cond, ok := loc.Condition.(security.Condition); ok {
				secLoc.Condition = cond
			}
		}
		locations[name] = secLoc
	}

	// Define default locations that should always exist
	defaultOffline := security.Location{
		Name:        "offline",
		DisplayName: "Offline",
		Condition:   security.NewBooleanCondition("online", false),
		Environment: make(map[string]string),
	}

	defaultUnknown := security.Location{
		Name:        "unknown",
		DisplayName: "Unknown",
		Conditions:  map[string][]string{}, // No conditions - this is a fallback
		Environment: make(map[string]string),
	}

	// Merge with user-defined defaults if they exist
	if userOffline, exists := locations["offline"]; exists {
		locations["offline"] = mergeLocation(defaultOffline, userOffline)
	} else {
		locations["offline"] = defaultOffline
	}

	if userUnknown, exists := locations["unknown"]; exists {
		locations["unknown"] = mergeLocation(defaultUnknown, userUnknown)
	} else {
		locations["unknown"] = defaultUnknown
	}

	// Define default contexts that should always exist
	defaultUntrusted := security.Rule{
		Name:        "untrusted",
		DisplayName: "Untrusted",
		Conditions:  map[string][]string{}, // Empty conditions = fallback/default
		Environment: make(map[string]string),
		Actions: security.RuleActions{
			Connect:    []string{},
			Disconnect: []string{}, // By default, disconnect nothing
		},
	}

	// Convert context rules from config to security rules
	// Extract user customizations for defaults but don't add them yet
	rules := make([]security.Rule, 0, len(core.Config.Contexts)+1)
	var userUntrusted *security.Rule

	for _, contextRule := range core.Config.Contexts {
		secRule := security.Rule{
			Name:        contextRule.Name,
			DisplayName: contextRule.DisplayName,
			Locations:   contextRule.Locations,
			Conditions:  contextRule.Conditions,
			Environment: contextRule.Environment,
			Actions: security.RuleActions{
				Connect:    contextRule.Actions.Connect,
				Disconnect: contextRule.Actions.Disconnect,
			},
		}
		// Convert structured condition if present
		if contextRule.Condition != nil {
			if cond, ok := contextRule.Condition.(security.Condition); ok {
				secRule.Condition = cond
			}
		}

		// If this is a default context, save it for merging but DON'T add it to rules yet
		// This ensures defaults appear in the correct order regardless of config position
		if secRule.Name == "untrusted" {
			userUntrusted = &secRule
			continue // Skip adding to rules
		}

		// Add non-default contexts
		rules = append(rules, secRule)
	}

	// Now add default "untrusted" fallback at the end
	// Merge with user customizations if provided
	if userUntrusted != nil {
		rules = append(rules, mergeRule(defaultUntrusted, *userUntrusted))
	} else {
		rules = append(rules, defaultUntrusted)
	}

	// Convert export configs to security.ExportConfig format
	exports := make([]security.ExportConfig, len(core.Config.Exports))
	for i, exportCfg := range core.Config.Exports {
		exports[i] = security.ExportConfig{
			Type: exportCfg.Type,
			Path: exportCfg.Path,
		}
	}

	// Create security manager
	config := security.ManagerConfig{
		Rules:           rules,
		Locations:       locations,
		Exports:         exports,
		CheckOnStartup:  checkOnStartup,
		OnContextChange: d.handleContextChange,
		Logger:          slog.Default(),
	}

	manager, err := security.NewManager(config)
	if err != nil {
		return fmt.Errorf("failed to create security manager: %w", err)
	}

	d.securityManager = manager

	// Enable database logging BEFORE starting the manager
	// This ensures initial sensor readings on startup are logged
	if d.database != nil {
		manager.SetDatabase(d.database)
	}

	// Start the manager
	if err := manager.Start(d.ctx, checkOnStartup); err != nil {
		return fmt.Errorf("failed to start security manager: %w", err)
	}

	return nil
}

// checkOnlineStatus checks if we're currently online
func (d *Daemon) checkOnlineStatus() bool {
	if d.securityManager != nil {
		if onlineSensor := d.securityManager.GetSensor("online"); onlineSensor != nil {
			if value, err := onlineSensor.Check(context.Background()); err == nil {
				return value.Bool()
			}
		}
	}
	return false
}

// handleContextChange is called when the security context changes
func (d *Daemon) handleContextChange(from, to string, rule *security.Rule) {
	slog.Info("Security context changed",
		"from", from,
		"to", to)

	// If no rule matched, nothing to do
	if rule == nil {
		slog.Debug("No rule matched, skipping context change actions")
		return
	}

	slog.Debug("Context change with rule",
		"rule_name", rule.Name,
		"connect_count", len(rule.Actions.Connect),
		"disconnect_count", len(rule.Actions.Disconnect))

	// Check if we're online before attempting connections
	isOnline := d.checkOnlineStatus()

	if !isOnline && len(rule.Actions.Connect) > 0 {
		slog.Info("Skipping tunnel connections - currently offline",
			"context", to,
			"tunnel_count", len(rule.Actions.Connect))
	}

	// Execute disconnect actions first (always, even when offline)
	for _, alias := range rule.Actions.Disconnect {
		d.mu.Lock()
		_, exists := d.tunnels[alias]
		d.mu.Unlock()

		if exists {
			slog.Info("Auto-disconnecting tunnel due to context change",
				"tunnel", alias,
				"context", to)
			d.stopTunnel(alias)
		}
	}

	// Only execute connect actions if we're online
	if isOnline {
		for _, alias := range rule.Actions.Connect {
			d.mu.Lock()
			tunnel, exists := d.tunnels[alias]
			d.mu.Unlock()

			// Connect tunnel if it doesn't exist OR if it's in a disconnected/reconnecting state
			shouldConnect := false
			if !exists {
				shouldConnect = true
				slog.Info("Auto-connecting tunnel due to context change",
					"tunnel", alias,
					"context", to)
			} else if tunnel.State == StateDisconnected || tunnel.State == StateReconnecting {
				shouldConnect = true
				slog.Info("Reconnecting tunnel due to context change",
					"tunnel", alias,
					"context", to,
					"previous_state", tunnel.State)
				// Stop existing tunnel first (cleans up processes and timers)
				d.stopTunnel(alias)
			}

			if shouldConnect {
				resp := d.startTunnel(alias)
				// Check if any response messages indicate an error
				for _, msg := range resp.Messages {
					if msg.Status == "ERROR" {
						slog.Error("Failed to start tunnel during context change",
							"tunnel", alias,
							"context", to,
							"error", msg.Message)
					}
				}
			}
		}
	}
}

// ContextStatus represents the current security context information
type ContextStatus struct {
	Context       string              `json:"context"`
	Location      string              `json:"location,omitempty"`
	LastChange    string              `json:"last_change"`
	Uptime        string              `json:"uptime"`
	Sensors       map[string]string   `json:"sensors"`
	ChangeHistory []ContextChangeInfo `json:"change_history,omitempty"`
}

// ContextChangeInfo represents a context change event
type ContextChangeInfo struct {
	From         string `json:"from"`
	To           string `json:"to"`
	FromLocation string `json:"from_location,omitempty"`
	ToLocation   string `json:"to_location,omitempty"`
	Timestamp    string `json:"timestamp"`
	Trigger      string `json:"trigger"`
}

// getContextStatus returns the current security context status
func (d *Daemon) getContextStatus() Response {
	response := Response{}

	// Check if security manager is initialized
	if d.securityManager == nil {
		response.AddMessage("Security context manager not initialized", "ERROR")
		return response
	}

	// Get current context
	ctx := d.securityManager.GetContext()

	// Build sensor map
	sensors := make(map[string]string)
	for key, value := range ctx.GetAllSensors() {
		sensors[key] = value.String()
	}

	// Get change history
	history := ctx.GetChangeHistory()
	changeHistory := make([]ContextChangeInfo, 0, len(history))

	// Only include last 10 changes to keep response size manageable
	startIdx := 0
	if len(history) > 10 {
		startIdx = len(history) - 10
	}

	for i := startIdx; i < len(history); i++ {
		change := history[i]
		changeHistory = append(changeHistory, ContextChangeInfo{
			From:         change.From,
			To:           change.To,
			FromLocation: change.FromLocation,
			ToLocation:   change.ToLocation,
			Timestamp:    change.Timestamp.Format(time.RFC3339),
			Trigger:      change.Trigger,
		})
	}

	// Build status
	status := ContextStatus{
		Context:       ctx.GetContext(),
		Location:      ctx.GetLocation(),
		LastChange:    ctx.GetLastChange().Format(time.RFC3339),
		Uptime:        ctx.GetUptime().Round(time.Second).String(),
		Sensors:       sensors,
		ChangeHistory: changeHistory,
	}

	response.AddMessage("OK", "INFO")
	response.AddData(status)
	return response
}

// stopDaemon handles the STOP command to shutdown the daemon
func (d *Daemon) stopDaemon() Response {
	response := Response{}

	d.mu.Lock()
	tunnelCount := len(d.tunnels)
	d.mu.Unlock()

	if tunnelCount > 0 {
		response.AddMessage(fmt.Sprintf("Stopping daemon and disconnecting %d active tunnel(s)...", tunnelCount), "INFO")
	} else {
		response.AddMessage("Stopping daemon...", "INFO")
	}

	return response
}

// reloadConfig reloads the configuration and restarts the security manager
func (d *Daemon) reloadConfig() error {
	// Save the old config in case we need to roll back
	oldConfig := core.Config

	// Reload the configuration from the KDL file
	kdlPath := filepath.Join(core.Config.ConfigPath, "config.kdl")
	newConfig, err := core.LoadConfig(kdlPath)
	if err != nil {
		// Config parsing failed - keep the old config and log error
		// Clean up the error message by removing verbose prefixes and visual pointer
		errMsg := err.Error()
		// Remove "failed to unmarshal KDL: parse failed: " prefix if present
		errMsg = strings.TrimPrefix(errMsg, "failed to unmarshal KDL: parse failed: ")
		errMsg = strings.TrimPrefix(errMsg, "failed to unmarshal KDL: scan failed: ")

		// Remove the visual pointer (everything after the line/column info)
		// Format: "error at line X, column Y:\n...code snippet...\n   ^"
		if idx := strings.Index(errMsg, ":\n"); idx != -1 {
			errMsg = errMsg[:idx]
		}

		slog.Error("Configuration file has syntax errors, keeping previous configuration",
			"file", kdlPath,
			"error", errMsg)
		return fmt.Errorf("config parse error")
	}

	// Preserve the config path
	newConfig.ConfigPath = oldConfig.ConfigPath

	// Save reference to old manager for cleanup
	var oldManager *security.Manager
	if d.securityManager != nil {
		oldManager = d.securityManager
	}

	// Temporarily update the global config for initialization
	core.Config = newConfig

	// Try to initialize new security manager with new config
	// Don't stop the old one yet in case this fails
	var newManager *security.Manager
	if err := func() error {
		// Convert location definitions from new config
		locations := make(map[string]security.Location)
		for name, loc := range newConfig.Locations {
			secLoc := security.Location{
				Name:        loc.Name,
				DisplayName: loc.DisplayName,
				Conditions:  loc.Conditions,
				Environment: loc.Environment,
			}
			// Convert structured condition if present
			if loc.Condition != nil {
				if cond, ok := loc.Condition.(security.Condition); ok {
					secLoc.Condition = cond
				}
			}
			locations[name] = secLoc
		}

		// Define default locations that should always exist
		defaultOffline := security.Location{
			Name:        "offline",
			DisplayName: "Offline",
			Condition:   security.NewBooleanCondition("online", false),
			Environment: make(map[string]string),
		}

		defaultUnknown := security.Location{
			Name:        "unknown",
			DisplayName: "Unknown",
			Conditions:  map[string][]string{}, // No conditions - this is a fallback
			Environment: make(map[string]string),
		}

		// Merge with user-defined defaults if they exist
		if userOffline, exists := locations["offline"]; exists {
			locations["offline"] = mergeLocation(defaultOffline, userOffline)
		} else {
			locations["offline"] = defaultOffline
		}

		if userUnknown, exists := locations["unknown"]; exists {
			locations["unknown"] = mergeLocation(defaultUnknown, userUnknown)
		} else {
			locations["unknown"] = defaultUnknown
		}

		// Define default contexts that should always exist
		defaultUntrusted := security.Rule{
			Name:        "untrusted",
			DisplayName: "Untrusted",
			Conditions:  map[string][]string{},
			Environment: make(map[string]string),
			Actions: security.RuleActions{
				Connect:    []string{},
				Disconnect: []string{},
			},
		}

		// Create a temporary manager to test the new config
		// Extract user customizations for defaults but don't add them yet
		rules := make([]security.Rule, 0, len(newConfig.Contexts)+1)
		var userUntrusted *security.Rule

		for _, contextRule := range newConfig.Contexts {
			secRule := security.Rule{
				Name:        contextRule.Name,
				DisplayName: contextRule.DisplayName,
				Locations:   contextRule.Locations,
				Conditions:  contextRule.Conditions,
				Environment: contextRule.Environment,
				Actions: security.RuleActions{
					Connect:    contextRule.Actions.Connect,
					Disconnect: contextRule.Actions.Disconnect,
				},
			}
			// Convert structured condition if present
			if contextRule.Condition != nil {
				if cond, ok := contextRule.Condition.(security.Condition); ok {
					secRule.Condition = cond
				}
			}

			// If this is a default context, save it for merging but DON'T add it to rules yet
			// This ensures defaults appear in the correct order regardless of config position
			if secRule.Name == "untrusted" {
				userUntrusted = &secRule
				continue // Skip adding to rules
			}

			// Add non-default contexts
			rules = append(rules, secRule)
		}

		// Now add default "untrusted" fallback at the end
		// Merge with user customizations if provided
		if userUntrusted != nil {
			rules = append(rules, mergeRule(defaultUntrusted, *userUntrusted))
		} else {
			rules = append(rules, defaultUntrusted)
		}

		// Convert export configs to security.ExportConfig format
		exports := make([]security.ExportConfig, len(newConfig.Exports))
		for i, exportCfg := range newConfig.Exports {
			exports[i] = security.ExportConfig{
				Type: exportCfg.Type,
				Path: exportCfg.Path,
			}
		}

		config := security.ManagerConfig{
			Rules:           rules,
			Locations:       locations,
			Exports:         exports,
			CheckOnStartup:  false,
			OnContextChange: d.handleContextChange,
			Logger:          slog.Default(),
		}

		manager, err := security.NewManager(config)
		if err != nil {
			return fmt.Errorf("failed to create security manager: %w", err)
		}

		// Enable database logging BEFORE starting the manager
		if d.database != nil {
			manager.SetDatabase(d.database)
		}

		slog.Info("Starting new security manager with reloaded configuration")
		if err := manager.Start(d.ctx, false); err != nil {
			return fmt.Errorf("failed to start security manager: %w", err)
		}

		newManager = manager
		return nil
	}(); err != nil {
		// New manager failed to initialize - rollback to old config
		core.Config = oldConfig
		slog.Error("New configuration is invalid, keeping previous configuration")
		slog.Debug("Security manager init error details", "error", err)
		return fmt.Errorf("security manager init failed")
	}

	// Success! Now we can safely stop the old manager and switch to the new one
	if oldManager != nil {
		slog.Info("Stopping previous security manager")
		oldManager.Stop()
	}
	d.securityManager = newManager
	slog.Info("Switched to new security manager")

	// Trigger an immediate context check to evaluate rules and update exports
	// With the wildcard fix, rules will evaluate correctly without false triggers
	if d.securityManager != nil {
		if err := d.securityManager.TriggerCheckWithReason("config_reload"); err != nil {
			slog.Warn("Failed to check context after config reload", "error", err)
		} else {
			slog.Info("Context re-evaluated after config reload")
		}
	}

	return nil
}

// watchConfig sets up automatic config file watching
func (d *Daemon) watchConfig() {
	// Watch the config file manually using fsnotify
	kdlPath := filepath.Join(core.Config.ConfigPath, "config.kdl")

	watcher, err := fsnotify.NewWatcher()
	if err != nil {
		slog.Error("Failed to create config file watcher", "error", err)
		return
	}

	if err := watcher.Add(kdlPath); err != nil {
		slog.Error("Failed to watch config file", "error", err, "path", kdlPath)
		watcher.Close()
		return
	}

	// Set up a debounced reload handler
	var reloadTimer *time.Timer
	var reloadMutex sync.Mutex

	go func() {
		defer watcher.Close()

		for {
			select {
			case <-d.ctx.Done():
				return
			case event, ok := <-watcher.Events:
				if !ok {
					return
				}

				// Log ALL events for debugging (helps identify editor-specific behaviors)
				slog.Debug("Filesystem event on config file", "event", event.Op.String(), "file", event.Name)

				// Re-add watch after RENAME, REMOVE, or CREATE events
				// Editors using atomic writes remove the original from the watch list.
				// We may need to retry if the file doesn't exist yet during the atomic operation.
				if event.Op&(fsnotify.Rename|fsnotify.Remove|fsnotify.Create) != 0 {
					go func() {
						// Retry with exponential backoff (10ms, 20ms, 40ms, 80ms, 160ms)
						for attempt := 0; attempt < 5; attempt++ {
							if attempt > 0 {
								delay := time.Duration(10<<uint(attempt-1)) * time.Millisecond
								time.Sleep(delay)
							}

							// Remove old watch (ignore errors - it might not exist)
							watcher.Remove(kdlPath)

							// Try to add the watch
							if err := watcher.Add(kdlPath); err == nil {
								slog.Debug("Successfully re-added watch", "path", kdlPath, "attempt", attempt+1)
								return
							} else if attempt == 4 {
								// Only log error on final attempt
								slog.Error("Failed to re-add watch after multiple attempts", "error", err, "path", kdlPath)
							}
						}
					}()
				}

				// Reload on write, create, or rename events
				// Many editors use atomic rename operations instead of direct writes
				if event.Op&(fsnotify.Write|fsnotify.Create|fsnotify.Rename) == 0 {
					slog.Debug("Ignoring event (not write/create/rename)", "event", event.Op.String())
					continue
				}

				slog.Debug("Config file change detected, will reload", "event", event.Op.String(), "file", event.Name)

				reloadMutex.Lock()
				// Debounce: wait 500ms after last change before reloading
				if reloadTimer != nil {
					reloadTimer.Stop()
				}

				reloadTimer = time.AfterFunc(500*time.Millisecond, func() {
					slog.Info("Configuration file changed, reloading...", "file", event.Name)
					if err := d.reloadConfig(); err != nil {
						// Error already logged in reloadConfig() with details
						// Just log that reload failed (no need to repeat the error)
						slog.Debug("Config reload failed", "error", err)
					} else {
						slog.Info("Configuration reloaded successfully")
					}
				})
				reloadMutex.Unlock()

			case err, ok := <-watcher.Errors:
				if !ok {
					return
				}
				slog.Error("Config file watcher error", "error", err)
			}
		}
	}()

	slog.Info("Watching configuration file for changes")
}
