package daemon

import (
	"bufio"
	"context"
	"net"
	"strings"
	"testing"

	"go.olrik.dev/overseer/internal/core"
)

func TestHandleCompanionAttach_StoppedCompanionResetsCtx(t *testing.T) {
	quietLogger(t)

	oldConfig := core.Config
	t.Cleanup(func() { core.Config = oldConfig })
	core.Config = &core.Configuration{
		Companion: core.CompanionSettings{HistorySize: 50},
		Tunnels: map[string]*core.TunnelConfig{
			"my-tunnel": {
				Companions: []core.CompanionConfig{
					{Name: "my-comp", Command: "echo hello"},
				},
			},
		},
	}

	cm := NewCompanionManager()
	broadcaster := NewLogBroadcaster(100)

	// Create a cancelled context to test the ctx reset path
	ctx, cancel := context.WithCancel(context.Background())
	cancel() // Cancel immediately

	cm.companions["my-tunnel"] = map[string]*CompanionProcess{
		"my-comp": {
			Name:        "my-comp",
			TunnelAlias: "my-tunnel",
			Pid:         0,
			State:       CompanionStateStopped,
			output:      broadcaster,
			Config:      core.CompanionConfig{Name: "my-comp", Command: "echo hello"},
			ctx:         ctx,
			cancel:      cancel,
		},
	}

	client, server := net.Pipe()
	done := make(chan struct{})
	go func() {
		defer close(done)
		cm.HandleCompanionAttach(server, "my-tunnel", "my-comp", false, 5)
	}()

	reader := bufio.NewReader(client)
	line, err := reader.ReadString('\n')
	if err != nil {
		t.Fatalf("failed to read: %v", err)
	}
	if !strings.Contains(line, "Attached to companion") {
		t.Errorf("expected 'Attached to companion' message, got %q", line)
	}

	// Should get a message about companion not running
	line2, err := reader.ReadString('\n')
	if err != nil {
		t.Fatalf("failed to read status message: %v", err)
	}
	if !strings.Contains(line2, "not currently running") {
		t.Errorf("expected 'not currently running' message, got %q", line2)
	}

	client.Close()
	<-done
}

func TestHandleCompanionAttach_FailedCompanionResetsCtx(t *testing.T) {
	quietLogger(t)

	oldConfig := core.Config
	t.Cleanup(func() { core.Config = oldConfig })
	core.Config = &core.Configuration{
		Companion: core.CompanionSettings{HistorySize: 50},
		Tunnels: map[string]*core.TunnelConfig{
			"my-tunnel": {
				Companions: []core.CompanionConfig{
					{Name: "my-comp", Command: "echo hello"},
				},
			},
		},
	}

	cm := NewCompanionManager()
	broadcaster := NewLogBroadcaster(100)

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // Cancelled context

	cm.companions["my-tunnel"] = map[string]*CompanionProcess{
		"my-comp": {
			Name:        "my-comp",
			TunnelAlias: "my-tunnel",
			Pid:         0,
			State:       CompanionStateFailed,
			output:      broadcaster,
			Config:      core.CompanionConfig{Name: "my-comp", Command: "echo hello"},
			ctx:         ctx,
			cancel:      cancel,
		},
	}

	client, server := net.Pipe()
	done := make(chan struct{})
	go func() {
		defer close(done)
		cm.HandleCompanionAttach(server, "my-tunnel", "my-comp", false, 5)
	}()

	reader := bufio.NewReader(client)
	line, err := reader.ReadString('\n')
	if err != nil {
		t.Fatalf("failed to read: %v", err)
	}
	if !strings.Contains(line, "Attached to companion") {
		t.Errorf("expected 'Attached to companion' message, got %q", line)
	}

	client.Close()
	<-done
}

func TestHandleCompanionAttach_CreatesNewDormantEntry(t *testing.T) {
	quietLogger(t)

	oldConfig := core.Config
	t.Cleanup(func() { core.Config = oldConfig })
	core.Config = &core.Configuration{
		Companion: core.CompanionSettings{HistorySize: 50},
		Tunnels: map[string]*core.TunnelConfig{
			"my-tunnel": {
				Companions: []core.CompanionConfig{
					{Name: "new-comp", Command: "echo hello"},
				},
			},
		},
	}

	cm := NewCompanionManager()

	client, server := net.Pipe()
	done := make(chan struct{})
	go func() {
		defer close(done)
		cm.HandleCompanionAttach(server, "my-tunnel", "new-comp", false, 5)
	}()

	reader := bufio.NewReader(client)
	line, err := reader.ReadString('\n')
	if err != nil {
		t.Fatalf("failed to read: %v", err)
	}
	if !strings.Contains(line, "Attached to companion") {
		t.Errorf("expected 'Attached to companion' message, got %q", line)
	}

	client.Close()
	<-done

	// Verify dormant entry was created
	proc := cm.GetCompanion("my-tunnel", "new-comp")
	if proc == nil {
		t.Error("expected dormant companion entry to be created")
	}
}

func TestHandleCompanionAttach_ReadyCompanionStreamsHistory(t *testing.T) {
	quietLogger(t)

	oldConfig := core.Config
	t.Cleanup(func() { core.Config = oldConfig })
	core.Config = &core.Configuration{
		Companion: core.CompanionSettings{HistorySize: 50},
		Tunnels: map[string]*core.TunnelConfig{
			"my-tunnel": {
				Companions: []core.CompanionConfig{
					{Name: "my-comp", Command: "echo hello"},
				},
			},
		},
	}

	cm := NewCompanionManager()
	broadcaster := NewLogBroadcaster(100)
	broadcaster.Broadcast("line 1\n")
	broadcaster.Broadcast("line 2\n")

	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)

	cm.companions["my-tunnel"] = map[string]*CompanionProcess{
		"my-comp": {
			Name:        "my-comp",
			TunnelAlias: "my-tunnel",
			Pid:         12345,
			State:       CompanionStateReady,
			output:      broadcaster,
			Config:      core.CompanionConfig{Name: "my-comp", Command: "echo hello"},
			ctx:         ctx,
			cancel:      cancel,
		},
	}

	client, server := net.Pipe()
	done := make(chan struct{})
	go func() {
		defer close(done)
		cm.HandleCompanionAttach(server, "my-tunnel", "my-comp", true, 10)
	}()

	reader := bufio.NewReader(client)
	// Read initial message
	line, err := reader.ReadString('\n')
	if err != nil {
		t.Fatalf("failed to read: %v", err)
	}
	if !strings.Contains(line, "Attached to companion") {
		t.Errorf("expected 'Attached to companion' message, got %q", line)
	}

	// Should get history lines
	histLine, err := reader.ReadString('\n')
	if err != nil {
		t.Fatalf("failed to read history: %v", err)
	}
	if !strings.Contains(histLine, "line 1") {
		t.Errorf("expected history line 1, got %q", histLine)
	}

	client.Close()
	<-done
}

func TestHandleCompanionAttach_CompanionTerminates(t *testing.T) {
	quietLogger(t)

	oldConfig := core.Config
	t.Cleanup(func() { core.Config = oldConfig })
	core.Config = &core.Configuration{
		Companion: core.CompanionSettings{HistorySize: 50},
		Tunnels: map[string]*core.TunnelConfig{
			"my-tunnel": {
				Companions: []core.CompanionConfig{
					{Name: "my-comp", Command: "echo hello"},
				},
			},
		},
	}

	cm := NewCompanionManager()
	broadcaster := NewLogBroadcaster(100)

	ctx, cancel := context.WithCancel(context.Background())

	cm.companions["my-tunnel"] = map[string]*CompanionProcess{
		"my-comp": {
			Name:        "my-comp",
			TunnelAlias: "my-tunnel",
			Pid:         12345,
			State:       CompanionStateRunning,
			output:      broadcaster,
			Config:      core.CompanionConfig{Name: "my-comp", Command: "echo hello"},
			ctx:         ctx,
			cancel:      cancel,
		},
	}

	client, server := net.Pipe()
	done := make(chan struct{})
	go func() {
		defer close(done)
		cm.HandleCompanionAttach(server, "my-tunnel", "my-comp", false, 5)
	}()

	reader := bufio.NewReader(client)
	// Read initial message
	_, err := reader.ReadString('\n')
	if err != nil {
		t.Fatalf("failed to read: %v", err)
	}

	// Read empty line separator
	_, err = reader.ReadString('\n')
	if err != nil {
		t.Fatalf("failed to read separator: %v", err)
	}

	// Cancel the context to simulate companion termination
	cancel()

	// Read until we find the termination message (may have separators/blank lines before it)
	foundTermination := false
	for i := 0; i < 5; i++ {
		msg, readErr := reader.ReadString('\n')
		if readErr != nil {
			break
		}
		if strings.Contains(msg, "terminated") {
			foundTermination = true
			break
		}
	}
	if !foundTermination {
		t.Error("expected termination message")
	}

	<-done
}
