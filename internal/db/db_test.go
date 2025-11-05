package db

import (
	"os"
	"path/filepath"
	"testing"
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
