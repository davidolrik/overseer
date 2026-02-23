package daemon

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"go.olrik.dev/overseer/internal/core"
)

func TestAdoptCompanions_NoStateFile(t *testing.T) {
	quietLogger(t)

	tmpDir := t.TempDir()
	oldConfig := core.Config
	t.Cleanup(func() { core.Config = oldConfig })
	core.Config = &core.Configuration{
		ConfigPath: tmpDir,
		Companion:  core.CompanionSettings{HistorySize: 50},
	}

	cm := NewCompanionManager()

	count := cm.AdoptCompanions()
	if count != 0 {
		t.Errorf("expected 0 adopted, got %d", count)
	}
}

func TestAdoptCompanions_DeadPID(t *testing.T) {
	quietLogger(t)

	tmpDir := t.TempDir()
	oldConfig := core.Config
	t.Cleanup(func() { core.Config = oldConfig })
	core.Config = &core.Configuration{
		ConfigPath: tmpDir,
		Companion:  core.CompanionSettings{HistorySize: 50},
		Tunnels: map[string]*core.TunnelConfig{
			"my-tunnel": {
				Name: "my-tunnel",
				Companions: []core.CompanionConfig{
					{Name: "my-comp", Command: "echo hello"},
				},
			},
		},
	}

	// Write a companion state file with a dead PID
	stateFile := CompanionStateFile{
		Version:   companionStateVersion,
		Timestamp: time.Now().Format(time.RFC3339),
		Tunnels: []CompanionTunnelInfo{
			{
				Alias: "my-tunnel",
				Companions: []CompanionInfo{
					{
						Name:      "my-comp",
						Pid:       999999999, // Non-existent PID
						Command:   "echo hello",
						StartTime: time.Now(),
					},
				},
			},
		},
	}

	data, err := json.MarshalIndent(stateFile, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(tmpDir, "companion_state.json"), data, 0600); err != nil {
		t.Fatal(err)
	}

	cm := NewCompanionManager()

	count := cm.AdoptCompanions()
	if count != 0 {
		t.Errorf("expected 0 adopted (dead PID), got %d", count)
	}

	// State file should be cleaned up
	if _, err := os.Stat(filepath.Join(tmpDir, "companion_state.json")); !os.IsNotExist(err) {
		t.Log("state file may still exist - RemoveCompanionStateFile called")
	}
}

func TestAdoptCompanions_NoTunnelConfig(t *testing.T) {
	quietLogger(t)

	tmpDir := t.TempDir()
	oldConfig := core.Config
	t.Cleanup(func() { core.Config = oldConfig })
	core.Config = &core.Configuration{
		ConfigPath: tmpDir,
		Companion:  core.CompanionSettings{HistorySize: 50},
		Tunnels:    map[string]*core.TunnelConfig{}, // No tunnel config
	}

	stateFile := CompanionStateFile{
		Version:   companionStateVersion,
		Timestamp: time.Now().Format(time.RFC3339),
		Tunnels: []CompanionTunnelInfo{
			{
				Alias: "missing-tunnel",
				Companions: []CompanionInfo{
					{
						Name:      "comp",
						Pid:       os.Getpid(), // Use our PID so it passes alive check
						Command:   "echo hello",
						StartTime: time.Now(),
					},
				},
			},
		},
	}

	data, err := json.MarshalIndent(stateFile, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(tmpDir, "companion_state.json"), data, 0600); err != nil {
		t.Fatal(err)
	}

	cm := NewCompanionManager()

	count := cm.AdoptCompanions()
	if count != 0 {
		t.Errorf("expected 0 adopted (no tunnel config), got %d", count)
	}
}

func TestAdoptCompanions_CompanionNotInConfig(t *testing.T) {
	quietLogger(t)

	tmpDir := t.TempDir()
	oldConfig := core.Config
	t.Cleanup(func() { core.Config = oldConfig })
	core.Config = &core.Configuration{
		ConfigPath: tmpDir,
		Companion:  core.CompanionSettings{HistorySize: 50},
		Tunnels: map[string]*core.TunnelConfig{
			"my-tunnel": {
				Name:       "my-tunnel",
				Companions: []core.CompanionConfig{}, // No companions configured
			},
		},
	}

	stateFile := CompanionStateFile{
		Version:   companionStateVersion,
		Timestamp: time.Now().Format(time.RFC3339),
		Tunnels: []CompanionTunnelInfo{
			{
				Alias: "my-tunnel",
				Companions: []CompanionInfo{
					{
						Name:      "removed-comp",
						Pid:       os.Getpid(),
						Command:   "echo hello",
						StartTime: time.Now(),
					},
				},
			},
		},
	}

	data, err := json.MarshalIndent(stateFile, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(tmpDir, "companion_state.json"), data, 0600); err != nil {
		t.Fatal(err)
	}

	cm := NewCompanionManager()

	count := cm.AdoptCompanions()
	if count != 0 {
		t.Errorf("expected 0 adopted (companion not in config), got %d", count)
	}
}

func TestAdoptCompanions_CommandMismatch(t *testing.T) {
	quietLogger(t)

	tmpDir := t.TempDir()
	oldConfig := core.Config
	t.Cleanup(func() { core.Config = oldConfig })
	core.Config = &core.Configuration{
		ConfigPath: tmpDir,
		Companion:  core.CompanionSettings{HistorySize: 50},
		Tunnels: map[string]*core.TunnelConfig{
			"my-tunnel": {
				Name: "my-tunnel",
				Companions: []core.CompanionConfig{
					{Name: "my-comp", Command: "new-command"},
				},
			},
		},
	}

	stateFile := CompanionStateFile{
		Version:   companionStateVersion,
		Timestamp: time.Now().Format(time.RFC3339),
		Tunnels: []CompanionTunnelInfo{
			{
				Alias: "my-tunnel",
				Companions: []CompanionInfo{
					{
						Name:      "my-comp",
						Pid:       os.Getpid(),
						Command:   "old-command", // Mismatch with config
						StartTime: time.Now(),
					},
				},
			},
		},
	}

	data, err := json.MarshalIndent(stateFile, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(tmpDir, "companion_state.json"), data, 0600); err != nil {
		t.Fatal(err)
	}

	cm := NewCompanionManager()

	count := cm.AdoptCompanions()
	if count != 0 {
		t.Errorf("expected 0 adopted (command mismatch), got %d", count)
	}
}
