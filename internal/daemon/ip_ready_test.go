package daemon

import (
	"context"
	"testing"

	"go.olrik.dev/overseer/internal/core"
)

func TestStartTunnelWhenIPReady_TunnelAlreadyExists(t *testing.T) {
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

	// Pre-populate a tunnel so the method returns immediately
	d.tunnels["existing-tunnel"] = Tunnel{
		Hostname: "test.example.com",
		Pid:      99999,
		State:    StateConnected,
	}

	// This should return immediately because the tunnel already exists
	d.startTunnelWhenIPReady("existing-tunnel", "")
}

func TestStartTunnelWhenIPReady_IPAlreadyKnown(t *testing.T) {
	quietLogger(t)

	old := stateOrchestrator
	t.Cleanup(func() { stateOrchestrator = old })
	stateOrchestrator = nil // nil orchestrator = IP always "known"

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

	// With nil orchestrator, isPublicIPKnown returns true immediately
	// startTunnelWhenIPReady will call startTunnel which will fail (no SSH)
	// but the code path is exercised
	d.startTunnelWhenIPReady("test-tunnel", "")
}

