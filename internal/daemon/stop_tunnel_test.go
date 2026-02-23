package daemon

import (
	"context"
	"os/exec"
	"testing"
	"time"

	"go.olrik.dev/overseer/internal/core"
)

func TestStopTunnel_AdoptedTunnel(t *testing.T) {
	quietLogger(t)

	cmd := exec.Command("sleep", "60")
	if err := cmd.Start(); err != nil {
		t.Fatalf("failed to start process: %v", err)
	}
	// Reap zombie in background so gracefulTerminate's Signal(0) check works
	go cmd.Wait()
	t.Cleanup(func() { cmd.Process.Kill() })

	d := &Daemon{
		tunnels: map[string]Tunnel{
			"adopted-tunnel": {
				Hostname:  "adopted-tunnel",
				Pid:       cmd.Process.Pid,
				Cmd:       nil, // Adopted tunnel has no Cmd
				StartDate: time.Now(),
				State:     StateConnected,
			},
		},
		askpassTokens: make(map[string]string),
		companionMgr:  NewCompanionManager(),
	}

	resp := d.stopTunnel("adopted-tunnel", false)
	if len(resp.Messages) == 0 {
		t.Fatal("expected at least one message")
	}
	if resp.Messages[0].Status != "INFO" {
		t.Errorf("expected INFO status, got %q: %s", resp.Messages[0].Status, resp.Messages[0].Message)
	}

	if _, exists := d.tunnels["adopted-tunnel"]; exists {
		t.Error("expected tunnel to be removed from map")
	}
}

func TestStopTunnel_NoProcessReference(t *testing.T) {
	quietLogger(t)

	d := &Daemon{
		tunnels: map[string]Tunnel{
			"broken-tunnel": {
				Hostname:  "broken-tunnel",
				Pid:       0,
				Cmd:       nil,
				StartDate: time.Now(),
				State:     StateConnected,
			},
		},
		askpassTokens: make(map[string]string),
		companionMgr:  NewCompanionManager(),
	}

	resp := d.stopTunnel("broken-tunnel", false)
	if len(resp.Messages) == 0 {
		t.Fatal("expected at least one message")
	}
	if resp.Messages[0].Status != "ERROR" {
		t.Errorf("expected ERROR status, got %q", resp.Messages[0].Status)
	}

	// Tunnel should still be removed from map even if kill failed
	if _, exists := d.tunnels["broken-tunnel"]; exists {
		t.Error("expected tunnel to be removed from map even on kill failure")
	}
}

func TestStopTunnel_CleansAskpassToken(t *testing.T) {
	quietLogger(t)

	cmd := exec.Command("sleep", "60")
	if err := cmd.Start(); err != nil {
		t.Fatalf("failed to start process: %v", err)
	}
	go cmd.Wait()
	t.Cleanup(func() { cmd.Process.Kill() })

	d := &Daemon{
		tunnels: map[string]Tunnel{
			"token-tunnel": {
				Hostname:     "token-tunnel",
				Pid:          cmd.Process.Pid,
				Cmd:          cmd,
				StartDate:    time.Now(),
				State:        StateConnected,
				AskpassToken: "secret-token",
			},
		},
		askpassTokens: map[string]string{
			"secret-token": "token-tunnel",
		},
		companionMgr: NewCompanionManager(),
	}

	d.stopTunnel("token-tunnel", false)

	if _, exists := d.askpassTokens["secret-token"]; exists {
		t.Error("expected askpass token to be cleaned up")
	}
}

func TestStopTunnel_ForReconnect_ClearsHistory(t *testing.T) {
	quietLogger(t)

	cmd := exec.Command("sleep", "60")
	if err := cmd.Start(); err != nil {
		t.Fatalf("failed to start process: %v", err)
	}
	go cmd.Wait()
	t.Cleanup(func() { cmd.Process.Kill() })

	broadcaster := NewLogBroadcaster(100)
	broadcaster.Broadcast("old output")

	cm := NewCompanionManager()
	cm.companions["test-tunnel"] = map[string]*CompanionProcess{
		"comp1": {
			Name:   "comp1",
			State:  CompanionStateRunning,
			output: broadcaster,
		},
	}

	d := &Daemon{
		tunnels: map[string]Tunnel{
			"test-tunnel": {
				Hostname:  "test-tunnel",
				Pid:       cmd.Process.Pid,
				Cmd:       cmd,
				StartDate: time.Now(),
				State:     StateConnected,
			},
		},
		askpassTokens: make(map[string]string),
		companionMgr:  cm,
	}

	resp := d.stopTunnel("test-tunnel", true)
	if len(resp.Messages) == 0 {
		t.Fatal("expected at least one message")
	}
	if resp.Messages[0].Status != "INFO" {
		t.Errorf("expected INFO status, got %q", resp.Messages[0].Status)
	}

	// Companions should still exist (not stopped) for reconnect
	if cm.companions["test-tunnel"]["comp1"] == nil {
		t.Error("expected companion to still exist for reconnect")
	}

	// History should be cleared
	_, history := broadcaster.SubscribeWithHistory(10)
	if len(history) != 0 {
		t.Errorf("expected history cleared for reconnect, got %d entries", len(history))
	}
}

func TestCheckOnlineStatus_WithOrchestrator(t *testing.T) {
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
	t.Cleanup(func() {
		stopStateOrchestrator()
		stateOrchestrator = old
	})

	d := &Daemon{
		tunnels: make(map[string]Tunnel),
	}
	d.ctx, d.cancelFunc = context.WithCancel(context.Background())
	d.companionMgr = NewCompanionManager()

	if err := d.initStateOrchestrator(); err != nil {
		t.Fatalf("initStateOrchestrator failed: %v", err)
	}

	// Should not panic with orchestrator initialized
	_ = d.checkOnlineStatus()
}

func TestIsPublicIPKnown_WithOrchestrator(t *testing.T) {
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
	t.Cleanup(func() {
		stopStateOrchestrator()
		stateOrchestrator = old
	})

	d := &Daemon{
		tunnels: make(map[string]Tunnel),
	}
	d.ctx, d.cancelFunc = context.WithCancel(context.Background())
	d.companionMgr = NewCompanionManager()

	if err := d.initStateOrchestrator(); err != nil {
		t.Fatalf("initStateOrchestrator failed: %v", err)
	}

	// Should not panic with orchestrator initialized
	_ = d.isPublicIPKnown()
}

func TestHandleOnlineChange_Online(t *testing.T) {
	quietLogger(t)

	oldConfig := core.Config
	t.Cleanup(func() { core.Config = oldConfig })
	core.Config = &core.Configuration{
		Tunnels: map[string]*core.TunnelConfig{},
	}

	d := &Daemon{
		tunnels:       make(map[string]Tunnel),
		askpassTokens: make(map[string]string),
		companionMgr:  NewCompanionManager(),
	}

	// wasOnline=false, isOnline=true (transition to online)
	d.handleOnlineChange(false, true)
}

func TestHandleOnlineChange_Offline(t *testing.T) {
	quietLogger(t)

	oldConfig := core.Config
	t.Cleanup(func() { core.Config = oldConfig })
	core.Config = &core.Configuration{
		Tunnels: map[string]*core.TunnelConfig{},
	}

	d := &Daemon{
		tunnels:       make(map[string]Tunnel),
		askpassTokens: make(map[string]string),
		companionMgr:  NewCompanionManager(),
	}

	// wasOnline=true, isOnline=false (transition to offline)
	d.handleOnlineChange(true, false)
}
