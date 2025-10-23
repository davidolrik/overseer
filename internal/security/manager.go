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
	fileWriter     *FileWriter
	logger         *slog.Logger
	mu             sync.RWMutex
	stopChan       chan struct{}
	stopped        bool

	// Callbacks
	onContextChange func(from, to string, rule *Rule)
}

// ManagerConfig holds configuration for the security manager
type ManagerConfig struct {
	Rules            []Rule
	OutputFile       string
	CheckOnStartup   bool
	OnContextChange  func(from, to string, rule *Rule)
	Logger           *slog.Logger
}

// NewManager creates a new security context manager
func NewManager(config ManagerConfig) (*Manager, error) {
	if config.Logger == nil {
		config.Logger = slog.Default()
	}

	m := &Manager{
		context:         NewSecurityContext(),
		ruleEngine:      NewRuleEngine(config.Rules),
		sensors:         []Sensor{NewIPSensor()},
		networkMonitor:  NewNetworkMonitor(config.Logger),
		logger:          config.Logger,
		stopChan:        make(chan struct{}),
		onContextChange: config.OnContextChange,
	}

	// Setup file writer if output file is specified
	if config.OutputFile != "" {
		fw, err := NewFileWriter(config.OutputFile)
		if err != nil {
			return nil, fmt.Errorf("failed to create file writer: %w", err)
		}
		m.fileWriter = fw
		m.logger.Info("Context file writer enabled", "path", fw.GetPath())
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
	newContext, rule := m.ruleEngine.Evaluate(sensors)

	// Get current context
	oldContext := m.context.GetContext()

	// Update context if changed
	changed := m.context.SetContext(newContext, trigger)

	if changed {
		m.logger.Debug("Security context changed",
			"from", oldContext,
			"to", newContext,
			"trigger", trigger)

		// Write to file if enabled
		if m.fileWriter != nil {
			if err := m.fileWriter.Write(newContext); err != nil {
				m.logger.Error("Failed to write context file", "error", err)
			} else {
				m.logger.Debug("Context written to file", "path", m.fileWriter.GetPath())
			}
		}

		// Call callback if registered
		if m.onContextChange != nil {
			m.onContextChange(oldContext, newContext, rule)
		}
	} else {
		m.logger.Debug("Context unchanged", "context", newContext)
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
