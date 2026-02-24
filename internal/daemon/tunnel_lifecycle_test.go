package daemon

import (
	"bufio"
	"encoding/json"
	"fmt"
	"net"
	"os"
	"os/exec"
	"strings"
	"syscall"
	"testing"
	"time"

	"go.olrik.dev/overseer/internal/core"
	"go.olrik.dev/overseer/internal/testutil/sshserver"
)

// appendIdentityFile appends an IdentityFile directive to an SSH config.
func appendIdentityFile(t *testing.T, configPath, keyPath string) {
	t.Helper()
	existing, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatalf("failed to read SSH config: %v", err)
	}
	updated := strings.TrimRight(string(existing), "\n") +
		"\n    IdentityFile " + keyPath +
		"\n    IdentitiesOnly yes\n"
	if err := os.WriteFile(configPath, []byte(updated), 0600); err != nil {
		t.Fatalf("failed to write SSH config: %v", err)
	}
}

// setupTestDaemon creates a Daemon and test SSH server configured for pubkey auth.
// Returns the daemon, server, and SSH alias. Caller must defer srv.Stop().
func setupTestDaemon(t *testing.T) (*Daemon, *sshserver.Server, string) {
	t.Helper()
	quietLogger(t)

	tmpDir := t.TempDir()

	// Generate client keypair
	_, pubKey, keyPath := sshserver.GenerateClientKeyPair(t, tmpDir)

	// Start test SSH server with pubkey auth
	srv := sshserver.New(t, sshserver.Options{
		Username:       "testuser",
		AuthorizedKeys: sshserver.PublicKeys(pubKey),
	})
	srv.Start()

	alias := srv.Alias()

	// Append IdentityFile to generated SSH config
	appendIdentityFile(t, srv.SSHConfigPath(), keyPath)

	// Save and restore global config
	oldConfig := core.Config
	t.Cleanup(func() { core.Config = oldConfig })

	core.Config = &core.Configuration{
		ConfigPath: tmpDir,
		SSH: core.SSHConfig{
			ReconnectEnabled: false,
			MaxRetries:       0,
		},
		Companion: core.CompanionSettings{HistorySize: 50},
		Tunnels:   map[string]*core.TunnelConfig{},
	}

	// Create daemon
	d := New()
	d.SetSSHConfigFile(srv.SSHConfigPath())

	return d, srv, alias
}

func TestStartTunnel_PubKeySuccess(t *testing.T) {
	d, srv, alias := setupTestDaemon(t)
	defer srv.Stop()

	resp := d.startTunnel(alias, nil)

	// Check for success
	for _, msg := range resp.Messages {
		if msg.Status == "ERROR" {
			t.Fatalf("startTunnel returned error: %s", msg.Message)
		}
	}

	// Verify tunnel state
	d.mu.Lock()
	tunnel, exists := d.tunnels[alias]
	d.mu.Unlock()

	if !exists {
		t.Fatal("expected tunnel to exist in map")
	}
	if tunnel.State != StateConnected {
		t.Errorf("expected state %q, got %q", StateConnected, tunnel.State)
	}
	if tunnel.Pid <= 0 {
		t.Errorf("expected positive PID, got %d", tunnel.Pid)
	}
	if !strings.Contains(tunnel.ResolvedHost, "127.0.0.1") {
		t.Errorf("expected ResolvedHost to contain '127.0.0.1', got %q", tunnel.ResolvedHost)
	}

	// Cleanup
	d.stopTunnel(alias, false)
}

func TestStartTunnel_AlreadyRunning(t *testing.T) {
	d, srv, alias := setupTestDaemon(t)
	defer srv.Stop()

	resp := d.startTunnel(alias, nil)
	for _, msg := range resp.Messages {
		if msg.Status == "ERROR" {
			t.Fatalf("first startTunnel failed: %s", msg.Message)
		}
	}

	// Try starting the same alias again
	resp2 := d.startTunnel(alias, nil)

	found := false
	for _, msg := range resp2.Messages {
		if msg.Status == "ERROR" && strings.Contains(msg.Message, "already running") {
			found = true
		}
	}
	if !found {
		t.Errorf("expected 'already running' error, got messages: %+v", resp2.Messages)
	}

	d.stopTunnel(alias, false)
}

func TestStartTunnel_AuthFailure(t *testing.T) {
	quietLogger(t)
	tmpDir := t.TempDir()

	// Generate TWO different keypairs — server trusts one, client uses the other
	_, serverPubKey, _ := sshserver.GenerateClientKeyPair(t, tmpDir)
	_, _, wrongKeyPath := sshserver.GenerateClientKeyPair(t, t.TempDir())

	srv := sshserver.New(t, sshserver.Options{
		Username:       "testuser",
		AuthorizedKeys: sshserver.PublicKeys(serverPubKey),
	})
	srv.Start()
	defer srv.Stop()

	alias := srv.Alias()

	// Append the WRONG key to the SSH config
	appendIdentityFile(t, srv.SSHConfigPath(), wrongKeyPath)

	oldConfig := core.Config
	defer func() { core.Config = oldConfig }()

	core.Config = &core.Configuration{
		ConfigPath: tmpDir,
		SSH: core.SSHConfig{
			ReconnectEnabled: false,
			MaxRetries:       0,
		},
		Companion: core.CompanionSettings{HistorySize: 50},
		Tunnels:   map[string]*core.TunnelConfig{},
	}

	d := New()
	d.SetSSHConfigFile(srv.SSHConfigPath())

	resp := d.startTunnel(alias, nil)

	found := false
	for _, msg := range resp.Messages {
		if msg.Status == "ERROR" && strings.Contains(msg.Message, "authentication failed") {
			found = true
		}
	}
	if !found {
		t.Errorf("expected 'authentication failed' error, got messages: %+v", resp.Messages)
	}
}

func TestStopTunnel_Running(t *testing.T) {
	d, srv, alias := setupTestDaemon(t)
	defer srv.Stop()

	resp := d.startTunnel(alias, nil)
	for _, msg := range resp.Messages {
		if msg.Status == "ERROR" {
			t.Fatalf("startTunnel failed: %s", msg.Message)
		}
	}

	// Capture PID before stopping
	d.mu.Lock()
	pid := d.tunnels[alias].Pid
	d.mu.Unlock()

	// Give monitorTunnel goroutine time to reach cmd.Wait(). Without this,
	// stopTunnel holds the mutex and monitorTunnel can't reap the zombie.
	time.Sleep(200 * time.Millisecond)

	stopResp := d.stopTunnel(alias, false)
	for _, msg := range stopResp.Messages {
		if msg.Status == "ERROR" {
			t.Errorf("stopTunnel error: %s", msg.Message)
		}
	}

	// Verify tunnel removed from map
	d.mu.Lock()
	_, exists := d.tunnels[alias]
	d.mu.Unlock()
	if exists {
		t.Error("expected tunnel to be removed from map")
	}

	// Verify SSH process is dead
	process, err := os.FindProcess(pid)
	if err == nil {
		// Give a moment for process to fully terminate
		time.Sleep(200 * time.Millisecond)
		if err := process.Signal(syscall.Signal(0)); err == nil {
			t.Error("expected SSH process to be dead")
		}
	}
}

func TestStopTunnel_NotRunning(t *testing.T) {
	d, srv, _ := setupTestDaemon(t)
	defer srv.Stop()

	resp := d.stopTunnel("nonexistent", false)

	found := false
	for _, msg := range resp.Messages {
		if msg.Status == "ERROR" && strings.Contains(msg.Message, "not running") {
			found = true
		}
	}
	if !found {
		t.Errorf("expected 'not running' error, got messages: %+v", resp.Messages)
	}
}

func TestStopTunnel_ForReconnect(t *testing.T) {
	d, srv, alias := setupTestDaemon(t)
	defer srv.Stop()

	resp := d.startTunnel(alias, nil)
	for _, msg := range resp.Messages {
		if msg.Status == "ERROR" {
			t.Fatalf("startTunnel failed: %s", msg.Message)
		}
	}

	// Give monitorTunnel goroutine time to reach cmd.Wait()
	time.Sleep(200 * time.Millisecond)

	// Stop with forReconnect=true
	stopResp := d.stopTunnel(alias, true)
	for _, msg := range stopResp.Messages {
		if msg.Status == "ERROR" {
			t.Errorf("stopTunnel error: %s", msg.Message)
		}
	}

	// Tunnel should be removed from map (forReconnect only affects companion cleanup)
	d.mu.Lock()
	_, exists := d.tunnels[alias]
	d.mu.Unlock()
	if exists {
		t.Error("expected tunnel to be removed from map after forReconnect stop")
	}
}

func TestMonitorTunnel_ServerStop(t *testing.T) {
	d, srv, alias := setupTestDaemon(t)
	// Don't defer srv.Stop() — we stop it mid-test

	resp := d.startTunnel(alias, nil)
	for _, msg := range resp.Messages {
		if msg.Status == "ERROR" {
			t.Fatalf("startTunnel failed: %s", msg.Message)
		}
	}

	// monitorTunnel was already launched by startTunnelStreaming as a goroutine.
	// Stop the SSH server — this kills the SSH connection.
	srv.Stop()

	// Wait for monitor to detect disconnect and clean up.
	// With AutoReconnect=false and MaxRetries=0, the tunnel should be removed.
	deadline := time.After(10 * time.Second)
	for {
		d.mu.Lock()
		_, exists := d.tunnels[alias]
		d.mu.Unlock()
		if !exists {
			return // Monitor cleaned up — success
		}
		select {
		case <-deadline:
			t.Fatal("timed out waiting for monitorTunnel to clean up after server stop")
		case <-time.After(200 * time.Millisecond):
		}
	}
}

func TestMonitorTunnel_ManualStop(t *testing.T) {
	d, srv, alias := setupTestDaemon(t)
	defer srv.Stop()

	resp := d.startTunnel(alias, nil)
	for _, msg := range resp.Messages {
		if msg.Status == "ERROR" {
			t.Fatalf("startTunnel failed: %s", msg.Message)
		}
	}

	// Manually stop the tunnel — monitor goroutine should exit cleanly
	d.stopTunnel(alias, false)

	// Give monitor goroutine time to notice and exit
	time.Sleep(1 * time.Second)

	// Verify tunnel is gone (no panic, no leak)
	d.mu.Lock()
	_, exists := d.tunnels[alias]
	d.mu.Unlock()
	if exists {
		t.Error("expected tunnel to be cleaned up after manual stop")
	}
}

func TestGracefulTerminate_Process(t *testing.T) {
	// Start a long-running process in its own session (matches SSH tunnel behavior).
	cmd := exec.Command("sleep", "60")
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	if err := cmd.Start(); err != nil {
		t.Fatalf("failed to start sleep: %v", err)
	}

	// Start a goroutine to reap the child (as monitorTunnel does in production
	// with cmd.Wait()). Without this, the process becomes a zombie and Signal(0)
	// keeps returning nil even after SIGTERM/SIGKILL.
	go cmd.Wait()

	err := gracefulTerminate(cmd.Process, 2*time.Second, "test-sleep")
	if err != nil {
		t.Errorf("expected nil error, got %v", err)
	}

	// Verify process is dead
	time.Sleep(200 * time.Millisecond)
	if err := cmd.Process.Signal(syscall.Signal(0)); err == nil {
		t.Error("expected process to be dead after gracefulTerminate")
		cmd.Process.Kill()
	}
}

func TestGracefulTerminate_AlreadyDead(t *testing.T) {
	// Start a process in its own session that exits immediately
	cmd := exec.Command("true")
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	if err := cmd.Start(); err != nil {
		t.Fatalf("failed to start true: %v", err)
	}

	// Reap the child and wait for it to fully exit
	cmd.Wait()

	err := gracefulTerminate(cmd.Process, 2*time.Second, "test-dead")
	if err != nil {
		t.Errorf("expected nil error for already-dead process, got %v", err)
	}
}

func TestResolveJumpChain_NoProxy(t *testing.T) {
	_, srv, alias := setupTestDaemon(t)
	defer srv.Stop()

	chain := resolveJumpChain(alias, nil, srv.SSHConfigPath())
	if chain != nil {
		t.Errorf("expected nil jump chain for direct connection, got %v", chain)
	}
}

func TestHandleConnection_IPC_StatusVersion(t *testing.T) {
	oldConfig := core.Config
	defer func() { core.Config = oldConfig }()
	core.Config = &core.Configuration{
		Companion: core.CompanionSettings{HistorySize: 50},
	}

	quietLogger(t)

	d := New()
	d.tunnels["test-tunnel"] = Tunnel{
		Hostname: "test.example.com",
		Pid:      9999,
		State:    StateConnected,
	}

	t.Run("STATUS", func(t *testing.T) {
		client, server := net.Pipe()
		defer client.Close()

		go d.handleConnection(server)

		// Send STATUS command
		fmt.Fprintln(client, "STATUS")

		// Read response
		scanner := bufio.NewScanner(client)
		if !scanner.Scan() {
			t.Fatal("expected response from STATUS command")
		}

		var resp Response
		if err := json.Unmarshal(scanner.Bytes(), &resp); err != nil {
			t.Fatalf("failed to parse STATUS response: %v\nraw: %s", err, scanner.Text())
		}

		if len(resp.Messages) == 0 {
			t.Fatal("expected at least one message in STATUS response")
		}
	})

	t.Run("VERSION", func(t *testing.T) {
		client, server := net.Pipe()
		defer client.Close()

		go d.handleConnection(server)

		// Send VERSION command
		fmt.Fprintln(client, "VERSION")

		// Read response
		scanner := bufio.NewScanner(client)
		if !scanner.Scan() {
			t.Fatal("expected response from VERSION command")
		}

		var resp Response
		if err := json.Unmarshal(scanner.Bytes(), &resp); err != nil {
			t.Fatalf("failed to parse VERSION response: %v\nraw: %s", err, scanner.Text())
		}

		if len(resp.Messages) == 0 {
			t.Fatal("expected at least one message")
		}
		if resp.Messages[0].Status != "INFO" {
			t.Errorf("expected INFO status, got %q", resp.Messages[0].Status)
		}
	})
}

func TestAdoptExistingTunnels_NoStateFile(t *testing.T) {
	quietLogger(t)

	tmpDir := t.TempDir()
	oldConfig := core.Config
	t.Cleanup(func() { core.Config = oldConfig })
	core.Config = &core.Configuration{
		ConfigPath: tmpDir,
		Companion:  core.CompanionSettings{HistorySize: 50},
	}

	d := New()

	// No state file exists — should return 0 adopted tunnels, no error
	adopted := d.adoptExistingTunnels()
	if adopted != 0 {
		t.Errorf("expected 0 adopted tunnels, got %d", adopted)
	}
}

func TestAdoptExistingTunnels_InvalidProcess(t *testing.T) {
	quietLogger(t)

	tmpDir := t.TempDir()
	oldConfig := core.Config
	t.Cleanup(func() { core.Config = oldConfig })
	core.Config = &core.Configuration{
		ConfigPath: tmpDir,
		Companion:  core.CompanionSettings{HistorySize: 50},
	}

	d := New()

	// Write a state file with PID 0 — process validation will fail
	if err := d.SaveTunnelState(); err != nil {
		t.Fatalf("failed to save empty state: %v", err)
	}

	// Create a state file with an invalid PID
	stateFile := TunnelStateFile{
		Version:   "1",
		Timestamp: time.Now().Format(time.RFC3339),
		Tunnels: []TunnelInfo{
			{
				PID:      0, // Invalid PID
				Alias:    "dead-tunnel",
				Hostname: "example.com",
				State:    "connected",
			},
		},
	}
	data, err := json.Marshal(stateFile)
	if err != nil {
		t.Fatalf("failed to marshal state: %v", err)
	}
	if err := os.WriteFile(GetTunnelStatePath(), data, 0600); err != nil {
		t.Fatalf("failed to write state file: %v", err)
	}

	adopted := d.adoptExistingTunnels()
	if adopted != 0 {
		t.Errorf("expected 0 adopted tunnels (invalid PID), got %d", adopted)
	}
}

func TestAdoptTunnel_InvalidProcess(t *testing.T) {
	quietLogger(t)

	tmpDir := t.TempDir()
	oldConfig := core.Config
	t.Cleanup(func() { core.Config = oldConfig })
	core.Config = &core.Configuration{
		ConfigPath: tmpDir,
		Companion:  core.CompanionSettings{HistorySize: 50},
	}

	d := New()

	info := TunnelInfo{
		PID:      0, // Invalid
		Alias:    "bad-tunnel",
		Hostname: "example.com",
		State:    "connected",
	}

	if d.adoptTunnel(info) {
		t.Error("expected adoptTunnel to return false for invalid PID")
	}

	d.mu.Lock()
	_, exists := d.tunnels["bad-tunnel"]
	d.mu.Unlock()
	if exists {
		t.Error("expected tunnel not to be added to map")
	}
}

func TestAdoptTunnel_AlreadyTracked(t *testing.T) {
	quietLogger(t)

	tmpDir := t.TempDir()
	oldConfig := core.Config
	t.Cleanup(func() { core.Config = oldConfig })
	core.Config = &core.Configuration{
		ConfigPath: tmpDir,
		Companion:  core.CompanionSettings{HistorySize: 50},
	}

	d := New()

	// Pre-populate tunnel map
	d.tunnels["existing-tunnel"] = Tunnel{
		Hostname: "existing.example.com",
		Pid:      os.Getpid(),
		State:    StateConnected,
	}

	info := TunnelInfo{
		PID:      os.Getpid(),
		Alias:    "existing-tunnel",
		Hostname: "example.com",
		Cmdline:  []string{"ssh", "existing-tunnel", "-N", "-o", "IgnoreUnknown=overseer-daemon", "-o", "overseer-daemon=true", "-o", "ExitOnForwardFailure=yes", "-v"},
		State:    "connected",
	}

	// adoptTunnel will validate the process first — if that passes,
	// it should still skip because alias already exists.
	// The result depends on whether process validation passes.
	result := d.adoptTunnel(info)

	// Either way, the existing tunnel should be preserved
	d.mu.Lock()
	tunnel := d.tunnels["existing-tunnel"]
	d.mu.Unlock()
	if tunnel.Hostname != "existing.example.com" {
		t.Errorf("existing tunnel was modified: hostname=%q", tunnel.Hostname)
	}
	_ = result
}

func TestCleanOrphanTunnels_TrackedNotKilled(t *testing.T) {
	quietLogger(t)

	// NOTE: We do NOT call cleanOrphanTunnels() here because it runs
	// pgrep -f overseer-daemon and kills any untracked SSH processes.
	// If a real overseer instance is running on this machine, the test
	// daemon (with empty tunnel map) would kill its SSH tunnels.
	// Instead, we only test findOverseerSSHProcesses() which is read-only.
	pids, err := findOverseerSSHProcesses()
	if err != nil {
		t.Fatalf("findOverseerSSHProcesses failed: %v", err)
	}
	// Just verify it returns a valid slice (may be empty or not)
	_ = pids
}

func TestSaveTunnelState_RoundTrip(t *testing.T) {
	quietLogger(t)

	tmpDir := t.TempDir()
	oldConfig := core.Config
	t.Cleanup(func() { core.Config = oldConfig })
	core.Config = &core.Configuration{
		ConfigPath: tmpDir,
		Companion:  core.CompanionSettings{HistorySize: 50},
	}

	d := New()

	// Add a tunnel with a valid PID
	d.tunnels["round-trip"] = Tunnel{
		Hostname:      "test.example.com",
		Pid:           os.Getpid(),
		State:         StateConnected,
		StartDate:     time.Now().Add(-1 * time.Hour),
		AutoReconnect: true,
		Environment:   map[string]string{"OVERSEER_TAG": "test-tag"},
	}

	// Save state
	if err := d.SaveTunnelState(); err != nil {
		t.Fatalf("SaveTunnelState failed: %v", err)
	}

	// Load state
	state, err := LoadTunnelState()
	if err != nil {
		t.Fatalf("LoadTunnelState failed: %v", err)
	}
	if state == nil {
		t.Fatal("expected non-nil state")
	}
	if len(state.Tunnels) != 1 {
		t.Fatalf("expected 1 tunnel, got %d", len(state.Tunnels))
	}

	info := state.Tunnels[0]
	if info.Alias != "round-trip" {
		t.Errorf("expected alias 'round-trip', got %q", info.Alias)
	}
	if info.Hostname != "test.example.com" {
		t.Errorf("expected hostname 'test.example.com', got %q", info.Hostname)
	}
	if info.PID != os.Getpid() {
		t.Errorf("expected PID %d, got %d", os.Getpid(), info.PID)
	}
	if !info.AutoReconnect {
		t.Error("expected AutoReconnect to be true")
	}
	if info.Environment["OVERSEER_TAG"] != "test-tag" {
		t.Errorf("expected environment OVERSEER_TAG='test-tag', got %q", info.Environment["OVERSEER_TAG"])
	}

	// Clean up
	if err := RemoveTunnelStateFile(); err != nil {
		t.Errorf("RemoveTunnelStateFile failed: %v", err)
	}
}

func TestSaveTunnelState_SkipsInvalidPID(t *testing.T) {
	quietLogger(t)

	tmpDir := t.TempDir()
	oldConfig := core.Config
	t.Cleanup(func() { core.Config = oldConfig })
	core.Config = &core.Configuration{
		ConfigPath: tmpDir,
		Companion:  core.CompanionSettings{HistorySize: 50},
	}

	d := New()

	// Tunnel with PID 0 should be skipped
	d.tunnels["invalid"] = Tunnel{
		Hostname: "test.example.com",
		Pid:      0,
		State:    StateConnected,
	}

	if err := d.SaveTunnelState(); err != nil {
		t.Fatalf("SaveTunnelState failed: %v", err)
	}

	state, err := LoadTunnelState()
	if err != nil {
		t.Fatalf("LoadTunnelState failed: %v", err)
	}
	if state == nil {
		t.Fatal("expected non-nil state")
	}
	if len(state.Tunnels) != 0 {
		t.Errorf("expected 0 tunnels (PID 0 skipped), got %d", len(state.Tunnels))
	}
}

func TestRemoveTunnelStateFile_NoFile(t *testing.T) {
	quietLogger(t)

	tmpDir := t.TempDir()
	oldConfig := core.Config
	t.Cleanup(func() { core.Config = oldConfig })
	core.Config = &core.Configuration{
		ConfigPath: tmpDir,
	}

	// Should not error when file doesn't exist
	if err := RemoveTunnelStateFile(); err != nil {
		t.Errorf("expected no error for missing file, got: %v", err)
	}
}
