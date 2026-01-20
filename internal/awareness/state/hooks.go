package state

import (
	"bytes"
	"context"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"time"
)

const (
	// MaxHookOutput is the maximum number of bytes to capture from hook output
	MaxHookOutput = 4096
)

// HookEvent represents a hook execution request
type HookEvent struct {
	Type       string            // "enter" or "leave"
	TargetType string            // "location" or "context"
	TargetName string            // Name of the location or context
	Hooks      []HookConfig      // Hooks to execute
	Env        map[string]string // Environment variables to pass to hooks
}

// HookExecutor executes hook scripts for location and context transitions
type HookExecutor struct {
	logger   *slog.Logger
	streamer *LogStreamer
	logEvent func(identifier, eventType, details string) error
}

// NewHookExecutor creates a new hook executor
func NewHookExecutor(logger *slog.Logger, streamer *LogStreamer) *HookExecutor {
	if logger == nil {
		logger = slog.Default()
	}
	return &HookExecutor{
		logger:   logger,
		streamer: streamer,
	}
}

// SetEventLogger sets the callback function for logging hook events to the database
func (he *HookExecutor) SetEventLogger(logger func(identifier, eventType, details string) error) {
	he.logEvent = logger
}

// Execute runs all hooks in the event
// Hooks are fire-and-forget - they do NOT block state transitions
func (he *HookExecutor) Execute(ctx context.Context, event HookEvent) {
	for _, hook := range event.Hooks {
		he.executeHook(ctx, event, hook)
	}
}

// executeHook runs a single hook command
func (he *HookExecutor) executeHook(ctx context.Context, event HookEvent, hook HookConfig) {
	startTime := time.Now()

	// Apply timeout
	timeout := hook.Timeout
	if timeout == 0 {
		timeout = 30 * time.Second
	}
	hookCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	// Build environment
	env := he.buildEnvironment(event)

	// Create command via shell
	cmd := exec.CommandContext(hookCtx, "sh", "-c", hook.Command)
	cmd.Env = env

	// Set up process group for clean termination
	cmd.SysProcAttr = &syscall.SysProcAttr{
		Setpgid: true,
	}

	// Capture combined stdout/stderr
	var output bytes.Buffer
	cmd.Stdout = &output
	cmd.Stderr = &output

	// Expand ~ in command if needed (for logging purposes)
	displayCmd := hook.Command

	he.logger.Debug("Executing hook",
		"type", event.Type,
		"target_type", event.TargetType,
		"target", event.TargetName,
		"command", displayCmd)

	// Run the command
	err := cmd.Run()
	duration := time.Since(startTime)

	// Truncate output if needed
	outputStr := output.String()
	if len(outputStr) > MaxHookOutput {
		outputStr = outputStr[:MaxHookOutput] + "\n... (truncated)"
	}
	outputStr = strings.TrimSpace(outputStr)

	// Determine success and error message
	success := err == nil
	errStr := ""
	level := LogInfo

	if err != nil {
		success = false
		if hookCtx.Err() == context.DeadlineExceeded {
			errStr = fmt.Sprintf("timeout after %s", timeout)
			level = LogWarn
			// Kill the process group
			if cmd.Process != nil {
				syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
			}
		} else if exitErr, ok := err.(*exec.ExitError); ok {
			errStr = fmt.Sprintf("exit code %d", exitErr.ExitCode())
			level = LogWarn
		} else {
			errStr = err.Error()
			level = LogError
		}
	}

	// Log the result
	he.logger.Log(context.Background(), slogLevel(level), "Hook executed",
		"type", event.Type,
		"target_type", event.TargetType,
		"target", event.TargetName,
		"command", displayCmd,
		"success", success,
		"duration", duration,
		"error", errStr)

	// Emit to log stream
	if he.streamer != nil {
		he.streamer.Emit(LogEntry{
			Timestamp: time.Now(),
			Level:     level,
			Category:  CategoryHook,
			Message:   fmt.Sprintf("%s %s: %s", event.Type, event.TargetType, event.TargetName),
			Hook: &HookLogData{
				Type:       event.Type,
				Target:     event.TargetName,
				TargetType: event.TargetType,
				Command:    displayCmd,
				Success:    success,
				Duration:   duration,
				Output:     outputStr,
				Error:      errStr,
			},
		})
	}

	// Log to database for status display
	if he.logEvent != nil {
		identifier := fmt.Sprintf("%s:%s:%s", event.Type, event.TargetType, event.TargetName)
		eventType := "hook_executed"

		// Extract script base name from command (first word)
		scriptName := hook.Command
		if fields := strings.Fields(hook.Command); len(fields) > 0 {
			scriptName = filepath.Base(fields[0])
		}

		details := fmt.Sprintf("%s - duration: %s", scriptName, duration)
		if !success {
			if hookCtx.Err() == context.DeadlineExceeded {
				eventType = "hook_timeout"
			} else {
				eventType = "hook_failed"
			}
			details = fmt.Sprintf("%s - %s", scriptName, errStr)
		}
		if err := he.logEvent(identifier, eventType, details); err != nil {
			he.logger.Warn("Failed to log hook event", "error", err)
		}
	}
}

// buildEnvironment creates the environment variables for hook execution
func (he *HookExecutor) buildEnvironment(event HookEvent) []string {
	// Start with current process environment
	env := os.Environ()

	// Add hook-specific variables
	hookEnv := map[string]string{
		"OVERSEER_HOOK_TYPE":        event.Type,
		"OVERSEER_HOOK_TARGET_TYPE": event.TargetType,
		"OVERSEER_HOOK_TARGET":      event.TargetName,
	}

	// Add custom environment from context/location
	for k, v := range event.Env {
		hookEnv[k] = v
	}

	// Convert to slice
	for k, v := range hookEnv {
		env = append(env, fmt.Sprintf("%s=%s", k, v))
	}

	return env
}

// slogLevel converts LogLevel to slog.Level
func slogLevel(level LogLevel) slog.Level {
	switch level {
	case LogDebug:
		return slog.LevelDebug
	case LogInfo:
		return slog.LevelInfo
	case LogWarn:
		return slog.LevelWarn
	case LogError:
		return slog.LevelError
	default:
		return slog.LevelInfo
	}
}
