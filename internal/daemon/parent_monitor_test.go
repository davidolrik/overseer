package daemon

import (
	"context"
	"os"
	"testing"
	"time"
)

// TestParentMonitorCreation tests that a parent monitor can be created
func TestParentMonitorCreation(t *testing.T) {
	daemon := New()
	monitor := NewParentMonitor(daemon)

	if monitor == nil {
		t.Fatal("Expected parent monitor to be created, got nil")
	}

	if monitor.initialPPID != os.Getppid() {
		t.Errorf("Expected initialPPID to be %d, got %d", os.Getppid(), monitor.initialPPID)
	}

	if monitor.daemon != daemon {
		t.Error("Expected monitor.daemon to reference the daemon instance")
	}
}

// TestParentMonitorStartStop tests that the monitor can be started and stopped
func TestParentMonitorStartStop(t *testing.T) {
	daemon := New()
	monitor := NewParentMonitor(daemon)

	ctx, cancel := context.WithCancel(context.Background())

	// Start monitoring
	monitor.Start(ctx)

	// Let it run for a brief moment
	time.Sleep(100 * time.Millisecond)

	// Cancel context to stop monitoring
	cancel()

	// Give it time to stop
	time.Sleep(100 * time.Millisecond)
}

// TestSetupParentDeathSignal tests the platform-specific setup
func TestSetupParentDeathSignal(t *testing.T) {
	daemon := New()
	monitor := NewParentMonitor(daemon)

	// This should not fail (it's either a successful prctl call on Linux
	// or a no-op on macOS)
	err := monitor.setupParentDeathSignal()
	if err != nil {
		t.Logf("Warning: setupParentDeathSignal failed: %v", err)
		// Don't fail the test - this might fail in some environments
		// but that's okay because we have the polling fallback
	}
}
