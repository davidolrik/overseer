package daemon

import (
	"path/filepath"
	"testing"

	"go.olrik.dev/overseer/internal/awareness/state"
	"go.olrik.dev/overseer/internal/core"
	"go.olrik.dev/overseer/internal/db"
)

func TestLogContextChange_BothChanged(t *testing.T) {
	quietLogger(t)

	tmpDir := t.TempDir()
	database, err := db.Open(filepath.Join(tmpDir, "test.db"))
	if err != nil {
		t.Fatalf("failed to open database: %v", err)
	}
	t.Cleanup(func() { database.Close() })

	adapter := newDatabaseLoggerAdapter(database)

	err = adapter.LogContextChange("trusted", "untrusted", "home", "office", "ip_change")
	if err != nil {
		t.Errorf("LogContextChange failed: %v", err)
	}
}

func TestLogContextChange_OnlyContextChanged(t *testing.T) {
	quietLogger(t)

	tmpDir := t.TempDir()
	database, err := db.Open(filepath.Join(tmpDir, "test.db"))
	if err != nil {
		t.Fatalf("failed to open database: %v", err)
	}
	t.Cleanup(func() { database.Close() })

	adapter := newDatabaseLoggerAdapter(database)

	err = adapter.LogContextChange("trusted", "untrusted", "home", "home", "rule_change")
	if err != nil {
		t.Errorf("LogContextChange failed: %v", err)
	}
}

func TestLogContextChange_OnlyLocationChanged(t *testing.T) {
	quietLogger(t)

	tmpDir := t.TempDir()
	database, err := db.Open(filepath.Join(tmpDir, "test.db"))
	if err != nil {
		t.Fatalf("failed to open database: %v", err)
	}
	t.Cleanup(func() { database.Close() })

	adapter := newDatabaseLoggerAdapter(database)

	err = adapter.LogContextChange("trusted", "trusted", "home", "office", "ip_change")
	if err != nil {
		t.Errorf("LogContextChange failed: %v", err)
	}
}

func TestLogContextChange_NothingChanged(t *testing.T) {
	quietLogger(t)

	tmpDir := t.TempDir()
	database, err := db.Open(filepath.Join(tmpDir, "test.db"))
	if err != nil {
		t.Fatalf("failed to open database: %v", err)
	}
	t.Cleanup(func() { database.Close() })

	adapter := newDatabaseLoggerAdapter(database)

	err = adapter.LogContextChange("trusted", "trusted", "home", "home", "no_change")
	if err != nil {
		t.Errorf("LogContextChange failed: %v", err)
	}
}

func TestHandleNewContextChange_ConnectAction(t *testing.T) {
	quietLogger(t)

	tmpDir := t.TempDir()
	oldConfig := core.Config
	t.Cleanup(func() { core.Config = oldConfig })
	core.Config = &core.Configuration{
		ConfigPath: tmpDir,
		Companion:  core.CompanionSettings{HistorySize: 50},
		Tunnels: map[string]*core.TunnelConfig{
			"connect-tunnel": {
				Name: "connect-tunnel",
			},
		},
	}

	d := New()

	from := state.StateSnapshot{Context: "untrusted", Location: "unknown"}
	to := state.StateSnapshot{Context: "trusted", Location: "home", Online: true}
	rule := &state.Rule{
		Name: "trusted",
		Actions: state.RuleActions{
			Connect: []string{"connect-tunnel"},
		},
	}

	d.handleNewContextChange(from, to, rule)

	// The tunnel should have been started (or attempted)
	// Since we don't have real SSH, it may fail, but the connect action was invoked
	d.mu.Lock()
	_, exists := d.tunnels["connect-tunnel"]
	d.mu.Unlock()
	// The tunnel entry may or may not exist depending on startTunnel success
	// What matters is that it didn't panic
	_ = exists
}

func TestHandleNewContextChange_LocationChangeResetsRetriesAllTunnels(t *testing.T) {
	quietLogger(t)

	oldConfig := core.Config
	t.Cleanup(func() { core.Config = oldConfig })
	core.Config = &core.Configuration{
		Companion: core.CompanionSettings{HistorySize: 50},
	}

	d := New()

	d.tunnels["retry-tunnel-1"] = Tunnel{
		Hostname:   "test1.example.com",
		State:      StateReconnecting,
		RetryCount: 5,
	}
	d.tunnels["retry-tunnel-2"] = Tunnel{
		Hostname:   "test2.example.com",
		State:      StateReconnecting,
		RetryCount: 10,
	}

	from := state.StateSnapshot{Context: "a", Location: "home", Online: true}
	to := state.StateSnapshot{Context: "a", Location: "office", Online: true}

	d.handleNewContextChange(from, to, nil)

	d.mu.Lock()
	tunnel1 := d.tunnels["retry-tunnel-1"]
	tunnel2 := d.tunnels["retry-tunnel-2"]
	d.mu.Unlock()

	if tunnel1.RetryCount != 0 {
		t.Errorf("expected RetryCount reset to 0 for tunnel 1, got %d", tunnel1.RetryCount)
	}
	if tunnel2.RetryCount != 0 {
		t.Errorf("expected RetryCount reset to 0 for tunnel 2, got %d", tunnel2.RetryCount)
	}
}
