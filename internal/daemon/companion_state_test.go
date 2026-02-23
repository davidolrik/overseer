package daemon

import (
	"context"
	"testing"
	"time"

	"go.olrik.dev/overseer/internal/core"
)

func TestSaveCompanionState_NoRunningCompanions(t *testing.T) {
	quietLogger(t)

	tmpDir := t.TempDir()
	oldConfig := core.Config
	t.Cleanup(func() { core.Config = oldConfig })
	core.Config = &core.Configuration{ConfigPath: tmpDir}

	cm := NewCompanionManager()
	cm.companions["tunnel1"] = map[string]*CompanionProcess{
		"comp1": {
			Name:  "comp1",
			Pid:   0,
			State: CompanionStateStopped,
		},
	}

	if err := cm.SaveCompanionState(); err != nil {
		t.Fatalf("SaveCompanionState failed: %v", err)
	}

	// Load and verify - the file is written but with empty tunnels list
	loaded, err := LoadCompanionState()
	if err != nil {
		t.Fatalf("LoadCompanionState failed: %v", err)
	}
	if loaded == nil {
		return // Acceptable if no file was written for empty state
	}
	if len(loaded.Tunnels) != 0 {
		t.Errorf("expected 0 tunnels (stopped companions filtered), got %d", len(loaded.Tunnels))
	}
}

func TestSaveCompanionState_MultipleTunnels(t *testing.T) {
	quietLogger(t)

	tmpDir := t.TempDir()
	oldConfig := core.Config
	t.Cleanup(func() { core.Config = oldConfig })
	core.Config = &core.Configuration{ConfigPath: tmpDir}

	now := time.Now()
	ctx1, cancel1 := context.WithCancel(context.Background())
	defer cancel1()
	ctx2, cancel2 := context.WithCancel(context.Background())
	defer cancel2()

	cm := NewCompanionManager()
	cm.companions["tunnel1"] = map[string]*CompanionProcess{
		"comp1": {
			Name:      "comp1",
			Pid:       11111,
			State:     CompanionStateRunning,
			StartTime: now,
			Config:    core.CompanionConfig{Command: "echo t1"},
			ctx:       ctx1,
			cancel:    cancel1,
		},
	}
	cm.companions["tunnel2"] = map[string]*CompanionProcess{
		"comp2": {
			Name:      "comp2",
			Pid:       22222,
			State:     CompanionStateRunning,
			StartTime: now,
			Config:    core.CompanionConfig{Command: "echo t2"},
			ctx:       ctx2,
			cancel:    cancel2,
		},
	}

	if err := cm.SaveCompanionState(); err != nil {
		t.Fatalf("SaveCompanionState failed: %v", err)
	}

	loaded, err := LoadCompanionState()
	if err != nil {
		t.Fatalf("LoadCompanionState failed: %v", err)
	}
	if loaded == nil {
		t.Fatal("expected non-nil state")
	}
	if len(loaded.Tunnels) != 2 {
		t.Fatalf("expected 2 tunnels, got %d", len(loaded.Tunnels))
	}
}

func TestSaveCompanionState_FiltersMixed(t *testing.T) {
	quietLogger(t)

	tmpDir := t.TempDir()
	oldConfig := core.Config
	t.Cleanup(func() { core.Config = oldConfig })
	core.Config = &core.Configuration{ConfigPath: tmpDir}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	cm := NewCompanionManager()
	cm.companions["tunnel1"] = map[string]*CompanionProcess{
		"running": {
			Name:      "running",
			Pid:       11111,
			State:     CompanionStateRunning,
			StartTime: time.Now(),
			Config:    core.CompanionConfig{Command: "run"},
			ctx:       ctx,
			cancel:    cancel,
		},
		"stopped": {
			Name:  "stopped",
			Pid:   22222,
			State: CompanionStateStopped,
		},
		"failed": {
			Name:  "failed",
			Pid:   33333,
			State: CompanionStateFailed,
		},
		"zero-pid": {
			Name:  "zero-pid",
			Pid:   0,
			State: CompanionStateRunning, // Running but PID=0 should be filtered
		},
	}

	if err := cm.SaveCompanionState(); err != nil {
		t.Fatalf("SaveCompanionState failed: %v", err)
	}

	loaded, err := LoadCompanionState()
	if err != nil {
		t.Fatalf("LoadCompanionState failed: %v", err)
	}
	if loaded == nil {
		t.Fatal("expected non-nil state")
	}
	if len(loaded.Tunnels) != 1 {
		t.Fatalf("expected 1 tunnel, got %d", len(loaded.Tunnels))
	}
	// Only the running companion with valid PID should be saved
	if len(loaded.Tunnels[0].Companions) != 1 {
		t.Fatalf("expected 1 companion (only running with valid PID), got %d", len(loaded.Tunnels[0].Companions))
	}
	if loaded.Tunnels[0].Companions[0].Name != "running" {
		t.Errorf("expected companion 'running', got %q", loaded.Tunnels[0].Companions[0].Name)
	}
}

func TestSaveCompanionState_EmptyManager(t *testing.T) {
	quietLogger(t)

	tmpDir := t.TempDir()
	oldConfig := core.Config
	t.Cleanup(func() { core.Config = oldConfig })
	core.Config = &core.Configuration{ConfigPath: tmpDir}

	cm := NewCompanionManager()

	if err := cm.SaveCompanionState(); err != nil {
		t.Fatalf("SaveCompanionState failed: %v", err)
	}

	// Should still be loadable
	loaded, err := LoadCompanionState()
	if err != nil {
		t.Fatalf("LoadCompanionState failed: %v", err)
	}
	if loaded != nil && len(loaded.Tunnels) != 0 {
		t.Errorf("expected no tunnels, got %d", len(loaded.Tunnels))
	}
}

func TestLoadCompanionState_ValidRoundTrip(t *testing.T) {
	quietLogger(t)

	tmpDir := t.TempDir()
	oldConfig := core.Config
	t.Cleanup(func() { core.Config = oldConfig })
	core.Config = &core.Configuration{ConfigPath: tmpDir}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	cm := NewCompanionManager()
	cm.companions["server1"] = map[string]*CompanionProcess{
		"comp1": {
			Name:      "comp1",
			Pid:       12345,
			State:     CompanionStateRunning,
			StartTime: time.Now(),
			Config:    core.CompanionConfig{Command: "echo hello", Workdir: "/tmp"},
			ctx:       ctx,
			cancel:    cancel,
		},
	}

	if err := cm.SaveCompanionState(); err != nil {
		t.Fatalf("SaveCompanionState failed: %v", err)
	}

	loaded, err := LoadCompanionState()
	if err != nil {
		t.Fatalf("LoadCompanionState failed: %v", err)
	}
	if loaded == nil {
		t.Fatal("expected non-nil state")
	}
	if loaded.Version != companionStateVersion {
		t.Errorf("expected version %q, got %q", companionStateVersion, loaded.Version)
	}
	if loaded.Timestamp == "" {
		t.Error("expected non-empty timestamp")
	}
	if len(loaded.Tunnels) != 1 {
		t.Fatalf("expected 1 tunnel, got %d", len(loaded.Tunnels))
	}
	comp := loaded.Tunnels[0].Companions[0]
	if comp.Pid != 12345 {
		t.Errorf("expected pid 12345, got %d", comp.Pid)
	}
	if comp.Command != "echo hello" {
		t.Errorf("expected command 'echo hello', got %q", comp.Command)
	}
	if comp.Workdir != "/tmp" {
		t.Errorf("expected workdir '/tmp', got %q", comp.Workdir)
	}
}
