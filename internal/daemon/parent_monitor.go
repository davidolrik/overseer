package daemon

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"strconv"
	"syscall"
	"time"
)

// ParentMonitor monitors a process and triggers shutdown when it dies
type ParentMonitor struct {
	monitoredPID int // The PID to monitor (might not be our direct parent)
	daemon       *Daemon
	logger       *slog.Logger
}

// NewParentMonitor creates a new parent process monitor
// If OVERSEER_MONITOR_PID env var is set, monitors that PID (SSH session)
// Otherwise monitors the daemon's actual parent PID
func NewParentMonitor(daemon *Daemon) *ParentMonitor {
	// Check if we should monitor a specific PID (set by 'overseer start')
	monitorPID := os.Getppid() // Default to actual parent

	if pidStr := os.Getenv("OVERSEER_MONITOR_PID"); pidStr != "" {
		if pid, err := strconv.Atoi(pidStr); err == nil {
			monitorPID = pid
			slog.Debug("Will monitor external PID from OVERSEER_MONITOR_PID",
				"monitor_pid", pid,
				"daemon_ppid", os.Getppid())
		}
	}

	return &ParentMonitor{
		monitoredPID: monitorPID,
		daemon:       daemon,
		logger:       slog.Default(),
	}
}

// Start begins monitoring the parent process
// This implements a multi-layer detection strategy:
// 1. SIGHUP (already handled in daemon.Run())
// 2. Platform-specific parent death signal (Linux: prctl) - only if monitoring actual parent
// 3. Process existence polling (all platforms)
func (pm *ParentMonitor) Start(ctx context.Context) {
	pm.logger.Info("Starting parent process monitor",
		"monitor_pid", pm.monitoredPID,
		"daemon_ppid", os.Getppid(),
		"mode", "remote")

	// Layer 2: Set up platform-specific parent death detection
	// Only works if we're monitoring our actual parent (not an external PID)
	if pm.monitoredPID == os.Getppid() {
		if err := pm.setupParentDeathSignal(); err != nil {
			pm.logger.Warn("Failed to set up parent death signal, relying on polling",
				"error", err)
		}
	} else {
		pm.logger.Info("Monitoring external process, using polling only",
			"external_pid", pm.monitoredPID)
	}

	// Layer 3: Process existence polling - works for any PID
	// This catches cases where SIGHUP doesn't work (nohup, screen, tmux, etc.)
	go pm.pollParentStatus(ctx)
}

// pollParentStatus periodically checks if the monitored process is still alive
func (pm *ParentMonitor) pollParentStatus(ctx context.Context) {
	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			pm.logger.Debug("Parent monitor stopping (context cancelled)")
			return

		case <-ticker.C:
			// Check if the monitored process still exists
			// syscall.Kill(pid, 0) doesn't send a signal, just checks if process exists
			err := syscall.Kill(pm.monitoredPID, 0)

			if err != nil {
				// Process doesn't exist or we can't signal it
				pm.logger.Info("Monitored process died - SSH session terminated",
					"monitor_pid", pm.monitoredPID,
					"error", err)

				// Log to database
				if pm.daemon.database != nil {
					pm.daemon.database.LogDaemonEvent("parent_death",
						fmt.Sprintf("Monitored process %d terminated, daemon shutting down", pm.monitoredPID))
				}

				// Trigger graceful shutdown
				pm.daemon.shutdown()
				os.Exit(0)
			}

			pm.logger.Debug("Monitored process check passed",
				"monitor_pid", pm.monitoredPID)
		}
	}
}
