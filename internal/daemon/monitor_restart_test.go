package daemon

import (
	"context"
	"os"
	"os/exec"
	"syscall"
	"testing"
	"time"

	"go.olrik.dev/overseer/internal/core"
)

func TestMonitorAdoptedCompanion_AutoRestart(t *testing.T) {
	quietLogger(t)

	oldConfig := core.Config
	t.Cleanup(func() { core.Config = oldConfig })
	core.Config = &core.Configuration{
		Companion: core.CompanionSettings{HistorySize: 50},
	}

	cm := NewCompanionManager()

	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)

	proc := &CompanionProcess{
		Name:        "restart-comp",
		TunnelAlias: "test-tunnel",
		Pid:         999999999, // Dead PID
		State:       CompanionStateRunning,
		Config: core.CompanionConfig{
			Name:        "restart-comp",
			Command:     "echo hello",
			AutoRestart: true,
		},
		output: NewLogBroadcaster(100),
		ctx:    ctx,
		cancel: cancel,
	}

	osProc, err := os.FindProcess(999999999)
	if err != nil {
		t.Fatalf("FindProcess failed: %v", err)
	}

	done := make(chan struct{})
	go func() {
		defer close(done)
		cm.monitorAdoptedCompanion(proc, osProc)
	}()

	select {
	case <-done:
	case <-time.After(15 * time.Second):
		t.Fatal("monitorAdoptedCompanion did not return")
	}

	// After auto-restart failure (restartCompanionInPlace will fail),
	// state should be Failed
	proc.mu.RLock()
	state := proc.State
	proc.mu.RUnlock()

	if state != CompanionStateFailed {
		t.Errorf("expected state Failed after restart failure, got %v", state)
	}
}

func TestMonitorCompanion_AutoRestart_Failed(t *testing.T) {
	quietLogger(t)

	oldConfig := core.Config
	t.Cleanup(func() { core.Config = oldConfig })
	core.Config = &core.Configuration{
		Companion: core.CompanionSettings{HistorySize: 50},
	}

	cm := NewCompanionManager()

	// Start a process that exits immediately
	cmd := exec.Command("true")
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	if err := cmd.Start(); err != nil {
		t.Fatalf("failed to start process: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)

	proc := &CompanionProcess{
		Name:        "restart-comp",
		TunnelAlias: "test-tunnel",
		Cmd:         cmd,
		Pid:         cmd.Process.Pid,
		State:       CompanionStateRunning,
		Config: core.CompanionConfig{
			Name:        "restart-comp",
			Command:     "echo hello",
			AutoRestart: true,
		},
		output: NewLogBroadcaster(100),
		ctx:    ctx,
		cancel: cancel,
	}

	done := make(chan struct{})
	go func() {
		defer close(done)
		cm.monitorCompanion(proc)
	}()

	select {
	case <-done:
	case <-time.After(15 * time.Second):
		t.Fatal("monitorCompanion did not return")
	}

	proc.mu.RLock()
	state := proc.State
	proc.mu.RUnlock()

	if state != CompanionStateFailed {
		t.Errorf("expected state Failed after restart failure, got %v", state)
	}
}

func TestMonitorTunnel_ProcessExitsMaxRetriesWithDB(t *testing.T) {
	quietLogger(t)

	oldConfig := core.Config
	t.Cleanup(func() { core.Config = oldConfig })
	core.Config = &core.Configuration{
		Companion: core.CompanionSettings{HistorySize: 50},
		SSH:       core.SSHConfig{MaxRetries: 2},
	}

	d := New()
	d.ctx, d.cancelFunc = context.WithCancel(context.Background())
	t.Cleanup(d.cancelFunc)

	cmd := exec.Command("false")
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	if err := cmd.Start(); err != nil {
		t.Fatalf("failed to start process: %v", err)
	}

	d.tunnels["max-retry"] = Tunnel{
		Hostname:      "test.example.com",
		Pid:           cmd.Process.Pid,
		Cmd:           cmd,
		State:         StateConnected,
		AutoReconnect: true,
		RetryCount:    2, // At max
		AskpassToken:  "tok-1",
	}
	d.askpassTokens["tok-1"] = "max-retry"

	done := make(chan struct{})
	go func() {
		defer close(done)
		d.monitorTunnel("max-retry")
	}()

	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("monitorTunnel did not return")
	}

	// Askpass token should be cleaned up
	if _, exists := d.askpassTokens["tok-1"]; exists {
		t.Error("expected askpass token to be cleaned up on max retries")
	}
}

func TestMonitorAdoptedTunnel_PIDReplacedDuringMonitor(t *testing.T) {
	quietLogger(t)

	oldConfig := core.Config
	t.Cleanup(func() { core.Config = oldConfig })
	core.Config = &core.Configuration{
		Companion: core.CompanionSettings{HistorySize: 50},
		SSH:       core.SSHConfig{MaxRetries: 0},
	}

	d := New()
	d.ctx, d.cancelFunc = context.WithCancel(context.Background())
	t.Cleanup(d.cancelFunc)

	d.tunnels["replaced-tunnel"] = Tunnel{
		Hostname: "test.example.com",
		Pid:      999999999, // Dead PID, will be detected
		State:    StateConnected,
	}

	done := make(chan struct{})
	go func() {
		defer close(done)
		d.monitorAdoptedTunnel("replaced-tunnel", 999999999)
	}()

	// Wait for monitor to detect dead PID, but replace the tunnel's PID first
	time.Sleep(3 * time.Second)
	d.mu.Lock()
	if t, exists := d.tunnels["replaced-tunnel"]; exists {
		t.Pid = 12345 // Different PID from what monitor is watching
		d.tunnels["replaced-tunnel"] = t
	}
	d.mu.Unlock()

	select {
	case <-done:
	case <-time.After(15 * time.Second):
		t.Fatal("monitorAdoptedTunnel did not return")
	}
}
