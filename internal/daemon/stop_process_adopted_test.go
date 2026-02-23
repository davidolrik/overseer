package daemon

import (
	"context"
	"os/exec"
	"syscall"
	"testing"
	"time"

	"go.olrik.dev/overseer/internal/core"
)

func TestStopProcess_AdoptedProcess(t *testing.T) {
	quietLogger(t)

	cm := NewCompanionManager()

	// Start a real process to simulate an adopted companion (no Cmd)
	cmd := exec.Command("sleep", "60")
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	if err := cmd.Start(); err != nil {
		t.Fatalf("failed to start process: %v", err)
	}
	go cmd.Wait() // Reap zombie
	t.Cleanup(func() { cmd.Process.Kill() })

	ctx, cancel := context.WithCancel(context.Background())
	broadcaster := NewLogBroadcaster(100)

	proc := &CompanionProcess{
		Name:   "adopted-comp",
		Cmd:    nil, // Adopted process - no Cmd
		Pid:    cmd.Process.Pid,
		State:  CompanionStateRunning,
		Config: core.CompanionConfig{StopSignal: "INT"},
		ctx:    ctx,
		cancel: cancel,
		output: broadcaster,
	}

	cm.stopProcess(proc, "adopted-comp", "test-alias")

	// Verify process is dead
	time.Sleep(500 * time.Millisecond)
	if err := cmd.Process.Signal(syscall.Signal(0)); err == nil {
		t.Error("expected adopted process to be dead after stopProcess")
		cmd.Process.Kill()
	}
}

func TestStopProcess_WithOutputBroadcaster(t *testing.T) {
	quietLogger(t)

	cm := NewCompanionManager()

	cmd := exec.Command("sleep", "60")
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	if err := cmd.Start(); err != nil {
		t.Fatalf("failed to start process: %v", err)
	}
	t.Cleanup(func() { cmd.Process.Kill() })

	ctx, cancel := context.WithCancel(context.Background())
	broadcaster := NewLogBroadcaster(100)
	broadcaster.Broadcast("old output line")

	proc := &CompanionProcess{
		Name:   "test-comp",
		Cmd:    cmd,
		Pid:    cmd.Process.Pid,
		State:  CompanionStateRunning,
		Config: core.CompanionConfig{StopSignal: "TERM"},
		ctx:    ctx,
		cancel: cancel,
		output: broadcaster,
	}

	cm.stopProcess(proc, "test-comp", "test-alias")

	// Verify history was cleared
	ch, history := broadcaster.SubscribeWithHistory(10)
	defer broadcaster.Unsubscribe(ch)
	if len(history) != 0 {
		t.Errorf("expected history to be cleared after stop, got %d entries", len(history))
	}
}

func TestStopProcess_ZeroPIDNoCmd(t *testing.T) {
	quietLogger(t)

	cm := NewCompanionManager()

	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)

	proc := &CompanionProcess{
		Name:   "test-comp",
		Cmd:    nil,
		Pid:    0, // No PID
		State:  CompanionStateRunning,
		Config: core.CompanionConfig{},
		ctx:    ctx,
		cancel: cancel,
	}

	// Should return early after setting state to Stopped (no process to signal)
	cm.stopProcess(proc, "test-comp", "test-alias")
}
