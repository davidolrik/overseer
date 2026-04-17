package daemon

import (
	"os"
	"os/exec"
	"strconv"
	"strings"
)

// extractLocalForwardPorts resolves the SSH config for alias the same way
// resolveJumpChain does (via `ssh -G`, optionally with -F for a custom config
// file) and returns the local TCP ports overseer would bind for any
// DynamicForward or LocalForward directives that apply to the host.
//
// RemoteForward is intentionally ignored: those binds happen on the remote
// host, so they cannot conflict with a local listener.
//
// On any failure (ssh missing, alias unresolvable, permission error) this
// returns nil rather than an error — port-conflict diagnostics are best-effort
// and must never gate the connect failure path that called us.
func extractLocalForwardPorts(alias string, env map[string]string, sshConfigFile string) []int {
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
		return nil
	}
	return parseLocalForwardPorts(string(out))
}

// parseLocalForwardPorts pulls local bind ports out of `ssh -G` output. It
// recognises `dynamicforward` and `localforward` directives (case-insensitive
// so a hand-passed `-G` output works too, even though real ssh always emits
// lowercase). Returns ports in the order seen, deduped.
func parseLocalForwardPorts(sshGOutput string) []int {
	var ports []int
	seen := make(map[int]struct{})

	for _, line := range strings.Split(sshGOutput, "\n") {
		fields := strings.Fields(line)
		if len(fields) < 2 {
			continue
		}
		directive := strings.ToLower(fields[0])

		var spec string
		switch directive {
		case "dynamicforward":
			// "dynamicforward 25000" or "dynamicforward 127.0.0.1:25000"
			spec = fields[1]
		case "localforward":
			// "localforward 5432 host:5432" — first arg is the local bind spec
			spec = fields[1]
		default:
			continue
		}

		port, ok := portFromBindSpec(spec)
		if !ok {
			continue
		}
		if _, dup := seen[port]; dup {
			continue
		}
		seen[port] = struct{}{}
		ports = append(ports, port)
	}
	return ports
}

// portFromBindSpec extracts the port from an SSH forward bind spec. Accepts
// "25000", "127.0.0.1:25000", or "[::1]:25000" — i.e. an optional bind address
// followed by a port. Returns (0, false) for anything that doesn't end in a
// positive integer port.
func portFromBindSpec(spec string) (int, bool) {
	// LastIndex handles both "host:port" and "[v6]:port" — for IPv6 the
	// closing bracket comes before the final colon.
	if i := strings.LastIndex(spec, ":"); i >= 0 {
		spec = spec[i+1:]
	}
	port, err := strconv.Atoi(spec)
	if err != nil || port <= 0 {
		return 0, false
	}
	return port, true
}
