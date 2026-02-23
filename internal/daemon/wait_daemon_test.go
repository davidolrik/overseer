package daemon

import (
	"os"
	"os/exec"
	"strings"
	"testing"

	"go.olrik.dev/overseer/internal/core"
)

func TestWaitForDaemon_ProcessCrashesImmediately(t *testing.T) {
	quietLogger(t)

	// Start a command that exits immediately with error
	cmd := exec.Command("false")

	// Set up stderr capture file like StartDaemon does
	stderrFile, err := os.CreateTemp("", "test-daemon-stderr-*")
	if err != nil {
		t.Fatalf("failed to create temp file: %v", err)
	}
	cmd.Stderr = stderrFile

	if err := cmd.Start(); err != nil {
		stderrFile.Close()
		os.Remove(stderrFile.Name())
		t.Fatalf("failed to start command: %v", err)
	}

	// Use a temp dir for socket path so WaitForDaemon can't actually connect
	tmpDir := shortTempDir(t)
	oldConfig := core.Config
	t.Cleanup(func() { core.Config = oldConfig })
	core.Config = &core.Configuration{
		ConfigPath: tmpDir,
	}

	err = WaitForDaemon(cmd)
	if err == nil {
		t.Fatal("expected error when daemon crashes")
	}
	if !strings.Contains(err.Error(), "crashed during startup") {
		t.Errorf("expected 'crashed during startup' error, got: %v", err)
	}
}

func TestWaitForDaemon_ProcessCrashesWithStderr(t *testing.T) {
	quietLogger(t)

	// Start a command that writes to stderr and exits
	cmd := exec.Command("sh", "-c", "echo 'fatal error: something broke' >&2; exit 1")

	stderrFile, err := os.CreateTemp("", "test-daemon-stderr-*")
	if err != nil {
		t.Fatalf("failed to create temp file: %v", err)
	}
	cmd.Stderr = stderrFile

	if err := cmd.Start(); err != nil {
		stderrFile.Close()
		os.Remove(stderrFile.Name())
		t.Fatalf("failed to start command: %v", err)
	}

	tmpDir := shortTempDir(t)
	oldConfig := core.Config
	t.Cleanup(func() { core.Config = oldConfig })
	core.Config = &core.Configuration{
		ConfigPath: tmpDir,
	}

	err = WaitForDaemon(cmd)
	if err == nil {
		t.Fatal("expected error when daemon crashes with stderr")
	}
	if !strings.Contains(err.Error(), "crashed during startup") {
		t.Errorf("expected 'crashed during startup' error, got: %v", err)
	}
	if !strings.Contains(err.Error(), "fatal error") {
		t.Errorf("expected stderr content in error, got: %v", err)
	}
}
