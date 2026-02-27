package daemon

import (
	"context"
	"log/slog"
	"os"
	"os/exec"
	"syscall"
	"testing"
	"time"

	"go.olrik.dev/overseer/internal/awareness/state"
	"go.olrik.dev/overseer/internal/core"
)

// setOnlineOrchestrator creates a minimal state orchestrator that reports online=true
// and sets the package-level stateOrchestrator for the duration of the test.
func setOnlineOrchestrator(t *testing.T) {
	t.Helper()

	old := stateOrchestrator
	t.Cleanup(func() { stateOrchestrator = old })

	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.Level(99)}))
	orch := state.NewOrchestrator(state.OrchestratorConfig{
		Logger: logger,
	})

	online := true
	orch.RestoreSensorCache([]state.SensorCacheEntry{
		{
			Sensor:    "tcp",
			Timestamp: time.Now().Format(time.RFC3339Nano),
			Online:    &online,
		},
	})

	stateOrchestrator = orch
}

// TestMonitorTunnel_TunnelReplacedDuringBackoff verifies that when a context change
// replaces a tunnel while monitorTunnel is sleeping during reconnect backoff,
// the old monitor goroutine detects the replacement and exits instead of
// overwriting the new tunnel entry (which would orphan the new SSH process).
func TestMonitorTunnel_TunnelReplacedDuringBackoff(t *testing.T) {
	quietLogger(t)
	setOnlineOrchestrator(t)

	oldConfig := core.Config
	t.Cleanup(func() { core.Config = oldConfig })
	core.Config = &core.Configuration{
		Companion: core.CompanionSettings{HistorySize: 50},
		SSH: core.SSHConfig{
			MaxRetries:     3,
			InitialBackoff: "2s", // Long enough for us to replace the tunnel during the sleep
		},
	}

	d := New()
	d.ctx, d.cancelFunc = context.WithCancel(context.Background())
	t.Cleanup(d.cancelFunc)

	// Start a short-lived process that exits immediately
	cmd := exec.Command("true")
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	if err := cmd.Start(); err != nil {
		t.Fatalf("failed to start process: %v", err)
	}

	d.tunnels["jump-zero"] = Tunnel{
		Hostname:      "jump-zero.example.com",
		Pid:           cmd.Process.Pid,
		Cmd:           cmd,
		State:         StateConnected,
		AutoReconnect: true,
	}

	done := make(chan struct{})
	go func() {
		defer close(done)
		d.monitorTunnel("jump-zero")
	}()

	// Wait for monitorTunnel to detect the process exit and enter backoff sleep.
	// The process exits immediately ("true"), so cmd.Wait() returns quickly.
	// Then the online check passes, backoff is calculated, and it starts sleeping.
	time.Sleep(500 * time.Millisecond)

	// Simulate a context change replacing the tunnel while monitor is sleeping.
	// Create a new long-lived process to represent the context change's tunnel.
	replacementCmd := exec.Command("sleep", "60")
	replacementCmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	if err := replacementCmd.Start(); err != nil {
		t.Fatalf("failed to start replacement process: %v", err)
	}
	t.Cleanup(func() { replacementCmd.Process.Kill() })

	d.mu.Lock()
	d.tunnels["jump-zero"] = Tunnel{
		Hostname:      "jump-zero.example.com",
		Pid:           replacementCmd.Process.Pid,
		Cmd:           replacementCmd,
		State:         StateConnected,
		AutoReconnect: true,
	}
	d.mu.Unlock()

	// Wait for the old monitorTunnel to wake from backoff and detect the replacement.
	// Backoff is 2s, we replaced after 500ms, so it should wake at ~2s and exit.
	select {
	case <-done:
		// monitorTunnel detected the replacement and exited
	case <-time.After(10 * time.Second):
		t.Fatal("monitorTunnel did not return after tunnel was replaced during backoff")
	}

	// Verify the replacement tunnel is still in the map and wasn't overwritten
	d.mu.Lock()
	tunnel, exists := d.tunnels["jump-zero"]
	d.mu.Unlock()

	if !exists {
		t.Fatal("replacement tunnel was removed from map")
	}
	if tunnel.Cmd != replacementCmd {
		t.Error("replacement tunnel's Cmd was overwritten by old monitor goroutine")
	}
	if tunnel.Pid != replacementCmd.Process.Pid {
		t.Errorf("replacement tunnel PID was overwritten: got %d, want %d",
			tunnel.Pid, replacementCmd.Process.Pid)
	}
}

// TestMonitorTunnel_NilCmdCleanup verifies that if a tunnel entry has a nil Cmd
// (e.g., from a failed Start() on a previous reconnect attempt), monitorTunnel
// cleans up the entry and exits instead of panicking.
func TestMonitorTunnel_NilCmdCleanup(t *testing.T) {
	quietLogger(t)

	oldConfig := core.Config
	t.Cleanup(func() { core.Config = oldConfig })
	core.Config = &core.Configuration{
		Companion: core.CompanionSettings{HistorySize: 50},
		SSH:       core.SSHConfig{MaxRetries: 3},
	}

	d := New()
	d.ctx, d.cancelFunc = context.WithCancel(context.Background())
	t.Cleanup(d.cancelFunc)

	// Create a tunnel entry with nil Cmd (simulates a failed Start())
	d.tunnels["nil-cmd"] = Tunnel{
		Hostname:      "nil-cmd.example.com",
		Pid:           12345,
		Cmd:           nil,
		State:         StateReconnecting,
		AutoReconnect: true,
	}

	done := make(chan struct{})
	go func() {
		defer close(done)
		d.monitorTunnel("nil-cmd")
	}()

	select {
	case <-done:
		// monitorTunnel exited cleanly instead of panicking
	case <-time.After(5 * time.Second):
		t.Fatal("monitorTunnel did not return for nil Cmd tunnel")
	}

	// Tunnel should be removed from the map
	d.mu.Lock()
	_, exists := d.tunnels["nil-cmd"]
	d.mu.Unlock()
	if exists {
		t.Error("expected tunnel with nil Cmd to be removed from map")
	}
}

// TestMonitorAdoptedTunnel_TunnelReplacedDuringBackoff verifies that when a context
// change replaces an adopted tunnel while monitorAdoptedTunnel is sleeping during
// reconnect backoff, the old monitor detects the replacement and exits.
func TestMonitorAdoptedTunnel_TunnelReplacedDuringBackoff(t *testing.T) {
	quietLogger(t)
	setOnlineOrchestrator(t)

	oldConfig := core.Config
	t.Cleanup(func() { core.Config = oldConfig })
	core.Config = &core.Configuration{
		Companion: core.CompanionSettings{HistorySize: 50},
		SSH: core.SSHConfig{
			MaxRetries:     3,
			InitialBackoff: "2s",
		},
	}

	d := New()
	d.ctx, d.cancelFunc = context.WithCancel(context.Background())
	t.Cleanup(d.cancelFunc)

	originalPID := 999999999 // Non-existent PID, will be detected as dead

	d.tunnels["adopted-jump"] = Tunnel{
		Hostname:      "adopted-jump.example.com",
		Pid:           originalPID,
		Cmd:           nil, // Adopted tunnels have nil Cmd
		State:         StateConnected,
		AutoReconnect: true,
	}

	done := make(chan struct{})
	go func() {
		defer close(done)
		d.monitorAdoptedTunnel("adopted-jump", originalPID)
	}()

	// Wait for monitor to detect dead PID and enter backoff sleep.
	// The monitor checks every 5s, so we need to wait for at least one tick
	// plus a bit for processing.
	time.Sleep(6 * time.Second)

	// Simulate a context change replacing the tunnel with a different PID
	replacementCmd := exec.Command("sleep", "60")
	replacementCmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	if err := replacementCmd.Start(); err != nil {
		t.Fatalf("failed to start replacement process: %v", err)
	}
	t.Cleanup(func() { replacementCmd.Process.Kill() })

	d.mu.Lock()
	d.tunnels["adopted-jump"] = Tunnel{
		Hostname:      "adopted-jump.example.com",
		Pid:           replacementCmd.Process.Pid,
		Cmd:           replacementCmd,
		State:         StateConnected,
		AutoReconnect: true,
	}
	d.mu.Unlock()

	// Wait for the old monitor to wake from backoff and detect the PID change
	select {
	case <-done:
		// monitorAdoptedTunnel detected the replacement and exited
	case <-time.After(15 * time.Second):
		t.Fatal("monitorAdoptedTunnel did not return after tunnel was replaced during backoff")
	}

	// Verify the replacement tunnel is still intact
	d.mu.Lock()
	tunnel, exists := d.tunnels["adopted-jump"]
	d.mu.Unlock()

	if !exists {
		t.Fatal("replacement tunnel was removed from map")
	}
	if tunnel.Pid != replacementCmd.Process.Pid {
		t.Errorf("replacement tunnel PID was overwritten: got %d, want %d",
			tunnel.Pid, replacementCmd.Process.Pid)
	}
}
