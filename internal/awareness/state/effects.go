package state

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

// EffectsProcessorConfig holds configuration for the effects processor
type EffectsProcessorConfig struct {
	// EnvWriters are the file writers for environment exports
	EnvWriters []EnvWriter

	// TrackedEnvVars are variable names to unset when switching contexts
	TrackedEnvVars []string

	// PreferredIP is "ipv4" or "ipv6" for OVERSEER_PUBLIC_IP
	PreferredIP string

	// OnContextChange is called when context or location changes
	OnContextChange func(from, to StateSnapshot)

	// OnOnlineChange is called when online status changes
	OnOnlineChange func(wasOnline, isOnline bool)

	// DatabaseLogger logs transitions to the database
	DatabaseLogger DatabaseLogger

	// LogStreamer broadcasts events to connected clients
	LogStreamer *LogStreamer

	// Logger for the processor
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

// EnvWriter writes environment data to a file
type EnvWriter interface {
	// Write writes the state data to the file
	Write(data EnvExportData, varsToUnset []string) error

	// Name returns the writer name for logging
	Name() string

	// Path returns the file path
	Path() string
}

// EnvExportData contains the data to export to environment files
type EnvExportData struct {
	Context             string
	ContextDisplayName  string
	Location            string
	LocationDisplayName string
	PublicIP            string // Preferred IP (ipv4 or ipv6)
	PublicIPv4          string
	PublicIPv6          string
	LocalIPv4           string
	CustomEnvironment   map[string]string
}

// DatabaseLogger logs state transitions to a database
type DatabaseLogger interface {
	// LogSensorChange logs a sensor value change
	LogSensorChange(sensor, sensorType, oldValue, newValue string) error

	// LogContextChange logs a context/location change
	LogContextChange(fromContext, toContext, fromLocation, toLocation, trigger string) error
}

// EffectsProcessor handles all side effects from state transitions.
// It processes transitions sequentially in a single goroutine,
// ensuring deterministic ordering of all side effects.
type EffectsProcessor struct {
	config EffectsProcessorConfig
	logger *slog.Logger

	// Input channel - transitions to process
	transitions <-chan StateTransition

	// Hook executor
	hookExecutor        *HookExecutor
	locationHooks       map[string]*HooksConfig
	contextHooks        map[string]*HooksConfig
	globalLocationHooks *HooksConfig
	globalContextHooks  *HooksConfig

	// Track last IPv4 written to env files (used to avoid race with in-memory state)
	lastWrittenIPv4 atomic.Value

	// Lifecycle
	ctx    context.Context
	cancel context.CancelFunc
	wg     sync.WaitGroup
}

// NewEffectsProcessor creates a new effects processor
func NewEffectsProcessor(transitions <-chan StateTransition, config EffectsProcessorConfig) *EffectsProcessor {
	if config.Logger == nil {
		config.Logger = slog.Default()
	}
	if config.PreferredIP == "" {
		config.PreferredIP = "ipv4"
	}

	ctx, cancel := context.WithCancel(context.Background())

	// Initialize hook maps if nil
	locationHooks := config.LocationHooks
	if locationHooks == nil {
		locationHooks = make(map[string]*HooksConfig)
	}
	contextHooks := config.ContextHooks
	if contextHooks == nil {
		contextHooks = make(map[string]*HooksConfig)
	}

	return &EffectsProcessor{
		config:              config,
		logger:              config.Logger,
		transitions:         transitions,
		hookExecutor:        NewHookExecutor(config.Logger, config.LogStreamer),
		locationHooks:       locationHooks,
		contextHooks:        contextHooks,
		globalLocationHooks: config.GlobalLocationHooks,
		globalContextHooks:  config.GlobalContextHooks,
		ctx:                 ctx,
		cancel:              cancel,
	}
}

// SetHookEventLogger sets the callback function for logging hook events to the database
func (ep *EffectsProcessor) SetHookEventLogger(logger func(identifier, eventType, details string) error) {
	ep.hookExecutor.SetEventLogger(logger)
}

// Start begins processing transitions
func (ep *EffectsProcessor) Start() {
	ep.wg.Add(1)
	go ep.run()
	ep.logger.Info("Effects processor started")
}

// Stop gracefully shuts down the processor
func (ep *EffectsProcessor) Stop() {
	ep.cancel()
	ep.wg.Wait()
	ep.logger.Info("Effects processor stopped")
}

// run is the main processing loop
func (ep *EffectsProcessor) run() {
	defer ep.wg.Done()

	for {
		select {
		case <-ep.ctx.Done():
			return

		case transition, ok := <-ep.transitions:
			if !ok {
				return // Channel closed
			}
			ep.processTransition(transition)
		}
	}
}

// processTransition handles a single state transition
// All side effects happen sequentially here
func (ep *EffectsProcessor) processTransition(t StateTransition) {
	startTime := time.Now()

	ep.logger.Debug("Processing transition",
		"trigger", t.Trigger,
		"changed", t.ChangedFields)

	// 1. Execute LEAVE hooks first (if location/context changed)
	ep.executeLeaveHooks(t)

	// 2. Emit log entries for state changes
	ep.emitTransitionLogs(t)

	// 3. Log to database
	if ep.config.DatabaseLogger != nil {
		ep.logToDatabase(t)
	}

	// 4. Write environment files
	if len(ep.config.EnvWriters) > 0 {
		ep.writeEnvFiles(t)
	}

	// 5. Execute ENTER hooks (if location/context changed)
	ep.executeEnterHooks(t)

	// 6. Execute callbacks
	ep.executeCallbacks(t)

	ep.logger.Debug("Transition processed",
		"trigger", t.Trigger,
		"duration", time.Since(startTime))
}

// emitTransitionLogs creates log entries for each changed field
func (ep *EffectsProcessor) emitTransitionLogs(t StateTransition) {
	if ep.config.LogStreamer == nil {
		return
	}

	// Emit a log entry for each changed field
	for _, field := range t.ChangedFields {
		var entry LogEntry

		switch field {
		case "online":
			entry = LogEntry{
				Timestamp: t.To.Timestamp,
				Level:     LogInfo,
				Category:  CategoryState,
				Message:   fmt.Sprintf("online: %v -> %v", t.From.Online, t.To.Online),
				Transition: &TransitionLogData{
					Field:  "online",
					From:   fmt.Sprintf("%v", t.From.Online),
					To:     fmt.Sprintf("%v", t.To.Online),
					Source: t.To.OnlineSource,
				},
			}

		case "context":
			entry = LogEntry{
				Timestamp: t.To.Timestamp,
				Level:     LogInfo,
				Category:  CategoryState,
				Message:   fmt.Sprintf("context: %s -> %s", t.From.Context, t.To.Context),
				Transition: &TransitionLogData{
					Field:  "context",
					From:   t.From.Context,
					To:     t.To.Context,
					Source: t.To.MatchedRule,
				},
			}

		case "location":
			entry = LogEntry{
				Timestamp: t.To.Timestamp,
				Level:     LogInfo,
				Category:  CategoryState,
				Message:   fmt.Sprintf("location: %s -> %s", t.From.Location, t.To.Location),
				Transition: &TransitionLogData{
					Field:  "location",
					From:   t.From.Location,
					To:     t.To.Location,
					Source: t.Trigger,
				},
			}

		case "ipv4":
			fromIP := ""
			toIP := ""
			if t.From.PublicIPv4 != nil {
				fromIP = t.From.PublicIPv4.String()
			}
			if t.To.PublicIPv4 != nil {
				toIP = t.To.PublicIPv4.String()
			}
			entry = LogEntry{
				Timestamp: t.To.Timestamp,
				Level:     LogInfo,
				Category:  CategoryState,
				Message:   fmt.Sprintf("ipv4: %s -> %s", fromIP, toIP),
				Transition: &TransitionLogData{
					Field:  "ipv4",
					From:   fromIP,
					To:     toIP,
					Source: t.Trigger,
				},
			}

		case "ipv6":
			fromIP := ""
			toIP := ""
			if t.From.PublicIPv6 != nil {
				fromIP = t.From.PublicIPv6.String()
			}
			if t.To.PublicIPv6 != nil {
				toIP = t.To.PublicIPv6.String()
			}
			entry = LogEntry{
				Timestamp: t.To.Timestamp,
				Level:     LogInfo,
				Category:  CategoryState,
				Message:   fmt.Sprintf("ipv6: %s -> %s", fromIP, toIP),
				Transition: &TransitionLogData{
					Field:  "ipv6",
					From:   fromIP,
					To:     toIP,
					Source: t.Trigger,
				},
			}
		}

		if entry.Message != "" {
			ep.config.LogStreamer.Emit(entry)
		}
	}
}

// logToDatabase logs the transition to the database
func (ep *EffectsProcessor) logToDatabase(t StateTransition) {
	start := time.Now()

	// Log online state changes
	if t.HasChanged("online") {
		err := ep.config.DatabaseLogger.LogSensorChange(
			"online",
			"boolean",
			fmt.Sprintf("%v", t.From.Online),
			fmt.Sprintf("%v", t.To.Online),
		)
		ep.emitEffectLog("db_log", "online_change", err, time.Since(start))
	}

	// Log IPv4 changes
	if t.HasChanged("ipv4") {
		fromIP := ""
		toIP := ""
		if t.From.PublicIPv4 != nil {
			fromIP = t.From.PublicIPv4.String()
		}
		if t.To.PublicIPv4 != nil {
			toIP = t.To.PublicIPv4.String()
		}
		err := ep.config.DatabaseLogger.LogSensorChange(
			"public_ipv4",
			"string",
			fromIP,
			toIP,
		)
		ep.emitEffectLog("db_log", "ipv4_change", err, time.Since(start))
	}

	// Log IPv6 changes
	if t.HasChanged("ipv6") {
		fromIP := ""
		toIP := ""
		if t.From.PublicIPv6 != nil {
			fromIP = t.From.PublicIPv6.String()
		}
		if t.To.PublicIPv6 != nil {
			toIP = t.To.PublicIPv6.String()
		}
		err := ep.config.DatabaseLogger.LogSensorChange(
			"public_ipv6",
			"string",
			fromIP,
			toIP,
		)
		ep.emitEffectLog("db_log", "ipv6_change", err, time.Since(start))
	}

	// Log local IPv4 changes
	if t.HasChanged("local_ipv4") {
		fromIP := ""
		toIP := ""
		if t.From.LocalIPv4 != nil {
			fromIP = t.From.LocalIPv4.String()
		}
		if t.To.LocalIPv4 != nil {
			toIP = t.To.LocalIPv4.String()
		}
		err := ep.config.DatabaseLogger.LogSensorChange(
			"local_ipv4",
			"string",
			fromIP,
			toIP,
		)
		ep.emitEffectLog("db_log", "local_ipv4_change", err, time.Since(start))
	}

	// Log context/location changes
	if t.HasChanged("context") || t.HasChanged("location") {
		err := ep.config.DatabaseLogger.LogContextChange(
			t.From.Context,
			t.To.Context,
			t.From.Location,
			t.To.Location,
			t.Trigger,
		)

		ep.emitEffectLog("db_log", "context_change", err, time.Since(start))
	}
}

// writeEnvFiles writes to all configured environment files
func (ep *EffectsProcessor) writeEnvFiles(t StateTransition) {
	// Prepare export data
	publicIP := ""
	publicIPv4 := ""
	publicIPv6 := ""
	localIPv4 := ""

	if t.To.PublicIPv4 != nil {
		publicIPv4 = t.To.PublicIPv4.String()
	}
	if t.To.PublicIPv6 != nil {
		publicIPv6 = t.To.PublicIPv6.String()
	}
	if t.To.LocalIPv4 != nil {
		localIPv4 = t.To.LocalIPv4.String()
	}

	// Set preferred IP
	if ep.config.PreferredIP == "ipv6" && publicIPv6 != "" {
		publicIP = publicIPv6
	} else if publicIPv4 != "" {
		publicIP = publicIPv4
	} else {
		publicIP = publicIPv6
	}

	data := EnvExportData{
		Context:             t.To.Context,
		ContextDisplayName:  t.To.ContextDisplayName,
		Location:            t.To.Location,
		LocationDisplayName: t.To.LocationDisplayName,
		PublicIP:            publicIP,
		PublicIPv4:          publicIPv4,
		PublicIPv6:          publicIPv6,
		LocalIPv4:           localIPv4,
		CustomEnvironment:   t.To.Environment,
	}

	// Write to each writer
	for _, writer := range ep.config.EnvWriters {
		start := time.Now()
		err := writer.Write(data, ep.config.TrackedEnvVars)
		ep.emitEffectLog("env_write", writer.Path(), err, time.Since(start))

		if err != nil {
			ep.logger.Error("Failed to write env file",
				"writer", writer.Name(),
				"path", writer.Path(),
				"error", err)
		} else {
			ep.logger.Debug("Wrote env file",
				"writer", writer.Name(),
				"path", writer.Path())
		}
	}

	// Track the IPv4 that was actually written to env files.
	// This is read by the tunnel startup code to avoid a race where
	// in-memory state has the real IP but the env file still has 0.0.0.0.
	ep.lastWrittenIPv4.Store(publicIPv4)
}

// LastWrittenPublicIPv4 returns the IPv4 string most recently written to env files.
// Returns "" if no write has occurred yet.
func (ep *EffectsProcessor) LastWrittenPublicIPv4() string {
	v := ep.lastWrittenIPv4.Load()
	if v == nil {
		return ""
	}
	return v.(string)
}

// executeCallbacks runs the registered callbacks
func (ep *EffectsProcessor) executeCallbacks(t StateTransition) {
	// Online change callback
	if t.HasChanged("online") && ep.config.OnOnlineChange != nil {
		start := time.Now()
		ep.config.OnOnlineChange(t.From.Online, t.To.Online)
		ep.emitEffectLog("callback", "on_online_change", nil, time.Since(start))
	}

	// Context change callback
	if (t.HasChanged("context") || t.HasChanged("location")) && ep.config.OnContextChange != nil {
		start := time.Now()
		ep.config.OnContextChange(t.From, t.To)
		ep.emitEffectLog("callback", "on_context_change", nil, time.Since(start))
	}
}

// executeLeaveHooks runs leave hooks when location or context changes
func (ep *EffectsProcessor) executeLeaveHooks(t StateTransition) {
	// Build environment for hooks
	env := ep.buildHookEnv(t.From)

	// Location leave hooks (if location changed)
	// LIFO order: specific hooks first (inner), then global hooks (outer)
	if t.HasChanged("location") && t.From.Location != "" {
		// Specific location leave hooks first (inner unwinding)
		if hooks, ok := ep.locationHooks[t.From.Location]; ok && hooks != nil && len(hooks.OnLeave) > 0 {
			ep.hookExecutor.Execute(ep.ctx, HookEvent{
				Type:       "leave",
				TargetType: "location",
				TargetName: t.From.Location,
				Hooks:      hooks.OnLeave,
				Env:        env,
			})
		}
		// Global location leave hooks second (outer unwinding)
		if ep.globalLocationHooks != nil && len(ep.globalLocationHooks.OnLeave) > 0 {
			ep.hookExecutor.Execute(ep.ctx, HookEvent{
				Type:       "leave",
				TargetType: "location",
				TargetName: "*",
				Hooks:      ep.globalLocationHooks.OnLeave,
				Env:        env,
			})
		}
	}

	// Context leave hooks (if context changed)
	// LIFO order: specific hooks first (inner), then global hooks (outer)
	if t.HasChanged("context") && t.From.Context != "" {
		// Specific context leave hooks first (inner unwinding)
		if hooks, ok := ep.contextHooks[t.From.Context]; ok && hooks != nil && len(hooks.OnLeave) > 0 {
			ep.hookExecutor.Execute(ep.ctx, HookEvent{
				Type:       "leave",
				TargetType: "context",
				TargetName: t.From.Context,
				Hooks:      hooks.OnLeave,
				Env:        env,
			})
		}
		// Global context leave hooks second (outer unwinding)
		if ep.globalContextHooks != nil && len(ep.globalContextHooks.OnLeave) > 0 {
			ep.hookExecutor.Execute(ep.ctx, HookEvent{
				Type:       "leave",
				TargetType: "context",
				TargetName: "*",
				Hooks:      ep.globalContextHooks.OnLeave,
				Env:        env,
			})
		}
	}
}

// executeEnterHooks runs enter hooks when location or context changes
func (ep *EffectsProcessor) executeEnterHooks(t StateTransition) {
	// Build environment for hooks
	env := ep.buildHookEnv(t.To)

	// Location enter hooks (if location changed)
	if t.HasChanged("location") && t.To.Location != "" {
		// Global location enter hooks first
		if ep.globalLocationHooks != nil && len(ep.globalLocationHooks.OnEnter) > 0 {
			ep.hookExecutor.Execute(ep.ctx, HookEvent{
				Type:       "enter",
				TargetType: "location",
				TargetName: "*",
				Hooks:      ep.globalLocationHooks.OnEnter,
				Env:        env,
			})
		}
		// Specific location enter hooks second
		if hooks, ok := ep.locationHooks[t.To.Location]; ok && hooks != nil && len(hooks.OnEnter) > 0 {
			ep.hookExecutor.Execute(ep.ctx, HookEvent{
				Type:       "enter",
				TargetType: "location",
				TargetName: t.To.Location,
				Hooks:      hooks.OnEnter,
				Env:        env,
			})
		}
	}

	// Context enter hooks (if context changed)
	if t.HasChanged("context") && t.To.Context != "" {
		// Global context enter hooks first
		if ep.globalContextHooks != nil && len(ep.globalContextHooks.OnEnter) > 0 {
			ep.hookExecutor.Execute(ep.ctx, HookEvent{
				Type:       "enter",
				TargetType: "context",
				TargetName: "*",
				Hooks:      ep.globalContextHooks.OnEnter,
				Env:        env,
			})
		}
		// Specific context enter hooks second
		if hooks, ok := ep.contextHooks[t.To.Context]; ok && hooks != nil && len(hooks.OnEnter) > 0 {
			ep.hookExecutor.Execute(ep.ctx, HookEvent{
				Type:       "enter",
				TargetType: "context",
				TargetName: t.To.Context,
				Hooks:      hooks.OnEnter,
				Env:        env,
			})
		}
	}
}

// buildHookEnv creates the environment map for hook execution
func (ep *EffectsProcessor) buildHookEnv(state StateSnapshot) map[string]string {
	env := make(map[string]string)

	// Add standard OVERSEER_ variables
	env["OVERSEER_CONTEXT"] = state.Context
	env["OVERSEER_LOCATION"] = state.Location

	if state.PublicIPv4 != nil {
		env["OVERSEER_PUBLIC_IP"] = state.PublicIPv4.String()
		env["OVERSEER_PUBLIC_IPV4"] = state.PublicIPv4.String()
	}
	if state.PublicIPv6 != nil {
		env["OVERSEER_PUBLIC_IPV6"] = state.PublicIPv6.String()
	}
	if state.LocalIPv4 != nil {
		env["OVERSEER_LOCAL_IP"] = state.LocalIPv4.String()
		env["OVERSEER_LOCAL_IPV4"] = state.LocalIPv4.String()
	}

	// Add custom environment from state
	for k, v := range state.Environment {
		env[k] = v
	}

	return env
}

// emitEffectLog emits a log entry for an effect execution
func (ep *EffectsProcessor) emitEffectLog(effectName, target string, err error, duration time.Duration) {
	if ep.config.LogStreamer == nil {
		return
	}

	level := LogDebug
	errStr := ""
	if err != nil {
		level = LogError
		errStr = err.Error()
	}

	ep.config.LogStreamer.Emit(LogEntry{
		Timestamp: time.Now(),
		Level:     level,
		Category:  CategoryEffect,
		Message:   fmt.Sprintf("%s: %s", effectName, target),
		Effect: &EffectLogData{
			Name:     effectName,
			Target:   target,
			Success:  err == nil,
			Duration: duration,
			Error:    errStr,
		},
	})
}

// DotenvWriter writes environment exports in shell-sourceable format
type DotenvWriter struct {
	path string
}

// NewDotenvWriter creates a new dotenv writer
func NewDotenvWriter(path string) (*DotenvWriter, error) {
	// Resolve path (handle ~)
	if path[0] == '~' {
		home, err := os.UserHomeDir()
		if err != nil {
			return nil, err
		}
		path = filepath.Join(home, path[1:])
	}

	absPath, err := filepath.Abs(path)
	if err != nil {
		return nil, err
	}

	// Ensure parent directory exists
	dir := filepath.Dir(absPath)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, fmt.Errorf("failed to create directory: %w", err)
	}

	return &DotenvWriter{path: absPath}, nil
}

func (w *DotenvWriter) Name() string { return "dotenv" }
func (w *DotenvWriter) Path() string { return w.path }

func (w *DotenvWriter) Write(data EnvExportData, trackedVars []string) error {
	// Step 1: Collect environment variables we're going to set
	envVars := make(map[string]string)

	// Add OVERSEER_ prefixed variables
	if data.Context != "" {
		envVars["OVERSEER_CONTEXT"] = data.Context
	}
	if data.ContextDisplayName != "" {
		envVars["OVERSEER_CONTEXT_DISPLAY_NAME"] = data.ContextDisplayName
	}
	if data.Location != "" {
		envVars["OVERSEER_LOCATION"] = data.Location
	}
	if data.LocationDisplayName != "" {
		envVars["OVERSEER_LOCATION_DISPLAY_NAME"] = data.LocationDisplayName
	}
	if data.PublicIP != "" {
		envVars["OVERSEER_PUBLIC_IP"] = data.PublicIP
	}
	if data.PublicIPv4 != "" {
		envVars["OVERSEER_PUBLIC_IPV4"] = data.PublicIPv4
	}
	if data.PublicIPv6 != "" {
		envVars["OVERSEER_PUBLIC_IPV6"] = data.PublicIPv6
	}
	if data.LocalIPv4 != "" {
		envVars["OVERSEER_LOCAL_IP"] = data.LocalIPv4
		envVars["OVERSEER_LOCAL_IPV4"] = data.LocalIPv4
	}

	// Add custom environment variables
	for key, value := range data.CustomEnvironment {
		envVars[key] = value
	}

	// Step 2: Find tracked vars that we're NOT setting (need to unset)
	var varsToUnset []string
	for _, v := range trackedVars {
		if _, willSet := envVars[v]; !willSet {
			varsToUnset = append(varsToUnset, v)
		}
	}
	sort.Strings(varsToUnset)

	// Step 3: Build output
	var lines []string

	if len(varsToUnset) > 0 {
		lines = append(lines, "# Unset variables not set in current context")
		lines = append(lines, fmt.Sprintf("unset %s", strings.Join(varsToUnset, " ")))
		lines = append(lines, "")
	}

	// Sort keys alphabetically for export
	keys := make([]string, 0, len(envVars))
	for key := range envVars {
		keys = append(keys, key)
	}
	sort.Strings(keys)

	// Build sorted export lines
	for _, key := range keys {
		lines = append(lines, fmt.Sprintf("export %s=\"%s\"", key, envVars[key]))
	}

	content := strings.Join(lines, "\n") + "\n"

	// Atomic write
	tempFile := w.path + ".tmp"
	if err := os.WriteFile(tempFile, []byte(content), 0o644); err != nil {
		return fmt.Errorf("failed to write temp file: %w", err)
	}

	if err := os.Rename(tempFile, w.path); err != nil {
		os.Remove(tempFile)
		return fmt.Errorf("failed to rename temp file: %w", err)
	}

	return nil
}

// ContextWriter writes just the context name
type ContextWriter struct {
	path string
}

func NewContextWriter(path string) (*ContextWriter, error) {
	if path[0] == '~' {
		home, err := os.UserHomeDir()
		if err != nil {
			return nil, err
		}
		path = filepath.Join(home, path[1:])
	}

	absPath, err := filepath.Abs(path)
	if err != nil {
		return nil, err
	}

	dir := filepath.Dir(absPath)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, fmt.Errorf("failed to create directory: %w", err)
	}

	return &ContextWriter{path: absPath}, nil
}

func (w *ContextWriter) Name() string { return "context" }
func (w *ContextWriter) Path() string { return w.path }

func (w *ContextWriter) Write(data EnvExportData, _ []string) error {
	tempFile := w.path + ".tmp"
	if err := os.WriteFile(tempFile, []byte(data.Context+"\n"), 0o644); err != nil {
		return err
	}
	if err := os.Rename(tempFile, w.path); err != nil {
		os.Remove(tempFile)
		return err
	}
	return nil
}

// LocationWriter writes just the location name
type LocationWriter struct {
	path string
}

func NewLocationWriter(path string) (*LocationWriter, error) {
	if path[0] == '~' {
		home, err := os.UserHomeDir()
		if err != nil {
			return nil, err
		}
		path = filepath.Join(home, path[1:])
	}

	absPath, err := filepath.Abs(path)
	if err != nil {
		return nil, err
	}

	dir := filepath.Dir(absPath)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, fmt.Errorf("failed to create directory: %w", err)
	}

	return &LocationWriter{path: absPath}, nil
}

func (w *LocationWriter) Name() string { return "location" }
func (w *LocationWriter) Path() string { return w.path }

func (w *LocationWriter) Write(data EnvExportData, _ []string) error {
	tempFile := w.path + ".tmp"
	if err := os.WriteFile(tempFile, []byte(data.Location+"\n"), 0o644); err != nil {
		return err
	}
	if err := os.Rename(tempFile, w.path); err != nil {
		os.Remove(tempFile)
		return err
	}
	return nil
}

// PublicIPWriter writes just the public IP
type PublicIPWriter struct {
	path string
}

func NewPublicIPWriter(path string) (*PublicIPWriter, error) {
	if path[0] == '~' {
		home, err := os.UserHomeDir()
		if err != nil {
			return nil, err
		}
		path = filepath.Join(home, path[1:])
	}

	absPath, err := filepath.Abs(path)
	if err != nil {
		return nil, err
	}

	dir := filepath.Dir(absPath)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, fmt.Errorf("failed to create directory: %w", err)
	}

	return &PublicIPWriter{path: absPath}, nil
}

func (w *PublicIPWriter) Name() string { return "public_ip" }
func (w *PublicIPWriter) Path() string { return w.path }

func (w *PublicIPWriter) Write(data EnvExportData, _ []string) error {
	tempFile := w.path + ".tmp"
	if err := os.WriteFile(tempFile, []byte(data.PublicIP+"\n"), 0o644); err != nil {
		return err
	}
	if err := os.Rename(tempFile, w.path); err != nil {
		os.Remove(tempFile)
		return err
	}
	return nil
}
