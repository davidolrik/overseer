package db

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestDB_OpenAndClose(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test.db")

	db, err := Open(dbPath)
	if err != nil {
		t.Fatalf("Failed to open database: %v", err)
	}
	defer db.Close()

	// Verify database file was created
	if _, err := os.Stat(dbPath); os.IsNotExist(err) {
		t.Error("Database file was not created")
	}

	// Verify we can close without error
	if err := db.Close(); err != nil {
		t.Errorf("Failed to close database: %v", err)
	}
}

func TestDB_LogSensorChange(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test.db")

	db, err := Open(dbPath)
	if err != nil {
		t.Fatalf("Failed to open database: %v", err)
	}
	defer db.Close()

	// Log a sensor change
	err = db.LogSensorChange("public_ip", "string", "192.168.1.100", "10.0.0.1")
	if err != nil {
		t.Errorf("Failed to log sensor change: %v", err)
	}

	// Query the change back
	rows, err := db.conn.Query(`
		SELECT sensor_name, sensor_type, old_value, new_value
		FROM sensor_changes
		ORDER BY timestamp DESC
		LIMIT 1
	`)
	if err != nil {
		t.Fatalf("Failed to query sensor changes: %v", err)
	}
	defer rows.Close()

	if !rows.Next() {
		t.Fatal("Expected at least one sensor change record")
	}

	var sensorName, sensorType, oldValue, newValue string
	if err := rows.Scan(&sensorName, &sensorType, &oldValue, &newValue); err != nil {
		t.Fatalf("Failed to scan row: %v", err)
	}

	if sensorName != "public_ip" {
		t.Errorf("Expected sensor_name='public_ip', got '%v'", sensorName)
	}
	if sensorType != "string" {
		t.Errorf("Expected sensor_type='string', got '%v'", sensorType)
	}
	if oldValue != "192.168.1.100" {
		t.Errorf("Expected old_value='192.168.1.100', got '%v'", oldValue)
	}
	if newValue != "10.0.0.1" {
		t.Errorf("Expected new_value='10.0.0.1', got '%v'", newValue)
	}
}

func TestDB_LogTunnelEvent(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test.db")

	db, err := Open(dbPath)
	if err != nil {
		t.Fatalf("Failed to open database: %v", err)
	}
	defer db.Close()

	// Log a tunnel event
	err = db.LogTunnelEvent("work-vpn", "connect", "Connected successfully")
	if err != nil {
		t.Errorf("Failed to log tunnel event: %v", err)
	}

	// Query the event back
	rows, err := db.conn.Query(`
		SELECT tunnel_alias, event_type, details
		FROM tunnel_events
		ORDER BY timestamp DESC
		LIMIT 1
	`)
	if err != nil {
		t.Fatalf("Failed to query tunnel events: %v", err)
	}
	defer rows.Close()

	if !rows.Next() {
		t.Fatal("Expected at least one tunnel event record")
	}

	var tunnelAlias, eventType, details string
	if err := rows.Scan(&tunnelAlias, &eventType, &details); err != nil {
		t.Fatalf("Failed to scan row: %v", err)
	}

	if tunnelAlias != "work-vpn" {
		t.Errorf("Expected tunnel_alias='work-vpn', got '%v'", tunnelAlias)
	}
	if eventType != "connect" {
		t.Errorf("Expected event_type='connect', got '%v'", eventType)
	}
	if details != "Connected successfully" {
		t.Errorf("Expected details='Connected successfully', got '%v'", details)
	}
}

func TestDB_LogDaemonEvent(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test.db")

	db, err := Open(dbPath)
	if err != nil {
		t.Fatalf("Failed to open database: %v", err)
	}
	defer db.Close()

	// Log a daemon event
	err = db.LogDaemonEvent("start", "Daemon started (PID: 12345)")
	if err != nil {
		t.Errorf("Failed to log daemon event: %v", err)
	}

	// Query the event back
	rows, err := db.conn.Query(`
		SELECT event_type, details
		FROM daemon_events
		ORDER BY timestamp DESC
		LIMIT 1
	`)
	if err != nil {
		t.Fatalf("Failed to query daemon events: %v", err)
	}
	defer rows.Close()

	if !rows.Next() {
		t.Fatal("Expected at least one daemon event record")
	}

	var eventType, details string
	if err := rows.Scan(&eventType, &details); err != nil {
		t.Fatalf("Failed to scan row: %v", err)
	}

	if eventType != "start" {
		t.Errorf("Expected event_type='start', got '%v'", eventType)
	}
	if details != "Daemon started (PID: 12345)" {
		t.Errorf("Expected details='Daemon started (PID: 12345)', got '%v'", details)
	}
}

func TestDB_MultipleSensorChanges(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test.db")

	db, err := Open(dbPath)
	if err != nil {
		t.Fatalf("Failed to open database: %v", err)
	}
	defer db.Close()

	// Log changes for different sensors
	sensors := []struct {
		name, sensorType, oldVal, newVal string
	}{
		{"public_ip", "string", "192.168.1.100", "10.0.0.1"},
		{"online", "boolean", "true", "false"},
		{"context", "string", "trusted", "untrusted"},
		{"location", "string", "home", ""},
	}

	for _, s := range sensors {
		err := db.LogSensorChange(s.name, s.sensorType, s.oldVal, s.newVal)
		if err != nil {
			t.Fatalf("Failed to log sensor change: %v", err)
		}
	}

	// Count total sensor changes
	var count int
	err = db.conn.QueryRow("SELECT COUNT(*) FROM sensor_changes").Scan(&count)
	if err != nil {
		t.Fatalf("Failed to count sensor changes: %v", err)
	}

	if count != len(sensors) {
		t.Errorf("Expected %d sensor changes, got %d", len(sensors), count)
	}
}

func TestDB_WALMode(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test.db")

	db, err := Open(dbPath)
	if err != nil {
		t.Fatalf("Failed to open database: %v", err)
	}
	defer db.Close()

	// Verify WAL mode is enabled
	var journalMode string
	err = db.conn.QueryRow("PRAGMA journal_mode").Scan(&journalMode)
	if err != nil {
		t.Fatalf("Failed to query journal mode: %v", err)
	}

	if journalMode != "wal" {
		t.Errorf("Expected WAL journal mode, got '%v'", journalMode)
	}
}

func TestDB_TablesCreated(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test.db")

	db, err := Open(dbPath)
	if err != nil {
		t.Fatalf("Failed to open database: %v", err)
	}
	defer db.Close()

	// Check that expected tables were created
	expectedTables := []string{
		"sensor_changes",
		"tunnel_events",
		"daemon_events",
	}

	for _, tableName := range expectedTables {
		var count int
		err := db.conn.QueryRow(`
			SELECT COUNT(*) FROM sqlite_master
			WHERE type='table' AND name=?
		`, tableName).Scan(&count)

		if err != nil {
			t.Fatalf("Failed to check for table '%s': %v", tableName, err)
		}

		if count != 1 {
			t.Errorf("Expected table '%s' to exist", tableName)
		}
	}
}

func TestDB_Indexes(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test.db")

	db, err := Open(dbPath)
	if err != nil {
		t.Fatalf("Failed to open database: %v", err)
	}
	defer db.Close()

	// Check that indexes were created
	expectedIndexes := []string{
		"idx_sensor_changes_timestamp",
		"idx_sensor_changes_name",
		"idx_tunnel_events_timestamp",
		"idx_tunnel_events_alias",
		"idx_daemon_events_timestamp",
	}

	for _, indexName := range expectedIndexes {
		var count int
		err := db.conn.QueryRow(`
			SELECT COUNT(*) FROM sqlite_master
			WHERE type='index' AND name=?
		`, indexName).Scan(&count)

		if err != nil {
			t.Fatalf("Failed to check for index '%s': %v", indexName, err)
		}

		if count != 1 {
			t.Errorf("Expected index '%s' to exist", indexName)
		}
	}
}

// openTestDB is a helper that creates and returns a temporary database
func openTestDB(t *testing.T) *DB {
	t.Helper()
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test.db")
	db, err := Open(dbPath)
	if err != nil {
		t.Fatalf("Failed to open database: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	return db
}

func TestDB_GetRecentSensorChanges(t *testing.T) {
	db := openTestDB(t)

	// Insert several sensor changes with distinct timestamps
	baseTime := time.Now().Add(-10 * time.Second)
	changes := []struct {
		name, sensorType, oldVal, newVal string
		ts                               time.Time
	}{
		{"public_ip", "string", "1.1.1.1", "2.2.2.2", baseTime},
		{"online", "boolean", "true", "false", baseTime.Add(1 * time.Second)},
		{"public_ip", "string", "2.2.2.2", "3.3.3.3", baseTime.Add(2 * time.Second)},
	}

	for _, c := range changes {
		if err := db.LogSensorChangeAt(c.name, c.sensorType, c.oldVal, c.newVal, c.ts); err != nil {
			t.Fatalf("Failed to log sensor change: %v", err)
		}
	}

	t.Run("returns all when limit exceeds count", func(t *testing.T) {
		got, err := db.GetRecentSensorChanges(10)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if len(got) != 3 {
			t.Fatalf("expected 3 changes, got %d", len(got))
		}
	})

	t.Run("respects limit", func(t *testing.T) {
		got, err := db.GetRecentSensorChanges(2)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if len(got) != 2 {
			t.Fatalf("expected 2 changes, got %d", len(got))
		}
	})

	t.Run("ordered by timestamp descending", func(t *testing.T) {
		got, err := db.GetRecentSensorChanges(10)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		// Most recent first
		if got[0].NewValue != "3.3.3.3" {
			t.Errorf("expected most recent change first, got new_value=%q", got[0].NewValue)
		}
	})

	t.Run("empty table returns empty slice", func(t *testing.T) {
		emptyDB := openTestDB(t)
		got, err := emptyDB.GetRecentSensorChanges(10)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if len(got) != 0 {
			t.Errorf("expected 0 changes, got %d", len(got))
		}
	})
}

func TestDB_GetRecentTunnelEvents(t *testing.T) {
	db := openTestDB(t)

	events := []struct {
		alias, eventType, details string
	}{
		{"vpn", "connect", "Connected"},
		{"vpn", "disconnect", "Disconnected"},
		{"homelab", "connect", "Connected"},
	}

	for _, e := range events {
		if err := db.LogTunnelEvent(e.alias, e.eventType, e.details); err != nil {
			t.Fatalf("Failed to log tunnel event: %v", err)
		}
	}

	t.Run("returns all when limit exceeds count", func(t *testing.T) {
		got, err := db.GetRecentTunnelEvents(10)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if len(got) != 3 {
			t.Fatalf("expected 3 events, got %d", len(got))
		}
	})

	t.Run("respects limit", func(t *testing.T) {
		got, err := db.GetRecentTunnelEvents(1)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if len(got) != 1 {
			t.Fatalf("expected 1 event, got %d", len(got))
		}
	})

	t.Run("fields are populated correctly", func(t *testing.T) {
		got, err := db.GetRecentTunnelEvents(10)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		// Find the homelab event
		found := false
		for _, e := range got {
			if e.TunnelAlias == "homelab" {
				found = true
				if e.EventType != "connect" {
					t.Errorf("expected event_type='connect', got %q", e.EventType)
				}
				if e.ID == 0 {
					t.Error("expected non-zero ID")
				}
				if e.Timestamp.IsZero() {
					t.Error("expected non-zero timestamp")
				}
			}
		}
		if !found {
			t.Error("expected to find homelab event")
		}
	})
}

func TestDB_GetRecentDaemonEvents(t *testing.T) {
	db := openTestDB(t)

	events := []struct {
		eventType, details string
	}{
		{"start", "Daemon started"},
		{"config_reload", "Config reloaded"},
		{"stop", "Daemon stopped"},
	}

	for _, e := range events {
		if err := db.LogDaemonEvent(e.eventType, e.details); err != nil {
			t.Fatalf("Failed to log daemon event: %v", err)
		}
	}

	t.Run("returns all when limit exceeds count", func(t *testing.T) {
		got, err := db.GetRecentDaemonEvents(10)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if len(got) != 3 {
			t.Fatalf("expected 3 events, got %d", len(got))
		}
	})

	t.Run("respects limit", func(t *testing.T) {
		got, err := db.GetRecentDaemonEvents(2)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if len(got) != 2 {
			t.Fatalf("expected 2 events, got %d", len(got))
		}
	})

	t.Run("fields are populated correctly", func(t *testing.T) {
		got, err := db.GetRecentDaemonEvents(10)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		last := got[len(got)-1]
		if last.EventType != "start" {
			t.Errorf("expected oldest event_type='start', got %q", last.EventType)
		}
		if last.Details != "Daemon started" {
			t.Errorf("expected details='Daemon started', got %q", last.Details)
		}
		if last.ID == 0 {
			t.Error("expected non-zero ID")
		}
	})
}

func TestDB_GetLastTunnelEventPerAlias(t *testing.T) {
	db := openTestDB(t)

	// Log multiple events for each tunnel - only the latest per alias should be returned
	events := []struct {
		alias, eventType, details string
	}{
		{"vpn", "connect", "Connected"},
		{"homelab", "connect", "Connected"},
		{"vpn", "disconnect", "Disconnected"},
		{"homelab", "disconnect", "Disconnected"},
		{"vpn", "connect", "Reconnected"},
	}

	for _, e := range events {
		if err := db.LogTunnelEvent(e.alias, e.eventType, e.details); err != nil {
			t.Fatalf("Failed to log tunnel event: %v", err)
		}
	}

	got, err := db.GetLastTunnelEventPerAlias()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(got) != 2 {
		t.Fatalf("expected 2 events (one per alias), got %d", len(got))
	}

	// Build a map for easier checking
	byAlias := make(map[string]TunnelEvent)
	for _, e := range got {
		byAlias[e.TunnelAlias] = e
	}

	// vpn's last event should be "connect" (Reconnected)
	vpnEvent, ok := byAlias["vpn"]
	if !ok {
		t.Fatal("expected vpn event")
	}
	if vpnEvent.EventType != "connect" {
		t.Errorf("expected vpn last event_type='connect', got %q", vpnEvent.EventType)
	}
	if vpnEvent.Details != "Reconnected" {
		t.Errorf("expected vpn details='Reconnected', got %q", vpnEvent.Details)
	}

	// homelab's last event should be "disconnect"
	homelabEvent, ok := byAlias["homelab"]
	if !ok {
		t.Fatal("expected homelab event")
	}
	if homelabEvent.EventType != "disconnect" {
		t.Errorf("expected homelab last event_type='disconnect', got %q", homelabEvent.EventType)
	}
}

func TestDB_LogSensorChangeAt(t *testing.T) {
	db := openTestDB(t)

	// Log a change at a specific timestamp in the past
	ts := time.Date(2025, 6, 15, 12, 0, 0, 0, time.UTC)
	err := db.LogSensorChangeAt("public_ip", "string", "1.1.1.1", "2.2.2.2", ts)
	if err != nil {
		t.Fatalf("Failed to log sensor change at timestamp: %v", err)
	}

	changes, err := db.GetRecentSensorChanges(1)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(changes) != 1 {
		t.Fatalf("expected 1 change, got %d", len(changes))
	}

	if changes[0].SensorName != "public_ip" {
		t.Errorf("expected sensor_name='public_ip', got %q", changes[0].SensorName)
	}
	if changes[0].OldValue != "1.1.1.1" {
		t.Errorf("expected old_value='1.1.1.1', got %q", changes[0].OldValue)
	}
	if changes[0].NewValue != "2.2.2.2" {
		t.Errorf("expected new_value='2.2.2.2', got %q", changes[0].NewValue)
	}
}

func TestDB_DeleteSensorChangesNear(t *testing.T) {
	db := openTestDB(t)

	baseTime := time.Date(2025, 6, 15, 12, 0, 0, 0, time.UTC)

	// Insert changes at different times
	if err := db.LogSensorChangeAt("public_ip", "string", "1.1.1.1", "2.2.2.2", baseTime); err != nil {
		t.Fatal(err)
	}
	if err := db.LogSensorChangeAt("public_ip", "string", "2.2.2.2", "3.3.3.3", baseTime.Add(30*time.Second)); err != nil {
		t.Fatal(err)
	}
	if err := db.LogSensorChangeAt("public_ip", "string", "1.1.1.1", "2.2.2.2", baseTime.Add(5*time.Minute)); err != nil {
		t.Fatal(err)
	}

	t.Run("deletes matching changes within window", func(t *testing.T) {
		// Delete changes matching old=1.1.1.1, new=2.2.2.2 within 1 minute of baseTime
		deleted, err := db.DeleteSensorChangesNear("public_ip", "1.1.1.1", "2.2.2.2", baseTime, 1*time.Minute)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if deleted != 1 {
			t.Errorf("expected 1 row deleted, got %d", deleted)
		}
	})

	t.Run("does not delete changes outside window", func(t *testing.T) {
		// The one at baseTime+5min should still exist
		changes, err := db.GetRecentSensorChanges(10)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		// Should have 2 remaining (the 30s one and the 5min one)
		if len(changes) != 2 {
			t.Errorf("expected 2 remaining changes, got %d", len(changes))
		}
	})

	t.Run("returns zero when nothing matches", func(t *testing.T) {
		deleted, err := db.DeleteSensorChangesNear("nonexistent", "a", "b", baseTime, 1*time.Hour)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if deleted != 0 {
			t.Errorf("expected 0 rows deleted, got %d", deleted)
		}
	})
}

func TestDB_HasSensorChangeAfter(t *testing.T) {
	db := openTestDB(t)

	baseTime := time.Date(2025, 6, 15, 12, 0, 0, 0, time.UTC)
	if err := db.LogSensorChangeAt("public_ip", "string", "old", "1.2.3.4", baseTime); err != nil {
		t.Fatal(err)
	}

	t.Run("finds change within forward window", func(t *testing.T) {
		found, err := db.HasSensorChangeAfter("public_ip", "1.2.3.4", baseTime.Add(-1*time.Second), 5*time.Second)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if !found {
			t.Error("expected to find sensor change within window")
		}
	})

	t.Run("does not find change outside window", func(t *testing.T) {
		found, err := db.HasSensorChangeAfter("public_ip", "1.2.3.4", baseTime.Add(1*time.Second), 5*time.Second)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if found {
			t.Error("expected not to find sensor change outside window")
		}
	})

	t.Run("does not find change with wrong value", func(t *testing.T) {
		found, err := db.HasSensorChangeAfter("public_ip", "9.9.9.9", baseTime.Add(-1*time.Second), 5*time.Second)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if found {
			t.Error("expected not to find sensor change with wrong value")
		}
	})
}

func TestDB_HasSensorChangeNear(t *testing.T) {
	db := openTestDB(t)

	baseTime := time.Date(2025, 6, 15, 12, 0, 0, 0, time.UTC)
	if err := db.LogSensorChangeAt("online", "boolean", "true", "false", baseTime); err != nil {
		t.Fatal(err)
	}

	t.Run("finds change within symmetric window", func(t *testing.T) {
		found, err := db.HasSensorChangeNear("online", baseTime.Add(2*time.Second), 5*time.Second)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if !found {
			t.Error("expected to find sensor change near timestamp")
		}
	})

	t.Run("finds change looking backward", func(t *testing.T) {
		found, err := db.HasSensorChangeNear("online", baseTime.Add(3*time.Second), 5*time.Second)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if !found {
			t.Error("expected to find sensor change looking backward")
		}
	})

	t.Run("does not find change outside window", func(t *testing.T) {
		found, err := db.HasSensorChangeNear("online", baseTime.Add(1*time.Minute), 5*time.Second)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if found {
			t.Error("expected not to find sensor change outside window")
		}
	})

	t.Run("does not find change for wrong sensor", func(t *testing.T) {
		found, err := db.HasSensorChangeNear("nonexistent", baseTime, 5*time.Second)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if found {
			t.Error("expected not to find change for wrong sensor")
		}
	})
}

func TestDB_Flush(t *testing.T) {
	db := openTestDB(t)

	// Write some data
	if err := db.LogSensorChange("test", "string", "a", "b"); err != nil {
		t.Fatalf("Failed to log: %v", err)
	}

	// Flush should not error
	if err := db.Flush(); err != nil {
		t.Errorf("Flush() error = %v", err)
	}
}

func TestDB_Flush_NilConn(t *testing.T) {
	db := &DB{conn: nil}

	// Flush on nil conn should return nil, not panic
	if err := db.Flush(); err != nil {
		t.Errorf("Flush() on nil conn error = %v", err)
	}
}

func TestDB_Close_NilConn(t *testing.T) {
	db := &DB{conn: nil}

	// Close on nil conn should return nil, not panic
	if err := db.Close(); err != nil {
		t.Errorf("Close() on nil conn error = %v", err)
	}
}

func TestDB_Open_CreatesDirectory(t *testing.T) {
	tmpDir := t.TempDir()
	// Nested path that doesn't exist yet
	dbPath := filepath.Join(tmpDir, "nested", "subdir", "test.db")

	db, err := Open(dbPath)
	if err != nil {
		t.Fatalf("Failed to open database with nested path: %v", err)
	}
	defer db.Close()

	if _, err := os.Stat(dbPath); os.IsNotExist(err) {
		t.Error("Database file was not created in nested directory")
	}
}
