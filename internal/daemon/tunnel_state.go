package daemon

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"go.olrik.dev/overseer/internal/core"
)

// TunnelStateFile contains the schema version and tunnel information
type TunnelStateFile struct {
	Version   string       `json:"version"`
	Timestamp string       `json:"timestamp"`
	Tunnels   []TunnelInfo `json:"tunnels"`
}

// TunnelInfo contains all information needed to adopt an existing tunnel process
// SECURITY: Does NOT include askpass tokens - those are regenerated on adoption
type TunnelInfo struct {
	PID               int       `json:"pid"`
	Alias             string    `json:"alias"`
	Hostname          string    `json:"hostname"`
	Cmdline           []string  `json:"cmdline"`
	StartDate         time.Time `json:"start_date"`
	LastConnectedTime time.Time `json:"last_connected_time"`
	RetryCount        int       `json:"retry_count"`
	TotalReconnects   int       `json:"total_reconnects"`
	AutoReconnect     bool      `json:"auto_reconnect"`
	State             string    `json:"state"`
	Environment       map[string]string `json:"environment,omitempty"`
	ResolvedHost      string            `json:"resolved_host,omitempty"`
	JumpChain         []string  `json:"jump_chain,omitempty"`
	// Note: AskpassToken is NOT persisted for security reasons
	// New tokens will be generated when adopting tunnels
}

const stateFileVersion = "2"

// GetTunnelStatePath returns the path to the tunnel state file
func GetTunnelStatePath() string {
	return filepath.Join(core.Config.ConfigPath, "tunnel_state.json")
}

// SaveTunnelState atomically writes the current tunnel state to disk
// Uses temp file + rename for atomic writes
// SECURITY: Does not save askpass tokens
func (d *Daemon) SaveTunnelState() error {
	d.mu.Lock()
	defer d.mu.Unlock()

	// Build state from current tunnels
	var tunnelInfos []TunnelInfo

	for alias, tunnel := range d.tunnels {
		// Skip tunnels that don't have a valid PID
		if tunnel.Pid <= 0 {
			continue
		}

		// Build command line for validation
		// Note: We can't get the full cmdline from exec.Cmd after Start(),
		// so we reconstruct it based on our config
		cmdline := []string{"ssh", alias, "-N", "-o", "IgnoreUnknown=overseer-daemon", "-o", "overseer-daemon=true", "-o", "ExitOnForwardFailure=yes", "-v"}

		info := TunnelInfo{
			PID:               tunnel.Pid,
			Alias:             alias,
			Hostname:          tunnel.Hostname,
			Cmdline:           cmdline,
			StartDate:         tunnel.StartDate,
			LastConnectedTime: tunnel.LastConnectedTime,
			RetryCount:        tunnel.RetryCount,
			TotalReconnects:   tunnel.TotalReconnects,
			AutoReconnect:     tunnel.AutoReconnect,
			State:             string(tunnel.State),
			Environment:       tunnel.Environment,
			ResolvedHost:      tunnel.ResolvedHost,
			JumpChain:         tunnel.JumpChain,
			// AskpassToken intentionally omitted for security
		}

		tunnelInfos = append(tunnelInfos, info)
	}

	state := TunnelStateFile{
		Version:   stateFileVersion,
		Timestamp: time.Now().Format(time.RFC3339),
		Tunnels:   tunnelInfos,
	}

	// Marshal to JSON
	data, err := json.MarshalIndent(state, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to marshal tunnel state: %w", err)
	}

	// Atomic write: write to temp file, then rename
	statePath := GetTunnelStatePath()
	tempPath := statePath + ".tmp"

	if err := os.WriteFile(tempPath, data, 0600); err != nil {
		return fmt.Errorf("failed to write tunnel state temp file: %w", err)
	}

	if err := os.Rename(tempPath, statePath); err != nil {
		os.Remove(tempPath) // Clean up on error
		return fmt.Errorf("failed to rename tunnel state file: %w", err)
	}

	return nil
}

// LoadTunnelState reads the tunnel state file from disk
// Returns nil if file doesn't exist (not an error - first run)
func LoadTunnelState() (*TunnelStateFile, error) {
	statePath := GetTunnelStatePath()

	// Check if file exists
	if _, err := os.Stat(statePath); os.IsNotExist(err) {
		return nil, nil // No state file - not an error
	}

	// Read file
	data, err := os.ReadFile(statePath)
	if err != nil {
		return nil, fmt.Errorf("failed to read tunnel state file: %w", err)
	}

	// Parse JSON
	var state TunnelStateFile
	if err := json.Unmarshal(data, &state); err != nil {
		return nil, fmt.Errorf("failed to parse tunnel state file: %w", err)
	}

	// Validate version
	if state.Version != stateFileVersion {
		return nil, fmt.Errorf("unsupported state file version: %s (expected %s)", state.Version, stateFileVersion)
	}

	return &state, nil
}

// RemoveTunnelStateFile removes the state file
// Used during clean shutdown or after successful adoption
func RemoveTunnelStateFile() error {
	statePath := GetTunnelStatePath()

	if err := os.Remove(statePath); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("failed to remove tunnel state file: %w", err)
	}

	return nil
}
