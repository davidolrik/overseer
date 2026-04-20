// Package portdiag inspects which process is holding a local TCP port and
// walks that process's ancestor chain. It exists to give actionable error
// output when overseer's SSH child fails with ExitOnForwardFailure=yes
// because another process beat us to the bind.
package portdiag

import (
	"errors"
	"fmt"
	"os/user"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"

	psnet "github.com/shirou/gopsutil/v3/net"
	"github.com/shirou/gopsutil/v3/process"
)

// ANSI colors used when rendering the conflict tree. The palette matches
// cmd/status.go (hostnames use bold blue there) so overseer's output stays
// visually consistent. Callers that don't want colors strip them downstream
// (see cmd/logs.go --no-color).
const (
	colorReset       = "\033[0m"
	colorHostname    = "\033[1;34m" // bold blue — SSH host args in cmdlines
	colorPort        = "\033[34m"   // plain blue — port numbers (matches status.go)
	colorProcessName = "\033[36m"   // cyan — first token of cmdline (executable)
	colorTreeChars   = "\033[90m"   // dim gray — tree connectors (└, ─) and colons
	colorCurrentUser = "\033[32m"   // green — the user running overseer
	colorOtherUser   = "\033[33m"   // yellow — any other user
	colorHighlight   = "\033[1;31m" // bold red — the PID the user needs to act on
)

// ErrHolderNotFound is returned by FindPortHolder when no process visible to
// the current user is listening on the given port. On macOS this commonly
// indicates the holder runs as a different user (lsof, used under the hood by
// gopsutil, hides those rows without root).
var ErrHolderNotFound = errors.New("no visible process holding port")

// maxAncestorHops bounds WalkAncestors as a defensive cap against pathological
// process tables. Real chains on darwin/linux are well under 32 deep.
const maxAncestorHops = 64

// ProcessInfo describes one node in the ancestor chain. Err is set when
// gopsutil could not fully inspect that PID (process exited mid-walk,
// permission denied, etc.) — partial nodes are still included so the output
// shows the gap rather than silently dropping ancestors.
type ProcessInfo struct {
	PID     int32
	PPID    int32
	Name    string
	User    string
	Cmdline string
	Err     error
}

// Holder describes the process bound to a conflicting local port plus its
// ancestor chain from the holder (Chain[0]) up to PID 1 or the last
// reachable ancestor.
type Holder struct {
	Port    int
	Addr    string // listen address as reported by gopsutil ("127.0.0.1", "::1", "0.0.0.0", ...)
	Chain   []ProcessInfo
	Partial bool // true if the ancestor walk was cut short by an error
}

// FindPortHolder locates the LISTEN socket on the given local port and walks
// the owning process's ancestor chain. Returns ErrHolderNotFound when no
// owning PID is visible.
func FindPortHolder(port int) (*Holder, error) {
	conns, err := psnet.Connections("tcp")
	if err != nil {
		return nil, fmt.Errorf("list tcp connections: %w", err)
	}
	for _, c := range conns {
		if !strings.EqualFold(c.Status, "LISTEN") {
			continue
		}
		if int(c.Laddr.Port) != port {
			continue
		}
		if c.Pid == 0 {
			// gopsutil saw the socket but couldn't attribute a PID — treat as
			// not found so the caller emits the cross-user fallback message.
			continue
		}
		chain, walkErr := WalkAncestors(c.Pid)
		h := &Holder{
			Port:  port,
			Addr:  c.Laddr.IP,
			Chain: chain,
		}
		if walkErr != nil {
			h.Partial = true
		}
		return h, nil
	}
	return nil, ErrHolderNotFound
}

// WalkAncestors walks parent PIDs starting at pid, stopping at PID 1 or when
// no further parent is available. Returns the chain it managed to assemble
// even on error, so callers can render a partial tree.
func WalkAncestors(pid int32) ([]ProcessInfo, error) {
	visited := make(map[int32]struct{}, maxAncestorHops)
	var chain []ProcessInfo
	current := pid

	for hop := 0; hop < maxAncestorHops; hop++ {
		if _, seen := visited[current]; seen {
			return chain, fmt.Errorf("cycle detected at pid %d", current)
		}
		visited[current] = struct{}{}

		info := ProcessInfo{PID: current}
		p, err := process.NewProcess(current)
		if err != nil {
			info.Err = err
			chain = append(chain, info)
			return chain, err
		}
		if ppid, err := p.Ppid(); err == nil {
			info.PPID = ppid
		} else if info.Err == nil {
			info.Err = err
		}
		if name, err := p.Name(); err == nil {
			info.Name = name
		}
		if user, err := p.Username(); err == nil {
			info.User = user
		}
		if cmd, err := p.Cmdline(); err == nil {
			info.Cmdline = cmd
		} else if info.Err == nil {
			info.Err = err
		}
		chain = append(chain, info)

		if current == 1 || info.PPID == 0 || info.PPID == current {
			return chain, nil
		}
		current = info.PPID
	}
	return chain, fmt.Errorf("ancestor walk exceeded %d hops", maxAncestorHops)
}

// FormatTree renders a port-conflict Holder as human-readable lines: a header
// followed by one line per ancestor laid out as a process tree. Thin wrapper
// around FormatConflictTree.
func FormatTree(h *Holder) []string {
	if h == nil {
		return nil
	}
	if len(h.Chain) == 0 {
		return []string{
			fmt.Sprintf("Port %d on %s is held (no process tree available).", h.Port, h.Addr),
		}
	}
	header := fmt.Sprintf("Port %d on %s is held by the following process tree:", h.Port, h.Addr)
	var highlight int32
	if len(h.Chain) > 0 {
		highlight = h.Chain[0].PID
	}
	return FormatConflictTree(header, h.Chain, highlight)
}

// FormatConflictTree renders an ancestor chain as a process tree under the
// given header. The root (PID 1) is the first entry; the conflict holder is
// the last. Each line has a fixed-width tree connector, right-aligned PID,
// left-aligned user, and command line (or process name if cmdline is empty).
//
// highlightPID draws that specific PID in bold red so the user can spot the
// process they need to act on. Pass 0 to disable highlighting.
//
// The first token of each cmdline (the executable) renders in cyan, SSH host
// arguments render in bold blue, and tree connectors render in dim gray, so
// the things a user typically scans for stand out.
func FormatConflictTree(header string, chain []ProcessInfo, highlightPID int32) []string {
	if len(chain) == 0 {
		if header == "" {
			return nil
		}
		return []string{header}
	}

	lines := []string{header}

	// Compute column widths for PID and user across the entire chain so each
	// row has consistent spacing.
	var maxPidW, maxUserW int
	for _, info := range chain {
		if w := len(strconv.Itoa(int(info.PID))); w > maxPidW {
			maxPidW = w
		}
		if w := len(info.User); w > maxUserW {
			maxUserW = w
		}
	}

	// Tree column: every line is padded to the same visual width so PIDs
	// stack in a single column. Width = maxDepth + 4 keeps at least 2 dashes
	// after the └ at the deepest level.
	maxDepth := len(chain) - 1
	treeColWidth := maxDepth + 4

	currentUser := ""
	if u, err := user.Current(); err == nil {
		currentUser = u.Username
	}

	// Walk chain in reverse so the root prints first; depth grows toward the
	// holder.
	for i := len(chain) - 1; i >= 0; i-- {
		info := chain[i]
		depth := len(chain) - 1 - i

		prefix := buildTreePrefix(depth, treeColWidth)
		cmd := formatCommand(info)
		coloredUser := colorizeUser(info.User, currentUser, maxUserW)
		pidField := formatPID(info.PID, maxPidW, highlightPID)

		line := fmt.Sprintf("%s%s %s %s",
			prefix,
			pidField,
			coloredUser,
			cmd)
		lines = append(lines, line)
	}
	return lines
}

// formatPID right-aligns a PID to width, wrapping it in bold red if it equals
// highlightPID. Padding is applied before the color escapes so the visible
// column width stays consistent across rows regardless of which PIDs are
// highlighted.
func formatPID(pid int32, width int, highlightPID int32) string {
	padded := fmt.Sprintf("%*d", width, pid)
	if highlightPID != 0 && pid == highlightPID {
		return colorHighlight + padded + colorReset
	}
	return padded
}

// colorizeUser pads user to width then wraps it in green if it matches the
// daemon's current user, yellow otherwise. Padding happens before color
// wrapping so the visible column width stays consistent regardless of which
// color escapes are emitted.
func colorizeUser(u, currentUser string, width int) string {
	padded := fmt.Sprintf("%-*s", width, u)
	if u == "" {
		return padded
	}
	if u == currentUser {
		return colorCurrentUser + padded + colorReset
	}
	return colorOtherUser + padded + colorReset
}

// buildTreePrefix returns the colored tree connector for one row of the
// FormatTree output. width is the total visual width of the prefix in runes
// (not bytes). The connector layout for depth K of a chain with max depth D
// (so width W = D+4) is: K spaces, "└", (W-K-2) "─" chars, one trailing
// space — totalling W visual chars.
func buildTreePrefix(depth, width int) string {
	indent := strings.Repeat(" ", depth)
	dashes := strings.Repeat("─", width-depth-2)
	return colorTreeChars + indent + "└" + dashes + colorReset + " "
}

// formatCommand picks the best display string for one ProcessInfo and
// applies coloring. Prefers the full cmdline; falls back to the process
// name; uses "(unknown)" only if both are empty. Per-node Err and the
// "Note: tree may be incomplete" trailer are intentionally suppressed —
// the surrounding context (Tunnel failed message + the tree) is enough,
// and partial errors from gopsutil were noisy in practice.
func formatCommand(info ProcessInfo) string {
	if info.Cmdline != "" {
		return colorizeCmdline(info.Cmdline)
	}
	if info.Name != "" {
		return colorProcessName + info.Name + colorReset
	}
	return "(unknown)"
}

// bracketedHostRe matches `[host]:port` patterns embedded in cmdline args
// (commonly seen in ssh's -W spec or with bracketed IPv6 literals). Group 1
// is the host inside the brackets; group 2 is the port digits.
var bracketedHostRe = regexp.MustCompile(`\[([^\]]+)\]:(\d+)`)

// colorizeCmdline colors the executable (first token) in cyan, wraps any
// `[host]:port` substrings so the host shows in bold blue, and — for ssh(1)
// invocations — also colors the SSH host positional argument in bold blue.
// Returns the original cmdline if it's empty.
func colorizeCmdline(cmdline string) string {
	fields := strings.Fields(cmdline)
	if len(fields) == 0 {
		return cmdline
	}

	// Detect ssh BEFORE wrapping the first token in color codes — otherwise
	// the basename check sees the escape sequence rather than the path.
	isSSH := filepath.Base(fields[0]) == "ssh"

	// Color any [host]:port spans in every field — host bold blue, colon
	// dim gray, port plain blue (matches the host:port rendering in
	// cmd/status.go). Done first so the SSH host-arg coloring below
	// doesn't accidentally wrap a field that already contains escapes.
	for i, f := range fields {
		fields[i] = bracketedHostRe.ReplaceAllString(f,
			"["+colorHostname+"$1"+colorReset+"]"+
				colorTreeChars+":"+colorReset+
				colorPort+"$2"+colorReset)
	}

	if isSSH {
		if hostIdx := findSSHHostIndex(fields); hostIdx > 0 {
			fields[hostIdx] = colorHostname + fields[hostIdx] + colorReset
		}
	}

	fields[0] = colorProcessName + fields[0] + colorReset
	return strings.Join(fields, " ")
}

// sshValueOpts is the set of single-character ssh(1) options that take a
// value, used by findSSHHostIndex to skip flag arguments while looking for
// the host. Booleans (-N, -T, -v, -q, -A, -C, -f, -g, -K, -k, -M, -n, -s,
// -t, -V, -x, -X, -Y, -y, -1, -2, -4, -6) are not listed.
var sshValueOpts = map[byte]bool{
	'b': true, 'B': true, 'c': true, 'D': true, 'e': true, 'E': true,
	'F': true, 'I': true, 'i': true, 'J': true, 'L': true, 'l': true,
	'm': true, 'O': true, 'o': true, 'p': true, 'Q': true, 'R': true,
	'S': true, 'W': true, 'w': true,
}

// findSSHHostIndex walks ssh argv looking for the first positional argument
// (the host), skipping options and their values. Returns -1 if no host
// argument is present.
func findSSHHostIndex(fields []string) int {
	for i := 1; i < len(fields); i++ {
		arg := fields[i]
		if arg == "--" {
			if i+1 < len(fields) {
				return i + 1
			}
			return -1
		}
		if !strings.HasPrefix(arg, "-") {
			return i
		}
		// Long option (--foo or --foo=bar): treat as boolean for our
		// purposes (current ssh has no long option that takes a value
		// without =).
		if strings.HasPrefix(arg, "--") {
			continue
		}
		// Short option: may be -X, -Xvalue, or -X value.
		if len(arg) < 2 {
			continue // bare "-"
		}
		optChar := arg[1]
		if !sshValueOpts[optChar] {
			continue // boolean flag (or cluster of booleans like -vv)
		}
		if len(arg) > 2 {
			// -Xvalue form — value is embedded, no extra arg to skip.
			continue
		}
		// -X value form — skip the value too.
		i++
	}
	return -1
}
