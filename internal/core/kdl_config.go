package core

import (
	"fmt"
	"os"

	"github.com/sblinch/kdl-go"
)

// Config is the global configuration instance
var Config *Configuration

// Configuration represents the complete Overseer configuration
type Configuration struct {
	ConfigPath        string                  // Directory containing config files
	Verbose           int                     // Verbosity level
	ContextOutputFile string                  // Optional file to write current context name
	SSH               SSHConfig               // SSH connection settings (including reconnect)
	Locations         map[string]*Location    // Location definitions keyed by location name
	Contexts          map[string]*ContextRule // Context rules keyed by context name
	// Context behavior settings
	CheckOnStartup       bool
	CheckOnNetworkChange bool
}

// SSHConfig represents SSH connection settings
type SSHConfig struct {
	ServerAliveInterval int    // Send keepalive every N seconds (0 to disable)
	ServerAliveCountMax int    // Exit after N failed keepalives
	ReconnectEnabled    bool   // Enable/disable auto-reconnect
	InitialBackoff      string // First retry delay
	MaxBackoff          string // Maximum delay between retries
	BackoffFactor       int    // Multiplier for each retry
	MaxRetries          int    // Give up after this many attempts
}

// Location represents a physical or network location with sensor conditions
type Location struct {
	Name        string              // Location name (e.g., "hq", "home")
	DisplayName string              // Human-friendly display name
	Conditions  map[string][]string // Sensor conditions (e.g., "public_ip": ["1.2.3.4", "5.6.7.0/24"])
}

// ContextRule represents a security context rule
type ContextRule struct {
	Name        string         // Context name (e.g., "home", "office")
	DisplayName string         // Human-friendly display name
	Locations   []string       // Location names this context applies to
	Conditions  map[string][]string // Sensor conditions (e.g., "public_ip": ["1.2.3.4", "5.6.7.0/24"])
	Actions     ContextActions // Actions to take when entering this context
}

// ContextActions represents actions for a context
type ContextActions struct {
	Connect    []string // Tunnels to connect
	Disconnect []string // Tunnels to disconnect
}

// KDL unmarshaling structs (internal use only)
type kdlConfig struct {
	Verbose           int                     `kdl:"verbose"`
	ContextOutputFile string                  `kdl:"context_output_file"`
	SSH               *kdlSSH                 `kdl:"ssh"`
	Locations         map[string]*kdlLocation `kdl:"location,multiple"`
	Contexts          map[string]*kdlContext  `kdl:"context,multiple"`
}

type kdlSSH struct {
	ServerAliveInterval int    `kdl:"server_alive_interval"`
	ServerAliveCountMax int    `kdl:"server_alive_count_max"`
	ReconnectEnabled    bool   `kdl:"reconnect_enabled"`
	InitialBackoff      string `kdl:"initial_backoff"`
	MaxBackoff          string `kdl:"max_backoff"`
	BackoffFactor       int    `kdl:"backoff_factor"`
	MaxRetries          int    `kdl:"max_retries"`
}

type kdlLocation struct {
	DisplayName string         `kdl:"display_name"`
	Conditions  *kdlConditions `kdl:"conditions"`
}

type kdlContext struct {
	DisplayName string         `kdl:"display_name"`
	Locations   []string       `kdl:"location"`
	Conditions  *kdlConditions `kdl:"conditions"`
	Actions     *kdlActions    `kdl:"actions"`
}

type kdlConditions struct {
	PublicIP []string `kdl:"public_ip"`
}

type kdlActions struct {
	Connect    []string `kdl:"connect"`
	Disconnect []string `kdl:"disconnect"`
}

// LoadConfig loads the KDL configuration file and returns a Configuration struct
func LoadConfig(filename string) (*Configuration, error) {
	// Read the KDL file
	data, err := os.ReadFile(filename)
	if err != nil {
		return nil, fmt.Errorf("failed to read KDL file: %w", err)
	}

	// Unmarshal KDL into internal struct
	var kdlCfg kdlConfig
	if err := kdl.Unmarshal(data, &kdlCfg); err != nil {
		return nil, fmt.Errorf("failed to unmarshal KDL: %w", err)
	}

	// Convert to our clean Configuration struct
	cfg := &Configuration{
		Verbose:              kdlCfg.Verbose,
		ContextOutputFile:    kdlCfg.ContextOutputFile,
		CheckOnStartup:       true,  // Default
		CheckOnNetworkChange: true,  // Default
		Locations:            make(map[string]*Location),
		Contexts:             make(map[string]*ContextRule),
	}

	// Convert SSH settings (including reconnect settings)
	if kdlCfg.SSH != nil {
		cfg.SSH = SSHConfig{
			ServerAliveInterval: kdlCfg.SSH.ServerAliveInterval,
			ServerAliveCountMax: kdlCfg.SSH.ServerAliveCountMax,
			ReconnectEnabled:    kdlCfg.SSH.ReconnectEnabled,
			InitialBackoff:      kdlCfg.SSH.InitialBackoff,
			MaxBackoff:          kdlCfg.SSH.MaxBackoff,
			BackoffFactor:       kdlCfg.SSH.BackoffFactor,
			MaxRetries:          kdlCfg.SSH.MaxRetries,
		}
	} else {
		// Defaults
		cfg.SSH = SSHConfig{
			ServerAliveInterval: 15,
			ServerAliveCountMax: 3,
			ReconnectEnabled:    true,
			InitialBackoff:      "1s",
			MaxBackoff:          "5m",
			BackoffFactor:       2,
			MaxRetries:          10,
		}
	}

	// Convert location definitions
	for name, kdlLoc := range kdlCfg.Locations {
		loc := &Location{
			Name:        name,
			DisplayName: kdlLoc.DisplayName,
			Conditions:  make(map[string][]string),
		}

		// Convert conditions
		if kdlLoc.Conditions != nil {
			if len(kdlLoc.Conditions.PublicIP) > 0 {
				loc.Conditions["public_ip"] = kdlLoc.Conditions.PublicIP
			}
		}

		cfg.Locations[name] = loc
	}

	// Convert context rules
	for name, kdlCtx := range kdlCfg.Contexts {
		rule := &ContextRule{
			Name:        name,
			DisplayName: kdlCtx.DisplayName,
			Locations:   kdlCtx.Locations,
			Conditions:  make(map[string][]string),
		}

		// Convert conditions
		if kdlCtx.Conditions != nil {
			if len(kdlCtx.Conditions.PublicIP) > 0 {
				rule.Conditions["public_ip"] = kdlCtx.Conditions.PublicIP
			}
		}

		// Convert actions
		if kdlCtx.Actions != nil {
			rule.Actions = ContextActions{
				Connect:    kdlCtx.Actions.Connect,
				Disconnect: kdlCtx.Actions.Disconnect,
			}
		}

		cfg.Contexts[name] = rule
	}

	return cfg, nil
}

// GetDefaultConfig returns a Configuration with default values
func GetDefaultConfig() *Configuration {
	return &Configuration{
		Verbose:              0,
		CheckOnStartup:       true,
		CheckOnNetworkChange: true,
		SSH: SSHConfig{
			ServerAliveInterval: 15,
			ServerAliveCountMax: 3,
			ReconnectEnabled:    true,
			InitialBackoff:      "1s",
			MaxBackoff:          "5m",
			BackoffFactor:       2,
			MaxRetries:          10,
		},
		Locations: make(map[string]*Location),
		Contexts:  make(map[string]*ContextRule),
	}
}
