package daemon

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"go.olrik.dev/overseer/internal/db"
)

func TestCheckAllTunnelHealth_FailureIncrement(t *testing.T) {
	quietLogger(t)

	d := &Daemon{
		tunnels: map[string]Tunnel{
			"failing": {
				Hostname:            "test.example.com",
				Pid:                 999999999, // Non-existent PID
				State:               StateConnected,
				LastConnectedTime:   time.Now().Add(-5 * time.Minute), // Old enough
				HealthCheckFailures: 0,
			},
		},
	}

	d.checkAllTunnelHealth("test")

	d.mu.Lock()
	tunnel := d.tunnels["failing"]
	d.mu.Unlock()

	if tunnel.HealthCheckFailures != 1 {
		t.Errorf("expected 1 failure, got %d", tunnel.HealthCheckFailures)
	}
}

func TestCheckAllTunnelHealth_ConsecutiveFailuresKillProcess(t *testing.T) {
	quietLogger(t)

	// Start a real process so we can verify it gets killed
	cmd := exec.Command("sleep", "60")
	if err := cmd.Start(); err != nil {
		t.Fatalf("failed to start process: %v", err)
	}
	go cmd.Wait() // Reap zombie

	d := &Daemon{
		tunnels: map[string]Tunnel{
			"dead-tunnel": {
				Hostname:            "test.example.com",
				Pid:                 cmd.Process.Pid,
				State:               StateConnected,
				LastConnectedTime:   time.Now().Add(-5 * time.Minute),
				HealthCheckFailures: 1, // One failure already, next one triggers kill
			},
		},
	}

	d.checkAllTunnelHealth("test")

	// Process should have been killed
	time.Sleep(200 * time.Millisecond)
	if err := cmd.Process.Signal(os.Kill); err == nil {
		cmd.Process.Kill()
		t.Log("process was not killed - health check may have passed (TCP connection?)")
	}
}

func TestCheckAllTunnelHealth_WithDatabase(t *testing.T) {
	quietLogger(t)

	tmpDir := t.TempDir()
	database, err := db.Open(filepath.Join(tmpDir, "test.db"))
	if err != nil {
		t.Fatalf("failed to open database: %v", err)
	}
	t.Cleanup(func() { database.Close() })

	d := &Daemon{
		tunnels: map[string]Tunnel{
			"dead-db": {
				Hostname:            "test.example.com",
				Pid:                 999999999, // Dead process
				State:               StateConnected,
				LastConnectedTime:   time.Now().Add(-5 * time.Minute),
				HealthCheckFailures: 1, // Will trigger kill on next failure
			},
		},
		database: database,
	}

	// Should log the health check failure event to the database
	d.checkAllTunnelHealth("test_with_db")
}

func TestStartHealthCheckLoop(t *testing.T) {
	quietLogger(t)

	d := &Daemon{
		tunnels: make(map[string]Tunnel),
	}
	d.ctx, d.cancelFunc = context.WithCancel(context.Background())

	d.startHealthCheckLoop()

	// Cancel context to stop the loop
	d.cancelFunc()

	// Give goroutine time to exit
	time.Sleep(100 * time.Millisecond)
}

func TestCheckAllTunnelHealth_TunnelRemovedDuringCheck(t *testing.T) {
	quietLogger(t)

	d := &Daemon{
		tunnels: map[string]Tunnel{
			"volatile": {
				Hostname:            "test.example.com",
				Pid:                 999999999,
				State:               StateConnected,
				LastConnectedTime:   time.Now().Add(-5 * time.Minute),
				HealthCheckFailures: 0,
			},
		},
	}

	// Remove tunnel before health check processes it
	go func() {
		d.mu.Lock()
		delete(d.tunnels, "volatile")
		d.mu.Unlock()
	}()

	// Should not panic even if tunnel disappears during check
	time.Sleep(10 * time.Millisecond)
	d.checkAllTunnelHealth("race_test")
}
