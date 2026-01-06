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

	return &EffectsProcessor{
		config:      config,
		logger:      config.Logger,
		transitions: transitions,
		ctx:         ctx,
		cancel:      cancel,
	}
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

	// 1. Emit log entries for state changes
	ep.emitTransitionLogs(t)

	// 2. Log to database
	if ep.config.DatabaseLogger != nil {
		ep.logToDatabase(t)
	}

	// 3. Write environment files
	if len(ep.config.EnvWriters) > 0 {
		ep.writeEnvFiles(t)
	}

	// 4. Execute callbacks
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

func (w *DotenvWriter) Write(data EnvExportData, varsToUnset []string) error {
	var lines []string

	// Step 1: Unset all tracked variables first
	if len(varsToUnset) > 0 {
		lines = append(lines, "# Unset all tracked variables from contexts/locations")
		lines = append(lines, fmt.Sprintf("unset %s", strings.Join(varsToUnset, " ")))
		lines = append(lines, "")
	}

	// Step 2: Collect and export current context's environment variables
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

	// Sort keys alphabetically
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
