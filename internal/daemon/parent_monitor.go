package daemon

import (
	"context"
	"log/slog"
	"os"
	"time"
)

// ParentMonitor monitors the parent process and triggers shutdown when it dies
type ParentMonitor struct {
	initialPPID int
	daemon      *Daemon
	logger      *slog.Logger
}

// NewParentMonitor creates a new parent process monitor
func NewParentMonitor(daemon *Daemon) *ParentMonitor {
	return &ParentMonitor{
		initialPPID: os.Getppid(),
		daemon:      daemon,
		logger:      slog.Default(),
	}
}

// Start begins monitoring the parent process
// This implements a multi-layer detection strategy:
// 1. SIGHUP (already handled in daemon.Run())
// 2. Platform-specific parent death signal (Linux: prctl)
// 3. PPID polling fallback (all platforms)
func (pm *ParentMonitor) Start(ctx context.Context) {
	pm.logger.Info("Starting parent process monitor",
		"ppid", pm.initialPPID,
		"mode", "remote")

	// Layer 2: Set up platform-specific parent death detection
	// On Linux, this uses PR_SET_PDEATHSIG which is kernel-level
	// On macOS/BSD, this is a no-op (we rely on polling)
	if err := pm.setupParentDeathSignal(); err != nil {
		pm.logger.Warn("Failed to set up parent death signal, relying on polling",
			"error", err)
	}

	// Layer 3: PPID polling - works on all platforms
	// This catches cases where SIGHUP doesn't work (nohup, screen, tmux, etc.)
	go pm.pollParentStatus(ctx)
}

// pollParentStatus periodically checks if the parent process is still alive
func (pm *ParentMonitor) pollParentStatus(ctx context.Context) {
	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			pm.logger.Debug("Parent monitor stopping (context cancelled)")
			return

		case <-ticker.C:
			currentPPID := os.Getppid()

			// Check if parent has changed
			// PPID of 1 means we've been reparented to init/launchd (parent died)
			// Any PPID change from initial means the original parent is gone
			if currentPPID != pm.initialPPID {
				pm.logger.Info("Parent process died - SSH session terminated",
					"initial_ppid", pm.initialPPID,
					"current_ppid", currentPPID)

				// Log to database
				if pm.daemon.database != nil {
					pm.daemon.database.LogDaemonEvent("parent_death",
						"Parent process terminated, daemon shutting down")
				}

				// Trigger graceful shutdown
				pm.daemon.shutdown()
				os.Exit(0)
			}

			pm.logger.Debug("Parent process check passed",
				"ppid", currentPPID)
		}
	}
}
