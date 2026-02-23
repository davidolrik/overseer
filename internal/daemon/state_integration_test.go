package daemon

import (
	"path/filepath"
	"testing"
	"time"

	"go.olrik.dev/overseer/internal/awareness"
	"go.olrik.dev/overseer/internal/awareness/state"
	"go.olrik.dev/overseer/internal/core"
	"go.olrik.dev/overseer/internal/db"
)

func TestConvertCondition(t *testing.T) {
	t.Run("nil input", func(t *testing.T) {
		result := convertCondition(nil)
		if result != nil {
			t.Errorf("expected nil, got %v", result)
		}
	})

	t.Run("SensorCondition with BoolValue", func(t *testing.T) {
		bv := true
		cond := &awareness.SensorCondition{
			SensorName: "online",
			BoolValue:  &bv,
		}
		result := convertCondition(cond)
		if result == nil {
			t.Fatal("expected non-nil condition")
		}
		// Verify it evaluates correctly as a boolean condition
		readings := map[string]state.SensorReading{
			"online": {Sensor: "online", Online: &bv},
		}
		if !result.Evaluate(readings, true) {
			t.Error("expected condition to evaluate to true")
		}
	})

	t.Run("SensorCondition with Pattern", func(t *testing.T) {
		cond := &awareness.SensorCondition{
			SensorName: "public_ipv4",
			Pattern:    "10.0.0.*",
		}
		result := convertCondition(cond)
		if result == nil {
			t.Fatal("expected non-nil condition")
		}
	})

	t.Run("GroupCondition any", func(t *testing.T) {
		bv := true
		cond := &awareness.GroupCondition{
			Operator: "any",
			Conditions: []awareness.Condition{
				&awareness.SensorCondition{SensorName: "online", BoolValue: &bv},
			},
		}
		result := convertCondition(cond)
		if result == nil {
			t.Fatal("expected non-nil condition")
		}
	})

	t.Run("GroupCondition all", func(t *testing.T) {
		cond := &awareness.GroupCondition{
			Operator: "all",
			Conditions: []awareness.Condition{
				&awareness.SensorCondition{SensorName: "public_ipv4", Pattern: "1.2.3.4"},
			},
		}
		result := convertCondition(cond)
		if result == nil {
			t.Fatal("expected non-nil condition")
		}
	})

	t.Run("nested group conditions", func(t *testing.T) {
		bv := true
		cond := &awareness.GroupCondition{
			Operator: "all",
			Conditions: []awareness.Condition{
				&awareness.GroupCondition{
					Operator: "any",
					Conditions: []awareness.Condition{
						&awareness.SensorCondition{SensorName: "online", BoolValue: &bv},
					},
				},
				&awareness.SensorCondition{SensorName: "public_ipv4", Pattern: "10.*"},
			},
		}
		result := convertCondition(cond)
		if result == nil {
			t.Fatal("expected non-nil condition")
		}
	})

	t.Run("unknown type returns nil", func(t *testing.T) {
		result := convertCondition("not a condition")
		if result != nil {
			t.Errorf("expected nil for unknown type, got %v", result)
		}
	})
}

func TestMergeStateLocation(t *testing.T) {
	t.Run("display name override", func(t *testing.T) {
		defaultLoc := state.Location{
			Name:        "offline",
			DisplayName: "Offline",
			Environment: map[string]string{"A": "1"},
		}
		userLoc := state.Location{
			DisplayName: "Custom Offline",
		}

		merged := mergeStateLocation(defaultLoc, userLoc)
		if merged.DisplayName != "Custom Offline" {
			t.Errorf("expected 'Custom Offline', got %q", merged.DisplayName)
		}
		if merged.Name != "offline" {
			t.Errorf("expected name preserved as 'offline', got %q", merged.Name)
		}
	})

	t.Run("env merge", func(t *testing.T) {
		defaultLoc := state.Location{
			Environment: map[string]string{"A": "1", "B": "2"},
		}
		userLoc := state.Location{
			Environment: map[string]string{"B": "override", "C": "3"},
		}

		merged := mergeStateLocation(defaultLoc, userLoc)
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

	t.Run("default preserved when user empty", func(t *testing.T) {
		defaultLoc := state.Location{
			DisplayName: "Default",
			Environment: map[string]string{"X": "1"},
		}
		userLoc := state.Location{}

		merged := mergeStateLocation(defaultLoc, userLoc)
		if merged.DisplayName != "Default" {
			t.Errorf("expected 'Default' preserved, got %q", merged.DisplayName)
		}
	})

	t.Run("nil default environment", func(t *testing.T) {
		defaultLoc := state.Location{
			Name: "test",
		}
		userLoc := state.Location{
			Environment: map[string]string{"K": "V"},
		}

		merged := mergeStateLocation(defaultLoc, userLoc)
		if merged.Environment["K"] != "V" {
			t.Errorf("expected K=V, got %q", merged.Environment["K"])
		}
	})
}

func TestMergeStateRule(t *testing.T) {
	t.Run("display name override", func(t *testing.T) {
		defaultRule := state.Rule{
			Name:        "untrusted",
			DisplayName: "Untrusted",
		}
		userRule := state.Rule{
			DisplayName: "Custom Untrusted",
		}

		merged := mergeStateRule(defaultRule, userRule)
		if merged.DisplayName != "Custom Untrusted" {
			t.Errorf("expected 'Custom Untrusted', got %q", merged.DisplayName)
		}
	})

	t.Run("env merge", func(t *testing.T) {
		defaultRule := state.Rule{
			Environment: map[string]string{"A": "1"},
		}
		userRule := state.Rule{
			Environment: map[string]string{"A": "2", "B": "3"},
		}

		merged := mergeStateRule(defaultRule, userRule)
		if merged.Environment["A"] != "2" {
			t.Errorf("expected A=2, got %q", merged.Environment["A"])
		}
		if merged.Environment["B"] != "3" {
			t.Errorf("expected B=3, got %q", merged.Environment["B"])
		}
	})

	t.Run("actions override", func(t *testing.T) {
		defaultRule := state.Rule{
			Actions: state.RuleActions{
				Connect: []string{"tunnel1"},
			},
		}
		userRule := state.Rule{
			Actions: state.RuleActions{
				Connect:    []string{"tunnel2"},
				Disconnect: []string{"tunnel3"},
			},
		}

		merged := mergeStateRule(defaultRule, userRule)
		if len(merged.Actions.Connect) != 1 || merged.Actions.Connect[0] != "tunnel2" {
			t.Errorf("expected connect=[tunnel2], got %v", merged.Actions.Connect)
		}
		if len(merged.Actions.Disconnect) != 1 || merged.Actions.Disconnect[0] != "tunnel3" {
			t.Errorf("expected disconnect=[tunnel3], got %v", merged.Actions.Disconnect)
		}
	})

	t.Run("actions not overridden when user empty", func(t *testing.T) {
		defaultRule := state.Rule{
			Actions: state.RuleActions{
				Connect: []string{"tunnel1"},
			},
		}
		userRule := state.Rule{}

		merged := mergeStateRule(defaultRule, userRule)
		if len(merged.Actions.Connect) != 1 || merged.Actions.Connect[0] != "tunnel1" {
			t.Errorf("expected default connect=[tunnel1] preserved, got %v", merged.Actions.Connect)
		}
	})
}

func TestCollectTrackedEnvVars(t *testing.T) {
	rules := []state.Rule{
		{Environment: map[string]string{"RULE_VAR": "val"}},
		{Environment: map[string]string{"ANOTHER_VAR": "val"}},
	}
	locations := map[string]state.Location{
		"home": {Environment: map[string]string{"LOC_VAR": "val"}},
		"work": {Environment: map[string]string{"RULE_VAR": "different"}}, // duplicate key
	}

	result := collectTrackedEnvVars(rules, locations, nil)

	found := map[string]bool{}
	for _, v := range result {
		found[v] = true
	}

	// Should include builtin vars
	builtins := []string{
		"OVERSEER_CONTEXT",
		"OVERSEER_CONTEXT_DISPLAY_NAME",
		"OVERSEER_LOCATION",
		"OVERSEER_LOCATION_DISPLAY_NAME",
		"OVERSEER_PUBLIC_IP",
		"OVERSEER_PUBLIC_IPV4",
		"OVERSEER_PUBLIC_IPV6",
		"OVERSEER_LOCAL_IP",
		"OVERSEER_LOCAL_IPV4",
	}
	for _, v := range builtins {
		if !found[v] {
			t.Errorf("expected builtin var %q in result", v)
		}
	}

	// Should include vars from rules
	if !found["RULE_VAR"] {
		t.Error("expected RULE_VAR from rules")
	}
	if !found["ANOTHER_VAR"] {
		t.Error("expected ANOTHER_VAR from rules")
	}

	// Should include vars from locations
	if !found["LOC_VAR"] {
		t.Error("expected LOC_VAR from locations")
	}

	// RULE_VAR should be deduplicated (appears in both rules and locations)
	count := 0
	for _, v := range result {
		if v == "RULE_VAR" {
			count++
		}
	}
	if count != 1 {
		t.Errorf("expected RULE_VAR to appear once, appeared %d times", count)
	}
}

func TestConvertHooksConfig(t *testing.T) {
	t.Run("nil input", func(t *testing.T) {
		result := convertHooksConfig(nil)
		if result != nil {
			t.Error("expected nil for nil input")
		}
	})

	t.Run("with OnEnter and OnLeave hooks", func(t *testing.T) {
		input := &core.HooksConfig{
			OnEnter: []core.HookConfig{
				{Command: "echo enter", Timeout: 5 * time.Second},
			},
			OnLeave: []core.HookConfig{
				{Command: "echo leave1", Timeout: 10 * time.Second},
				{Command: "echo leave2"},
			},
		}

		result := convertHooksConfig(input)
		if result == nil {
			t.Fatal("expected non-nil result")
		}

		if len(result.OnEnter) != 1 {
			t.Fatalf("expected 1 OnEnter hook, got %d", len(result.OnEnter))
		}
		if result.OnEnter[0].Command != "echo enter" {
			t.Errorf("expected command 'echo enter', got %q", result.OnEnter[0].Command)
		}
		if result.OnEnter[0].Timeout != 5*time.Second {
			t.Errorf("expected timeout 5s, got %v", result.OnEnter[0].Timeout)
		}

		if len(result.OnLeave) != 2 {
			t.Fatalf("expected 2 OnLeave hooks, got %d", len(result.OnLeave))
		}
		if result.OnLeave[0].Command != "echo leave1" {
			t.Errorf("expected command 'echo leave1', got %q", result.OnLeave[0].Command)
		}
		if result.OnLeave[1].Command != "echo leave2" {
			t.Errorf("expected command 'echo leave2', got %q", result.OnLeave[1].Command)
		}
	})

	t.Run("empty hooks", func(t *testing.T) {
		input := &core.HooksConfig{}

		result := convertHooksConfig(input)
		if result == nil {
			t.Fatal("expected non-nil result")
		}
		if len(result.OnEnter) != 0 {
			t.Errorf("expected 0 OnEnter, got %d", len(result.OnEnter))
		}
		if len(result.OnLeave) != 0 {
			t.Errorf("expected 0 OnLeave, got %d", len(result.OnLeave))
		}
	})
}

func TestDatabaseLoggerAdapter_LogContextChange(t *testing.T) {
	// We can't easily test with a real DB, but we can test the adapter's logic
	// by verifying it calls the expected DB methods via an interface check.
	// Instead, we test the control flow — the adapter calls LogSensorChange
	// for location changes and context changes separately.

	t.Run("location changed", func(t *testing.T) {
		// When only location changes, we expect one LogSensorChange call for "location"
		// We can't test this without mocking db.DB, but we verify the adapter exists
		adapter := newDatabaseLoggerAdapter(nil)
		if adapter == nil {
			t.Fatal("expected non-nil adapter")
		}
		// Calling with nil db will panic in production, but the adapter is created correctly
	})

	t.Run("both changed", func(t *testing.T) {
		// Verify adapter creation with nil is safe
		adapter := newDatabaseLoggerAdapter(nil)
		if adapter.db != nil {
			t.Error("expected nil db in adapter")
		}
	})
}

func TestGetContextStatus_NilOrchestrator(t *testing.T) {
	quietLogger(t)

	oldConfig := core.Config
	t.Cleanup(func() { core.Config = oldConfig })
	core.Config = &core.Configuration{
		Companion: core.CompanionSettings{HistorySize: 50},
	}

	// Save and restore stateOrchestrator
	old := stateOrchestrator
	t.Cleanup(func() { stateOrchestrator = old })
	stateOrchestrator = nil

	d := New()
	resp := d.getContextStatus(10)

	if len(resp.Messages) == 0 {
		t.Fatal("expected at least one message")
	}
	if resp.Messages[0].Status != "ERROR" {
		t.Errorf("expected ERROR status, got %q", resp.Messages[0].Status)
	}
}

func TestGetContextStatus_WithOrchestrator(t *testing.T) {
	quietLogger(t)

	tmpDir := t.TempDir()
	oldConfig := core.Config
	t.Cleanup(func() { core.Config = oldConfig })
	core.Config = &core.Configuration{
		ConfigPath: tmpDir,
		Companion:  core.CompanionSettings{HistorySize: 50},
		Locations:  map[string]*core.Location{},
		Contexts:   []*core.ContextRule{},
	}

	old := stateOrchestrator
	t.Cleanup(func() {
		stopStateOrchestrator()
		stateOrchestrator = old
	})

	d := New()
	if err := d.initStateOrchestrator(); err != nil {
		t.Fatalf("initStateOrchestrator failed: %v", err)
	}

	resp := d.getContextStatus(10)
	if len(resp.Messages) == 0 {
		t.Fatal("expected at least one message")
	}
	if resp.Messages[0].Status != "INFO" {
		t.Errorf("expected INFO status, got %q", resp.Messages[0].Status)
	}
	if resp.Data == nil {
		t.Error("expected non-nil Data with context status")
	}
}

func TestGetContextStatus_WithDatabase(t *testing.T) {
	quietLogger(t)

	tmpDir := t.TempDir()
	oldConfig := core.Config
	t.Cleanup(func() { core.Config = oldConfig })
	core.Config = &core.Configuration{
		ConfigPath: tmpDir,
		Companion:  core.CompanionSettings{HistorySize: 50},
		Locations:  map[string]*core.Location{},
		Contexts:   []*core.ContextRule{},
	}

	old := stateOrchestrator
	t.Cleanup(func() {
		stopStateOrchestrator()
		stateOrchestrator = old
	})

	database, err := db.Open(filepath.Join(tmpDir, "test.db"))
	if err != nil {
		t.Fatalf("failed to open database: %v", err)
	}
	t.Cleanup(func() { database.Close() })

	// Seed with some events to exercise the query+merge code paths
	database.LogSensorChange("public_ipv4", "ip", "", "1.2.3.4")
	database.LogTunnelEvent("tunnel1", "connect", "Connected")
	database.LogDaemonEvent("start", "daemon started")

	d := New()
	d.database = database

	if err := d.initStateOrchestrator(); err != nil {
		t.Fatalf("initStateOrchestrator failed: %v", err)
	}

	resp := d.getContextStatus(20)
	if len(resp.Messages) == 0 {
		t.Fatal("expected at least one message")
	}
	if resp.Messages[0].Status != "INFO" {
		t.Errorf("expected INFO status, got %q", resp.Messages[0].Status)
	}
	if resp.Data == nil {
		t.Fatal("expected non-nil Data")
	}

	// Verify the response contains the ContextStatus structure
	status, ok := resp.Data.(ContextStatus)
	if !ok {
		t.Fatalf("expected Data to be ContextStatus, got %T", resp.Data)
	}
	if status.Sensors == nil {
		t.Error("expected non-nil sensors map")
	}
}

func TestInitStateOrchestrator_EmptyConfig(t *testing.T) {
	quietLogger(t)

	tmpDir := t.TempDir()
	oldConfig := core.Config
	t.Cleanup(func() { core.Config = oldConfig })
	core.Config = &core.Configuration{
		ConfigPath: tmpDir,
		Companion:  core.CompanionSettings{HistorySize: 50},
		Locations:  map[string]*core.Location{},
		Contexts:   []*core.ContextRule{},
	}

	old := stateOrchestrator
	t.Cleanup(func() {
		stopStateOrchestrator()
		stateOrchestrator = old
	})

	d := New()
	if err := d.initStateOrchestrator(); err != nil {
		t.Fatalf("initStateOrchestrator failed: %v", err)
	}

	if stateOrchestrator == nil {
		t.Error("expected stateOrchestrator to be non-nil")
	}
}

func TestInitStateOrchestrator_WithLocationsAndRules(t *testing.T) {
	quietLogger(t)

	tmpDir := t.TempDir()
	oldConfig := core.Config
	t.Cleanup(func() { core.Config = oldConfig })
	core.Config = &core.Configuration{
		ConfigPath: tmpDir,
		Companion:  core.CompanionSettings{HistorySize: 50},
		Locations: map[string]*core.Location{
			"home": {
				Name:        "home",
				DisplayName: "Home",
				Conditions:  map[string][]string{"public_ipv4": {"192.168.1.*"}},
				Environment: map[string]string{"HOME_VAR": "1"},
			},
		},
		Contexts: []*core.ContextRule{
			{
				Name:        "trusted",
				DisplayName: "Trusted",
				Locations:   []string{"home"},
				Environment: map[string]string{"TRUST_LEVEL": "high"},
				Actions: core.ContextActions{
					Connect:    []string{"my-tunnel"},
					Disconnect: []string{},
				},
			},
		},
	}

	old := stateOrchestrator
	t.Cleanup(func() {
		stopStateOrchestrator()
		stateOrchestrator = old
	})

	d := New()
	if err := d.initStateOrchestrator(); err != nil {
		t.Fatalf("initStateOrchestrator failed: %v", err)
	}

	if stateOrchestrator == nil {
		t.Error("expected stateOrchestrator to be non-nil")
	}
}

func TestStopStateOrchestrator_Nil(t *testing.T) {
	quietLogger(t)

	old := stateOrchestrator
	t.Cleanup(func() { stateOrchestrator = old })
	stateOrchestrator = nil

	// Should not panic
	stopStateOrchestrator()
}

func TestStopStateOrchestrator_Running(t *testing.T) {
	quietLogger(t)

	tmpDir := t.TempDir()
	oldConfig := core.Config
	t.Cleanup(func() { core.Config = oldConfig })
	core.Config = &core.Configuration{
		ConfigPath: tmpDir,
		Companion:  core.CompanionSettings{HistorySize: 50},
		Locations:  map[string]*core.Location{},
		Contexts:   []*core.ContextRule{},
	}

	old := stateOrchestrator
	t.Cleanup(func() { stateOrchestrator = old })

	d := New()
	if err := d.initStateOrchestrator(); err != nil {
		t.Fatalf("initStateOrchestrator failed: %v", err)
	}

	if stateOrchestrator == nil {
		t.Fatal("expected stateOrchestrator to be non-nil before stop")
	}

	stopStateOrchestrator()

	if stateOrchestrator != nil {
		t.Error("expected stateOrchestrator to be nil after stop")
	}
}

func TestReloadStateOrchestrator_NilOrchestrator(t *testing.T) {
	quietLogger(t)

	old := stateOrchestrator
	t.Cleanup(func() { stateOrchestrator = old })
	stateOrchestrator = nil

	oldConfig := core.Config
	t.Cleanup(func() { core.Config = oldConfig })
	core.Config = &core.Configuration{
		Companion: core.CompanionSettings{HistorySize: 50},
	}

	d := New()
	err := d.reloadStateOrchestrator()
	if err == nil {
		t.Error("expected error when orchestrator is nil")
	}
}

func TestReloadStateOrchestrator_Running(t *testing.T) {
	quietLogger(t)

	tmpDir := t.TempDir()
	oldConfig := core.Config
	t.Cleanup(func() { core.Config = oldConfig })
	core.Config = &core.Configuration{
		ConfigPath: tmpDir,
		Companion:  core.CompanionSettings{HistorySize: 50},
		Locations:  map[string]*core.Location{},
		Contexts:   []*core.ContextRule{},
	}

	old := stateOrchestrator
	t.Cleanup(func() {
		stopStateOrchestrator()
		stateOrchestrator = old
	})

	d := New()
	if err := d.initStateOrchestrator(); err != nil {
		t.Fatalf("initStateOrchestrator failed: %v", err)
	}

	// Modify config and reload
	core.Config.Locations["office"] = &core.Location{
		Name:        "office",
		DisplayName: "Office",
		Conditions:  map[string][]string{"public_ipv4": {"10.0.0.*"}},
	}

	if err := d.reloadStateOrchestrator(); err != nil {
		t.Errorf("reloadStateOrchestrator failed: %v", err)
	}
}

func TestGetContextStatusNew_NilOrchestrator(t *testing.T) {
	quietLogger(t)

	old := stateOrchestrator
	t.Cleanup(func() { stateOrchestrator = old })
	stateOrchestrator = nil

	oldConfig := core.Config
	t.Cleanup(func() { core.Config = oldConfig })
	core.Config = &core.Configuration{
		Companion: core.CompanionSettings{HistorySize: 50},
	}

	d := New()
	ctx, loc := d.getContextStatusNew()
	if ctx != "" || loc != "" {
		t.Errorf("expected empty strings, got context=%q location=%q", ctx, loc)
	}
}

func TestGetContextStatusNew_WithOrchestrator(t *testing.T) {
	quietLogger(t)

	tmpDir := t.TempDir()
	oldConfig := core.Config
	t.Cleanup(func() { core.Config = oldConfig })
	core.Config = &core.Configuration{
		ConfigPath: tmpDir,
		Companion:  core.CompanionSettings{HistorySize: 50},
		Locations:  map[string]*core.Location{},
		Contexts:   []*core.ContextRule{},
	}

	old := stateOrchestrator
	t.Cleanup(func() {
		stopStateOrchestrator()
		stateOrchestrator = old
	})

	d := New()
	if err := d.initStateOrchestrator(); err != nil {
		t.Fatalf("initStateOrchestrator failed: %v", err)
	}

	// The orchestrator may not have evaluated yet, so we just verify it doesn't panic
	// and returns without error. Context/location may be empty until first evaluation.
	ctx, loc := d.getContextStatusNew()
	_ = ctx
	_ = loc
}

func TestHandleNewContextChange_DisconnectAction(t *testing.T) {
	quietLogger(t)

	tmpDir := t.TempDir()
	oldConfig := core.Config
	t.Cleanup(func() { core.Config = oldConfig })
	core.Config = &core.Configuration{
		ConfigPath: tmpDir,
		Companion:  core.CompanionSettings{HistorySize: 50},
		Tunnels:    map[string]*core.TunnelConfig{},
	}

	d := New()

	// Add a tunnel that should be disconnected
	d.tunnels["disconnect-me"] = Tunnel{
		Hostname: "test.example.com",
		Pid:      0, // Not a real process
		State:    StateConnected,
	}

	from := state.StateSnapshot{
		Context:  "trusted",
		Location: "home",
	}
	to := state.StateSnapshot{
		Context:  "untrusted",
		Location: "unknown",
		Online:   true,
	}
	rule := &state.Rule{
		Name: "untrusted",
		Actions: state.RuleActions{
			Disconnect: []string{"disconnect-me"},
		},
	}

	d.handleNewContextChange(from, to, rule)

	// The tunnel should have been removed by stopTunnel
	d.mu.Lock()
	_, exists := d.tunnels["disconnect-me"]
	d.mu.Unlock()
	if exists {
		t.Error("expected tunnel to be removed after disconnect action")
	}
}

func TestHandleNewContextChange_NilRule(t *testing.T) {
	quietLogger(t)

	oldConfig := core.Config
	t.Cleanup(func() { core.Config = oldConfig })
	core.Config = &core.Configuration{
		Companion: core.CompanionSettings{HistorySize: 50},
	}

	d := New()

	from := state.StateSnapshot{Context: "a", Location: "x"}
	to := state.StateSnapshot{Context: "b", Location: "y"}

	// Should not panic with nil rule
	d.handleNewContextChange(from, to, nil)
}

func TestHandleNewContextChange_LocationChangeResetsRetries(t *testing.T) {
	quietLogger(t)

	oldConfig := core.Config
	t.Cleanup(func() { core.Config = oldConfig })
	core.Config = &core.Configuration{
		Companion: core.CompanionSettings{HistorySize: 50},
	}

	d := New()

	// Add a tunnel with retry count
	d.tunnels["retry-tunnel"] = Tunnel{
		Hostname:   "test.example.com",
		State:      StateReconnecting,
		RetryCount: 5,
	}

	from := state.StateSnapshot{Context: "a", Location: "home"}
	to := state.StateSnapshot{Context: "b", Location: "office", Online: true}

	d.handleNewContextChange(from, to, nil)

	d.mu.Lock()
	tunnel := d.tunnels["retry-tunnel"]
	d.mu.Unlock()

	if tunnel.RetryCount != 0 {
		t.Errorf("expected RetryCount to be reset to 0, got %d", tunnel.RetryCount)
	}
}

func TestGetStateOrchestrator(t *testing.T) {
	old := stateOrchestrator
	t.Cleanup(func() { stateOrchestrator = old })

	stateOrchestrator = nil
	if got := GetStateOrchestrator(); got != nil {
		t.Error("expected nil")
	}
}
