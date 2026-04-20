package cmd

import (
	"testing"
)

// connectForceDefault mirrors the default-resolution logic in connect.go and
// reconnect.go so we can test it without plumbing a fake *cobra.Command. It
// returns what `--force` ends up as when the user didn't explicitly set it.
func connectForceDefault(flagChanged, flagValue, tty bool) bool {
	if flagChanged {
		return flagValue
	}
	return !tty
}

func TestForceDefault_InteractiveTTY_DoesNotForce(t *testing.T) {
	if connectForceDefault(false, false, true) {
		t.Error("expected force=false when stdin is a TTY and flag not set")
	}
}

func TestForceDefault_NonInteractive_Forces(t *testing.T) {
	if !connectForceDefault(false, false, false) {
		t.Error("expected force=true when stdin is not a TTY and flag not set")
	}
}

func TestForceDefault_ExplicitFalse_HonoredOverNonTTY(t *testing.T) {
	// User ran `overseer connect foo --force=false | tee log.txt` — even
	// though stdout is piped, they explicitly asked for no-force.
	if connectForceDefault(true, false, false) {
		t.Error("expected explicit --force=false to win over auto-detection")
	}
}

func TestForceDefault_ExplicitTrue_HonoredOnTTY(t *testing.T) {
	if !connectForceDefault(true, true, true) {
		t.Error("expected explicit --force=true to win over auto-detection")
	}
}

func TestIsStdinTerminal_OverridableForTests(t *testing.T) {
	// Preserve and restore the hook so this test doesn't leak state.
	orig := isStdinTerminalFn
	t.Cleanup(func() { isStdinTerminalFn = orig })

	isStdinTerminalFn = func() bool { return true }
	if !isStdinTerminal() {
		t.Error("expected isStdinTerminal to return true when hook returns true")
	}

	isStdinTerminalFn = func() bool { return false }
	if isStdinTerminal() {
		t.Error("expected isStdinTerminal to return false when hook returns false")
	}
}
