package daemon

import (
	"context"
	"os/exec"
	"strings"
	"testing"
	"time"
)

func TestWaitForCompletion_Success(t *testing.T) {
	quietLogger(t)

	cm := NewCompanionManager()

	cmd := exec.Command("true")
	if err := cmd.Start(); err != nil {
		t.Fatalf("failed to start process: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)

	proc := &CompanionProcess{
		Name:   "test-comp",
		Cmd:    cmd,
		ctx:    ctx,
		cancel: cancel,
	}

	err := cm.waitForCompletion(proc, 5*time.Second)
	if err != nil {
		t.Errorf("expected nil error for successful command, got: %v", err)
	}
}

func TestWaitForCompletion_Failure(t *testing.T) {
	quietLogger(t)

	cm := NewCompanionManager()

	cmd := exec.Command("false")
	if err := cmd.Start(); err != nil {
		t.Fatalf("failed to start process: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)

	proc := &CompanionProcess{
		Name:   "test-comp",
		Cmd:    cmd,
		ctx:    ctx,
		cancel: cancel,
	}

	err := cm.waitForCompletion(proc, 5*time.Second)
	if err == nil {
		t.Fatal("expected error for failed command")
	}
	if !strings.Contains(err.Error(), "exited with error") {
		t.Errorf("expected 'exited with error' message, got: %v", err)
	}
}

func TestWaitForCompletion_Timeout(t *testing.T) {
	quietLogger(t)

	cm := NewCompanionManager()

	cmd := exec.Command("sleep", "60")
	if err := cmd.Start(); err != nil {
		t.Fatalf("failed to start process: %v", err)
	}
	t.Cleanup(func() { cmd.Process.Kill() })

	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)

	proc := &CompanionProcess{
		Name:   "test-comp",
		Cmd:    cmd,
		ctx:    ctx,
		cancel: cancel,
	}

	err := cm.waitForCompletion(proc, 200*time.Millisecond)
	if err == nil {
		t.Fatal("expected timeout error")
	}
	if !strings.Contains(err.Error(), "timeout") {
		t.Errorf("expected 'timeout' message, got: %v", err)
	}
}

func TestWaitForCompletion_ContextCancelled(t *testing.T) {
	quietLogger(t)

	cm := NewCompanionManager()

	cmd := exec.Command("sleep", "60")
	if err := cmd.Start(); err != nil {
		t.Fatalf("failed to start process: %v", err)
	}
	t.Cleanup(func() { cmd.Process.Kill() })

	ctx, cancel := context.WithCancel(context.Background())

	proc := &CompanionProcess{
		Name:   "test-comp",
		Cmd:    cmd,
		ctx:    ctx,
		cancel: cancel,
	}

	// Cancel context after a short delay
	go func() {
		time.Sleep(100 * time.Millisecond)
		cancel()
	}()

	err := cm.waitForCompletion(proc, 10*time.Second)
	if err == nil {
		t.Fatal("expected cancellation error")
	}
	if !strings.Contains(err.Error(), "cancelled") {
		t.Errorf("expected 'cancelled' message, got: %v", err)
	}
}
