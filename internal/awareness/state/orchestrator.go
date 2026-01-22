package state

import (
	"context"
	"log/slog"
	"sync"
	"time"
)

// OrchestratorConfig holds configuration for the state orchestrator
type OrchestratorConfig struct {
	// Rules for context evaluation
	Rules []Rule

	// Locations for location detection
	Locations map[string]Location

	// EnvWriters for exporting state
	EnvWriters []EnvWriter

	// TrackedEnvVars for clean unset on context switch
	TrackedEnvVars []string

	// PreferredIP is "ipv4" or "ipv6"
	PreferredIP string

	// OnContextChange callback with rule info
	OnContextChange func(from, to StateSnapshot, rule *Rule)

	// OnOnlineChange callback
	OnOnlineChange func(wasOnline, isOnline bool)

	// DatabaseLogger for audit logging
	DatabaseLogger DatabaseLogger

	// HistorySize is how many log entries to keep for replay
	HistorySize int

	// Logger for all components
	Logger *slog.Logger

	// LocationHooks maps location names to their hook configurations
	LocationHooks map[string]*HooksConfig

	// ContextHooks maps context names to their hook configurations
	ContextHooks map[string]*HooksConfig

	// GlobalLocationHooks are hooks that run for ALL location changes
	GlobalLocationHooks *HooksConfig

	// GlobalContextHooks are hooks that run for ALL context changes
	GlobalContextHooks *HooksConfig
}

// Orchestrator ties together all the state management components.
// It provides a high-level interface for the daemon to use.
type Orchestrator struct {
	config OrchestratorConfig
	logger *slog.Logger

	// Core components
	manager    *StateManager
	effects    *EffectsProcessor
	streamer   *LogStreamer
	ruleEngine *RuleEngine

	// Probes
	tcpProbe       *TCPProbe
	ipv4Probe      *IPProbe
	ipv6Probe      *IPProbe
	localIPv4Probe *LocalIPProbe
	networkProbe   *NetworkMonitorProbe
	envProbes      []*EnvProbe

	// Readings channel - all probes emit to this
	readings chan SensorReading

	// Track matched rule for callbacks
	currentRule   *Rule
	currentRuleMu sync.RWMutex

	// Lifecycle
	ctx    context.Context
	cancel context.CancelFunc
	wg     sync.WaitGroup
}

// NewOrchestrator creates a new state orchestrator
func NewOrchestrator(config OrchestratorConfig) *Orchestrator {
	if config.Logger == nil {
		config.Logger = slog.Default()
	}
	if config.HistorySize == 0 {
		config.HistorySize = 100
	}

	ctx, cancel := context.WithCancel(context.Background())

	// Create the log streamer first so other components can use it
	streamer := NewLogStreamer(config.HistorySize)

	// Create rule engine
	ruleEngine := NewRuleEngine(config.Rules, config.Locations)

	// Create readings channel
	readings := make(chan SensorReading, 256)

	// Create state manager with the rule engine
	manager := NewStateManager(ManagerConfig{
		Policy:             NewTCPPriorityPolicy(),
		RuleEvaluator:      ruleEngine,
		ReadingsBufferSize: 256,
		Logger:             config.Logger,
	})

	// Create orchestrator first so we can reference it in the effects processor
	o := &Orchestrator{
		config:       config,
		logger:       config.Logger,
		manager:      manager,
		streamer:     streamer,
		ruleEngine:   ruleEngine,
		readings:     readings,
		ctx:          ctx,
		cancel:       cancel,
	}

	// Create effects processor with wrapped callbacks
	effects := NewEffectsProcessor(manager.Transitions(), EffectsProcessorConfig{
		EnvWriters:     config.EnvWriters,
		TrackedEnvVars: config.TrackedEnvVars,
		PreferredIP:    config.PreferredIP,
		OnContextChange: func(from, to StateSnapshot) {
			if config.OnContextChange != nil {
				o.currentRuleMu.RLock()
				rule := o.currentRule
				o.currentRuleMu.RUnlock()
				config.OnContextChange(from, to, rule)
			}
		},
		OnOnlineChange:      config.OnOnlineChange,
		DatabaseLogger:      config.DatabaseLogger,
		LogStreamer:         streamer,
		Logger:              config.Logger,
		LocationHooks:       config.LocationHooks,
		ContextHooks:        config.ContextHooks,
		GlobalLocationHooks: config.GlobalLocationHooks,
		GlobalContextHooks:  config.GlobalContextHooks,
	})
	o.effects = effects

	// Create probes
	o.tcpProbe = NewTCPProbe(config.Logger)
	o.ipv4Probe = NewIPv4Probe(config.Logger)
	o.ipv6Probe = NewIPv6Probe(config.Logger)
	o.localIPv4Probe = NewLocalIPv4Probe(config.Logger)
	o.networkProbe = NewNetworkMonitorProbe(o.ipv4Probe, o.ipv6Probe, o.localIPv4Probe, config.Logger)

	// Create env probes for any env conditions in the config
	envVarNames := CollectEnvSensors(config.Rules, config.Locations)
	for _, varName := range envVarNames {
		o.envProbes = append(o.envProbes, NewEnvProbe(varName))
	}

	// Subscribe to state changes to track current rule
	manager.Subscribe(func(snapshot StateSnapshot) {
		if snapshot.MatchedRule != "" {
			// Find the rule by name
			for i := range config.Rules {
				if config.Rules[i].Name == snapshot.Context {
					o.currentRuleMu.Lock()
					o.currentRule = &config.Rules[i]
					o.currentRuleMu.Unlock()
					break
				}
			}
		}
	})

	return o
}

// Start begins all components
func (o *Orchestrator) Start() {
	// Emit system start event
	o.streamer.Emit(LogEntry{
		Timestamp: time.Now(),
		Level:     LogInfo,
		Category:  CategorySystem,
		Message:   "State orchestrator starting",
		System: &SystemLogData{
			Event:   "orchestrator_start",
			Details: "Starting state management system",
		},
	})

	// Start the state manager
	o.manager.Start()

	// Start the effects processor
	o.effects.Start()

	// Start the readings forwarder
	o.wg.Add(1)
	go o.forwardReadings()

	// Start probes
	o.tcpProbe.Start(o.ctx, o.readings)
	o.networkProbe.Start(o.ctx, o.readings)

	// Check env probes once at startup (env vars don't change during process lifetime)
	for _, envProbe := range o.envProbes {
		reading := envProbe.Check(o.ctx)
		o.manager.SubmitReading(reading)
	}

	o.logger.Info("State orchestrator started")
}

// forwardReadings forwards readings from the central channel to the manager
func (o *Orchestrator) forwardReadings() {
	defer o.wg.Done()

	for {
		select {
		case <-o.ctx.Done():
			return
		case reading := <-o.readings:
			// Emit sensor reading to log stream
			o.emitSensorLog(reading)

			// Forward to state manager
			o.manager.SubmitReading(reading)
		}
	}
}

// emitSensorLog creates a log entry for a sensor reading
func (o *Orchestrator) emitSensorLog(reading SensorReading) {
	level := LogDebug
	if reading.Error != nil {
		level = LogWarn
	}

	errStr := ""
	if reading.Error != nil {
		errStr = reading.Error.Error()
	}

	ip := ""
	if reading.IP != nil {
		ip = reading.IP.String()
	} else if reading.Value != "" {
		ip = reading.Value
	}

	o.streamer.Emit(LogEntry{
		Timestamp: reading.Timestamp,
		Level:     level,
		Category:  CategorySensor,
		Message:   reading.Sensor,
		Sensor: &SensorLogData{
			Name:    reading.Sensor,
			Online:  reading.Online,
			IP:      ip,
			Value:   reading.Value,
			Latency: reading.Latency,
			Error:   errStr,
		},
	})
}

// Stop gracefully shuts down all components
func (o *Orchestrator) Stop() {
	o.streamer.Emit(LogEntry{
		Timestamp: time.Now(),
		Level:     LogInfo,
		Category:  CategorySystem,
		Message:   "State orchestrator stopping",
		System: &SystemLogData{
			Event:   "orchestrator_stop",
			Details: "Shutting down state management system",
		},
	})

	// Cancel context to stop probes
	o.cancel()

	// Wait for readings forwarder to stop
	o.wg.Wait()

	// Stop manager (this will close transitions channel)
	o.manager.Stop()

	// Stop effects processor
	o.effects.Stop()

	o.logger.Info("State orchestrator stopped")
}

// GetCurrentState returns the current state snapshot
func (o *Orchestrator) GetCurrentState() StateSnapshot {
	return o.manager.GetCurrentState()
}

// TriggerCheck forces an immediate state check
func (o *Orchestrator) TriggerCheck(reason string) {
	o.logger.Debug("Manual check triggered", "reason", reason)

	// Check TCP
	reading := o.tcpProbe.Check(o.ctx)
	o.readings <- reading

	// Trigger network probe to check IPs
	o.networkProbe.TriggerCheck(o.ctx, o.readings)

	// Force the manager to re-evaluate
	o.manager.ForceCheck(reason)
}

// SubscribeLogs returns a channel that receives log entries
// If replay is true, recent history is sent first
func (o *Orchestrator) SubscribeLogs(replay bool) (uint64, <-chan LogEntry) {
	return o.streamer.Subscribe(replay)
}

// SubscribeLogsWithHistory returns a channel that receives log entries
// If replay is true, the last 'lines' entries from history are sent first
func (o *Orchestrator) SubscribeLogsWithHistory(replay bool, lines int) (uint64, <-chan LogEntry) {
	return o.streamer.SubscribeWithHistory(replay, lines)
}

// UnsubscribeLogs removes a log subscription
func (o *Orchestrator) UnsubscribeLogs(id uint64) {
	o.streamer.Unsubscribe(id)
}

// SubscribeState adds a callback for state changes
func (o *Orchestrator) SubscribeState(callback func(StateSnapshot)) {
	o.manager.Subscribe(callback)
}

// EmitSystemEvent emits a system event to the log stream
func (o *Orchestrator) EmitSystemEvent(event, details string) {
	o.streamer.Emit(LogEntry{
		Timestamp: time.Now(),
		Level:     LogInfo,
		Category:  CategorySystem,
		Message:   event,
		System: &SystemLogData{
			Event:   event,
			Details: details,
		},
	})
}

// IsOnline returns the current online status
func (o *Orchestrator) IsOnline() bool {
	return o.manager.GetCurrentState().Online
}

// GetLogStreamer returns the log streamer for direct access
func (o *Orchestrator) GetLogStreamer() *LogStreamer {
	return o.streamer
}

// SetHookEventLogger sets the callback function for logging hook events to the database
func (o *Orchestrator) SetHookEventLogger(logger func(identifier, eventType, details string) error) {
	o.effects.SetHookEventLogger(logger)
}

// GetRuleEngine returns the rule engine
func (o *Orchestrator) GetRuleEngine() *RuleEngine {
	return o.ruleEngine
}

// GetCurrentRule returns the currently matched rule (may be nil)
func (o *Orchestrator) GetCurrentRule() *Rule {
	o.currentRuleMu.RLock()
	defer o.currentRuleMu.RUnlock()
	return o.currentRule
}

// Reload updates the rules and locations (called on config reload)
func (o *Orchestrator) Reload(rules []Rule, locations map[string]Location) {
	o.ruleEngine = NewRuleEngine(rules, locations)
	o.config.Rules = rules
	o.config.Locations = locations

	// Recreate env probes for new config
	o.envProbes = nil
	envVarNames := CollectEnvSensors(rules, locations)
	for _, varName := range envVarNames {
		o.envProbes = append(o.envProbes, NewEnvProbe(varName))
	}

	// Check env probes and submit readings
	for _, envProbe := range o.envProbes {
		reading := envProbe.Check(o.ctx)
		o.manager.SubmitReading(reading)
	}

	o.streamer.Emit(LogEntry{
		Timestamp: time.Now(),
		Level:     LogInfo,
		Category:  CategorySystem,
		Message:   "Configuration reloaded",
		System: &SystemLogData{
			Event:   "config_reload",
			Details: "Rules and locations updated",
		},
	})

	// Force a re-check with new rules
	o.TriggerCheck("config_reload")
}

// GetSensorCache returns the current sensor cache for persistence
func (o *Orchestrator) GetSensorCache() []SensorCacheEntry {
	return o.manager.GetSensorCache()
}

// RestoreSensorCache restores a previously saved sensor cache
// This should be called before Start() to preserve state across restarts
func (o *Orchestrator) RestoreSensorCache(entries []SensorCacheEntry) {
	o.manager.RestoreSensorCache(entries)
}
