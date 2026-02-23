package state

import (
	"log/slog"
	"testing"
)

func TestSleepMonitorCreation(t *testing.T) {
	m := NewSleepMonitor(nil, nil, nil)

	if m == nil {
		t.Fatal("Expected non-nil SleepMonitor")
	}
	if m.IsSleeping() {
		t.Error("Expected IsSleeping()=false for new monitor")
	}
}

func TestSleepMonitorWithLogger(t *testing.T) {
	logger := slog.Default()
	m := NewSleepMonitor(logger, nil, nil)

	if m.logger != logger {
		t.Error("Expected provided logger to be used")
	}
}

func TestSleepMonitorNilLoggerUsesDefault(t *testing.T) {
	m := NewSleepMonitor(nil, nil, nil)

	if m.logger == nil {
		t.Error("Expected default logger when nil is provided")
	}
}

func TestSleepMonitorMarkSleep(t *testing.T) {
	sleepCalled := false
	m := NewSleepMonitor(nil, func() { sleepCalled = true }, nil)

	m.markSleep()

	if !m.IsSleeping() {
		t.Error("Expected IsSleeping()=true after markSleep()")
	}
	if !sleepCalled {
		t.Error("Expected onSleep callback to be called")
	}
}

func TestSleepMonitorMarkWake(t *testing.T) {
	wakeCalled := false
	m := NewSleepMonitor(nil, nil, func() { wakeCalled = true })

	// Must be sleeping first
	m.markSleep()
	m.markWake()

	if m.IsSleeping() {
		t.Error("Expected IsSleeping()=false after markWake()")
	}
	if !wakeCalled {
		t.Error("Expected onWake callback to be called")
	}
}

func TestSleepMonitorMarkWakeWhenNotSleeping(t *testing.T) {
	wakeCalled := false
	m := NewSleepMonitor(nil, nil, func() { wakeCalled = true })

	// markWake when already awake should be a no-op
	m.markWake()

	if wakeCalled {
		t.Error("Expected onWake callback NOT to be called when not sleeping")
	}
}

func TestSleepMonitorIsSuppressedWhenSleeping(t *testing.T) {
	m := NewSleepMonitor(nil, nil, nil)

	m.markSleep()

	if !m.IsSuppressed() {
		t.Error("Expected IsSuppressed()=true when sleeping")
	}
}

func TestSleepMonitorIsSuppressedDuringGracePeriod(t *testing.T) {
	m := NewSleepMonitor(nil, nil, nil)

	// Sleep then wake - grace period is 10 seconds
	m.markSleep()
	m.markWake()

	// Should be suppressed during grace period
	if !m.IsSuppressed() {
		t.Error("Expected IsSuppressed()=true during grace period after wake")
	}
}

func TestSleepMonitorNilCallbacks(t *testing.T) {
	m := NewSleepMonitor(nil, nil, nil)

	// Should not panic with nil callbacks
	m.markSleep()
	m.markWake()
}
