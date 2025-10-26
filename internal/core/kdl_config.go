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
	SSH               SSHConfig               // SSH connection settings
	Reconnect         ReconnectConfig         // Reconnect settings
	Contexts          map[string]*ContextRule // Context rules keyed by context name
	// Context behavior settings
	CheckOnStartup       bool
	CheckOnNetworkChange bool
}

// SSHConfig represents SSH connection settings
type SSHConfig struct {
	ServerAliveInterval int // Send keepalive every N seconds (0 to disable)
	ServerAliveCountMax int // Exit after N failed keepalives
}

// ReconnectConfig represents reconnect settings for SSH tunnels
type ReconnectConfig struct {
	Enabled        bool
	InitialBackoff string
	MaxBackoff     string
	BackoffFactor  int
	MaxRetries     int
}

// ContextRule represents a security context rule
type ContextRule struct {
	Name        string            // Context name (e.g., "home", "office")
	DisplayName string            // Human-friendly display name
	Conditions  map[string][]string // Sensor conditions (e.g., "public_ip": ["1.2.3.4", "5.6.7.0/24"])
	Actions     ContextActions    // Actions to take when entering this context
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
	Reconnect         *kdlReconnect           `kdl:"reconnect"`
	Contexts          map[string]*kdlContext  `kdl:"context,multiple"`
}

type kdlSSH struct {
	ServerAliveInterval int `kdl:"server_alive_interval"`
	ServerAliveCountMax int `kdl:"server_alive_count_max"`
}

type kdlReconnect struct {
	Enabled        bool   `kdl:"enabled"`
	InitialBackoff string `kdl:"initial_backoff"`
	MaxBackoff     string `kdl:"max_backoff"`
	BackoffFactor  int    `kdl:"backoff_factor"`
	MaxRetries     int    `kdl:"max_retries"`
}

type kdlContext struct {
	DisplayName string         `kdl:"display_name"`
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
		Contexts:             make(map[string]*ContextRule),
	}

	// Convert SSH settings
	if kdlCfg.SSH != nil {
		cfg.SSH = SSHConfig{
			ServerAliveInterval: kdlCfg.SSH.ServerAliveInterval,
			ServerAliveCountMax: kdlCfg.SSH.ServerAliveCountMax,
		}
	} else {
		// Defaults
		cfg.SSH = SSHConfig{
			ServerAliveInterval: 15,
			ServerAliveCountMax: 3,
		}
	}

	// Convert reconnect settings
	if kdlCfg.Reconnect != nil {
		cfg.Reconnect = ReconnectConfig{
			Enabled:        kdlCfg.Reconnect.Enabled,
			InitialBackoff: kdlCfg.Reconnect.InitialBackoff,
			MaxBackoff:     kdlCfg.Reconnect.MaxBackoff,
			BackoffFactor:  kdlCfg.Reconnect.BackoffFactor,
			MaxRetries:     kdlCfg.Reconnect.MaxRetries,
		}
	} else {
		// Defaults
		cfg.Reconnect = ReconnectConfig{
			Enabled:        true,
			InitialBackoff: "1s",
			MaxBackoff:     "5m",
			BackoffFactor:  2,
			MaxRetries:     10,
		}
	}

	// Convert context rules
	for name, kdlCtx := range kdlCfg.Contexts {
		rule := &ContextRule{
			Name:        name,
			DisplayName: kdlCtx.DisplayName,
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
		},
		Reconnect: ReconnectConfig{
			Enabled:        true,
			InitialBackoff: "1s",
			MaxBackoff:     "5m",
			BackoffFactor:  2,
			MaxRetries:     10,
		},
		Contexts: make(map[string]*ContextRule),
	}
}
