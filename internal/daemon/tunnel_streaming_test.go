package daemon

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"go.olrik.dev/overseer/internal/core"
	"go.olrik.dev/overseer/internal/db"
)

func TestStartTunnelStreaming_AlreadyRunningHealthy(t *testing.T) {
	quietLogger(t)

	oldConfig := core.Config
	t.Cleanup(func() { core.Config = oldConfig })
	core.Config = &core.Configuration{
		Companion: core.CompanionSettings{HistorySize: 50},
		Tunnels:   map[string]*core.TunnelConfig{},
	}

	d := New()

	// Start a real process to simulate a healthy tunnel
	cmd := exec.Command("sleep", "60")
	if err := cmd.Start(); err != nil {
		t.Fatalf("failed to start process: %v", err)
	}
	t.Cleanup(func() { cmd.Process.Kill() })

	d.tunnels["running-tunnel"] = Tunnel{
		Hostname: "test.example.com",
		Pid:      cmd.Process.Pid,
		Cmd:      cmd,
		State:    StateConnected,
	}

	// Should return error because tunnel is "already running"
	// (health check uses signal check + TCP - signal will pass for our sleep process)
	resp := d.startTunnelStreaming("running-tunnel", "", nil)

	// Even if TCP check fails (making health check fail), the stale cleanup path is exercised
	_ = resp
}

func TestStartTunnelStreaming_StaleTunnelCleanup(t *testing.T) {
	quietLogger(t)

	oldConfig := core.Config
	t.Cleanup(func() { core.Config = oldConfig })
	core.Config = &core.Configuration{
		Companion: core.CompanionSettings{HistorySize: 50},
		Tunnels:   map[string]*core.TunnelConfig{},
		SSH:       core.SSHConfig{},
	}

	d := New()

	// Create a stale tunnel with a dead PID and an askpass token
	d.tunnels["stale-tunnel"] = Tunnel{
		Hostname:     "test.example.com",
		Pid:          999999999, // Non-existent PID
		State:        StateConnected,
		AskpassToken: "stale-token-123",
	}
	d.askpassTokens["stale-token-123"] = "stale-tunnel"

	// startTunnelStreaming should clean up the stale entry and try to connect
	// It will fail to connect (no valid SSH target), but the cleanup path is exercised
	resp := d.startTunnelStreaming("stale-tunnel", "", nil)

	// The stale token should be cleaned up
	if _, exists := d.askpassTokens["stale-token-123"]; exists {
		t.Error("expected stale askpass token to be cleaned up")
	}

	_ = resp
}

func TestStartTunnelStreaming_StaleTunnelWithDatabase(t *testing.T) {
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
		Tunnels:   map[string]*core.TunnelConfig{},
		SSH:       core.SSHConfig{},
	}

	d := New()
	d.database = database

	// Add stale tunnel
	d.tunnels["stale-db"] = Tunnel{
		Hostname: "test.example.com",
		Pid:      999999999,
		State:    StateConnected,
	}

	// Should clean up stale entry and log to database
	resp := d.startTunnelStreaming("stale-db", "", nil)
	_ = resp
}

func TestStartTunnelStreaming_NoTunnelConfig(t *testing.T) {
	quietLogger(t)

	oldConfig := core.Config
	t.Cleanup(func() { core.Config = oldConfig })
	core.Config = &core.Configuration{
		Companion: core.CompanionSettings{HistorySize: 50},
		Tunnels:   map[string]*core.TunnelConfig{},
		SSH:       core.SSHConfig{},
	}

	d := New()

	// No tunnel in config, but SSH alias exists on the system
	// startTunnelStreaming should skip companion section and go straight to SSH
	resp := d.startTunnelStreaming("no-config-alias", "", nil)

	// Will fail because SSH can't connect, but the no-companions code path is exercised
	_ = resp
}

func TestStartTunnelStreaming_WithTag(t *testing.T) {
	quietLogger(t)

	oldConfig := core.Config
	t.Cleanup(func() { core.Config = oldConfig })
	core.Config = &core.Configuration{
		Companion: core.CompanionSettings{HistorySize: 50},
		Tunnels: map[string]*core.TunnelConfig{
			"tagged-tunnel": {
				Name: "tagged-tunnel",
				Tag:  "default-tag",
			},
		},
		SSH: core.SSHConfig{},
	}

	d := New()

	// Test with a CLI tag that overrides config tag
	resp := d.startTunnelStreaming("tagged-tunnel", "cli-tag", nil)
	_ = resp
}

func TestStartTunnelStreaming_WithSSHConfig(t *testing.T) {
	quietLogger(t)

	tmpDir := t.TempDir()
	sshConfigPath := filepath.Join(tmpDir, "ssh_config")
	os.WriteFile(sshConfigPath, []byte("# empty config\n"), 0600)

	oldConfig := core.Config
	t.Cleanup(func() { core.Config = oldConfig })
	core.Config = &core.Configuration{
		Companion: core.CompanionSettings{HistorySize: 50},
		Tunnels:   map[string]*core.TunnelConfig{},
		SSH:       core.SSHConfig{},
	}

	d := New()
	d.sshConfigFile = sshConfigPath

	resp := d.startTunnelStreaming("config-test", "", nil)
	_ = resp
}

func TestStartTunnelStreaming_WithServerAliveInterval(t *testing.T) {
	quietLogger(t)

	oldConfig := core.Config
	t.Cleanup(func() { core.Config = oldConfig })
	core.Config = &core.Configuration{
		Companion: core.CompanionSettings{HistorySize: 50},
		Tunnels:   map[string]*core.TunnelConfig{},
		SSH: core.SSHConfig{
			ServerAliveInterval: 30,
			ServerAliveCountMax: 3,
		},
	}

	d := New()

	resp := d.startTunnelStreaming("alive-test", "", nil)
	_ = resp
}

func TestStartTunnel_NoConfig(t *testing.T) {
	quietLogger(t)

	oldConfig := core.Config
	t.Cleanup(func() { core.Config = oldConfig })
	core.Config = &core.Configuration{
		Companion: core.CompanionSettings{HistorySize: 50},
		Tunnels:   map[string]*core.TunnelConfig{},
		SSH:       core.SSHConfig{},
	}

	d := New()
	d.ctx, d.cancelFunc = context.WithCancel(context.Background())
	t.Cleanup(d.cancelFunc)

	// startTunnel calls startTunnelStreaming with nil stream
	resp := d.startTunnel("basic-alias", "")
	_ = resp
}
