package daemon

import (
	"bufio"
	"context"
	"net"
	"strings"
	"testing"

	"go.olrik.dev/overseer/internal/core"
)

func TestHandleLogsWithState_WithOrchestrator(t *testing.T) {
	quietLogger(t)

	tmpDir := t.TempDir()
	oldConfig := core.Config
	t.Cleanup(func() { core.Config = oldConfig })
	core.Config = &core.Configuration{
		ConfigPath: tmpDir,
		Companion:  core.CompanionSettings{HistorySize: 50},
		Locations:  map[string]*core.Location{},
		Contexts:   []*core.ContextRule{},
	}

	old := stateOrchestrator
	t.Cleanup(func() {
		stopStateOrchestrator()
		stateOrchestrator = old
	})

	d := &Daemon{
		tunnels:      make(map[string]Tunnel),
		logBroadcast: NewLogBroadcaster(100),
	}
	d.ctx, d.cancelFunc = context.WithCancel(context.Background())
	d.companionMgr = NewCompanionManager()

	if err := d.initStateOrchestrator(); err != nil {
		t.Fatalf("initStateOrchestrator failed: %v", err)
	}

	// Test handleLogsWithState which delegates to handleLogsWithStateAndHistory
	client, server := net.Pipe()
	done := make(chan struct{})
	go func() {
		defer close(done)
		d.handleLogsWithState(server)
	}()

	reader := bufio.NewReader(client)
	line, err := reader.ReadString('\n')
	if err != nil {
		t.Fatalf("failed to read: %v", err)
	}
	if !strings.Contains(line, "Connected to overseer daemon logs") {
		t.Errorf("expected logs connected message, got %q", line)
	}

	client.Close()
	<-done
}

func TestHandleLogsWithStateAndHistory_NoHistory(t *testing.T) {
	quietLogger(t)

	tmpDir := t.TempDir()
	oldConfig := core.Config
	t.Cleanup(func() { core.Config = oldConfig })
	core.Config = &core.Configuration{
		ConfigPath: tmpDir,
		Companion:  core.CompanionSettings{HistorySize: 50},
		Locations:  map[string]*core.Location{},
		Contexts:   []*core.ContextRule{},
	}

	old := stateOrchestrator
	t.Cleanup(func() {
		stopStateOrchestrator()
		stateOrchestrator = old
	})

	d := &Daemon{
		tunnels:      make(map[string]Tunnel),
		logBroadcast: NewLogBroadcaster(100),
	}
	d.ctx, d.cancelFunc = context.WithCancel(context.Background())
	d.companionMgr = NewCompanionManager()

	if err := d.initStateOrchestrator(); err != nil {
		t.Fatalf("initStateOrchestrator failed: %v", err)
	}

	client, server := net.Pipe()
	done := make(chan struct{})
	go func() {
		defer close(done)
		d.handleLogsWithStateAndHistory(server, false, 0)
	}()

	reader := bufio.NewReader(client)
	line, err := reader.ReadString('\n')
	if err != nil {
		t.Fatalf("failed to read: %v", err)
	}
	if !strings.Contains(line, "Connected to overseer daemon logs") {
		t.Errorf("expected logs connected message, got %q", line)
	}

	client.Close()
	<-done
}

func TestHandleLogsWithHistory_WithOrchestratorDelegates(t *testing.T) {
	quietLogger(t)

	tmpDir := t.TempDir()
	oldConfig := core.Config
	t.Cleanup(func() { core.Config = oldConfig })
	core.Config = &core.Configuration{
		ConfigPath: tmpDir,
		Companion:  core.CompanionSettings{HistorySize: 50},
		Locations:  map[string]*core.Location{},
		Contexts:   []*core.ContextRule{},
	}

	old := stateOrchestrator
	t.Cleanup(func() {
		stopStateOrchestrator()
		stateOrchestrator = old
	})

	d := &Daemon{
		tunnels:      make(map[string]Tunnel),
		logBroadcast: NewLogBroadcaster(100),
	}
	d.ctx, d.cancelFunc = context.WithCancel(context.Background())
	d.companionMgr = NewCompanionManager()

	if err := d.initStateOrchestrator(); err != nil {
		t.Fatalf("initStateOrchestrator failed: %v", err)
	}

	// handleLogsWithHistory should delegate to handleLogsWithStateAndHistory
	// when stateOrchestrator != nil
	client, server := net.Pipe()
	done := make(chan struct{})
	go func() {
		defer close(done)
		d.handleLogsWithHistory(server, true, 5)
	}()

	reader := bufio.NewReader(client)
	line, err := reader.ReadString('\n')
	if err != nil {
		t.Fatalf("failed to read: %v", err)
	}
	if !strings.Contains(line, "Connected to overseer daemon logs") {
		t.Errorf("expected logs connected message, got %q", line)
	}

	client.Close()
	<-done
}

func TestHandleLogsWithHistory_NoHistoryFallback(t *testing.T) {
	quietLogger(t)

	old := stateOrchestrator
	t.Cleanup(func() { stateOrchestrator = old })
	stateOrchestrator = nil

	d := &Daemon{
		logBroadcast: NewLogBroadcaster(100),
	}

	d.logBroadcast.Broadcast("log msg 1\n")
	d.logBroadcast.Broadcast("log msg 2\n")

	// Test with showHistory=false
	client, server := net.Pipe()
	done := make(chan struct{})
	go func() {
		defer close(done)
		d.handleLogsWithHistory(server, false, 0)
	}()

	reader := bufio.NewReader(client)
	line, err := reader.ReadString('\n')
	if err != nil {
		t.Fatalf("failed to read: %v", err)
	}
	if !strings.Contains(line, "Connected to overseer daemon logs") {
		t.Errorf("expected logs connected message, got %q", line)
	}

	client.Close()
	<-done
}

func TestHandleAttachWithHistory_NoHistory(t *testing.T) {
	quietLogger(t)

	d := &Daemon{
		logBroadcast: NewLogBroadcaster(100),
	}

	d.logBroadcast.Broadcast("some log\n")

	client, server := net.Pipe()
	done := make(chan struct{})
	go func() {
		defer close(done)
		d.handleAttachWithHistory(server, false, 0)
	}()

	reader := bufio.NewReader(client)
	line, err := reader.ReadString('\n')
	if err != nil {
		t.Fatalf("failed to read: %v", err)
	}
	if !strings.Contains(line, "Attached to overseer daemon") {
		t.Errorf("expected attach message, got %q", line)
	}

	client.Close()
	<-done
}
