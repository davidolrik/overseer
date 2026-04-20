package cmd

import (
	"os"

	"golang.org/x/term"
)

// isStdinTerminal reports whether stdin is attached to a terminal. Used to
// decide the default for --force on connect/reconnect: interactive shells
// (TTY) should not silently evict a user's existing ssh session, but
// scripts/cron/CI (no TTY) have no one around to resolve a conflict and
// should proceed.
//
// Overridable in tests via isStdinTerminalFn.
var isStdinTerminalFn = func() bool {
	return term.IsTerminal(int(os.Stdin.Fd()))
}

func isStdinTerminal() bool { return isStdinTerminalFn() }
