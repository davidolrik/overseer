package daemon

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"go.olrik.dev/overseer/internal/core"
)

func TestGetTunnelStatePath(t *testing.T) {
	oldConfig := core.Config
	defer func() { core.Config = oldConfig }()

	core.Config = &core.Configuration{
		ConfigPath: "/tmp/test-overseer",
	}

	expected := "/tmp/test-overseer/tunnel_state.json"
	if got := GetTunnelStatePath(); got != expected {
		t.Errorf("expected %q, got %q", expected, got)
	}
}

func TestSaveTunnelState(t *testing.T) {
	tmpDir := t.TempDir()

	oldConfig := core.Config
	defer func() { core.Config = oldConfig }()
	core.Config = &core.Configuration{
		ConfigPath: tmpDir,
	}

	d := &Daemon{
		tunnels: map[string]Tunnel{
			"server1": {
				Hostname:          "server1.example.com",
				Pid:               12345,
				StartDate:         time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC),
				LastConnectedTime: time.Date(2025, 1, 1, 0, 1, 0, 0, time.UTC),
				RetryCount:        2,
				TotalReconnects:   5,
				AutoReconnect:     true,
				State:             StateConnected,
				Environment:       map[string]string{"OVERSEER_TAG": "work"},
				ResolvedHost:      "1.2.3.4",
				JumpChain:         []string{"jump1", "jump2"},
			},
		},
	}

	if err := d.SaveTunnelState(); err != nil {
		t.Fatalf("SaveTunnelState failed: %v", err)
	}

	// Verify file exists and is valid JSON
	statePath := filepath.Join(tmpDir, "tunnel_state.json")
	data, err := os.ReadFile(statePath)
	if err != nil {
		t.Fatalf("failed to read state file: %v", err)
	}

	var stateFile TunnelStateFile
	if err := json.Unmarshal(data, &stateFile); err != nil {
		t.Fatalf("failed to parse state file: %v", err)
	}

	if stateFile.Version != stateFileVersion {
		t.Errorf("expected version %q, got %q", stateFileVersion, stateFile.Version)
	}
	if len(stateFile.Tunnels) != 1 {
		t.Fatalf("expected 1 tunnel, got %d", len(stateFile.Tunnels))
	}

	info := stateFile.Tunnels[0]
	if info.Alias != "server1" {
		t.Errorf("expected alias 'server1', got %q", info.Alias)
	}
	if info.PID != 12345 {
		t.Errorf("expected PID 12345, got %d", info.PID)
	}
	if info.State != "connected" {
		t.Errorf("expected state 'connected', got %q", info.State)
	}
	if info.Environment["OVERSEER_TAG"] != "work" {
		t.Errorf("expected environment OVERSEER_TAG='work', got %q", info.Environment["OVERSEER_TAG"])
	}
	if info.ResolvedHost != "1.2.3.4" {
		t.Errorf("expected resolved host '1.2.3.4', got %q", info.ResolvedHost)
	}
	if len(info.JumpChain) != 2 || info.JumpChain[0] != "jump1" {
		t.Errorf("expected jump chain [jump1, jump2], got %v", info.JumpChain)
	}
}

func TestSaveTunnelState_SkipsZeroPID(t *testing.T) {
	tmpDir := t.TempDir()

	oldConfig := core.Config
	defer func() { core.Config = oldConfig }()
	core.Config = &core.Configuration{
		ConfigPath: tmpDir,
	}

	d := &Daemon{
		tunnels: map[string]Tunnel{
			"server1": {
				Pid:   0, // No PID — should be skipped
				State: StateConnecting,
			},
		},
	}

	if err := d.SaveTunnelState(); err != nil {
		t.Fatalf("SaveTunnelState failed: %v", err)
	}

	data, err := os.ReadFile(filepath.Join(tmpDir, "tunnel_state.json"))
	if err != nil {
		t.Fatalf("failed to read state file: %v", err)
	}

	var stateFile TunnelStateFile
	if err := json.Unmarshal(data, &stateFile); err != nil {
		t.Fatalf("failed to parse: %v", err)
	}

	if len(stateFile.Tunnels) != 0 {
		t.Errorf("expected 0 tunnels (PID=0 skipped), got %d", len(stateFile.Tunnels))
	}
}

func TestLoadTunnelState_FileExists(t *testing.T) {
	tmpDir := t.TempDir()

	oldConfig := core.Config
	defer func() { core.Config = oldConfig }()
	core.Config = &core.Configuration{
		ConfigPath: tmpDir,
	}

	stateFile := TunnelStateFile{
		Version:   stateFileVersion,
		Timestamp: time.Now().Format(time.RFC3339),
		Tunnels: []TunnelInfo{
			{
				PID:           42,
				Alias:         "myserver",
				Hostname:      "myserver.example.com",
				State:         "connected",
				AutoReconnect: true,
			},
		},
	}

	data, err := json.Marshal(stateFile)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(tmpDir, "tunnel_state.json"), data, 0600); err != nil {
		t.Fatal(err)
	}

	loaded, err := LoadTunnelState()
	if err != nil {
		t.Fatalf("LoadTunnelState failed: %v", err)
	}
	if loaded == nil {
		t.Fatal("expected non-nil state")
	}
	if loaded.Version != stateFileVersion {
		t.Errorf("expected version %q, got %q", stateFileVersion, loaded.Version)
	}
	if len(loaded.Tunnels) != 1 {
		t.Fatalf("expected 1 tunnel, got %d", len(loaded.Tunnels))
	}
	if loaded.Tunnels[0].Alias != "myserver" {
		t.Errorf("expected alias 'myserver', got %q", loaded.Tunnels[0].Alias)
	}
}

func TestLoadTunnelState_FileDoesNotExist(t *testing.T) {
	tmpDir := t.TempDir()

	oldConfig := core.Config
	defer func() { core.Config = oldConfig }()
	core.Config = &core.Configuration{
		ConfigPath: tmpDir,
	}

	loaded, err := LoadTunnelState()
	if err != nil {
		t.Fatalf("expected nil error, got %v", err)
	}
	if loaded != nil {
		t.Errorf("expected nil state, got %v", loaded)
	}
}

func TestLoadTunnelState_InvalidJSON(t *testing.T) {
	tmpDir := t.TempDir()

	oldConfig := core.Config
	defer func() { core.Config = oldConfig }()
	core.Config = &core.Configuration{
		ConfigPath: tmpDir,
	}

	if err := os.WriteFile(filepath.Join(tmpDir, "tunnel_state.json"), []byte("not json"), 0600); err != nil {
		t.Fatal(err)
	}

	_, err := LoadTunnelState()
	if err == nil {
		t.Fatal("expected error for invalid JSON")
	}
}

func TestLoadTunnelState_WrongVersion(t *testing.T) {
	tmpDir := t.TempDir()

	oldConfig := core.Config
	defer func() { core.Config = oldConfig }()
	core.Config = &core.Configuration{
		ConfigPath: tmpDir,
	}

	data, _ := json.Marshal(TunnelStateFile{
		Version:   "999",
		Timestamp: time.Now().Format(time.RFC3339),
	})
	if err := os.WriteFile(filepath.Join(tmpDir, "tunnel_state.json"), data, 0600); err != nil {
		t.Fatal(err)
	}

	_, err := LoadTunnelState()
	if err == nil {
		t.Fatal("expected error for wrong version")
	}
}

func TestRemoveTunnelStateFile(t *testing.T) {
	t.Run("file exists", func(t *testing.T) {
		tmpDir := t.TempDir()

		oldConfig := core.Config
		defer func() { core.Config = oldConfig }()
		core.Config = &core.Configuration{
			ConfigPath: tmpDir,
		}

		path := filepath.Join(tmpDir, "tunnel_state.json")
		if err := os.WriteFile(path, []byte("{}"), 0600); err != nil {
			t.Fatal(err)
		}

		if err := RemoveTunnelStateFile(); err != nil {
			t.Fatalf("RemoveTunnelStateFile failed: %v", err)
		}

		if _, err := os.Stat(path); !os.IsNotExist(err) {
			t.Error("expected file to be removed")
		}
	})

	t.Run("file does not exist", func(t *testing.T) {
		tmpDir := t.TempDir()

		oldConfig := core.Config
		defer func() { core.Config = oldConfig }()
		core.Config = &core.Configuration{
			ConfigPath: tmpDir,
		}

		// Should not return error when file doesn't exist
		if err := RemoveTunnelStateFile(); err != nil {
			t.Fatalf("expected no error, got %v", err)
		}
	})
}

func TestTunnelState_RoundTrip(t *testing.T) {
	tmpDir := t.TempDir()

	oldConfig := core.Config
	defer func() { core.Config = oldConfig }()
	core.Config = &core.Configuration{
		ConfigPath: tmpDir,
	}

	startDate := time.Date(2025, 6, 15, 10, 30, 0, 0, time.UTC)
	connDate := time.Date(2025, 6, 15, 10, 31, 0, 0, time.UTC)

	d := &Daemon{
		tunnels: map[string]Tunnel{
			"prod": {
				Hostname:          "prod.example.com",
				Pid:               9999,
				StartDate:         startDate,
				LastConnectedTime: connDate,
				RetryCount:        3,
				TotalReconnects:   10,
				AutoReconnect:     true,
				State:             StateConnected,
				Environment:       map[string]string{"OVERSEER_TAG": "production"},
				ResolvedHost:      "10.0.0.1",
				JumpChain:         []string{"bastion1", "bastion2"},
			},
		},
	}

	// Save
	if err := d.SaveTunnelState(); err != nil {
		t.Fatalf("save failed: %v", err)
	}

	// Load
	loaded, err := LoadTunnelState()
	if err != nil {
		t.Fatalf("load failed: %v", err)
	}
	if loaded == nil {
		t.Fatal("expected non-nil state")
	}

	if len(loaded.Tunnels) != 1 {
		t.Fatalf("expected 1 tunnel, got %d", len(loaded.Tunnels))
	}

	info := loaded.Tunnels[0]
	if info.PID != 9999 {
		t.Errorf("PID: expected 9999, got %d", info.PID)
	}
	if info.Alias != "prod" {
		t.Errorf("Alias: expected 'prod', got %q", info.Alias)
	}
	if info.Hostname != "prod.example.com" {
		t.Errorf("Hostname: expected 'prod.example.com', got %q", info.Hostname)
	}
	if info.State != "connected" {
		t.Errorf("State: expected 'connected', got %q", info.State)
	}
	if info.Environment["OVERSEER_TAG"] != "production" {
		t.Errorf("Environment OVERSEER_TAG: expected 'production', got %q", info.Environment["OVERSEER_TAG"])
	}
	if info.ResolvedHost != "10.0.0.1" {
		t.Errorf("ResolvedHost: expected '10.0.0.1', got %q", info.ResolvedHost)
	}
	if len(info.JumpChain) != 2 || info.JumpChain[0] != "bastion1" || info.JumpChain[1] != "bastion2" {
		t.Errorf("JumpChain: expected [bastion1, bastion2], got %v", info.JumpChain)
	}
	if info.RetryCount != 3 {
		t.Errorf("RetryCount: expected 3, got %d", info.RetryCount)
	}
	if info.TotalReconnects != 10 {
		t.Errorf("TotalReconnects: expected 10, got %d", info.TotalReconnects)
	}
	if !info.AutoReconnect {
		t.Error("AutoReconnect: expected true")
	}
}
