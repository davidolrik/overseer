package core

import (
	"fmt"
	"os"

	"github.com/hashicorp/hcl/v2/hclsimple"
	"overseer.olrik.dev/internal/awareness"
)

// Config is the global configuration instance
var Config *Configuration

// ExportConfig represents a single export configuration
type ExportConfig struct {
	Type string // Export type: "dotenv", "context", "location", "public_ip"
	Path string // File path to write to
}

// Configuration represents the complete Overseer configuration
type Configuration struct {
	ConfigPath  string                  // Directory containing config files
	Verbose     int                     // Verbosity level
	Exports     []ExportConfig          // Export configurations
	PreferredIP string                  // Preferred IP version for OVERSEER_PUBLIC_IP: "ipv4" (default) or "ipv6"
	SSH         SSHConfig               // SSH connection settings (including reconnect)
	Locations   map[string]*Location    // Location definitions keyed by location name
	Contexts    []*ContextRule          // Context rules in evaluation order (first match wins)
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
	Conditions  map[string][]string // Simple sensor conditions (e.g., "public_ip": ["1.2.3.4", "5.6.7.0/24"])
	Condition   interface{}         // Structured condition (supports nesting with any/all) - will be awareness.Condition
	Environment map[string]string   // Custom environment variables to export
}

// ContextRule represents a context rule
type ContextRule struct {
	Name        string              // Context name (e.g., "home", "office")
	DisplayName string              // Human-friendly display name
	Locations   []string            // Location names this context applies to
	Conditions  map[string][]string // Simple sensor conditions (e.g., "public_ip": ["1.2.3.4", "5.6.7.0/24"])
	Condition   interface{}         // Structured condition (supports nesting with any/all) - will be awareness.Condition
	Actions     ContextActions      // Actions to take when entering this context
	Environment map[string]string   // Custom environment variables to export
}

// ContextActions represents actions for a context
type ContextActions struct {
	Connect    []string // Tunnels to connect
	Disconnect []string // Tunnels to disconnect
}

// HCL parsing structs

type hclConfig struct {
	Verbose   int            `hcl:"verbose,optional"`
	Exports   *hclExports    `hcl:"exports,block"`
	SSH       *hclSSH        `hcl:"ssh,block"`
	Locations []hclLocation  `hcl:"location,block"`
	Contexts  []hclContext   `hcl:"context,block"`
}

type hclExports struct {
	Dotenv      string `hcl:"dotenv,optional"`
	Context     string `hcl:"context,optional"`
	Location    string `hcl:"location,optional"`
	PublicIP    string `hcl:"public_ip,optional"`
	PreferredIP string `hcl:"preferred_ip,optional"`
}

type hclSSH struct {
	ServerAliveInterval int    `hcl:"server_alive_interval,optional"`
	ServerAliveCountMax int    `hcl:"server_alive_count_max,optional"`
	ReconnectEnabled    *bool  `hcl:"reconnect_enabled,optional"`
	InitialBackoff      string `hcl:"initial_backoff,optional"`
	MaxBackoff          string `hcl:"max_backoff,optional"`
	BackoffFactor       int    `hcl:"backoff_factor,optional"`
	MaxRetries          int    `hcl:"max_retries,optional"`
}

type hclLocation struct {
	Name        string            `hcl:"name,label"`
	DisplayName string            `hcl:"display_name,optional"`
	Conditions  *hclConditions    `hcl:"conditions,block"`
	Environment map[string]string `hcl:"environment,optional"`
}

type hclContext struct {
	Name        string            `hcl:"name,label"`
	DisplayName string            `hcl:"display_name,optional"`
	Locations   []string          `hcl:"locations,optional"`
	Conditions  *hclConditions    `hcl:"conditions,block"`
	Actions     *hclActions       `hcl:"actions,block"`
	Environment map[string]string `hcl:"environment,optional"`
}

type hclConditions struct {
	PublicIP []string          `hcl:"public_ip,optional"`
	Online   *bool             `hcl:"online,optional"`
	Env      map[string]string `hcl:"env,optional"`
	Any      []hclConditions   `hcl:"any,block"`
	All      []hclConditions   `hcl:"all,block"`
}

type hclActions struct {
	Connect    []string `hcl:"connect,optional"`
	Disconnect []string `hcl:"disconnect,optional"`
}

// LoadConfig loads the HCL configuration file and returns a Configuration struct
func LoadConfig(filename string) (*Configuration, error) {
	var hclCfg hclConfig

	err := hclsimple.DecodeFile(filename, nil, &hclCfg)
	if err != nil {
		return nil, fmt.Errorf("failed to parse HCL config: %w", err)
	}

	// Convert to our clean Configuration struct
	cfg := &Configuration{
		Verbose:              hclCfg.Verbose,
		PreferredIP:          "ipv4", // Default to IPv4
		CheckOnStartup:       true,   // Default
		CheckOnNetworkChange: true,   // Default
		Locations:            make(map[string]*Location),
		Contexts:             make([]*ContextRule, 0),
		Exports:              make([]ExportConfig, 0),
	}

	// Convert exports
	if hclCfg.Exports != nil {
		if hclCfg.Exports.Dotenv != "" {
			cfg.Exports = append(cfg.Exports, ExportConfig{Type: "dotenv", Path: hclCfg.Exports.Dotenv})
		}
		if hclCfg.Exports.Context != "" {
			cfg.Exports = append(cfg.Exports, ExportConfig{Type: "context", Path: hclCfg.Exports.Context})
		}
		if hclCfg.Exports.Location != "" {
			cfg.Exports = append(cfg.Exports, ExportConfig{Type: "location", Path: hclCfg.Exports.Location})
		}
		if hclCfg.Exports.PublicIP != "" {
			cfg.Exports = append(cfg.Exports, ExportConfig{Type: "public_ip", Path: hclCfg.Exports.PublicIP})
		}
		if hclCfg.Exports.PreferredIP == "ipv6" {
			cfg.PreferredIP = "ipv6"
		}
	}

	// Convert SSH settings
	if hclCfg.SSH != nil {
		cfg.SSH = SSHConfig{
			ServerAliveInterval: hclCfg.SSH.ServerAliveInterval,
			ServerAliveCountMax: hclCfg.SSH.ServerAliveCountMax,
			InitialBackoff:      hclCfg.SSH.InitialBackoff,
			MaxBackoff:          hclCfg.SSH.MaxBackoff,
			BackoffFactor:       hclCfg.SSH.BackoffFactor,
			MaxRetries:          hclCfg.SSH.MaxRetries,
		}
		if hclCfg.SSH.ReconnectEnabled != nil {
			cfg.SSH.ReconnectEnabled = *hclCfg.SSH.ReconnectEnabled
		} else {
			cfg.SSH.ReconnectEnabled = true // Default
		}
		// Apply defaults for zero values
		if cfg.SSH.ServerAliveInterval == 0 {
			cfg.SSH.ServerAliveInterval = 15
		}
		if cfg.SSH.ServerAliveCountMax == 0 {
			cfg.SSH.ServerAliveCountMax = 3
		}
		if cfg.SSH.InitialBackoff == "" {
			cfg.SSH.InitialBackoff = "1s"
		}
		if cfg.SSH.MaxBackoff == "" {
			cfg.SSH.MaxBackoff = "5m"
		}
		if cfg.SSH.BackoffFactor == 0 {
			cfg.SSH.BackoffFactor = 2
		}
		if cfg.SSH.MaxRetries == 0 {
			cfg.SSH.MaxRetries = 10
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
	for _, hclLoc := range hclCfg.Locations {
		loc := &Location{
			Name:        hclLoc.Name,
			DisplayName: hclLoc.DisplayName,
			Conditions:  make(map[string][]string),
			Environment: hclLoc.Environment,
		}
		if loc.Environment == nil {
			loc.Environment = make(map[string]string)
		}

		// Parse conditions
		if hclLoc.Conditions != nil {
			cond := parseHCLConditions(hclLoc.Conditions)
			if cond != nil {
				loc.Condition = cond
			}
		}

		cfg.Locations[hclLoc.Name] = loc
	}

	// Convert context rules (preserving order from HCL file)
	for _, hclCtx := range hclCfg.Contexts {
		rule := &ContextRule{
			Name:        hclCtx.Name,
			DisplayName: hclCtx.DisplayName,
			Locations:   hclCtx.Locations,
			Conditions:  make(map[string][]string),
			Environment: hclCtx.Environment,
		}
		if rule.Environment == nil {
			rule.Environment = make(map[string]string)
		}

		// Parse conditions
		if hclCtx.Conditions != nil {
			cond := parseHCLConditions(hclCtx.Conditions)
			if cond != nil {
				rule.Condition = cond
			}
		}

		// Convert actions
		if hclCtx.Actions != nil {
			rule.Actions = ContextActions{
				Connect:    hclCtx.Actions.Connect,
				Disconnect: hclCtx.Actions.Disconnect,
			}
		}

		cfg.Contexts = append(cfg.Contexts, rule)
	}

	return cfg, nil
}

// parseHCLConditions converts HCL conditions to an awareness.Condition
func parseHCLConditions(cond *hclConditions) awareness.Condition {
	var conditions []awareness.Condition

	// Handle public_ip conditions
	if len(cond.PublicIP) > 0 {
		if len(cond.PublicIP) == 1 {
			conditions = append(conditions, awareness.NewSensorCondition("public_ipv4", cond.PublicIP[0]))
		} else {
			// Multiple IPs = OR
			ipConds := make([]awareness.Condition, len(cond.PublicIP))
			for i, ip := range cond.PublicIP {
				ipConds[i] = awareness.NewSensorCondition("public_ipv4", ip)
			}
			conditions = append(conditions, awareness.NewAnyCondition(ipConds...))
		}
	}

	// Handle online condition
	if cond.Online != nil {
		conditions = append(conditions, awareness.NewBooleanCondition("online", *cond.Online))
	}

	// Handle env conditions
	for varName, pattern := range cond.Env {
		sensorName := "env:" + varName
		conditions = append(conditions, awareness.NewSensorCondition(sensorName, pattern))
	}

	// Handle nested any blocks
	for _, anyBlock := range cond.Any {
		anyCond := parseHCLConditions(&anyBlock)
		if anyCond != nil {
			conditions = append(conditions, anyCond)
		}
	}

	// Handle nested all blocks
	for _, allBlock := range cond.All {
		allCond := parseHCLConditions(&allBlock)
		if allCond != nil {
			// Wrap in an all condition
			conditions = append(conditions, awareness.NewAllCondition(allCond))
		}
	}

	// Return based on number of conditions
	if len(conditions) == 0 {
		return nil
	}
	if len(conditions) == 1 {
		return conditions[0]
	}
	// Multiple conditions at same level = OR (any)
	return awareness.NewAnyCondition(conditions...)
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
		Contexts:  make([]*ContextRule, 0),
	}
}

// ConfigExists checks if a config file exists
func ConfigExists(configPath string) bool {
	_, err := os.Stat(configPath)
	return err == nil
}
