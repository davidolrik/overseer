package core

import (
	"fmt"
	"os"
	"strings"
	"time"

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
	ConfigPath  string                   // Directory containing config files
	Verbose     int                      // Verbosity level
	Exports     []ExportConfig           // Export configurations
	PreferredIP string                   // Preferred IP version for OVERSEER_PUBLIC_IP: "ipv4" (default) or "ipv6"
	SSH         SSHConfig                // SSH connection settings (including reconnect)
	Companion   CompanionSettings        // Global companion script settings
	Locations   map[string]*Location     // Location definitions keyed by location name
	Contexts    []*ContextRule           // Context rules in evaluation order (first match wins)
	Tunnels     map[string]*TunnelConfig // Per-tunnel configurations keyed by tunnel name
	// Global hooks for all location/context transitions
	GlobalLocationHooks *HooksConfig // Global hooks for all locations
	GlobalContextHooks  *HooksConfig // Global hooks for all contexts
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

// CompanionSettings represents global companion script settings
type CompanionSettings struct {
	HistorySize int // Ring buffer size for output history (default 1000)
}

// Location represents a physical or network location with sensor conditions
type Location struct {
	Name        string              // Location name (e.g., "hq", "home")
	DisplayName string              // Human-friendly display name
	Conditions  map[string][]string // Simple sensor conditions (e.g., "public_ip": ["1.2.3.4", "5.6.7.0/24"])
	Condition   interface{}         // Structured condition (supports nesting with any/all) - will be awareness.Condition
	Environment map[string]string   // Custom environment variables to export
	Hooks       *HooksConfig        // Enter/leave hooks
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
	Hooks       *HooksConfig        // Enter/leave hooks
}

// ContextActions represents actions for a context
type ContextActions struct {
	Connect    []string // Tunnels to connect
	Disconnect []string // Tunnels to disconnect
}

// TunnelConfig represents per-tunnel configuration
type TunnelConfig struct {
	Name       string            // Tunnel name (matches SSH alias)
	Tags       []string          // SSH tags for config matching (passed as -P arguments)
	Companions []CompanionConfig // Companion scripts to run before tunnel starts
}

// CompanionConfig represents a companion script configuration
type CompanionConfig struct {
	Name        string            // Unique identifier within tunnel
	Command     string            // Command to execute
	Workdir     string            // Working directory
	Environment map[string]string // Environment variables
	WaitMode    string            // "completion" or "string"
	WaitFor     string            // String to wait for (if WaitMode = "string")
	Timeout     time.Duration     // Wait timeout
	ReadyDelay  time.Duration     // Delay after ready before proceeding with tunnel startup
	OnFailure   string            // "block" or "continue"
	KeepAlive   bool              // Keep running after tunnel connects
	AutoRestart bool              // Automatically restart if exits unexpectedly
	Persistent  bool              // Keep running when tunnel stops (don't stop with tunnel)
	StopSignal  string            // Signal to send on stop: "INT" (default), "TERM", "HUP"
}

// HookConfig represents a single hook command
type HookConfig struct {
	Command string        // Command to execute (via shell)
	Timeout time.Duration // Execution timeout
}

// HooksConfig represents hooks for a location or context
type HooksConfig struct {
	OnEnter []HookConfig // Commands to run when entering
	OnLeave []HookConfig // Commands to run when leaving
}

// HCL parsing structs

type hclConfig struct {
	Verbose       int                   `hcl:"verbose,optional"`
	Exports       *hclExports           `hcl:"exports,block"`
	SSH           *hclSSH               `hcl:"ssh,block"`
	Companion     *hclCompanionSettings `hcl:"companion,block"`
	LocationHooks *hclHooks             `hcl:"location_hooks,block"`
	ContextHooks  *hclHooks             `hcl:"context_hooks,block"`
	Locations     []hclLocation         `hcl:"location,block"`
	Contexts      []hclContext          `hcl:"context,block"`
	Tunnels       []hclTunnel           `hcl:"tunnel,block"`
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

type hclCompanionSettings struct {
	HistorySize int `hcl:"history_size,optional"`
}

type hclHooks struct {
	OnEnter []string `hcl:"on_enter,optional"`
	OnLeave []string `hcl:"on_leave,optional"`
	Timeout string   `hcl:"timeout,optional"`
}

type hclLocation struct {
	Name        string            `hcl:"name,label"`
	DisplayName string            `hcl:"display_name,optional"`
	Conditions  *hclConditions    `hcl:"conditions,block"`
	Environment map[string]string `hcl:"environment,optional"`
	Hooks       *hclHooks         `hcl:"hooks,block"`
}

type hclContext struct {
	Name        string            `hcl:"name,label"`
	DisplayName string            `hcl:"display_name,optional"`
	Locations   []string          `hcl:"locations,optional"`
	Conditions  *hclConditions    `hcl:"conditions,block"`
	Actions     *hclActions       `hcl:"actions,block"`
	Environment map[string]string `hcl:"environment,optional"`
	Hooks       *hclHooks         `hcl:"hooks,block"`
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

type hclTunnel struct {
	Name       string         `hcl:"name,label"`
	Tags       []string       `hcl:"tags,optional"`
	Companions []hclCompanion `hcl:"companion,block"`
}

type hclCompanion struct {
	Name        string            `hcl:"name,label"`
	Command     string            `hcl:"command"`
	Workdir     string            `hcl:"workdir,optional"`
	Environment map[string]string `hcl:"environment,optional"`
	WaitMode    string            `hcl:"wait_mode,optional"`
	WaitFor     string            `hcl:"wait_for,optional"`
	Timeout     string            `hcl:"timeout,optional"`
	ReadyDelay  string            `hcl:"ready_delay,optional"`
	OnFailure   string            `hcl:"on_failure,optional"`
	KeepAlive   *bool             `hcl:"keep_alive,optional"`
	AutoRestart *bool             `hcl:"auto_restart,optional"`
	Persistent  *bool             `hcl:"persistent,optional"`
	StopSignal  string            `hcl:"stop_signal,optional"`
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
		Tunnels:              make(map[string]*TunnelConfig),
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

	// Convert companion settings
	cfg.Companion = CompanionSettings{HistorySize: 1000} // Default
	if hclCfg.Companion != nil && hclCfg.Companion.HistorySize > 0 {
		cfg.Companion.HistorySize = hclCfg.Companion.HistorySize
	}

	// Convert global location hooks
	if hclCfg.LocationHooks != nil {
		hooks, err := parseHCLHooks(hclCfg.LocationHooks)
		if err != nil {
			return nil, fmt.Errorf("location_hooks: %w", err)
		}
		cfg.GlobalLocationHooks = hooks
	}

	// Convert global context hooks
	if hclCfg.ContextHooks != nil {
		hooks, err := parseHCLHooks(hclCfg.ContextHooks)
		if err != nil {
			return nil, fmt.Errorf("context_hooks: %w", err)
		}
		cfg.GlobalContextHooks = hooks
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

		// Parse hooks
		if hclLoc.Hooks != nil {
			hooks, err := parseHCLHooks(hclLoc.Hooks)
			if err != nil {
				return nil, fmt.Errorf("location %q: %w", hclLoc.Name, err)
			}
			loc.Hooks = hooks
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

		// Parse hooks
		if hclCtx.Hooks != nil {
			hooks, err := parseHCLHooks(hclCtx.Hooks)
			if err != nil {
				return nil, fmt.Errorf("context %q: %w", hclCtx.Name, err)
			}
			rule.Hooks = hooks
		}

		cfg.Contexts = append(cfg.Contexts, rule)
	}

	// Convert tunnel configurations
	for _, hclTun := range hclCfg.Tunnels {
		tunnel := &TunnelConfig{
			Name:       hclTun.Name,
			Tags:       hclTun.Tags,
			Companions: make([]CompanionConfig, 0, len(hclTun.Companions)),
		}

		// Track companion names for uniqueness validation
		companionNames := make(map[string]bool)

		for _, hclComp := range hclTun.Companions {
			// Validate companion name uniqueness
			if companionNames[hclComp.Name] {
				return nil, fmt.Errorf("tunnel %q: duplicate companion name %q", hclTun.Name, hclComp.Name)
			}
			companionNames[hclComp.Name] = true

			// Validate command is required
			if len(hclComp.Command) == 0 {
				return nil, fmt.Errorf("tunnel %q companion %q: command is required", hclTun.Name, hclComp.Name)
			}

			// Parse wait mode and validate
			waitMode := hclComp.WaitMode
			if waitMode == "" {
				waitMode = "completion" // Default
			}
			if waitMode != "completion" && waitMode != "string" {
				return nil, fmt.Errorf("tunnel %q companion %q: wait_mode must be 'completion' or 'string', got %q", hclTun.Name, hclComp.Name, waitMode)
			}

			// Validate wait_for is required when wait_mode = "string"
			if waitMode == "string" && hclComp.WaitFor == "" {
				return nil, fmt.Errorf("tunnel %q companion %q: wait_for is required when wait_mode is 'string'", hclTun.Name, hclComp.Name)
			}

			// Parse timeout
			timeout := 30 * time.Second // Default
			if hclComp.Timeout != "" {
				var err error
				timeout, err = time.ParseDuration(hclComp.Timeout)
				if err != nil {
					return nil, fmt.Errorf("tunnel %q companion %q: invalid timeout %q: %w", hclTun.Name, hclComp.Name, hclComp.Timeout, err)
				}
			}

			// Parse ready_delay (delay after companion is ready before proceeding)
			var readyDelay time.Duration // Default: 0 (no delay)
			if hclComp.ReadyDelay != "" {
				var err error
				readyDelay, err = time.ParseDuration(hclComp.ReadyDelay)
				if err != nil {
					return nil, fmt.Errorf("tunnel %q companion %q: invalid ready_delay %q: %w", hclTun.Name, hclComp.Name, hclComp.ReadyDelay, err)
				}
			}

			// Parse on_failure
			onFailure := hclComp.OnFailure
			if onFailure == "" {
				onFailure = "block" // Default
			}
			if onFailure != "block" && onFailure != "continue" {
				return nil, fmt.Errorf("tunnel %q companion %q: on_failure must be 'block' or 'continue', got %q", hclTun.Name, hclComp.Name, onFailure)
			}

			// Parse keep_alive
			keepAlive := true // Default
			if hclComp.KeepAlive != nil {
				keepAlive = *hclComp.KeepAlive
			}

			// Parse auto_restart
			autoRestart := false // Default
			if hclComp.AutoRestart != nil {
				autoRestart = *hclComp.AutoRestart
			}

			// Parse persistent
			persistent := false // Default
			if hclComp.Persistent != nil {
				persistent = *hclComp.Persistent
			}

			// Parse stop_signal (default: INT - better for foreground processes)
			stopSignal := "INT"
			if hclComp.StopSignal != "" {
				stopSignal = strings.ToUpper(hclComp.StopSignal)
			}

			companion := CompanionConfig{
				Name:        hclComp.Name,
				Command:     hclComp.Command,
				Workdir:     hclComp.Workdir,
				Environment: hclComp.Environment,
				WaitMode:    waitMode,
				WaitFor:     hclComp.WaitFor,
				Timeout:     timeout,
				ReadyDelay:  readyDelay,
				OnFailure:   onFailure,
				KeepAlive:   keepAlive,
				AutoRestart: autoRestart,
				Persistent:  persistent,
				StopSignal:  stopSignal,
			}

			if companion.Environment == nil {
				companion.Environment = make(map[string]string)
			}

			tunnel.Companions = append(tunnel.Companions, companion)
		}

		cfg.Tunnels[hclTun.Name] = tunnel
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

// parseHCLHooks converts HCL hooks block to HooksConfig
func parseHCLHooks(hooks *hclHooks) (*HooksConfig, error) {
	if hooks == nil {
		return nil, nil
	}

	// Parse timeout (default 30s)
	timeout := 30 * time.Second
	if hooks.Timeout != "" {
		var err error
		timeout, err = time.ParseDuration(hooks.Timeout)
		if err != nil {
			return nil, fmt.Errorf("invalid timeout %q: %w", hooks.Timeout, err)
		}
	}

	result := &HooksConfig{}

	// Convert on_enter hooks
	for _, cmd := range hooks.OnEnter {
		result.OnEnter = append(result.OnEnter, HookConfig{
			Command: cmd,
			Timeout: timeout,
		})
	}

	// Convert on_leave hooks
	for _, cmd := range hooks.OnLeave {
		result.OnLeave = append(result.OnLeave, HookConfig{
			Command: cmd,
			Timeout: timeout,
		})
	}

	return result, nil
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
		Companion: CompanionSettings{HistorySize: 1000},
		Locations: make(map[string]*Location),
		Contexts:  make([]*ContextRule, 0),
		Tunnels:   make(map[string]*TunnelConfig),
	}
}

// ConfigExists checks if a config file exists
func ConfigExists(configPath string) bool {
	_, err := os.Stat(configPath)
	return err == nil
}
