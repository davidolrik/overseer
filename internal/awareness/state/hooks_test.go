package state

import (
	"context"
	"log/slog"
	"os"
	"strings"
	"testing"
	"time"
)

func TestNewHookExecutor(t *testing.T) {
	t.Run("nil logger uses default", func(t *testing.T) {
		he := NewHookExecutor(nil, nil)
		if he == nil {
			t.Fatal("expected non-nil HookExecutor")
		}
		if he.logger == nil {
			t.Error("expected logger to be set to default")
		}
	})

	t.Run("with logger and streamer", func(t *testing.T) {
		logger := slog.New(slog.NewTextHandler(os.Stderr, nil))
		streamer := NewLogStreamer(10)
		he := NewHookExecutor(logger, streamer)
		if he.logger != logger {
			t.Error("expected custom logger")
		}
		if he.streamer != streamer {
			t.Error("expected custom streamer")
		}
	})
}

func TestHookExecutor_ExecuteSuccess(t *testing.T) {
	streamer := NewLogStreamer(10)
	he := NewHookExecutor(
		slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError})),
		streamer,
	)

	_, ch := streamer.Subscribe(false)

	he.Execute(context.Background(), HookEvent{
		Type:       "enter",
		TargetType: "location",
		TargetName: "home",
		Hooks: []HookConfig{
			{Command: "echo hello"},
		},
	})

	select {
	case entry := <-ch:
		if entry.Category != CategoryHook {
			t.Errorf("expected CategoryHook, got %v", entry.Category)
		}
		if entry.Hook == nil {
			t.Fatal("expected hook log data")
		}
		if !entry.Hook.Success {
			t.Error("expected hook to succeed")
		}
		if !strings.Contains(entry.Hook.Output, "hello") {
			t.Errorf("expected output containing 'hello', got %q", entry.Hook.Output)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for hook log")
	}
}

func TestHookExecutor_ExecuteFailure(t *testing.T) {
	streamer := NewLogStreamer(10)
	he := NewHookExecutor(
		slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError})),
		streamer,
	)

	_, ch := streamer.Subscribe(false)

	he.Execute(context.Background(), HookEvent{
		Type:       "leave",
		TargetType: "context",
		TargetName: "office",
		Hooks: []HookConfig{
			{Command: "exit 1"},
		},
	})

	select {
	case entry := <-ch:
		if entry.Hook == nil {
			t.Fatal("expected hook log data")
		}
		if entry.Hook.Success {
			t.Error("expected hook to fail")
		}
		if !strings.Contains(entry.Hook.Error, "exit code 1") {
			t.Errorf("expected exit code 1 error, got %q", entry.Hook.Error)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for hook log")
	}
}

func TestHookExecutor_ExecuteTimeout(t *testing.T) {
	streamer := NewLogStreamer(10)
	he := NewHookExecutor(
		slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError})),
		streamer,
	)

	_, ch := streamer.Subscribe(false)

	he.Execute(context.Background(), HookEvent{
		Type:       "enter",
		TargetType: "location",
		TargetName: "slow",
		Hooks: []HookConfig{
			{Command: "sleep 10", Timeout: 100 * time.Millisecond},
		},
	})

	select {
	case entry := <-ch:
		if entry.Hook == nil {
			t.Fatal("expected hook log data")
		}
		if entry.Hook.Success {
			t.Error("expected hook to fail on timeout")
		}
		if !strings.Contains(entry.Hook.Error, "timeout") {
			t.Errorf("expected timeout error, got %q", entry.Hook.Error)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for hook log")
	}
}

func TestHookExecutor_BuildEnvironment(t *testing.T) {
	he := NewHookExecutor(
		slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError})),
		nil,
	)

	event := HookEvent{
		Type:       "enter",
		TargetType: "location",
		TargetName: "office",
		Env: map[string]string{
			"CUSTOM_VAR": "custom_value",
		},
	}

	env := he.buildEnvironment(event)

	found := map[string]string{}
	for _, entry := range env {
		parts := strings.SplitN(entry, "=", 2)
		if len(parts) == 2 {
			found[parts[0]] = parts[1]
		}
	}

	if found["OVERSEER_HOOK_TYPE"] != "enter" {
		t.Errorf("expected OVERSEER_HOOK_TYPE=enter, got %q", found["OVERSEER_HOOK_TYPE"])
	}
	if found["OVERSEER_HOOK_TARGET_TYPE"] != "location" {
		t.Errorf("expected OVERSEER_HOOK_TARGET_TYPE=location, got %q", found["OVERSEER_HOOK_TARGET_TYPE"])
	}
	if found["OVERSEER_HOOK_TARGET"] != "office" {
		t.Errorf("expected OVERSEER_HOOK_TARGET=office, got %q", found["OVERSEER_HOOK_TARGET"])
	}
	if found["CUSTOM_VAR"] != "custom_value" {
		t.Errorf("expected CUSTOM_VAR=custom_value, got %q", found["CUSTOM_VAR"])
	}
}

func TestHookExecutor_SetEventLogger(t *testing.T) {
	he := NewHookExecutor(
		slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError})),
		nil,
	)

	var logged []string
	he.SetEventLogger(func(identifier, eventType, details string) error {
		logged = append(logged, eventType)
		return nil
	})

	he.Execute(context.Background(), HookEvent{
		Type:       "enter",
		TargetType: "location",
		TargetName: "test",
		Hooks: []HookConfig{
			{Command: "echo ok"},
		},
	})

	if len(logged) != 1 {
		t.Fatalf("expected 1 logged event, got %d", len(logged))
	}
	if logged[0] != "hook_executed" {
		t.Errorf("expected event type 'hook_executed', got %q", logged[0])
	}
}

func TestHookExecutor_EventLoggerFailure(t *testing.T) {
	he := NewHookExecutor(
		slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError})),
		nil,
	)

	var logged []string
	he.SetEventLogger(func(identifier, eventType, details string) error {
		logged = append(logged, eventType)
		return nil
	})

	he.Execute(context.Background(), HookEvent{
		Type:       "leave",
		TargetType: "context",
		TargetName: "test",
		Hooks: []HookConfig{
			{Command: "exit 42"},
		},
	})

	if len(logged) != 1 {
		t.Fatalf("expected 1 logged event, got %d", len(logged))
	}
	if logged[0] != "hook_failed" {
		t.Errorf("expected event type 'hook_failed', got %q", logged[0])
	}
}

func TestHookExecutor_EventLoggerTimeout(t *testing.T) {
	he := NewHookExecutor(
		slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError})),
		nil,
	)

	var logged []string
	he.SetEventLogger(func(identifier, eventType, details string) error {
		logged = append(logged, eventType)
		return nil
	})

	he.Execute(context.Background(), HookEvent{
		Type:       "enter",
		TargetType: "location",
		TargetName: "test",
		Hooks: []HookConfig{
			{Command: "sleep 10", Timeout: 100 * time.Millisecond},
		},
	})

	if len(logged) != 1 {
		t.Fatalf("expected 1 logged event, got %d", len(logged))
	}
	if logged[0] != "hook_timeout" {
		t.Errorf("expected event type 'hook_timeout', got %q", logged[0])
	}
}

func TestSlogLevel(t *testing.T) {
	tests := []struct {
		level    LogLevel
		expected slog.Level
	}{
		{LogDebug, slog.LevelDebug},
		{LogInfo, slog.LevelInfo},
		{LogWarn, slog.LevelWarn},
		{LogError, slog.LevelError},
		{LogLevel(99), slog.LevelInfo}, // unknown defaults to Info
	}

	for _, tt := range tests {
		t.Run(tt.level.String(), func(t *testing.T) {
			if got := slogLevel(tt.level); got != tt.expected {
				t.Errorf("slogLevel(%v) = %v, want %v", tt.level, got, tt.expected)
			}
		})
	}
}

func TestHookExecutor_OutputTruncation(t *testing.T) {
	streamer := NewLogStreamer(10)
	he := NewHookExecutor(
		slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError})),
		streamer,
	)

	_, ch := streamer.Subscribe(false)

	// Generate output larger than MaxHookOutput (4096 bytes)
	// Using yes | head -c to generate exactly 5000 bytes
	he.Execute(context.Background(), HookEvent{
		Type:       "enter",
		TargetType: "location",
		TargetName: "test",
		Hooks: []HookConfig{
			{Command: "head -c 5000 /dev/zero | tr '\\0' 'A'"},
		},
	})

	select {
	case entry := <-ch:
		if entry.Hook == nil {
			t.Fatal("expected hook log data")
		}
		if !strings.Contains(entry.Hook.Output, "truncated") {
			t.Error("expected output to be truncated")
		}
		// The output before truncation marker should be at most MaxHookOutput
		idx := strings.Index(entry.Hook.Output, "\n... (truncated)")
		if idx > MaxHookOutput {
			t.Errorf("output before truncation marker is %d bytes, expected <= %d", idx, MaxHookOutput)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for hook log")
	}
}

func TestHookExecutor_MultipleHooksInEvent(t *testing.T) {
	streamer := NewLogStreamer(10)
	he := NewHookExecutor(
		slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError})),
		streamer,
	)

	_, ch := streamer.Subscribe(false)

	he.Execute(context.Background(), HookEvent{
		Type:       "enter",
		TargetType: "location",
		TargetName: "test",
		Hooks: []HookConfig{
			{Command: "echo first"},
			{Command: "echo second"},
			{Command: "echo third"},
		},
	})

	count := 0
	timeout := time.After(5 * time.Second)
	for count < 3 {
		select {
		case entry := <-ch:
			if entry.Hook != nil && entry.Hook.Success {
				count++
			}
		case <-timeout:
			t.Fatalf("timed out after receiving %d hook logs, expected 3", count)
		}
	}
}

func TestHookExecutor_DefaultTimeout(t *testing.T) {
	// Verify that a hook with Timeout=0 gets the default 30s timeout
	// We test this indirectly: a fast command should succeed with default timeout
	he := NewHookExecutor(
		slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError})),
		nil,
	)

	var logged []string
	he.SetEventLogger(func(identifier, eventType, details string) error {
		logged = append(logged, eventType)
		return nil
	})

	he.Execute(context.Background(), HookEvent{
		Type:       "enter",
		TargetType: "location",
		TargetName: "test",
		Hooks: []HookConfig{
			{Command: "true", Timeout: 0}, // 0 means use default (30s)
		},
	})

	if len(logged) != 1 || logged[0] != "hook_executed" {
		t.Errorf("expected successful execution with default timeout, got %v", logged)
	}
}
