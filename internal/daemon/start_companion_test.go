package daemon

import (
	"context"
	"testing"

	"go.olrik.dev/overseer/internal/core"
)

func TestStartSingleCompanion_NoTunnelConfig(t *testing.T) {
	quietLogger(t)

	oldConfig := core.Config
	t.Cleanup(func() { core.Config = oldConfig })
	core.Config = &core.Configuration{
		Tunnels: map[string]*core.TunnelConfig{},
	}

	cm := NewCompanionManager()

	err := cm.StartSingleCompanion("nonexistent-tunnel", "comp1")
	if err == nil {
		t.Fatal("expected error for nonexistent tunnel config")
	}
}

func TestStartSingleCompanion_CompanionNotInConfig(t *testing.T) {
	quietLogger(t)

	oldConfig := core.Config
	t.Cleanup(func() { core.Config = oldConfig })
	core.Config = &core.Configuration{
		Tunnels: map[string]*core.TunnelConfig{
			"my-tunnel": {
				Name:       "my-tunnel",
				Companions: []core.CompanionConfig{},
			},
		},
	}

	cm := NewCompanionManager()

	err := cm.StartSingleCompanion("my-tunnel", "nonexistent-comp")
	if err == nil {
		t.Fatal("expected error for nonexistent companion in config")
	}
}

func TestStartSingleCompanion_AlreadyRunning(t *testing.T) {
	quietLogger(t)

	oldConfig := core.Config
	t.Cleanup(func() { core.Config = oldConfig })
	core.Config = &core.Configuration{
		Tunnels: map[string]*core.TunnelConfig{
			"my-tunnel": {
				Name: "my-tunnel",
				Companions: []core.CompanionConfig{
					{Name: "my-comp", Command: "echo hello"},
				},
			},
		},
	}

	cm := NewCompanionManager()

	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)

	cm.companions["my-tunnel"] = map[string]*CompanionProcess{
		"my-comp": {
			Name:   "my-comp",
			State:  CompanionStateRunning,
			ctx:    ctx,
			cancel: cancel,
		},
	}

	err := cm.StartSingleCompanion("my-tunnel", "my-comp")
	if err == nil {
		t.Fatal("expected error when companion is already running")
	}
}

func TestStartSingleCompanion_AlreadyReady(t *testing.T) {
	quietLogger(t)

	oldConfig := core.Config
	t.Cleanup(func() { core.Config = oldConfig })
	core.Config = &core.Configuration{
		Tunnels: map[string]*core.TunnelConfig{
			"my-tunnel": {
				Name: "my-tunnel",
				Companions: []core.CompanionConfig{
					{Name: "my-comp", Command: "echo hello"},
				},
			},
		},
	}

	cm := NewCompanionManager()

	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)

	cm.companions["my-tunnel"] = map[string]*CompanionProcess{
		"my-comp": {
			Name:   "my-comp",
			State:  CompanionStateReady,
			ctx:    ctx,
			cancel: cancel,
		},
	}

	err := cm.StartSingleCompanion("my-tunnel", "my-comp")
	if err == nil {
		t.Fatal("expected error when companion is already ready")
	}
}

func TestStartSingleCompanion_AlreadyWaiting(t *testing.T) {
	quietLogger(t)

	oldConfig := core.Config
	t.Cleanup(func() { core.Config = oldConfig })
	core.Config = &core.Configuration{
		Tunnels: map[string]*core.TunnelConfig{
			"my-tunnel": {
				Name: "my-tunnel",
				Companions: []core.CompanionConfig{
					{Name: "my-comp", Command: "echo hello"},
				},
			},
		},
	}

	cm := NewCompanionManager()

	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)

	cm.companions["my-tunnel"] = map[string]*CompanionProcess{
		"my-comp": {
			Name:   "my-comp",
			State:  CompanionStateWaiting,
			ctx:    ctx,
			cancel: cancel,
		},
	}

	err := cm.StartSingleCompanion("my-tunnel", "my-comp")
	if err == nil {
		t.Fatal("expected error when companion is already waiting")
	}
}

func TestRestartSingleCompanion_NoTunnelConfig(t *testing.T) {
	quietLogger(t)

	oldConfig := core.Config
	t.Cleanup(func() { core.Config = oldConfig })
	core.Config = &core.Configuration{
		Tunnels: map[string]*core.TunnelConfig{},
	}

	cm := NewCompanionManager()

	err := cm.RestartSingleCompanion("nonexistent-tunnel", "comp1")
	if err == nil {
		t.Fatal("expected error for nonexistent tunnel config")
	}
}

func TestRestartSingleCompanion_CompanionNotInConfig(t *testing.T) {
	quietLogger(t)

	oldConfig := core.Config
	t.Cleanup(func() { core.Config = oldConfig })
	core.Config = &core.Configuration{
		Tunnels: map[string]*core.TunnelConfig{
			"my-tunnel": {
				Name:       "my-tunnel",
				Companions: []core.CompanionConfig{},
			},
		},
	}

	cm := NewCompanionManager()

	err := cm.RestartSingleCompanion("my-tunnel", "nonexistent-comp")
	if err == nil {
		t.Fatal("expected error for nonexistent companion in config")
	}
}
