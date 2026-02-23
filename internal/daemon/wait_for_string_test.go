package daemon

import (
	"context"
	"os/exec"
	"strings"
	"testing"
	"time"
)

func TestWaitForString_FoundInHistory(t *testing.T) {
	quietLogger(t)

	cm := NewCompanionManager()
	broadcaster := NewLogBroadcaster(100)

	// Pre-populate history with the target string using a timestamp after StartTime
	startTime := time.Now().Add(-1 * time.Second)
	broadcaster.Broadcast(time.Now().Format("2006-01-02 15:04:05") + " [output] READY\n")

	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)

	// Need a real process for the timeout kill path (even though we won't hit it)
	cmd := exec.Command("sleep", "60")
	if err := cmd.Start(); err != nil {
		t.Fatalf("failed to start process: %v", err)
	}
	t.Cleanup(func() { cmd.Process.Kill() })

	proc := &CompanionProcess{
		Name:      "test-comp",
		output:    broadcaster,
		ctx:       ctx,
		cancel:    cancel,
		StartTime: startTime,
		Cmd:       cmd,
	}

	err := cm.waitForString(proc, "READY", 5*time.Second)
	if err != nil {
		t.Errorf("expected nil error, got: %v", err)
	}
}

func TestWaitForString_FoundInStream(t *testing.T) {
	quietLogger(t)

	cm := NewCompanionManager()
	broadcaster := NewLogBroadcaster(100)

	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)

	cmd := exec.Command("sleep", "60")
	if err := cmd.Start(); err != nil {
		t.Fatalf("failed to start process: %v", err)
	}
	t.Cleanup(func() { cmd.Process.Kill() })

	proc := &CompanionProcess{
		Name:      "test-comp",
		output:    broadcaster,
		ctx:       ctx,
		cancel:    cancel,
		StartTime: time.Now(),
		Cmd:       cmd,
	}

	// Broadcast the target string after a short delay
	go func() {
		time.Sleep(100 * time.Millisecond)
		broadcaster.Broadcast("some output\n")
		broadcaster.Broadcast("SERVER READY\n")
	}()

	err := cm.waitForString(proc, "READY", 5*time.Second)
	if err != nil {
		t.Errorf("expected nil error, got: %v", err)
	}
}

func TestWaitForString_Timeout(t *testing.T) {
	quietLogger(t)

	cm := NewCompanionManager()
	broadcaster := NewLogBroadcaster(100)

	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)

	cmd := exec.Command("sleep", "60")
	if err := cmd.Start(); err != nil {
		t.Fatalf("failed to start process: %v", err)
	}
	t.Cleanup(func() { cmd.Process.Kill() })

	proc := &CompanionProcess{
		Name:      "test-comp",
		output:    broadcaster,
		ctx:       ctx,
		cancel:    cancel,
		StartTime: time.Now(),
		Cmd:       cmd,
	}

	err := cm.waitForString(proc, "NEVER_APPEARS", 200*time.Millisecond)
	if err == nil {
		t.Fatal("expected timeout error")
	}
	if !strings.Contains(err.Error(), "timeout") {
		t.Errorf("expected timeout error, got: %v", err)
	}
}

func TestWaitForString_CtxCancelled(t *testing.T) {
	quietLogger(t)

	cm := NewCompanionManager()
	broadcaster := NewLogBroadcaster(100)

	ctx, cancel := context.WithCancel(context.Background())

	cmd := exec.Command("sleep", "60")
	if err := cmd.Start(); err != nil {
		t.Fatalf("failed to start process: %v", err)
	}
	t.Cleanup(func() { cmd.Process.Kill() })

	proc := &CompanionProcess{
		Name:      "test-comp",
		output:    broadcaster,
		ctx:       ctx,
		cancel:    cancel,
		StartTime: time.Now(),
		Cmd:       cmd,
	}

	// Cancel context after a short delay
	go func() {
		time.Sleep(100 * time.Millisecond)
		cancel()
	}()

	err := cm.waitForString(proc, "NEVER_APPEARS", 10*time.Second)
	if err == nil {
		t.Fatal("expected cancellation error")
	}
	if !strings.Contains(err.Error(), "cancelled") {
		t.Errorf("expected cancelled error, got: %v", err)
	}
}

func TestWaitForString_SkipsOldHistoryLines(t *testing.T) {
	quietLogger(t)

	cm := NewCompanionManager()
	broadcaster := NewLogBroadcaster(100)

	// Add a history line from BEFORE the process started
	oldTimestamp := time.Now().Add(-10 * time.Minute).Format("2006-01-02 15:04:05")
	broadcaster.Broadcast(oldTimestamp + " [output] READY\n")

	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)

	cmd := exec.Command("sleep", "60")
	if err := cmd.Start(); err != nil {
		t.Fatalf("failed to start process: %v", err)
	}
	t.Cleanup(func() { cmd.Process.Kill() })

	proc := &CompanionProcess{
		Name:      "test-comp",
		output:    broadcaster,
		ctx:       ctx,
		cancel:    cancel,
		StartTime: time.Now(), // After the old history line
		Cmd:       cmd,
	}

	// The old history line should be skipped, so it should timeout
	err := cm.waitForString(proc, "READY", 200*time.Millisecond)
	if err == nil {
		t.Fatal("expected timeout error (old history should be skipped)")
	}
	if !strings.Contains(err.Error(), "timeout") {
		t.Errorf("expected timeout error, got: %v", err)
	}
}

