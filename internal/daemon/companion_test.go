package daemon

import (
	"log/slog"
	"os"
	"strings"
	"testing"
	"time"

	"go.olrik.dev/overseer/internal/core"
)

// quietLoggerCompanion suppresses default slog output during tests.
func quietLoggerCompanion(t *testing.T) {
	t.Helper()
	old := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.Level(99)})))
	t.Cleanup(func() { slog.SetDefault(old) })
}

func TestNewCompanionManager(t *testing.T) {
	cm := NewCompanionManager()
	if cm == nil {
		t.Fatal("expected non-nil CompanionManager")
	}
	if cm.companions == nil {
		t.Error("expected companions map to be initialized")
	}
	if len(cm.companions) != 0 {
		t.Errorf("expected empty companions map, got %d entries", len(cm.companions))
	}
}

func TestCompanionManager_SetTokenRegistrar(t *testing.T) {
	cm := NewCompanionManager()

	called := false
	cm.SetTokenRegistrar(func(token, alias string) {
		called = true
		if token != "test-token" {
			t.Errorf("expected token 'test-token', got %q", token)
		}
		if alias != "server1" {
			t.Errorf("expected alias 'server1', got %q", alias)
		}
	})

	if cm.registerToken == nil {
		t.Fatal("expected registerToken to be set")
	}

	cm.registerToken("test-token", "server1")
	if !called {
		t.Error("expected registrar callback to be called")
	}
}

func TestCompanionManager_SetEventLogger(t *testing.T) {
	cm := NewCompanionManager()

	var logged []string
	cm.SetEventLogger(func(alias, eventType, details string) error {
		logged = append(logged, eventType)
		return nil
	})

	if cm.logEvent == nil {
		t.Fatal("expected logEvent to be set")
	}

	// Call it via logCompanionEvent
	cm.logCompanionEvent("server1", "comp1", "started", "process started")
	if len(logged) != 1 || logged[0] != "started" {
		t.Errorf("expected [started], got %v", logged)
	}
}

func TestCompanionManager_GetCompanionStatus_Empty(t *testing.T) {
	cm := NewCompanionManager()

	status := cm.GetCompanionStatus()
	if len(status) != 0 {
		t.Errorf("expected empty status, got %d entries", len(status))
	}
}

func TestCompanionManager_LogCompanionEvent_NilLogger(t *testing.T) {
	cm := NewCompanionManager()
	// logEvent is nil — should not panic
	cm.logCompanionEvent("server1", "comp1", "test", "details")
}

func TestFormatDaemonMessage(t *testing.T) {
	msg := formatDaemonMessage("hello %s", "world")

	// Should contain timestamp in expected format
	now := time.Now().Format("2006-01-02")
	if !strings.Contains(msg, now) {
		t.Errorf("expected message to contain date %q, got %q", now, msg)
	}
	if !strings.Contains(msg, "[DAEMON]") {
		t.Errorf("expected [DAEMON] prefix, got %q", msg)
	}
	if !strings.Contains(msg, "hello world") {
		t.Errorf("expected 'hello world' in message, got %q", msg)
	}
}

func TestGenerateCompanionToken(t *testing.T) {
	token, err := generateCompanionToken()
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	// 32 bytes = 64 hex chars
	if len(token) != 64 {
		t.Errorf("expected 64-char hex string, got %d chars: %q", len(token), token)
	}

	// Should be valid hex
	for _, c := range token {
		if !((c >= '0' && c <= '9') || (c >= 'a' && c <= 'f')) {
			t.Errorf("unexpected char %c in token", c)
		}
	}

	// Generate another and verify uniqueness
	token2, err := generateCompanionToken()
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if token == token2 {
		t.Error("expected unique tokens")
	}
}

func TestCompanionManager_GetCompanionStatus_WithData(t *testing.T) {
	cm := NewCompanionManager()

	exitCode := 1
	cm.companions["server1"] = map[string]*CompanionProcess{
		"comp1": {
			Name:      "comp1",
			Pid:       1234,
			State:     CompanionStateRunning,
			StartTime: time.Now(),
			Config:    core.CompanionConfig{Command: "echo hello"},
		},
		"comp2": {
			Name:      "comp2",
			Pid:       0,
			State:     CompanionStateFailed,
			ExitCode:  &exitCode,
			ExitError: "signal: killed",
			Config:    core.CompanionConfig{Command: "bad-cmd"},
		},
	}

	status := cm.GetCompanionStatus()
	if len(status) != 1 {
		t.Fatalf("expected 1 tunnel, got %d", len(status))
	}

	statuses, ok := status["server1"]
	if !ok {
		t.Fatal("expected 'server1' in status")
	}
	if len(statuses) != 2 {
		t.Fatalf("expected 2 companions, got %d", len(statuses))
	}

	found := map[string]CompanionStatus{}
	for _, s := range statuses {
		found[s.Name] = s
	}

	if comp1, ok := found["comp1"]; ok {
		if comp1.State != "running" {
			t.Errorf("expected state 'running', got %q", comp1.State)
		}
		if comp1.Pid != 1234 {
			t.Errorf("expected PID 1234, got %d", comp1.Pid)
		}
	} else {
		t.Error("expected comp1 in status")
	}

	if comp2, ok := found["comp2"]; ok {
		if comp2.ExitCode == nil || *comp2.ExitCode != 1 {
			t.Errorf("expected exit code 1, got %v", comp2.ExitCode)
		}
		if comp2.ExitError != "signal: killed" {
			t.Errorf("expected exit error 'signal: killed', got %q", comp2.ExitError)
		}
	} else {
		t.Error("expected comp2 in status")
	}
}

func TestCompanionManager_HasCompanions_WithData(t *testing.T) {
	cm := NewCompanionManager()

	cm.companions["server1"] = map[string]*CompanionProcess{
		"comp1": {Name: "comp1", State: CompanionStateStopped},
	}

	if !cm.HasCompanions("server1") {
		t.Error("expected HasCompanions=true")
	}
	if cm.HasCompanions("nonexistent") {
		t.Error("expected HasCompanions=false for nonexistent")
	}
}

func TestCompanionManager_HasRunningCompanions_WithData(t *testing.T) {
	cm := NewCompanionManager()

	t.Run("stopped companion is not running", func(t *testing.T) {
		cm.companions["server1"] = map[string]*CompanionProcess{
			"comp1": {Name: "comp1", State: CompanionStateStopped},
		}
		if cm.HasRunningCompanions("server1") {
			t.Error("expected HasRunningCompanions=false for stopped")
		}
	})

	t.Run("running companion is running", func(t *testing.T) {
		cm.companions["server2"] = map[string]*CompanionProcess{
			"comp1": {Name: "comp1", State: CompanionStateRunning},
		}
		if !cm.HasRunningCompanions("server2") {
			t.Error("expected HasRunningCompanions=true for running")
		}
	})

	t.Run("ready companion is running", func(t *testing.T) {
		cm.companions["server3"] = map[string]*CompanionProcess{
			"comp1": {Name: "comp1", State: CompanionStateReady},
		}
		if !cm.HasRunningCompanions("server3") {
			t.Error("expected HasRunningCompanions=true for ready")
		}
	})

	t.Run("waiting companion is running", func(t *testing.T) {
		cm.companions["server4"] = map[string]*CompanionProcess{
			"comp1": {Name: "comp1", State: CompanionStateWaiting},
		}
		if !cm.HasRunningCompanions("server4") {
			t.Error("expected HasRunningCompanions=true for waiting")
		}
	})
}

func TestCompanionManager_GetCompanion_WithData(t *testing.T) {
	cm := NewCompanionManager()

	proc := &CompanionProcess{Name: "comp1", State: CompanionStateRunning}
	cm.companions["server1"] = map[string]*CompanionProcess{
		"comp1": proc,
	}

	got := cm.GetCompanion("server1", "comp1")
	if got != proc {
		t.Error("expected to get the stored process")
	}

	got = cm.GetCompanion("server1", "nonexistent")
	if got != nil {
		t.Error("expected nil for nonexistent companion")
	}
}

func TestCompanionManager_HasCompanions(t *testing.T) {
	cm := NewCompanionManager()

	if cm.HasCompanions("nonexistent") {
		t.Error("expected HasCompanions=false for nonexistent alias")
	}
}

func TestCompanionManager_HasRunningCompanions(t *testing.T) {
	cm := NewCompanionManager()

	if cm.HasRunningCompanions("nonexistent") {
		t.Error("expected HasRunningCompanions=false for nonexistent alias")
	}
}

func TestCompanionManager_GetCompanion(t *testing.T) {
	cm := NewCompanionManager()

	comp := cm.GetCompanion("server1", "comp1")
	if comp != nil {
		t.Error("expected nil for nonexistent companion")
	}
}

func TestExpandPath(t *testing.T) {
	t.Run("tilde prefix", func(t *testing.T) {
		result := expandPath("~/test")
		if strings.HasPrefix(result, "~/") {
			t.Error("expected ~ to be expanded")
		}
		if !strings.HasSuffix(result, "/test") {
			t.Errorf("expected path to end with /test, got %q", result)
		}
	})

	t.Run("no tilde", func(t *testing.T) {
		result := expandPath("/absolute/path")
		if result != "/absolute/path" {
			t.Errorf("expected '/absolute/path', got %q", result)
		}
	})

	t.Run("relative path", func(t *testing.T) {
		result := expandPath("relative/path")
		if result != "relative/path" {
			t.Errorf("expected 'relative/path', got %q", result)
		}
	})
}

func TestGetCompanionStatePath(t *testing.T) {
	oldConfig := core.Config
	defer func() { core.Config = oldConfig }()

	core.Config = &core.Configuration{
		ConfigPath: "/tmp/test-overseer",
	}

	expected := "/tmp/test-overseer/companion_state.json"
	if got := GetCompanionStatePath(); got != expected {
		t.Errorf("expected %q, got %q", expected, got)
	}
}

func TestCompanionManager_SaveCompanionState(t *testing.T) {
	tmpDir := t.TempDir()
	quietLoggerCompanion(t)

	oldConfig := core.Config
	defer func() { core.Config = oldConfig }()
	core.Config = &core.Configuration{
		ConfigPath: tmpDir,
	}

	cm := NewCompanionManager()
	cm.companions["server1"] = map[string]*CompanionProcess{
		"comp1": {
			Name:      "comp1",
			Pid:       5678,
			State:     CompanionStateRunning,
			StartTime: time.Now(),
			Config:    core.CompanionConfig{Command: "echo hello", Workdir: "/tmp"},
		},
		"comp2": {
			Name:  "comp2",
			Pid:   0,
			State: CompanionStateStopped, // Stopped — should be skipped
		},
	}

	if err := cm.SaveCompanionState(); err != nil {
		t.Fatalf("SaveCompanionState failed: %v", err)
	}

	// Load and verify
	loaded, err := LoadCompanionState()
	if err != nil {
		t.Fatalf("LoadCompanionState failed: %v", err)
	}
	if loaded == nil {
		t.Fatal("expected non-nil state")
	}
	if len(loaded.Tunnels) != 1 {
		t.Fatalf("expected 1 tunnel, got %d", len(loaded.Tunnels))
	}
	if len(loaded.Tunnels[0].Companions) != 1 {
		t.Fatalf("expected 1 companion (stopped skipped), got %d", len(loaded.Tunnels[0].Companions))
	}
	if loaded.Tunnels[0].Companions[0].Name != "comp1" {
		t.Errorf("expected comp1, got %q", loaded.Tunnels[0].Companions[0].Name)
	}
}

func TestLoadCompanionState_FileDoesNotExist(t *testing.T) {
	tmpDir := t.TempDir()

	oldConfig := core.Config
	defer func() { core.Config = oldConfig }()
	core.Config = &core.Configuration{
		ConfigPath: tmpDir,
	}

	loaded, err := LoadCompanionState()
	if err != nil {
		t.Fatalf("expected nil error, got %v", err)
	}
	if loaded != nil {
		t.Error("expected nil state")
	}
}

func TestLoadCompanionState_InvalidJSON(t *testing.T) {
	tmpDir := t.TempDir()

	oldConfig := core.Config
	defer func() { core.Config = oldConfig }()
	core.Config = &core.Configuration{
		ConfigPath: tmpDir,
	}

	os.WriteFile(tmpDir+"/companion_state.json", []byte("not json"), 0600)

	_, err := LoadCompanionState()
	if err == nil {
		t.Fatal("expected error for invalid JSON")
	}
}

func TestLoadCompanionState_WrongVersion(t *testing.T) {
	tmpDir := t.TempDir()

	oldConfig := core.Config
	defer func() { core.Config = oldConfig }()
	core.Config = &core.Configuration{
		ConfigPath: tmpDir,
	}

	os.WriteFile(tmpDir+"/companion_state.json", []byte(`{"version":"999"}`), 0600)

	_, err := LoadCompanionState()
	if err == nil {
		t.Fatal("expected error for wrong version")
	}
}

func TestRemoveCompanionStateFile(t *testing.T) {
	t.Run("file exists", func(t *testing.T) {
		tmpDir := t.TempDir()

		oldConfig := core.Config
		defer func() { core.Config = oldConfig }()
		core.Config = &core.Configuration{ConfigPath: tmpDir}

		os.WriteFile(tmpDir+"/companion_state.json", []byte("{}"), 0600)
		if err := RemoveCompanionStateFile(); err != nil {
			t.Fatalf("RemoveCompanionStateFile failed: %v", err)
		}
	})

	t.Run("file does not exist", func(t *testing.T) {
		tmpDir := t.TempDir()

		oldConfig := core.Config
		defer func() { core.Config = oldConfig }()
		core.Config = &core.Configuration{ConfigPath: tmpDir}

		if err := RemoveCompanionStateFile(); err != nil {
			t.Fatalf("expected no error, got %v", err)
		}
	})
}
