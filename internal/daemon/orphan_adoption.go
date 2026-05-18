package daemon

import (
	"log/slog"
	"net"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"time"

	psnet "github.com/shirou/gopsutil/v3/net"
	"github.com/shirou/gopsutil/v3/process"

	"go.olrik.dev/overseer/internal/core"
)

// parseAliasFromSSHCmdline extracts the SSH alias from a cmdline that overseer
// itself wrote. Returns "" when the cmdline does not match the shape
// `ssh <alias> -N ...` we produce in startTunnelStreaming. The alias is always
// at position 1 because we set sshArgs as []string{alias, "-N", ...} in
// internal/daemon/server.go; gopsutil may report position 0 as either "ssh" or
// "/usr/bin/ssh", but the alias slot is stable.
func parseAliasFromSSHCmdline(cmdline string) string {
	fields := strings.Fields(cmdline)
	if len(fields) < 2 {
		return ""
	}
	alias := fields[1]
	if strings.HasPrefix(alias, "-") {
		return ""
	}
	return alias
}

// synthesizeOrphanTunnelInfo builds a TunnelInfo from data we can recover for
// a stranded ssh process: its PID, its cmdline, and its OS-reported start
// time. The result is suitable for passing to (d *Daemon).adoptTunnel — it
// carries enough fields for ValidateTunnelProcess to pass the cmdline check.
// Counters reset to zero because the new daemon has no history for this
// process; AutoReconnect follows the live SSH config so the adopted tunnel
// behaves like one started under the current daemon.
func synthesizeOrphanTunnelInfo(pid int, cmdline string, start time.Time) (TunnelInfo, bool) {
	alias := parseAliasFromSSHCmdline(cmdline)
	if alias == "" {
		return TunnelInfo{}, false
	}
	autoReconnect := true
	if core.Config != nil {
		autoReconnect = core.Config.SSH.ReconnectEnabled
	}
	return TunnelInfo{
		PID:               pid,
		Alias:             alias,
		Hostname:          alias,
		Cmdline:           strings.Fields(cmdline),
		StartDate:         start,
		LastConnectedTime: start,
		AutoReconnect:     autoReconnect,
		State:             string(StateConnected),
	}, true
}

// adoptOrphanedSSHProcesses scans the process table for SSH children carrying
// our overseer-daemon marker that this daemon doesn't already track, and
// adopts each one. Returns the aliases that were freshly adopted so callers
// can surface them via INFO messages. Safe to call repeatedly.
func (d *Daemon) adoptOrphanedSSHProcesses() []string {
	pids, err := findOverseerSSHProcesses()
	if err != nil {
		slog.Warn("Failed to search for orphan SSH processes", "error", err)
		return nil
	}
	return d.adoptOrphanPIDs(pids)
}

// adoptOrphanPIDs is the testable seam for adoptOrphanedSSHProcesses: given a
// PID list, it skips ones we already track and tries to adopt the rest.
// Returns the aliases successfully adopted (in the order PIDs were processed).
func (d *Daemon) adoptOrphanPIDs(pids []int) []string {
	if len(pids) == 0 {
		return nil
	}
	var adopted []string
	for _, pid := range pids {
		info, ok := buildOrphanTunnelInfo(pid)
		if !ok {
			slog.Debug("Skipping orphan candidate: could not build TunnelInfo", "pid", pid)
			continue
		}
		d.mu.Lock()
		_, exists := d.tunnels[info.Alias]
		d.mu.Unlock()
		if exists {
			continue
		}
		// Best-effort enrichment from live config + connection table — these
		// fields aren't recoverable from the process alone but make the
		// adopted tunnel render the same as a freshly-connected one.
		d.enrichOrphanInfo(&info)
		if d.adoptTunnel(info) {
			adopted = append(adopted, info.Alias)
		}
	}
	return adopted
}

// enrichOrphanInfo fills in TunnelInfo fields that aren't part of the
// per-process data carried by an orphaned ssh child: Environment (from the
// tunnel's static config), JumpChain (from ssh -G), and — for direct
// connections only — ResolvedHost (from the process's TCP connection table).
//
// When a JumpChain is set the display uses it instead of ResolvedHost
// (cmd/status.go renders only one of the two), so we deliberately skip the
// connection-table lookup in that case to avoid the lsof-equivalent cost.
func (d *Daemon) enrichOrphanInfo(info *TunnelInfo) {
	if info == nil || info.Alias == "" {
		return
	}

	env := mergedEnvForTunnel(info.Alias)
	if len(env) > 0 {
		info.Environment = env
	}

	info.JumpChain = resolveJumpChain(info.Alias, env, d.sshConfigFile)
	if len(info.JumpChain) > 0 {
		return
	}

	_, port := resolveSSHDestination(info.Alias, env, d.sshConfigFile)
	if port == "" {
		return
	}
	conns, err := psnet.ConnectionsPid("tcp", int32(info.PID))
	if err != nil {
		slog.Debug("Could not read connections for orphan", "pid", info.PID, "error", err)
		return
	}
	if rh := pickEstablishedRemote(conns, port); rh != "" {
		info.ResolvedHost = rh
	}
}

// mergedEnvForTunnel rebuilds the static parts of the SSH environment for an
// alias from current config. Matches the connect path's merge order
// (orchestrator/global → tunnel config) but omits any CLI-supplied -E
// variables, which we have no way to recover for an orphan.
func mergedEnvForTunnel(alias string) map[string]string {
	env := make(map[string]string)
	if orch := GetStateOrchestrator(); orch != nil {
		for k, v := range orch.BuildSSHEnv() {
			env[k] = v
		}
	} else if core.Config != nil {
		for k, v := range core.Config.Environment {
			env[k] = v
		}
	}
	if core.Config != nil {
		if tc := core.Config.Tunnels[alias]; tc != nil {
			for k, v := range tc.Environment {
				env[k] = v
			}
		}
	}
	return env
}

// resolveSSHDestination runs `ssh -G <alias>` and returns the resolved
// (hostname, port). Returns ("", "") on failure. The hostname value comes
// straight from ssh_config — it may be a name or an IP depending on what the
// user wrote in their config.
func resolveSSHDestination(alias string, env map[string]string, sshConfigFile string) (string, string) {
	args := []string{"-G"}
	if sshConfigFile != "" {
		args = append(args, "-F", sshConfigFile)
	}
	args = append(args, alias)

	cmd := exec.Command("ssh", args...)
	if len(env) > 0 {
		cmd.Env = os.Environ()
		for k, v := range env {
			cmd.Env = append(cmd.Env, k+"="+v)
		}
	}
	out, err := cmd.Output()
	if err != nil {
		return "", ""
	}

	var host, port string
	for _, line := range strings.Split(string(out), "\n") {
		parts := strings.SplitN(strings.TrimSpace(line), " ", 2)
		if len(parts) != 2 {
			continue
		}
		switch parts[0] {
		case "hostname":
			host = parts[1]
		case "port":
			port = parts[1]
		}
	}
	return host, port
}

// pickEstablishedRemote returns the "ip:port" of the first ESTABLISHED TCP
// connection in conns whose remote port matches destPort. Returns "" when no
// match is found or destPort is empty. Empty destPort is treated as
// "unknown, refuse to guess" — the ssh process may also have connections to
// the SSH agent socket or to additional hosts via ProxyCommand, and picking
// one arbitrarily would be misleading.
func pickEstablishedRemote(conns []psnet.ConnectionStat, destPort string) string {
	if destPort == "" {
		return ""
	}
	wantPort, err := strconv.Atoi(destPort)
	if err != nil {
		return ""
	}
	for _, c := range conns {
		if !strings.EqualFold(c.Status, "ESTABLISHED") {
			continue
		}
		if int(c.Raddr.Port) != wantPort {
			continue
		}
		if c.Raddr.IP == "" {
			continue
		}
		return net.JoinHostPort(c.Raddr.IP, strconv.Itoa(int(c.Raddr.Port)))
	}
	return ""
}

// buildOrphanTunnelInfo synthesizes a TunnelInfo for a live PID known (by the
// caller) to carry the overseer-daemon marker. Returns ok=false if the
// process is no longer readable or its cmdline doesn't parse.
func buildOrphanTunnelInfo(pid int) (TunnelInfo, bool) {
	p, err := process.NewProcess(int32(pid))
	if err != nil {
		return TunnelInfo{}, false
	}
	cmdline, err := p.Cmdline()
	if err != nil {
		return TunnelInfo{}, false
	}
	start := time.Now()
	if ms, err := p.CreateTime(); err == nil && ms > 0 {
		start = time.UnixMilli(ms)
	} else {
		// Falling back to time.Now() means "age" displays will read 0s
		// immediately after adoption; the process is genuinely still ours, so
		// adopt it anyway rather than refusing.
		slog.Debug("Could not read process start time, using now", "pid", pid, "error", err)
	}
	return synthesizeOrphanTunnelInfo(pid, cmdline, start)
}
