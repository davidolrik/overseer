package daemon

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"go.olrik.dev/overseer/internal/core"
)

func TestExecuteSingleTunnelHook_Success(t *testing.T) {
	quietLogger(t)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	d := &Daemon{ctx: ctx}

	hook := core.HookConfig{
		Command: "true",
		Timeout: 5 * time.Second,
	}

	// Should complete without panic or error (no database, so logging is skipped)
	d.executeSingleTunnelHook("test-alias", "on_connect", hook, StateConnected)
}

func TestExecuteSingleTunnelHook_Failure(t *testing.T) {
	quietLogger(t)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	d := &Daemon{ctx: ctx}

	hook := core.HookConfig{
		Command: "false",
		Timeout: 5 * time.Second,
	}

	// Should complete without panic — failure is logged, not returned
	d.executeSingleTunnelHook("test-alias", "on_connect", hook, StateConnected)
}

func TestExecuteSingleTunnelHook_Timeout(t *testing.T) {
	quietLogger(t)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	d := &Daemon{ctx: ctx}

	hook := core.HookConfig{
		Command: "sleep 10",
		Timeout: 100 * time.Millisecond,
	}

	start := time.Now()
	d.executeSingleTunnelHook("test-alias", "on_connect", hook, StateConnected)
	elapsed := time.Since(start)

	if elapsed > 3*time.Second {
		t.Errorf("hook should have timed out quickly, took %s", elapsed)
	}
}

func TestExecuteSingleTunnelHook_Environment(t *testing.T) {
	quietLogger(t)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	d := &Daemon{ctx: ctx}

	// Write env vars to a temp file so we can inspect them
	tmpDir := t.TempDir()
	envFile := filepath.Join(tmpDir, "env.txt")

	hook := core.HookConfig{
		Command: "env > " + envFile,
		Timeout: 5 * time.Second,
	}

	d.executeSingleTunnelHook("my-tunnel", "on_connect", hook, StateConnected)

	data, err := os.ReadFile(envFile)
	if err != nil {
		t.Fatalf("failed to read env file: %v", err)
	}
	envStr := string(data)

	expectedVars := map[string]string{
		"OVERSEER_HOOK_TYPE":        "on_connect",
		"OVERSEER_HOOK_TARGET_TYPE": "tunnel",
		"OVERSEER_HOOK_TARGET":      "my-tunnel",
		"OVERSEER_TUNNEL_ALIAS":     "my-tunnel",
		"OVERSEER_TUNNEL_STATE":     "connected",
	}

	for k, v := range expectedVars {
		expected := k + "=" + v
		if !contains(envStr, expected) {
			t.Errorf("expected env var %s not found in output", expected)
		}
	}
}

func TestExecuteSingleTunnelHook_DefaultTimeout(t *testing.T) {
	quietLogger(t)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	d := &Daemon{ctx: ctx}

	// Timeout=0 should use default 30s, not block forever
	hook := core.HookConfig{
		Command: "true",
		Timeout: 0,
	}

	d.executeSingleTunnelHook("test-alias", "on_connect", hook, StateConnected)
}

func TestExecuteTunnelHooks_Multiple(t *testing.T) {
	quietLogger(t)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	d := &Daemon{ctx: ctx}

	tmpDir := t.TempDir()
	marker1 := filepath.Join(tmpDir, "hook1.done")
	marker2 := filepath.Join(tmpDir, "hook2.done")

	hooks := []core.HookConfig{
		{Command: "touch " + marker1, Timeout: 5 * time.Second},
		{Command: "touch " + marker2, Timeout: 5 * time.Second},
	}

	d.executeTunnelHooks("test-alias", "on_connect", hooks, StateConnected)

	// Hooks are dispatched as goroutines; wait for them to complete
	deadline := time.After(5 * time.Second)
	for {
		_, err1 := os.Stat(marker1)
		_, err2 := os.Stat(marker2)
		if err1 == nil && err2 == nil {
			break
		}
		select {
		case <-deadline:
			t.Fatal("timed out waiting for hook marker files")
		default:
			time.Sleep(50 * time.Millisecond)
		}
	}
}

func TestExecuteTunnelHooks_NoHooksDoesNotPanic(t *testing.T) {
	quietLogger(t)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	d := &Daemon{ctx: ctx}

	// Should return immediately without panic
	d.executeTunnelHooks("test-alias", "on_connect", nil, StateConnected)
	d.executeTunnelHooks("test-alias", "on_connect", []core.HookConfig{}, StateConnected)
}

// contains checks if s contains substr
func contains(s, substr string) bool {
	return len(s) >= len(substr) && strings.Contains(s, substr)
}
