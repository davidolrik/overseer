// Package state provides centralized state management for the security system.
// It implements a single-goroutine architecture that eliminates race conditions
// by processing all sensor readings sequentially through a single channel.
package state

import (
	"net"
	"time"
)

// SensorReading represents a single observation from a sensor.
// Sensors emit these readings to a central channel for processing.
// Readings are immutable and timestamped.
type SensorReading struct {
	// Sensor is the name of the sensor that produced this reading
	Sensor string

	// Timestamp is when this reading was taken
	Timestamp time.Time

	// Online indicates network connectivity status (nil if sensor doesn't report this)
	Online *bool

	// IP is the detected IP address (nil if sensor doesn't report this)
	IP net.IP

	// Value is a generic string value for sensors that report strings
	Value string

	// Error is set if the sensor check failed
	Error error

	// Latency is how long the sensor check took
	Latency time.Duration
}

// StateSnapshot represents the authoritative state at a point in time.
// Snapshots are immutable and safe to share across goroutines.
type StateSnapshot struct {
	// Timestamp is when this snapshot was created
	Timestamp time.Time

	// Online indicates whether we have network connectivity
	Online bool

	// OnlineSource identifies which sensor determined the online status
	OnlineSource string

	// PublicIPv4 is the detected public IPv4 address
	PublicIPv4 net.IP

	// PublicIPv6 is the detected public IPv6 address
	PublicIPv6 net.IP

	// LocalIPv4 is the local LAN IPv4 address
	LocalIPv4 net.IP

	// Context is the current security context name
	Context string

	// ContextDisplayName is the human-readable context name
	ContextDisplayName string

	// Location is the current location name
	Location string

	// LocationDisplayName is the human-readable location name
	LocationDisplayName string

	// MatchedRule is the name of the rule that determined this context
	MatchedRule string

	// Environment contains the merged environment variables for this state
	Environment map[string]string
}

// StateTransition represents a change from one state to another.
// This is passed to the effects processor for handling side effects.
type StateTransition struct {
	// From is the previous state
	From StateSnapshot

	// To is the new state
	To StateSnapshot

	// Trigger identifies what caused this transition
	Trigger string

	// ChangedFields lists which fields actually changed
	ChangedFields []string
}

// HasChanged returns true if a specific field changed in this transition
func (t StateTransition) HasChanged(field string) bool {
	for _, f := range t.ChangedFields {
		if f == field {
			return true
		}
	}
	return false
}

// LogLevel represents the severity of a log entry
type LogLevel int

const (
	LogDebug LogLevel = iota
	LogInfo
	LogWarn
	LogError
)

func (l LogLevel) String() string {
	switch l {
	case LogDebug:
		return "DEBUG"
	case LogInfo:
		return "INFO"
	case LogWarn:
		return "WARN"
	case LogError:
		return "ERROR"
	default:
		return "UNKNOWN"
	}
}

// LogCategory identifies the type of event being logged
type LogCategory int

const (
	CategorySensor LogCategory = iota // Sensor readings
	CategoryState                     // State transitions
	CategoryEffect                    // Side effects (env writes, callbacks)
	CategorySystem                    // System events (daemon start/stop)
	CategoryHook                      // Hook script execution
)

func (c LogCategory) String() string {
	switch c {
	case CategorySensor:
		return "sensor"
	case CategoryState:
		return "state"
	case CategoryEffect:
		return "effect"
	case CategorySystem:
		return "system"
	case CategoryHook:
		return "hook"
	default:
		return "unknown"
	}
}

func (c LogCategory) Icon() string {
	switch c {
	case CategorySensor:
		return "~" // sensor/probe
	case CategoryState:
		return "*" // state change
	case CategoryEffect:
		return ">" // effect/write
	case CategorySystem:
		return "#" // system
	case CategoryHook:
		return "!" // hook execution
	default:
		return "?"
	}
}

// LogEntry is the universal event type for streaming to clients.
// It captures all types of events that can be displayed in the logs.
type LogEntry struct {
	// Timestamp is when this event occurred
	Timestamp time.Time

	// Level is the severity of this event
	Level LogLevel

	// Category identifies what type of event this is
	Category LogCategory

	// Message is the human-readable description
	Message string

	// Sensor is set for sensor readings
	Sensor *SensorLogData

	// Transition is set for state transitions
	Transition *TransitionLogData

	// Effect is set for effect results
	Effect *EffectLogData

	// System is set for system events
	System *SystemLogData

	// Hook is set for hook script executions
	Hook *HookLogData
}

// SensorLogData contains details about a sensor reading
type SensorLogData struct {
	Name    string
	Online  *bool
	IP      string
	Value   string
	Latency time.Duration
	Error   string
}

// TransitionLogData contains details about a state transition
type TransitionLogData struct {
	Field  string // "online", "context", "location", "ipv4", "ipv6"
	From   string
	To     string
	Source string // What triggered this transition
}

// EffectLogData contains details about an effect execution
type EffectLogData struct {
	Name     string // "env_write", "db_log", "callback"
	Target   string // File path, callback name, etc.
	Success  bool
	Duration time.Duration
	Error    string
}

// SystemLogData contains details about a system event
type SystemLogData struct {
	Event   string // "daemon_start", "daemon_stop", "config_reload", "client_connect"
	Details string
}

// HookLogData contains details about a hook execution
type HookLogData struct {
	Type       string        // "enter" or "leave"
	Target     string        // Location or context name
	TargetType string        // "location" or "context"
	Command    string        // Command that was executed
	Success    bool          // Whether the hook succeeded
	Duration   time.Duration // How long the hook took
	Output     string        // Captured stdout/stderr (truncated)
	Error      string        // Error message if failed
}
