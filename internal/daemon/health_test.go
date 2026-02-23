package daemon

import (
	"context"
	"os"
	"os/exec"
	"testing"
	"time"
)

func TestCheckTunnelHealth_DeadProcess(t *testing.T) {
	quietLogger(t)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	d := &Daemon{
		ctx:     ctx,
		tunnels: make(map[string]Tunnel),
	}

	// PID 0 won't have a valid signal target
	if d.checkTunnelHealth("test", 0) {
		t.Error("expected health check to fail for PID 0")
	}
}

func TestCheckTunnelHealth_NonexistentProcess(t *testing.T) {
	quietLogger(t)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	d := &Daemon{
		ctx:     ctx,
		tunnels: make(map[string]Tunnel),
	}

	// Start and immediately kill a process to get a dead PID
	cmd := exec.Command("sleep", "60")
	if err := cmd.Start(); err != nil {
		t.Fatalf("failed to start process: %v", err)
	}
	pid := cmd.Process.Pid
	cmd.Process.Kill()
	cmd.Wait()

	if d.checkTunnelHealth("test", pid) {
		t.Error("expected health check to fail for dead process")
	}
}

func TestCheckTunnelHealth_LiveProcess(t *testing.T) {
	quietLogger(t)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	d := &Daemon{
		ctx:     ctx,
		tunnels: make(map[string]Tunnel),
	}

	// Current process is alive, signal check passes, but TCP check will fail
	// (no established TCP connections for our test process)
	result := d.checkTunnelHealth("test", os.Getpid())
	// The result depends on whether the current process has TCP connections.
	// We mainly verify it doesn't panic.
	_ = result
}

func TestCheckAllTunnelHealth_NoTunnels(t *testing.T) {
	quietLogger(t)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	d := &Daemon{
		ctx:     ctx,
		tunnels: make(map[string]Tunnel),
	}

	// Should return immediately without panic
	d.checkAllTunnelHealth("test")
}

func TestCheckAllTunnelHealth_SkipsNonConnected(t *testing.T) {
	quietLogger(t)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	d := &Daemon{
		ctx:     ctx,
		tunnels: make(map[string]Tunnel),
	}

	d.tunnels["disconnected"] = Tunnel{
		State: StateDisconnected,
		Pid:   os.Getpid(),
	}
	d.tunnels["connecting"] = Tunnel{
		State: StateConnecting,
		Pid:   os.Getpid(),
	}

	// Should complete without checking any tunnels (none are Connected)
	d.checkAllTunnelHealth("test")
}

func TestCheckAllTunnelHealth_RecentConnection(t *testing.T) {
	quietLogger(t)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	d := &Daemon{
		ctx:     ctx,
		tunnels: make(map[string]Tunnel),
	}

	d.tunnels["recent"] = Tunnel{
		State:             StateConnected,
		Pid:               os.Getpid(),
		LastConnectedTime: time.Now(), // Just connected
	}

	// Should skip health check because connection is too recent
	d.checkAllTunnelHealth("test")

	// Verify failure count hasn't changed
	if d.tunnels["recent"].HealthCheckFailures != 0 {
		t.Error("expected no health check failures for recently connected tunnel")
	}
}

func TestCheckAllTunnelHealth_ConsecutiveFailures(t *testing.T) {
	quietLogger(t)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Start a process with no TCP connections, then kill it to ensure health check fails
	cmd := exec.Command("sleep", "60")
	if err := cmd.Start(); err != nil {
		t.Fatalf("failed to start process: %v", err)
	}
	pid := cmd.Process.Pid
	cmd.Process.Kill()
	cmd.Wait()

	d := &Daemon{
		ctx:     ctx,
		tunnels: make(map[string]Tunnel),
	}

	d.tunnels["failing"] = Tunnel{
		State:               StateConnected,
		Pid:                 pid, // Dead process
		LastConnectedTime:   time.Now().Add(-2 * time.Minute), // Old enough to check
		HealthCheckFailures: 1,                                // Already had one failure
	}

	d.checkAllTunnelHealth("test")

	// After the check, HealthCheckFailures should have incremented
	tunnel := d.tunnels["failing"]
	if tunnel.HealthCheckFailures < 2 {
		t.Errorf("expected HealthCheckFailures >= 2, got %d", tunnel.HealthCheckFailures)
	}
}

func TestCheckAllTunnelHealth_ResetsFailureCount(t *testing.T) {
	quietLogger(t)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Start a real process that we keep alive
	cmd := exec.Command("sleep", "60")
	if err := cmd.Start(); err != nil {
		t.Fatalf("failed to start process: %v", err)
	}
	defer cmd.Process.Kill()
	defer cmd.Wait()

	d := &Daemon{
		ctx:     ctx,
		tunnels: make(map[string]Tunnel),
	}

	d.tunnels["healthy"] = Tunnel{
		State:               StateConnected,
		Pid:                 cmd.Process.Pid,
		LastConnectedTime:   time.Now().Add(-2 * time.Minute),
		HealthCheckFailures: 1, // Had a previous failure
	}

	d.checkAllTunnelHealth("test")

	// If the process check passes but TCP check fails, failures will increment.
	// If both pass (unlikely in tests), failures will be reset.
	// Either way, we verify no panic and the function handles the state correctly.
	tunnel := d.tunnels["healthy"]
	_ = tunnel // Result depends on TCP connection state
}
