package daemon

import (
	"bufio"
	"net"
	"strings"
	"testing"

	"go.olrik.dev/overseer/internal/core"
)

func TestHandleLogsWithHistory_ReplayHistory(t *testing.T) {
	quietLogger(t)

	old := stateOrchestrator
	t.Cleanup(func() { stateOrchestrator = old })
	stateOrchestrator = nil

	oldConfig := core.Config
	t.Cleanup(func() { core.Config = oldConfig })
	core.Config = &core.Configuration{
		Companion: core.CompanionSettings{HistorySize: 50},
	}

	d := New()

	// Add history messages before connecting
	d.logBroadcast.Broadcast("history line 1\n")
	d.logBroadcast.Broadcast("history line 2\n")
	d.logBroadcast.Broadcast("history line 3\n")

	clientConn, serverConn := net.Pipe()
	done := make(chan struct{})
	go func() {
		defer close(done)
		d.handleLogsWithHistory(serverConn, true, 10)
	}()

	reader := bufio.NewReader(clientConn)

	// Read initial "Connected" message
	line, err := reader.ReadString('\n')
	if err != nil {
		t.Fatalf("failed to read initial message: %v", err)
	}
	if !strings.Contains(line, "Connected") {
		t.Errorf("expected 'Connected' message, got %q", line)
	}

	// Read history lines
	for i := 1; i <= 3; i++ {
		line, err := reader.ReadString('\n')
		if err != nil {
			t.Fatalf("failed to read history line %d: %v", i, err)
		}
		if !strings.Contains(line, "history line") {
			t.Errorf("expected history line, got %q", line)
		}
	}

	clientConn.Close()
	<-done
}

func TestHandleAttachWithHistory_ReplayHistory(t *testing.T) {
	quietLogger(t)

	oldConfig := core.Config
	t.Cleanup(func() { core.Config = oldConfig })
	core.Config = &core.Configuration{
		Companion: core.CompanionSettings{HistorySize: 50},
	}

	d := New()

	// Add history messages
	d.logBroadcast.Broadcast("attach history 1\n")
	d.logBroadcast.Broadcast("attach history 2\n")

	clientConn, serverConn := net.Pipe()
	done := make(chan struct{})
	go func() {
		defer close(done)
		d.handleAttachWithHistory(serverConn, true, 10)
	}()

	reader := bufio.NewReader(clientConn)

	// Read initial "Attached" message (includes trailing newline)
	line, err := reader.ReadString('\n')
	if err != nil {
		t.Fatalf("failed to read: %v", err)
	}
	if !strings.Contains(line, "Attached") {
		t.Errorf("expected 'Attached' message, got %q", line)
	}

	// Read the blank line after initial message
	line, err = reader.ReadString('\n')
	if err != nil {
		t.Fatalf("failed to read blank line: %v", err)
	}

	// Read history lines
	for i := 1; i <= 2; i++ {
		line, err := reader.ReadString('\n')
		if err != nil {
			t.Fatalf("failed to read history line %d: %v", i, err)
		}
		if !strings.Contains(line, "attach history") {
			t.Errorf("expected history line, got %q", line)
		}
	}

	clientConn.Close()
	<-done
}

func TestHandleAttachWithHistory_NoHistoryLive(t *testing.T) {
	quietLogger(t)

	oldConfig := core.Config
	t.Cleanup(func() { core.Config = oldConfig })
	core.Config = &core.Configuration{
		Companion: core.CompanionSettings{HistorySize: 50},
	}

	d := New()

	// Add history that should NOT be sent
	d.logBroadcast.Broadcast("should not see this\n")

	clientConn, serverConn := net.Pipe()
	done := make(chan struct{})
	go func() {
		defer close(done)
		d.handleAttachWithHistory(serverConn, false, 0)
	}()

	reader := bufio.NewReader(clientConn)

	// Read initial message ("Attached to overseer daemon. Press Ctrl+C to detach.\n\n")
	line, err := reader.ReadString('\n')
	if err != nil {
		t.Fatalf("failed to read: %v", err)
	}
	if !strings.Contains(line, "Attached") {
		t.Errorf("expected 'Attached' message, got %q", line)
	}

	// Read the blank line after the initial message
	_, _ = reader.ReadString('\n')

	// Broadcast a new message to read
	d.logBroadcast.Broadcast("new live message\n")

	line, err = reader.ReadString('\n')
	if err != nil {
		t.Fatalf("failed to read live message: %v", err)
	}
	if !strings.Contains(line, "new live message") {
		t.Errorf("expected 'new live message', got %q", line)
	}

	clientConn.Close()
	<-done
}
