package daemon

import (
	"context"
	"path/filepath"
	"testing"

	"go.olrik.dev/overseer/internal/core"
	"go.olrik.dev/overseer/internal/db"
)

func TestInitStateOrchestrator_WithExports(t *testing.T) {
	quietLogger(t)

	tmpDir := t.TempDir()

	oldConfig := core.Config
	t.Cleanup(func() { core.Config = oldConfig })
	core.Config = &core.Configuration{
		ConfigPath: tmpDir,
		Companion:  core.CompanionSettings{HistorySize: 50},
		Locations:  map[string]*core.Location{},
		Contexts:   []*core.ContextRule{},
		Exports: []core.ExportConfig{
			{Type: "dotenv", Path: filepath.Join(tmpDir, "env.env")},
			{Type: "context", Path: filepath.Join(tmpDir, "context")},
			{Type: "location", Path: filepath.Join(tmpDir, "location")},
			{Type: "public_ip", Path: filepath.Join(tmpDir, "ip")},
			{Type: "unknown_type", Path: filepath.Join(tmpDir, "unknown")},
		},
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
	t.Cleanup(d.cancelFunc)
	d.companionMgr = NewCompanionManager()

	if err := d.initStateOrchestrator(); err != nil {
		t.Fatalf("initStateOrchestrator failed: %v", err)
	}
	if stateOrchestrator == nil {
		t.Fatal("expected stateOrchestrator to be non-nil")
	}
}

func TestInitStateOrchestrator_WithLocationsAndContexts(t *testing.T) {
	quietLogger(t)

	tmpDir := t.TempDir()

	oldConfig := core.Config
	t.Cleanup(func() { core.Config = oldConfig })
	core.Config = &core.Configuration{
		ConfigPath: tmpDir,
		Companion:  core.CompanionSettings{HistorySize: 50},
		Locations: map[string]*core.Location{
			"home": {
				Name:        "home",
				DisplayName: "Home",
				Conditions:  map[string][]string{"public_ipv4": {"1.2.3.4"}},
				Environment: map[string]string{"LOC": "home"},
			},
			"offline": {
				Name:        "offline",
				DisplayName: "Offline (custom)",
				Environment: map[string]string{"LOC": "offline"},
			},
			"unknown": {
				Name:        "unknown",
				DisplayName: "Unknown (custom)",
				Environment: map[string]string{"LOC": "unknown"},
			},
		},
		Contexts: []*core.ContextRule{
			{
				Name:        "trusted",
				DisplayName: "Trusted",
				Locations:   []string{"home"},
				Environment: map[string]string{"CTX": "trusted"},
				Actions: core.ContextActions{
					Connect:    []string{"tunnel1"},
					Disconnect: []string{},
				},
			},
			{
				Name:        "untrusted",
				DisplayName: "Untrusted (custom)",
				Environment: map[string]string{"CTX": "untrusted"},
				Actions: core.ContextActions{
					Disconnect: []string{"tunnel1"},
				},
			},
		},
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
	t.Cleanup(d.cancelFunc)
	d.companionMgr = NewCompanionManager()

	if err := d.initStateOrchestrator(); err != nil {
		t.Fatalf("initStateOrchestrator failed: %v", err)
	}
	if stateOrchestrator == nil {
		t.Fatal("expected stateOrchestrator to be non-nil")
	}
}

func TestInitStateOrchestrator_WithDatabase(t *testing.T) {
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
		tunnels:  make(map[string]Tunnel),
		database: database,
	}
	d.ctx, d.cancelFunc = context.WithCancel(context.Background())
	t.Cleanup(d.cancelFunc)
	d.companionMgr = NewCompanionManager()

	if err := d.initStateOrchestrator(); err != nil {
		t.Fatalf("initStateOrchestrator failed: %v", err)
	}
}

func TestExpandPath_HomeTilde(t *testing.T) {
	result := expandPath("~/some/path")
	if result == "~/some/path" {
		t.Error("expected tilde to be expanded")
	}
	if result == "" {
		t.Error("expected non-empty path")
	}
}

func TestExpandPath_NoTilde(t *testing.T) {
	result := expandPath("/absolute/path")
	if result != "/absolute/path" {
		t.Errorf("expected unchanged path, got %q", result)
	}
}

func TestExpandPath_RelativePath(t *testing.T) {
	result := expandPath("relative/path")
	if result != "relative/path" {
		t.Errorf("expected unchanged path, got %q", result)
	}
}
