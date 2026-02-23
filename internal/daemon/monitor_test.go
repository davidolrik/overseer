package daemon

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"syscall"
	"testing"
	"time"

	"go.olrik.dev/overseer/internal/core"
	"go.olrik.dev/overseer/internal/db"
)

func TestMonitorTunnel_ProcessExitsNoReconnect(t *testing.T) {
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

	// Start a process that exits immediately
	cmd := exec.Command("true")
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	if err := cmd.Start(); err != nil {
		t.Fatalf("failed to start process: %v", err)
	}

	d.tunnels["test-tunnel"] = Tunnel{
		Hostname:      "test.example.com",
		Pid:           cmd.Process.Pid,
		Cmd:           cmd,
		State:         StateConnected,
		AutoReconnect: false, // No auto-reconnect
	}

	// monitorTunnel will call cmd.Wait(), see the exit,
	// and since AutoReconnect is false, it will clean up and return
	done := make(chan struct{})
	go func() {
		defer close(done)
		d.monitorTunnel("test-tunnel")
	}()

	select {
	case <-done:
		// Good - monitor exited
	case <-time.After(5 * time.Second):
		t.Fatal("monitorTunnel did not return in time")
	}

	// Tunnel should be removed
	d.mu.Lock()
	_, exists := d.tunnels["test-tunnel"]
	d.mu.Unlock()
	if exists {
		t.Error("expected tunnel to be removed after process exit with no auto-reconnect")
	}
}

func TestMonitorTunnel_ProcessExitsMaxRetries(t *testing.T) {
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

	cmd := exec.Command("true")
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	if err := cmd.Start(); err != nil {
		t.Fatalf("failed to start process: %v", err)
	}

	d.tunnels["retry-tunnel"] = Tunnel{
		Hostname:      "test.example.com",
		Pid:           cmd.Process.Pid,
		Cmd:           cmd,
		State:         StateConnected,
		AutoReconnect: true,
		RetryCount:    3, // Already at max retries
	}

	done := make(chan struct{})
	go func() {
		defer close(done)
		d.monitorTunnel("retry-tunnel")
	}()

	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("monitorTunnel did not return in time")
	}

	d.mu.Lock()
	_, exists := d.tunnels["retry-tunnel"]
	d.mu.Unlock()
	if exists {
		t.Error("expected tunnel to be removed after max retries exceeded")
	}
}

func TestMonitorTunnel_ProcessExitsWithDatabase(t *testing.T) {
	quietLogger(t)

	tmpDir := t.TempDir()
	database, err := db.Open(filepath.Join(tmpDir, "test.db"))
	if err != nil {
		t.Fatalf("failed to open database: %v", err)
	}
	t.Cleanup(func() { database.Close() })

	oldConfig := core.Config
	t.Cleanup(func() { core.Config = oldConfig })
	core.Config = &core.Configuration{
		Companion: core.CompanionSettings{HistorySize: 50},
		SSH:       core.SSHConfig{MaxRetries: 3},
	}

	d := New()
	d.ctx, d.cancelFunc = context.WithCancel(context.Background())
	t.Cleanup(d.cancelFunc)
	d.database = database

	cmd := exec.Command("false") // Exit with error
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	if err := cmd.Start(); err != nil {
		t.Fatalf("failed to start process: %v", err)
	}

	d.tunnels["db-tunnel"] = Tunnel{
		Hostname:      "test.example.com",
		Pid:           cmd.Process.Pid,
		Cmd:           cmd,
		State:         StateConnected,
		AutoReconnect: false,
		AskpassToken:  "token-123",
	}
	d.askpassTokens["token-123"] = "db-tunnel"

	done := make(chan struct{})
	go func() {
		defer close(done)
		d.monitorTunnel("db-tunnel")
	}()

	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("monitorTunnel did not return in time")
	}

	// Verify askpass token was cleaned up
	if _, exists := d.askpassTokens["token-123"]; exists {
		t.Error("expected askpass token to be cleaned up")
	}
}

func TestMonitorTunnel_TunnelRemovedDuringWait(t *testing.T) {
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

	cmd := exec.Command("sleep", "60")
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	if err := cmd.Start(); err != nil {
		t.Fatalf("failed to start process: %v", err)
	}
	t.Cleanup(func() { cmd.Process.Kill() })

	d.tunnels["removed-tunnel"] = Tunnel{
		Hostname: "test.example.com",
		Pid:      cmd.Process.Pid,
		Cmd:      cmd,
		State:    StateConnected,
	}

	done := make(chan struct{})
	go func() {
		defer close(done)
		d.monitorTunnel("removed-tunnel")
	}()

	// Wait a bit for the goroutine to start then remove the tunnel and kill the process
	time.Sleep(100 * time.Millisecond)
	d.mu.Lock()
	delete(d.tunnels, "removed-tunnel")
	d.mu.Unlock()
	cmd.Process.Kill()

	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("monitorTunnel did not return in time after tunnel removal")
	}
}

func TestMonitorAdoptedTunnel_DeadPID(t *testing.T) {
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

	d.tunnels["adopted-dead"] = Tunnel{
		Hostname:      "test.example.com",
		Pid:           999999999, // Non-existent PID
		Cmd:           nil,
		State:         StateConnected,
		AutoReconnect: false,
	}

	done := make(chan struct{})
	go func() {
		defer close(done)
		d.monitorAdoptedTunnel("adopted-dead", 999999999)
	}()

	select {
	case <-done:
	case <-time.After(15 * time.Second):
		t.Fatal("monitorAdoptedTunnel did not return in time")
	}

	// Tunnel should be cleaned up
	d.mu.Lock()
	_, exists := d.tunnels["adopted-dead"]
	d.mu.Unlock()
	if exists {
		t.Error("expected adopted tunnel to be removed after dead PID detected")
	}
}

func TestMonitorAdoptedTunnel_ContextCancelled(t *testing.T) {
	quietLogger(t)

	oldConfig := core.Config
	t.Cleanup(func() { core.Config = oldConfig })
	core.Config = &core.Configuration{
		Companion: core.CompanionSettings{HistorySize: 50},
	}

	d := New()
	d.ctx, d.cancelFunc = context.WithCancel(context.Background())

	d.tunnels["cancel-tunnel"] = Tunnel{
		Hostname: "test.example.com",
		Pid:      os.Getpid(), // Our PID is alive
		Cmd:      nil,
		State:    StateConnected,
	}

	done := make(chan struct{})
	go func() {
		defer close(done)
		d.monitorAdoptedTunnel("cancel-tunnel", os.Getpid())
	}()

	// Cancel context after a short delay
	time.Sleep(100 * time.Millisecond)
	d.cancelFunc()

	select {
	case <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("monitorAdoptedTunnel did not return after context cancel")
	}
}

func TestMonitorCompanion_ExitNormally_NoRestart(t *testing.T) {
	quietLogger(t)

	cm := NewCompanionManager()

	// Start a process that exits immediately with success
	cmd := exec.Command("true")
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	if err := cmd.Start(); err != nil {
		t.Fatalf("failed to start process: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)

	proc := &CompanionProcess{
		Name:        "test-comp",
		TunnelAlias: "test-tunnel",
		Cmd:         cmd,
		Pid:         cmd.Process.Pid,
		State:       CompanionStateRunning,
		Config: core.CompanionConfig{
			Name:        "test-comp",
			AutoRestart: false,
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
	case <-time.After(5 * time.Second):
		t.Fatal("monitorCompanion did not return in time")
	}

	proc.mu.RLock()
	state := proc.State
	exitCode := proc.ExitCode
	proc.mu.RUnlock()

	if state != CompanionStateExited {
		t.Errorf("expected state Exited, got %v", state)
	}
	if exitCode == nil || *exitCode != 0 {
		t.Errorf("expected exit code 0, got %v", exitCode)
	}
}

func TestMonitorCompanion_ExitError_NoRestart(t *testing.T) {
	quietLogger(t)

	cm := NewCompanionManager()

	cmd := exec.Command("false")
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	if err := cmd.Start(); err != nil {
		t.Fatalf("failed to start process: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)

	proc := &CompanionProcess{
		Name:        "test-comp",
		TunnelAlias: "test-tunnel",
		Cmd:         cmd,
		Pid:         cmd.Process.Pid,
		State:       CompanionStateRunning,
		Config: core.CompanionConfig{
			Name:        "test-comp",
			AutoRestart: false,
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
	case <-time.After(5 * time.Second):
		t.Fatal("monitorCompanion did not return in time")
	}

	proc.mu.RLock()
	state := proc.State
	exitCode := proc.ExitCode
	exitError := proc.ExitError
	proc.mu.RUnlock()

	if state != CompanionStateExited {
		t.Errorf("expected state Exited, got %v", state)
	}
	if exitCode == nil || *exitCode == 0 {
		t.Errorf("expected non-zero exit code, got %v", exitCode)
	}
	if exitError == "" {
		t.Error("expected non-empty exit error")
	}
}

func TestMonitorCompanion_StoppedDuringRun(t *testing.T) {
	quietLogger(t)

	cm := NewCompanionManager()

	cmd := exec.Command("sleep", "60")
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	if err := cmd.Start(); err != nil {
		t.Fatalf("failed to start process: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)

	proc := &CompanionProcess{
		Name:        "test-comp",
		TunnelAlias: "test-tunnel",
		Cmd:         cmd,
		Pid:         cmd.Process.Pid,
		State:       CompanionStateRunning,
		Config: core.CompanionConfig{
			Name:        "test-comp",
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

	// Simulate external stop: set state to Stopped and kill the process
	time.Sleep(100 * time.Millisecond)
	proc.mu.Lock()
	proc.State = CompanionStateStopped
	proc.mu.Unlock()
	cmd.Process.Kill()

	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("monitorCompanion did not return after stop")
	}
}

func TestMonitorAdoptedCompanion_DeadPID(t *testing.T) {
	quietLogger(t)

	cm := NewCompanionManager()

	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)

	proc := &CompanionProcess{
		Name:        "adopted-comp",
		TunnelAlias: "test-tunnel",
		Pid:         999999999, // Dead PID
		State:       CompanionStateRunning,
		Config: core.CompanionConfig{
			Name:        "adopted-comp",
			AutoRestart: false,
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
	case <-time.After(10 * time.Second):
		t.Fatal("monitorAdoptedCompanion did not return for dead PID")
	}

	proc.mu.RLock()
	state := proc.State
	proc.mu.RUnlock()

	if state != CompanionStateExited {
		t.Errorf("expected state Exited, got %v", state)
	}
}

func TestMonitorAdoptedCompanion_StoppedBeforeCheck(t *testing.T) {
	quietLogger(t)

	cm := NewCompanionManager()

	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)

	proc := &CompanionProcess{
		Name:        "adopted-comp",
		TunnelAlias: "test-tunnel",
		Pid:         os.Getpid(), // Our PID is alive
		State:       CompanionStateStopped,
		Config: core.CompanionConfig{
			Name: "adopted-comp",
		},
		output: NewLogBroadcaster(100),
		ctx:    ctx,
		cancel: cancel,
	}

	osProc, err := os.FindProcess(os.Getpid())
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
	case <-time.After(10 * time.Second):
		t.Fatal("monitorAdoptedCompanion did not return for stopped state")
	}
}
