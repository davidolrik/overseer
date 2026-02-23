package daemon

import (
	"os"
	"os/exec"
	"path/filepath"
	"syscall"
	"testing"
	"time"

	"go.olrik.dev/overseer/internal/core"
	"go.olrik.dev/overseer/internal/db"
)

func TestShutdown_CleansUpTunnels(t *testing.T) {
	quietLogger(t)

	oldConfig := core.Config
	t.Cleanup(func() { core.Config = oldConfig })
	core.Config = &core.Configuration{
		Companion: core.CompanionSettings{HistorySize: 50},
	}

	d := New()

	// Start real child processes as fake tunnels
	cmd1 := exec.Command("sleep", "60")
	cmd1.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	if err := cmd1.Start(); err != nil {
		t.Fatalf("failed to start process 1: %v", err)
	}
	// Reap child in a goroutine to avoid zombies
	go cmd1.Wait()

	cmd2 := exec.Command("sleep", "60")
	cmd2.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	if err := cmd2.Start(); err != nil {
		t.Fatalf("failed to start process 2: %v", err)
	}
	go cmd2.Wait()

	d.tunnels["tunnel-1"] = Tunnel{
		Hostname: "host1.example.com",
		Pid:      cmd1.Process.Pid,
		Cmd:      cmd1,
		State:    StateConnected,
	}
	d.tunnels["tunnel-2"] = Tunnel{
		Hostname: "host2.example.com",
		Pid:      cmd2.Process.Pid,
		Cmd:      cmd2,
		State:    StateConnected,
	}

	d.shutdown()

	// Give processes time to die
	time.Sleep(500 * time.Millisecond)

	// Verify processes are dead
	if err := cmd1.Process.Signal(syscall.Signal(0)); err == nil {
		t.Error("expected tunnel-1 process to be dead")
		cmd1.Process.Kill()
	}
	if err := cmd2.Process.Signal(syscall.Signal(0)); err == nil {
		t.Error("expected tunnel-2 process to be dead")
		cmd2.Process.Kill()
	}

	// Verify tunnel map is cleared
	if len(d.tunnels) != 0 {
		t.Errorf("expected empty tunnel map, got %d entries", len(d.tunnels))
	}
}

func TestShutdown_StopsOrchestrator(t *testing.T) {
	quietLogger(t)

	tmpDir := t.TempDir()
	oldConfig := core.Config
	t.Cleanup(func() { core.Config = oldConfig })
	core.Config = &core.Configuration{
		ConfigPath: tmpDir,
		Companion:  core.CompanionSettings{HistorySize: 50},
		Locations:  map[string]*core.Location{},
		Contexts:   []*core.ContextRule{},
	}

	old := stateOrchestrator
	t.Cleanup(func() { stateOrchestrator = old })

	d := New()
	if err := d.initStateOrchestrator(); err != nil {
		t.Fatalf("initStateOrchestrator failed: %v", err)
	}

	if stateOrchestrator == nil {
		t.Fatal("expected orchestrator to be non-nil before shutdown")
	}

	d.shutdown()

	if stateOrchestrator != nil {
		t.Error("expected orchestrator to be nil after shutdown")
	}
}

func TestShutdown_ClosesDatabase(t *testing.T) {
	quietLogger(t)

	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test.db")

	oldConfig := core.Config
	t.Cleanup(func() { core.Config = oldConfig })
	core.Config = &core.Configuration{
		ConfigPath: tmpDir,
		Companion:  core.CompanionSettings{HistorySize: 50},
	}

	database, err := db.Open(dbPath)
	if err != nil {
		t.Fatalf("failed to create database: %v", err)
	}

	d := New()
	d.database = database

	d.shutdown()

	// Verify database is closed by confirming that the file exists
	// but is no longer locked (we can open it again)
	database2, err := db.Open(dbPath)
	if err != nil {
		t.Fatalf("expected to be able to reopen database after shutdown: %v", err)
	}
	database2.Close()
}

func TestShutdown_Idempotent(t *testing.T) {
	quietLogger(t)

	oldConfig := core.Config
	t.Cleanup(func() { core.Config = oldConfig })
	core.Config = &core.Configuration{
		Companion: core.CompanionSettings{HistorySize: 50},
	}

	old := stateOrchestrator
	t.Cleanup(func() { stateOrchestrator = old })
	stateOrchestrator = nil

	d := New()

	// Call shutdown twice — second call should be no-op due to shutdownOnce
	d.shutdown()
	d.shutdown() // Should not panic
}

func TestShutdown_AdoptedTunnel(t *testing.T) {
	quietLogger(t)

	oldConfig := core.Config
	t.Cleanup(func() { core.Config = oldConfig })
	core.Config = &core.Configuration{
		Companion: core.CompanionSettings{HistorySize: 50},
	}

	old := stateOrchestrator
	t.Cleanup(func() { stateOrchestrator = old })
	stateOrchestrator = nil

	d := New()

	// Start a process to represent an adopted tunnel (no Cmd, only PID)
	cmd := exec.Command("sleep", "60")
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	if err := cmd.Start(); err != nil {
		t.Fatalf("failed to start process: %v", err)
	}
	go cmd.Wait()

	d.tunnels["adopted"] = Tunnel{
		Hostname: "adopted.example.com",
		Pid:      cmd.Process.Pid,
		Cmd:      nil, // Adopted tunnel — no Cmd
		State:    StateConnected,
	}

	d.shutdown()

	time.Sleep(500 * time.Millisecond)

	if err := cmd.Process.Signal(syscall.Signal(0)); err == nil {
		t.Error("expected adopted tunnel process to be dead")
		cmd.Process.Kill()
	}
}

func TestShutdown_NoPidTunnel(t *testing.T) {
	quietLogger(t)

	oldConfig := core.Config
	t.Cleanup(func() { core.Config = oldConfig })
	core.Config = &core.Configuration{
		Companion: core.CompanionSettings{HistorySize: 50},
	}

	old := stateOrchestrator
	t.Cleanup(func() { stateOrchestrator = old })
	stateOrchestrator = nil

	d := New()

	// Tunnel with no PID and no Cmd — should not panic
	d.tunnels["ghost"] = Tunnel{
		Hostname: "ghost.example.com",
		Pid:      0,
		Cmd:      nil,
		State:    StateDisconnected,
	}

	d.shutdown() // Should log warning but not panic
}

func TestShutdown_WithDatabase(t *testing.T) {
	quietLogger(t)

	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test.db")

	oldConfig := core.Config
	t.Cleanup(func() { core.Config = oldConfig })
	core.Config = &core.Configuration{
		ConfigPath: tmpDir,
		Companion:  core.CompanionSettings{HistorySize: 50},
	}

	old := stateOrchestrator
	t.Cleanup(func() { stateOrchestrator = old })
	stateOrchestrator = nil

	database, err := db.Open(dbPath)
	if err != nil {
		t.Fatalf("failed to create database: %v", err)
	}

	d := New()
	d.database = database

	// Add a tunnel so the shutdown logs a disconnect event
	cmd := exec.Command("sleep", "60")
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	if err := cmd.Start(); err != nil {
		t.Fatalf("failed to start process: %v", err)
	}
	go cmd.Wait()

	d.tunnels["db-tunnel"] = Tunnel{
		Hostname: "db.example.com",
		Pid:      cmd.Process.Pid,
		Cmd:      cmd,
		State:    StateConnected,
	}

	d.shutdown()

	// Verify file exists (database was flushed and closed properly)
	if _, err := os.Stat(dbPath); os.IsNotExist(err) {
		t.Error("expected database file to exist after shutdown")
	}
}
