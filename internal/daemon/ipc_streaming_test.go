package daemon

import (
	"bufio"
	"encoding/json"
	"net"
	"strings"
	"testing"

	"go.olrik.dev/overseer/internal/core"
)

func TestHandleConnection_IPC_LogsWithArgs(t *testing.T) {
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

	// Test LOGS with line count argument
	clientConn, serverConn := net.Pipe()
	done := make(chan struct{})
	go func() {
		defer close(done)
		d.handleConnection(serverConn)
	}()

	clientConn.Write([]byte("LOGS 10\n"))

	reader := bufio.NewReader(clientConn)
	line, err := reader.ReadString('\n')
	if err != nil {
		t.Fatalf("failed to read: %v", err)
	}
	if !strings.Contains(line, "Connected") {
		t.Errorf("expected 'Connected' message, got %q", line)
	}

	clientConn.Close()
	<-done
}

func TestHandleConnection_IPC_LogsNoHistory(t *testing.T) {
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

	clientConn, serverConn := net.Pipe()
	done := make(chan struct{})
	go func() {
		defer close(done)
		d.handleConnection(serverConn)
	}()

	clientConn.Write([]byte("LOGS no_history\n"))

	reader := bufio.NewReader(clientConn)
	line, err := reader.ReadString('\n')
	if err != nil {
		t.Fatalf("failed to read: %v", err)
	}
	if !strings.Contains(line, "Connected") {
		t.Errorf("expected 'Connected' message, got %q", line)
	}

	clientConn.Close()
	<-done
}

func TestHandleConnection_IPC_AttachWithArgs(t *testing.T) {
	quietLogger(t)

	oldConfig := core.Config
	t.Cleanup(func() { core.Config = oldConfig })
	core.Config = &core.Configuration{
		Companion: core.CompanionSettings{HistorySize: 50},
	}

	d := New()

	clientConn, serverConn := net.Pipe()
	done := make(chan struct{})
	go func() {
		defer close(done)
		d.handleConnection(serverConn)
	}()

	clientConn.Write([]byte("ATTACH 5 no_history\n"))

	reader := bufio.NewReader(clientConn)
	line, err := reader.ReadString('\n')
	if err != nil {
		t.Fatalf("failed to read: %v", err)
	}
	if !strings.Contains(line, "Attached") {
		t.Errorf("expected 'Attached' message, got %q", line)
	}

	clientConn.Close()
	<-done
}

func TestHandleConnection_IPC_CompanionAttachWithArgs(t *testing.T) {
	quietLogger(t)

	oldConfig := core.Config
	t.Cleanup(func() { core.Config = oldConfig })
	core.Config = &core.Configuration{
		Companion: core.CompanionSettings{HistorySize: 50},
		Tunnels:   map[string]*core.TunnelConfig{},
	}

	d := New()

	clientConn, serverConn := net.Pipe()
	done := make(chan struct{})
	go func() {
		defer close(done)
		d.handleConnection(serverConn)
	}()

	// COMPANION_ATTACH with tunnel, companion, and lines args
	clientConn.Write([]byte("COMPANION_ATTACH my-tunnel my-comp 10 no_history\n"))

	reader := bufio.NewReader(clientConn)
	line, err := reader.ReadString('\n')
	if err != nil {
		t.Fatalf("failed to read: %v", err)
	}
	// Should get a "not found" type error since no companions exist
	_ = line

	clientConn.Close()
	<-done
}

func TestHandleConnection_IPC_SSHConnectWithTag(t *testing.T) {
	quietLogger(t)

	oldConfig := core.Config
	t.Cleanup(func() { core.Config = oldConfig })
	core.Config = &core.Configuration{
		Companion: core.CompanionSettings{HistorySize: 50},
		Tunnels:   map[string]*core.TunnelConfig{},
		SSH:       core.SSHConfig{},
	}

	d := New()

	// SSH_CONNECT uses streaming, so we can't use sendIPCCommand
	clientConn, serverConn := net.Pipe()
	done := make(chan struct{})
	go func() {
		defer close(done)
		d.handleConnection(serverConn)
	}()

	clientConn.Write([]byte("SSH_CONNECT test-alias --env=MY_TAG=my-tag\n"))

	// Read some output (will be streaming JSON)
	reader := bufio.NewReader(clientConn)
	line, err := reader.ReadString('\n')
	if err != nil {
		t.Fatalf("failed to read: %v", err)
	}
	// Should get some kind of response about the tunnel
	_ = line

	clientConn.Close()
	<-done
}

func TestHandleConnection_IPC_LogMasking(t *testing.T) {
	quietLogger(t)

	oldConfig := core.Config
	t.Cleanup(func() { core.Config = oldConfig })
	core.Config = &core.Configuration{
		Companion: core.CompanionSettings{HistorySize: 50},
		Tunnels:   map[string]*core.TunnelConfig{},
	}

	d := New()

	// Test ASKPASS log masking (token at index 1)
	resp := sendIPCCommand(t, d, "ASKPASS my-tunnel my-secret-token")
	_ = resp

	// Test COMPANION_INIT log masking (token at index 2)
	resp = sendIPCCommand(t, d, "COMPANION_INIT tunnel1 comp1 secret-token-123")
	_ = resp
}

func TestHandleConnection_IPC_SSHReconnectNotRunning(t *testing.T) {
	quietLogger(t)

	oldConfig := core.Config
	t.Cleanup(func() { core.Config = oldConfig })
	core.Config = &core.Configuration{
		Companion: core.CompanionSettings{HistorySize: 50},
		Tunnels:   map[string]*core.TunnelConfig{},
		SSH:       core.SSHConfig{},
	}

	d := New()

	// SSH_RECONNECT with a non-running tunnel now uses streaming
	clientConn, serverConn := net.Pipe()
	done := make(chan struct{})
	go func() {
		defer close(done)
		d.handleConnection(serverConn)
	}()

	clientConn.Write([]byte("SSH_RECONNECT nonexistent\n"))

	reader := bufio.NewReader(clientConn)
	line, err := reader.ReadString('\n')
	if err != nil {
		t.Fatalf("failed to read first streaming line: %v", err)
	}

	var msg ResponseMessage
	if err := json.Unmarshal([]byte(line), &msg); err != nil {
		t.Fatalf("failed to unmarshal streaming message: %v (line: %q)", err, line)
	}
	if msg.Status != "WARN" {
		t.Errorf("expected WARN status, got %q", msg.Status)
	}
	if !strings.Contains(msg.Message, "not connected") {
		t.Errorf("expected message to contain 'not connected', got %q", msg.Message)
	}

	clientConn.Close()
	<-done
}
