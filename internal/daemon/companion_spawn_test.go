package daemon

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
)

func TestSpawnCompanionWrapper_ExitCode(t *testing.T) {
	cmd, err := spawnCompanionWrapper("/bin/sh", []string{"sh", "-c", "exit 42"}, os.Environ(), "")
	if err != nil {
		t.Fatalf("spawn failed: %v", err)
	}
	err = cmd.Wait()
	if err == nil {
		t.Fatal("expected non-nil error for non-zero exit, got nil")
	}
	exitErr, ok := err.(*exec.ExitError)
	if !ok {
		t.Fatalf("expected *exec.ExitError, got %T: %v", err, err)
	}
	if exitErr.ExitCode() != 42 {
		t.Errorf("expected exit code 42, got %d", exitErr.ExitCode())
	}
}

func TestSpawnCompanionWrapper_Success(t *testing.T) {
	cmd, err := spawnCompanionWrapper("/usr/bin/true", []string{"true"}, os.Environ(), "")
	if err != nil {
		t.Fatalf("spawn failed: %v", err)
	}
	if err := cmd.Wait(); err != nil {
		t.Errorf("expected nil error for successful exit, got: %v", err)
	}
}

func TestSpawnCompanionWrapper_Env(t *testing.T) {
	tmp := t.TempDir()
	out := filepath.Join(tmp, "env")
	env := []string{"OVERSEER_SPAWN_TEST=hello_world", "PATH=/usr/bin:/bin"}

	cmd, err := spawnCompanionWrapper(
		"/bin/sh",
		[]string{"sh", "-c", "printf %s \"$OVERSEER_SPAWN_TEST\" > " + out},
		env,
		"",
	)
	if err != nil {
		t.Fatalf("spawn failed: %v", err)
	}
	if err := cmd.Wait(); err != nil {
		t.Fatalf("wait failed: %v", err)
	}

	data, err := os.ReadFile(out)
	if err != nil {
		t.Fatalf("read output: %v", err)
	}
	if string(data) != "hello_world" {
		t.Errorf("env not passed to child: got %q", string(data))
	}
}

func TestSpawnCompanionWrapper_Workdir(t *testing.T) {
	tmp := t.TempDir()
	out := filepath.Join(tmp, "pwd")

	cmd, err := spawnCompanionWrapper(
		"/bin/sh",
		[]string{"sh", "-c", "pwd > " + out},
		[]string{"PATH=/usr/bin:/bin"},
		tmp,
	)
	if err != nil {
		t.Fatalf("spawn failed: %v", err)
	}
	if err := cmd.Wait(); err != nil {
		t.Fatalf("wait failed: %v", err)
	}

	data, err := os.ReadFile(out)
	if err != nil {
		t.Fatalf("read output: %v", err)
	}

	// Resolve symlinks on both sides — macOS /var is a symlink to /private/var.
	wantResolved, _ := filepath.EvalSymlinks(tmp)
	gotResolved, _ := filepath.EvalSymlinks(strings.TrimSpace(string(data)))
	if gotResolved != wantResolved {
		t.Errorf("workdir not honored: got %q, want %q", gotResolved, wantResolved)
	}
}

func TestSpawnCompanionWrapper_NewSession(t *testing.T) {
	// Spawn a brief-sleep child so we can inspect its pgid while it's alive.
	// After POSIX_SPAWN_SETSID the child is its own session leader, which
	// means pgid == pid (session leaders always lead their own pgroup).
	cmd, err := spawnCompanionWrapper(
		"/bin/sh",
		[]string{"sh", "-c", "sleep 0.3"},
		[]string{"PATH=/usr/bin:/bin"},
		"",
	)
	if err != nil {
		t.Fatalf("spawn failed: %v", err)
	}
	pid := cmd.Process.Pid

	pgid, err := syscall.Getpgid(pid)
	if err != nil {
		t.Fatalf("getpgid(%d): %v", pid, err)
	}
	if pgid != pid {
		t.Errorf("child is not session leader: pgid=%d pid=%d (expected equal)", pgid, pid)
	}
	parentPgid, _ := syscall.Getpgid(os.Getpid())
	if pgid == parentPgid {
		t.Errorf("child shares parent's process group (pgid=%d); expected new session", pgid)
	}

	if err := cmd.Wait(); err != nil {
		t.Fatalf("wait failed: %v", err)
	}
}

func TestSpawnCompanionWrapper_BadPath(t *testing.T) {
	_, err := spawnCompanionWrapper("/nonexistent/binary/overseer-test", []string{"x"}, os.Environ(), "")
	if err == nil {
		t.Fatal("expected error spawning nonexistent binary, got nil")
	}
}
