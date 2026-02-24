package daemon

import (
	"bufio"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net"
	"os"
	"strings"
	"testing"
	"time"

	"go.olrik.dev/overseer/internal/core"
)

// quietLoggerIPC suppresses default slog output during tests.
func quietLoggerIPC(t *testing.T) {
	t.Helper()
	old := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.Level(99)})))
	t.Cleanup(func() { slog.SetDefault(old) })
}

// sendIPCCommand sends a command string to handleConnection via net.Pipe
// and reads back the JSON response.
func sendIPCCommand(t *testing.T, d *Daemon, command string) Response {
	t.Helper()

	clientConn, serverConn := net.Pipe()

	done := make(chan struct{})
	go func() {
		defer close(done)
		d.handleConnection(serverConn)
	}()

	// Write the command
	_, err := clientConn.Write([]byte(command + "\n"))
	if err != nil {
		t.Fatalf("failed to write command: %v", err)
	}

	// Read the response (handleConnection closes the server side when done)
	data, err := io.ReadAll(clientConn)
	if err != nil {
		t.Fatalf("failed to read response: %v", err)
	}
	clientConn.Close()

	<-done

	var resp Response
	if len(data) > 0 {
		if err := json.Unmarshal(data, &resp); err != nil {
			t.Fatalf("failed to parse response JSON %q: %v", string(data), err)
		}
	}
	return resp
}

func TestHandleConnection_IPC_UnknownCommand(t *testing.T) {
	quietLoggerIPC(t)

	oldConfig := core.Config
	defer func() { core.Config = oldConfig }()
	core.Config = &core.Configuration{}

	d := &Daemon{
		tunnels:       make(map[string]Tunnel),
		askpassTokens: make(map[string]string),
		logBroadcast:  NewLogBroadcaster(100),
		companionMgr:  NewCompanionManager(),
	}

	resp := sendIPCCommand(t, d, "FOOBAR")

	if len(resp.Messages) != 1 {
		t.Fatalf("expected 1 message, got %d", len(resp.Messages))
	}
	if resp.Messages[0].Status != "ERROR" {
		t.Errorf("expected ERROR status, got %q", resp.Messages[0].Status)
	}
	if resp.Messages[0].Message != "Unknown command." {
		t.Errorf("expected 'Unknown command.', got %q", resp.Messages[0].Message)
	}
}

func TestHandleConnection_IPC_SSHDisconnectNotRunning(t *testing.T) {
	quietLoggerIPC(t)

	oldConfig := core.Config
	defer func() { core.Config = oldConfig }()
	core.Config = &core.Configuration{}

	d := &Daemon{
		tunnels:       make(map[string]Tunnel),
		askpassTokens: make(map[string]string),
		logBroadcast:  NewLogBroadcaster(100),
		companionMgr:  NewCompanionManager(),
	}

	resp := sendIPCCommand(t, d, "SSH_DISCONNECT nonexistent")

	if len(resp.Messages) != 1 {
		t.Fatalf("expected 1 message, got %d", len(resp.Messages))
	}
	if resp.Messages[0].Status != "ERROR" {
		t.Errorf("expected ERROR status, got %q", resp.Messages[0].Status)
	}
	if !strings.Contains(resp.Messages[0].Message, "not running") {
		t.Errorf("expected 'not running' in message, got %q", resp.Messages[0].Message)
	}
}

func TestHandleConnection_IPC_SSHDisconnectAllEmpty(t *testing.T) {
	quietLoggerIPC(t)

	oldConfig := core.Config
	defer func() { core.Config = oldConfig }()
	core.Config = &core.Configuration{}

	d := &Daemon{
		tunnels:       make(map[string]Tunnel),
		askpassTokens: make(map[string]string),
		logBroadcast:  NewLogBroadcaster(100),
		companionMgr:  NewCompanionManager(),
	}

	resp := sendIPCCommand(t, d, "SSH_DISCONNECT_ALL")

	// Empty tunnels map means the loop doesn't execute; response has no messages
	if len(resp.Messages) != 0 {
		t.Errorf("expected 0 messages for empty tunnels, got %d: %+v", len(resp.Messages), resp.Messages)
	}
}

func TestHandleConnection_IPC_ResetNoTunnels(t *testing.T) {
	quietLoggerIPC(t)

	oldConfig := core.Config
	defer func() { core.Config = oldConfig }()
	core.Config = &core.Configuration{}

	d := &Daemon{
		tunnels:       make(map[string]Tunnel),
		askpassTokens: make(map[string]string),
		logBroadcast:  NewLogBroadcaster(100),
		companionMgr:  NewCompanionManager(),
	}

	resp := sendIPCCommand(t, d, "RESET")

	if len(resp.Messages) != 1 {
		t.Fatalf("expected 1 message, got %d", len(resp.Messages))
	}
	if resp.Messages[0].Status != "WARN" {
		t.Errorf("expected WARN status, got %q", resp.Messages[0].Status)
	}
	if !strings.Contains(resp.Messages[0].Message, "No tunnels") {
		t.Errorf("expected 'No tunnels' in message, got %q", resp.Messages[0].Message)
	}
}

func TestHandleLogsWithHistory(t *testing.T) {
	quietLoggerIPC(t)

	d := &Daemon{
		logBroadcast: NewLogBroadcaster(100),
	}

	// stateOrchestrator is nil, so handleLogsWithHistory uses the fallback path

	t.Run("sends initial message", func(t *testing.T) {
		clientConn, serverConn := net.Pipe()

		done := make(chan struct{})
		go func() {
			defer close(done)
			d.handleLogsWithHistory(serverConn, false, 0)
		}()

		reader := bufio.NewReader(clientConn)
		line, err := reader.ReadString('\n')
		if err != nil {
			t.Fatalf("failed to read initial message: %v", err)
		}

		if !strings.Contains(line, "Connected to overseer daemon logs") {
			t.Errorf("expected initial message, got %q", line)
		}

		clientConn.Close()
		<-done
	})

	t.Run("with history sends history before subscribing", func(t *testing.T) {
		// Pre-populate history
		broadcaster := NewLogBroadcaster(100)
		broadcaster.Broadcast("history line 1\n")
		broadcaster.Broadcast("history line 2\n")

		d2 := &Daemon{
			logBroadcast: broadcaster,
		}

		clientConn, serverConn := net.Pipe()

		done := make(chan struct{})
		go func() {
			defer close(done)
			d2.handleLogsWithHistory(serverConn, true, 10)
		}()

		reader := bufio.NewReader(clientConn)

		// Read initial message
		line, err := reader.ReadString('\n')
		if err != nil {
			t.Fatalf("failed to read initial message: %v", err)
		}
		if !strings.Contains(line, "Connected to overseer daemon logs") {
			t.Errorf("expected initial message, got %q", line)
		}

		// Read history lines
		line1, err := reader.ReadString('\n')
		if err != nil {
			t.Fatalf("failed to read history line 1: %v", err)
		}
		if !strings.Contains(line1, "history line 1") {
			t.Errorf("expected 'history line 1', got %q", line1)
		}

		line2, err := reader.ReadString('\n')
		if err != nil {
			t.Fatalf("failed to read history line 2: %v", err)
		}
		if !strings.Contains(line2, "history line 2") {
			t.Errorf("expected 'history line 2', got %q", line2)
		}

		clientConn.Close()
		<-done
	})
}

func TestHandleAttachWithHistory(t *testing.T) {
	quietLoggerIPC(t)

	t.Run("sends initial attached message", func(t *testing.T) {
		d := &Daemon{
			logBroadcast: NewLogBroadcaster(100),
		}

		clientConn, serverConn := net.Pipe()

		done := make(chan struct{})
		go func() {
			defer close(done)
			d.handleAttachWithHistory(serverConn, false, 0)
		}()

		reader := bufio.NewReader(clientConn)
		line, err := reader.ReadString('\n')
		if err != nil {
			t.Fatalf("failed to read initial message: %v", err)
		}

		if !strings.Contains(line, "Attached to overseer daemon") {
			t.Errorf("expected attached message, got %q", line)
		}

		clientConn.Close()
		<-done
	})

	t.Run("streams live messages after connection", func(t *testing.T) {
		broadcaster := NewLogBroadcaster(100)
		d := &Daemon{
			logBroadcast: broadcaster,
		}

		clientConn, serverConn := net.Pipe()

		done := make(chan struct{})
		go func() {
			defer close(done)
			d.handleAttachWithHistory(serverConn, false, 0)
		}()

		reader := bufio.NewReader(clientConn)

		// Read and discard the initial message ("Attached to overseer daemon...\n\n")
		// The message ends with two newlines, so we need two reads.
		_, err := reader.ReadString('\n')
		if err != nil {
			t.Fatalf("failed to read initial message: %v", err)
		}
		_, _ = reader.ReadString('\n') // second newline

		// Broadcast a live message
		broadcaster.Broadcast("live log entry\n")

		// Read the live message
		line, err := reader.ReadString('\n')
		if err != nil {
			t.Fatalf("failed to read live message: %v", err)
		}
		if !strings.Contains(line, "live log entry") {
			t.Errorf("expected 'live log entry', got %q", line)
		}

		clientConn.Close()
		<-done
	})
}

func TestHandleConnection_IPC_ContextStatus(t *testing.T) {
	quietLoggerIPC(t)

	tmpDir := t.TempDir()
	oldConfig := core.Config
	defer func() { core.Config = oldConfig }()
	core.Config = &core.Configuration{
		ConfigPath: tmpDir,
		Companion:  core.CompanionSettings{HistorySize: 50},
		Locations:  map[string]*core.Location{},
		Contexts:   []*core.ContextRule{},
	}

	old := stateOrchestrator
	defer func() {
		stopStateOrchestrator()
		stateOrchestrator = old
	}()

	d := New()
	if err := d.initStateOrchestrator(); err != nil {
		t.Fatalf("initStateOrchestrator failed: %v", err)
	}

	resp := sendIPCCommand(t, d, "CONTEXT_STATUS")
	if len(resp.Messages) == 0 {
		t.Fatal("expected at least one message")
	}
	if resp.Messages[0].Status != "INFO" {
		t.Errorf("expected INFO status, got %q", resp.Messages[0].Status)
	}
}

func TestHandleConnection_IPC_CompanionStatus(t *testing.T) {
	quietLoggerIPC(t)

	oldConfig := core.Config
	defer func() { core.Config = oldConfig }()
	core.Config = &core.Configuration{
		Companion: core.CompanionSettings{HistorySize: 50},
	}

	d := New()
	resp := sendIPCCommand(t, d, "COMPANION_STATUS")
	if len(resp.Messages) == 0 {
		t.Fatal("expected at least one message")
	}
	if resp.Messages[0].Status != "INFO" {
		t.Errorf("expected INFO status, got %q", resp.Messages[0].Status)
	}
}

func TestHandleConnection_IPC_CompanionInitInvalid(t *testing.T) {
	quietLoggerIPC(t)

	oldConfig := core.Config
	defer func() { core.Config = oldConfig }()
	core.Config = &core.Configuration{
		Companion: core.CompanionSettings{HistorySize: 50},
	}

	d := New()

	// Missing args
	resp := sendIPCCommand(t, d, "COMPANION_INIT tunnel1")
	if len(resp.Messages) == 0 {
		t.Fatal("expected at least one message")
	}
	if resp.Messages[0].Status != "ERROR" {
		t.Errorf("expected ERROR status, got %q", resp.Messages[0].Status)
	}
}

func TestHandleConnection_IPC_CompanionStopInvalid(t *testing.T) {
	quietLoggerIPC(t)

	oldConfig := core.Config
	defer func() { core.Config = oldConfig }()
	core.Config = &core.Configuration{
		Companion: core.CompanionSettings{HistorySize: 50},
	}

	d := New()

	// Missing args
	resp := sendIPCCommand(t, d, "COMPANION_STOP")
	if len(resp.Messages) == 0 {
		t.Fatal("expected at least one message")
	}
	if resp.Messages[0].Status != "ERROR" {
		t.Errorf("expected ERROR status, got %q", resp.Messages[0].Status)
	}
}

func TestHandleConnection_IPC_CompanionStartNoTunnel(t *testing.T) {
	quietLoggerIPC(t)

	oldConfig := core.Config
	defer func() { core.Config = oldConfig }()
	core.Config = &core.Configuration{
		Companion: core.CompanionSettings{HistorySize: 50},
	}

	d := New()

	resp := sendIPCCommand(t, d, "COMPANION_START nonexistent comp1")
	if len(resp.Messages) == 0 {
		t.Fatal("expected at least one message")
	}
	if resp.Messages[0].Status != "ERROR" {
		t.Errorf("expected ERROR status, got %q", resp.Messages[0].Status)
	}
	if !strings.Contains(resp.Messages[0].Message, "not running") {
		t.Errorf("expected 'not running' in message, got %q", resp.Messages[0].Message)
	}
}

func TestHandleConnection_IPC_CompanionRestartNoTunnel(t *testing.T) {
	quietLoggerIPC(t)

	oldConfig := core.Config
	defer func() { core.Config = oldConfig }()
	core.Config = &core.Configuration{
		Companion: core.CompanionSettings{HistorySize: 50},
	}

	d := New()

	resp := sendIPCCommand(t, d, "COMPANION_RESTART nonexistent comp1")
	if len(resp.Messages) == 0 {
		t.Fatal("expected at least one message")
	}
	if resp.Messages[0].Status != "ERROR" {
		t.Errorf("expected ERROR status, got %q", resp.Messages[0].Status)
	}
}

func TestHandleConnection_IPC_CompanionRestartInvalid(t *testing.T) {
	quietLoggerIPC(t)

	oldConfig := core.Config
	defer func() { core.Config = oldConfig }()
	core.Config = &core.Configuration{
		Companion: core.CompanionSettings{HistorySize: 50},
	}

	d := New()

	// Missing args
	resp := sendIPCCommand(t, d, "COMPANION_RESTART")
	if len(resp.Messages) == 0 {
		t.Fatal("expected at least one message")
	}
	if resp.Messages[0].Status != "ERROR" {
		t.Errorf("expected ERROR status, got %q", resp.Messages[0].Status)
	}
}

func TestHandleConnection_IPC_CompanionStartInvalid(t *testing.T) {
	quietLoggerIPC(t)

	oldConfig := core.Config
	defer func() { core.Config = oldConfig }()
	core.Config = &core.Configuration{
		Companion: core.CompanionSettings{HistorySize: 50},
	}

	d := New()

	// Missing args
	resp := sendIPCCommand(t, d, "COMPANION_START")
	if len(resp.Messages) == 0 {
		t.Fatal("expected at least one message")
	}
	if resp.Messages[0].Status != "ERROR" {
		t.Errorf("expected ERROR status, got %q", resp.Messages[0].Status)
	}
}

func TestHandleConnection_IPC_AskpassInvalid(t *testing.T) {
	quietLoggerIPC(t)

	oldConfig := core.Config
	defer func() { core.Config = oldConfig }()
	core.Config = &core.Configuration{
		Companion: core.CompanionSettings{HistorySize: 50},
	}

	d := New()

	// Missing args
	resp := sendIPCCommand(t, d, "ASKPASS alias-only")
	if len(resp.Messages) == 0 {
		t.Fatal("expected at least one message")
	}
	if resp.Messages[0].Status != "ERROR" {
		t.Errorf("expected ERROR status, got %q", resp.Messages[0].Status)
	}
}

func TestHandleConnection_IPC_CompanionAttachInvalid(t *testing.T) {
	quietLoggerIPC(t)

	oldConfig := core.Config
	defer func() { core.Config = oldConfig }()
	core.Config = &core.Configuration{
		Companion: core.CompanionSettings{HistorySize: 50},
	}

	d := New()

	// Missing args
	resp := sendIPCCommand(t, d, "COMPANION_ATTACH tunnel-only")
	if len(resp.Messages) == 0 {
		t.Fatal("expected at least one message")
	}
	if resp.Messages[0].Status != "ERROR" {
		t.Errorf("expected ERROR status, got %q", resp.Messages[0].Status)
	}
}

// TestHandleConnection_IPC_SSHReconnectNotRunning moved to ipc_streaming_test.go
// (reconnect of a non-running tunnel now streams instead of returning a single response)

func TestHandleLogs_Wrapper(t *testing.T) {
	quietLoggerIPC(t)

	old := stateOrchestrator
	defer func() { stateOrchestrator = old }()
	stateOrchestrator = nil

	d := &Daemon{
		logBroadcast: NewLogBroadcaster(100),
	}

	clientConn, serverConn := net.Pipe()
	done := make(chan struct{})
	go func() {
		defer close(done)
		d.handleLogs(serverConn)
	}()

	reader := bufio.NewReader(clientConn)
	line, err := reader.ReadString('\n')
	if err != nil {
		t.Fatalf("failed to read: %v", err)
	}
	if !strings.Contains(line, "Connected") {
		t.Errorf("expected connected message, got %q", line)
	}

	clientConn.Close()
	<-done
}

func TestHandleAttach_Wrapper(t *testing.T) {
	quietLoggerIPC(t)

	d := &Daemon{
		logBroadcast: NewLogBroadcaster(100),
	}

	clientConn, serverConn := net.Pipe()
	done := make(chan struct{})
	go func() {
		defer close(done)
		d.handleAttach(serverConn)
	}()

	reader := bufio.NewReader(clientConn)
	line, err := reader.ReadString('\n')
	if err != nil {
		t.Fatalf("failed to read: %v", err)
	}
	if !strings.Contains(line, "Attached") {
		t.Errorf("expected attached message, got %q", line)
	}

	clientConn.Close()
	<-done
}

func TestHandleConnection_IPC_CompanionStopSuccess(t *testing.T) {
	quietLoggerIPC(t)

	oldConfig := core.Config
	defer func() { core.Config = oldConfig }()
	core.Config = &core.Configuration{
		Companion: core.CompanionSettings{HistorySize: 50},
	}

	d := New()

	// Companion stop for nonexistent returns success (nil error)
	resp := sendIPCCommand(t, d, "COMPANION_STOP tunnel1 comp1")
	if len(resp.Messages) == 0 {
		t.Fatal("expected at least one message")
	}
	if resp.Messages[0].Status != "INFO" {
		t.Errorf("expected INFO status, got %q", resp.Messages[0].Status)
	}
}

func TestHandleConnection_IPC_SSHDisconnectAll_WithTunnels(t *testing.T) {
	quietLoggerIPC(t)

	oldConfig := core.Config
	defer func() { core.Config = oldConfig }()
	core.Config = &core.Configuration{
		Companion: core.CompanionSettings{HistorySize: 50},
	}

	d := New()
	d.tunnels["tunnel1"] = Tunnel{
		Hostname: "host1.example.com",
		State:    StateDisconnected,
	}

	resp := sendIPCCommand(t, d, "SSH_DISCONNECT_ALL")
	if len(resp.Messages) == 0 {
		t.Fatal("expected at least one message")
	}
}

func TestHandleConnection_IPC_ResetWithTunnels(t *testing.T) {
	quietLoggerIPC(t)

	oldConfig := core.Config
	defer func() { core.Config = oldConfig }()
	core.Config = &core.Configuration{
		Companion: core.CompanionSettings{HistorySize: 50},
	}

	d := New()
	d.tunnels["tunnel1"] = Tunnel{
		Hostname:   "host1.example.com",
		State:      StateReconnecting,
		RetryCount: 5,
	}

	resp := sendIPCCommand(t, d, "RESET")
	if len(resp.Messages) == 0 {
		t.Fatal("expected at least one message")
	}

	// Verify retries were reset
	d.mu.Lock()
	tunnel := d.tunnels["tunnel1"]
	d.mu.Unlock()
	if tunnel.RetryCount != 0 {
		t.Errorf("expected RetryCount=0 after RESET, got %d", tunnel.RetryCount)
	}
}

func TestCheckOnlineStatus_NilOrchestrator(t *testing.T) {
	// stateOrchestrator is package-level; ensure it's nil for this test
	old := stateOrchestrator
	stateOrchestrator = nil
	defer func() { stateOrchestrator = old }()

	d := &Daemon{}
	if d.checkOnlineStatus() {
		t.Error("expected false when stateOrchestrator is nil")
	}
}

func TestIsPublicIPKnown_NilOrchestrator(t *testing.T) {
	// When GetStateOrchestrator() returns nil, isPublicIPKnown should return true
	// (no orchestrator = no env file dependency)
	old := stateOrchestrator
	stateOrchestrator = nil
	defer func() { stateOrchestrator = old }()

	d := &Daemon{}
	if !d.isPublicIPKnown() {
		t.Error("expected true when stateOrchestrator is nil")
	}
}

func TestSendCommandStreaming(t *testing.T) {
	quietLoggerIPC(t)

	t.Run("parses INFO WARN ERROR messages", func(t *testing.T) {
		// Create a temporary unix socket server that sends streaming JSON messages
		socketPath := os.TempDir() + "/overseer-test-streaming.sock"
		os.Remove(socketPath)

		listener, err := net.Listen("unix", socketPath)
		if err != nil {
			t.Fatalf("failed to create listener: %v", err)
		}
		defer listener.Close()
		defer os.Remove(socketPath)

		// Override socket path temporarily
		oldConfig := core.Config
		defer func() { core.Config = oldConfig }()
		core.Config = &core.Configuration{
			ConfigPath: os.TempDir(),
		}

		// Server goroutine that reads the command and sends back streaming responses
		serverDone := make(chan struct{})
		go func() {
			defer close(serverDone)
			conn, err := listener.Accept()
			if err != nil {
				return
			}
			defer conn.Close()

			// Read the command
			reader := bufio.NewReader(conn)
			reader.ReadString('\n')

			// Send streaming JSON messages
			msgs := []ResponseMessage{
				{Message: "Starting...", Status: "INFO"},
				{Message: "Something odd", Status: "WARN"},
				{Message: "Failed!", Status: "ERROR"},
			}
			for _, msg := range msgs {
				data, _ := json.Marshal(msg)
				conn.Write(append(data, '\n'))
			}
		}()

		// Use the socket directly - SendCommandStreaming uses core.GetSocketPath()
		// which we can't easily override, so test the parsing logic directly
		conn, err := net.Dial("unix", socketPath)
		if err != nil {
			t.Fatalf("failed to connect: %v", err)
		}

		_, err = conn.Write([]byte("TEST_CMD\n"))
		if err != nil {
			t.Fatalf("failed to write: %v", err)
		}

		// Read and parse the responses (mirroring SendCommandStreaming logic)
		reader := bufio.NewReader(conn)
		var parsed []ResponseMessage
		for {
			line, err := reader.ReadBytes('\n')
			if err != nil {
				if err == io.EOF {
					break
				}
				t.Fatalf("read error: %v", err)
			}
			if len(line) <= 1 {
				continue
			}
			var msg ResponseMessage
			if err := json.Unmarshal(line, &msg); err != nil {
				continue
			}
			parsed = append(parsed, msg)
		}
		conn.Close()

		<-serverDone

		if len(parsed) != 3 {
			t.Fatalf("expected 3 messages, got %d", len(parsed))
		}
		if parsed[0].Status != "INFO" || parsed[0].Message != "Starting..." {
			t.Errorf("first message = %+v, want {Starting..., INFO}", parsed[0])
		}
		if parsed[1].Status != "WARN" || parsed[1].Message != "Something odd" {
			t.Errorf("second message = %+v, want {Something odd, WARN}", parsed[1])
		}
		if parsed[2].Status != "ERROR" || parsed[2].Message != "Failed!" {
			t.Errorf("third message = %+v, want {Failed!, ERROR}", parsed[2])
		}
	})

	t.Run("handles EOF cleanly", func(t *testing.T) {
		socketPath := os.TempDir() + "/overseer-test-eof.sock"
		os.Remove(socketPath)

		listener, err := net.Listen("unix", socketPath)
		if err != nil {
			t.Fatalf("failed to create listener: %v", err)
		}
		defer listener.Close()
		defer os.Remove(socketPath)

		// Server closes immediately after accepting (EOF)
		serverDone := make(chan struct{})
		go func() {
			defer close(serverDone)
			conn, err := listener.Accept()
			if err != nil {
				return
			}
			// Read the command then close
			reader := bufio.NewReader(conn)
			reader.ReadString('\n')
			conn.Close()
		}()

		conn, err := net.Dial("unix", socketPath)
		if err != nil {
			t.Fatalf("failed to connect: %v", err)
		}

		conn.Write([]byte("TEST\n"))

		// Read until EOF — should not error
		reader := bufio.NewReader(conn)
		for {
			_, err := reader.ReadBytes('\n')
			if err != nil {
				if err == io.EOF {
					break // Expected
				}
				t.Fatalf("unexpected error: %v", err)
			}
		}
		conn.Close()

		select {
		case <-serverDone:
		case <-time.After(2 * time.Second):
			t.Fatal("server goroutine did not finish")
		}
	})
}

func TestHandleConnection_IPC_EmptyCommand(t *testing.T) {
	quietLoggerIPC(t)

	oldConfig := core.Config
	defer func() { core.Config = oldConfig }()
	core.Config = &core.Configuration{}

	d := &Daemon{
		tunnels:       make(map[string]Tunnel),
		askpassTokens: make(map[string]string),
		logBroadcast:  NewLogBroadcaster(100),
		companionMgr:  NewCompanionManager(),
	}

	clientConn, serverConn := net.Pipe()

	done := make(chan struct{})
	go func() {
		defer close(done)
		d.handleConnection(serverConn)
	}()

	// Send empty line
	clientConn.Write([]byte("\n"))

	// handleConnection should return without sending anything meaningful
	data, _ := io.ReadAll(clientConn)
	clientConn.Close()
	<-done

	// Empty command returns no JSON (just closes)
	if len(data) > 0 {
		t.Errorf("expected no response for empty command, got %q", string(data))
	}
}

func TestHandleConnection_IPC_VersionCommand(t *testing.T) {
	quietLoggerIPC(t)

	oldConfig := core.Config
	defer func() { core.Config = oldConfig }()
	core.Config = &core.Configuration{}

	d := &Daemon{
		tunnels:       make(map[string]Tunnel),
		askpassTokens: make(map[string]string),
		logBroadcast:  NewLogBroadcaster(100),
		companionMgr:  NewCompanionManager(),
	}

	resp := sendIPCCommand(t, d, "VERSION")

	if len(resp.Messages) < 1 {
		t.Fatal("expected at least 1 message for VERSION")
	}
	if resp.Messages[0].Status != "INFO" {
		t.Errorf("expected INFO status, got %q", resp.Messages[0].Status)
	}
	if resp.Data == nil {
		t.Error("expected data to contain version info")
	}
}

func TestHandleConnection_IPC_StatusCommand(t *testing.T) {
	quietLoggerIPC(t)

	oldConfig := core.Config
	defer func() { core.Config = oldConfig }()
	core.Config = &core.Configuration{}

	d := &Daemon{
		tunnels:       make(map[string]Tunnel),
		askpassTokens: make(map[string]string),
		logBroadcast:  NewLogBroadcaster(100),
		companionMgr:  NewCompanionManager(),
	}

	resp := sendIPCCommand(t, d, "STATUS")

	if len(resp.Messages) < 1 {
		t.Fatal("expected at least 1 message for STATUS")
	}
	// Empty tunnels returns WARN
	if resp.Messages[0].Status != "WARN" {
		t.Errorf("expected WARN status for empty tunnels, got %q", resp.Messages[0].Status)
	}
}

func TestHandleConnection_IPC_CompanionStatusCommand(t *testing.T) {
	quietLoggerIPC(t)

	oldConfig := core.Config
	defer func() { core.Config = oldConfig }()
	core.Config = &core.Configuration{}

	d := &Daemon{
		tunnels:       make(map[string]Tunnel),
		askpassTokens: make(map[string]string),
		logBroadcast:  NewLogBroadcaster(100),
		companionMgr:  NewCompanionManager(),
	}

	resp := sendIPCCommand(t, d, "COMPANION_STATUS")

	if len(resp.Messages) < 1 {
		t.Fatal("expected at least 1 message")
	}
	if resp.Messages[0].Status != "INFO" {
		t.Errorf("expected INFO status, got %q", resp.Messages[0].Status)
	}
}

func TestHandleConnection_IPC_AskpassMissingArgs(t *testing.T) {
	quietLoggerIPC(t)

	oldConfig := core.Config
	defer func() { core.Config = oldConfig }()
	core.Config = &core.Configuration{}

	d := &Daemon{
		tunnels:       make(map[string]Tunnel),
		askpassTokens: make(map[string]string),
		logBroadcast:  NewLogBroadcaster(100),
		companionMgr:  NewCompanionManager(),
	}

	resp := sendIPCCommand(t, d, "ASKPASS")

	if len(resp.Messages) < 1 {
		t.Fatal("expected at least 1 message")
	}
	if resp.Messages[0].Status != "ERROR" {
		t.Errorf("expected ERROR status, got %q", resp.Messages[0].Status)
	}
	if !strings.Contains(resp.Messages[0].Message, "Invalid ASKPASS") {
		t.Errorf("expected 'Invalid ASKPASS' message, got %q", resp.Messages[0].Message)
	}
}

func TestHandleConnection_IPC_CompanionInitMissingArgs(t *testing.T) {
	quietLoggerIPC(t)

	oldConfig := core.Config
	defer func() { core.Config = oldConfig }()
	core.Config = &core.Configuration{}

	d := &Daemon{
		tunnels:       make(map[string]Tunnel),
		askpassTokens: make(map[string]string),
		logBroadcast:  NewLogBroadcaster(100),
		companionMgr:  NewCompanionManager(),
	}

	resp := sendIPCCommand(t, d, "COMPANION_INIT server1")

	if len(resp.Messages) < 1 {
		t.Fatal("expected at least 1 message")
	}
	if resp.Messages[0].Status != "ERROR" {
		t.Errorf("expected ERROR status, got %q", resp.Messages[0].Status)
	}
}

func TestHandleConnection_IPC_CompanionAttachMissingArgs(t *testing.T) {
	quietLoggerIPC(t)

	oldConfig := core.Config
	defer func() { core.Config = oldConfig }()
	core.Config = &core.Configuration{}

	d := &Daemon{
		tunnels:       make(map[string]Tunnel),
		askpassTokens: make(map[string]string),
		logBroadcast:  NewLogBroadcaster(100),
		companionMgr:  NewCompanionManager(),
	}

	resp := sendIPCCommand(t, d, "COMPANION_ATTACH")

	if len(resp.Messages) < 1 {
		t.Fatal("expected at least 1 message")
	}
	if resp.Messages[0].Status != "ERROR" {
		t.Errorf("expected ERROR status, got %q", resp.Messages[0].Status)
	}
}

func TestHandleConnection_IPC_CompanionStartMissingArgs(t *testing.T) {
	quietLoggerIPC(t)

	oldConfig := core.Config
	defer func() { core.Config = oldConfig }()
	core.Config = &core.Configuration{}

	d := &Daemon{
		tunnels:       make(map[string]Tunnel),
		askpassTokens: make(map[string]string),
		logBroadcast:  NewLogBroadcaster(100),
		companionMgr:  NewCompanionManager(),
	}

	resp := sendIPCCommand(t, d, "COMPANION_START")

	if len(resp.Messages) < 1 {
		t.Fatal("expected at least 1 message")
	}
	if resp.Messages[0].Status != "ERROR" {
		t.Errorf("expected ERROR status, got %q", resp.Messages[0].Status)
	}
}

func TestHandleConnection_IPC_CompanionStopMissingArgs(t *testing.T) {
	quietLoggerIPC(t)

	oldConfig := core.Config
	defer func() { core.Config = oldConfig }()
	core.Config = &core.Configuration{}

	d := &Daemon{
		tunnels:       make(map[string]Tunnel),
		askpassTokens: make(map[string]string),
		logBroadcast:  NewLogBroadcaster(100),
		companionMgr:  NewCompanionManager(),
	}

	resp := sendIPCCommand(t, d, "COMPANION_STOP")

	if len(resp.Messages) < 1 {
		t.Fatal("expected at least 1 message")
	}
	if resp.Messages[0].Status != "ERROR" {
		t.Errorf("expected ERROR status, got %q", resp.Messages[0].Status)
	}
}

func TestHandleConnection_IPC_CompanionRestartMissingArgs(t *testing.T) {
	quietLoggerIPC(t)

	oldConfig := core.Config
	defer func() { core.Config = oldConfig }()
	core.Config = &core.Configuration{}

	d := &Daemon{
		tunnels:       make(map[string]Tunnel),
		askpassTokens: make(map[string]string),
		logBroadcast:  NewLogBroadcaster(100),
		companionMgr:  NewCompanionManager(),
	}

	resp := sendIPCCommand(t, d, "COMPANION_RESTART")

	if len(resp.Messages) < 1 {
		t.Fatal("expected at least 1 message")
	}
	if resp.Messages[0].Status != "ERROR" {
		t.Errorf("expected ERROR status, got %q", resp.Messages[0].Status)
	}
}

func TestHandleConnection_IPC_CompanionStartTunnelNotRunning(t *testing.T) {
	quietLoggerIPC(t)

	oldConfig := core.Config
	defer func() { core.Config = oldConfig }()
	core.Config = &core.Configuration{}

	d := &Daemon{
		tunnels:       make(map[string]Tunnel),
		askpassTokens: make(map[string]string),
		logBroadcast:  NewLogBroadcaster(100),
		companionMgr:  NewCompanionManager(),
	}

	resp := sendIPCCommand(t, d, "COMPANION_START server1 comp1")

	if len(resp.Messages) < 1 {
		t.Fatal("expected at least 1 message")
	}
	if resp.Messages[0].Status != "ERROR" {
		t.Errorf("expected ERROR status, got %q", resp.Messages[0].Status)
	}
	if !strings.Contains(resp.Messages[0].Message, "not running") {
		t.Errorf("expected 'not running' message, got %q", resp.Messages[0].Message)
	}
}

func TestHandleConnection_IPC_ContextStatusNoOrchestrator(t *testing.T) {
	quietLoggerIPC(t)

	old := stateOrchestrator
	stateOrchestrator = nil
	defer func() { stateOrchestrator = old }()

	oldConfig := core.Config
	defer func() { core.Config = oldConfig }()
	core.Config = &core.Configuration{}

	d := &Daemon{
		tunnels:       make(map[string]Tunnel),
		askpassTokens: make(map[string]string),
		logBroadcast:  NewLogBroadcaster(100),
		companionMgr:  NewCompanionManager(),
	}

	resp := sendIPCCommand(t, d, "CONTEXT_STATUS")

	if len(resp.Messages) < 1 {
		t.Fatal("expected at least 1 message")
	}
	if resp.Messages[0].Status != "ERROR" {
		t.Errorf("expected ERROR status, got %q", resp.Messages[0].Status)
	}
	if !strings.Contains(resp.Messages[0].Message, "not initialized") {
		t.Errorf("expected 'not initialized' message, got %q", resp.Messages[0].Message)
	}
}

func TestHandleConnection_IPC_SSHConnectNoArgs(t *testing.T) {
	quietLoggerIPC(t)

	oldConfig := core.Config
	defer func() { core.Config = oldConfig }()
	core.Config = &core.Configuration{}

	d := &Daemon{
		tunnels:       make(map[string]Tunnel),
		askpassTokens: make(map[string]string),
		logBroadcast:  NewLogBroadcaster(100),
		companionMgr:  NewCompanionManager(),
	}

	// SSH_CONNECT with no args — no alias provided, response has no messages
	resp := sendIPCCommand(t, d, "SSH_CONNECT")

	if len(resp.Messages) != 0 {
		t.Errorf("expected 0 messages for SSH_CONNECT with no args, got %d: %+v",
			len(resp.Messages), resp.Messages)
	}
}

func TestHandleConnection_IPC_SSHDisconnectNoArgs(t *testing.T) {
	quietLoggerIPC(t)

	oldConfig := core.Config
	defer func() { core.Config = oldConfig }()
	core.Config = &core.Configuration{}

	d := &Daemon{
		tunnels:       make(map[string]Tunnel),
		askpassTokens: make(map[string]string),
		logBroadcast:  NewLogBroadcaster(100),
		companionMgr:  NewCompanionManager(),
	}

	// SSH_DISCONNECT with no args — no alias, response has no messages
	resp := sendIPCCommand(t, d, "SSH_DISCONNECT")

	if len(resp.Messages) != 0 {
		t.Errorf("expected 0 messages for SSH_DISCONNECT with no args, got %d: %+v",
			len(resp.Messages), resp.Messages)
	}
}

func TestHandleConnection_IPC_CompanionRestartTunnelNotRunning(t *testing.T) {
	quietLoggerIPC(t)

	oldConfig := core.Config
	defer func() { core.Config = oldConfig }()
	core.Config = &core.Configuration{}

	d := &Daemon{
		tunnels:       make(map[string]Tunnel),
		askpassTokens: make(map[string]string),
		logBroadcast:  NewLogBroadcaster(100),
		companionMgr:  NewCompanionManager(),
	}

	resp := sendIPCCommand(t, d, "COMPANION_RESTART server1 comp1")

	if len(resp.Messages) < 1 {
		t.Fatal("expected at least 1 message")
	}
	if resp.Messages[0].Status != "ERROR" {
		t.Errorf("expected ERROR status, got %q", resp.Messages[0].Status)
	}
	if !strings.Contains(resp.Messages[0].Message, "not running") {
		t.Errorf("expected 'not running' message, got %q", resp.Messages[0].Message)
	}
}

func TestHandleConnection_IPC_AskpassValidToken(t *testing.T) {
	quietLoggerIPC(t)

	oldConfig := core.Config
	defer func() { core.Config = oldConfig }()
	core.Config = &core.Configuration{}

	d := &Daemon{
		tunnels:       make(map[string]Tunnel),
		askpassTokens: map[string]string{"tok123": "server1"},
		logBroadcast:  NewLogBroadcaster(100),
		companionMgr:  NewCompanionManager(),
	}

	// Valid token but no keyring entry — exercises the token validation path
	resp := sendIPCCommand(t, d, "ASKPASS server1 tok123")

	// We expect either INFO (keyring hit) or ERROR (keyring miss), but the token
	// validation path itself should be exercised without panicking.
	if len(resp.Messages) < 1 {
		t.Fatal("expected at least 1 message")
	}
}

func TestHandleConnection_IPC_SSHReconnectNoArgs(t *testing.T) {
	quietLoggerIPC(t)

	oldConfig := core.Config
	defer func() { core.Config = oldConfig }()
	core.Config = &core.Configuration{}

	d := &Daemon{
		tunnels:       make(map[string]Tunnel),
		askpassTokens: make(map[string]string),
		logBroadcast:  NewLogBroadcaster(100),
		companionMgr:  NewCompanionManager(),
	}

	// SSH_RECONNECT with no args — no alias, empty response
	resp := sendIPCCommand(t, d, "SSH_RECONNECT")

	if len(resp.Messages) != 0 {
		t.Errorf("expected 0 messages for SSH_RECONNECT with no args, got %d: %+v",
			len(resp.Messages), resp.Messages)
	}
}

func TestHandleConnection_IPC_CompanionStopWithAlias(t *testing.T) {
	quietLoggerIPC(t)

	oldConfig := core.Config
	defer func() { core.Config = oldConfig }()
	core.Config = &core.Configuration{}

	d := &Daemon{
		tunnels:       make(map[string]Tunnel),
		askpassTokens: make(map[string]string),
		logBroadcast:  NewLogBroadcaster(100),
		companionMgr:  NewCompanionManager(),
	}

	// Companion stop with alias+name but no running companion — should succeed
	resp := sendIPCCommand(t, d, "COMPANION_STOP server1 comp1")

	if len(resp.Messages) < 1 {
		t.Fatal("expected at least 1 message")
	}
	if resp.Messages[0].Status != "INFO" {
		t.Errorf("expected INFO status, got %q", resp.Messages[0].Status)
	}
}

func TestHandleConnection_IPC_ContextStatusWithLimit(t *testing.T) {
	quietLoggerIPC(t)

	old := stateOrchestrator
	stateOrchestrator = nil
	defer func() { stateOrchestrator = old }()

	oldConfig := core.Config
	defer func() { core.Config = oldConfig }()
	core.Config = &core.Configuration{}

	d := &Daemon{
		tunnels:       make(map[string]Tunnel),
		askpassTokens: make(map[string]string),
		logBroadcast:  NewLogBroadcaster(100),
		companionMgr:  NewCompanionManager(),
	}

	// CONTEXT_STATUS with a numeric limit argument
	resp := sendIPCCommand(t, d, "CONTEXT_STATUS 5")

	if len(resp.Messages) < 1 {
		t.Fatal("expected at least 1 message")
	}
	if resp.Messages[0].Status != "ERROR" {
		t.Errorf("expected ERROR (no orchestrator), got %q", resp.Messages[0].Status)
	}
}

func TestHandleConnection_IPC_LogsCommand(t *testing.T) {
	quietLoggerIPC(t)

	// Ensure stateOrchestrator is nil to use fallback path
	old := stateOrchestrator
	stateOrchestrator = nil
	defer func() { stateOrchestrator = old }()

	d := &Daemon{
		tunnels:       make(map[string]Tunnel),
		askpassTokens: make(map[string]string),
		logBroadcast:  NewLogBroadcaster(100),
		companionMgr:  NewCompanionManager(),
	}

	clientConn, serverConn := net.Pipe()

	done := make(chan struct{})
	go func() {
		defer close(done)
		d.handleConnection(serverConn)
	}()

	// Send LOGS command with no_history flag
	clientConn.Write([]byte("LOGS no_history\n"))

	reader := bufio.NewReader(clientConn)
	line, err := reader.ReadString('\n')
	if err != nil {
		t.Fatalf("failed to read: %v", err)
	}
	if !strings.Contains(line, "Connected to overseer daemon logs") {
		t.Errorf("expected logs initial message, got %q", line)
	}

	clientConn.Close()
	<-done
}

func TestHandleConnection_IPC_AttachCommand(t *testing.T) {
	quietLoggerIPC(t)

	d := &Daemon{
		tunnels:       make(map[string]Tunnel),
		askpassTokens: make(map[string]string),
		logBroadcast:  NewLogBroadcaster(100),
		companionMgr:  NewCompanionManager(),
	}

	clientConn, serverConn := net.Pipe()

	done := make(chan struct{})
	go func() {
		defer close(done)
		d.handleConnection(serverConn)
	}()

	// Send ATTACH command with no_history flag
	clientConn.Write([]byte("ATTACH no_history\n"))

	reader := bufio.NewReader(clientConn)
	line, err := reader.ReadString('\n')
	if err != nil {
		t.Fatalf("failed to read: %v", err)
	}
	if !strings.Contains(line, "Attached to overseer daemon") {
		t.Errorf("expected attach initial message, got %q", line)
	}

	clientConn.Close()
	<-done
}

func TestHandleConnection_IPC_SSHDisconnectWithRunningTunnel(t *testing.T) {
	quietLoggerIPC(t)

	oldConfig := core.Config
	t.Cleanup(func() { core.Config = oldConfig })
	core.Config = &core.Configuration{
		Companion: core.CompanionSettings{HistorySize: 50},
	}

	d := New()
	// Put a tunnel in the map with PID 0 (no actual process)
	d.tunnels["test-alias"] = Tunnel{
		Hostname:  "test-alias",
		Pid:       0,
		Cmd:       nil,
		StartDate: time.Now(),
		State:     StateConnected,
	}

	resp := sendIPCCommand(t, d, "SSH_DISCONNECT test-alias")
	if len(resp.Messages) == 0 {
		t.Fatal("expected at least one message")
	}
	// Should get an error since PID is 0 (no process to kill)
	// but it still goes through the stopTunnel path
}

func TestHandleConnection_IPC_SSHDisconnectAllWithTunnels(t *testing.T) {
	quietLoggerIPC(t)

	oldConfig := core.Config
	t.Cleanup(func() { core.Config = oldConfig })
	core.Config = &core.Configuration{
		Companion: core.CompanionSettings{HistorySize: 50},
	}

	d := New()
	d.tunnels["alias1"] = Tunnel{Hostname: "alias1", Pid: 0, State: StateConnected, StartDate: time.Now()}
	d.tunnels["alias2"] = Tunnel{Hostname: "alias2", Pid: 0, State: StateConnected, StartDate: time.Now()}

	resp := sendIPCCommand(t, d, "SSH_DISCONNECT_ALL")
	if len(resp.Messages) < 2 {
		t.Errorf("expected at least 2 messages for 2 tunnels, got %d", len(resp.Messages))
	}
}

func TestHandleConnection_IPC_LogsWithHistoryCount(t *testing.T) {
	quietLoggerIPC(t)

	old := stateOrchestrator
	t.Cleanup(func() { stateOrchestrator = old })
	stateOrchestrator = nil

	d := &Daemon{
		tunnels:       make(map[string]Tunnel),
		askpassTokens: make(map[string]string),
		logBroadcast:  NewLogBroadcaster(100),
		companionMgr:  NewCompanionManager(),
	}

	d.logBroadcast.Broadcast("history line 1\n")
	d.logBroadcast.Broadcast("history line 2\n")
	d.logBroadcast.Broadcast("history line 3\n")

	clientConn, serverConn := net.Pipe()
	done := make(chan struct{})
	go func() {
		defer close(done)
		d.handleConnection(serverConn)
	}()

	clientConn.Write([]byte("LOGS 2\n"))

	reader := bufio.NewReader(clientConn)
	line, err := reader.ReadString('\n')
	if err != nil {
		t.Fatalf("failed to read: %v", err)
	}
	if !strings.Contains(line, "Connected to overseer daemon logs") {
		t.Errorf("expected logs message, got %q", line)
	}

	clientConn.Close()
	<-done
}

func TestHandleConnection_IPC_AttachNoHistory(t *testing.T) {
	quietLoggerIPC(t)

	d := &Daemon{
		tunnels:       make(map[string]Tunnel),
		askpassTokens: make(map[string]string),
		logBroadcast:  NewLogBroadcaster(100),
		companionMgr:  NewCompanionManager(),
	}

	clientConn, serverConn := net.Pipe()
	done := make(chan struct{})
	go func() {
		defer close(done)
		d.handleConnection(serverConn)
	}()

	clientConn.Write([]byte("ATTACH 10 no_history\n"))

	reader := bufio.NewReader(clientConn)
	line, err := reader.ReadString('\n')
	if err != nil {
		t.Fatalf("failed to read: %v", err)
	}
	if !strings.Contains(line, "Attached to overseer daemon") {
		t.Errorf("expected attach message, got %q", line)
	}

	clientConn.Close()
	<-done
}

func TestHandleConnection_IPC_CompanionAttachWithConfig(t *testing.T) {
	quietLoggerIPC(t)

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

	d := &Daemon{
		tunnels:       make(map[string]Tunnel),
		askpassTokens: make(map[string]string),
		logBroadcast:  NewLogBroadcaster(100),
		companionMgr:  NewCompanionManager(),
	}

	clientConn, serverConn := net.Pipe()
	done := make(chan struct{})
	go func() {
		defer close(done)
		d.handleConnection(serverConn)
	}()

	clientConn.Write([]byte("COMPANION_ATTACH my-tunnel my-comp no_history\n"))

	reader := bufio.NewReader(clientConn)
	line, err := reader.ReadString('\n')
	if err != nil {
		t.Fatalf("failed to read: %v", err)
	}
	if !strings.Contains(line, "Attached to companion") {
		t.Errorf("expected 'Attached to companion' message, got %q", line)
	}

	clientConn.Close()
	<-done
}

func TestHandleConnection_IPC_CompanionAttachRunning(t *testing.T) {
	quietLoggerIPC(t)

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
	broadcaster.Broadcast("output line 1\n")
	broadcaster.Broadcast("output line 2\n")

	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)

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

	d := &Daemon{
		tunnels:       make(map[string]Tunnel),
		askpassTokens: make(map[string]string),
		logBroadcast:  NewLogBroadcaster(100),
		companionMgr:  cm,
	}

	clientConn, serverConn := net.Pipe()
	done := make(chan struct{})
	go func() {
		defer close(done)
		d.handleConnection(serverConn)
	}()

	clientConn.Write([]byte("COMPANION_ATTACH my-tunnel my-comp 5\n"))

	reader := bufio.NewReader(clientConn)
	line, err := reader.ReadString('\n')
	if err != nil {
		t.Fatalf("failed to read: %v", err)
	}
	if !strings.Contains(line, "Attached to companion") {
		t.Errorf("expected 'Attached to companion' message, got %q", line)
	}

	clientConn.Close()
	<-done
}

func TestHandleConnection_IPC_CompanionAttachRunningNoHistory(t *testing.T) {
	quietLoggerIPC(t)

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
	broadcaster.Broadcast("output line\n")

	ctx2, cancel2 := context.WithCancel(context.Background())
	t.Cleanup(cancel2)

	cm.companions["my-tunnel"] = map[string]*CompanionProcess{
		"my-comp": {
			Name:        "my-comp",
			TunnelAlias: "my-tunnel",
			Pid:         12345,
			State:       CompanionStateRunning,
			output:      broadcaster,
			Config:      core.CompanionConfig{Name: "my-comp", Command: "echo hello"},
			ctx:         ctx2,
			cancel:      cancel2,
		},
	}

	d := &Daemon{
		tunnels:       make(map[string]Tunnel),
		askpassTokens: make(map[string]string),
		logBroadcast:  NewLogBroadcaster(100),
		companionMgr:  cm,
	}

	clientConn, serverConn := net.Pipe()
	done := make(chan struct{})
	go func() {
		defer close(done)
		d.handleConnection(serverConn)
	}()

	clientConn.Write([]byte("COMPANION_ATTACH my-tunnel my-comp 5 no_history\n"))

	reader := bufio.NewReader(clientConn)
	line, err := reader.ReadString('\n')
	if err != nil {
		t.Fatalf("failed to read: %v", err)
	}
	if !strings.Contains(line, "Attached to companion") {
		t.Errorf("expected 'Attached to companion' message, got %q", line)
	}

	clientConn.Close()
	<-done
}

func TestHandleConnection_IPC_CompanionStartWithConfig(t *testing.T) {
	quietLoggerIPC(t)

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

	d := New()
	d.tunnels["my-tunnel"] = Tunnel{Hostname: "my-tunnel", State: StateConnected}

	// COMPANION_START exercises StartSingleCompanion which calls runCompanion
	resp := sendIPCCommand(t, d, "COMPANION_START my-tunnel my-comp")
	_ = resp
}

func TestHandleConnection_IPC_CompanionRestartWithConfig(t *testing.T) {
	quietLoggerIPC(t)

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

	d := New()
	d.tunnels["my-tunnel"] = Tunnel{Hostname: "my-tunnel", State: StateConnected}

	resp := sendIPCCommand(t, d, "COMPANION_RESTART my-tunnel my-comp")
	_ = resp
}

func TestHandleConnection_IPC_SSHReconnectWithRunningTunnel(t *testing.T) {
	quietLoggerIPC(t)

	oldConfig := core.Config
	t.Cleanup(func() { core.Config = oldConfig })
	core.Config = &core.Configuration{
		Companion: core.CompanionSettings{HistorySize: 50},
		Tunnels:   map[string]*core.TunnelConfig{},
	}

	d := New()
	d.tunnels["my-tunnel"] = Tunnel{
		Hostname:  "my-tunnel",
		Pid:       0,
		Cmd:       nil,
		StartDate: time.Now(),
		State:     StateConnected,
		Environment: map[string]string{"OVERSEER_TAG": "test-tag"},
	}

	resp := sendIPCCommand(t, d, "SSH_RECONNECT my-tunnel")
	if len(resp.Messages) == 0 {
		t.Fatal("expected at least one message")
	}
}

func TestHandleConnection_IPC_SensitiveCommandMasking(t *testing.T) {
	quietLoggerIPC(t)

	d := &Daemon{
		tunnels:       make(map[string]Tunnel),
		askpassTokens: make(map[string]string),
		logBroadcast:  NewLogBroadcaster(100),
		companionMgr:  NewCompanionManager(),
	}

	// ASKPASS with 2 args exercises masking path
	resp := sendIPCCommand(t, d, "ASKPASS test-alias invalid-token")
	if len(resp.Messages) == 0 {
		t.Fatal("expected at least one message")
	}

	// COMPANION_INIT with 3 args exercises masking path
	resp = sendIPCCommand(t, d, "COMPANION_INIT tunnel1 comp1 some-token")
	if len(resp.Messages) == 0 {
		t.Fatal("expected at least one message for COMPANION_INIT")
	}
}
