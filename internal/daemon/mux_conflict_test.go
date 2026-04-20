package daemon

import (
	"testing"
)

func TestParseMuxCheckOutput_AliveMaster(t *testing.T) {
	pid, alive := parseMuxCheckOutput("Master running (pid=12345)\r\n", nil)

	if !alive {
		t.Fatal("expected alive=true for 'Master running' output")
	}
	if pid != 12345 {
		t.Errorf("expected pid=12345, got %d", pid)
	}
}

// OpenSSH writes "Master running (pid=N)" to stderr, so the parser must match
// the pattern regardless of which stream it arrives on. The caller uses
// CombinedOutput which merges both into a single buffer.
func TestParseMuxCheckOutput_AliveMasterOnStderr(t *testing.T) {
	combined := "Master running (pid=86945)\n"
	pid, alive := parseMuxCheckOutput(combined, nil)

	if !alive || pid != 86945 {
		t.Errorf("expected alive=true pid=86945 from stderr output, got alive=%v pid=%d", alive, pid)
	}
}

func TestParseMuxCheckOutput_AliveMasterNoTrailingWhitespace(t *testing.T) {
	pid, alive := parseMuxCheckOutput("Master running (pid=9)", nil)

	if !alive || pid != 9 {
		t.Errorf("expected alive=true pid=9, got alive=%v pid=%d", alive, pid)
	}
}

func TestParseMuxCheckOutput_NoSocket(t *testing.T) {
	// ssh -O check on a host with no master exits non-zero with this stderr
	stderr := "Control socket connect(/home/user/.ssh/sockets/x): No such file or directory\n"
	pid, alive := parseMuxCheckOutput("", stderrErr(stderr))

	if alive {
		t.Errorf("expected alive=false when socket missing, got alive=true pid=%d", pid)
	}
}

func TestParseMuxCheckOutput_StaleSocket(t *testing.T) {
	stderr := "Control socket connect(/home/user/.ssh/sockets/x): Connection refused\n"
	pid, alive := parseMuxCheckOutput("", stderrErr(stderr))

	if alive {
		t.Errorf("expected alive=false for stale socket, got alive=true pid=%d", pid)
	}
}

func TestParseMuxCheckOutput_UnrecognizedOutput(t *testing.T) {
	// Defensive: if ssh changes its output format, we must not claim a master
	// is alive without a PID we can act on.
	pid, alive := parseMuxCheckOutput("something completely different\n", nil)

	if alive {
		t.Errorf("expected alive=false for unrecognized output, got alive=true pid=%d", pid)
	}
}

func TestParseMuxCheckOutput_MalformedPID(t *testing.T) {
	// "Master running" header without a parseable PID — treat as not alive
	// so we don't try to walk ancestors for garbage.
	pid, alive := parseMuxCheckOutput("Master running (pid=notanumber)\n", nil)

	if alive {
		t.Errorf("expected alive=false for malformed pid, got alive=true pid=%d", pid)
	}
}

// stderrErr fabricates an *exec.ExitError-like wrapper for tests. We only need
// parseMuxCheckOutput to see "exit was non-zero" — the exact error type is not
// inspected by the parser.
type fakeExitErr struct{ msg string }

func (e *fakeExitErr) Error() string { return e.msg }

func stderrErr(msg string) error { return &fakeExitErr{msg: msg} }
