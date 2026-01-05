package daemon

import (
	"fmt"
	"log/slog"
	"time"

	"overseer.olrik.dev/internal/core"
	"overseer.olrik.dev/internal/db"
	"overseer.olrik.dev/internal/awareness"
	"overseer.olrik.dev/internal/awareness/state"
)

// stateOrchestrator is the state management system
var stateOrchestrator *state.Orchestrator

// initStateOrchestrator initializes the new state orchestrator
func (d *Daemon) initStateOrchestrator() error {
	// Convert location definitions from config
	locations := make(map[string]state.Location)
	for name, loc := range core.Config.Locations {
		stateLoc := state.Location{
			Name:        loc.Name,
			DisplayName: loc.DisplayName,
			Conditions:  loc.Conditions,
			Environment: loc.Environment,
		}
		// Convert structured condition if present
		if loc.Condition != nil {
			stateLoc.Condition = convertCondition(loc.Condition)
		}
		locations[name] = stateLoc
	}

	// Add default locations
	defaultOffline := state.Location{
		Name:        "offline",
		DisplayName: "Offline",
		Condition:   state.NewBooleanCondition("online", false),
		Environment: make(map[string]string),
	}
	defaultUnknown := state.Location{
		Name:        "unknown",
		DisplayName: "Unknown",
		Conditions:  map[string][]string{},
		Environment: make(map[string]string),
	}

	if userOffline, exists := locations["offline"]; exists {
		locations["offline"] = mergeStateLocation(defaultOffline, userOffline)
	} else {
		locations["offline"] = defaultOffline
	}

	if userUnknown, exists := locations["unknown"]; exists {
		locations["unknown"] = mergeStateLocation(defaultUnknown, userUnknown)
	} else {
		locations["unknown"] = defaultUnknown
	}

	// Convert rules
	rules := make([]state.Rule, 0, len(core.Config.Contexts)+1)
	var userUntrusted *state.Rule

	defaultUntrusted := state.Rule{
		Name:        "untrusted",
		DisplayName: "Untrusted",
		Conditions:  map[string][]string{},
		Environment: make(map[string]string),
		Actions: state.RuleActions{
			Connect:    []string{},
			Disconnect: []string{},
		},
	}

	for _, contextRule := range core.Config.Contexts {
		stateRule := state.Rule{
			Name:        contextRule.Name,
			DisplayName: contextRule.DisplayName,
			Locations:   contextRule.Locations,
			Conditions:  contextRule.Conditions,
			Environment: contextRule.Environment,
			Actions: state.RuleActions{
				Connect:    contextRule.Actions.Connect,
				Disconnect: contextRule.Actions.Disconnect,
			},
		}
		if contextRule.Condition != nil {
			stateRule.Condition = convertCondition(contextRule.Condition)
		}

		if stateRule.Name == "untrusted" {
			userUntrusted = &stateRule
			continue
		}
		rules = append(rules, stateRule)
	}

	// Add untrusted fallback at the end
	if userUntrusted != nil {
		rules = append(rules, mergeStateRule(defaultUntrusted, *userUntrusted))
	} else {
		rules = append(rules, defaultUntrusted)
	}

	// Create env writers
	var envWriters []state.EnvWriter
	for _, exportCfg := range core.Config.Exports {
		var writer state.EnvWriter
		var err error

		switch exportCfg.Type {
		case "dotenv":
			writer, err = state.NewDotenvWriter(exportCfg.Path)
		case "context":
			writer, err = state.NewContextWriter(exportCfg.Path)
		case "location":
			writer, err = state.NewLocationWriter(exportCfg.Path)
		case "public_ip":
			writer, err = state.NewPublicIPWriter(exportCfg.Path)
		default:
			slog.Warn("Unknown export type", "type", exportCfg.Type)
			continue
		}

		if err != nil {
			slog.Error("Failed to create export writer", "type", exportCfg.Type, "path", exportCfg.Path, "error", err)
			continue
		}
		envWriters = append(envWriters, writer)
	}

	// Collect tracked env vars from all rules and locations
	trackedVars := collectTrackedEnvVars(rules, locations)

	// Create database logger adapter if database is available
	var dbLogger state.DatabaseLogger
	if d.database != nil {
		dbLogger = newDatabaseLoggerAdapter(d.database)
	}

	// Create orchestrator
	stateOrchestrator = state.NewOrchestrator(state.OrchestratorConfig{
		Rules:          rules,
		Locations:      locations,
		EnvWriters:     envWriters,
		TrackedEnvVars: trackedVars,
		PreferredIP:    core.Config.PreferredIP,
		OnContextChange: func(from, to state.StateSnapshot, rule *state.Rule) {
			d.handleNewContextChange(from, to, rule)
		},
		OnOnlineChange: d.handleOnlineChange,
		DatabaseLogger: dbLogger,
		HistorySize:    200,
		Logger:         slog.Default(),
	})

	// Restore sensor state from hot reload if available
	if sensorState, err := LoadSensorState(); err != nil {
		slog.Warn("Failed to load sensor state", "error", err)
	} else if sensorState != nil {
		slog.Info("Restoring sensor state from hot reload", "sensors", len(sensorState.Sensors))
		stateOrchestrator.RestoreSensorCache(sensorState.Sensors)
		// Remove the state file after successful restoration
		if err := RemoveSensorStateFile(); err != nil {
			slog.Warn("Failed to remove sensor state file", "error", err)
		}
	}

	stateOrchestrator.Start()

	slog.Info("New state orchestrator started")
	return nil
}

// handleNewContextChange is the callback for the new state system
func (d *Daemon) handleNewContextChange(from, to state.StateSnapshot, rule *state.Rule) {
	slog.Info("Security context changed (new system)",
		"from_context", from.Context,
		"to_context", to.Context,
		"from_location", from.Location,
		"to_location", to.Location)

	// If location changed, reset retry counters for ALL tunnels
	if from.Location != to.Location {
		d.mu.Lock()
		resetCount := 0
		for alias, tunnel := range d.tunnels {
			if tunnel.RetryCount > 0 {
				tunnel.RetryCount = 0
				tunnel.NextRetryTime = time.Time{}
				d.tunnels[alias] = tunnel
				resetCount++
			}
		}
		d.mu.Unlock()

		if resetCount > 0 {
			slog.Info("Reset retry counters due to location change",
				"tunnels_reset", resetCount,
				"from_location", from.Location,
				"to_location", to.Location)
		}
	}

	// If no rule matched, nothing more to do
	if rule == nil {
		slog.Debug("No rule matched, skipping context change actions")
		return
	}

	slog.Debug("Context change with rule",
		"rule_name", rule.Name,
		"connect_count", len(rule.Actions.Connect),
		"disconnect_count", len(rule.Actions.Disconnect))

	// Check if we're online before attempting connections
	isOnline := to.Online

	if !isOnline && len(rule.Actions.Connect) > 0 {
		slog.Info("Skipping tunnel connections - currently offline",
			"context", to.Context,
			"tunnel_count", len(rule.Actions.Connect))
	}

	// Execute disconnect actions first (always, even when offline)
	for _, alias := range rule.Actions.Disconnect {
		d.mu.Lock()
		_, exists := d.tunnels[alias]
		d.mu.Unlock()

		if exists {
			slog.Info("Auto-disconnecting tunnel due to context change",
				"tunnel", alias,
				"context", to.Context)
			d.stopTunnel(alias)
		}
	}

	// Only execute connect actions if we're online
	if isOnline {
		for _, alias := range rule.Actions.Connect {
			d.mu.Lock()
			tunnel, exists := d.tunnels[alias]
			d.mu.Unlock()

			shouldConnect := false
			if !exists {
				shouldConnect = true
				slog.Info("Auto-connecting tunnel due to context change",
					"tunnel", alias,
					"context", to.Context)
			} else if tunnel.State == StateDisconnected || tunnel.State == StateReconnecting {
				shouldConnect = true
				slog.Info("Reconnecting tunnel due to context change",
					"tunnel", alias,
					"context", to.Context,
					"previous_state", tunnel.State,
					"previous_retry_count", tunnel.RetryCount)
				d.stopTunnel(alias)
			} else {
				slog.Debug("Skipping tunnel - already connected",
					"tunnel", alias,
					"state", tunnel.State,
					"pid", tunnel.Pid)
			}

			if shouldConnect {
				resp := d.startTunnel(alias)
				for _, msg := range resp.Messages {
					if msg.Status == "ERROR" {
						slog.Error("Failed to start tunnel during context change",
							"tunnel", alias,
							"context", to.Context,
							"error", msg.Message)
					}
				}
			}
		}
	}
}

// databaseLoggerAdapter adapts the database to the state.DatabaseLogger interface
type databaseLoggerAdapter struct {
	db *db.DB
}

func newDatabaseLoggerAdapter(database *db.DB) *databaseLoggerAdapter {
	return &databaseLoggerAdapter{db: database}
}

func (a *databaseLoggerAdapter) LogSensorChange(sensor, sensorType, oldValue, newValue string) error {
	return a.db.LogSensorChange(sensor, sensorType, oldValue, newValue)
}

func (a *databaseLoggerAdapter) LogContextChange(fromContext, toContext, fromLocation, toLocation, trigger string) error {
	// Log context changes as sensor changes (context is tracked as a sensor in the DB)
	if fromContext != toContext {
		if err := a.db.LogSensorChange("context", "string", fromContext, toContext); err != nil {
			return err
		}
	}
	if fromLocation != toLocation {
		if err := a.db.LogSensorChange("location", "string", fromLocation, toLocation); err != nil {
			return err
		}
	}
	return nil
}

// convertCondition converts from awareness.Condition interface to state.Condition
func convertCondition(cond interface{}) state.Condition {
	if cond == nil {
		return nil
	}

	switch c := cond.(type) {
	case *awareness.SensorCondition:
		if c.BoolValue != nil {
			return state.NewBooleanCondition(c.SensorName, *c.BoolValue)
		}
		return state.NewSensorCondition(c.SensorName, c.Pattern)

	case *awareness.GroupCondition:
		conditions := make([]state.Condition, len(c.Conditions))
		for i, child := range c.Conditions {
			conditions[i] = convertCondition(child)
		}
		if c.Operator == "any" {
			return state.NewAnyCondition(conditions...)
		}
		return state.NewAllCondition(conditions...)

	case awareness.Condition:
		// Try to convert via type assertion on methods
		// This handles cases where we have the interface but not the concrete type
		return nil

	default:
		return nil
	}
}

// mergeStateLocation merges user location with defaults
func mergeStateLocation(defaultLoc, userLoc state.Location) state.Location {
	merged := defaultLoc
	if userLoc.DisplayName != "" {
		merged.DisplayName = userLoc.DisplayName
	}
	if len(userLoc.Environment) > 0 {
		if merged.Environment == nil {
			merged.Environment = make(map[string]string)
		}
		for k, v := range userLoc.Environment {
			merged.Environment[k] = v
		}
	}
	return merged
}

// mergeStateRule merges user rule with defaults
func mergeStateRule(defaultRule, userRule state.Rule) state.Rule {
	merged := defaultRule
	if userRule.DisplayName != "" {
		merged.DisplayName = userRule.DisplayName
	}
	if len(userRule.Environment) > 0 {
		if merged.Environment == nil {
			merged.Environment = make(map[string]string)
		}
		for k, v := range userRule.Environment {
			merged.Environment[k] = v
		}
	}
	if len(userRule.Actions.Connect) > 0 || len(userRule.Actions.Disconnect) > 0 {
		merged.Actions = userRule.Actions
	}
	return merged
}

// collectTrackedEnvVars extracts all env var names from rules and locations
func collectTrackedEnvVars(rules []state.Rule, locations map[string]state.Location) []string {
	vars := make(map[string]bool)

	for _, rule := range rules {
		for k := range rule.Environment {
			vars[k] = true
		}
	}

	for _, loc := range locations {
		for k := range loc.Environment {
			vars[k] = true
		}
	}

	result := make([]string, 0, len(vars))
	for v := range vars {
		result = append(result, v)
	}
	return result
}

// GetStateOrchestrator returns the current state orchestrator
func GetStateOrchestrator() *state.Orchestrator {
	return stateOrchestrator
}

// stopStateOrchestrator stops the state orchestrator
func stopStateOrchestrator() {
	if stateOrchestrator != nil {
		stateOrchestrator.Stop()
		stateOrchestrator = nil
	}
}

// reloadStateOrchestrator reloads the state orchestrator with new config
func (d *Daemon) reloadStateOrchestrator() error {
	if stateOrchestrator == nil {
		return fmt.Errorf("state orchestrator not initialized")
	}

	// Convert new config to rules and locations
	locations := make(map[string]state.Location)
	for name, loc := range core.Config.Locations {
		stateLoc := state.Location{
			Name:        loc.Name,
			DisplayName: loc.DisplayName,
			Conditions:  loc.Conditions,
			Environment: loc.Environment,
		}
		if loc.Condition != nil {
			stateLoc.Condition = convertCondition(loc.Condition)
		}
		locations[name] = stateLoc
	}

	// Add defaults
	if _, exists := locations["offline"]; !exists {
		locations["offline"] = state.Location{
			Name:        "offline",
			DisplayName: "Offline",
			Condition:   state.NewBooleanCondition("online", false),
		}
	}
	if _, exists := locations["unknown"]; !exists {
		locations["unknown"] = state.Location{
			Name:        "unknown",
			DisplayName: "Unknown",
		}
	}

	rules := make([]state.Rule, 0, len(core.Config.Contexts)+1)
	for _, contextRule := range core.Config.Contexts {
		stateRule := state.Rule{
			Name:        contextRule.Name,
			DisplayName: contextRule.DisplayName,
			Locations:   contextRule.Locations,
			Conditions:  contextRule.Conditions,
			Environment: contextRule.Environment,
			Actions: state.RuleActions{
				Connect:    contextRule.Actions.Connect,
				Disconnect: contextRule.Actions.Disconnect,
			},
		}
		if contextRule.Condition != nil {
			stateRule.Condition = convertCondition(contextRule.Condition)
		}
		if stateRule.Name != "untrusted" {
			rules = append(rules, stateRule)
		}
	}

	// Add untrusted fallback
	rules = append(rules, state.Rule{
		Name:        "untrusted",
		DisplayName: "Untrusted",
	})

	stateOrchestrator.Reload(rules, locations)
	return nil
}

// checkOnlineStatusNew checks online status using the new state system
func (d *Daemon) checkOnlineStatusNew() bool {
	if stateOrchestrator != nil {
		return stateOrchestrator.IsOnline()
	}
	return d.checkOnlineStatus()
}

// getContextStatusNew returns context status from new state system
func (d *Daemon) getContextStatusNew() (context, location string) {
	if stateOrchestrator != nil {
		state := stateOrchestrator.GetCurrentState()
		return state.Context, state.Location
	}
	return "", ""
}
