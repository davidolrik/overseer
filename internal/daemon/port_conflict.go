package daemon

import (
	"errors"
	"fmt"
	"log/slog"
	"strconv"
	"strings"
	"time"

	"go.olrik.dev/overseer/internal/awareness/state"
	"go.olrik.dev/overseer/internal/portdiag"
)

// reportConnectFailure runs best-effort port-conflict diagnosis for a failed
// tunnel connect and emits the results through three channels:
//   - send: IPC stream to the CLI client (nil for background reconnects)
//   - slog: daemon stderr (visible via `overseer attach`)
//   - state log streamer: the user-facing log (visible via `overseer logs`)
func (d *Daemon) reportConnectFailure(alias string, env map[string]string, sshErr error, send func(message, status string)) {
	forwardPorts := extractLocalForwardPorts(alias, env, d.sshConfigFile)
	conflicts := findPortConflicts(forwardPorts)

	emit := func(message, status string) {
		if send != nil {
			send(message, status)
		}
		switch status {
		case "WARN":
			slog.Warn(message)
		default:
			slog.Error(message)
		}
		emitToUserLog(message, status)
	}

	if len(conflicts) > 0 {
		emit(sshErr.Error(), "ERROR")
		conflictPorts := make([]int, len(conflicts))
		for i, c := range conflicts {
			conflictPorts[i] = c.Port
		}
		emit(fmt.Sprintf("Tunnel '%s' failed to connect: %s",
			alias, portConflictHeadline(conflictPorts)), "ERROR")
		emitPortConflictTree(conflicts, emit)
	} else {
		emit(fmt.Sprintf("Tunnel '%s' failed to connect: %v", alias, sshErr), "ERROR")
	}
}

// emitToUserLog sends a message to the user-facing log stream (visible via
// `overseer logs`) as a system-category event.
func emitToUserLog(message, status string) {
	orch := GetStateOrchestrator()
	if orch == nil {
		return
	}
	streamer := orch.GetLogStreamer()
	if streamer == nil {
		return
	}
	level := state.LogError
	if status == "WARN" {
		level = state.LogWarn
	}
	streamer.Emit(state.LogEntry{
		Timestamp: time.Now(),
		Level:     level,
		Category:  state.CategoryTunnel,
		Message:   message,
		System: &state.SystemLogData{
			Event:   "tunnel",
			Details: message,
		},
	})
}

// findPortConflicts checks each port and returns a Holder for those that are
// actually in use. Ports that are free (or whose holder we can't see) are
// silently dropped, since the caller scans every configured forward and we
// don't want to spam non-conflicts when the tunnel failed for unrelated
// reasons (auth, network, etc.).
func findPortConflicts(ports []int) []*portdiag.Holder {
	var conflicts []*portdiag.Holder
	for _, port := range ports {
		h, err := portdiag.FindPortHolder(port)
		if err != nil {
			if errors.Is(err, portdiag.ErrHolderNotFound) {
				continue
			}
			conflicts = append(conflicts, &portdiag.Holder{
				Port:    port,
				Partial: true,
				Chain:   []portdiag.ProcessInfo{{Err: err}},
			})
			continue
		}
		conflicts = append(conflicts, h)
	}
	return conflicts
}

// emitPortConflictTree streams each holder's formatted ancestor tree via send.
func emitPortConflictTree(conflicts []*portdiag.Holder, send func(message, status string)) {
	for _, h := range conflicts {
		for _, line := range portdiag.FormatTree(h) {
			send(line, "ERROR")
		}
	}
}

// portConflictHeadline returns the human headline for the connect error when
// one or more configured forward ports are already bound by something else.
func portConflictHeadline(ports []int) string {
	switch len(ports) {
	case 0:
		return ""
	case 1:
		return fmt.Sprintf("port %d is already in use", ports[0])
	default:
		strs := make([]string, len(ports))
		for i, p := range ports {
			strs[i] = strconv.Itoa(p)
		}
		return fmt.Sprintf("ports %s are already in use", strings.Join(strs, ", "))
	}
}
