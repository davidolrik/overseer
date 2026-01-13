package state

import (
	"context"
	"fmt"
	"log/slog"
	"net"
	"sync"
	"time"
)

// ManagerConfig holds configuration for the StateManager
type ManagerConfig struct {
	// Policy determines how online state is calculated from sensors
	Policy OnlinePolicy

	// RuleEvaluator evaluates context rules against sensor state
	RuleEvaluator RuleEvaluator

	// ReadingsBufferSize is the size of the readings channel buffer
	ReadingsBufferSize int

	// TransitionsBufferSize is the size of the transitions channel buffer
	TransitionsBufferSize int

	// Logger for the manager
	Logger *slog.Logger
}

// RuleEvaluator is implemented by the rule engine to evaluate context rules
type RuleEvaluator interface {
	// Evaluate determines the context and location based on sensor values
	Evaluate(readings map[string]SensorReading, online bool) RuleResult
}

// StateManager coordinates all state changes through a single goroutine.
// All sensor readings flow through the readings channel and are processed
// sequentially, eliminating race conditions by design.
type StateManager struct {
	// Configuration
	policy        OnlinePolicy
	ruleEvaluator RuleEvaluator
	logger        *slog.Logger

	// Input channel - all sensor readings come here
	readings chan SensorReading

	// Sensor cache - only accessed by manager goroutine
	sensorCache map[string]SensorReading

	// Current state - only accessed by manager goroutine
	current StateSnapshot

	// Output channel - state transitions go here for effects processing
	transitions chan StateTransition

	// Subscribers receive state snapshots on every change
	subscribersMu sync.RWMutex
	subscribers   []func(StateSnapshot)

	// Lifecycle
	ctx    context.Context
	cancel context.CancelFunc
	wg     sync.WaitGroup

	// For external access to current state (read-only)
	stateMu      sync.RWMutex
	currentState StateSnapshot
}

// NewStateManager creates a new state manager with the given configuration
func NewStateManager(config ManagerConfig) *StateManager {
	if config.Policy == nil {
		config.Policy = NewTCPPriorityPolicy()
	}
	if config.ReadingsBufferSize == 0 {
		config.ReadingsBufferSize = 256
	}
	if config.TransitionsBufferSize == 0 {
		config.TransitionsBufferSize = 64
	}
	if config.Logger == nil {
		config.Logger = slog.Default()
	}

	ctx, cancel := context.WithCancel(context.Background())

	return &StateManager{
		policy:        config.Policy,
		ruleEvaluator: config.RuleEvaluator,
		logger:        config.Logger,
		readings:      make(chan SensorReading, config.ReadingsBufferSize),
		sensorCache:   make(map[string]SensorReading),
		transitions:   make(chan StateTransition, config.TransitionsBufferSize),
		subscribers:   make([]func(StateSnapshot), 0),
		ctx:           ctx,
		cancel:        cancel,
	}
}

// Start begins processing sensor readings
func (m *StateManager) Start() {
	m.wg.Add(1)
	go m.run()
	m.logger.Info("State manager started", "policy", m.policy.Name())
}

// Stop gracefully shuts down the manager
func (m *StateManager) Stop() {
	m.cancel()
	m.wg.Wait()
	close(m.transitions)
	m.logger.Info("State manager stopped")
}

// Readings returns the channel for submitting sensor readings
func (m *StateManager) Readings() chan<- SensorReading {
	return m.readings
}

// Transitions returns the channel that emits state transitions
func (m *StateManager) Transitions() <-chan StateTransition {
	return m.transitions
}

// Subscribe adds a callback that will be called on every state change
func (m *StateManager) Subscribe(callback func(StateSnapshot)) {
	m.subscribersMu.Lock()
	defer m.subscribersMu.Unlock()
	m.subscribers = append(m.subscribers, callback)
}

// GetCurrentState returns a copy of the current state (thread-safe)
func (m *StateManager) GetCurrentState() StateSnapshot {
	m.stateMu.RLock()
	defer m.stateMu.RUnlock()
	return m.currentState
}

// GetSensorReading returns the latest reading for a sensor (thread-safe)
// Returns nil if no reading exists for that sensor
func (m *StateManager) GetSensorReading(name string) *SensorReading {
	m.stateMu.RLock()
	defer m.stateMu.RUnlock()
	// We need to return a copy since the cache is owned by the manager goroutine
	// For now, return nil - callers should use Subscribe() for reactive updates
	return nil
}

// SubmitReading submits a sensor reading for processing
// This is a convenience method that doesn't block if the buffer is full
func (m *StateManager) SubmitReading(reading SensorReading) bool {
	select {
	case m.readings <- reading:
		return true
	default:
		m.logger.Warn("Readings channel full, dropping reading",
			"sensor", reading.Sensor)
		return false
	}
}

// run is the main processing loop - all state changes happen here
func (m *StateManager) run() {
	defer m.wg.Done()

	for {
		select {
		case <-m.ctx.Done():
			return

		case reading := <-m.readings:
			m.processReading(reading)
		}
	}
}

// processReading handles a single sensor reading
// This is the only place where state is modified
func (m *StateManager) processReading(reading SensorReading) {
	// 1. Update sensor cache
	oldReading, hadOld := m.sensorCache[reading.Sensor]
	m.sensorCache[reading.Sensor] = reading

	// Log the reading at debug level
	m.logger.Debug("Sensor reading received",
		"sensor", reading.Sensor,
		"online", reading.Online,
		"ip", reading.IP,
		"value", reading.Value,
		"error", reading.Error,
		"latency", reading.Latency)

	// 2. Evaluate online policy
	online, onlineSource := m.policy.Evaluate(m.sensorCache)

	// 3. Evaluate context rules if we have a rule evaluator
	var ruleResult RuleResult
	if m.ruleEvaluator != nil {
		ruleResult = m.ruleEvaluator.Evaluate(m.sensorCache, online)
	}

	// 4. Build new snapshot
	newSnapshot := StateSnapshot{
		Timestamp:           time.Now(),
		Online:              online,
		OnlineSource:        onlineSource,
		PublicIPv4:          m.getIP("public_ipv4"),
		PublicIPv6:          m.getIP("public_ipv6"),
		LocalIPv4:           m.getIP("local_ipv4"),
		Context:             ruleResult.Context,
		ContextDisplayName:  ruleResult.ContextDisplayName,
		Location:            ruleResult.Location,
		LocationDisplayName: ruleResult.LocationDisplayName,
		MatchedRule:         ruleResult.MatchedRule,
		Environment:         ruleResult.Environment,
	}

	// When online but IP unknown, set to 0.0.0.0/:: to distinguish from offline
	if online {
		if newSnapshot.PublicIPv4 == nil {
			newSnapshot.PublicIPv4 = net.ParseIP("0.0.0.0")
		}
		if newSnapshot.PublicIPv6 == nil {
			newSnapshot.PublicIPv6 = net.ParseIP("::")
		}
		if newSnapshot.LocalIPv4 == nil {
			newSnapshot.LocalIPv4 = net.ParseIP("0.0.0.0")
		}
	}

	// 5. Determine what changed
	changedFields := m.detectChanges(m.current, newSnapshot)

	// Also check if this specific sensor reading represents a change
	sensorChanged := !hadOld || !readingsEqual(oldReading, reading)

	// 6. If anything meaningful changed, emit transition
	if len(changedFields) > 0 || sensorChanged {
		transition := StateTransition{
			From:          m.current,
			To:            newSnapshot,
			Trigger:       reading.Sensor,
			ChangedFields: changedFields,
		}

		// Update current state
		m.current = newSnapshot

		// Update the thread-safe copy
		m.stateMu.Lock()
		m.currentState = newSnapshot
		m.stateMu.Unlock()

		// Only emit transition if actual state fields changed
		if len(changedFields) > 0 {
			// Non-blocking send to transitions channel
			select {
			case m.transitions <- transition:
			default:
				m.logger.Warn("Transitions channel full, dropping transition",
					"trigger", transition.Trigger,
					"changed", transition.ChangedFields)
			}

			// Notify subscribers (they receive snapshot, not transition)
			m.notifySubscribers(newSnapshot)

			m.logger.Debug("State transition emitted",
				"trigger", reading.Sensor,
				"changed", changedFields)
		}
	}
}

// getIP extracts an IP from the sensor cache
func (m *StateManager) getIP(sensorName string) net.IP {
	if reading, ok := m.sensorCache[sensorName]; ok {
		if reading.IP != nil {
			return reading.IP
		}
		if reading.Value != "" {
			return net.ParseIP(reading.Value)
		}
	}
	return nil
}

// detectChanges compares two snapshots and returns which fields changed
func (m *StateManager) detectChanges(old, new StateSnapshot) []string {
	var changed []string

	if old.Online != new.Online {
		changed = append(changed, "online")
	}
	if old.Location != new.Location {
		changed = append(changed, "location")
	}
	if old.Context != new.Context {
		changed = append(changed, "context")
	}
	if !ipEqual(old.PublicIPv4, new.PublicIPv4) {
		changed = append(changed, "ipv4")
	}
	if !ipEqual(old.PublicIPv6, new.PublicIPv6) {
		changed = append(changed, "ipv6")
	}
	if !ipEqual(old.LocalIPv4, new.LocalIPv4) {
		changed = append(changed, "local_ipv4")
	}

	return changed
}

// notifySubscribers calls all registered callbacks with the new state
func (m *StateManager) notifySubscribers(snapshot StateSnapshot) {
	m.subscribersMu.RLock()
	subscribers := make([]func(StateSnapshot), len(m.subscribers))
	copy(subscribers, m.subscribers)
	m.subscribersMu.RUnlock()

	for _, sub := range subscribers {
		sub(snapshot)
	}
}

// ipEqual compares two IPs for equality (handles nil)
func ipEqual(a, b net.IP) bool {
	if a == nil && b == nil {
		return true
	}
	if a == nil || b == nil {
		return false
	}
	return a.Equal(b)
}

// readingsEqual compares two sensor readings for equality
func readingsEqual(a, b SensorReading) bool {
	if a.Sensor != b.Sensor {
		return false
	}
	if (a.Online == nil) != (b.Online == nil) {
		return false
	}
	if a.Online != nil && b.Online != nil && *a.Online != *b.Online {
		return false
	}
	if !ipEqual(a.IP, b.IP) {
		return false
	}
	if a.Value != b.Value {
		return false
	}
	if (a.Error == nil) != (b.Error == nil) {
		return false
	}
	if a.Error != nil && b.Error != nil && a.Error.Error() != b.Error.Error() {
		return false
	}
	return true
}

// ForceCheck triggers an immediate context evaluation
// This is useful after config changes
func (m *StateManager) ForceCheck(trigger string) {
	// Submit a synthetic reading to trigger re-evaluation
	m.SubmitReading(SensorReading{
		Sensor:    fmt.Sprintf("force_check:%s", trigger),
		Timestamp: time.Now(),
	})
}

// SensorCacheEntry is a serializable version of a sensor reading
type SensorCacheEntry struct {
	Sensor    string  `json:"sensor"`
	Timestamp string  `json:"timestamp"`
	Online    *bool   `json:"online,omitempty"`
	IP        string  `json:"ip,omitempty"`
	Value     string  `json:"value,omitempty"`
}

// GetSensorCache returns a serializable copy of the current sensor cache
// This is thread-safe and can be called from any goroutine
func (m *StateManager) GetSensorCache() []SensorCacheEntry {
	m.stateMu.RLock()
	defer m.stateMu.RUnlock()

	entries := make([]SensorCacheEntry, 0, len(m.sensorCache))
	for _, reading := range m.sensorCache {
		entry := SensorCacheEntry{
			Sensor:    reading.Sensor,
			Timestamp: reading.Timestamp.Format(time.RFC3339Nano),
			Online:    reading.Online,
			Value:     reading.Value,
		}
		if reading.IP != nil {
			entry.IP = reading.IP.String()
		}
		entries = append(entries, entry)
	}
	return entries
}

// RestoreSensorCache restores sensor readings from a saved cache
// This should be called before Start() to pre-populate the cache
func (m *StateManager) RestoreSensorCache(entries []SensorCacheEntry) {
	for _, entry := range entries {
		ts, err := time.Parse(time.RFC3339Nano, entry.Timestamp)
		if err != nil {
			ts = time.Now()
		}

		reading := SensorReading{
			Sensor:    entry.Sensor,
			Timestamp: ts,
			Online:    entry.Online,
			Value:     entry.Value,
		}
		if entry.IP != "" {
			reading.IP = net.ParseIP(entry.IP)
		}

		m.sensorCache[entry.Sensor] = reading
	}

	// Evaluate state based on restored cache
	if len(entries) > 0 {
		online, onlineSource := m.policy.Evaluate(m.sensorCache)
		var ruleResult RuleResult
		if m.ruleEvaluator != nil {
			ruleResult = m.ruleEvaluator.Evaluate(m.sensorCache, online)
		}

		m.current = StateSnapshot{
			Timestamp:           time.Now(),
			Online:              online,
			OnlineSource:        onlineSource,
			PublicIPv4:          m.getIP("public_ipv4"),
			PublicIPv6:          m.getIP("public_ipv6"),
			LocalIPv4:           m.getIP("local_ipv4"),
			Context:             ruleResult.Context,
			ContextDisplayName:  ruleResult.ContextDisplayName,
			Location:            ruleResult.Location,
			LocationDisplayName: ruleResult.LocationDisplayName,
			MatchedRule:         ruleResult.MatchedRule,
			Environment:         ruleResult.Environment,
		}

		m.stateMu.Lock()
		m.currentState = m.current
		m.stateMu.Unlock()

		m.logger.Info("Sensor cache restored",
			"entries", len(entries),
			"context", m.current.Context,
			"location", m.current.Location,
			"online", m.current.Online)
	}
}
