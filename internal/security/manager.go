package security

import (
	"context"
	"fmt"
	"log/slog"
	"sort"
	"sync"
)

// Manager coordinates security context monitoring
type Manager struct {
	context        *SecurityContext
	ruleEngine     *RuleEngine
	sensors        map[string]Sensor // Changed to map for easier lookup
	networkMonitor *NetworkMonitor
	exportWriters  []*ExportWriter
	logger         *slog.Logger
	mu             sync.RWMutex
	stopChan       chan struct{}
	stopped        bool
	dbLogger       *DatabaseLogger // Database logger for sensor changes
	trackedEnvVars []string        // All env var names exported by contexts/locations (for clean unset)
	preferredIP    string          // Preferred IP version for OVERSEER_PUBLIC_IP: "ipv4" or "ipv6"

	// Callbacks
	onContextChange func(from, to string, rule *Rule)

	// Flag to prevent recursive context checks from sensor notifications
	checkingContext bool
}

// ExportConfig represents a single export configuration
type ExportConfig struct {
	Type string // Export type: "dotenv", "context", "location", "public_ip"
	Path string // File path to write to
}

// ManagerConfig holds configuration for the security manager
type ManagerConfig struct {
	Rules           []Rule
	Locations       map[string]Location
	Exports         []ExportConfig
	PreferredIP     string // "ipv4" (default) or "ipv6"
	CheckOnStartup  bool
	OnContextChange func(from, to string, rule *Rule)
	Logger          *slog.Logger
}

// NewManager creates a new security context manager
func NewManager(config ManagerConfig) (*Manager, error) {
	if config.Logger == nil {
		config.Logger = slog.Default()
	}

	// Create sensor map
	sensors := make(map[string]Sensor)

	// Add core active sensors - both IPv4 and IPv6
	ipv4Sensor := NewIPv4Sensor()
	ipv6Sensor := NewIPv6Sensor()
	sensors[ipv4Sensor.Name()] = ipv4Sensor
	sensors[ipv6Sensor.Name()] = ipv6Sensor

	// Add TCP sensor - provides reliable connectivity check via TCP connections
	tcpSensor := NewTCPSensor()
	sensors[tcpSensor.Name()] = tcpSensor

	// Add passive sensors (Context, Location, Online)
	contextSensor := NewContextSensor()
	locationSensor := NewLocationSensor()
	onlineSensor := NewOnlineSensor()

	sensors[contextSensor.Name()] = contextSensor
	sensors[locationSensor.Name()] = locationSensor
	sensors[onlineSensor.Name()] = onlineSensor

	// Wire up Online sensor to listen to TCP and IPv4 sensor changes
	// TCP takes precedence over public_ip for online status determination
	tcpSensor.Subscribe(onlineSensor)
	ipv4Sensor.Subscribe(onlineSensor)

	// Extract all unique environment variable names from rules and locations
	envVars := make(map[string]bool)

	// Check all rules for env conditions (both simple and structured)
	for _, rule := range config.Rules {
		// Simple format
		for key := range rule.Conditions {
			if len(key) > 4 && key[:4] == "env:" {
				varName := key[4:]
				envVars[varName] = true
			}
		}
		// Structured format
		if rule.Condition != nil {
			sensorNames := ExtractRequiredSensors(rule.Condition)
			for _, sensorName := range sensorNames {
				if len(sensorName) > 4 && sensorName[:4] == "env:" {
					varName := sensorName[4:]
					envVars[varName] = true
				}
			}
		}
	}

	// Check all locations for env conditions (both simple and structured)
	for _, location := range config.Locations {
		// Simple format
		for key := range location.Conditions {
			if len(key) > 4 && key[:4] == "env:" {
				varName := key[4:]
				envVars[varName] = true
			}
		}
		// Structured format
		if location.Condition != nil {
			sensorNames := ExtractRequiredSensors(location.Condition)
			for _, sensorName := range sensorNames {
				if len(sensorName) > 4 && sensorName[:4] == "env:" {
					varName := sensorName[4:]
					envVars[varName] = true
				}
			}
		}
	}

	// Create an EnvSensor for each unique environment variable
	for varName := range envVars {
		envSensor := NewEnvSensor(varName)
		sensors[envSensor.Name()] = envSensor
		config.Logger.Debug("Environment sensor created", "var", varName)
	}

	// Collect all environment variable names that are EXPORTED (for auto-unset)
	// These are the vars defined in Environment maps of contexts and locations
	exportedEnvVars := make(map[string]bool)

	// Collect from all locations
	for _, location := range config.Locations {
		if location.Environment != nil {
			for key := range location.Environment {
				exportedEnvVars[key] = true
			}
		}
	}

	// Collect from all contexts/rules
	for _, rule := range config.Rules {
		if rule.Environment != nil {
			for key := range rule.Environment {
				exportedEnvVars[key] = true
			}
		}
	}

	// Convert to sorted slice for consistent output
	trackedVarNames := make([]string, 0, len(exportedEnvVars))
	for key := range exportedEnvVars {
		trackedVarNames = append(trackedVarNames, key)
	}
	// Sort alphabetically for deterministic output
	sort.Strings(trackedVarNames)

	// Set preferred IP version, default to ipv4
	preferredIP := config.PreferredIP
	if preferredIP == "" {
		preferredIP = "ipv4"
	}

	m := &Manager{
		context:         NewSecurityContext(),
		ruleEngine:      NewRuleEngine(config.Rules, config.Locations),
		sensors:         sensors,
		networkMonitor:  NewNetworkMonitor(config.Logger),
		logger:          config.Logger,
		stopChan:        make(chan struct{}),
		onContextChange: config.OnContextChange,
		exportWriters:   make([]*ExportWriter, 0),
		checkingContext: false,
		trackedEnvVars:  trackedVarNames,
		preferredIP:     preferredIP,
	}

	// Subscribe to sensors that should trigger context re-evaluation
	// We subscribe to active sensors (public_ip, env:*) and the online sensor
	for name, sensor := range sensors {
		// Skip passive sensors that are SET by rule evaluation (context, location)
		if name == "context" || name == "location" {
			continue
		}
		// Subscribe to all other sensors (public_ip, online, env:*)
		sensor.Subscribe(m)
	}

	// Setup export writers
	for _, exportCfg := range config.Exports {
		ew, err := NewExportWriter(exportCfg.Type, exportCfg.Path)
		if err != nil {
			return nil, fmt.Errorf("failed to create export writer for %s: %w", exportCfg.Type, err)
		}
		m.exportWriters = append(m.exportWriters, ew)
		m.logger.Info("Export writer enabled", "type", exportCfg.Type, "path", ew.GetPath())
	}

	return m, nil
}

// Start begins monitoring the security context
func (m *Manager) Start(ctx context.Context, checkOnStartup bool) error {
	m.mu.Lock()
	if m.stopped {
		m.mu.Unlock()
		return fmt.Errorf("manager has been stopped")
	}
	m.mu.Unlock()

	// Start network monitor
	m.networkMonitor.Start(ctx)

	// Perform initial check if requested
	if checkOnStartup {
		if err := m.checkContext("startup"); err != nil {
			m.logger.Warn("Initial context check failed", "error", err)
		}
	}

	// Start monitoring loop
	go m.monitorLoop(ctx)

	m.logger.Info("Security context manager started")
	return nil
}

// monitorLoop handles context checks triggered by network changes
func (m *Manager) monitorLoop(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			m.logger.Debug("Security monitor stopping")
			return

		case <-m.stopChan:
			m.logger.Debug("Security monitor stopped")
			return

		case <-m.networkMonitor.Events():
			m.logger.Debug("Network change detected, checking context")
			if err := m.checkContext("network_change"); err != nil {
				m.logger.Error("Context check failed", "error", err)
			}
		}
	}
}

// checkContext performs a full context check with all sensors
func (m *Manager) checkContext(trigger string) error {
	m.mu.Lock()
	m.checkingContext = true
	m.mu.Unlock()

	defer func() {
		m.mu.Lock()
		m.checkingContext = false
		m.mu.Unlock()
	}()

	ctx := context.Background()

	// Check all active sensors (skip passive sensors like context/location/online)
	// Check TCP FIRST before IP sensors so that online status precedence works correctly
	prioritySensors := []string{"tcp", "public_ipv4", "public_ipv6"}
	checkedSensors := make(map[string]bool)

	// First pass: check priority sensors in order
	for _, sensorName := range prioritySensors {
		sensor, exists := m.sensors[sensorName]
		if !exists {
			continue
		}
		checkedSensors[sensorName] = true

		value, err := sensor.Check(ctx)
		if err != nil {
			m.logger.Warn("Sensor check failed",
				"sensor", sensor.Name(),
				"error", err)
			continue
		}

		m.logger.Debug("Sensor reading",
			"sensor", sensor.Name(),
			"type", value.Type,
			"value", value.String())

		// Update sensor value in legacy context
		m.context.UpdateSensor(value)
	}

	// Second pass: check remaining active sensors
	for _, sensor := range m.sensors {
		// Skip already checked sensors
		if checkedSensors[sensor.Name()] {
			continue
		}

		// Skip passive sensors - they are set by rule evaluation or other sensors
		// - context/location: set by rule evaluation
		// - online: set by ping/public_ip sensor notifications
		if sensor.Name() == "context" || sensor.Name() == "location" || sensor.Name() == "online" {
			continue
		}

		value, err := sensor.Check(ctx)
		if err != nil {
			m.logger.Warn("Sensor check failed",
				"sensor", sensor.Name(),
				"error", err)
			continue
		}

		m.logger.Debug("Sensor reading",
			"sensor", sensor.Name(),
			"type", value.Type,
			"value", value.String())

		// Update sensor value in legacy context
		m.context.UpdateSensor(value)
	}

	// Evaluate rules to determine context (pass sensor map directly)
	result := m.ruleEngine.Evaluate(ctx, m.sensors)

	// Get current context and location
	oldContext := m.context.GetContext()
	oldLocation := m.context.GetLocation()

	// Update context and location sensors
	if contextSensor, ok := m.sensors["context"]; ok {
		contextSensor.SetValue(result.Context)
		// Also update legacy context with context sensor value
		value, _ := contextSensor.Check(ctx)
		m.context.UpdateSensor(value)
	}
	if locationSensor, ok := m.sensors["location"]; ok {
		locationSensor.SetValue(result.Location)
		// Also update legacy context with location sensor value
		value, _ := locationSensor.Check(ctx)
		m.context.UpdateSensor(value)
	}

	// Update legacy context with online sensor value (it's reactive, not polled)
	if onlineSensor, ok := m.sensors["online"]; ok {
		value, _ := onlineSensor.Check(ctx)
		m.context.UpdateSensor(value)
	}

	// Update context and location if changed
	changed := m.context.SetContextAndLocation(result.Context, result.Location, trigger)

	// During config reload, always update exports even if context name didn't change
	// (rule properties like environment variables may have changed)
	forceExport := trigger == "config_reload"

	if changed {
		m.logger.Debug("Security context changed",
			"from", oldContext,
			"to", result.Context,
			"from_location", oldLocation,
			"to_location", result.Location,
			"matched_by", result.MatchedBy,
			"trigger", trigger)
	} else if forceExport {
		m.logger.Debug("Context unchanged but updating exports due to config reload",
			"context", result.Context,
			"location", result.Location)
	}

	// Write exports if context changed OR if this is a config reload
	if changed || forceExport {
		// Write to all export files
		if len(m.exportWriters) > 0 {
			// Get public IP sensor values for exports (use cached values)
			publicIPv4 := ""
			publicIPv6 := ""
			if ipv4Sensor, exists := m.sensors["public_ipv4"]; exists {
				if lastValue := ipv4Sensor.GetLastValue(); lastValue != nil {
					publicIPv4 = lastValue.String()
				}
			}
			if ipv6Sensor, exists := m.sensors["public_ipv6"]; exists {
				if lastValue := ipv6Sensor.GetLastValue(); lastValue != nil {
					publicIPv6 = lastValue.String()
				}
			}

			// Determine preferred IP based on config
			publicIP := publicIPv4
			if m.preferredIP == "ipv6" {
				publicIP = publicIPv6
			}

			// Get display names and environment variables
			contextDisplayName := ""
			customEnv := make(map[string]string)

			if result.Rule != nil {
				contextDisplayName = result.Rule.DisplayName
				// Start with context environment variables
				if result.Rule.Environment != nil {
					for k, v := range result.Rule.Environment {
						customEnv[k] = v
					}
				}
			}

			locationDisplayName := ""
			if result.Location != "" {
				if loc := m.ruleEngine.GetLocation(result.Location); loc != nil {
					locationDisplayName = loc.DisplayName
					// Add location environment variables (context vars take precedence)
					if loc.Environment != nil {
						for k, v := range loc.Environment {
							// Only add if not already set by context
							if _, exists := customEnv[k]; !exists {
								customEnv[k] = v
							}
						}
					}
				}
			}

			// Prepare export data
			exportData := ExportData{
				Context:             result.Context,
				ContextDisplayName:  contextDisplayName,
				Location:            result.Location,
				LocationDisplayName: locationDisplayName,
				PublicIP:            publicIP,
				PublicIPv4:          publicIPv4,
				PublicIPv6:          publicIPv6,
				CustomEnvironment:   customEnv,
			}

			// Write to each export writer
			for _, ew := range m.exportWriters {
				// Only pass varsToUnset for dotenv exports
				var varsToUnset []string
				if ew.GetType() == "dotenv" {
					varsToUnset = m.trackedEnvVars
				}

				if err := ew.Write(exportData, varsToUnset); err != nil {
					m.logger.Error("Failed to write export file",
						"type", ew.GetType(),
						"path", ew.GetPath(),
						"error", err)
				} else {
					m.logger.Debug("Export written",
						"type", ew.GetType(),
						"path", ew.GetPath())
				}
			}
		}
	}

	// Call callback if context changed OR on startup (to initialize tunnel state)
	// On startup, we always need to apply the context actions even if starting from "unknown"
	isStartup := trigger == "startup"
	if (changed || isStartup) && m.onContextChange != nil {
		if isStartup && !changed {
			m.logger.Debug("Applying context actions on startup",
				"context", result.Context,
				"location", result.Location)
		}
		m.onContextChange(oldContext, result.Context, result.Rule)
	}

	if !changed && !forceExport && !isStartup {
		m.logger.Debug("Context unchanged", "context", result.Context, "location", result.Location)
	}

	return nil
}

// Stop stops the security manager
func (m *Manager) Stop() {
	m.mu.Lock()
	defer m.mu.Unlock()

	if m.stopped {
		return
	}

	m.stopped = true
	close(m.stopChan)
	m.logger.Info("Security context manager stopped")
}

// GetContext returns the current security context
func (m *Manager) GetContext() *SecurityContext {
	return m.context
}

// GetSensor returns a sensor by name
func (m *Manager) GetSensor(name string) Sensor {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.sensors[name]
}

// TriggerCheck manually triggers a context check
func (m *Manager) TriggerCheck() error {
	m.mu.RLock()
	if m.stopped {
		m.mu.RUnlock()
		return fmt.Errorf("manager is stopped")
	}
	m.mu.RUnlock()

	return m.checkContext("manual")
}

// TriggerCheckWithReason manually triggers a context check with a custom trigger reason
func (m *Manager) TriggerCheckWithReason(trigger string) error {
	m.mu.RLock()
	if m.stopped {
		m.mu.RUnlock()
		return fmt.Errorf("manager is stopped")
	}
	m.mu.RUnlock()

	return m.checkContext(trigger)
}

// AddSensor adds a new sensor to the manager
func (m *Manager) AddSensor(sensor Sensor) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.sensors[sensor.Name()] = sensor

	// If database logger is active, subscribe to this sensor
	if m.dbLogger != nil {
		sensor.Subscribe(m.dbLogger)
	}

	m.logger.Info("Sensor added", "sensor", sensor.Name())
}

// SetDatabase sets the database connection and enables database logging
func (m *Manager) SetDatabase(db DatabaseInterface) {
	m.mu.Lock()
	defer m.mu.Unlock()

	// Create database logger
	m.dbLogger = NewDatabaseLogger(db, m.logger)

	// Subscribe to all existing sensors
	for _, sensor := range m.sensors {
		sensor.Subscribe(m.dbLogger)
	}

	m.logger.Info("Database logging enabled for security manager")
}

// OnSensorChange implements SensorListener to trigger context re-evaluation
// when sensors change (public_ip, online, env:*)
func (m *Manager) OnSensorChange(sensor Sensor, oldValue, newValue SensorValue) {
	m.mu.Lock()
	// Prevent recursive checks - if we're already checking context, don't trigger another check
	if m.checkingContext {
		m.mu.Unlock()
		return
	}
	m.mu.Unlock()

	m.logger.Debug("Sensor change detected, triggering context check",
		"sensor", sensor.Name(),
		"old_value", oldValue.String(),
		"new_value", newValue.String())

	// Trigger a context check asynchronously to avoid blocking the sensor update
	go func() {
		if err := m.checkContext("sensor_change:" + sensor.Name()); err != nil {
			m.logger.Error("Context check failed after sensor change",
				"sensor", sensor.Name(),
				"error", err)
		}
	}()
}
