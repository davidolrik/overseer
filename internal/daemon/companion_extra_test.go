package daemon

import (
	"context"
	"net"
	"os"
	"os/exec"
	"strings"
	"syscall"
	"testing"
	"time"

	"go.olrik.dev/overseer/internal/core"
)


func TestParseOutputTimestamp(t *testing.T) {
	tests := []struct {
		name     string
		line     string
		wantOK   bool
		wantYear int // zero means don't check
	}{
		{
			name:     "valid format",
			line:     "2024-03-15 10:30:45 [output] some message",
			wantOK:   true,
			wantYear: 2024,
		},
		{
			name:   "too short",
			line:   "2024-03-15",
			wantOK: false,
		},
		{
			name:   "wrong format 19 chars but not a date",
			line:   "not a date at all!!",
			wantOK: false,
		},
		{
			name:   "empty string",
			line:   "",
			wantOK: false,
		},
		{
			name:     "exactly 19 chars valid timestamp",
			line:     "2025-01-01 00:00:00",
			wantOK:   true,
			wantYear: 2025,
		},
		{
			name:   "18 chars just under threshold",
			line:   "2025-01-01 00:00:0",
			wantOK: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, ok := parseOutputTimestamp(tt.line)
			if ok != tt.wantOK {
				t.Errorf("parseOutputTimestamp(%q) ok = %v, want %v", tt.line, ok, tt.wantOK)
			}
			if tt.wantOK && tt.wantYear != 0 && got.Year() != tt.wantYear {
				t.Errorf("parseOutputTimestamp(%q) year = %d, want %d", tt.line, got.Year(), tt.wantYear)
			}
		})
	}
}

func TestParseOutputTimestamp_TimeFields(t *testing.T) {
	ts, ok := parseOutputTimestamp("2024-06-15 14:30:59 [output] data")
	if !ok {
		t.Fatal("expected ok=true")
	}
	if ts.Month() != time.June || ts.Day() != 15 || ts.Hour() != 14 || ts.Minute() != 30 || ts.Second() != 59 {
		t.Errorf("unexpected parsed time: %v", ts)
	}
}

func TestGetCompanionSocketPath(t *testing.T) {
	path := getCompanionSocketPath("myalias", "myname")

	tmpDir := os.TempDir()
	if !strings.HasPrefix(path, tmpDir) {
		t.Errorf("expected path to start with %q, got %q", tmpDir, path)
	}
	if !strings.Contains(path, "myalias") {
		t.Errorf("expected path to contain alias 'myalias', got %q", path)
	}
	if !strings.Contains(path, "myname") {
		t.Errorf("expected path to contain name 'myname', got %q", path)
	}
	if !strings.Contains(path, "overseer-companion-") {
		t.Errorf("expected path to contain prefix 'overseer-companion-', got %q", path)
	}
	if !strings.HasSuffix(path, ".sock") {
		t.Errorf("expected path to end with .sock, got %q", path)
	}
}

func TestStopCompanions(t *testing.T) {
	t.Run("nil alias does not panic", func(t *testing.T) {
		cm := NewCompanionManager()
		cm.StopCompanions("nonexistent")
	})

	t.Run("skips persistent companions", func(t *testing.T) {
		quietLogger(t)
		cm := NewCompanionManager()

		cm.companions["server1"] = map[string]*CompanionProcess{
			"persistent-comp": {
				Name:   "persistent-comp",
				State:  CompanionStateRunning,
				Config: core.CompanionConfig{Persistent: true},
			},
		}

		cm.StopCompanions("server1")

		// Persistent companion should still be in the map
		if cm.companions["server1"]["persistent-comp"] == nil {
			t.Error("expected persistent companion to remain in map")
		}
	})
}

func TestStopAllCompanions(t *testing.T) {
	cm := NewCompanionManager()

	cm.companions["server1"] = map[string]*CompanionProcess{
		"comp1": {
			Name:  "comp1",
			State: CompanionStateStopped,
		},
	}
	cm.companions["server2"] = map[string]*CompanionProcess{
		"comp2": {
			Name:  "comp2",
			State: CompanionStateStopped,
		},
	}

	cm.StopAllCompanions()

	if len(cm.companions) != 0 {
		t.Errorf("expected empty companions map after StopAllCompanions, got %d entries", len(cm.companions))
	}
}

func TestStopSingleCompanion(t *testing.T) {
	t.Run("nonexistent alias returns nil", func(t *testing.T) {
		cm := NewCompanionManager()
		err := cm.StopSingleCompanion("nonexistent", "comp1")
		if err != nil {
			t.Errorf("expected nil error, got %v", err)
		}
	})

	t.Run("nonexistent companion returns nil", func(t *testing.T) {
		cm := NewCompanionManager()
		cm.companions["server1"] = map[string]*CompanionProcess{}

		err := cm.StopSingleCompanion("server1", "nonexistent")
		if err != nil {
			t.Errorf("expected nil error, got %v", err)
		}
	})
}

func TestClearCompanionHistory(t *testing.T) {
	t.Run("clears history from broadcasters", func(t *testing.T) {
		cm := NewCompanionManager()
		broadcaster := NewLogBroadcaster(100)
		broadcaster.Broadcast("message 1")
		broadcaster.Broadcast("message 2")

		cm.companions["server1"] = map[string]*CompanionProcess{
			"comp1": {
				Name:   "comp1",
				output: broadcaster,
			},
		}

		// Verify history exists before clearing
		_, history := broadcaster.SubscribeWithHistory(10)
		if len(history) != 2 {
			t.Fatalf("expected 2 history entries before clear, got %d", len(history))
		}

		cm.ClearCompanionHistory("server1")

		// Verify history is cleared
		ch, history := broadcaster.SubscribeWithHistory(10)
		defer broadcaster.Unsubscribe(ch)
		if len(history) != 0 {
			t.Errorf("expected 0 history entries after clear, got %d", len(history))
		}
	})

	t.Run("nil output broadcaster does not panic", func(t *testing.T) {
		cm := NewCompanionManager()
		cm.companions["server1"] = map[string]*CompanionProcess{
			"comp1": {
				Name:   "comp1",
				output: nil,
			},
		}

		// Should not panic
		cm.ClearCompanionHistory("server1")
	})

	t.Run("nonexistent alias does not panic", func(t *testing.T) {
		cm := NewCompanionManager()

		// Should not panic
		cm.ClearCompanionHistory("nonexistent")
	})
}

func TestStopProcess_NilProcess(t *testing.T) {
	cm := NewCompanionManager()
	// Should not panic
	cm.stopProcess(nil, "test", "alias")
}

func TestStopProcess_AlreadyStopped(t *testing.T) {
	quietLogger(t)
	cm := NewCompanionManager()

	proc := &CompanionProcess{
		Name:  "test-comp",
		State: CompanionStateStopped,
	}

	// Should return early without error
	cm.stopProcess(proc, "test-comp", "test-alias")
}

func TestStopProcess_AlreadyExited(t *testing.T) {
	quietLogger(t)
	cm := NewCompanionManager()

	proc := &CompanionProcess{
		Name:  "test-comp",
		State: CompanionStateExited,
	}

	cm.stopProcess(proc, "test-comp", "test-alias")
}

func TestStopProcess_RunningProcess(t *testing.T) {
	quietLogger(t)
	cm := NewCompanionManager()

	// Start a real process
	cmd := exec.Command("sleep", "60")
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	if err := cmd.Start(); err != nil {
		t.Fatalf("failed to start process: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	proc := &CompanionProcess{
		Name:   "test-comp",
		Cmd:    cmd,
		Pid:    cmd.Process.Pid,
		State:  CompanionStateRunning,
		Config: core.CompanionConfig{StopSignal: "TERM"},
		ctx:    ctx,
		cancel: cancel,
	}

	cm.stopProcess(proc, "test-comp", "test-alias")

	// Verify process is dead
	time.Sleep(500 * time.Millisecond)
	if err := cmd.Process.Signal(syscall.Signal(0)); err == nil {
		t.Error("expected process to be dead after stopProcess")
		cmd.Process.Kill()
	}
}

func TestStopProcess_SignalVariants(t *testing.T) {
	quietLogger(t)

	signals := []string{"INT", "TERM", "SIGTERM", "HUP", "SIGHUP", ""}
	for _, sig := range signals {
		t.Run("signal_"+sig, func(t *testing.T) {
			cm := NewCompanionManager()

			cmd := exec.Command("sleep", "60")
			cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
			if err := cmd.Start(); err != nil {
				t.Fatalf("failed to start process: %v", err)
			}

			ctx, cancel := context.WithCancel(context.Background())
			proc := &CompanionProcess{
				Name:   "test-comp",
				Cmd:    cmd,
				Pid:    cmd.Process.Pid,
				State:  CompanionStateRunning,
				Config: core.CompanionConfig{StopSignal: sig},
				ctx:    ctx,
				cancel: cancel,
			}

			cm.stopProcess(proc, "test-comp", "test-alias")

			time.Sleep(500 * time.Millisecond)
			if err := cmd.Process.Signal(syscall.Signal(0)); err == nil {
				cmd.Process.Kill()
			}
		})
	}
}

func TestHandleCompanionAttach_NoSuchCompanion(t *testing.T) {
	quietLogger(t)

	oldConfig := core.Config
	t.Cleanup(func() { core.Config = oldConfig })
	core.Config = &core.Configuration{
		Tunnels: map[string]*core.TunnelConfig{},
	}

	cm := NewCompanionManager()

	// Use a pipe to capture the output
	client, server := net.Pipe()
	defer client.Close()

	go cm.HandleCompanionAttach(server, "nonexistent", "comp1", false, 10)

	// Read the error message
	buf := make([]byte, 1024)
	n, _ := client.Read(buf)
	msg := string(buf[:n])

	if !strings.Contains(msg, "not found") {
		t.Errorf("expected 'not found' error message, got: %q", msg)
	}
}

func TestHandleCompanionAttach_CompanionNotConfigured(t *testing.T) {
	quietLogger(t)

	oldConfig := core.Config
	t.Cleanup(func() { core.Config = oldConfig })
	core.Config = &core.Configuration{
		Tunnels: map[string]*core.TunnelConfig{
			"my-tunnel": {
				Companions: []core.CompanionConfig{},
			},
		},
	}

	cm := NewCompanionManager()

	client, server := net.Pipe()
	defer client.Close()

	go cm.HandleCompanionAttach(server, "my-tunnel", "nonexistent-comp", false, 10)

	buf := make([]byte, 1024)
	n, _ := client.Read(buf)
	msg := string(buf[:n])

	if !strings.Contains(msg, "not configured") {
		t.Errorf("expected 'not configured' error message, got: %q", msg)
	}
}


func TestGetCompanionStatus_Empty(t *testing.T) {
	cm := NewCompanionManager()
	status := cm.GetCompanionStatus()
	if len(status) != 0 {
		t.Errorf("expected empty status map, got %d entries", len(status))
	}
}

func TestGetCompanionStatus_WithCompanions(t *testing.T) {
	cm := NewCompanionManager()
	cm.companions["tunnel1"] = map[string]*CompanionProcess{
		"comp1": {
			Name:      "comp1",
			Pid:       12345,
			State:     CompanionStateRunning,
			StartTime: time.Now(),
		},
	}

	status := cm.GetCompanionStatus()
	if len(status) != 1 {
		t.Fatalf("expected 1 tunnel entry, got %d", len(status))
	}
	companions := status["tunnel1"]
	if len(companions) != 1 {
		t.Fatalf("expected 1 companion, got %d", len(companions))
	}
	if companions[0].Name != "comp1" {
		t.Errorf("expected name 'comp1', got %q", companions[0].Name)
	}
	if companions[0].State != "running" {
		t.Errorf("expected state 'running', got %q", companions[0].State)
	}
}

func TestHasCompanions(t *testing.T) {
	cm := NewCompanionManager()
	if cm.HasCompanions("nonexistent") {
		t.Error("expected false for nonexistent alias")
	}

	cm.companions["tunnel1"] = map[string]*CompanionProcess{
		"comp1": {Name: "comp1"},
	}
	if !cm.HasCompanions("tunnel1") {
		t.Error("expected true for existing alias")
	}
}

func TestHasRunningCompanions(t *testing.T) {
	cm := NewCompanionManager()
	if cm.HasRunningCompanions("nonexistent") {
		t.Error("expected false for nonexistent alias")
	}

	cm.companions["tunnel1"] = map[string]*CompanionProcess{
		"comp1": {Name: "comp1", State: CompanionStateStopped},
	}
	if cm.HasRunningCompanions("tunnel1") {
		t.Error("expected false when all stopped")
	}

	cm.companions["tunnel1"]["comp2"] = &CompanionProcess{Name: "comp2", State: CompanionStateRunning}
	if !cm.HasRunningCompanions("tunnel1") {
		t.Error("expected true when one is running")
	}
}

func TestGetCompanion(t *testing.T) {
	cm := NewCompanionManager()

	if cm.GetCompanion("nonexistent", "comp") != nil {
		t.Error("expected nil for nonexistent alias")
	}

	cm.companions["tunnel1"] = map[string]*CompanionProcess{
		"comp1": {Name: "comp1"},
	}

	if cm.GetCompanion("tunnel1", "nonexistent") != nil {
		t.Error("expected nil for nonexistent companion")
	}

	proc := cm.GetCompanion("tunnel1", "comp1")
	if proc == nil {
		t.Fatal("expected non-nil process")
	}
	if proc.Name != "comp1" {
		t.Errorf("expected name 'comp1', got %q", proc.Name)
	}
}

func TestSetTokenRegistrar(t *testing.T) {
	cm := NewCompanionManager()

	var calledToken, calledAlias string
	cm.SetTokenRegistrar(func(token, alias string) {
		calledToken = token
		calledAlias = alias
	})

	cm.registerToken("mytoken", "myalias")
	if calledToken != "mytoken" {
		t.Errorf("expected token 'mytoken', got %q", calledToken)
	}
	if calledAlias != "myalias" {
		t.Errorf("expected alias 'myalias', got %q", calledAlias)
	}
}

func TestSetEventLogger(t *testing.T) {
	cm := NewCompanionManager()

	var logged bool
	cm.SetEventLogger(func(alias, eventType, details string) error {
		logged = true
		return nil
	})

	cm.logCompanionEvent("alias", "name", "event", "details")
	if !logged {
		t.Error("expected event logger to be called")
	}
}
