package daemon

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"go.olrik.dev/overseer/internal/awareness/state"
	"go.olrik.dev/overseer/internal/core"
)

func TestGetSensorStatePath(t *testing.T) {
	oldConfig := core.Config
	defer func() { core.Config = oldConfig }()

	core.Config = &core.Configuration{
		ConfigPath: "/tmp/test-overseer",
	}

	expected := "/tmp/test-overseer/sensor_state.json"
	if got := GetSensorStatePath(); got != expected {
		t.Errorf("expected %q, got %q", expected, got)
	}
}

func TestLoadSensorState_FileDoesNotExist(t *testing.T) {
	tmpDir := t.TempDir()

	oldConfig := core.Config
	defer func() { core.Config = oldConfig }()
	core.Config = &core.Configuration{
		ConfigPath: tmpDir,
	}

	loaded, err := LoadSensorState()
	if err != nil {
		t.Fatalf("expected nil error, got %v", err)
	}
	if loaded != nil {
		t.Errorf("expected nil state, got %v", loaded)
	}
}

func TestLoadSensorState_ValidJSON(t *testing.T) {
	tmpDir := t.TempDir()

	oldConfig := core.Config
	defer func() { core.Config = oldConfig }()
	core.Config = &core.Configuration{
		ConfigPath: tmpDir,
	}

	online := true
	stateFile := SensorStateFile{
		Version:   sensorStateFileVersion,
		Timestamp: time.Now().Format(time.RFC3339),
		Sensors: []state.SensorCacheEntry{
			{
				Sensor:    "tcp",
				Timestamp: time.Now().Format(time.RFC3339Nano),
				Online:    &online,
			},
			{
				Sensor:    "public_ipv4",
				Timestamp: time.Now().Format(time.RFC3339Nano),
				IP:        "1.2.3.4",
			},
		},
	}

	data, err := json.Marshal(stateFile)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(tmpDir, "sensor_state.json"), data, 0600); err != nil {
		t.Fatal(err)
	}

	loaded, err := LoadSensorState()
	if err != nil {
		t.Fatalf("LoadSensorState failed: %v", err)
	}
	if loaded == nil {
		t.Fatal("expected non-nil state")
	}
	if loaded.Version != sensorStateFileVersion {
		t.Errorf("expected version %q, got %q", sensorStateFileVersion, loaded.Version)
	}
	if len(loaded.Sensors) != 2 {
		t.Fatalf("expected 2 sensors, got %d", len(loaded.Sensors))
	}
	if loaded.Sensors[0].Sensor != "tcp" {
		t.Errorf("expected first sensor 'tcp', got %q", loaded.Sensors[0].Sensor)
	}
	if loaded.Sensors[0].Online == nil || !*loaded.Sensors[0].Online {
		t.Error("expected tcp sensor online=true")
	}
	if loaded.Sensors[1].IP != "1.2.3.4" {
		t.Errorf("expected IP '1.2.3.4', got %q", loaded.Sensors[1].IP)
	}
}

func TestLoadSensorState_InvalidJSON(t *testing.T) {
	tmpDir := t.TempDir()

	oldConfig := core.Config
	defer func() { core.Config = oldConfig }()
	core.Config = &core.Configuration{
		ConfigPath: tmpDir,
	}

	if err := os.WriteFile(filepath.Join(tmpDir, "sensor_state.json"), []byte("{bad json"), 0600); err != nil {
		t.Fatal(err)
	}

	_, err := LoadSensorState()
	if err == nil {
		t.Fatal("expected error for invalid JSON")
	}
}

func TestLoadSensorState_WrongVersion(t *testing.T) {
	tmpDir := t.TempDir()

	oldConfig := core.Config
	defer func() { core.Config = oldConfig }()
	core.Config = &core.Configuration{
		ConfigPath: tmpDir,
	}

	data, _ := json.Marshal(SensorStateFile{
		Version:   "999",
		Timestamp: time.Now().Format(time.RFC3339),
	})
	if err := os.WriteFile(filepath.Join(tmpDir, "sensor_state.json"), data, 0600); err != nil {
		t.Fatal(err)
	}

	_, err := LoadSensorState()
	if err == nil {
		t.Fatal("expected error for wrong version")
	}
}

func TestRemoveSensorStateFile(t *testing.T) {
	t.Run("file exists", func(t *testing.T) {
		tmpDir := t.TempDir()

		oldConfig := core.Config
		defer func() { core.Config = oldConfig }()
		core.Config = &core.Configuration{
			ConfigPath: tmpDir,
		}

		path := filepath.Join(tmpDir, "sensor_state.json")
		if err := os.WriteFile(path, []byte("{}"), 0600); err != nil {
			t.Fatal(err)
		}

		if err := RemoveSensorStateFile(); err != nil {
			t.Fatalf("RemoveSensorStateFile failed: %v", err)
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

		if err := RemoveSensorStateFile(); err != nil {
			t.Fatalf("expected no error, got %v", err)
		}
	})
}

func TestSaveSensorState_NilOrchestrator(t *testing.T) {
	old := stateOrchestrator
	t.Cleanup(func() { stateOrchestrator = old })
	stateOrchestrator = nil

	tmpDir := t.TempDir()
	oldConfig := core.Config
	t.Cleanup(func() { core.Config = oldConfig })
	core.Config = &core.Configuration{
		ConfigPath: tmpDir,
	}

	err := SaveSensorState()
	if err != nil {
		t.Errorf("expected nil error for nil orchestrator, got: %v", err)
	}

	// No file should be written
	loaded, err := LoadSensorState()
	if err != nil {
		t.Fatalf("LoadSensorState failed: %v", err)
	}
	if loaded != nil {
		t.Error("expected nil state when orchestrator is nil")
	}
}

func TestSaveSensorState_WithOrchestrator(t *testing.T) {
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

	// SaveSensorState should succeed (may or may not write a file depending on sensor cache)
	err := SaveSensorState()
	if err != nil {
		t.Errorf("SaveSensorState failed: %v", err)
	}
}

func TestSensorState_RoundTrip(t *testing.T) {
	tmpDir := t.TempDir()

	oldConfig := core.Config
	defer func() { core.Config = oldConfig }()
	core.Config = &core.Configuration{
		ConfigPath: tmpDir,
	}

	// Write a sensor state file manually
	online := true
	stateFile := SensorStateFile{
		Version:   sensorStateFileVersion,
		Timestamp: time.Now().Format(time.RFC3339),
		Sensors: []state.SensorCacheEntry{
			{
				Sensor:    "tcp",
				Timestamp: time.Now().Format(time.RFC3339Nano),
				Online:    &online,
			},
			{
				Sensor:    "public_ipv4",
				Timestamp: time.Now().Format(time.RFC3339Nano),
				IP:        "203.0.113.1",
			},
			{
				Sensor:    "env_test",
				Timestamp: time.Now().Format(time.RFC3339Nano),
				Value:     "test_value",
			},
		},
	}

	data, err := json.MarshalIndent(stateFile, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(tmpDir, "sensor_state.json"), data, 0600); err != nil {
		t.Fatal(err)
	}

	// Load and verify
	loaded, err := LoadSensorState()
	if err != nil {
		t.Fatalf("LoadSensorState failed: %v", err)
	}
	if loaded == nil {
		t.Fatal("expected non-nil state")
	}

	if len(loaded.Sensors) != 3 {
		t.Fatalf("expected 3 sensors, got %d", len(loaded.Sensors))
	}

	sensorMap := map[string]state.SensorCacheEntry{}
	for _, s := range loaded.Sensors {
		sensorMap[s.Sensor] = s
	}

	tcp, ok := sensorMap["tcp"]
	if !ok {
		t.Fatal("expected 'tcp' sensor")
	}
	if tcp.Online == nil || !*tcp.Online {
		t.Error("expected tcp online=true")
	}

	ipv4, ok := sensorMap["public_ipv4"]
	if !ok {
		t.Fatal("expected 'public_ipv4' sensor")
	}
	if ipv4.IP != "203.0.113.1" {
		t.Errorf("expected IP '203.0.113.1', got %q", ipv4.IP)
	}

	env, ok := sensorMap["env_test"]
	if !ok {
		t.Fatal("expected 'env_test' sensor")
	}
	if env.Value != "test_value" {
		t.Errorf("expected value 'test_value', got %q", env.Value)
	}
}
