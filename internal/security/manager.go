package security

import (
	"context"
	"fmt"
	"log/slog"
	"sync"
)

// Manager coordinates security context monitoring
type Manager struct {
	context        *SecurityContext
	ruleEngine     *RuleEngine
	sensors        []Sensor
	networkMonitor *NetworkMonitor
	exportWriters  []*ExportWriter
	logger         *slog.Logger
	mu             sync.RWMutex
	stopChan       chan struct{}
	stopped        bool

	// Callbacks
	onContextChange func(from, to string, rule *Rule)
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
	CheckOnStartup  bool
	OnContextChange func(from, to string, rule *Rule)
	Logger          *slog.Logger
}

// NewManager creates a new security context manager
func NewManager(config ManagerConfig) (*Manager, error) {
	if config.Logger == nil {
		config.Logger = slog.Default()
	}

	m := &Manager{
		context:         NewSecurityContext(),
		ruleEngine:      NewRuleEngine(config.Rules, config.Locations),
		sensors:         []Sensor{NewIPSensor()},
		networkMonitor:  NewNetworkMonitor(config.Logger),
		logger:          config.Logger,
		stopChan:        make(chan struct{}),
		onContextChange: config.OnContextChange,
		exportWriters:   make([]*ExportWriter, 0),
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
	ctx := context.Background()

	// Check all sensors
	for _, sensor := range m.sensors {
		value, err := sensor.Check(ctx)
		if err != nil {
			m.logger.Warn("Sensor check failed",
				"sensor", sensor.Name(),
				"error", err)
			continue
		}

		m.logger.Debug("Sensor reading",
			"sensor", sensor.Name(),
			"value", value.String())

		// Update sensor value
		m.context.UpdateSensor(value)
	}

	// Evaluate rules to determine context
	sensors := m.context.GetAllSensors()
	result := m.ruleEngine.Evaluate(sensors)

	// Get current context and location
	oldContext := m.context.GetContext()
	oldLocation := m.context.GetLocation()

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
			// Get public IP sensor value for exports
			publicIP := ""
			if ipSensor, exists := sensors["public_ip"]; exists {
				publicIP = ipSensor.String()
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
				CustomEnvironment:   customEnv,
			}

			// Write to each export writer
			for _, ew := range m.exportWriters {
				if err := ew.Write(exportData); err != nil {
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

	// Call callback only if context actually changed (not just forced export)
	if changed && m.onContextChange != nil {
		m.onContextChange(oldContext, result.Context, result.Rule)
	}

	if !changed && !forceExport {
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
	m.sensors = append(m.sensors, sensor)
	m.logger.Info("Sensor added", "sensor", sensor.Name())
}
