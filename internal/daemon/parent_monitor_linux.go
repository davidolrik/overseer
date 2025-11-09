//go:build linux

package daemon

import (
	"fmt"
	"syscall"
)

// setupParentDeathSignal sets up platform-specific parent death detection
// On Linux, we use prctl(PR_SET_PDEATHSIG) which is a kernel-level guarantee
// that we'll receive a signal when our parent process dies.
func (pm *ParentMonitor) setupParentDeathSignal() error {
	// PR_SET_PDEATHSIG = 1
	// SIGTERM = 15
	// This tells the kernel: "send me SIGTERM when my parent dies"
	err := syscall.Prctl(syscall.PR_SET_PDEATHSIG, uintptr(syscall.SIGTERM), 0, 0, 0)
	if err != nil {
		return fmt.Errorf("prctl(PR_SET_PDEATHSIG) failed: %w", err)
	}

	pm.logger.Info("Parent death signal configured",
		"signal", "SIGTERM",
		"mechanism", "prctl(PR_SET_PDEATHSIG)")

	return nil
}
