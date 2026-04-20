package daemon

import (
	"slices"
	"strings"
	"testing"
)

// containsOption checks that args has a matching "-o key=value" pair in order.
func containsOption(args []string, key, value string) bool {
	want := key + "=" + value
	for i := 0; i < len(args)-1; i++ {
		if args[i] == "-o" && args[i+1] == want {
			return true
		}
	}
	return false
}

// TestBuildTunnelSSHArgs_ForcesControlPersistNo guards the fix for the
// ControlMaster + ControlPersist fork-to-background bug. If the user's ssh
// config enables ControlPersist, OpenSSH forks the mux master into the
// background after authentication, leaving overseer tracking the already-exited
// parent PID. Forcing ControlPersist=no on the command line suppresses the
// detach fork while still leaving the mux master socket live for the duration
// of the tunnel, so interactive sessions, scp, and rsync keep multiplexing.
func TestBuildTunnelSSHArgs_ForcesControlPersistNo(t *testing.T) {
	args := buildTunnelSSHArgs("b1.fibianet.dk", "", 0, 0)

	if !containsOption(args, "ControlPersist", "no") {
		t.Fatalf("expected args to contain -o ControlPersist=no, got %v", args)
	}
}

func TestBuildTunnelSSHArgs_IncludesCoreOptions(t *testing.T) {
	args := buildTunnelSSHArgs("myhost", "", 0, 0)

	// Alias must be present
	if !slices.Contains(args, "myhost") {
		t.Errorf("expected alias 'myhost' in args, got %v", args)
	}

	// -N (no command) and -v (verbose for verifyConnection) are required
	for _, flag := range []string{"-N", "-v"} {
		if !slices.Contains(args, flag) {
			t.Errorf("expected %s in args, got %v", flag, args)
		}
	}

	if !containsOption(args, "ExitOnForwardFailure", "yes") {
		t.Errorf("expected ExitOnForwardFailure=yes, got %v", args)
	}
	if !containsOption(args, "IgnoreUnknown", "overseer-daemon") {
		t.Errorf("expected IgnoreUnknown=overseer-daemon, got %v", args)
	}

	// ProcessTag option must appear (value is runtime-dependent, just check key)
	found := false
	for i := 0; i < len(args)-1; i++ {
		if args[i] == "-o" && strings.HasPrefix(args[i+1], "overseer-daemon=") {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("expected -o overseer-daemon=<tag> in args, got %v", args)
	}
}

func TestBuildTunnelSSHArgs_PrependsConfigFile(t *testing.T) {
	args := buildTunnelSSHArgs("myhost", "/tmp/custom_ssh_config", 0, 0)

	if len(args) < 2 || args[0] != "-F" || args[1] != "/tmp/custom_ssh_config" {
		t.Errorf("expected args to start with -F /tmp/custom_ssh_config, got %v", args[:min(2, len(args))])
	}
}

func TestBuildTunnelSSHArgs_OmitsConfigFileWhenEmpty(t *testing.T) {
	args := buildTunnelSSHArgs("myhost", "", 0, 0)

	if slices.Contains(args, "-F") {
		t.Errorf("expected no -F flag when sshConfigFile is empty, got %v", args)
	}
}

func TestBuildTunnelSSHArgs_AddsServerAliveWhenConfigured(t *testing.T) {
	args := buildTunnelSSHArgs("myhost", "", 30, 3)

	if !containsOption(args, "ServerAliveInterval", "30") {
		t.Errorf("expected ServerAliveInterval=30, got %v", args)
	}
	if !containsOption(args, "ServerAliveCountMax", "3") {
		t.Errorf("expected ServerAliveCountMax=3, got %v", args)
	}
}

func TestBuildTunnelSSHArgs_OmitsServerAliveWhenZero(t *testing.T) {
	args := buildTunnelSSHArgs("myhost", "", 0, 3)

	for _, a := range args {
		if strings.HasPrefix(a, "ServerAliveInterval=") {
			t.Errorf("expected no ServerAliveInterval when interval is 0, got %v", args)
		}
	}
}
