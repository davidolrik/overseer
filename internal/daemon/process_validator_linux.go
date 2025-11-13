// +build linux

package daemon

import (
	"fmt"
	"os"
	"strings"
)

// getProcessCommandLinePlatform reads /proc/<pid>/cmdline on Linux
// The cmdline file contains null-separated arguments
func getProcessCommandLinePlatform(pid int) (string, error) {
	// Read /proc/<pid>/cmdline
	cmdlinePath := fmt.Sprintf("/proc/%d/cmdline", pid)
	data, err := os.ReadFile(cmdlinePath)
	if err != nil {
		return "", fmt.Errorf("failed to read %s: %w", cmdlinePath, err)
	}

	// Parse null-separated command line
	// Convert null bytes to spaces for easier matching
	cmdline := string(data)
	cmdline = strings.ReplaceAll(cmdline, "\x00", " ")
	cmdline = strings.TrimSpace(cmdline)

	if cmdline == "" {
		return "", fmt.Errorf("empty command line for PID %d", pid)
	}

	return cmdline, nil
}
