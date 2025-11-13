package daemon

import (
	"log/slog"
	"os"
	"strings"
	"syscall"
)

// ValidateTunnelProcess checks if a PID is still running and matches the expected tunnel process
// This prevents adopting wrong processes (PID reuse) or processes that have changed
func ValidateTunnelProcess(info TunnelInfo) bool {
	// Step 1: Check if process exists
	process, err := os.FindProcess(info.PID)
	if err != nil {
		slog.Debug("Process not found", "pid", info.PID, "alias", info.Alias)
		return false
	}

	// Send signal 0 (null signal) - just checks if process exists and we can signal it
	if err := process.Signal(syscall.Signal(0)); err != nil {
		slog.Debug("Process not accessible", "pid", info.PID, "alias", info.Alias, "error", err)
		return false
	}

	// Step 2: Verify command line matches expected (platform-specific)
	if !verifyProcessCommandLine(info.PID, info.Cmdline, info.Alias) {
		return false
	}

	slog.Debug("Process validated successfully", "pid", info.PID, "alias", info.Alias)
	return true
}

// matchesCommandLine checks if the actual command line contains the expected components
// We use "contains" matching because the actual cmdline might have additional args
func matchesCommandLine(actual string, expected []string) bool {
	// Must contain "ssh" and the alias (first positional arg after ssh)
	if !strings.Contains(actual, "ssh") {
		return false
	}

	// Check that key arguments are present
	// For SSH tunnels, we expect: ssh <alias> -N -o ExitOnForwardFailure=yes
	for _, arg := range expected {
		// Skip common flags that might appear in different order
		if arg == "ssh" || arg == "-v" {
			continue
		}

		if !strings.Contains(actual, arg) {
			return false
		}
	}

	return true
}

// verifyProcessCommandLine is implemented in platform-specific files:
// - process_validator_linux.go
// - process_validator_darwin.go
func verifyProcessCommandLine(pid int, expectedCmdline []string, alias string) bool {
	cmdline, err := getProcessCommandLine(pid)
	if err != nil {
		slog.Debug("Failed to get process command line",
			"pid", pid,
			"alias", alias,
			"error", err)
		return false
	}

	if !matchesCommandLine(cmdline, expectedCmdline) {
		slog.Debug("Process command line mismatch",
			"pid", pid,
			"alias", alias,
			"expected", strings.Join(expectedCmdline, " "),
			"actual", cmdline)
		return false
	}

	return true
}

// getProcessCommandLine is implemented in platform-specific files
// It returns the full command line string for the given PID
func getProcessCommandLine(pid int) (string, error) {
	return getProcessCommandLinePlatform(pid)
}
