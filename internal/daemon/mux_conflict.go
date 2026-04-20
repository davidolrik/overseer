package daemon

import (
	"context"
	"fmt"
	"log/slog"
	"os/exec"
	"regexp"
	"strconv"
	"time"

	"go.olrik.dev/overseer/internal/portdiag"
)

// muxCheckTimeout is how long we wait for `ssh -O check` and `ssh -O exit`.
// The calls talk to a local Unix socket, so they should return in
// milliseconds; 2 seconds is a generous ceiling that still prevents a
// misbehaving master from blocking tunnel startup.
const muxCheckTimeout = 2 * time.Second

// ANSI escapes for highlighting the conflicting master's PID. Bold red draws
// the eye to the actionable datum the user needs in order to `kill -s …` it
// or run `ssh -O exit`. The rest of the line and the process tree stay in
// their normal colors so the highlight stands out.
const (
	colorBrightRed = "\033[1;31m"
	colorReset     = "\033[0m"
)

// masterRunningRe matches the single "Master running" line that `ssh -O check`
// prints on stdout when a mux master is alive. Group 1 captures the PID.
var masterRunningRe = regexp.MustCompile(`Master running \(pid=(\d+)\)`)

// parseMuxCheckOutput decides whether a mux master is alive based on the
// combined stdout+stderr of `ssh -O check <alias>`. Returns the master PID
// when alive, else pid=0 and alive=false. The "stale vs absent socket"
// distinction is not surfaced because evictMuxMaster handles both paths
// idempotently.
//
// cmdErr is accepted for symmetry but intentionally not used: OpenSSH writes
// "Master running (pid=N)" to stderr (not stdout), so we must trust the regex
// match from combined output rather than the exit code.
func parseMuxCheckOutput(output string, cmdErr error) (pid int, alive bool) {
	_ = cmdErr
	matches := masterRunningRe.FindStringSubmatch(output)
	if len(matches) != 2 {
		return 0, false
	}
	p, err := strconv.Atoi(matches[1])
	if err != nil || p <= 0 {
		return 0, false
	}
	return p, true
}

// checkMuxMaster runs `ssh -O check <alias>` and returns the master's PID if
// one is alive, else pid=0 and alive=false. A non-nil err is returned only on
// exec failures (ssh binary missing, context timeout) — a "no master" result
// is a successful call with alive=false.
func checkMuxMaster(alias, sshConfigFile string) (pid int, alive bool, err error) {
	ctx, cancel := context.WithTimeout(context.Background(), muxCheckTimeout)
	defer cancel()

	args := []string{}
	if sshConfigFile != "" {
		args = append(args, "-F", sshConfigFile)
	}
	args = append(args, "-O", "check", alias)

	cmd := exec.CommandContext(ctx, "ssh", args...)
	// CombinedOutput merges stderr — "Master running (pid=N)" is on stderr.
	out, runErr := cmd.CombinedOutput()
	if ctx.Err() != nil {
		return 0, false, ctx.Err()
	}
	p, ok := parseMuxCheckOutput(string(out), runErr)
	return p, ok, nil
}

// evictMuxMaster runs `ssh -O exit <alias>` to tear down a mux master and
// remove its socket file. Idempotent: errors (no socket, stale socket, master
// already gone) are logged at debug and swallowed, because the only outcome
// the caller cares about is "socket gone by the time we return".
func evictMuxMaster(alias, sshConfigFile string) {
	ctx, cancel := context.WithTimeout(context.Background(), muxCheckTimeout)
	defer cancel()

	args := []string{}
	if sshConfigFile != "" {
		args = append(args, "-F", sshConfigFile)
	}
	args = append(args, "-O", "exit", alias)

	cmd := exec.CommandContext(ctx, "ssh", args...)
	if err := cmd.Run(); err != nil {
		slog.Debug("ssh -O exit returned non-zero (expected when no master or stale socket)",
			"alias", alias, "error", err)
	}
}

// reportMuxConflict builds a process tree for a live foreign mux master and
// emits it through the same send channel that port_conflict.go uses. The
// message shape mirrors reportConnectFailure so the interactive UX stays
// consistent across conflict types.
func reportMuxConflict(alias string, masterPid int, send func(message, status string)) {
	pidStr := fmt.Sprintf("%s%d%s", colorBrightRed, masterPid, colorReset)
	send(fmt.Sprintf("Tunnel '%s' failed to connect: an SSH ControlMaster is already running (pid %s)",
		alias, pidStr), "ERROR")

	chain, _ := portdiag.WalkAncestors(int32(masterPid))
	header := fmt.Sprintf("SSH ControlMaster for %q is held by the following process tree:", alias)
	for _, line := range portdiag.FormatConflictTree(header, chain, int32(masterPid)) {
		send(line, "ERROR")
	}

	send(fmt.Sprintf("Close the running session or retry with `overseer connect %s --force` to evict it.",
		alias), "ERROR")
}
