package daemon

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"go.olrik.dev/overseer/internal/awareness/state"
	"go.olrik.dev/overseer/internal/core"
)

// SensorStateFile contains the sensor cache for hot reload preservation
type SensorStateFile struct {
	Version   string                   `json:"version"`
	Timestamp string                   `json:"timestamp"`
	Sensors   []state.SensorCacheEntry `json:"sensors"`
}

const sensorStateFileVersion = "1"

// GetSensorStatePath returns the path to the sensor state file
func GetSensorStatePath() string {
	return filepath.Join(core.Config.ConfigPath, "sensor_state.json")
}

// SaveSensorState saves the current sensor cache to disk for hot reload
func SaveSensorState() error {
	if stateOrchestrator == nil {
		return nil // Nothing to save
	}

	sensors := stateOrchestrator.GetSensorCache()
	if len(sensors) == 0 {
		return nil // Nothing to save
	}

	stateFile := SensorStateFile{
		Version:   sensorStateFileVersion,
		Timestamp: time.Now().Format(time.RFC3339),
		Sensors:   sensors,
	}

	data, err := json.MarshalIndent(stateFile, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to marshal sensor state: %w", err)
	}

	// Atomic write: write to temp file, then rename
	statePath := GetSensorStatePath()
	tempPath := statePath + ".tmp"

	if err := os.WriteFile(tempPath, data, 0600); err != nil {
		return fmt.Errorf("failed to write sensor state temp file: %w", err)
	}

	if err := os.Rename(tempPath, statePath); err != nil {
		os.Remove(tempPath) // Clean up on error
		return fmt.Errorf("failed to rename sensor state file: %w", err)
	}

	return nil
}

// LoadSensorState reads the sensor state file from disk
// Returns nil if file doesn't exist (not an error - first run or clean shutdown)
func LoadSensorState() (*SensorStateFile, error) {
	statePath := GetSensorStatePath()

	// Check if file exists
	if _, err := os.Stat(statePath); os.IsNotExist(err) {
		return nil, nil // No state file - not an error
	}

	// Read file
	data, err := os.ReadFile(statePath)
	if err != nil {
		return nil, fmt.Errorf("failed to read sensor state file: %w", err)
	}

	// Parse JSON
	var stateFile SensorStateFile
	if err := json.Unmarshal(data, &stateFile); err != nil {
		return nil, fmt.Errorf("failed to parse sensor state file: %w", err)
	}

	// Validate version
	if stateFile.Version != sensorStateFileVersion {
		return nil, fmt.Errorf("unsupported sensor state file version: %s (expected %s)", stateFile.Version, sensorStateFileVersion)
	}

	return &stateFile, nil
}

// RemoveSensorStateFile removes the sensor state file
// Used after successful restoration or on clean shutdown
func RemoveSensorStateFile() error {
	statePath := GetSensorStatePath()

	if err := os.Remove(statePath); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("failed to remove sensor state file: %w", err)
	}

	return nil
}
