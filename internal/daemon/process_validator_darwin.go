//go:build darwin
// +build darwin

package daemon

import (
	"fmt"
	"os/exec"
	"strconv"
	"strings"
)

// getProcessCommandLinePlatform uses ps command on macOS
// macOS doesn't have /proc, so we use ps to get process information
func getProcessCommandLinePlatform(pid int) (string, error) {
	// Use ps to get the full command line
	// -p: process ID
	// -o command=: output only command column, no header
	cmd := exec.Command("/bin/ps", "-p", strconv.Itoa(pid), "-o", "command=")
	output, err := cmd.Output()
	if err != nil {
		return "", fmt.Errorf("ps command failed for PID %d: %w", pid, err)
	}

	cmdline := strings.TrimSpace(string(output))

	if cmdline == "" {
		return "", fmt.Errorf("empty command line for PID %d", pid)
	}

	return cmdline, nil
}
