package daemon

import (
	"context"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	psnet "github.com/shirou/gopsutil/v3/net"

	"go.olrik.dev/overseer/internal/core"
)

func TestParseAliasFromSSHCmdline_HappyPath(t *testing.T) {
	cmdline := "ssh zero -N -o IgnoreUnknown=overseer-daemon -o overseer-daemon=29d4412d -o ExitOnForwardFailure=yes -v"
	alias := parseAliasFromSSHCmdline(cmdline)
	if alias != "zero" {
		t.Errorf("expected alias 'zero', got %q", alias)
	}
}

func TestParseAliasFromSSHCmdline_AbsolutePathExecutable(t *testing.T) {
	// On macOS gopsutil sometimes reports the full path to ssh; the alias is
	// still in position 1.
	cmdline := "/usr/bin/ssh prod-db -N -o IgnoreUnknown=overseer-daemon -o overseer-daemon=abc"
	alias := parseAliasFromSSHCmdline(cmdline)
	if alias != "prod-db" {
		t.Errorf("expected alias 'prod-db', got %q", alias)
	}
}

func TestParseAliasFromSSHCmdline_AliasWithDashes(t *testing.T) {
	cmdline := "ssh corp-vpn-eu -N -o overseer-daemon=tag"
	alias := parseAliasFromSSHCmdline(cmdline)
	if alias != "corp-vpn-eu" {
		t.Errorf("expected alias 'corp-vpn-eu', got %q", alias)
	}
}

func TestParseAliasFromSSHCmdline_TooFewFields(t *testing.T) {
	if alias := parseAliasFromSSHCmdline("ssh"); alias != "" {
		t.Errorf("expected empty alias for single-field cmdline, got %q", alias)
	}
	if alias := parseAliasFromSSHCmdline(""); alias != "" {
		t.Errorf("expected empty alias for empty cmdline, got %q", alias)
	}
}

func TestParseAliasFromSSHCmdline_FirstPositionalIsFlag(t *testing.T) {
	// Defensive: if something other than our writer produced this cmdline and
	// the slot we expect the alias in is actually a flag, refuse to guess.
	cmdline := "ssh -v zero -N"
	if alias := parseAliasFromSSHCmdline(cmdline); alias != "" {
		t.Errorf("expected empty alias when position 1 is a flag, got %q", alias)
	}
}

func TestSynthesizeOrphanTunnelInfo_HappyPath(t *testing.T) {
	cmdline := "ssh zero -N -o IgnoreUnknown=overseer-daemon -o overseer-daemon=29d4412d -o ExitOnForwardFailure=yes -v"
	start := time.Unix(1_700_000_000, 0).UTC()

	info, ok := synthesizeOrphanTunnelInfo(6135, cmdline, start)
	if !ok {
		t.Fatal("expected ok=true for a well-formed overseer ssh cmdline")
	}
	if info.PID != 6135 {
		t.Errorf("expected PID 6135, got %d", info.PID)
	}
	if info.Alias != "zero" {
		t.Errorf("expected alias 'zero', got %q", info.Alias)
	}
	if info.Hostname != "zero" {
		t.Errorf("expected hostname mirror of alias, got %q", info.Hostname)
	}
	if !info.StartDate.Equal(start) {
		t.Errorf("expected StartDate=%v, got %v", start, info.StartDate)
	}
	if !info.LastConnectedTime.Equal(start) {
		t.Errorf("expected LastConnectedTime=%v, got %v", start, info.LastConnectedTime)
	}
	if info.State != string(StateConnected) {
		t.Errorf("expected state %q, got %q", StateConnected, info.State)
	}
	if info.RetryCount != 0 || info.TotalReconnects != 0 {
		t.Errorf("expected zeroed retry/reconnect counters, got retry=%d total=%d",
			info.RetryCount, info.TotalReconnects)
	}
	// Cmdline must round-trip back to the original string when joined, so the
	// existing ValidateTunnelProcess.matchesCommandLine logic can use it.
	if joined := strings.Join(info.Cmdline, " "); joined != cmdline {
		t.Errorf("expected cmdline slice to rejoin to original\n got: %s\nwant: %s", joined, cmdline)
	}
}

func TestSynthesizeOrphanTunnelInfo_RejectsUnparseableCmdline(t *testing.T) {
	// Cmdline doesn't have an alias in position 1 (only a flag) — refuse to
	// synthesize rather than guessing.
	if _, ok := synthesizeOrphanTunnelInfo(123, "ssh -v -N", time.Now()); ok {
		t.Error("expected ok=false when alias cannot be parsed")
	}
	if _, ok := synthesizeOrphanTunnelInfo(123, "", time.Now()); ok {
		t.Error("expected ok=false for empty cmdline")
	}
}

// spawnOrphanStub starts a child process that exists purely to be adopted. It
// re-execs this test binary with argv[0]="ssh" and an overseer-shaped argv,
// so `ps -o command=` reports a cmdline indistinguishable from a real
// overseer ssh child. The OVERSEER_TEST_ORPHAN_STUB env var in TestMain makes
// the child sleep instead of running tests.
func spawnOrphanStub(t *testing.T, alias string) int {
	t.Helper()
	cmd := &exec.Cmd{
		Path: os.Args[0],
		Args: []string{
			"ssh",
			alias,
			"-N",
			"-o", "IgnoreUnknown=overseer-daemon",
			"-o", "overseer-daemon=" + core.ProcessTag(),
			"-o", "ExitOnForwardFailure=yes",
			"-v",
		},
		Env: append(os.Environ(), "OVERSEER_TEST_ORPHAN_STUB=1"),
	}
	if err := cmd.Start(); err != nil {
		t.Fatalf("failed to start orphan stub: %v", err)
	}
	t.Cleanup(func() {
		_ = cmd.Process.Kill()
		_, _ = cmd.Process.Wait()
	})
	return cmd.Process.Pid
}

func TestAdoptOrphanPIDs_AdoptsRealOrphan(t *testing.T) {
	quietLogger(t)

	oldConfig := core.Config
	t.Cleanup(func() { core.Config = oldConfig })
	core.Config = core.GetDefaultConfig()
	// Stable ConfigPath so ProcessTag() returns a consistent value across
	// parent and child.
	core.Config.ConfigPath = t.TempDir()

	d := New()
	d.ctx, d.cancelFunc = context.WithCancel(context.Background())
	t.Cleanup(d.cancelFunc)

	alias := fmt.Sprintf("orphan-test-%d", time.Now().UnixNano())
	pid := spawnOrphanStub(t, alias)

	// Give the kernel a moment so `ps` can see the new argv reliably.
	time.Sleep(100 * time.Millisecond)

	adopted := d.adoptOrphanPIDs([]int{pid})

	if len(adopted) != 1 || adopted[0] != alias {
		t.Fatalf("expected adopted=[%s], got %v", alias, adopted)
	}

	d.mu.Lock()
	tunnel, exists := d.tunnels[alias]
	d.mu.Unlock()
	if !exists {
		t.Fatalf("expected d.tunnels[%q] to exist after adoption", alias)
	}
	if tunnel.Pid != pid {
		t.Errorf("expected adopted Pid=%d, got %d", pid, tunnel.Pid)
	}
	if tunnel.State != StateConnected {
		t.Errorf("expected state %q, got %q", StateConnected, tunnel.State)
	}
}

func TestAdoptOrphanPIDs_SkipsAlreadyTracked(t *testing.T) {
	quietLogger(t)

	oldConfig := core.Config
	t.Cleanup(func() { core.Config = oldConfig })
	core.Config = core.GetDefaultConfig()
	core.Config.ConfigPath = t.TempDir()

	d := New()
	d.ctx, d.cancelFunc = context.WithCancel(context.Background())
	t.Cleanup(d.cancelFunc)

	alias := fmt.Sprintf("tracked-test-%d", time.Now().UnixNano())
	pid := spawnOrphanStub(t, alias)
	time.Sleep(100 * time.Millisecond)

	// Pre-populate with the same alias so adoption should skip it.
	d.tunnels[alias] = Tunnel{
		Hostname: alias,
		Pid:      pid,
		State:    StateConnected,
	}

	adopted := d.adoptOrphanPIDs([]int{pid})
	if len(adopted) != 0 {
		t.Errorf("expected no adoptions when alias already tracked, got %v", adopted)
	}
}

func TestAdoptOrphanPIDs_EmptyInputReturnsNil(t *testing.T) {
	d := New()
	if got := d.adoptOrphanPIDs(nil); got != nil {
		t.Errorf("expected nil for nil input, got %v", got)
	}
	if got := d.adoptOrphanPIDs([]int{}); got != nil {
		t.Errorf("expected nil for empty input, got %v", got)
	}
}

func TestPickEstablishedRemote_HappyPath(t *testing.T) {
	conns := []psnet.ConnectionStat{
		// Listen socket — must be ignored.
		{Status: "LISTEN", Laddr: psnet.Addr{IP: "0.0.0.0", Port: 22}},
		// Established connection on a different port — must be skipped.
		{Status: "ESTABLISHED", Raddr: psnet.Addr{IP: "10.0.0.99", Port: 443}},
		// The match.
		{Status: "ESTABLISHED", Raddr: psnet.Addr{IP: "10.0.0.7", Port: 22}},
	}
	if got := pickEstablishedRemote(conns, "22"); got != "10.0.0.7:22" {
		t.Errorf("expected 10.0.0.7:22, got %q", got)
	}
}

func TestPickEstablishedRemote_NoMatch(t *testing.T) {
	conns := []psnet.ConnectionStat{
		{Status: "ESTABLISHED", Raddr: psnet.Addr{IP: "1.2.3.4", Port: 443}},
	}
	if got := pickEstablishedRemote(conns, "22"); got != "" {
		t.Errorf("expected empty for no match, got %q", got)
	}
}

func TestPickEstablishedRemote_EmptyPort(t *testing.T) {
	conns := []psnet.ConnectionStat{
		{Status: "ESTABLISHED", Raddr: psnet.Addr{IP: "1.2.3.4", Port: 22}},
	}
	// Without a port to match against we can't pick — refuse to guess rather
	// than returning an arbitrary connection (the ssh process may hold
	// multiple, including the agent socket and proxy hops).
	if got := pickEstablishedRemote(conns, ""); got != "" {
		t.Errorf("expected empty when destPort is unknown, got %q", got)
	}
}

func TestPickEstablishedRemote_IPv6Bracketed(t *testing.T) {
	conns := []psnet.ConnectionStat{
		{Status: "ESTABLISHED", Raddr: psnet.Addr{IP: "2a05:f6c3::1", Port: 22}},
	}
	// net.JoinHostPort puts IPv6 in brackets — verify we delegate to it.
	if got := pickEstablishedRemote(conns, "22"); got != "[2a05:f6c3::1]:22" {
		t.Errorf("expected bracketed IPv6, got %q", got)
	}
}

func TestResolveSSHDestination_DirectHost(t *testing.T) {
	if _, err := exec.LookPath("ssh"); err != nil {
		t.Skip("ssh not available")
	}

	tmpDir := t.TempDir()
	configPath := filepath.Join(tmpDir, "ssh_config")
	cfg := `Host direct-orphan-test
    HostName 10.10.10.10
    Port 2222
`
	if err := os.WriteFile(configPath, []byte(cfg), 0600); err != nil {
		t.Fatalf("write config: %v", err)
	}

	host, port := resolveSSHDestination("direct-orphan-test", nil, configPath)
	if host != "10.10.10.10" {
		t.Errorf("expected host=10.10.10.10, got %q", host)
	}
	if port != "2222" {
		t.Errorf("expected port=2222, got %q", port)
	}
}

func TestEnrichOrphanInfo_PopulatesResolvedHostFromConnection(t *testing.T) {
	quietLogger(t)
	if _, err := exec.LookPath("ssh"); err != nil {
		t.Skip("ssh not available")
	}

	// Stand up a localhost TCP listener and open a connection to it from
	// this test process. The connection will appear as ESTABLISHED in our
	// own PID's connection table.
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	t.Cleanup(func() { ln.Close() })

	acceptDone := make(chan struct{})
	go func() {
		conn, _ := ln.Accept()
		if conn != nil {
			t.Cleanup(func() { conn.Close() })
		}
		close(acceptDone)
	}()

	port := ln.Addr().(*net.TCPAddr).Port
	dialer, err := net.Dial("tcp", ln.Addr().String())
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	t.Cleanup(func() { dialer.Close() })
	<-acceptDone

	// Build an ssh_config so ssh -G resolves the alias to that exact port.
	tmpDir := t.TempDir()
	configPath := filepath.Join(tmpDir, "ssh_config")
	cfg := fmt.Sprintf(`Host orphan-resolved-test
    HostName 127.0.0.1
    Port %d
`, port)
	if err := os.WriteFile(configPath, []byte(cfg), 0600); err != nil {
		t.Fatalf("write config: %v", err)
	}

	oldConfig := core.Config
	t.Cleanup(func() { core.Config = oldConfig })
	core.Config = core.GetDefaultConfig()

	d := New()
	d.sshConfigFile = configPath

	info := TunnelInfo{
		PID:   os.Getpid(),
		Alias: "orphan-resolved-test",
	}
	d.enrichOrphanInfo(&info)

	expected := fmt.Sprintf("127.0.0.1:%d", port)
	if info.ResolvedHost != expected {
		t.Errorf("expected ResolvedHost=%q, got %q", expected, info.ResolvedHost)
	}
}

func TestEnrichOrphanInfo_PopulatesJumpChainFromConfig(t *testing.T) {
	quietLogger(t)
	if _, err := exec.LookPath("ssh"); err != nil {
		t.Skip("ssh not available")
	}

	tmpDir := t.TempDir()
	configPath := filepath.Join(tmpDir, "ssh_config")
	cfg := `Host orphan-jump-test
    HostName 10.0.0.1
    Port 22
    ProxyJump orphan-hop-test

Host orphan-hop-test
    HostName 10.0.0.2
    Port 2222
`
	if err := os.WriteFile(configPath, []byte(cfg), 0600); err != nil {
		t.Fatalf("write config: %v", err)
	}

	oldConfig := core.Config
	t.Cleanup(func() { core.Config = oldConfig })
	core.Config = core.GetDefaultConfig()

	d := New()
	d.sshConfigFile = configPath

	info := TunnelInfo{
		PID:   os.Getpid(),
		Alias: "orphan-jump-test",
	}
	d.enrichOrphanInfo(&info)

	if len(info.JumpChain) < 2 {
		t.Fatalf("expected at least 2 hops, got %v", info.JumpChain)
	}
	// When a JumpChain is set, ResolvedHost should be left empty — the
	// display uses JumpChain in that case, so populating both would be
	// redundant noise.
	if info.ResolvedHost != "" {
		t.Errorf("expected ResolvedHost empty when JumpChain is set, got %q", info.ResolvedHost)
	}
}

func TestEnrichOrphanInfo_PopulatesEnvironmentFromTunnelConfig(t *testing.T) {
	quietLogger(t)

	oldConfig := core.Config
	t.Cleanup(func() { core.Config = oldConfig })
	core.Config = core.GetDefaultConfig()
	core.Config.Tunnels = map[string]*core.TunnelConfig{
		"orphan-env-test": {
			Name:        "orphan-env-test",
			Environment: map[string]string{"FOO": "bar", "BAZ": "qux"},
		},
	}

	d := New()

	info := TunnelInfo{
		PID:   os.Getpid(),
		Alias: "orphan-env-test",
	}
	d.enrichOrphanInfo(&info)

	if info.Environment["FOO"] != "bar" || info.Environment["BAZ"] != "qux" {
		t.Errorf("expected env to include FOO=bar BAZ=qux, got %v", info.Environment)
	}
}
