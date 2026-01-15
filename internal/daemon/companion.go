package daemon

import (
	"bufio"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"time"

	"overseer.olrik.dev/internal/core"
)

// formatDaemonMessage formats a daemon message with timestamp
func formatDaemonMessage(format string, args ...interface{}) string {
	timestamp := time.Now().Format("2006-01-02 15:04:05")
	msg := fmt.Sprintf(format, args...)
	return fmt.Sprintf("%s [DAEMON] %s", timestamp, msg)
}

// generateCompanionToken generates a random authentication token
func generateCompanionToken() (string, error) {
	bytes := make([]byte, 32) // 256 bits
	if _, err := rand.Read(bytes); err != nil {
		return "", fmt.Errorf("failed to generate random token: %w", err)
	}
	return hex.EncodeToString(bytes), nil
}

// CompanionState represents the current state of a companion process
type CompanionState string

const (
	CompanionStateStarting CompanionState = "starting"
	CompanionStateWaiting  CompanionState = "waiting"
	CompanionStateReady    CompanionState = "ready"
	CompanionStateRunning  CompanionState = "running"
	CompanionStateStopped  CompanionState = "stopped"
	CompanionStateFailed   CompanionState = "failed"
	CompanionStateExited   CompanionState = "exited"
)

// CompanionProcess represents a running companion script
type CompanionProcess struct {
	TunnelAlias  string
	Name         string
	Config       core.CompanionConfig
	Cmd          *exec.Cmd
	Pid          int
	StartTime    time.Time
	State        CompanionState
	ExitCode     *int
	ExitError    string
	output       *LogBroadcaster // For streaming combined stdout/stderr
	socketPath   string          // Unix socket for wrapper communication
	socketListen net.Listener    // Socket listener
	ctx          context.Context
	cancel       context.CancelFunc
	mu           sync.RWMutex
}

// CompanionManager manages companion processes for tunnels
type CompanionManager struct {
	companions    map[string]map[string]*CompanionProcess // alias -> name -> process
	mu            sync.RWMutex
	registerToken func(token, alias string)                    // Callback to register tokens with daemon
	logEvent      func(alias, eventType, details string) error // Callback to log events to database
}

// NewCompanionManager creates a new companion manager
func NewCompanionManager() *CompanionManager {
	return &CompanionManager{
		companions: make(map[string]map[string]*CompanionProcess),
	}
}

// SetTokenRegistrar sets the callback for registering tokens with the daemon
func (cm *CompanionManager) SetTokenRegistrar(registrar func(token, alias string)) {
	cm.registerToken = registrar
}

// SetEventLogger sets the callback for logging companion events to the database
func (cm *CompanionManager) SetEventLogger(logger func(alias, eventType, details string) error) {
	cm.logEvent = logger
}

// logCompanionEvent logs a companion event if the logger is set
func (cm *CompanionManager) logCompanionEvent(alias, name, eventType, details string) {
	if cm.logEvent == nil {
		return
	}
	fullDetails := fmt.Sprintf("[%s] %s", name, details)
	if err := cm.logEvent(alias, eventType, fullDetails); err != nil {
		slog.Error("Failed to log companion event", "error", err)
	}
}

// CompanionProgress represents a progress message during companion startup
type CompanionProgress struct {
	Name    string
	Message string
	IsError bool
}

// ProgressCallback is called for each progress message during companion startup
type ProgressCallback func(CompanionProgress)

// StartCompanions starts all companion scripts for a tunnel
// The optional onProgress callback is called for each progress message as it occurs,
// allowing callers to stream progress to clients in real-time.
func (cm *CompanionManager) StartCompanions(alias string, configs []core.CompanionConfig, onProgress ProgressCallback) error {
	cm.mu.Lock()
	if cm.companions[alias] == nil {
		cm.companions[alias] = make(map[string]*CompanionProcess)
	}
	existingCompanions := cm.companions[alias]
	cm.mu.Unlock()

	// Helper to send progress
	sendProgress := func(p CompanionProgress) {
		if onProgress != nil {
			onProgress(p)
		}
	}

	// Run companions sequentially
	for _, config := range configs {
		// Check if companion already exists
		if existing := existingCompanions[config.Name]; existing != nil {
			existing.mu.RLock()
			state := existing.State
			pid := existing.Pid
			existing.mu.RUnlock()

			if state == CompanionStateRunning || state == CompanionStateReady {
				// Already running, skip
				slog.Info("Companion already running (adopted), skipping start",
					"tunnel", alias,
					"companion", config.Name,
					"pid", pid)
				sendProgress(CompanionProgress{
					Name:    config.Name,
					Message: fmt.Sprintf("Companion '%s' already running (PID: %d)", config.Name, pid),
				})
				continue
			}

			// Existing entry but not running - restart in place to preserve broadcaster
			sendProgress(CompanionProgress{
				Name:    config.Name,
				Message: fmt.Sprintf("Starting companion '%s'...", config.Name),
			})

			// Update config in case it changed
			existing.mu.Lock()
			existing.Config = config
			existing.mu.Unlock()

			if err := cm.restartCompanionInPlace(existing); err != nil {
				if config.OnFailure == "block" {
					cm.StopCompanions(alias)
					sendProgress(CompanionProgress{
						Name:    config.Name,
						Message: fmt.Sprintf("Companion '%s' failed: %v", config.Name, err),
						IsError: true,
					})
					return fmt.Errorf("companion %q failed: %w", config.Name, err)
				}
				slog.Warn("Companion script failed but continuing",
					"tunnel", alias,
					"companion", config.Name,
					"error", err)
				sendProgress(CompanionProgress{
					Name:    config.Name,
					Message: fmt.Sprintf("Companion '%s' failed (continuing): %v", config.Name, err),
					IsError: true,
				})
				continue
			}

			sendProgress(CompanionProgress{
				Name:    config.Name,
				Message: fmt.Sprintf("Companion '%s' started", config.Name),
			})
			continue
		}

		// No existing entry - create fresh
		sendProgress(CompanionProgress{
			Name:    config.Name,
			Message: fmt.Sprintf("Starting companion '%s'...", config.Name),
		})

		proc, readyMsg, err := cm.runCompanion(alias, config)
		if err != nil {
			if config.OnFailure == "block" {
				// Stop any companions we already started
				cm.StopCompanions(alias)
				sendProgress(CompanionProgress{
					Name:    config.Name,
					Message: fmt.Sprintf("Companion '%s' failed: %v", config.Name, err),
					IsError: true,
				})
				return fmt.Errorf("companion %q failed: %w", config.Name, err)
			}
			// on_failure = "continue", log warning but proceed
			slog.Warn("Companion script failed but continuing",
				"tunnel", alias,
				"companion", config.Name,
				"error", err)
			sendProgress(CompanionProgress{
				Name:    config.Name,
				Message: fmt.Sprintf("Companion '%s' failed (continuing): %v", config.Name, err),
				IsError: true,
			})
			continue
		}

		// Add ready message
		sendProgress(CompanionProgress{
			Name:    config.Name,
			Message: readyMsg,
		})

		cm.mu.Lock()
		cm.companions[alias][config.Name] = proc
		cm.mu.Unlock()
	}

	return nil
}

// StopCompanions terminates all companions for a tunnel but keeps entries in map
// This allows attach to work even when tunnel isn't running
// Persistent companions are not stopped - they keep running across tunnel restarts
func (cm *CompanionManager) StopCompanions(alias string) {
	cm.mu.RLock()
	companions := cm.companions[alias]
	cm.mu.RUnlock()

	if companions == nil {
		return
	}

	for name, proc := range companions {
		// Skip persistent companions - they stay running when tunnel stops
		proc.mu.RLock()
		persistent := proc.Config.Persistent
		proc.mu.RUnlock()

		if persistent {
			slog.Debug("Skipping stop for persistent companion",
				"tunnel", alias,
				"companion", name)
			continue
		}

		cm.stopProcess(proc, name, alias)
	}
	// Note: we intentionally don't delete from map so attach can still work
}

// ClearCompanionHistory clears the output history for all companions of a tunnel
// Used when tunnel disconnects to prevent showing stale output on reattach
func (cm *CompanionManager) ClearCompanionHistory(alias string) {
	cm.mu.RLock()
	companions := cm.companions[alias]
	cm.mu.RUnlock()

	for _, proc := range companions {
		if proc.output != nil {
			proc.output.ClearHistory()
		}
	}
}

// RestartCompanions restarts all companions for a tunnel in-place, preserving attach connections
func (cm *CompanionManager) RestartCompanions(alias string) error {
	cm.mu.RLock()
	companions := cm.companions[alias]
	cm.mu.RUnlock()

	if companions == nil {
		return nil
	}

	for name, proc := range companions {
		proc.output.Broadcast(formatDaemonMessage("Restarting companion '%s'...\n", name))

		// Restart in place (preserves broadcaster)
		if err := cm.restartCompanionInPlace(proc); err != nil {
			proc.output.Broadcast(formatDaemonMessage("Failed to restart: %v\n", err))
			continue
		}

		// Wait for companion to be ready based on wait_mode (same as fresh start)
		config := proc.Config
		var waitErr error
		switch config.WaitMode {
		case "string":
			waitErr = cm.waitForString(proc, config.WaitFor, config.Timeout)
		default: // "completion"
			waitErr = cm.waitForCompletion(proc, config.Timeout)
		}

		if waitErr != nil {
			proc.mu.Lock()
			proc.State = CompanionStateFailed
			proc.ExitError = waitErr.Error()
			proc.mu.Unlock()
			proc.output.Broadcast(formatDaemonMessage("Failed to become ready: %v\n", waitErr))
			continue
		}

		proc.mu.Lock()
		proc.State = CompanionStateReady
		proc.mu.Unlock()

		// Apply ready_delay if configured
		if config.ReadyDelay > 0 {
			slog.Debug("Waiting after companion ready",
				"tunnel", alias,
				"companion", name,
				"delay", config.ReadyDelay)
			time.Sleep(config.ReadyDelay)
		}

		proc.output.Broadcast(formatDaemonMessage("Companion '%s' ready.\n", name))
	}
	return nil
}

// StopAllCompanions terminates all running companions
func (cm *CompanionManager) StopAllCompanions() {
	cm.mu.Lock()
	allCompanions := cm.companions
	cm.companions = make(map[string]map[string]*CompanionProcess)
	cm.mu.Unlock()

	for alias, companions := range allCompanions {
		for name, proc := range companions {
			cm.stopProcess(proc, name, alias)
		}
	}
}

// stopProcess gracefully stops a companion process
func (cm *CompanionManager) stopProcess(proc *CompanionProcess, name, alias string) {
	if proc == nil {
		return
	}

	proc.mu.Lock()
	if proc.State == CompanionStateStopped || proc.State == CompanionStateExited {
		proc.mu.Unlock()
		return
	}
	proc.State = CompanionStateStopped
	pid := proc.Pid
	proc.mu.Unlock()

	// Determine which signal to send based on config (default: INT for foreground processes)
	stopSignal := proc.Config.StopSignal
	if stopSignal == "" {
		stopSignal = "INT"
	}

	var sig syscall.Signal
	switch stopSignal {
	case "TERM", "SIGTERM":
		sig = syscall.SIGTERM
	case "HUP", "SIGHUP":
		sig = syscall.SIGHUP
	default:
		sig = syscall.SIGINT
	}

	slog.Info("Stopping companion", "tunnel", alias, "companion", name, "pid", pid, "signal", stopSignal)
	cm.logCompanionEvent(alias, name, "companion_stopped", fmt.Sprintf("PID: %d, signal: %s", pid, stopSignal))

	// Get process handle - either from Cmd or by PID (for adopted processes)
	var osProc *os.Process
	if proc.Cmd != nil && proc.Cmd.Process != nil {
		osProc = proc.Cmd.Process
	} else if pid > 0 {
		// Adopted process - find by PID
		var err error
		osProc, err = os.FindProcess(pid)
		if err != nil {
			slog.Debug("Failed to find companion process", "pid", pid, "error", err)
			return
		}
	} else {
		return
	}

	// Send signal to process group (negative PID) so all children receive it too
	// This is important for scripts running foreground processes
	if err := syscall.Kill(-pid, sig); err != nil {
		// Fall back to signaling just the process if group signal fails
		if err := osProc.Signal(sig); err != nil {
			slog.Debug("Failed to send signal to companion", "signal", stopSignal, "error", err)
			return
		}
	}

	// Wait for graceful shutdown (6s to allow wrapper's 5s child timeout to complete first)
	done := make(chan struct{})
	go func() {
		if proc.Cmd != nil {
			proc.Cmd.Wait()
		} else {
			// For adopted processes, poll for exit
			for range 60 { // 60 * 100ms = 6s
				if err := osProc.Signal(syscall.Signal(0)); err != nil {
					break
				}
				time.Sleep(100 * time.Millisecond)
			}
		}
		close(done)
	}()

	select {
	case <-done:
		slog.Debug("Companion stopped gracefully", "tunnel", alias, "companion", name)
	case <-time.After(6 * time.Second):
		// Force kill
		slog.Warn("Companion did not stop gracefully, force killing", "tunnel", alias, "companion", name)
		syscall.Kill(-pid, syscall.SIGKILL)
	}

	// Now that process has exited, cancel context and clear history
	// This disconnects attached clients and prepares for next attach
	if proc.cancel != nil {
		proc.cancel()
	}
	if proc.output != nil {
		proc.output.ClearHistory()
	}
}

// getCompanionSocketPath returns the unix socket path for wrapper communication
func getCompanionSocketPath(alias, name string) string {
	return filepath.Join(os.TempDir(), fmt.Sprintf("overseer-companion-%s-%s.sock", alias, name))
}

// runCompanion executes a single companion script via the wrapper
// Returns the process, a ready message describing how it became ready, and any error
func (cm *CompanionManager) runCompanion(alias string, config core.CompanionConfig) (*CompanionProcess, string, error) {
	// Use background context, not daemon context - we don't want the process killed
	// when daemon context is cancelled during reload. Companions use Setsid to survive
	// daemon death and are stopped manually via stopProcess().
	ctx, cancel := context.WithCancel(context.Background())

	// Get the overseer executable path for the wrapper
	execPath, err := os.Executable()
	if err != nil {
		cancel()
		return nil, "", fmt.Errorf("failed to get executable path: %w", err)
	}

	// Generate authentication token
	token, err := generateCompanionToken()
	if err != nil {
		cancel()
		return nil, "", fmt.Errorf("failed to generate token: %w", err)
	}

	// Register token with daemon for validation
	if cm.registerToken != nil {
		cm.registerToken(token, alias)
	}

	// Create unix socket for wrapper to send output
	socketPath := getCompanionSocketPath(alias, config.Name)
	// Remove existing socket if present
	os.Remove(socketPath)

	listener, err := net.Listen("unix", socketPath)
	if err != nil {
		cancel()
		return nil, "", fmt.Errorf("failed to create socket: %w", err)
	}

	// Create log broadcaster for output streaming
	broadcaster := NewLogBroadcaster(core.Config.Companion.HistorySize)

	// Run companion via environment variable injection (like askpass)
	// The wrapper command is invoked with "daemon" arg for easy identification as "overseer daemon" in the process list
	// env vars trigger injection in main.go
	cmd := exec.Command(execPath, "daemon")

	// Set working directory (wrapper will inherit it and run child in it)
	if config.Workdir != "" {
		workdir := expandPath(config.Workdir)
		if _, err := os.Stat(workdir); os.IsNotExist(err) {
			listener.Close()
			os.Remove(socketPath)
			cancel()
			return nil, "", fmt.Errorf("workdir does not exist: %s", workdir)
		}
		cmd.Dir = workdir
	}

	// Set environment variables for companion-run command injection and user config
	cmd.Env = os.Environ()
	cmd.Env = append(cmd.Env,
		fmt.Sprintf("OVERSEER_COMPANION_RUN_ALIAS=%s", alias),
		fmt.Sprintf("OVERSEER_TUNNEL_TOKEN=%s", token),
		fmt.Sprintf("OVERSEER_COMPANION_NAME=%s", config.Name),
	)
	for k, v := range config.Environment {
		cmd.Env = append(cmd.Env, fmt.Sprintf("%s=%s", k, v))
	}

	// Create wrapper process in its own session so it survives daemon restart (for hot reload)
	cmd.SysProcAttr = &syscall.SysProcAttr{
		Setsid: true,
	}

	// Start the process
	slog.Info("Starting companion",
		"tunnel", alias,
		"companion", config.Name,
		"command", config.Command)

	if err := cmd.Start(); err != nil {
		listener.Close()
		os.Remove(socketPath)
		cancel()
		return nil, "", fmt.Errorf("failed to start companion: %w", err)
	}

	proc := &CompanionProcess{
		TunnelAlias:  alias,
		Name:         config.Name,
		Config:       config,
		Cmd:          cmd,
		Pid:          cmd.Process.Pid,
		StartTime:    time.Now(),
		State:        CompanionStateStarting,
		output:       broadcaster,
		socketPath:   socketPath,
		socketListen: listener,
		ctx:          ctx,
		cancel:       cancel,
	}

	// Start listening for wrapper output
	go cm.listenForWrapperOutput(proc)

	// Wait based on mode
	proc.mu.Lock()
	proc.State = CompanionStateWaiting
	proc.mu.Unlock()

	var waitErr error
	switch config.WaitMode {
	case "string":
		waitErr = cm.waitForString(proc, config.WaitFor, config.Timeout)
	default: // "completion"
		waitErr = cm.waitForCompletion(proc, config.Timeout)
	}

	if waitErr != nil {
		proc.mu.Lock()
		proc.State = CompanionStateFailed
		proc.ExitError = waitErr.Error()
		proc.mu.Unlock()
		cm.logCompanionEvent(alias, config.Name, "companion_failed", waitErr.Error())
		cancel()
		return nil, "", waitErr
	}

	proc.mu.Lock()
	proc.State = CompanionStateReady
	proc.mu.Unlock()

	slog.Info("Companion ready",
		"tunnel", alias,
		"companion", config.Name,
		"pid", proc.Pid)
	cm.logCompanionEvent(alias, config.Name, "companion_ready", fmt.Sprintf("PID: %d", proc.Pid))

	// Apply ready_delay if configured (allows time for networking to stabilize)
	if config.ReadyDelay > 0 {
		slog.Debug("Waiting after companion ready",
			"tunnel", alias,
			"companion", config.Name,
			"delay", config.ReadyDelay)
		time.Sleep(config.ReadyDelay)
	}

	// Build ready message based on wait mode
	var readyMsg string
	switch config.WaitMode {
	case "string":
		readyMsg = fmt.Sprintf("Companion '%s' ready (matched '%s')", config.Name, config.WaitFor)
	default: // "completion"
		readyMsg = fmt.Sprintf("Companion '%s' completed successfully", config.Name)
	}

	// If keep_alive, monitor the process
	if config.KeepAlive {
		proc.mu.Lock()
		proc.State = CompanionStateRunning
		proc.mu.Unlock()
		go cm.monitorCompanion(proc)
		readyMsg = fmt.Sprintf("Companion '%s' ready (running, PID: %d)", config.Name, proc.Pid)
	}

	return proc, readyMsg, nil
}

// listenForWrapperOutput accepts connections from wrapper and streams to LogBroadcaster
func (cm *CompanionManager) listenForWrapperOutput(proc *CompanionProcess) {
	for {
		select {
		case <-proc.ctx.Done():
			proc.socketListen.Close()
			os.Remove(proc.socketPath)
			return
		default:
		}

		// Set accept deadline so we can check context periodically
		if unixListener, ok := proc.socketListen.(*net.UnixListener); ok {
			unixListener.SetDeadline(time.Now().Add(1 * time.Second))
		}

		conn, err := proc.socketListen.Accept()
		if err != nil {
			if netErr, ok := err.(net.Error); ok && netErr.Timeout() {
				continue
			}
			// Listener closed or error
			return
		}

		// Handle this connection - read lines and broadcast
		go cm.handleWrapperConnection(proc, conn)
	}
}

// handleWrapperConnection reads output from wrapper and broadcasts to subscribers
func (cm *CompanionManager) handleWrapperConnection(proc *CompanionProcess, conn net.Conn) {
	defer conn.Close()

	reader := bufio.NewReader(conn)
	inHistoryReplay := false

	for {
		select {
		case <-proc.ctx.Done():
			return
		default:
		}

		line, err := reader.ReadString('\n')
		if err != nil {
			// Connection closed or error - wrapper will reconnect
			return
		}

		// Handle protocol markers for history replay
		trimmed := strings.TrimSpace(line)
		if trimmed == "HISTORY_START" {
			slog.Debug("Companion wrapper history replay starting",
				"tunnel", proc.TunnelAlias, "companion", proc.Name)
			inHistoryReplay = true
			continue
		}
		if trimmed == "HISTORY_END" {
			slog.Debug("Companion wrapper history replay complete",
				"tunnel", proc.TunnelAlias, "companion", proc.Name)
			inHistoryReplay = false
			continue
		}

		if inHistoryReplay {
			// History replay - only add to buffer, don't broadcast to existing subscribers
			proc.output.AddToHistory(line)
		} else {
			// Normal output - broadcast to all subscribers
			proc.output.Broadcast(line)
		}
	}
}

// waitForCompletion waits for the script to exit successfully
func (cm *CompanionManager) waitForCompletion(proc *CompanionProcess, timeout time.Duration) error {
	done := make(chan error, 1)
	go func() {
		done <- proc.Cmd.Wait()
	}()

	select {
	case err := <-done:
		if err != nil {
			return fmt.Errorf("script exited with error: %w", err)
		}
		return nil
	case <-time.After(timeout):
		proc.Cmd.Process.Kill()
		return fmt.Errorf("timeout after %v waiting for completion", timeout)
	case <-proc.ctx.Done():
		return fmt.Errorf("cancelled")
	}
}

// waitForString monitors output for a specific string by subscribing to LogBroadcaster
func (cm *CompanionManager) waitForString(proc *CompanionProcess, waitFor string, timeout time.Duration) error {
	// Subscribe with history to catch strings that arrived before subscription
	outputChan, history := proc.output.SubscribeWithHistory(100)
	defer proc.output.Unsubscribe(outputChan)

	// Check history first - the string may have already arrived
	// Only check lines from after this process started (timestamp filtering)
	for _, line := range history {
		if lineTime, ok := parseOutputTimestamp(line); ok {
			if lineTime.Before(proc.StartTime) {
				continue // Skip lines from previous connection
			}
		}
		if strings.Contains(line, waitFor) {
			return nil
		}
	}

	timeoutChan := time.After(timeout)

	for {
		select {
		case line, ok := <-outputChan:
			if !ok {
				return fmt.Errorf("output stream closed before finding %q", waitFor)
			}
			if strings.Contains(line, waitFor) {
				return nil
			}
		case <-timeoutChan:
			proc.Cmd.Process.Kill()
			return fmt.Errorf("timeout after %v waiting for %q", timeout, waitFor)
		case <-proc.ctx.Done():
			return fmt.Errorf("cancelled")
		}
	}
}

// parseOutputTimestamp extracts timestamp from output line format: "2006-01-02 15:04:05 [output] ..."
func parseOutputTimestamp(line string) (time.Time, bool) {
	if len(line) < 19 {
		return time.Time{}, false
	}
	t, err := time.ParseInLocation("2006-01-02 15:04:05", line[:19], time.Local)
	if err != nil {
		return time.Time{}, false
	}
	return t, true
}

// monitorCompanion watches a companion process after it's ready
func (cm *CompanionManager) monitorCompanion(proc *CompanionProcess) {
	for {
		err := proc.Cmd.Wait()

		proc.mu.Lock()
		if proc.State == CompanionStateStopped {
			// We stopped it intentionally
			proc.mu.Unlock()
			return
		}

		alias := proc.TunnelAlias
		name := proc.Name
		autoRestart := proc.Config.AutoRestart

		var exitDetails string
		if err != nil {
			if exitErr, ok := err.(*exec.ExitError); ok {
				exitCode := exitErr.ExitCode()
				proc.ExitCode = &exitCode
				proc.ExitError = err.Error()
				exitDetails = fmt.Sprintf("exit code %d", exitCode)
			} else {
				proc.ExitError = err.Error()
				exitDetails = err.Error()
			}
			slog.Warn("Companion exited with error",
				"tunnel", alias,
				"companion", name,
				"error", err)
		} else {
			exitCode := 0
			proc.ExitCode = &exitCode
			exitDetails = "exit code 0"
			slog.Info("Companion exited normally",
				"tunnel", alias,
				"companion", name)
		}

		if !autoRestart {
			proc.State = CompanionStateExited
			proc.mu.Unlock()
			cm.logCompanionEvent(alias, name, "companion_exited", exitDetails)
			return
		}

		// Auto-restart is enabled
		proc.mu.Unlock()
		cm.logCompanionEvent(alias, name, "companion_exited", exitDetails+" (will restart)")

		// Brief delay before restart
		time.Sleep(1 * time.Second)

		// Check if we should still restart (not stopped during delay)
		proc.mu.Lock()
		if proc.State == CompanionStateStopped {
			proc.mu.Unlock()
			return
		}
		proc.mu.Unlock()

		slog.Info("Auto-restarting companion",
			"tunnel", alias,
			"companion", name)
		cm.logCompanionEvent(alias, name, "companion_restarting", "auto-restart triggered")

		// Restart the companion
		if err := cm.restartCompanionInPlace(proc); err != nil {
			slog.Error("Failed to auto-restart companion",
				"tunnel", alias,
				"companion", name,
				"error", err)
			proc.mu.Lock()
			proc.State = CompanionStateFailed
			proc.ExitError = fmt.Sprintf("auto-restart failed: %v", err)
			proc.mu.Unlock()
			cm.logCompanionEvent(alias, name, "companion_failed", fmt.Sprintf("auto-restart failed: %v", err))
			return
		}
	}
}

// restartCompanionInPlace restarts a companion process in-place (reusing the same CompanionProcess struct)
func (cm *CompanionManager) restartCompanionInPlace(proc *CompanionProcess) error {
	alias := proc.TunnelAlias
	config := proc.Config

	// Stop the existing process if it's still running
	// IMPORTANT: Set state to Stopped BEFORE killing so the old monitoring goroutine exits
	proc.mu.Lock()
	oldPid := proc.Pid
	if oldPid > 0 {
		proc.State = CompanionStateStopped
	}
	proc.mu.Unlock()

	if oldPid > 0 {
		if osProc, err := os.FindProcess(oldPid); err == nil {
			// Check if process is still alive
			if err := osProc.Signal(syscall.Signal(0)); err == nil {
				slog.Debug("Stopping existing companion process before restart",
					"tunnel", alias,
					"companion", config.Name,
					"pid", oldPid)

				// Try graceful termination first
				if err := osProc.Signal(syscall.SIGTERM); err == nil {
					// Wait up to 5 seconds for graceful shutdown
					for i := 0; i < 50; i++ {
						time.Sleep(100 * time.Millisecond)
						if err := osProc.Signal(syscall.Signal(0)); err != nil {
							break // Process exited
						}
					}

					// Force kill if still running after 5 seconds
					if err := osProc.Signal(syscall.Signal(0)); err == nil {
						slog.Debug("Force killing companion process",
							"tunnel", alias,
							"companion", config.Name,
							"pid", oldPid)
						osProc.Signal(syscall.SIGKILL)
					}
				}
			}
		}
	}

	// Cancel old context to stop any monitoring goroutines and close old socket
	if proc.cancel != nil {
		proc.cancel()
	}

	// Close old socket listener if it exists
	proc.mu.Lock()
	if proc.socketListen != nil {
		proc.socketListen.Close()
	}
	if proc.socketPath != "" {
		os.Remove(proc.socketPath)
	}
	proc.mu.Unlock()

	// Clear history buffer - new run, fresh output
	proc.output.ClearHistory()

	// Create new socket for wrapper output
	socketPath := getCompanionSocketPath(alias, config.Name)
	os.Remove(socketPath) // Remove any stale socket
	listener, err := net.Listen("unix", socketPath)
	if err != nil {
		return fmt.Errorf("failed to create socket: %w", err)
	}

	// Get the overseer executable path for the wrapper
	execPath, err := os.Executable()
	if err != nil {
		return fmt.Errorf("failed to get executable path: %w", err)
	}

	// Generate new authentication token
	token, err := generateCompanionToken()
	if err != nil {
		return fmt.Errorf("failed to generate token: %w", err)
	}

	// Register token with daemon for validation
	if cm.registerToken != nil {
		cm.registerToken(token, alias)
	}

	// Create new command
	cmd := exec.Command(execPath, "daemon")

	// Set working directory
	if config.Workdir != "" {
		workdir := expandPath(config.Workdir)
		cmd.Dir = workdir
	}

	// Set environment variables
	cmd.Env = os.Environ()
	cmd.Env = append(cmd.Env,
		fmt.Sprintf("OVERSEER_COMPANION_RUN_ALIAS=%s", alias),
		fmt.Sprintf("OVERSEER_TUNNEL_TOKEN=%s", token),
		fmt.Sprintf("OVERSEER_COMPANION_NAME=%s", config.Name),
	)
	for k, v := range config.Environment {
		cmd.Env = append(cmd.Env, fmt.Sprintf("%s=%s", k, v))
	}

	// Create process in its own session
	cmd.SysProcAttr = &syscall.SysProcAttr{
		Setsid: true,
	}

	// Start the process
	if err := cmd.Start(); err != nil {
		listener.Close()
		os.Remove(socketPath)
		return fmt.Errorf("failed to start companion: %w", err)
	}

	// Create new context for monitoring
	ctx, cancel := context.WithCancel(context.Background())

	// Update process info
	proc.mu.Lock()
	proc.Cmd = cmd
	proc.Pid = cmd.Process.Pid
	proc.StartTime = time.Now()
	proc.State = CompanionStateWaiting // Start in waiting state until ready criteria met
	proc.ExitCode = nil
	proc.ExitError = ""
	proc.ctx = ctx
	proc.cancel = cancel
	proc.socketPath = socketPath
	proc.socketListen = listener
	proc.mu.Unlock()

	// Start listening for wrapper output
	go cm.listenForWrapperOutput(proc)

	slog.Info("Companion restarted, waiting for ready",
		"tunnel", alias,
		"companion", config.Name,
		"pid", cmd.Process.Pid)
	cm.logCompanionEvent(alias, config.Name, "companion_restarted", fmt.Sprintf("PID: %d", cmd.Process.Pid))

	// Wait for ready criteria (same as runCompanion)
	var waitErr error
	switch config.WaitMode {
	case "string":
		waitErr = cm.waitForString(proc, config.WaitFor, config.Timeout)
	default: // "completion"
		waitErr = cm.waitForCompletion(proc, config.Timeout)
	}

	if waitErr != nil {
		slog.Warn("Companion ready wait failed after restart",
			"tunnel", alias,
			"companion", config.Name,
			"error", waitErr)
		proc.mu.Lock()
		proc.State = CompanionStateFailed
		proc.ExitError = waitErr.Error()
		proc.mu.Unlock()
		cm.logCompanionEvent(alias, config.Name, "companion_failed", waitErr.Error())
		cancel()
		return waitErr
	}

	proc.mu.Lock()
	proc.State = CompanionStateReady
	proc.mu.Unlock()

	slog.Info("Companion ready after restart",
		"tunnel", alias,
		"companion", config.Name,
		"pid", cmd.Process.Pid)
	cm.logCompanionEvent(alias, config.Name, "companion_ready", fmt.Sprintf("PID: %d", cmd.Process.Pid))

	// Apply ready_delay if configured
	if config.ReadyDelay > 0 {
		slog.Debug("Waiting after companion ready",
			"tunnel", alias,
			"companion", config.Name,
			"delay", config.ReadyDelay)
		time.Sleep(config.ReadyDelay)
	}

	// If keep_alive, start monitor and transition to running state
	if config.KeepAlive {
		proc.mu.Lock()
		proc.State = CompanionStateRunning
		proc.mu.Unlock()
		go cm.monitorCompanion(proc)
	}

	return nil
}

// GetCompanionStatus returns status of all companions
func (cm *CompanionManager) GetCompanionStatus() map[string][]CompanionStatus {
	cm.mu.RLock()
	defer cm.mu.RUnlock()

	result := make(map[string][]CompanionStatus)
	for alias, companions := range cm.companions {
		statuses := make([]CompanionStatus, 0, len(companions))
		for _, proc := range companions {
			proc.mu.RLock()
			status := CompanionStatus{
				Name:      proc.Name,
				Pid:       proc.Pid,
				State:     string(proc.State),
				StartTime: proc.StartTime,
				Command:   proc.Config.Command,
			}
			if proc.ExitCode != nil {
				status.ExitCode = proc.ExitCode
			}
			if proc.ExitError != "" {
				status.ExitError = proc.ExitError
			}
			proc.mu.RUnlock()
			statuses = append(statuses, status)
		}
		result[alias] = statuses
	}
	return result
}

// CompanionStatus represents the status of a companion for reporting
type CompanionStatus struct {
	Name      string    `json:"name"`
	Pid       int       `json:"pid"`
	State     string    `json:"state"`
	StartTime time.Time `json:"start_time"`
	Command   string    `json:"command"`
	ExitCode  *int      `json:"exit_code,omitempty"`
	ExitError string    `json:"exit_error,omitempty"`
}

// HandleCompanionAttach streams companion output to client via LogBroadcaster
// showHistory controls whether to send recent history on attach (false for reconnects)
// historyLines controls how many lines of history to show (default 20)
func (cm *CompanionManager) HandleCompanionAttach(conn net.Conn, alias string, name string, showHistory bool, historyLines int) {
	defer conn.Close()

	cm.mu.Lock()
	companions := cm.companions[alias]
	var proc *CompanionProcess
	if companions != nil {
		proc = companions[name]
	}

	// If proc exists but is stopped with a cancelled context, reset the context
	// so we can wait for the companion to start again
	if proc != nil {
		proc.mu.Lock()
		if proc.State == CompanionStateStopped || proc.State == CompanionStateFailed {
			// Check if context is cancelled
			select {
			case <-proc.ctx.Done():
				// Context is cancelled, create a fresh one
				ctx, cancel := context.WithCancel(context.Background())
				proc.ctx = ctx
				proc.cancel = cancel
			default:
				// Context is still active, nothing to do
			}
		}
		proc.mu.Unlock()
	}

	// If proc doesn't exist, check if companion is configured and create dormant entry
	if proc == nil {
		tunnelConfig := core.Config.Tunnels[alias]
		if tunnelConfig == nil {
			cm.mu.Unlock()
			errMsg := fmt.Sprintf("Tunnel %q not found in configuration\n", alias)
			conn.Write([]byte(errMsg))
			return
		}

		// Find companion in config
		var companionConfig *core.CompanionConfig
		for i := range tunnelConfig.Companions {
			if tunnelConfig.Companions[i].Name == name {
				companionConfig = &tunnelConfig.Companions[i]
				break
			}
		}

		if companionConfig == nil {
			cm.mu.Unlock()
			errMsg := fmt.Sprintf("Companion %q not configured for tunnel %q\n", name, alias)
			conn.Write([]byte(errMsg))
			return
		}

		// Create dormant entry so we can attach and wait for it to start
		ctx, cancel := context.WithCancel(context.Background())
		proc = &CompanionProcess{
			TunnelAlias: alias,
			Name:        name,
			Config:      *companionConfig,
			State:       CompanionStateStopped,
			output:      NewLogBroadcaster(core.Config.Companion.HistorySize),
			ctx:         ctx,
			cancel:      cancel,
		}

		// Add to map
		if cm.companions[alias] == nil {
			cm.companions[alias] = make(map[string]*CompanionProcess)
		}
		cm.companions[alias][name] = proc
	}
	cm.mu.Unlock()

	// Check state and send appropriate initial message
	proc.mu.RLock()
	state := proc.State
	pid := proc.Pid
	proc.mu.RUnlock()

	// Send initial message
	initialMsg := fmt.Sprintf("Attached to companion %q for tunnel %q (pid: %d). Press Ctrl+C to detach.\n",
		name, alias, pid)
	if _, err := conn.Write([]byte(initialMsg)); err != nil {
		return
	}

	// Notify if companion isn't currently running
	if state != CompanionStateRunning && state != CompanionStateReady {
		stateMsg := formatDaemonMessage("Companion is not currently running (state: %s)\n", state)
		conn.Write([]byte(stateMsg))
	}

	// Subscribe to output - with history on first connect, without on reconnect
	var outputChan chan string
	if showHistory && (state == CompanionStateRunning || state == CompanionStateReady) {
		var history []string
		outputChan, history = proc.output.SubscribeWithHistory(historyLines)
		// Send history before streaming live output
		for _, line := range history {
			if _, err := conn.Write([]byte(line)); err != nil {
				proc.output.Unsubscribe(outputChan)
				return
			}
		}
	} else {
		outputChan = proc.output.Subscribe()
	}
	defer proc.output.Unsubscribe(outputChan)

	conn.Write([]byte("\n"))

	// Detect when client disconnects
	done := make(chan bool)
	go func() {
		reader := bufio.NewReader(conn)
		io.Copy(io.Discard, reader)
		done <- true
	}()

	// Stream output to client
	for {
		select {
		case <-done:
			return
		case <-proc.ctx.Done():
			conn.Write([]byte("\nCompanion process terminated.\n"))
			return
		case line, ok := <-outputChan:
			if !ok {
				return
			}
			if _, err := conn.Write([]byte(line)); err != nil {
				return
			}
		}
	}
}

// StartSingleCompanion starts a specific companion for a running tunnel
func (cm *CompanionManager) StartSingleCompanion(alias string, name string) error {
	// Get tunnel config
	tunnelConfig := core.Config.Tunnels[alias]
	if tunnelConfig == nil {
		return fmt.Errorf("no tunnel configuration for %q", alias)
	}

	// Find companion config
	var config *core.CompanionConfig
	for i := range tunnelConfig.Companions {
		if tunnelConfig.Companions[i].Name == name {
			config = &tunnelConfig.Companions[i]
			break
		}
	}
	if config == nil {
		return fmt.Errorf("companion %q not found in tunnel %q configuration", name, alias)
	}

	// Check if already running
	cm.mu.RLock()
	if companions := cm.companions[alias]; companions != nil {
		if existing := companions[name]; existing != nil {
			existing.mu.RLock()
			state := existing.State
			existing.mu.RUnlock()
			if state == CompanionStateRunning || state == CompanionStateReady || state == CompanionStateWaiting {
				cm.mu.RUnlock()
				return fmt.Errorf("companion %q is already running (state: %s), use restart instead", name, state)
			}
		}
	}
	cm.mu.RUnlock()

	// Start the companion
	proc, _, err := cm.runCompanion(alias, *config)
	if err != nil {
		return err
	}

	cm.mu.Lock()
	if cm.companions[alias] == nil {
		cm.companions[alias] = make(map[string]*CompanionProcess)
	}
	cm.companions[alias][name] = proc
	cm.mu.Unlock()

	return nil
}

// StopSingleCompanion stops a specific companion without affecting the tunnel
func (cm *CompanionManager) StopSingleCompanion(alias string, name string) error {
	cm.mu.Lock()
	companions := cm.companions[alias]
	if companions == nil {
		cm.mu.Unlock()
		return nil // No companions for this tunnel, not an error
	}

	proc := companions[name]
	if proc == nil {
		cm.mu.Unlock()
		return nil // Companion not running, not an error
	}

	delete(companions, name)
	cm.mu.Unlock()

	cm.stopProcess(proc, name, alias)
	return nil
}

// RestartSingleCompanion restarts a specific companion
func (cm *CompanionManager) RestartSingleCompanion(alias string, name string) error {
	// Stop first
	if err := cm.StopSingleCompanion(alias, name); err != nil {
		return fmt.Errorf("failed to stop companion: %w", err)
	}

	// Give a brief pause for cleanup
	time.Sleep(100 * time.Millisecond)

	// Start again
	if err := cm.StartSingleCompanion(alias, name); err != nil {
		return fmt.Errorf("failed to start companion: %w", err)
	}

	return nil
}

// GetCompanion returns a specific companion process
func (cm *CompanionManager) GetCompanion(alias, name string) *CompanionProcess {
	cm.mu.RLock()
	defer cm.mu.RUnlock()

	if companions := cm.companions[alias]; companions != nil {
		return companions[name]
	}
	return nil
}

// HasCompanions returns true if the tunnel has any companions in the map (running or dormant)
func (cm *CompanionManager) HasCompanions(alias string) bool {
	cm.mu.RLock()
	defer cm.mu.RUnlock()
	return len(cm.companions[alias]) > 0
}

// HasRunningCompanions returns true if the tunnel has any companions actually running
func (cm *CompanionManager) HasRunningCompanions(alias string) bool {
	cm.mu.RLock()
	defer cm.mu.RUnlock()

	companions := cm.companions[alias]
	if companions == nil {
		return false
	}

	for _, proc := range companions {
		proc.mu.RLock()
		state := proc.State
		proc.mu.RUnlock()

		if state == CompanionStateRunning || state == CompanionStateReady || state == CompanionStateWaiting {
			return true
		}
	}
	return false
}

// expandPath expands ~ to home directory
func expandPath(path string) string {
	if strings.HasPrefix(path, "~/") {
		home, err := os.UserHomeDir()
		if err != nil {
			return path
		}
		return home + path[1:]
	}
	return path
}

// =============================================================================
// Companion State Persistence (for hot reload)
// =============================================================================

const companionStateVersion = "1"

// CompanionStateFile contains saved companion state for hot reload
type CompanionStateFile struct {
	Version   string          `json:"version"`
	Timestamp string          `json:"timestamp"`
	Tunnels   []CompanionTunnelInfo `json:"tunnels"`
}

// CompanionTunnelInfo contains companion info for a single tunnel
type CompanionTunnelInfo struct {
	Alias      string          `json:"alias"`
	Companions []CompanionInfo `json:"companions"`
}

// CompanionInfo contains info needed to adopt a running companion
type CompanionInfo struct {
	Name      string    `json:"name"`
	Pid       int       `json:"pid"`
	Command   string    `json:"command"`
	Workdir   string    `json:"workdir"`
	StartTime time.Time `json:"start_time"`
	State     string    `json:"state"`
}

// GetCompanionStatePath returns the path to the companion state file
func GetCompanionStatePath() string {
	return filepath.Join(core.Config.ConfigPath, "companion_state.json")
}

// SaveCompanionState saves all running companion state to disk
func (cm *CompanionManager) SaveCompanionState() error {
	cm.mu.RLock()
	defer cm.mu.RUnlock()

	var tunnelInfos []CompanionTunnelInfo

	for alias, companions := range cm.companions {
		var compInfos []CompanionInfo
		for _, proc := range companions {
			proc.mu.RLock()
			// Only save running companions
			if proc.Pid <= 0 || proc.State == CompanionStateStopped || proc.State == CompanionStateFailed {
				proc.mu.RUnlock()
				continue
			}

			info := CompanionInfo{
				Name:      proc.Name,
				Pid:       proc.Pid,
				Command:   proc.Config.Command,
				Workdir:   proc.Config.Workdir,
				StartTime: proc.StartTime,
				State:     string(proc.State),
			}
			proc.mu.RUnlock()
			compInfos = append(compInfos, info)
		}

		if len(compInfos) > 0 {
			tunnelInfos = append(tunnelInfos, CompanionTunnelInfo{
				Alias:      alias,
				Companions: compInfos,
			})
		}
	}

	state := CompanionStateFile{
		Version:   companionStateVersion,
		Timestamp: time.Now().Format(time.RFC3339),
		Tunnels:   tunnelInfos,
	}

	data, err := json.MarshalIndent(state, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to marshal companion state: %w", err)
	}

	// Atomic write
	statePath := GetCompanionStatePath()
	tempPath := statePath + ".tmp"

	if err := os.WriteFile(tempPath, data, 0600); err != nil {
		return fmt.Errorf("failed to write companion state temp file: %w", err)
	}

	if err := os.Rename(tempPath, statePath); err != nil {
		os.Remove(tempPath)
		return fmt.Errorf("failed to rename companion state file: %w", err)
	}

	slog.Info("Saved companion state for hot reload", "tunnels", len(tunnelInfos))
	return nil
}

// LoadCompanionState reads companion state from disk
func LoadCompanionState() (*CompanionStateFile, error) {
	statePath := GetCompanionStatePath()

	if _, err := os.Stat(statePath); os.IsNotExist(err) {
		return nil, nil
	}

	data, err := os.ReadFile(statePath)
	if err != nil {
		return nil, fmt.Errorf("failed to read companion state file: %w", err)
	}

	var state CompanionStateFile
	if err := json.Unmarshal(data, &state); err != nil {
		return nil, fmt.Errorf("failed to parse companion state file: %w", err)
	}

	if state.Version != companionStateVersion {
		return nil, fmt.Errorf("unsupported companion state version: %s", state.Version)
	}

	return &state, nil
}

// RemoveCompanionStateFile removes the companion state file
func RemoveCompanionStateFile() error {
	statePath := GetCompanionStatePath()
	if err := os.Remove(statePath); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("failed to remove companion state file: %w", err)
	}
	return nil
}

// AdoptCompanions adopts running companion processes from previous daemon
func (cm *CompanionManager) AdoptCompanions() int {
	state, err := LoadCompanionState()
	if err != nil {
		slog.Error("Failed to load companion state", "error", err)
		return 0
	}

	if state == nil {
		return 0
	}

	slog.Info("Attempting to adopt companions from previous daemon", "tunnels", len(state.Tunnels))

	adoptedCount := 0

	for _, tunnelInfo := range state.Tunnels {
		if cm.companions[tunnelInfo.Alias] == nil {
			cm.companions[tunnelInfo.Alias] = make(map[string]*CompanionProcess)
		}

		for _, compInfo := range tunnelInfo.Companions {
			slog.Debug("Checking companion for adoption",
				"tunnel", tunnelInfo.Alias,
				"companion", compInfo.Name,
				"pid", compInfo.Pid)

			// Check if process is still running
			process, err := os.FindProcess(compInfo.Pid)
			if err != nil {
				slog.Debug("Companion process not found", "pid", compInfo.Pid, "companion", compInfo.Name)
				continue
			}

			// Verify process is still alive (on Unix, Signal(0) checks existence)
			if err := process.Signal(syscall.Signal(0)); err != nil {
				slog.Warn("Companion process no longer running",
					"pid", compInfo.Pid,
					"companion", compInfo.Name,
					"error", err)
				continue
			}

			// Find companion config
			tunnelConfig := core.Config.Tunnels[tunnelInfo.Alias]
			if tunnelConfig == nil {
				slog.Debug("Tunnel config not found for adopted companion", "alias", tunnelInfo.Alias)
				continue
			}

			var config *core.CompanionConfig
			for i := range tunnelConfig.Companions {
				if tunnelConfig.Companions[i].Name == compInfo.Name {
					config = &tunnelConfig.Companions[i]
					break
				}
			}
			if config == nil {
				slog.Debug("Companion config not found", "companion", compInfo.Name)
				continue
			}

			// Verify command matches
			if compInfo.Command != "" && config.Command != "" {
				if compInfo.Command != config.Command {
					slog.Warn("Companion command mismatch, not adopting",
						"companion", compInfo.Name,
						"saved", compInfo.Command,
						"config", config.Command)
					continue
				}
			}

			// Create socket listener for wrapper to reconnect
			socketPath := getCompanionSocketPath(tunnelInfo.Alias, compInfo.Name)
			// Remove existing socket if present (stale from previous daemon)
			os.Remove(socketPath)

			listener, err := net.Listen("unix", socketPath)
			if err != nil {
				slog.Warn("Failed to create socket for adopted companion",
					"companion", compInfo.Name,
					"error", err)
				continue
			}

			// Create adopted companion process
			ctx, cancel := context.WithCancel(context.Background())
			broadcaster := NewLogBroadcaster(core.Config.Companion.HistorySize)

			proc := &CompanionProcess{
				TunnelAlias:  tunnelInfo.Alias,
				Name:         compInfo.Name,
				Config:       *config,
				Cmd:          nil, // No cmd for adopted processes
				Pid:          compInfo.Pid,
				StartTime:    compInfo.StartTime,
				State:        CompanionStateRunning,
				output:       broadcaster,
				socketPath:   socketPath,
				socketListen: listener,
				ctx:          ctx,
				cancel:       cancel,
			}

			cm.mu.Lock()
			cm.companions[tunnelInfo.Alias][compInfo.Name] = proc
			cm.mu.Unlock()

			// Start listening for wrapper output (wrapper will reconnect)
			go cm.listenForWrapperOutput(proc)

			// Start monitoring the adopted process
			go cm.monitorAdoptedCompanion(proc, process)

			slog.Info("Adopted companion process",
				"tunnel", tunnelInfo.Alias,
				"companion", compInfo.Name,
				"pid", compInfo.Pid)
			cm.logCompanionEvent(tunnelInfo.Alias, compInfo.Name, "companion_adopted", fmt.Sprintf("PID: %d", compInfo.Pid))
			adoptedCount++
		}
	}

	// Clean up state file after adoption
	if err := RemoveCompanionStateFile(); err != nil {
		slog.Warn("Failed to remove companion state file", "error", err)
	}

	return adoptedCount
}

// monitorAdoptedCompanion monitors an adopted companion process
func (cm *CompanionManager) monitorAdoptedCompanion(proc *CompanionProcess, osProc *os.Process) {
	slog.Debug("Started monitoring adopted companion",
		"tunnel", proc.TunnelAlias,
		"companion", proc.Name,
		"pid", proc.Pid,
		"osProc.Pid", osProc.Pid)

	// Poll for process exit since we don't have cmd.Wait()
	for {
		time.Sleep(1 * time.Second)

		proc.mu.RLock()
		state := proc.State
		proc.mu.RUnlock()

		if state == CompanionStateStopped {
			slog.Debug("Stopping monitor - companion was stopped",
				"tunnel", proc.TunnelAlias,
				"companion", proc.Name)
			return
		}

		// Check if process is still alive
		if err := osProc.Signal(syscall.Signal(0)); err != nil {
			slog.Debug("Signal(0) failed for adopted companion",
				"tunnel", proc.TunnelAlias,
				"companion", proc.Name,
				"pid", proc.Pid,
				"error", err)

			proc.mu.Lock()
			if proc.State == CompanionStateStopped {
				proc.mu.Unlock()
				return
			}

			alias := proc.TunnelAlias
			name := proc.Name
			pid := proc.Pid
			autoRestart := proc.Config.AutoRestart

			slog.Info("Adopted companion exited",
				"tunnel", alias,
				"companion", name,
				"pid", pid)

			if !autoRestart {
				proc.State = CompanionStateExited
				proc.mu.Unlock()
				cm.logCompanionEvent(alias, name, "companion_exited", fmt.Sprintf("adopted process PID %d", pid))
				return
			}

			// Auto-restart is enabled
			proc.mu.Unlock()
			cm.logCompanionEvent(alias, name, "companion_exited", fmt.Sprintf("adopted process PID %d (will restart)", pid))

			// Brief delay before restart
			time.Sleep(1 * time.Second)

			// Check if we should still restart
			proc.mu.Lock()
			if proc.State == CompanionStateStopped {
				proc.mu.Unlock()
				return
			}
			proc.mu.Unlock()

			slog.Info("Auto-restarting adopted companion",
				"tunnel", alias,
				"companion", name)
			cm.logCompanionEvent(alias, name, "companion_restarting", "auto-restart triggered")

			// Restart the companion
			if err := cm.restartCompanionInPlace(proc); err != nil {
				slog.Error("Failed to auto-restart adopted companion",
					"tunnel", alias,
					"companion", name,
					"error", err)
				proc.mu.Lock()
				proc.State = CompanionStateFailed
				proc.ExitError = fmt.Sprintf("auto-restart failed: %v", err)
				proc.mu.Unlock()
				cm.logCompanionEvent(alias, name, "companion_failed", fmt.Sprintf("auto-restart failed: %v", err))
				return
			}

			// After restart, proc.Cmd is set, switch to regular monitoring
			go cm.monitorCompanion(proc)
			return
		}
	}
}
