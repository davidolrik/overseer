package daemon

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"go.olrik.dev/overseer/internal/core"
	"go.olrik.dev/overseer/internal/db"
)

func TestAdoptTunnel_ValidProcessWithDatabase(t *testing.T) {
	quietLogger(t)

	tmpDir := t.TempDir()
	database, err := db.Open(filepath.Join(tmpDir, "test.db"))
	if err != nil {
		t.Fatalf("failed to open database: %v", err)
	}
	t.Cleanup(func() { database.Close() })

	oldConfig := core.Config
	t.Cleanup(func() { core.Config = oldConfig })
	core.Config = &core.Configuration{
		Companion: core.CompanionSettings{HistorySize: 50},
	}

	d := New()
	d.ctx, d.cancelFunc = context.WithCancel(context.Background())
	t.Cleanup(d.cancelFunc)
	d.database = database

	// Use our own PID - it's a valid process
	// ValidateTunnelProcess will check if it looks like an SSH process,
	// which will fail for our test process, but exercises the validation path
	info := TunnelInfo{
		Alias:     "test-tunnel",
		PID:       os.Getpid(),
		Hostname:  "test.example.com",
		StartDate: time.Now(),
	}

	_ = d.adoptTunnel(info)
}

func TestAdoptTunnel_WithAllFields(t *testing.T) {
	quietLogger(t)

	oldConfig := core.Config
	t.Cleanup(func() { core.Config = oldConfig })
	core.Config = &core.Configuration{
		Companion: core.CompanionSettings{HistorySize: 50},
	}

	d := New()
	d.ctx, d.cancelFunc = context.WithCancel(context.Background())
	t.Cleanup(d.cancelFunc)

	// Test with all fields populated
	info := TunnelInfo{
		Alias:             "full-tunnel",
		PID:               999999999, // Dead PID - will fail validation
		Hostname:          "test.example.com",
		StartDate:         time.Now().Add(-1 * time.Hour),
		LastConnectedTime: time.Now(),
		RetryCount:        3,
		TotalReconnects:   10,
		AutoReconnect:     true,
		State:             string(StateConnected),
		Tag:               "production",
		ResolvedHost:      "10.0.0.1",
		JumpChain: []string{"jump1.example.com:22"},
	}

	// Will fail validation (dead PID) but exercises field mapping
	result := d.adoptTunnel(info)
	if result {
		t.Error("expected false for dead PID")
	}
}
