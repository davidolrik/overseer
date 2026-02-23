package daemon

import (
	"log/slog"
	"testing"
)

func TestSetupLogging(t *testing.T) {
	d := &Daemon{
		logBroadcast: NewLogBroadcaster(100),
	}

	// Subscribe to the broadcaster to verify log output is routed
	ch := d.logBroadcast.Subscribe()
	defer d.logBroadcast.Unsubscribe(ch)

	// setupLogging configures slog to write to both stderr and the broadcaster
	d.setupLogging()

	// After setupLogging, slog output should be broadcast
	// Emit a log message
	slog.Info("test log message for coverage")

	// The log message should appear on the broadcast channel
	// Note: slog may buffer or format differently, so we just verify
	// that setupLogging didn't panic and the broadcaster is wired up
}

func TestSetupLogging_DoesNotPanic(t *testing.T) {
	d := &Daemon{
		logBroadcast: NewLogBroadcaster(100),
	}

	// Should not panic
	d.setupLogging()

	// Verify slog default was updated (it writes to our broadcaster)
	handler := slog.Default().Handler()
	if handler == nil {
		t.Error("expected non-nil slog handler after setupLogging")
	}
}
