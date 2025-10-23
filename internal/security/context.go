package security

import (
	"sync"
	"time"
)

// SecurityContext represents the current security/location context
type SecurityContext struct {
	mu             sync.RWMutex
	currentContext string                 // Current context name (e.g., "home", "office", "unknown")
	sensors        map[string]SensorValue // Current sensor readings
	lastChange     time.Time              // When the context last changed
	changeHistory  []ContextChange        // History of context changes
	maxHistory     int                    // Maximum number of history entries to keep
}

// ContextChange represents a transition from one context to another
type ContextChange struct {
	From      string    // Previous context
	To        string    // New context
	Timestamp time.Time // When the change occurred
	Trigger   string    // What triggered the change (e.g., "network_change", "startup")
}

// NewSecurityContext creates a new security context tracker
func NewSecurityContext() *SecurityContext {
	return &SecurityContext{
		currentContext: "unknown",
		sensors:        make(map[string]SensorValue),
		changeHistory:  make([]ContextChange, 0, 10),
		maxHistory:     100, // Keep last 100 changes
		lastChange:     time.Now(),
	}
}

// GetContext returns the current context name
func (sc *SecurityContext) GetContext() string {
	sc.mu.RLock()
	defer sc.mu.RUnlock()
	return sc.currentContext
}

// GetSensorValue returns the current value for a specific sensor
func (sc *SecurityContext) GetSensorValue(sensorName string) (SensorValue, bool) {
	sc.mu.RLock()
	defer sc.mu.RUnlock()
	val, exists := sc.sensors[sensorName]
	return val, exists
}

// GetAllSensors returns all current sensor values
func (sc *SecurityContext) GetAllSensors() map[string]SensorValue {
	sc.mu.RLock()
	defer sc.mu.RUnlock()

	// Return a copy to prevent external modification
	sensors := make(map[string]SensorValue, len(sc.sensors))
	for k, v := range sc.sensors {
		sensors[k] = v
	}
	return sensors
}

// GetLastChange returns the time of the last context change
func (sc *SecurityContext) GetLastChange() time.Time {
	sc.mu.RLock()
	defer sc.mu.RUnlock()
	return sc.lastChange
}

// GetChangeHistory returns a copy of the context change history
func (sc *SecurityContext) GetChangeHistory() []ContextChange {
	sc.mu.RLock()
	defer sc.mu.RUnlock()

	history := make([]ContextChange, len(sc.changeHistory))
	copy(history, sc.changeHistory)
	return history
}

// UpdateSensor updates a sensor value
func (sc *SecurityContext) UpdateSensor(value SensorValue) {
	sc.mu.Lock()
	defer sc.mu.Unlock()
	sc.sensors[value.Key] = value
}

// SetContext updates the current context and records the change
func (sc *SecurityContext) SetContext(newContext string, trigger string) bool {
	sc.mu.Lock()
	defer sc.mu.Unlock()

	// Check if context actually changed
	if sc.currentContext == newContext {
		return false // No change
	}

	// Record the change
	change := ContextChange{
		From:      sc.currentContext,
		To:        newContext,
		Timestamp: time.Now(),
		Trigger:   trigger,
	}

	sc.currentContext = newContext
	sc.lastChange = change.Timestamp
	sc.changeHistory = append(sc.changeHistory, change)

	// Trim history if it gets too long
	if len(sc.changeHistory) > sc.maxHistory {
		sc.changeHistory = sc.changeHistory[len(sc.changeHistory)-sc.maxHistory:]
	}

	return true // Context changed
}

// GetUptime returns how long the current context has been active
func (sc *SecurityContext) GetUptime() time.Duration {
	sc.mu.RLock()
	defer sc.mu.RUnlock()
	return time.Since(sc.lastChange)
}
