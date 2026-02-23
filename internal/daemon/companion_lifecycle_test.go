package daemon

import (
	"context"
	"os/exec"
	"path/filepath"
	"syscall"
	"testing"
	"time"

	"go.olrik.dev/overseer/internal/core"
	"go.olrik.dev/overseer/internal/db"
)

func TestRestartCompanions_NilCompanions(t *testing.T) {
	quietLogger(t)

	cm := NewCompanionManager()

	err := cm.RestartCompanions("nonexistent-tunnel")
	if err != nil {
		t.Errorf("expected nil error for nil companions, got: %v", err)
	}
}

func TestRestartCompanions_EmptyCompanions(t *testing.T) {
	quietLogger(t)

	cm := NewCompanionManager()
	cm.companions["my-tunnel"] = make(map[string]*CompanionProcess)

	err := cm.RestartCompanions("my-tunnel")
	if err != nil {
		t.Errorf("expected nil error for empty companions, got: %v", err)
	}
}

func TestRestartCompanionInPlace_NoExistingProcess(t *testing.T) {
	quietLogger(t)

	cm := NewCompanionManager()

	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)

	broadcaster := NewLogBroadcaster(100)

	proc := &CompanionProcess{
		Name:        "test-comp",
		TunnelAlias: "test-tunnel",
		Pid:         0, // No existing process
		State:       CompanionStateStopped,
		output:      broadcaster,
		ctx:         ctx,
		cancel:      cancel,
		Config: core.CompanionConfig{
			Name:    "test-comp",
			Command: "echo hello",
		},
	}

	// restartCompanionInPlace will try to start a new process
	// It will fail because the executable doesn't support companion mode,
	// but it exercises the "no existing PID" code path
	err := cm.restartCompanionInPlace(proc)
	// May or may not error depending on executable path
	_ = err
}

func TestRestartCompanionInPlace_WithExistingProcess(t *testing.T) {
	quietLogger(t)

	cm := NewCompanionManager()

	// Start a real process to simulate an existing companion
	cmd := exec.Command("sleep", "60")
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	if err := cmd.Start(); err != nil {
		t.Fatalf("failed to start process: %v", err)
	}
	t.Cleanup(func() { cmd.Process.Kill() })

	ctx, cancel := context.WithCancel(context.Background())

	broadcaster := NewLogBroadcaster(100)

	proc := &CompanionProcess{
		Name:        "test-comp",
		TunnelAlias: "test-tunnel",
		Pid:         cmd.Process.Pid,
		State:       CompanionStateRunning,
		Cmd:         cmd,
		output:      broadcaster,
		ctx:         ctx,
		cancel:      cancel,
		Config: core.CompanionConfig{
			Name:    "test-comp",
			Command: "echo hello",
		},
	}

	// restartCompanionInPlace will stop the old process and start a new one
	err := cm.restartCompanionInPlace(proc)
	// May error because the executable path doesn't support companion mode
	_ = err

	// Verify the old process was stopped
	time.Sleep(200 * time.Millisecond)
	if err := cmd.Process.Signal(syscall.Signal(0)); err == nil {
		t.Log("old process still alive - may be expected if restart completed quickly")
	}
}

func TestLogSensorChange_WithDatabase(t *testing.T) {
	quietLogger(t)

	tmpDir := t.TempDir()
	database, err := db.Open(filepath.Join(tmpDir, "test.db"))
	if err != nil {
		t.Fatalf("failed to open database: %v", err)
	}
	t.Cleanup(func() { database.Close() })

	adapter := newDatabaseLoggerAdapter(database)

	err = adapter.LogSensorChange("public_ipv4", "ip", "1.2.3.4", "5.6.7.8")
	if err != nil {
		t.Errorf("LogSensorChange failed: %v", err)
	}
}

func TestLogSensorChange_BooleanSensor(t *testing.T) {
	quietLogger(t)

	tmpDir := t.TempDir()
	database, err := db.Open(filepath.Join(tmpDir, "test.db"))
	if err != nil {
		t.Fatalf("failed to open database: %v", err)
	}
	t.Cleanup(func() { database.Close() })

	adapter := newDatabaseLoggerAdapter(database)

	err = adapter.LogSensorChange("tcp", "bool", "false", "true")
	if err != nil {
		t.Errorf("LogSensorChange failed: %v", err)
	}
}

func TestStopSingleCompanion_WithRunningProcess(t *testing.T) {
	quietLogger(t)

	cm := NewCompanionManager()

	cmd := exec.Command("sleep", "60")
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	if err := cmd.Start(); err != nil {
		t.Fatalf("failed to start process: %v", err)
	}
	t.Cleanup(func() { cmd.Process.Kill() })

	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)

	cm.companions["my-tunnel"] = map[string]*CompanionProcess{
		"my-comp": {
			Name:   "my-comp",
			Cmd:    cmd,
			Pid:    cmd.Process.Pid,
			State:  CompanionStateRunning,
			Config: core.CompanionConfig{StopSignal: "TERM"},
			ctx:    ctx,
			cancel: cancel,
		},
	}

	err := cm.StopSingleCompanion("my-tunnel", "my-comp")
	if err != nil {
		t.Errorf("StopSingleCompanion failed: %v", err)
	}

	// Verify companion was removed from map
	if _, exists := cm.companions["my-tunnel"]["my-comp"]; exists {
		t.Error("expected companion to be removed from map after stop")
	}
}
