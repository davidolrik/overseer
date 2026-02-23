package daemon

import (
	"encoding/json"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"

	"go.olrik.dev/overseer/internal/core"
)

// shortTempDir creates a short temp directory to avoid macOS socket path length limits.
func shortTempDir(t *testing.T) string {
	t.Helper()
	dir, err := os.MkdirTemp("/tmp", "ov-")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	t.Cleanup(func() { os.RemoveAll(dir) })
	return dir
}

// setupSocketServer creates a Unix socket listener at the daemon's socket path.
// Returns the listener and a cleanup function.
func setupSocketServer(t *testing.T) net.Listener {
	t.Helper()

	tmpDir := shortTempDir(t)
	oldConfig := core.Config
	t.Cleanup(func() { core.Config = oldConfig })
	core.Config = &core.Configuration{
		ConfigPath: tmpDir,
	}

	socketPath := core.GetSocketPath()
	listener, err := net.Listen("unix", socketPath)
	if err != nil {
		t.Fatalf("failed to create Unix listener: %v", err)
	}
	t.Cleanup(func() { listener.Close() })

	return listener
}

func TestSendCommand_Success(t *testing.T) {
	quietLogger(t)

	listener := setupSocketServer(t)

	// Server: accept one connection, read the command, write a valid Response JSON
	go func() {
		conn, err := listener.Accept()
		if err != nil {
			return
		}
		defer conn.Close()

		// Read the command (we don't need it for this test)
		buf := make([]byte, 1024)
		conn.Read(buf)

		// Send a valid Response
		resp := Response{
			Messages: []ResponseMessage{
				{Message: "OK", Status: "INFO"},
			},
		}
		data, _ := json.Marshal(resp)
		conn.Write(data)
	}()

	resp, err := SendCommand("STATUS")
	if err != nil {
		t.Fatalf("SendCommand failed: %v", err)
	}
	if len(resp.Messages) == 0 {
		t.Fatal("expected at least one message")
	}
	if resp.Messages[0].Status != "INFO" {
		t.Errorf("expected INFO status, got %q", resp.Messages[0].Status)
	}
}

func TestSendCommand_ConnectionRefused(t *testing.T) {
	quietLogger(t)

	tmpDir := shortTempDir(t)
	oldConfig := core.Config
	t.Cleanup(func() { core.Config = oldConfig })
	core.Config = &core.Configuration{
		ConfigPath: tmpDir,
	}

	// No listener on the socket — should get connection error
	_, err := SendCommand("STATUS")
	if err == nil {
		t.Error("expected error when no listener exists")
	}
}

func TestSendCommandWithTimeout_Timeout(t *testing.T) {
	quietLogger(t)

	listener := setupSocketServer(t)

	// Server: accept connection but never respond
	go func() {
		conn, err := listener.Accept()
		if err != nil {
			return
		}
		defer conn.Close()
		// Read command but hang forever
		buf := make([]byte, 1024)
		conn.Read(buf)
		time.Sleep(10 * time.Second)
	}()

	start := time.Now()
	_, err := sendCommandWithTimeout("STATUS", 200*time.Millisecond)
	elapsed := time.Since(start)

	if err == nil {
		t.Error("expected timeout error")
	}
	if elapsed > 5*time.Second {
		t.Errorf("expected quick timeout, took %s", elapsed)
	}
}

func TestSendCommandWithTimeout_Success(t *testing.T) {
	quietLogger(t)

	listener := setupSocketServer(t)

	go func() {
		conn, err := listener.Accept()
		if err != nil {
			return
		}
		defer conn.Close()

		buf := make([]byte, 1024)
		conn.Read(buf)

		resp := Response{
			Messages: []ResponseMessage{
				{Message: "OK", Status: "INFO"},
			},
		}
		data, _ := json.Marshal(resp)
		conn.Write(data)
	}()

	resp, err := sendCommandWithTimeout("STATUS", 5*time.Second)
	if err != nil {
		t.Fatalf("sendCommandWithTimeout failed: %v", err)
	}
	if len(resp.Messages) == 0 {
		t.Fatal("expected at least one message")
	}
}

func TestSendCommandStreaming_MultipleMessages(t *testing.T) {
	quietLogger(t)

	listener := setupSocketServer(t)

	go func() {
		conn, err := listener.Accept()
		if err != nil {
			return
		}
		defer conn.Close()

		buf := make([]byte, 1024)
		conn.Read(buf)

		// Send 3 JSON messages, one per line
		messages := []ResponseMessage{
			{Message: "Step 1", Status: "INFO"},
			{Message: "Step 2", Status: "INFO"},
			{Message: "Done", Status: "INFO"},
		}
		for _, msg := range messages {
			data, _ := json.Marshal(msg)
			fmt.Fprintf(conn, "%s\n", data)
		}
	}()

	err := SendCommandStreaming("START test")
	if err != nil {
		t.Fatalf("SendCommandStreaming failed: %v", err)
	}
}

func TestSendCommandStreaming_ConnectionRefused(t *testing.T) {
	quietLogger(t)

	tmpDir := shortTempDir(t)
	oldConfig := core.Config
	t.Cleanup(func() { core.Config = oldConfig })
	core.Config = &core.Configuration{
		ConfigPath: tmpDir,
	}

	err := SendCommandStreaming("START test")
	if err == nil {
		t.Error("expected error when no listener exists")
	}
}

func TestWaitForDaemonStop_AlreadyStopped(t *testing.T) {
	quietLogger(t)

	tmpDir := shortTempDir(t)
	oldConfig := core.Config
	t.Cleanup(func() { core.Config = oldConfig })
	core.Config = &core.Configuration{
		ConfigPath: tmpDir,
	}

	// No listener at socket path — daemon is already stopped
	start := time.Now()
	err := WaitForDaemonStop()
	elapsed := time.Since(start)

	if err != nil {
		t.Errorf("expected nil error, got: %v", err)
	}
	if elapsed > 5*time.Second {
		t.Errorf("expected quick return, took %s", elapsed)
	}
}

func TestWaitForDaemonStop_StopsDuringWait(t *testing.T) {
	quietLogger(t)

	tmpDir := shortTempDir(t)
	oldConfig := core.Config
	t.Cleanup(func() { core.Config = oldConfig })
	core.Config = &core.Configuration{
		ConfigPath: tmpDir,
	}

	socketPath := core.GetSocketPath()
	listener, err := net.Listen("unix", socketPath)
	if err != nil {
		t.Fatalf("failed to create Unix listener: %v", err)
	}

	// Accept connections and respond to STATUS until stopped
	done := make(chan struct{})
	go func() {
		defer close(done)
		for {
			conn, err := listener.Accept()
			if err != nil {
				return // Listener closed
			}
			buf := make([]byte, 1024)
			conn.Read(buf)
			resp := Response{Messages: []ResponseMessage{{Message: "OK", Status: "INFO"}}}
			data, _ := json.Marshal(resp)
			conn.Write(data)
			conn.Close()
		}
	}()

	// Close the listener after a short delay to simulate daemon stopping
	go func() {
		time.Sleep(300 * time.Millisecond)
		listener.Close()
		os.Remove(socketPath)
	}()

	start := time.Now()
	err = WaitForDaemonStop()
	elapsed := time.Since(start)

	if err != nil {
		t.Errorf("expected nil error, got: %v", err)
	}
	if elapsed > 5*time.Second {
		t.Errorf("expected stop within 5s, took %s", elapsed)
	}
	<-done // Wait for server goroutine to exit
}

func TestSendCommand_InvalidJSON(t *testing.T) {
	quietLogger(t)

	listener := setupSocketServer(t)

	go func() {
		conn, err := listener.Accept()
		if err != nil {
			return
		}
		defer conn.Close()

		buf := make([]byte, 1024)
		conn.Read(buf)

		// Send invalid JSON
		conn.Write([]byte("not valid json"))
	}()

	_, err := SendCommand("STATUS")
	if err == nil {
		t.Error("expected error for invalid JSON response")
	}
}

func TestGetSocketPath(t *testing.T) {
	tmpDir := t.TempDir()
	oldConfig := core.Config
	t.Cleanup(func() { core.Config = oldConfig })
	core.Config = &core.Configuration{
		ConfigPath: tmpDir,
	}

	socketPath := core.GetSocketPath()
	expected := filepath.Join(tmpDir, core.SocketName)
	if socketPath != expected {
		t.Errorf("expected socket path %q, got %q", expected, socketPath)
	}
}
