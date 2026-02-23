package daemon

import (
	"os/exec"
	"syscall"
	"testing"
	"time"
)

func TestGracefulTerminate_ProcessAlreadyDone(t *testing.T) {
	quietLogger(t)

	// Start a process that exits immediately
	cmd := exec.Command("true")
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	if err := cmd.Start(); err != nil {
		t.Fatalf("failed to start process: %v", err)
	}
	// Wait for it to finish
	cmd.Wait()

	// Process is already done - SIGTERM should fail gracefully
	err := gracefulTerminate(cmd.Process, 5*time.Second, "test-done")
	if err != nil {
		t.Errorf("expected nil error for already-done process, got: %v", err)
	}
}

func TestGracefulTerminate_ProcessExitsGracefully(t *testing.T) {
	quietLogger(t)

	// Start a process that will respond to SIGTERM
	cmd := exec.Command("sleep", "60")
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	if err := cmd.Start(); err != nil {
		t.Fatalf("failed to start process: %v", err)
	}
	go cmd.Wait()
	t.Cleanup(func() { cmd.Process.Kill() })

	err := gracefulTerminate(cmd.Process, 5*time.Second, "test-graceful")
	if err != nil {
		t.Errorf("expected nil error, got: %v", err)
	}
}
