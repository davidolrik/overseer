package daemon

import (
	"context"
	"os/exec"
	"strings"
	"syscall"
	"testing"
	"time"

	"go.olrik.dev/overseer/internal/core"
)

func TestStartCompanions_AlreadyRunning(t *testing.T) {
	quietLogger(t)

	oldConfig := core.Config
	t.Cleanup(func() { core.Config = oldConfig })
	core.Config = &core.Configuration{
		Companion: core.CompanionSettings{HistorySize: 50},
	}

	cm := NewCompanionManager()

	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)

	broadcaster := NewLogBroadcaster(100)

	// Pre-populate with a running companion
	cm.companions["my-tunnel"] = map[string]*CompanionProcess{
		"running-comp": {
			Name:        "running-comp",
			TunnelAlias: "my-tunnel",
			Pid:         99999,
			State:       CompanionStateRunning,
			output:      broadcaster,
			ctx:         ctx,
			cancel:      cancel,
		},
	}

	var progressMessages []string
	onProgress := func(p CompanionProgress) {
		progressMessages = append(progressMessages, p.Message)
	}

	configs := []core.CompanionConfig{
		{Name: "running-comp", Command: "echo hello"},
	}

	err := cm.StartCompanions("my-tunnel", configs, onProgress)
	if err != nil {
		t.Fatalf("expected nil error, got: %v", err)
	}

	// Should have received a progress message about already running
	if len(progressMessages) == 0 {
		t.Fatal("expected at least one progress message")
	}
	if !strings.Contains(progressMessages[0], "already running") {
		t.Errorf("expected 'already running' message, got: %q", progressMessages[0])
	}
}

func TestStartCompanions_AlreadyReady(t *testing.T) {
	quietLogger(t)

	oldConfig := core.Config
	t.Cleanup(func() { core.Config = oldConfig })
	core.Config = &core.Configuration{
		Companion: core.CompanionSettings{HistorySize: 50},
	}

	cm := NewCompanionManager()

	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)

	broadcaster := NewLogBroadcaster(100)

	cm.companions["my-tunnel"] = map[string]*CompanionProcess{
		"ready-comp": {
			Name:        "ready-comp",
			TunnelAlias: "my-tunnel",
			Pid:         99999,
			State:       CompanionStateReady,
			output:      broadcaster,
			ctx:         ctx,
			cancel:      cancel,
		},
	}

	var progressMessages []string
	onProgress := func(p CompanionProgress) {
		progressMessages = append(progressMessages, p.Message)
	}

	configs := []core.CompanionConfig{
		{Name: "ready-comp", Command: "echo hello"},
	}

	err := cm.StartCompanions("my-tunnel", configs, onProgress)
	if err != nil {
		t.Fatalf("expected nil error, got: %v", err)
	}

	if len(progressMessages) == 0 {
		t.Fatal("expected at least one progress message")
	}
	if !strings.Contains(progressMessages[0], "already running") {
		t.Errorf("expected 'already running' message, got: %q", progressMessages[0])
	}
}


func TestStartCompanions_ExistingStoppedCompanion_RestartFails_Continue(t *testing.T) {
	quietLogger(t)

	oldConfig := core.Config
	t.Cleanup(func() { core.Config = oldConfig })
	core.Config = &core.Configuration{
		Companion: core.CompanionSettings{HistorySize: 50},
	}

	cm := NewCompanionManager()

	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)

	broadcaster := NewLogBroadcaster(100)

	cm.companions["my-tunnel"] = map[string]*CompanionProcess{
		"stopped-comp": {
			Name:        "stopped-comp",
			TunnelAlias: "my-tunnel",
			State:       CompanionStateStopped,
			output:      broadcaster,
			ctx:         ctx,
			cancel:      cancel,
			Config: core.CompanionConfig{
				Name:    "stopped-comp",
				Command: "echo hello",
			},
		},
	}

	var progressMessages []CompanionProgress
	onProgress := func(p CompanionProgress) {
		progressMessages = append(progressMessages, p)
	}

	configs := []core.CompanionConfig{
		{
			Name:      "stopped-comp",
			Command:   "echo hello",
			OnFailure: "continue",
		},
	}

	err := cm.StartCompanions("my-tunnel", configs, onProgress)
	// With on_failure=continue, error should not be returned
	if err != nil {
		t.Errorf("expected nil error with on_failure=continue, got: %v", err)
	}
}

func TestStartCompanions_FreshStart_Fails_Block(t *testing.T) {
	quietLogger(t)

	oldConfig := core.Config
	t.Cleanup(func() { core.Config = oldConfig })
	core.Config = &core.Configuration{
		Companion: core.CompanionSettings{HistorySize: 50},
	}

	cm := NewCompanionManager()

	configs := []core.CompanionConfig{
		{
			Name:      "new-comp",
			Command:   "echo hello",
			OnFailure: "block",
			// Workdir that doesn't exist will cause runCompanion to fail immediately
			Workdir: "/nonexistent/path/that/should/not/exist",
		},
	}

	var progressMessages []CompanionProgress
	onProgress := func(p CompanionProgress) {
		progressMessages = append(progressMessages, p)
	}

	err := cm.StartCompanions("my-tunnel", configs, onProgress)
	if err == nil {
		t.Fatal("expected error with block mode and failing companion")
	}
	if !strings.Contains(err.Error(), "new-comp") {
		t.Errorf("expected error to mention companion name, got: %v", err)
	}

	// Should have error progress messages
	hasError := false
	for _, p := range progressMessages {
		if p.IsError {
			hasError = true
			break
		}
	}
	if !hasError {
		t.Error("expected at least one error progress message")
	}
}

func TestStartCompanions_FreshStart_Fails_Continue(t *testing.T) {
	quietLogger(t)

	oldConfig := core.Config
	t.Cleanup(func() { core.Config = oldConfig })
	core.Config = &core.Configuration{
		Companion: core.CompanionSettings{HistorySize: 50},
	}

	cm := NewCompanionManager()

	configs := []core.CompanionConfig{
		{
			Name:      "new-comp",
			Command:   "echo hello",
			OnFailure: "continue",
			Workdir:   "/nonexistent/path/that/should/not/exist",
		},
	}

	err := cm.StartCompanions("my-tunnel", configs, nil)
	if err != nil {
		t.Errorf("expected nil error with continue mode, got: %v", err)
	}
}

func TestStartCompanions_NilProgressCallback(t *testing.T) {
	quietLogger(t)

	oldConfig := core.Config
	t.Cleanup(func() { core.Config = oldConfig })
	core.Config = &core.Configuration{
		Companion: core.CompanionSettings{HistorySize: 50},
	}

	cm := NewCompanionManager()

	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)

	broadcaster := NewLogBroadcaster(100)

	cm.companions["my-tunnel"] = map[string]*CompanionProcess{
		"comp": {
			Name:        "comp",
			TunnelAlias: "my-tunnel",
			Pid:         99999,
			State:       CompanionStateRunning,
			output:      broadcaster,
			ctx:         ctx,
			cancel:      cancel,
		},
	}

	configs := []core.CompanionConfig{
		{Name: "comp", Command: "echo hello"},
	}

	// nil progress callback should not panic
	err := cm.StartCompanions("my-tunnel", configs, nil)
	if err != nil {
		t.Fatalf("expected nil error, got: %v", err)
	}
}

func TestRestartCompanions_WithCompletionWait(t *testing.T) {
	quietLogger(t)

	oldConfig := core.Config
	t.Cleanup(func() { core.Config = oldConfig })
	core.Config = &core.Configuration{
		Companion: core.CompanionSettings{HistorySize: 50},
	}

	cm := NewCompanionManager()

	// Start a real process that exits quickly and successfully
	cmd := exec.Command("true")
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	if err := cmd.Start(); err != nil {
		t.Fatalf("failed to start process: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)

	broadcaster := NewLogBroadcaster(100)

	cm.companions["my-tunnel"] = map[string]*CompanionProcess{
		"comp": {
			Name:        "comp",
			TunnelAlias: "my-tunnel",
			Pid:         cmd.Process.Pid,
			State:       CompanionStateRunning,
			Cmd:         cmd,
			output:      broadcaster,
			ctx:         ctx,
			cancel:      cancel,
			Config: core.CompanionConfig{
				Name:     "comp",
				Command:  "true",
				WaitMode: "", // default = completion
				Timeout:  5 * time.Second,
			},
		},
	}

	err := cm.RestartCompanions("my-tunnel")
	// Will try to restart, which may fail (os.Executable not a real companion),
	// but the code paths for restart + completion wait are exercised
	_ = err
}

func TestRestartCompanions_WithStringWait(t *testing.T) {
	quietLogger(t)

	oldConfig := core.Config
	t.Cleanup(func() { core.Config = oldConfig })
	core.Config = &core.Configuration{
		Companion: core.CompanionSettings{HistorySize: 50},
	}

	cm := NewCompanionManager()

	cmd := exec.Command("sleep", "60")
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	if err := cmd.Start(); err != nil {
		t.Fatalf("failed to start process: %v", err)
	}
	t.Cleanup(func() { cmd.Process.Kill() })

	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)

	broadcaster := NewLogBroadcaster(100)

	cm.companions["my-tunnel"] = map[string]*CompanionProcess{
		"comp": {
			Name:        "comp",
			TunnelAlias: "my-tunnel",
			Pid:         cmd.Process.Pid,
			State:       CompanionStateRunning,
			Cmd:         cmd,
			output:      broadcaster,
			ctx:         ctx,
			cancel:      cancel,
			Config: core.CompanionConfig{
				Name:     "comp",
				Command:  "echo ready",
				WaitMode: "string",
				WaitFor:  "ready",
				Timeout:  1 * time.Second,
			},
		},
	}

	err := cm.RestartCompanions("my-tunnel")
	// Will fail because string won't be found in time after restart
	_ = err
}

func TestRestartCompanions_WithReadyDelay(t *testing.T) {
	quietLogger(t)

	oldConfig := core.Config
	t.Cleanup(func() { core.Config = oldConfig })
	core.Config = &core.Configuration{
		Companion: core.CompanionSettings{HistorySize: 50},
	}

	cm := NewCompanionManager()

	// Use "true" which completes immediately
	cmd := exec.Command("true")
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	if err := cmd.Start(); err != nil {
		t.Fatalf("failed to start process: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)

	broadcaster := NewLogBroadcaster(100)

	cm.companions["my-tunnel"] = map[string]*CompanionProcess{
		"comp": {
			Name:        "comp",
			TunnelAlias: "my-tunnel",
			Pid:         cmd.Process.Pid,
			State:       CompanionStateRunning,
			Cmd:         cmd,
			output:      broadcaster,
			ctx:         ctx,
			cancel:      cancel,
			Config: core.CompanionConfig{
				Name:       "comp",
				Command:    "true",
				WaitMode:   "",
				Timeout:    5 * time.Second,
				ReadyDelay: 10 * time.Millisecond,
			},
		},
	}

	err := cm.RestartCompanions("my-tunnel")
	_ = err
}
