//go:build darwin

package daemon

// setupParentDeathSignal sets up platform-specific parent death detection
// On macOS/Darwin, there is no equivalent to Linux's prctl(PR_SET_PDEATHSIG).
// We rely entirely on PPID polling in pollParentStatus().
func (pm *ParentMonitor) setupParentDeathSignal() error {
	pm.logger.Info("Parent death detection using PPID polling",
		"mechanism", "polling",
		"interval", "5s",
		"note", "macOS lacks PR_SET_PDEATHSIG equivalent")

	// No-op on macOS - we rely on the polling mechanism
	return nil
}
