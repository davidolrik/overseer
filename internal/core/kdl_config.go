package core

import (
	"bytes"
	"fmt"
	"os"

	"github.com/sblinch/kdl-go"
	"github.com/sblinch/kdl-go/document"
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
	ConfigPath        string                  // Directory containing config files
	Verbose           int                     // Verbosity level
	Exports           []ExportConfig          // Export configurations
	SSH               SSHConfig               // SSH connection settings (including reconnect)
	Locations         map[string]*Location    // Location definitions keyed by location name
	Contexts          []*ContextRule          // Context rules in evaluation order (first match wins)
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
	Condition   interface{}         // New structured condition (supports nesting with any/all) - will be security.Condition
	Environment map[string]string   // Custom environment variables to export
}

// ContextRule represents a security context rule
type ContextRule struct {
	Name        string              // Context name (e.g., "home", "office")
	DisplayName string              // Human-friendly display name
	Locations   []string            // Location names this context applies to
	Conditions  map[string][]string // Simple sensor conditions (e.g., "public_ip": ["1.2.3.4", "5.6.7.0/24"])
	Condition   interface{}         // New structured condition (supports nesting with any/all) - will be security.Condition
	Actions     ContextActions      // Actions to take when entering this context
	Environment map[string]string   // Custom environment variables to export
}

// ContextActions represents actions for a context
type ContextActions struct {
	Connect    []string // Tunnels to connect
	Disconnect []string // Tunnels to disconnect
}

// KDL unmarshaling structs (internal use only)
type kdlConfig struct {
	Verbose   int                     `kdl:"verbose"`
	Exports   *kdlExports             `kdl:"exports"`
	SSH       *kdlSSH                 `kdl:"ssh"`
	Locations map[string]*kdlLocation `kdl:"location,multiple"`
	Contexts  map[string]*kdlContext  `kdl:"context,multiple"`
}

type kdlExports struct {
	Dotenv   string `kdl:"dotenv"`
	Context  string `kdl:"context"`
	Location string `kdl:"location"`
	PublicIP string `kdl:"public_ip"`
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
	Environment interface{}    `kdl:"environment"` // Parsed manually, field here to satisfy unmarshaler
}

type kdlContext struct {
	DisplayName string         `kdl:"display_name"`
	Locations   []string       `kdl:"location"`
	Conditions  *kdlConditions `kdl:"conditions"`
	Actions     *kdlActions    `kdl:"actions"`
	Environment interface{}    `kdl:"environment"` // Parsed manually, field here to satisfy unmarshaler
}

type kdlConditions struct {
	PublicIP []string          `kdl:"public_ip"`
	Online   *bool             `kdl:"online"` // Pointer to distinguish between unset and false
	Env      map[string]string `kdl:"env,multiple"`
}

type kdlActions struct {
	Connect    []string `kdl:"connect"`
	Disconnect []string `kdl:"disconnect"`
}

// extractEnvironmentFromNode extracts environment variables from a KDL node's children
func extractEnvironmentFromNode(node *document.Node) map[string]string {
	env := make(map[string]string)
	if node.Children == nil {
		return env
	}

	// Look for an "environment" child node
	for _, child := range node.Children {
		if child.Name != nil && child.Name.Value == "environment" {
			// Extract all properties from the environment node
			if child.Children != nil {
				for _, envVar := range child.Children {
					if envVar.Name != nil && len(envVar.Arguments) > 0 {
						key := envVar.Name.Value.(string)
						if val, ok := envVar.Arguments[0].Value.(string); ok {
							env[key] = val
						}
					}
				}
			}
			break
		}
	}

	return env
}

// extractEnvConditionsFromNode extracts env conditions from a conditions node
// Returns a map where keys are "env:VAR_NAME" and values are slices of patterns
func extractEnvConditionsFromNode(node *document.Node) map[string][]string {
	envConditions := make(map[string][]string)
	if node.Children == nil {
		return envConditions
	}

	// Look for a "conditions" child node
	for _, child := range node.Children {
		if child.Name != nil && child.Name.Value == "conditions" {
			// Extract all env entries from the conditions node
			if child.Children != nil {
				for _, condNode := range child.Children {
					if condNode.Name != nil && condNode.Name.Value == "env" {
						// env nodes have format: env "VAR_NAME" "pattern"
						if len(condNode.Arguments) >= 2 {
							varName, ok1 := condNode.Arguments[0].Value.(string)
							pattern, ok2 := condNode.Arguments[1].Value.(string)
							if ok1 && ok2 {
								key := "env:" + varName
								envConditions[key] = append(envConditions[key], pattern)
							}
						}
					}
				}
			}
			break
		}
	}

	return envConditions
}

// LoadConfig loads the KDL configuration file and returns a Configuration struct
func LoadConfig(filename string) (*Configuration, error) {
	// Read the KDL file
	data, err := os.ReadFile(filename)
	if err != nil {
		return nil, fmt.Errorf("failed to read KDL file: %w", err)
	}

	// First, parse the KDL document to preserve context order
	doc, err := kdl.Parse(bytes.NewReader(data))
	if err != nil {
		return nil, fmt.Errorf("failed to parse KDL: %w", err)
	}

	// Extract context order, environment variables, env conditions, and structured conditions
	contextOrder := make([]string, 0)
	contextEnvs := make(map[string]map[string]string)
	contextEnvConditions := make(map[string]map[string][]string)
	contextStructuredConditions := make(map[string]interface{}) // Will be security.Condition
	locationEnvs := make(map[string]map[string]string)
	locationEnvConditions := make(map[string]map[string][]string)
	locationStructuredConditions := make(map[string]interface{}) // Will be security.Condition

	for _, node := range doc.Nodes {
		if node.Name == nil {
			continue
		}

		// Extract context order, environment, and conditions
		if node.Name.Value == "context" && len(node.Arguments) > 0 {
			if contextName, ok := node.Arguments[0].Value.(string); ok {
				contextOrder = append(contextOrder, contextName)
				// Extract environment variables from this context
				env := extractEnvironmentFromNode(node)
				if len(env) > 0 {
					contextEnvs[contextName] = env
				}
				// Extract env conditions from this context (simple)
				envConds := extractEnvConditionsFromNode(node)
				if len(envConds) > 0 {
					contextEnvConditions[contextName] = envConds
				}
				// Try to parse structured conditions (new format)
				if cond, err := parseConditionsBlock(node); err == nil && cond != nil {
					contextStructuredConditions[contextName] = cond
				}
			}
		}

		// Extract location environment, env conditions, and structured conditions
		if node.Name.Value == "location" && len(node.Arguments) > 0 {
			if locationName, ok := node.Arguments[0].Value.(string); ok {
				// Extract environment variables from this location
				env := extractEnvironmentFromNode(node)
				if len(env) > 0 {
					locationEnvs[locationName] = env
				}
				// Extract env conditions from this location (simple)
				envConds := extractEnvConditionsFromNode(node)
				if len(envConds) > 0 {
					locationEnvConditions[locationName] = envConds
				}
				// Try to parse structured conditions (new format)
				if cond, err := parseConditionsBlock(node); err == nil && cond != nil {
					locationStructuredConditions[locationName] = cond
				}
			}
		}
	}

	// Unmarshal KDL into internal struct
	var kdlCfg kdlConfig
	if err := kdl.Unmarshal(data, &kdlCfg); err != nil {
		return nil, fmt.Errorf("failed to unmarshal KDL: %w", err)
	}

	// Convert to our clean Configuration struct
	cfg := &Configuration{
		Verbose:              kdlCfg.Verbose,
		CheckOnStartup:       true,  // Default
		CheckOnNetworkChange: true,  // Default
		Locations:            make(map[string]*Location),
		Contexts:             make([]*ContextRule, 0),
		Exports:              make([]ExportConfig, 0),
	}

	// Convert exports
	if kdlCfg.Exports != nil {
		if kdlCfg.Exports.Dotenv != "" {
			cfg.Exports = append(cfg.Exports, ExportConfig{Type: "dotenv", Path: kdlCfg.Exports.Dotenv})
		}
		if kdlCfg.Exports.Context != "" {
			cfg.Exports = append(cfg.Exports, ExportConfig{Type: "context", Path: kdlCfg.Exports.Context})
		}
		if kdlCfg.Exports.Location != "" {
			cfg.Exports = append(cfg.Exports, ExportConfig{Type: "location", Path: kdlCfg.Exports.Location})
		}
		if kdlCfg.Exports.PublicIP != "" {
			cfg.Exports = append(cfg.Exports, ExportConfig{Type: "public_ip", Path: kdlCfg.Exports.PublicIP})
		}
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
			Environment: make(map[string]string),
		}

		// Check if structured condition exists (new format)
		if structCond, exists := locationStructuredConditions[name]; exists {
			loc.Condition = structCond
		} else {
			// Fall back to simple conditions
			if kdlLoc.Conditions != nil {
				if len(kdlLoc.Conditions.PublicIP) > 0 {
					loc.Conditions["public_ip"] = kdlLoc.Conditions.PublicIP
				}
			}

			// Add env conditions from manually parsed data
			if envConds, exists := locationEnvConditions[name]; exists {
				for key, patterns := range envConds {
					loc.Conditions[key] = patterns
				}
			}
		}

		// Add environment variables from manually parsed data
		if env, exists := locationEnvs[name]; exists {
			loc.Environment = env
		}

		cfg.Locations[name] = loc
	}

	// Convert context rules in the order they appear in the file
	for _, name := range contextOrder {
		kdlCtx, exists := kdlCfg.Contexts[name]
		if !exists {
			continue // Skip if context wasn't properly parsed
		}

		rule := &ContextRule{
			Name:        name,
			DisplayName: kdlCtx.DisplayName,
			Locations:   kdlCtx.Locations,
			Conditions:  make(map[string][]string),
			Environment: make(map[string]string),
		}

		// Check if structured condition exists (new format)
		if structCond, exists := contextStructuredConditions[name]; exists {
			rule.Condition = structCond
		} else {
			// Fall back to simple conditions
			if kdlCtx.Conditions != nil {
				if len(kdlCtx.Conditions.PublicIP) > 0 {
					rule.Conditions["public_ip"] = kdlCtx.Conditions.PublicIP
				}
			}

			// Add env conditions from manually parsed data
			if envConds, exists := contextEnvConditions[name]; exists {
				for key, patterns := range envConds {
					rule.Conditions[key] = patterns
				}
			}
		}

		// Convert actions
		if kdlCtx.Actions != nil {
			rule.Actions = ContextActions{
				Connect:    kdlCtx.Actions.Connect,
				Disconnect: kdlCtx.Actions.Disconnect,
			}
		}

		// Add environment variables from manually parsed data
		if env, exists := contextEnvs[name]; exists {
			rule.Environment = env
		}

		cfg.Contexts = append(cfg.Contexts, rule)
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
		Contexts:  make([]*ContextRule, 0),
	}
}
