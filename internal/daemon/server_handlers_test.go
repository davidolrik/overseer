package daemon

import (
	"encoding/json"
	"log/slog"
	"os"
	"strings"
	"testing"
	"time"

	"go.olrik.dev/overseer/internal/awareness"
	"go.olrik.dev/overseer/internal/core"
)

// quietLogger suppresses default slog output during tests and restores it after.
func quietLogger(t *testing.T) {
	t.Helper()
	old := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.Level(99)})))
	t.Cleanup(func() { slog.SetDefault(old) })
}

func TestNew(t *testing.T) {
	oldConfig := core.Config
	defer func() { core.Config = oldConfig }()
	core.Config = &core.Configuration{
		Companion: core.CompanionSettings{HistorySize: 50},
	}

	d := New()
	if d == nil {
		t.Fatal("expected non-nil Daemon")
	}
	if d.tunnels == nil {
		t.Error("expected tunnels map to be initialized")
	}
	if d.askpassTokens == nil {
		t.Error("expected askpassTokens map to be initialized")
	}
	if d.logBroadcast == nil {
		t.Error("expected logBroadcast to be initialized")
	}
	if d.companionMgr == nil {
		t.Error("expected companionMgr to be initialized")
	}
	if d.ctx == nil {
		t.Error("expected context to be initialized")
	}

	// Verify token registrar wiring: when companion registers a token,
	// it should appear in daemon's askpassTokens
	d.companionMgr.registerToken("test-token", "server1")
	d.mu.Lock()
	alias, ok := d.askpassTokens["test-token"]
	d.mu.Unlock()
	if !ok {
		t.Fatal("expected token to be registered")
	}
	if alias != "server1" {
		t.Errorf("expected alias 'server1', got %q", alias)
	}
}

func TestSetSSHConfigFile(t *testing.T) {
	oldConfig := core.Config
	defer func() { core.Config = oldConfig }()
	core.Config = &core.Configuration{}

	d := New()
	d.SetSSHConfigFile("/path/to/ssh_config")
	if d.sshConfigFile != "/path/to/ssh_config" {
		t.Errorf("expected '/path/to/ssh_config', got %q", d.sshConfigFile)
	}
}

func TestMergeEnvironment(t *testing.T) {
	t.Run("defaults overridden by user values", func(t *testing.T) {
		defaults := map[string]string{"A": "1", "B": "2"}
		user := map[string]string{"B": "override", "C": "3"}

		merged := mergeEnvironment(defaults, user)

		if merged["A"] != "1" {
			t.Errorf("expected A=1, got %q", merged["A"])
		}
		if merged["B"] != "override" {
			t.Errorf("expected B=override, got %q", merged["B"])
		}
		if merged["C"] != "3" {
			t.Errorf("expected C=3, got %q", merged["C"])
		}
	})

	t.Run("nil maps", func(t *testing.T) {
		merged := mergeEnvironment(nil, nil)
		if len(merged) != 0 {
			t.Errorf("expected empty map, got %v", merged)
		}
	})

	t.Run("only defaults", func(t *testing.T) {
		merged := mergeEnvironment(map[string]string{"X": "1"}, nil)
		if merged["X"] != "1" {
			t.Errorf("expected X=1, got %q", merged["X"])
		}
	})
}

func TestMergeLocation(t *testing.T) {
	t.Run("display name override", func(t *testing.T) {
		defaultLoc := awareness.Location{
			Name:        "offline",
			DisplayName: "Default Offline",
			Environment: map[string]string{"A": "1"},
		}
		userLoc := awareness.Location{
			DisplayName: "Custom Offline",
		}

		merged := mergeLocation(defaultLoc, userLoc)
		if merged.DisplayName != "Custom Offline" {
			t.Errorf("expected 'Custom Offline', got %q", merged.DisplayName)
		}
	})

	t.Run("env merge", func(t *testing.T) {
		defaultLoc := awareness.Location{
			Environment: map[string]string{"A": "1", "B": "2"},
		}
		userLoc := awareness.Location{
			Environment: map[string]string{"B": "override", "C": "3"},
		}

		merged := mergeLocation(defaultLoc, userLoc)
		if merged.Environment["A"] != "1" {
			t.Errorf("expected A=1, got %q", merged.Environment["A"])
		}
		if merged.Environment["B"] != "override" {
			t.Errorf("expected B=override, got %q", merged.Environment["B"])
		}
		if merged.Environment["C"] != "3" {
			t.Errorf("expected C=3, got %q", merged.Environment["C"])
		}
	})

	t.Run("empty user preserves defaults", func(t *testing.T) {
		defaultLoc := awareness.Location{
			DisplayName: "Default",
			Environment: map[string]string{"X": "1"},
		}
		merged := mergeLocation(defaultLoc, awareness.Location{})
		if merged.DisplayName != "Default" {
			t.Errorf("expected 'Default', got %q", merged.DisplayName)
		}
	})
}

func TestMergeRule(t *testing.T) {
	t.Run("display name override", func(t *testing.T) {
		defaultRule := awareness.Rule{
			Name:        "untrusted",
			DisplayName: "Default",
		}
		userRule := awareness.Rule{
			DisplayName: "Custom",
		}

		merged := mergeRule(defaultRule, userRule)
		if merged.DisplayName != "Custom" {
			t.Errorf("expected 'Custom', got %q", merged.DisplayName)
		}
	})

	t.Run("env merge", func(t *testing.T) {
		defaultRule := awareness.Rule{
			Environment: map[string]string{"A": "1"},
		}
		userRule := awareness.Rule{
			Environment: map[string]string{"A": "override", "B": "2"},
		}

		merged := mergeRule(defaultRule, userRule)
		if merged.Environment["A"] != "override" {
			t.Errorf("expected A=override, got %q", merged.Environment["A"])
		}
		if merged.Environment["B"] != "2" {
			t.Errorf("expected B=2, got %q", merged.Environment["B"])
		}
	})

	t.Run("actions override", func(t *testing.T) {
		defaultRule := awareness.Rule{
			Actions: awareness.RuleActions{
				Connect: []string{"default_tunnel"},
			},
		}
		userRule := awareness.Rule{
			Actions: awareness.RuleActions{
				Connect:    []string{"user_tunnel"},
				Disconnect: []string{"other_tunnel"},
			},
		}

		merged := mergeRule(defaultRule, userRule)
		if len(merged.Actions.Connect) != 1 || merged.Actions.Connect[0] != "user_tunnel" {
			t.Errorf("expected connect=[user_tunnel], got %v", merged.Actions.Connect)
		}
	})

	t.Run("empty user actions preserve default", func(t *testing.T) {
		defaultRule := awareness.Rule{
			Actions: awareness.RuleActions{
				Connect: []string{"default_tunnel"},
			},
		}
		merged := mergeRule(defaultRule, awareness.Rule{})
		if len(merged.Actions.Connect) != 1 || merged.Actions.Connect[0] != "default_tunnel" {
			t.Errorf("expected default connect preserved, got %v", merged.Actions.Connect)
		}
	})
}

func TestCalculateBackoff(t *testing.T) {
	oldConfig := core.Config
	defer func() { core.Config = oldConfig }()

	core.Config = &core.Configuration{
		SSH: core.SSHConfig{
			InitialBackoff: "1s",
			MaxBackoff:     "1m",
			BackoffFactor:  2,
		},
	}

	t.Run("retryCount zero", func(t *testing.T) {
		d := calculateBackoff(0)
		if d != 1*time.Second {
			t.Errorf("expected 1s, got %v", d)
		}
	})

	t.Run("exponential growth", func(t *testing.T) {
		d1 := calculateBackoff(1)
		d2 := calculateBackoff(2)
		d3 := calculateBackoff(3)

		if d1 != 2*time.Second {
			t.Errorf("retry 1: expected 2s, got %v", d1)
		}
		if d2 != 4*time.Second {
			t.Errorf("retry 2: expected 4s, got %v", d2)
		}
		if d3 != 8*time.Second {
			t.Errorf("retry 3: expected 8s, got %v", d3)
		}
	})

	t.Run("cap at max", func(t *testing.T) {
		d := calculateBackoff(100)
		if d > 1*time.Minute {
			t.Errorf("expected backoff capped at 1m, got %v", d)
		}
	})

	t.Run("invalid config falls back to defaults", func(t *testing.T) {
		quietLogger(t)
		core.Config = &core.Configuration{
			SSH: core.SSHConfig{
				InitialBackoff: "not-a-duration",
				MaxBackoff:     "also-invalid",
				BackoffFactor:  2,
			},
		}

		d := calculateBackoff(0)
		if d != 1*time.Second {
			t.Errorf("expected fallback initial 1s, got %v", d)
		}
	})
}

func TestGetStatus(t *testing.T) {
	t.Run("empty tunnels", func(t *testing.T) {
		d := &Daemon{
			tunnels: map[string]Tunnel{},
		}

		resp := d.getStatus()
		if len(resp.Messages) != 1 {
			t.Fatalf("expected 1 message, got %d", len(resp.Messages))
		}
		if resp.Messages[0].Status != "WARN" {
			t.Errorf("expected WARN status, got %q", resp.Messages[0].Status)
		}
		if !strings.Contains(resp.Messages[0].Message, "No tunnels") {
			t.Errorf("expected 'No tunnels' message, got %q", resp.Messages[0].Message)
		}

		// Data should be empty array
		data, err := json.Marshal(resp.Data)
		if err != nil {
			t.Fatal(err)
		}
		if string(data) != "[]" {
			t.Errorf("expected empty array data, got %s", data)
		}
	})

	t.Run("single connected tunnel", func(t *testing.T) {
		now := time.Now()
		d := &Daemon{
			tunnels: map[string]Tunnel{
				"server1": {
					Hostname:          "server1.example.com",
					Pid:               1234,
					StartDate:         now,
					LastConnectedTime: now,
					State:             StateConnected,
					AutoReconnect:     true,
					Environment:       map[string]string{"OVERSEER_TAG": "work"},
				},
			},
		}

		resp := d.getStatus()
		if resp.Messages[0].Status != "INFO" {
			t.Errorf("expected INFO status, got %q", resp.Messages[0].Status)
		}

		statuses, ok := resp.Data.([]DaemonStatus)
		if !ok {
			t.Fatal("expected []DaemonStatus data")
		}
		if len(statuses) != 1 {
			t.Fatalf("expected 1 status, got %d", len(statuses))
		}
		if statuses[0].Hostname != "server1" {
			t.Errorf("expected hostname 'server1', got %q", statuses[0].Hostname)
		}
		if statuses[0].State != StateConnected {
			t.Errorf("expected state Connected, got %q", statuses[0].State)
		}
		if statuses[0].Environment["OVERSEER_TAG"] != "work" {
			t.Errorf("expected environment OVERSEER_TAG='work', got %q", statuses[0].Environment["OVERSEER_TAG"])
		}
	})

	t.Run("environment includes computed OVERSEER_ variables", func(t *testing.T) {
		now := time.Now()
		d := &Daemon{
			tunnels: map[string]Tunnel{
				"server1": {
					Hostname:          "server1.example.com",
					Pid:               1234,
					StartDate:         now,
					LastConnectedTime: now,
					State:             StateConnected,
					Environment: map[string]string{
						"MY_VAR":            "user-value",
						"OVERSEER_CONTEXT":  "trusted",
						"OVERSEER_LOCATION": "home",
					},
				},
			},
		}

		resp := d.getStatus()
		statuses := resp.Data.([]DaemonStatus)
		env := statuses[0].Environment

		// API must return all keys — filtering is display-only
		if env["MY_VAR"] != "user-value" {
			t.Errorf("expected MY_VAR='user-value', got %q", env["MY_VAR"])
		}
		if env["OVERSEER_CONTEXT"] != "trusted" {
			t.Errorf("expected OVERSEER_CONTEXT='trusted', got %q", env["OVERSEER_CONTEXT"])
		}
		if env["OVERSEER_LOCATION"] != "home" {
			t.Errorf("expected OVERSEER_LOCATION='home', got %q", env["OVERSEER_LOCATION"])
		}
	})

	t.Run("disconnected tunnel includes times", func(t *testing.T) {
		now := time.Now()
		d := &Daemon{
			tunnels: map[string]Tunnel{
				"server1": {
					Pid:              1234,
					StartDate:        now.Add(-time.Hour),
					DisconnectedTime: now,
					State:            StateDisconnected,
				},
			},
		}

		resp := d.getStatus()
		statuses := resp.Data.([]DaemonStatus)
		if statuses[0].DisconnectedTime == "" {
			t.Error("expected disconnected time to be set")
		}
	})

	t.Run("reconnecting tunnel includes next retry", func(t *testing.T) {
		now := time.Now()
		nextRetry := now.Add(30 * time.Second)
		d := &Daemon{
			tunnels: map[string]Tunnel{
				"server1": {
					Pid:              1234,
					StartDate:        now.Add(-time.Hour),
					DisconnectedTime: now,
					State:            StateReconnecting,
					NextRetryTime:    nextRetry,
				},
			},
		}

		resp := d.getStatus()
		statuses := resp.Data.([]DaemonStatus)
		if statuses[0].NextRetry == "" {
			t.Error("expected next retry time to be set")
		}
		if statuses[0].DisconnectedTime == "" {
			t.Error("expected disconnected time to be set for reconnecting tunnel")
		}
	})
}

func TestGetVersion(t *testing.T) {
	d := &Daemon{
		isRemote: false,
	}

	resp := d.getVersion()
	if resp.Messages[0].Status != "INFO" {
		t.Errorf("expected INFO, got %q", resp.Messages[0].Status)
	}

	data, ok := resp.Data.(map[string]interface{})
	if !ok {
		t.Fatal("expected map data")
	}
	if _, exists := data["version"]; !exists {
		t.Error("expected 'version' in data")
	}
	if data["is_remote"] != false {
		t.Errorf("expected is_remote=false, got %v", data["is_remote"])
	}
}

func TestGetVersion_Remote(t *testing.T) {
	d := &Daemon{
		isRemote:      true,
		parentMonitor: &ParentMonitor{monitoredPID: 42},
	}

	resp := d.getVersion()
	data := resp.Data.(map[string]interface{})

	if data["is_remote"] != true {
		t.Error("expected is_remote=true")
	}
	if data["monitored_pid"] != 42 {
		t.Errorf("expected monitored_pid=42, got %v", data["monitored_pid"])
	}
}

func TestHandleAskpass(t *testing.T) {
	t.Run("invalid token", func(t *testing.T) {
		d := &Daemon{
			askpassTokens: map[string]string{},
		}

		resp := d.handleAskpass("server1", "bad-token")
		if resp.Messages[0].Status != "ERROR" {
			t.Errorf("expected ERROR, got %q", resp.Messages[0].Status)
		}
	})

	t.Run("wrong alias for token", func(t *testing.T) {
		d := &Daemon{
			askpassTokens: map[string]string{
				"valid-token": "server1",
			},
		}

		resp := d.handleAskpass("server2", "valid-token")
		if resp.Messages[0].Status != "ERROR" {
			t.Errorf("expected ERROR for wrong alias, got %q", resp.Messages[0].Status)
		}
	})
}

func TestResetRetries(t *testing.T) {
	t.Run("no tunnels", func(t *testing.T) {
		d := &Daemon{
			tunnels: map[string]Tunnel{},
		}

		resp := d.resetRetries()
		if resp.Messages[0].Status != "WARN" {
			t.Errorf("expected WARN, got %q", resp.Messages[0].Status)
		}
	})

	t.Run("tunnels with retries", func(t *testing.T) {
		quietLogger(t)
		nextRetry := time.Now().Add(time.Minute)
		d := &Daemon{
			tunnels: map[string]Tunnel{
				"server1": {
					RetryCount:      5,
					TotalReconnects: 10,
					NextRetryTime:   nextRetry,
					State:           StateReconnecting,
				},
				"server2": {
					RetryCount: 0,
					State:      StateConnected,
				},
			},
		}

		resp := d.resetRetries()
		if resp.Messages[0].Status != "INFO" {
			t.Errorf("expected INFO, got %q", resp.Messages[0].Status)
		}

		// server1 should be reset
		if d.tunnels["server1"].RetryCount != 0 {
			t.Errorf("expected RetryCount=0, got %d", d.tunnels["server1"].RetryCount)
		}
		if d.tunnels["server1"].TotalReconnects != 0 {
			t.Errorf("expected TotalReconnects=0, got %d", d.tunnels["server1"].TotalReconnects)
		}
		if !d.tunnels["server1"].NextRetryTime.IsZero() {
			t.Error("expected NextRetryTime to be zero")
		}
	})

	t.Run("tunnels without retries", func(t *testing.T) {
		d := &Daemon{
			tunnels: map[string]Tunnel{
				"server1": {
					RetryCount: 0,
					State:      StateConnected,
				},
			},
		}

		resp := d.resetRetries()
		if !strings.Contains(resp.Messages[0].Message, "No tunnels needed resetting") {
			t.Errorf("expected 'No tunnels needed resetting' message, got %q", resp.Messages[0].Message)
		}
	})
}

func TestHandleCompanionInit(t *testing.T) {
	oldConfig := core.Config
	defer func() { core.Config = oldConfig }()

	t.Run("invalid token", func(t *testing.T) {
		core.Config = &core.Configuration{}
		d := &Daemon{
			askpassTokens: map[string]string{},
		}

		resp := d.handleCompanionInit("server1", "comp1", "bad-token")
		if resp.Messages[0].Status != "ERROR" {
			t.Errorf("expected ERROR, got %q", resp.Messages[0].Status)
		}
	})

	t.Run("valid token, companion found", func(t *testing.T) {
		core.Config = &core.Configuration{
			Tunnels: map[string]*core.TunnelConfig{
				"server1": {
					Companions: []core.CompanionConfig{
						{Name: "comp1", Command: "echo hello"},
					},
				},
			},
		}

		d := &Daemon{
			askpassTokens: map[string]string{
				"valid-token": "server1",
			},
		}

		resp := d.handleCompanionInit("server1", "comp1", "valid-token")
		if resp.Messages[0].Status != "INFO" {
			t.Errorf("expected INFO, got %q", resp.Messages[0].Status)
		}
		if resp.Messages[0].Message != "echo hello" {
			t.Errorf("expected command 'echo hello', got %q", resp.Messages[0].Message)
		}
	})

	t.Run("valid token, tunnel not in config", func(t *testing.T) {
		core.Config = &core.Configuration{
			Tunnels: map[string]*core.TunnelConfig{},
		}

		d := &Daemon{
			askpassTokens: map[string]string{
				"valid-token": "server1",
			},
		}

		resp := d.handleCompanionInit("server1", "comp1", "valid-token")
		if resp.Messages[0].Status != "ERROR" {
			t.Errorf("expected ERROR, got %q", resp.Messages[0].Status)
		}
	})

	t.Run("valid token, companion not found", func(t *testing.T) {
		core.Config = &core.Configuration{
			Tunnels: map[string]*core.TunnelConfig{
				"server1": {
					Companions: []core.CompanionConfig{
						{Name: "other_comp", Command: "echo other"},
					},
				},
			},
		}

		d := &Daemon{
			askpassTokens: map[string]string{
				"valid-token": "server1",
			},
		}

		resp := d.handleCompanionInit("server1", "comp1", "valid-token")
		if resp.Messages[0].Status != "ERROR" {
			t.Errorf("expected ERROR, got %q", resp.Messages[0].Status)
		}
	})
}

func TestStopDaemon(t *testing.T) {
	t.Run("with tunnels", func(t *testing.T) {
		d := &Daemon{
			tunnels: map[string]Tunnel{
				"server1": {State: StateConnected},
				"server2": {State: StateConnected},
			},
		}

		resp := d.stopDaemon()
		if !strings.Contains(resp.Messages[0].Message, "2 active tunnel") {
			t.Errorf("expected message about 2 tunnels, got %q", resp.Messages[0].Message)
		}
	})

	t.Run("without tunnels", func(t *testing.T) {
		d := &Daemon{
			tunnels: map[string]Tunnel{},
		}

		resp := d.stopDaemon()
		if !strings.Contains(resp.Messages[0].Message, "Stopping daemon...") {
			t.Errorf("expected 'Stopping daemon...' message, got %q", resp.Messages[0].Message)
		}
	})
}
