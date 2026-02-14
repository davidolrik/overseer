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
	"sort"
	"strconv"
	"regexp"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/fsnotify/fsnotify"
	psnet "github.com/shirou/gopsutil/v3/net"
	"overseer.olrik.dev/internal/awareness"
	"overseer.olrik.dev/internal/core"
	"overseer.olrik.dev/internal/db"
	"overseer.olrik.dev/internal/keyring"
)

// Daemon manages the SSH tunnel processes and security context.
type Daemon struct {
	tunnels       map[string]Tunnel
	askpassTokens map[string]string // Maps token -> alias for validation
	mu            sync.Mutex
	listener      net.Listener
	shutdownOnce  sync.Once
	logBroadcast  *LogBroadcaster   // For streaming logs to clients
	companionMgr  *CompanionManager // For managing companion scripts
	database      *db.DB            // Database for logging
	isRemote      bool              // Running on remote server (via SSH)
	parentMonitor *ParentMonitor    // Monitors parent process in remote mode
	ctx           context.Context   // Context for lifecycle management
	cancelFunc    context.CancelFunc
}

type TunnelState string

const (
	StateConnecting   TunnelState = "connecting"
	StateConnected    TunnelState = "connected"
	StateDisconnected TunnelState = "disconnected"
	StateReconnecting TunnelState = "reconnecting"
)

type Tunnel struct {
	Hostname            string
	Pid                 int
	Cmd                 *exec.Cmd
	StartDate           time.Time // Original tunnel creation time
	LastConnectedTime   time.Time // Time of last successful connection (for age display)
	DisconnectedTime    time.Time // Time when connection was lost (for "disconnected since" display)
	AskpassToken        string    // Token for this tunnel's askpass validation
	RetryCount          int       // Current reconnection attempt number
	TotalReconnects     int       // Total successful reconnections (stability indicator)
	LastRetryTime       time.Time
	AutoReconnect       bool        // Whether to auto-reconnect on failure
	State               TunnelState // Current connection state
	NextRetryTime       time.Time   // When the next retry will occur
	Tag                 string      // Custom SSH tag for -P argument (used with Match tagged in ssh_config)
	HealthCheckFailures int         // Consecutive health check failures (requires multiple before killing)
	ResolvedHost        string      // Actual IP:port from SSH "Authenticated to" output
}

func New() *Daemon {
	ctx, cancel := context.WithCancel(context.Background())
	d := &Daemon{
		tunnels:       make(map[string]Tunnel),
		askpassTokens: make(map[string]string),
		logBroadcast:  NewLogBroadcaster(core.Config.Companion.HistorySize),
		companionMgr:  NewCompanionManager(),
		ctx:           ctx,
		cancelFunc:    cancel,
	}
	// Set token registrar so companions can register tokens for validation
	d.companionMgr.SetTokenRegistrar(func(token, alias string) {
		d.mu.Lock()
		d.askpassTokens[token] = alias
		d.mu.Unlock()
	})
	return d
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
func mergeLocation(defaultLoc, userLoc awareness.Location) awareness.Location {
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
func mergeRule(defaultRule, userRule awareness.Rule) awareness.Rule {
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
		// - Layer 2: Platform-specific parent death signal (Linux: prctl, only for direct parent)
		// - Layer 3: Process existence polling (all platforms, works for any PID)
		//
		// When started via 'overseer start', the monitor watches the shell PID (passed via
		// OVERSEER_MONITOR_PID env var). When started via 'overseer daemon', it watches
		// the SSH session (daemon's actual parent).
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
		// Don't use defer here - we'll close it explicitly in shutdown()
		// to avoid race condition where Run() returns and closes DB before shutdown() completes
		slog.Info("Database opened", "path", dbPath)

		// Log daemon start event
		version := core.FormatVersion(core.Version)
		if err := d.database.LogDaemonEvent("start", fmt.Sprintf("daemon started - version: %s, PID: %d, remote: %v", version, os.Getpid(), d.isRemote)); err != nil {
			slog.Error("Failed to log daemon start", "error", err)
		}

		// Set event logger for companion manager
		d.companionMgr.SetEventLogger(func(alias, eventType, details string) error {
			return d.database.LogTunnelEvent(alias, eventType, details)
		})
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

	// Attempt to adopt existing tunnels from previous daemon (hot reload)
	// IMPORTANT: This must happen BEFORE initializing security manager
	// so that when the security manager evaluates context rules, it sees
	// the adopted tunnels and doesn't try to reconnect them
	adoptedTunnels := d.adoptExistingTunnels()
	adoptedCompanions := d.companionMgr.AdoptCompanions()
	if adoptedTunnels > 0 || adoptedCompanions > 0 {
		slog.Info("Hot reload complete",
			"adopted_tunnels", adoptedTunnels,
			"adopted_companions", adoptedCompanions)
	}

	// Clean up orphan SSH processes from previous daemon instances
	// This handles cases where:
	// - Previous daemon was killed without graceful shutdown
	// - State file was lost/corrupted so tunnels couldn't be adopted
	// Must happen AFTER adoption (so we keep adopted tunnels) but BEFORE
	// state orchestrator (so orphans don't block new connections)
	if orphansKilled := d.cleanOrphanTunnels(); orphansKilled > 0 {
		slog.Info("Cleaned up orphan tunnels from previous daemon", "count", orphansKilled)
	}

	// Initialize state orchestrator (new centralized state management)
	if err := d.initStateOrchestrator(); err != nil {
		slog.Error("Failed to initialize state orchestrator", "error", err)
	} else {
		slog.Info("State orchestrator started")
	}

	// Start periodic health check loop for SSH tunnels
	d.startHealthCheckLoop()

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
		if d.listener != nil {
			d.listener.Close()
		}
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
			if d.listener != nil {
				d.listener.Close()
			}
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

	// Log the command execution (skip VERSION as it's automatic, mask tokens in sensitive commands)
	if command != "VERSION" {
		logArgs := args
		// Mask tokens in commands that contain sensitive auth data
		switch command {
		case "ASKPASS":
			// ASKPASS <alias> <token> - mask token at index 1
			if len(args) >= 2 {
				logArgs = make([]string, len(args))
				copy(logArgs, args)
				logArgs[1] = "[MASKED]"
			}
		case "COMPANION_INIT":
			// COMPANION_INIT <tunnel> <name> <token> - mask token at index 2
			if len(args) >= 3 {
				logArgs = make([]string, len(args))
				copy(logArgs, args)
				logArgs[2] = "[MASKED]"
			}
		}

		if len(logArgs) > 0 {
			slog.Info(fmt.Sprintf("Executing command: %s %v", command, logArgs))
		} else {
			slog.Info(fmt.Sprintf("Executing command: %s", command))
		}
	}

	var response Response
	switch command {
	case "SSH_CONNECT":
		if len(args) > 0 {
			alias := args[0]
			var tag string

			// Parse optional tag argument (format: --tag=value)
			for _, arg := range args[1:] {
				if strings.HasPrefix(arg, "--tag=") {
					tag = strings.TrimPrefix(arg, "--tag=")
					break
				}
			}

			// Use streaming to send progress messages as they occur
			stream := NewStreamingResponse(conn)
			response = d.startTunnelStreaming(alias, tag, stream)
		}
	case "SSH_DISCONNECT":
		if len(args) > 0 {
			response = d.stopTunnel(args[0], false)
		}
	case "SSH_DISCONNECT_ALL":
		for alias := range d.tunnels {
			stopResponse := d.stopTunnel(alias, false)
			response.AddMessage(stopResponse.Messages[0].Message, stopResponse.Messages[0].Status)
		}
	case "SSH_RECONNECT":
		if len(args) > 0 {
			alias := args[0]

			// Get existing tags before stopping (to preserve them on reconnect)
			d.mu.Lock()
			var tag string
			if tunnel, exists := d.tunnels[alias]; exists {
				tag = tunnel.Tag
			}
			d.mu.Unlock()

			// Stop tunnel but preserve companions
			stopResponse := d.stopTunnel(alias, true)
			if len(stopResponse.Messages) > 0 && stopResponse.Messages[0].Status == "ERROR" {
				response = stopResponse
			} else {
				// Reconnect using streaming to show progress (with preserved tag)
				stream := NewStreamingResponse(conn)
				response = d.startTunnelStreaming(alias, tag, stream)
			}
		}
	case "RELOAD":
		// Hot reload: save tunnel, companion, and sensor state before stopping
		slog.Info("Reload command received. Saving state for hot reload...")
		if err := d.SaveTunnelState(); err != nil {
			slog.Error("Failed to save tunnel state", "error", err)
			response.AddMessage(fmt.Sprintf("Failed to save tunnel state: %v", err), "ERROR")
			conn.Write([]byte(response.ToJSON()))
			return
		}
		if err := d.companionMgr.SaveCompanionState(); err != nil {
			slog.Error("Failed to save companion state", "error", err)
			// Non-fatal - continue with reload
		}
		if err := SaveSensorState(); err != nil {
			slog.Error("Failed to save sensor state", "error", err)
			// Non-fatal - continue with reload
		}

		slog.Info("State saved successfully")
		response.AddMessage("State saved, shutting down for reload", "INFO")

		// Send response before shutting down
		conn.Write([]byte(response.ToJSON()))

		// Hot reload shutdown: minimal cleanup WITHOUT killing tunnels
		// Tunnels will survive due to Setsid and be adopted by new daemon
		slog.Info("Shutting down for hot reload (preserving tunnels)...")

		// Stop state orchestrator
		stopStateOrchestrator()

		// Cancel context to stop background tasks
		if d.cancelFunc != nil {
			d.cancelFunc()
		}

		// Log daemon stop event (but don't log tunnel disconnects - they're not disconnecting!)
		if d.database != nil {
			version := core.FormatVersion(core.Version)
			tunnelCount := len(d.tunnels)
			details := fmt.Sprintf("daemon stopped for hot reload - version: %s, PID: %d, preserved tunnels: %d", version, os.Getpid(), tunnelCount)
			if err := d.database.LogDaemonEvent("reload", details); err != nil {
				slog.Error("Failed to log daemon reload event", "error", err)
			}

			// Flush and close database
			if err := d.database.Flush(); err != nil {
				slog.Error("Failed to flush database during reload", "error", err)
			}
			if err := d.database.Close(); err != nil {
				slog.Error("Failed to close database during reload", "error", err)
			}
		}

		// Close listener to unblock Accept() loop
		if d.listener != nil {
			d.listener.Close()
		}

		// Exit WITHOUT killing tunnels/companions - they will be adopted by new daemon
		os.Exit(0)
	case "STOP":
		response = d.stopDaemon()
		// Send response before shutting down
		conn.Write([]byte(response.ToJSON()))
		// Shutdown the daemon
		slog.Info("Stop command received. Shutting down daemon.")
		d.shutdown()
		// Close listener to unblock Accept() loop and allow clean exit
		if d.listener != nil {
			d.listener.Close()
		}
		os.Exit(0) // Exit after shutdown completes
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
		// Parse optional lines count and no_history flag
		historyLines := 20 // default
		showHistory := true
		if len(args) >= 1 {
			if n, err := strconv.Atoi(args[0]); err == nil {
				historyLines = n
			}
			// Check for no_history flag (in 1st or 2nd position)
			if args[0] == "no_history" || (len(args) >= 2 && args[1] == "no_history") {
				showHistory = false
			}
		}
		d.handleLogsWithHistory(conn, showHistory, historyLines)
		return // Don't send JSON response
	case "ATTACH":
		// Stream raw slog output for debugging
		// Parse optional lines count and no_history flag
		historyLines := 20 // default
		showHistory := true
		if len(args) >= 1 {
			if n, err := strconv.Atoi(args[0]); err == nil {
				historyLines = n
			}
			// Check for no_history flag (in 1st or 2nd position)
			if args[0] == "no_history" || (len(args) >= 2 && args[1] == "no_history") {
				showHistory = false
			}
		}
		d.handleAttachWithHistory(conn, showHistory, historyLines)
		return // Don't send JSON response
	case "CONTEXT_STATUS":
		// Parse optional event limit parameter (default: 20)
		limit := 20
		if len(args) > 0 {
			if parsedLimit, err := strconv.Atoi(args[0]); err == nil && parsedLimit > 0 {
				limit = parsedLimit
			}
		}
		response = d.getContextStatus(limit)
	case "COMPANION_STATUS":
		status := d.companionMgr.GetCompanionStatus()
		response.Data = map[string]interface{}{"companions": status}
		response.AddMessage("Companion status retrieved", "INFO")
	case "COMPANION_INIT":
		// Internal command for companion wrapper to validate token and get command
		// Like ASKPASS, validates token and returns useful data
		if len(args) >= 3 {
			response = d.handleCompanionInit(args[0], args[1], args[2])
		} else {
			response.AddMessage("Usage: COMPANION_INIT <tunnel> <name> <token>", "ERROR")
		}
	case "COMPANION_ATTACH":
		if len(args) >= 2 {
			// Parse optional lines count and no_history flag
			historyLines := 20 // default
			showHistory := true
			if len(args) >= 3 {
				if n, err := strconv.Atoi(args[2]); err == nil {
					historyLines = n
				}
				// Check for no_history flag (in 3rd or 4th position)
				if args[2] == "no_history" || (len(args) >= 4 && args[3] == "no_history") {
					showHistory = false
				}
			}
			d.companionMgr.HandleCompanionAttach(conn, args[0], args[1], showHistory, historyLines)
			return // Don't send JSON response
		}
		response.AddMessage("Usage: COMPANION_ATTACH <tunnel> <name> [lines]", "ERROR")
	case "COMPANION_START":
		if len(args) >= 2 {
			// Check if tunnel is running
			d.mu.Lock()
			_, tunnelExists := d.tunnels[args[0]]
			d.mu.Unlock()
			if !tunnelExists {
				response.AddMessage(fmt.Sprintf("Tunnel '%s' is not running", args[0]), "ERROR")
			} else if err := d.companionMgr.StartSingleCompanion(args[0], args[1]); err != nil {
				response.AddMessage(fmt.Sprintf("Failed to start companion: %v", err), "ERROR")
			} else {
				response.AddMessage(fmt.Sprintf("Companion '%s' started for tunnel '%s'", args[1], args[0]), "INFO")
			}
		} else {
			response.AddMessage("Usage: COMPANION_START <tunnel> <name>", "ERROR")
		}
	case "COMPANION_STOP":
		if len(args) >= 2 {
			if err := d.companionMgr.StopSingleCompanion(args[0], args[1]); err != nil {
				response.AddMessage(fmt.Sprintf("Failed to stop companion: %v", err), "ERROR")
			} else {
				response.AddMessage(fmt.Sprintf("Companion '%s' stopped for tunnel '%s'", args[1], args[0]), "INFO")
			}
		} else {
			response.AddMessage("Usage: COMPANION_STOP <tunnel> <name>", "ERROR")
		}
	case "COMPANION_RESTART":
		if len(args) >= 2 {
			// Check if tunnel is running
			d.mu.Lock()
			_, tunnelExists := d.tunnels[args[0]]
			d.mu.Unlock()
			if !tunnelExists {
				response.AddMessage(fmt.Sprintf("Tunnel '%s' is not running", args[0]), "ERROR")
			} else if err := d.companionMgr.RestartSingleCompanion(args[0], args[1]); err != nil {
				response.AddMessage(fmt.Sprintf("Failed to restart companion: %v", err), "ERROR")
			} else {
				response.AddMessage(fmt.Sprintf("Companion '%s' restarted for tunnel '%s'", args[1], args[0]), "INFO")
			}
		} else {
			response.AddMessage("Usage: COMPANION_RESTART <tunnel> <name>", "ERROR")
		}
	default:
		response.AddMessage("Unknown command.", "ERROR")
	}
	conn.Write([]byte(response.ToJSON()))
}

// startTunnelStreaming starts a tunnel with optional streaming of progress messages.
// If stream is non-nil, progress messages are written as they occur.
// Tag is passed to SSH as a -P argument for use with Match tagged in ssh_config.
func (d *Daemon) startTunnelStreaming(alias string, tag string, stream *StreamingResponse) Response {
	// Note: We cannot use defer d.mu.Unlock() here because we need to unlock
	// early (before waiting for connection verification) and the function continues
	// to execute afterward. Using defer would cause a double-unlock panic.
	d.mu.Lock()

	response := Response{}

	// Helper to send a message - streams if available, otherwise adds to response
	sendMessage := func(message, status string) {
		if stream != nil {
			stream.WriteMessage(message, status)
		} else {
			response.AddMessage(message, status)
		}
	}

	if existingTunnel, exists := d.tunnels[alias]; exists {
		// Check if the existing tunnel process is actually still alive
		if d.checkTunnelHealth(alias, existingTunnel.Pid) {
			d.mu.Unlock()
			sendMessage(fmt.Sprintf("Tunnel '%s' is already running.", alias), "ERROR")
			return response
		}

		// Process is dead - clean up the stale entry and proceed with connection
		slog.Info("Cleaning up stale tunnel entry", "alias", alias, "old_pid", existingTunnel.Pid)
		if existingTunnel.AskpassToken != "" {
			delete(d.askpassTokens, existingTunnel.AskpassToken)
		}
		delete(d.tunnels, alias)

		// Log to database
		if d.database != nil {
			if err := d.database.LogTunnelEvent(alias, "stale_cleanup", fmt.Sprintf("Cleaned up stale tunnel entry (PID %d was dead)", existingTunnel.Pid)); err != nil {
				slog.Error("Failed to log stale cleanup", "error", err)
			}
		}
	}

	// Start or restart companion scripts before establishing SSH tunnel
	// Unlock mutex during companion startup since it may take time
	if tunnelConfig := core.Config.Tunnels[alias]; tunnelConfig != nil && len(tunnelConfig.Companions) > 0 {
		d.mu.Unlock()

		// Check if companions already exist (reconnect case)
		if d.companionMgr.HasRunningCompanions(alias) {
			// Reconnect case - restart existing companions in place to preserve attach connections
			sendMessage("Restarting companion scripts...", "INFO")
			if err := d.companionMgr.RestartCompanions(alias); err != nil {
				sendMessage(fmt.Sprintf("Failed to restart companions: %v", err), "WARN")
			}
		} else {
			// Fresh start - start new companions
			err := d.companionMgr.StartCompanions(alias, tunnelConfig.Companions, func(p CompanionProgress) {
				if p.IsError {
					sendMessage(p.Message, "WARN")
				} else {
					sendMessage(p.Message, "INFO")
				}
			})
			if err != nil {
				sendMessage(fmt.Sprintf("Companion script failed: %v", err), "ERROR")
				return response
			}
		}
		d.mu.Lock()
	}

	// Execute before_connect hooks (after companions ready, before SSH connection)
	// Order: global hooks first, then specific hooks (setup order)
	if core.Config.GlobalTunnelHooks != nil && len(core.Config.GlobalTunnelHooks.BeforeConnect) > 0 {
		d.executeTunnelHooks(alias, "before_connect", core.Config.GlobalTunnelHooks.BeforeConnect, StateConnecting)
	}
	if tunnelConfig := core.Config.Tunnels[alias]; tunnelConfig != nil && tunnelConfig.Hooks != nil && len(tunnelConfig.Hooks.BeforeConnect) > 0 {
		d.executeTunnelHooks(alias, "before_connect", tunnelConfig.Hooks.BeforeConnect, StateConnecting)
	}

	// Check if a password is stored for this alias
	hasPassword := keyring.HasPassword(alias)

	// CLI tag takes precedence, otherwise use config tag
	if tag == "" {
		if tunnelConfig := core.Config.Tunnels[alias]; tunnelConfig != nil && tunnelConfig.Tag != "" {
			tag = tunnelConfig.Tag
		}
	}

	// Create SSH command with verbose mode to detect connection status
	// Build SSH options from config
	sshArgs := []string{alias, "-N", "-o", "IgnoreUnknown=overseer-daemon", "-o", "overseer-daemon=true", "-o", "ExitOnForwardFailure=yes", "-v"}

	// Add custom tag for SSH config matching (Match tagged)
	if tag != "" {
		sshArgs = append(sshArgs, "-P", tag)
	}

	// Add ServerAliveInterval if configured (0 means disabled)
	if core.Config.SSH.ServerAliveInterval > 0 {
		sshArgs = append(sshArgs,
			"-o", fmt.Sprintf("ServerAliveInterval=%d", core.Config.SSH.ServerAliveInterval),
			"-o", fmt.Sprintf("ServerAliveCountMax=%d", core.Config.SSH.ServerAliveCountMax))
	}

	cmd := exec.Command("ssh", sshArgs...)
	cmd.Env = os.Environ()

	// Make tunnel process independent - survives daemon death (for hot reload)
	cmd.SysProcAttr = &syscall.SysProcAttr{
		Setsid: true, // Create new session, detach from parent
	}

	// Capture stderr to monitor connection status
	stderrPipe, err := cmd.StderrPipe()
	if err != nil {
		d.mu.Unlock()
		sendMessage(fmt.Sprintf("Failed to create stderr pipe: %v", err), "ERROR")
		return response
	}

	var token string
	if hasPassword {
		// Configure SSH to use overseer binary as askpass helper
		token, err = keyring.ConfigureSSHAskpass(cmd, alias)
		if err != nil {
			d.mu.Unlock()
			sendMessage(fmt.Sprintf("Failed to configure askpass: %v", err), "ERROR")
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
		sendMessage(fmt.Sprintf("Failed to launch SSH process for '%s': %v", alias, err), "ERROR")
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
		State:             StateConnecting,                  // Initial state is connecting, updated to connected after verification
		Tag:               tag,                              // Store tag for reconnection
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
		sendMessage(fmt.Sprintf("Tunnel '%s' failed to connect: %v", alias, err), "ERROR")

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

		// Stop companions (unless persistent)
		d.companionMgr.StopCompanions(alias)

		return response
	}

	// Log success in daemon
	slog.Info(fmt.Sprintf("Tunnel '%s' connected successfully (PID %d)", alias, cmd.Process.Pid))

	// Update state to connected now that verification passed
	d.mu.Lock()
	if t, exists := d.tunnels[alias]; exists {
		t.State = StateConnected
		t.LastConnectedTime = time.Now()
		d.tunnels[alias] = t
	}
	d.mu.Unlock()

	// Log to database
	if d.database != nil {
		details := fmt.Sprintf("PID: %d", cmd.Process.Pid)
		if err := d.database.LogTunnelEvent(alias, "connect", details); err != nil {
			slog.Error("Failed to log tunnel connect event", "error", err)
		}
	}

	// Trigger context check after successful SSH connection
	// Trigger state check after SSH connect
	if stateOrchestrator != nil {
		stateOrchestrator.TriggerCheck("ssh_connect")
	}

	// Execute after_connect hooks (after successful connection)
	// Order: specific hooks first, then global hooks (LIFO/cleanup order)
	if tunnelConfig := core.Config.Tunnels[alias]; tunnelConfig != nil && tunnelConfig.Hooks != nil && len(tunnelConfig.Hooks.AfterConnect) > 0 {
		d.executeTunnelHooks(alias, "after_connect", tunnelConfig.Hooks.AfterConnect, StateConnected)
	}
	if core.Config.GlobalTunnelHooks != nil && len(core.Config.GlobalTunnelHooks.AfterConnect) > 0 {
		d.executeTunnelHooks(alias, "after_connect", core.Config.GlobalTunnelHooks.AfterConnect, StateConnected)
	}

	// Send success message to client
	sendMessage(fmt.Sprintf("Tunnel '%s' connected successfully.", alias), "INFO")

	// This goroutine monitors the tunnel process and handles reconnection
	go d.monitorTunnel(alias)

	return response
}

// startTunnel starts a tunnel without streaming (used for reconnection).
func (d *Daemon) startTunnel(alias string, tag string) Response {
	return d.startTunnelStreaming(alias, tag, nil)
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

		// Check if this is still the same tunnel we were monitoring
		// (tunnel may have been replaced by a rapid disconnect/connect cycle)
		if tunnel.Cmd != cmd {
			d.mu.Unlock()
			return // Tunnel was replaced, new monitorTunnel goroutine is watching it
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
		slog.Debug("Recording tunnel disconnect event",
			"alias", alias,
			"pid", tunnel.Pid,
			"exit_details", exitDetails,
			"database_available", d.database != nil)
		if d.database != nil {
			if err := d.database.LogTunnelEvent(alias, "disconnect", exitDetails); err != nil {
				slog.Error("Failed to log tunnel disconnect", "error", err)
			}
		}

		// Update state to disconnected
		tunnel.State = StateDisconnected
		tunnel.DisconnectedTime = time.Now()
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

		// Wait for wake grace period if suppressed (just woke from sleep)
		if orch := GetStateOrchestrator(); orch != nil && orch.IsSuppressed() {
			slog.Info(fmt.Sprintf("Tunnel '%s' waiting for wake grace period", alias))
			d.mu.Unlock()
			// Wait until no longer suppressed (check every second)
			for orch.IsSuppressed() {
				time.Sleep(1 * time.Second)
			}
			d.mu.Lock()
			// Re-check tunnel still exists
			tunnel, exists = d.tunnels[alias]
			if !exists {
				d.mu.Unlock()
				return
			}
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
		sshArgs := []string{alias, "-N", "-o", "IgnoreUnknown=overseer-daemon", "-o", "overseer-daemon=true", "-o", "ExitOnForwardFailure=yes", "-v"}

		// Add custom tag (preserved from original connection)
		if tunnel.Tag != "" {
			sshArgs = append(sshArgs, "-P", tunnel.Tag)
		}

		// Add ServerAliveInterval if configured (0 means disabled)
		if core.Config.SSH.ServerAliveInterval > 0 {
			sshArgs = append(sshArgs,
				"-o", fmt.Sprintf("ServerAliveInterval=%d", core.Config.SSH.ServerAliveInterval),
				"-o", fmt.Sprintf("ServerAliveCountMax=%d", core.Config.SSH.ServerAliveCountMax))
		}

		newCmd := exec.Command("ssh", sshArgs...)
		newCmd.Env = os.Environ()

		// Make tunnel process independent - survives daemon death (for hot reload)
		newCmd.SysProcAttr = &syscall.SysProcAttr{
			Setsid: true, // Create new session, detach from parent
		}

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
		// Trigger state check after SSH reconnect
		if stateOrchestrator != nil {
			stateOrchestrator.TriggerCheck("ssh_reconnect")
		}

		// Continue monitoring this tunnel (loop back to Wait())
	}
}

// verifyConnection monitors SSH stderr output to detect connection success or failure
var authenticatedToRe = regexp.MustCompile(`Authenticated to \S+ \(\[([^\]]+)\]:(\d+)\)`)

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
	verified := false

	for scanner.Scan() {
		line := scanner.Text()
		slog.Debug(fmt.Sprintf("[%s] SSH: %s", alias, line))

		// After verification, keep reading to drain stderr and prevent pipe buffer deadlock.
		// If we stop reading, SSH's stderr pipe buffer fills up (~64KB) and the SSH process
		// blocks on write(), freezing the tunnel and all multiplexed connections.
		if verified {
			continue
		}

		// Track authentication completion
		if strings.Contains(line, "Authentication succeeded") ||
			strings.Contains(line, "Authenticated to") {
			authenticated = true
			if matches := authenticatedToRe.FindStringSubmatch(line); len(matches) == 3 {
				resolvedHost := matches[1] + ":" + matches[2]
				d.mu.Lock()
				if t, exists := d.tunnels[alias]; exists {
					t.ResolvedHost = resolvedHost
					d.tunnels[alias] = t
				}
				d.mu.Unlock()
			}
			// Don't return yet - we need to wait for the session to be established
		}

		// Look for success indicators - session fully established
		// For -N (no command), look for "pledge: network" or "Entering interactive session"
		if authenticated && (strings.Contains(line, "Entering interactive session") ||
			strings.Contains(line, "pledge: network")) {
			result <- nil
			verified = true
			continue
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

	if err := scanner.Err(); err != nil {
		slog.Debug(fmt.Sprintf("[%s] Error reading SSH output: %v", alias, err))
	}
}

// gracefulTerminate sends SIGTERM first, waits for graceful exit, then falls back to SIGKILL.
// Returns nil if process terminated gracefully, or the kill error if force kill was needed.
// Note: Uses Signal(0) polling instead of Wait() because Wait() only works for child processes,
// and our SSH tunnels run in separate sessions (Setsid: true) which may not be direct children.
func gracefulTerminate(process *os.Process, timeout time.Duration, label string) error {
	// Send SIGTERM for graceful shutdown
	if err := process.Signal(syscall.SIGTERM); err != nil {
		// Process may have already exited
		if err == os.ErrProcessDone {
			return nil
		}
		// Fall back to kill if we can't send SIGTERM
		slog.Warn(fmt.Sprintf("Failed to send SIGTERM to %s, forcing kill", label), "error", err)
		return process.Kill()
	}

	// Poll for process death using Signal(0) instead of Wait()
	// Wait() only works for direct child processes, but our SSH processes
	// run in separate sessions (Setsid: true) and may be orphaned/adopted
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if err := process.Signal(syscall.Signal(0)); err != nil {
			// Process is dead (ESRCH or similar)
			slog.Info(fmt.Sprintf("Process %s terminated gracefully", label))
			return nil
		}
		time.Sleep(100 * time.Millisecond)
	}

	// Timeout - force kill
	slog.Warn(fmt.Sprintf("Process %s did not exit within %v, forcing kill", label, timeout))
	if err := process.Kill(); err != nil {
		if err == os.ErrProcessDone {
			return nil
		}
		return err
	}

	// Verify kill succeeded
	time.Sleep(100 * time.Millisecond)
	if err := process.Signal(syscall.Signal(0)); err == nil {
		slog.Error(fmt.Sprintf("Process %s survived SIGKILL", label))
		return fmt.Errorf("process survived SIGKILL")
	}

	return nil
}

func (d *Daemon) stopTunnel(alias string, forReconnect bool) Response {
	d.mu.Lock()
	defer d.mu.Unlock()

	response := Response{}

	tunnel, exists := d.tunnels[alias]
	if !exists {
		response.AddMessage(fmt.Sprintf("Tunnel '%s' is not running.", alias), "ERROR")
		return response
	}

	// Gracefully terminate the tunnel process - handle both normal and adopted tunnels
	const gracefulTimeout = 5 * time.Second
	var killErr error
	if tunnel.Cmd != nil && tunnel.Cmd.Process != nil {
		// Normal tunnel spawned by this daemon
		killErr = gracefulTerminate(tunnel.Cmd.Process, gracefulTimeout, alias)
	} else if tunnel.Pid > 0 {
		// Adopted tunnel from hot reload - terminate by PID
		process, err := os.FindProcess(tunnel.Pid)
		if err != nil {
			killErr = err
		} else {
			killErr = gracefulTerminate(process, gracefulTimeout, alias)
		}
	} else {
		killErr = fmt.Errorf("tunnel has no process reference")
	}

	if killErr != nil {
		// Even if killing fails, we should clean up the map and token
		if tunnel.AskpassToken != "" {
			delete(d.askpassTokens, tunnel.AskpassToken)
		}
		delete(d.tunnels, alias)
		response.AddMessage(fmt.Sprintf("Failed to kill process for '%s': %v", alias, killErr), "ERROR")
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
		if err := d.database.LogTunnelEvent(alias, "manual_disconnect", ""); err != nil {
			slog.Error("Failed to log tunnel manual stop", "error", err)
		}
	}

	// Stop companion scripts unless this is for a reconnect
	if !forReconnect {
		// Permanent stop - stop companions
		d.companionMgr.StopCompanions(alias)
	} else {
		// For reconnect, companions stay in the map but clear history
		// to prevent showing stale output on reattach
		d.companionMgr.ClearCompanionHistory(alias)
	}

	response.AddMessage(fmt.Sprintf("Tunnel process for '%s' stopped.", alias), "INFO")
	return response
}

type DaemonStatus struct {
	Hostname          string      `json:"hostname"`
	Pid               int         `json:"pid"`
	StartDate         string      `json:"start_date"`                  // Original tunnel creation time
	LastConnectedTime string      `json:"last_connected_time"`         // Time of last successful connection
	DisconnectedTime  string      `json:"disconnected_time,omitempty"` // Time when connection was lost
	RetryCount        int         `json:"retry_count,omitempty"`
	TotalReconnects   int         `json:"total_reconnects"` // Total successful reconnections
	AutoReconnect     bool        `json:"auto_reconnect"`
	State             TunnelState `json:"state"`
	NextRetry         string      `json:"next_retry,omitempty"` // ISO 8601 format
	Tag               string      `json:"tag,omitempty"`
	ResolvedHost      string      `json:"resolved_host,omitempty"`
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
			Tag:               tunnel.Tag,
			ResolvedHost:      tunnel.ResolvedHost,
		}

		// Add disconnected time if tunnel is disconnected or reconnecting
		if (tunnel.State == StateDisconnected || tunnel.State == StateReconnecting) && !tunnel.DisconnectedTime.IsZero() {
			status.DisconnectedTime = tunnel.DisconnectedTime.Format(time.RFC3339)
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

	// Return the daemon version and mode information
	response.AddMessage("OK", "INFO")
	data := map[string]interface{}{
		"version":   core.Version,
		"is_remote": d.isRemote,
		"pid":       os.Getpid(),
	}

	// Add monitored PID if in remote mode
	if d.isRemote && d.parentMonitor != nil {
		data["monitored_pid"] = d.parentMonitor.monitoredPID
	}

	response.AddData(data)

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

// handleCompanionInit validates the token and returns the command to execute
// This is like ASKPASS - validates token and returns useful data in one call
func (d *Daemon) handleCompanionInit(alias, name, token string) Response {
	d.mu.Lock()
	defer d.mu.Unlock()

	response := Response{}

	// Validate token matches the stored alias
	storedAlias, exists := d.askpassTokens[token]
	if !exists || storedAlias != alias {
		response.AddMessage("Invalid token", "ERROR")
		return response
	}

	// Look up the companion command from config
	tunnelConfig, exists := core.Config.Tunnels[alias]
	if !exists {
		response.AddMessage(fmt.Sprintf("Tunnel '%s' not found in config", alias), "ERROR")
		return response
	}

	var command string
	for _, comp := range tunnelConfig.Companions {
		if comp.Name == name {
			command = comp.Command
			break
		}
	}

	if command == "" {
		response.AddMessage(fmt.Sprintf("Companion '%s' not found in tunnel '%s'", name, alias), "ERROR")
		return response
	}

	// Return the command to execute
	response.AddMessage(command, "INFO")
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

		// Stop state orchestrator
		stopStateOrchestrator()

		// Stop all companion scripts
		slog.Debug("Stopping all companion scripts...")
		d.companionMgr.StopAllCompanions()

		// Cancel context to stop all background tasks
		if d.cancelFunc != nil {
			d.cancelFunc()
		}

		d.mu.Lock()
		defer d.mu.Unlock()

		// Disconnect and kill all tunnels first
		tunnelCount := len(d.tunnels)
		for alias, tunnel := range d.tunnels {
			// Log disconnect event before killing
			if d.database != nil {
				slog.Debug("Logging disconnect event for tunnel during shutdown", "alias", alias)
				if err := d.database.LogTunnelEvent(alias, "disconnect", "Daemon shutdown"); err != nil {
					slog.Error("Failed to log tunnel disconnect during shutdown", "error", err, "alias", alias)
				} else {
					slog.Debug("Successfully logged disconnect event", "alias", alias)
				}
			}

			// Gracefully terminate the tunnel process
			// Handle both normal tunnels (with Cmd) and adopted tunnels (PID only)
			const shutdownTimeout = 5 * time.Second
			if tunnel.Cmd != nil && tunnel.Cmd.Process != nil {
				// Normal tunnel spawned by this daemon
				pid := tunnel.Cmd.Process.Pid
				slog.Debug("Terminating tunnel process", "alias", alias, "pid", pid)
				if err := gracefulTerminate(tunnel.Cmd.Process, shutdownTimeout, alias); err != nil {
					slog.Error("Failed to terminate tunnel process", "error", err, "alias", alias, "pid", pid)
				}
			} else if tunnel.Pid > 0 {
				// Adopted tunnel from hot reload - terminate by PID
				pid := tunnel.Pid
				slog.Debug("Terminating adopted tunnel process", "alias", alias, "pid", pid)
				process, err := os.FindProcess(pid)
				if err != nil {
					slog.Error("Failed to find tunnel process", "error", err, "alias", alias, "pid", pid)
				} else {
					if err := gracefulTerminate(process, shutdownTimeout, alias); err != nil {
						slog.Error("Failed to terminate adopted tunnel process", "error", err, "alias", alias, "pid", pid)
					}
				}
			} else {
				slog.Warn("Tunnel has no process reference, cannot terminate", "alias", alias)
			}
		}

		// Log daemon stop event as the final event after all tunnels are disconnected
		if d.database != nil {
			version := core.FormatVersion(core.Version)
			details := fmt.Sprintf("daemon stopped - version: %s, PID: %d, remote: %v, active tunnels: %d", version, os.Getpid(), d.isRemote, tunnelCount)
			if err := d.database.LogDaemonEvent("stop", details); err != nil {
				slog.Error("Failed to log daemon stop event", "error", err)
			}
		}

		// Flush database to ensure all events are written before daemon exits
		if d.database != nil {
			if err := d.database.Flush(); err != nil {
				slog.Error("Failed to flush database during shutdown", "error", err)
			}
		}

		// Close database after all logging is complete
		if d.database != nil {
			if err := d.database.Close(); err != nil {
				slog.Error("Failed to close database during shutdown", "error", err)
			} else {
				slog.Info("Database closed successfully")
			}
		}

		d.tunnels = make(map[string]Tunnel)
	})
}

// checkOnlineStatus checks if we're currently online
func (d *Daemon) checkOnlineStatus() bool {
	if stateOrchestrator != nil {
		return stateOrchestrator.IsOnline()
	}
	return false
}

// hasEstablishedTCPConnection checks if a process has any ESTABLISHED TCP connections
// This is used to verify that SSH connections are actually alive, not just that the process exists
func hasEstablishedTCPConnection(pid int) bool {
	conns, err := psnet.ConnectionsPid("tcp", int32(pid))
	if err != nil {
		slog.Debug("Failed to get connections for PID", "pid", pid, "error", err)
		return false
	}

	slog.Debug("TCP connection check", "pid", pid, "total_connections", len(conns))

	for _, conn := range conns {
		slog.Debug("TCP connection detail",
			"pid", pid,
			"status", conn.Status,
			"local", fmt.Sprintf("%s:%d", conn.Laddr.IP, conn.Laddr.Port),
			"remote", fmt.Sprintf("%s:%d", conn.Raddr.IP, conn.Raddr.Port))
		if conn.Status == "ESTABLISHED" {
			return true
		}
	}

	if len(conns) == 0 {
		slog.Debug("No TCP connections found for PID (lsof may have returned empty)", "pid", pid)
	}

	return false
}

// checkTunnelHealth verifies that a tunnel's SSH connection is actually alive
// Returns true if the connection is healthy, false if it should be considered dead
func (d *Daemon) checkTunnelHealth(alias string, pid int) bool {
	// First check if the process exists
	process, err := os.FindProcess(pid)
	if err != nil {
		return false
	}
	if err := process.Signal(syscall.Signal(0)); err != nil {
		return false
	}

	// Process exists, now check if it has an established TCP connection
	return hasEstablishedTCPConnection(pid)
}

// checkAllTunnelHealth checks all tunnels and marks dead ones for reconnection
// This is called periodically and on network state changes
func (d *Daemon) checkAllTunnelHealth(reason string) {
	const (
		minConnectionAge      = 60 * time.Second // Skip health checks for recently connected tunnels
		requiredFailures      = 2                // Require consecutive failures before killing
	)

	d.mu.Lock()
	type tunnelCheck struct {
		pid               int
		lastConnectedTime time.Time
		failures          int
	}
	tunnelsToCheck := make(map[string]tunnelCheck) // alias -> check info
	for alias, tunnel := range d.tunnels {
		if tunnel.State == StateConnected {
			tunnelsToCheck[alias] = tunnelCheck{
				pid:               tunnel.Pid,
				lastConnectedTime: tunnel.LastConnectedTime,
				failures:          tunnel.HealthCheckFailures,
			}
		}
	}
	d.mu.Unlock()

	if len(tunnelsToCheck) == 0 {
		return
	}

	slog.Debug("Checking tunnel health", "reason", reason, "tunnel_count", len(tunnelsToCheck))

	for alias, check := range tunnelsToCheck {
		// Skip health checks for recently connected tunnels
		connectionAge := time.Since(check.lastConnectedTime)
		if connectionAge < minConnectionAge {
			slog.Debug("Skipping health check for recently connected tunnel",
				"alias", alias,
				"age", connectionAge.Round(time.Second),
				"min_age", minConnectionAge)
			continue
		}

		if !d.checkTunnelHealth(alias, check.pid) {
			// Increment failure count
			d.mu.Lock()
			tunnel, exists := d.tunnels[alias]
			if !exists || tunnel.Pid != check.pid {
				d.mu.Unlock()
				continue // Tunnel was removed or replaced
			}
			tunnel.HealthCheckFailures++
			failures := tunnel.HealthCheckFailures
			d.tunnels[alias] = tunnel
			d.mu.Unlock()

			slog.Warn("Tunnel health check failed",
				"alias", alias,
				"pid", check.pid,
				"consecutive_failures", failures,
				"required_failures", requiredFailures,
				"reason", reason)

			if failures < requiredFailures {
				slog.Info("Not killing tunnel yet, waiting for more consecutive failures",
					"alias", alias,
					"failures", failures,
					"required", requiredFailures)
				continue
			}

			slog.Warn("Tunnel connection is dead, killing process to trigger reconnection",
				"alias", alias,
				"pid", check.pid,
				"consecutive_failures", failures,
				"reason", reason)

			// Log to database
			if d.database != nil {
				details := fmt.Sprintf("Health check failed (%s), %d consecutive failures, killing PID %d", reason, failures, check.pid)
				if err := d.database.LogTunnelEvent(alias, "health_check_failed", details); err != nil {
					slog.Error("Failed to log health check failure", "error", err)
				}
			}

			// Kill the SSH process - the monitor goroutine will handle reconnection
			process, err := os.FindProcess(check.pid)
			if err == nil {
				process.Kill()
			}
		} else {
			// Health check passed - reset failure count if it was non-zero
			d.mu.Lock()
			tunnel, exists := d.tunnels[alias]
			if exists && tunnel.Pid == check.pid && tunnel.HealthCheckFailures > 0 {
				slog.Debug("Tunnel health check passed, resetting failure count",
					"alias", alias,
					"previous_failures", tunnel.HealthCheckFailures)
				tunnel.HealthCheckFailures = 0
				d.tunnels[alias] = tunnel
			}
			d.mu.Unlock()
		}
	}
}

// startHealthCheckLoop starts a goroutine that periodically checks tunnel health
func (d *Daemon) startHealthCheckLoop() {
	go func() {
		ticker := time.NewTicker(30 * time.Second)
		defer ticker.Stop()

		for {
			select {
			case <-d.ctx.Done():
				return
			case <-ticker.C:
				d.checkAllTunnelHealth("periodic_check")
			}
		}
	}()
	slog.Info("Started tunnel health check loop", "interval", "30s")
}

// handleOnlineChange is called when the online sensor changes state
// This triggers health checks when coming back online to detect dead SSH connections
func (d *Daemon) handleOnlineChange(wasOnline, isOnline bool) {
	slog.Info("Online status changed", "was_online", wasOnline, "is_online", isOnline)

	// When coming back online, check all tunnel health immediately
	// This catches SSH connections that died during the offline period
	if isOnline && !wasOnline {
		slog.Info("Back online, checking tunnel health")
		// Small delay to let network stabilize
		time.Sleep(2 * time.Second)
		d.checkAllTunnelHealth("back_online")
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
	SensorChanges []SensorChangeInfo  `json:"sensor_changes,omitempty"`
	TunnelEvents  []TunnelEventInfo   `json:"tunnel_events,omitempty"`
	DaemonEvents  []DaemonEventInfo   `json:"daemon_events,omitempty"`
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

// SensorChangeInfo represents a sensor state change
type SensorChangeInfo struct {
	SensorName string `json:"sensor_name"`
	SensorType string `json:"sensor_type"`
	OldValue   string `json:"old_value"`
	NewValue   string `json:"new_value"`
	Timestamp  string `json:"timestamp"`
}

// TunnelEventInfo represents a tunnel lifecycle event
type TunnelEventInfo struct {
	TunnelAlias string `json:"tunnel_alias"`
	EventType   string `json:"event_type"`
	Details     string `json:"details,omitempty"`
	Timestamp   string `json:"timestamp"`
}

// DaemonEventInfo represents a daemon lifecycle event
type DaemonEventInfo struct {
	EventType string `json:"event_type"`
	Details   string `json:"details,omitempty"`
	Timestamp string `json:"timestamp"`
}

// getContextStatus returns the current security context status
func (d *Daemon) getContextStatus(eventLimit int) Response {
	response := Response{}

	// Check if state orchestrator is initialized
	if stateOrchestrator == nil {
		response.AddMessage("State orchestrator not initialized", "ERROR")
		return response
	}

	// Get current state
	currentState := stateOrchestrator.GetCurrentState()

	// Build sensor map from state
	sensors := make(map[string]string)
	sensors["online"] = fmt.Sprintf("%v", currentState.Online)
	sensors["context"] = currentState.Context
	sensors["location"] = currentState.Location
	if currentState.PublicIPv4 != nil {
		sensors["public_ipv4"] = currentState.PublicIPv4.String()
	}
	if currentState.PublicIPv6 != nil {
		sensors["public_ipv6"] = currentState.PublicIPv6.String()
	}
	if currentState.LocalIPv4 != nil {
		sensors["local_ipv4"] = currentState.LocalIPv4.String()
	}

	// Change history is no longer maintained in-memory
	// It can be retrieved from the database if needed
	var changeHistory []ContextChangeInfo

	// Get recent sensor changes, tunnel events, and daemon events from database
	// Fetch eventLimit of each type, then combine and limit to eventLimit total
	var sensorChanges []SensorChangeInfo
	var tunnelEvents []TunnelEventInfo
	var daemonEvents []DaemonEventInfo

	if d.database != nil {
		// Fetch sensor changes
		dbSensorChanges, err := d.database.GetRecentSensorChanges(eventLimit)
		if err != nil {
			slog.Warn("Failed to fetch sensor changes from database", "error", err)
		} else {
			sensorChanges = make([]SensorChangeInfo, 0, len(dbSensorChanges))
			for _, sc := range dbSensorChanges {
				sensorChanges = append(sensorChanges, SensorChangeInfo{
					SensorName: sc.SensorName,
					SensorType: sc.SensorType,
					OldValue:   sc.OldValue,
					NewValue:   sc.NewValue,
					Timestamp:  sc.Timestamp.Format(time.RFC3339Nano),
				})
			}
		}

		// Fetch tunnel events
		dbTunnelEvents, err := d.database.GetRecentTunnelEvents(eventLimit)
		if err != nil {
			slog.Warn("Failed to fetch tunnel events from database", "error", err)
		} else {
			tunnelEvents = make([]TunnelEventInfo, 0, len(dbTunnelEvents))
			for _, te := range dbTunnelEvents {
				tunnelEvents = append(tunnelEvents, TunnelEventInfo{
					TunnelAlias: te.TunnelAlias,
					EventType:   te.EventType,
					Details:     te.Details,
					Timestamp:   te.Timestamp.Format(time.RFC3339Nano),
				})
			}
		}

		// Fetch daemon events
		dbDaemonEvents, err := d.database.GetRecentDaemonEvents(eventLimit)
		if err != nil {
			slog.Warn("Failed to fetch daemon events from database", "error", err)
		} else {
			daemonEvents = make([]DaemonEventInfo, 0, len(dbDaemonEvents))
			for _, de := range dbDaemonEvents {
				daemonEvents = append(daemonEvents, DaemonEventInfo{
					EventType: de.EventType,
					Details:   de.Details,
					Timestamp: de.Timestamp.Format(time.RFC3339Nano),
				})
			}
		}

		// Combine events and sort by timestamp to get the most recent eventLimit events
		type combinedEvent struct {
			timestamp  time.Time
			sensorInfo *SensorChangeInfo
			tunnelInfo *TunnelEventInfo
			daemonInfo *DaemonEventInfo
		}
		combined := make([]combinedEvent, 0, len(sensorChanges)+len(tunnelEvents)+len(daemonEvents))

		for i := range sensorChanges {
			ts, err := time.Parse(time.RFC3339Nano, sensorChanges[i].Timestamp)
			if err != nil {
				continue
			}
			combined = append(combined, combinedEvent{
				timestamp:  ts,
				sensorInfo: &sensorChanges[i],
			})
		}

		for i := range tunnelEvents {
			ts, err := time.Parse(time.RFC3339Nano, tunnelEvents[i].Timestamp)
			if err != nil {
				continue
			}
			combined = append(combined, combinedEvent{
				timestamp:  ts,
				tunnelInfo: &tunnelEvents[i],
			})
		}

		for i := range daemonEvents {
			ts, err := time.Parse(time.RFC3339Nano, daemonEvents[i].Timestamp)
			if err != nil {
				continue
			}
			combined = append(combined, combinedEvent{
				timestamp:  ts,
				daemonInfo: &daemonEvents[i],
			})
		}

		// Sort by timestamp (most recent first)
		sort.Slice(combined, func(i, j int) bool {
			return combined[i].timestamp.After(combined[j].timestamp)
		})

		// Limit to eventLimit total events
		if len(combined) > eventLimit {
			combined = combined[:eventLimit]
		}

		// Split back into separate slices
		sensorChanges = make([]SensorChangeInfo, 0, eventLimit)
		tunnelEvents = make([]TunnelEventInfo, 0, eventLimit)
		daemonEvents = make([]DaemonEventInfo, 0, eventLimit)
		for _, e := range combined {
			if e.sensorInfo != nil {
				sensorChanges = append(sensorChanges, *e.sensorInfo)
			}
			if e.tunnelInfo != nil {
				tunnelEvents = append(tunnelEvents, *e.tunnelInfo)
			}
			if e.daemonInfo != nil {
				daemonEvents = append(daemonEvents, *e.daemonInfo)
			}
		}
	}

	// Build status - prefer display names, fallback to internal names
	contextName := currentState.ContextDisplayName
	if contextName == "" {
		contextName = currentState.Context
	}
	locationName := currentState.LocationDisplayName
	if locationName == "" {
		locationName = currentState.Location
	}

	status := ContextStatus{
		Context:       contextName,
		Location:      locationName,
		LastChange:    currentState.Timestamp.Format(time.RFC3339),
		Uptime:        time.Since(currentState.Timestamp).Round(time.Second).String(),
		Sensors:       sensors,
		ChangeHistory: changeHistory,
		SensorChanges: sensorChanges,
		TunnelEvents:  tunnelEvents,
		DaemonEvents:  daemonEvents,
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

// reloadConfig reloads the configuration and restarts the state orchestrator
func (d *Daemon) reloadConfig() error {
	// Save the old config in case we need to roll back
	oldConfig := core.Config

	// Reload the configuration from the KDL file
	configPath := filepath.Join(core.Config.ConfigPath, "config.hcl")
	newConfig, err := core.LoadConfig(configPath)
	if err != nil {
		// Config parsing failed - keep the old config and log error
		errMsg := err.Error()
		errMsg = strings.TrimPrefix(errMsg, "failed to unmarshal KDL: parse failed: ")
		errMsg = strings.TrimPrefix(errMsg, "failed to unmarshal KDL: scan failed: ")

		if idx := strings.Index(errMsg, ":\n"); idx != -1 {
			errMsg = errMsg[:idx]
		}

		slog.Error("Configuration file has syntax errors, keeping previous configuration",
			"file", configPath,
			"error", errMsg)
		return fmt.Errorf("config parse error")
	}

	// Preserve the config path
	newConfig.ConfigPath = oldConfig.ConfigPath

	// Update the global config
	core.Config = newConfig

	// Reload the state orchestrator with new config
	if err := d.reloadStateOrchestrator(); err != nil {
		// Rollback to old config
		core.Config = oldConfig
		slog.Error("Failed to reload state orchestrator", "error", err)
		return fmt.Errorf("state orchestrator reload failed")
	}

	slog.Info("Configuration reloaded successfully")
	return nil
}

// watchConfig sets up automatic config file watching
func (d *Daemon) watchConfig() {
	// Watch the config file manually using fsnotify
	configPath := filepath.Join(core.Config.ConfigPath, "config.hcl")

	watcher, err := fsnotify.NewWatcher()
	if err != nil {
		slog.Error("Failed to create config file watcher", "error", err)
		return
	}

	if err := watcher.Add(configPath); err != nil {
		slog.Error("Failed to watch config file", "error", err, "path", configPath)
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
							watcher.Remove(configPath)

							// Try to add the watch
							if err := watcher.Add(configPath); err == nil {
								slog.Debug("Successfully re-added watch", "path", configPath, "attempt", attempt+1)
								return
							} else if attempt == 4 {
								// Only log error on final attempt
								slog.Error("Failed to re-add watch after multiple attempts", "error", err, "path", configPath)
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

// cleanOrphanTunnels finds and kills SSH processes from previous daemon instances
// that weren't properly cleaned up. This can happen if:
// - The daemon was killed with SIGKILL (no graceful shutdown)
// - The tunnel state file was lost or corrupted
// - A race condition during shutdown left processes running
// Returns the number of orphan processes killed
func (d *Daemon) cleanOrphanTunnels() int {
	// Find SSH processes that were started by overseer
	// They'll have the tag "overseer daemon" in their command line
	pids, err := findOverseerSSHProcesses()
	if err != nil {
		slog.Warn("Failed to search for orphan SSH processes", "error", err)
		return 0
	}

	if len(pids) == 0 {
		slog.Debug("No orphan SSH processes found")
		return 0
	}

	slog.Info("Found SSH processes with overseer tag, checking for orphans", "count", len(pids))

	// For each found process, check if it's in our tunnel map
	// If not, it's an orphan and should be killed
	d.mu.Lock()
	defer d.mu.Unlock()

	killedCount := 0
	for _, pid := range pids {
		// Check if this PID belongs to any tunnel we're tracking
		isTracked := false
		for _, tunnel := range d.tunnels {
			tunnelPid := tunnel.Pid
			if tunnel.Cmd != nil && tunnel.Cmd.Process != nil {
				tunnelPid = tunnel.Cmd.Process.Pid
			}
			if tunnelPid == pid {
				isTracked = true
				break
			}
		}

		if !isTracked {
			// This is an orphan process - kill it
			slog.Warn("Found orphan SSH process, killing", "pid", pid)
			process, err := os.FindProcess(pid)
			if err != nil {
				slog.Error("Failed to find orphan process", "pid", pid, "error", err)
				continue
			}

			// Try graceful termination first, then force kill
			if err := gracefulTerminate(process, 2*time.Second, fmt.Sprintf("orphan-pid-%d", pid)); err != nil {
				slog.Error("Failed to kill orphan process", "pid", pid, "error", err)
			} else {
				killedCount++
				slog.Info("Killed orphan SSH process", "pid", pid)

				// Log to database
				if d.database != nil {
					if err := d.database.LogTunnelEvent("_orphan", "orphan_killed", fmt.Sprintf("Killed orphan SSH process with PID %d", pid)); err != nil {
						slog.Error("Failed to log orphan kill event", "error", err)
					}
				}
			}
		}
	}

	if killedCount > 0 {
		slog.Info("Orphan tunnel cleanup complete", "killed", killedCount)
	}

	return killedCount
}

// findOverseerSSHProcesses finds SSH processes started by overseer daemon
// by looking for processes with "overseer-daemon" in their command line
func findOverseerSSHProcesses() ([]int, error) {
	// Use pgrep to find SSH processes with our marker
	// -f: match against full command line
	// The marker "overseer-daemon" is added via SSH's -o option
	cmd := exec.Command("pgrep", "-f", "overseer-daemon")
	output, err := cmd.Output()
	if err != nil {
		// pgrep returns exit code 1 when no processes found - that's OK
		if exitErr, ok := err.(*exec.ExitError); ok {
			if exitErr.ExitCode() == 1 {
				return nil, nil // No matching processes
			}
		}
		return nil, fmt.Errorf("pgrep failed: %w", err)
	}

	// Parse PIDs from output (one per line)
	lines := strings.Split(strings.TrimSpace(string(output)), "\n")
	pids := make([]int, 0, len(lines))
	myPID := os.Getpid()
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		pid, err := strconv.Atoi(line)
		if err != nil {
			slog.Warn("Failed to parse PID from pgrep output", "line", line, "error", err)
			continue
		}
		// Skip our own PID — pgrep -f "overseer-daemon" matches the daemon itself
		// since its cmdline contains "--overseer-daemon"
		if pid == myPID {
			continue
		}
		pids = append(pids, pid)
	}

	return pids, nil
}

// adoptExistingTunnels attempts to adopt tunnel processes from a previous daemon instance
// This enables hot reload - daemon can restart without killing active SSH tunnels
// Returns the number of successfully adopted tunnels
func (d *Daemon) adoptExistingTunnels() int {
	// Load tunnel state from previous daemon
	state, err := LoadTunnelState()
	if err != nil {
		slog.Warn("Failed to load tunnel state", "error", err)
		return 0
	}

	if state == nil {
		slog.Debug("No tunnel state file found (first run or clean restart)")
		return 0
	}

	slog.Info("Found tunnel state from previous daemon",
		"tunnel_count", len(state.Tunnels),
		"state_timestamp", state.Timestamp)

	adoptedCount := 0

	for _, info := range state.Tunnels {
		if d.adoptTunnel(info) {
			adoptedCount++
		}
	}

	if adoptedCount > 0 {
		slog.Info("Successfully adopted tunnels",
			"adopted", adoptedCount,
			"total", len(state.Tunnels))
	} else if len(state.Tunnels) > 0 {
		slog.Warn("Failed to adopt any tunnels, will reconnect via security rules")
	}

	// Clean up state file after processing
	if err := RemoveTunnelStateFile(); err != nil {
		slog.Warn("Failed to remove tunnel state file", "error", err)
	}

	return adoptedCount
}

// adoptTunnel attempts to adopt a single tunnel process
// Returns true if adoption succeeded, false otherwise
func (d *Daemon) adoptTunnel(info TunnelInfo) bool {
	// Validate that the process still exists and is the expected SSH tunnel
	if !ValidateTunnelProcess(info) {
		slog.Warn("Tunnel process validation failed, will not adopt",
			"alias", info.Alias,
			"pid", info.PID)
		return false
	}

	d.mu.Lock()
	defer d.mu.Unlock()

	// Check if we already have a tunnel with this alias
	if _, exists := d.tunnels[info.Alias]; exists {
		slog.Warn("Tunnel alias already exists, skipping adoption",
			"alias", info.Alias,
			"existing_pid", d.tunnels[info.Alias].Pid,
			"adopt_pid", info.PID)
		return false
	}

	// Create tunnel entry
	// Note: Cmd will be nil for adopted tunnels - we can't recreate exec.Cmd
	// We'll monitor via PID polling instead
	// AskpassToken is empty for adopted tunnels - they're already authenticated and running
	tunnel := Tunnel{
		Hostname:          info.Hostname,
		Pid:               info.PID,
		Cmd:               nil, // Can't recreate exec.Cmd from PID
		StartDate:         info.StartDate,
		LastConnectedTime: info.LastConnectedTime,
		AskpassToken:      "", // Adopted tunnels don't need auth - already running
		RetryCount:        info.RetryCount,
		TotalReconnects:   info.TotalReconnects,
		AutoReconnect:     info.AutoReconnect,
		State:             TunnelState(info.State),
		Tag:               info.Tag,
		ResolvedHost:      info.ResolvedHost,
	}

	d.tunnels[info.Alias] = tunnel

	// Start monitoring goroutine for adopted tunnel
	// Since we don't have exec.Cmd, we poll the process instead
	go d.monitorAdoptedTunnel(info.Alias, info.PID)

	slog.Info("Tunnel adopted successfully",
		"alias", info.Alias,
		"pid", info.PID,
		"age", time.Since(info.StartDate).Round(time.Second))

	// Log to database
	if d.database != nil {
		if err := d.database.LogTunnelEvent(info.Alias, "tunnel_adopted", fmt.Sprintf("PID: %d, age: %s", info.PID, time.Since(info.StartDate).Round(time.Second))); err != nil {
			slog.Error("Failed to log tunnel adoption event", "error", err)
		}
	}

	return true
}

// monitorAdoptedTunnel monitors an adopted tunnel process by polling its PID
// This is used instead of cmd.Wait() for tunnels we adopted from a previous daemon
func (d *Daemon) monitorAdoptedTunnel(alias string, pid int) {
	slog.Debug("Starting monitor for adopted tunnel", "alias", alias, "pid", pid)

	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-d.ctx.Done():
			// Daemon is shutting down
			return

		case <-ticker.C:
			// Check if process still exists and has an established TCP connection
			process, err := os.FindProcess(pid)
			processExists := err == nil && process.Signal(syscall.Signal(0)) == nil
			hasConnection := processExists && hasEstablishedTCPConnection(pid)

			if !processExists || !hasConnection {
				// Process died or connection is dead
				reason := "process died"
				if processExists && !hasConnection {
					reason = "TCP connection dead"
					// Kill the process since it's a zombie SSH
					process.Kill()
				}
				slog.Info("Adopted tunnel connection lost", "alias", alias, "pid", pid, "reason", reason)

				d.mu.Lock()
				tunnel, exists := d.tunnels[alias]
				if !exists {
					d.mu.Unlock()
					return
				}

				// Check if this is still the same tunnel we were monitoring
				// (tunnel may have been replaced by a rapid disconnect/connect cycle)
				if tunnel.Pid != pid {
					d.mu.Unlock()
					return // Tunnel was replaced, different monitor is watching it
				}

				// Log disconnect event
				if d.database != nil {
					d.database.LogTunnelEvent(alias, "disconnect", "Adopted tunnel process died")
				}

				// Mark as disconnected
				tunnel.State = StateDisconnected
				tunnel.DisconnectedTime = time.Now()
				d.tunnels[alias] = tunnel

				// Get max retries from config
				maxRetries := core.Config.SSH.MaxRetries

				// Check if auto-reconnect is enabled and we haven't exceeded max retries
				if !tunnel.AutoReconnect || tunnel.RetryCount >= maxRetries {
					// Clean up and don't reconnect
					delete(d.tunnels, alias)

					if tunnel.RetryCount >= maxRetries {
						slog.Info("Adopted tunnel exceeded max retry attempts, giving up",
							"alias", alias,
							"max_retries", maxRetries)

						if d.database != nil {
							details := fmt.Sprintf("Max retries (%d) exceeded", maxRetries)
							d.database.LogTunnelEvent(alias, "max_retries_exceeded", details)
						}
					} else {
						slog.Info("Adopted tunnel auto-reconnect disabled, not reconnecting", "alias", alias)
					}

					d.mu.Unlock()
					return
				}

				// Check if we're online before attempting reconnection
				isOnline := d.checkOnlineStatus()
				if !isOnline {
					slog.Info("Adopted tunnel not reconnecting - currently offline",
						"alias", alias)
					d.mu.Unlock()
					return
				}

				// Calculate backoff delay
				backoff := calculateBackoff(tunnel.RetryCount)
				tunnel.RetryCount++
				tunnel.LastRetryTime = time.Now()
				tunnel.State = StateReconnecting
				tunnel.NextRetryTime = time.Now().Add(backoff)

				slog.Info("Adopted tunnel will reconnect after backoff",
					"alias", alias,
					"backoff", backoff,
					"attempt", tunnel.RetryCount,
					"max_retries", maxRetries)

				// Update tunnel state
				d.tunnels[alias] = tunnel
				d.mu.Unlock()

				// Wait for backoff period
				time.Sleep(backoff)

				// Attempt to reconnect
				slog.Info("Attempting to reconnect adopted tunnel",
					"alias", alias,
					"attempt", tunnel.RetryCount,
					"max_retries", maxRetries)

				d.mu.Lock()
				// Check if tunnel still exists (might have been manually stopped during backoff)
				tunnel, exists = d.tunnels[alias]
				if !exists {
					d.mu.Unlock()
					return
				}

				// Check if we're still online
				if !d.checkOnlineStatus() {
					slog.Info("Adopted tunnel reconnection cancelled - went offline during backoff",
						"alias", alias)
					d.mu.Unlock()
					return
				}

				// Remove tunnel from map before calling startTunnel()
				// startTunnel() will create a fresh entry with proper monitoring
				// Preserve tag from the existing tunnel
				tag := tunnel.Tag
				delete(d.tunnels, alias)
				d.mu.Unlock()

				// Start the tunnel (this creates a new SSH process)
				response := d.startTunnel(alias, tag)

				// Check if reconnection succeeded
				hasError := false
				for _, msg := range response.Messages {
					if msg.Status == "ERROR" {
						slog.Error("Adopted tunnel reconnection failed",
							"alias", alias,
							"error", msg.Message)
						hasError = true
					}
				}

				if hasError {
					// The tunnel monitoring goroutine spawned by startTunnel will handle
					// further retries, so we exit this adopted tunnel monitor
					return
				}

				// Successfully reconnected - exit this monitor since the new tunnel
				// will have its own monitoring goroutine
				slog.Info("Adopted tunnel successfully reconnected",
					"alias", alias)
				return
			}
		}
	}
}

// executeTunnelHooks executes tunnel lifecycle hooks (before_connect, after_connect)
// Hooks are fire-and-forget and do NOT block the tunnel connection
func (d *Daemon) executeTunnelHooks(alias, hookType string, hooks []core.HookConfig, tunnelState TunnelState) {
	if len(hooks) == 0 {
		return
	}

	slog.Info("Executing tunnel hooks", "alias", alias, "type", hookType, "count", len(hooks))

	for _, hook := range hooks {
		go d.executeSingleTunnelHook(alias, hookType, hook, tunnelState)
	}
}

// executeSingleTunnelHook executes a single tunnel hook with timeout
func (d *Daemon) executeSingleTunnelHook(alias, hookType string, hook core.HookConfig, tunnelState TunnelState) {
	startTime := time.Now()

	// Apply timeout
	timeout := hook.Timeout
	if timeout == 0 {
		timeout = 30 * time.Second
	}
	ctx, cancel := context.WithTimeout(d.ctx, timeout)
	defer cancel()

	// Build environment
	env := os.Environ()
	hookEnv := map[string]string{
		"OVERSEER_HOOK_TYPE":        hookType,
		"OVERSEER_HOOK_TARGET_TYPE": "tunnel",
		"OVERSEER_HOOK_TARGET":      alias,
		"OVERSEER_TUNNEL_ALIAS":     alias,
		"OVERSEER_TUNNEL_STATE":     string(tunnelState),
	}
	for k, v := range hookEnv {
		env = append(env, fmt.Sprintf("%s=%s", k, v))
	}

	// Create command via shell
	cmd := exec.CommandContext(ctx, "sh", "-c", hook.Command)
	cmd.Env = env

	// Set up process group for clean termination
	cmd.SysProcAttr = &syscall.SysProcAttr{
		Setpgid: true,
	}

	// Run the command
	err := cmd.Run()
	duration := time.Since(startTime)

	// Determine success and log
	success := err == nil
	var eventType, details string

	if success {
		eventType = "hook_executed"
		details = fmt.Sprintf("%s hook - duration: %s", hookType, duration)
		slog.Info("Tunnel hook executed",
			"alias", alias,
			"type", hookType,
			"command", hook.Command,
			"duration", duration)
	} else {
		if ctx.Err() == context.DeadlineExceeded {
			eventType = "hook_timeout"
			details = fmt.Sprintf("%s hook - timeout after %s", hookType, timeout)
			slog.Warn("Tunnel hook timed out",
				"alias", alias,
				"type", hookType,
				"command", hook.Command,
				"timeout", timeout)
			// Kill the process group
			if cmd.Process != nil {
				syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
			}
		} else {
			eventType = "hook_failed"
			details = fmt.Sprintf("%s hook - %v", hookType, err)
			slog.Warn("Tunnel hook failed",
				"alias", alias,
				"type", hookType,
				"command", hook.Command,
				"error", err)
		}
	}

	// Log to database
	if d.database != nil {
		identifier := fmt.Sprintf("%s:tunnel:%s", hookType, alias)
		if err := d.database.LogTunnelEvent(identifier, eventType, details); err != nil {
			slog.Warn("Failed to log tunnel hook event", "error", err)
		}
	}
}
