package daemon

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"go.olrik.dev/overseer/internal/core"
)

func TestReloadConfig_ValidConfig(t *testing.T) {
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

	// Write a valid HCL config file
	configContent := `companion {
  history_size = 50
}
`
	if err := os.WriteFile(filepath.Join(tmpDir, "config.hcl"), []byte(configContent), 0600); err != nil {
		t.Fatal(err)
	}

	// reloadConfig should succeed
	err := d.reloadConfig()
	if err != nil {
		t.Errorf("reloadConfig failed: %v", err)
	}
}

func TestReloadConfig_InvalidConfig(t *testing.T) {
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

	d := &Daemon{
		tunnels: make(map[string]Tunnel),
	}
	d.ctx, d.cancelFunc = context.WithCancel(context.Background())
	d.companionMgr = NewCompanionManager()

	// Write invalid config
	if err := os.WriteFile(filepath.Join(tmpDir, "config.hcl"), []byte("{{{invalid"), 0600); err != nil {
		t.Fatal(err)
	}

	err := d.reloadConfig()
	if err == nil {
		t.Error("expected error for invalid config")
	}
}

func TestReloadConfig_MissingConfig(t *testing.T) {
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

	d := &Daemon{
		tunnels: make(map[string]Tunnel),
	}
	d.ctx, d.cancelFunc = context.WithCancel(context.Background())
	d.companionMgr = NewCompanionManager()

	// No config file exists
	err := d.reloadConfig()
	if err == nil {
		t.Error("expected error for missing config")
	}
}

func TestCheckOnlineStatusNew_NilOrchestrator(t *testing.T) {
	old := stateOrchestrator
	t.Cleanup(func() { stateOrchestrator = old })
	stateOrchestrator = nil

	d := &Daemon{}
	// Should fall back to checkOnlineStatus which returns false for nil orchestrator
	result := d.checkOnlineStatusNew()
	if result {
		t.Error("expected false for nil orchestrator")
	}
}

func TestCheckOnlineStatusNew_WithOrchestrator(t *testing.T) {
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
	_ = d.checkOnlineStatusNew()
}
