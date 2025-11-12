package db

import (
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	_ "modernc.org/sqlite"
)

// DB wraps the SQLite database connection and provides logging methods
type DB struct {
	conn *sql.DB
	path string
}

// Open opens or creates the SQLite database at the specified path
func Open(path string) (*DB, error) {
	// Ensure directory exists
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return nil, fmt.Errorf("failed to create database directory: %w", err)
	}

	// Open database
	conn, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, fmt.Errorf("failed to open database: %w", err)
	}

	// Enable WAL mode for better concurrency
	if _, err := conn.Exec("PRAGMA journal_mode=WAL"); err != nil {
		conn.Close()
		return nil, fmt.Errorf("failed to enable WAL mode: %w", err)
	}

	db := &DB{
		conn: conn,
		path: path,
	}

	// Initialize schema
	if err := db.initSchema(); err != nil {
		conn.Close()
		return nil, fmt.Errorf("failed to initialize schema: %w", err)
	}

	return db, nil
}

// Close closes the database connection
func (db *DB) Close() error {
	if db.conn != nil {
		// Checkpoint the WAL to ensure all data is written to the main database file
		db.conn.Exec("PRAGMA wal_checkpoint(TRUNCATE)")
		return db.conn.Close()
	}
	return nil
}

// Flush forces a WAL checkpoint to write pending changes to the main database file
func (db *DB) Flush() error {
	if db.conn != nil {
		// Use RESTART mode to force checkpoint even if there are active readers
		_, err := db.conn.Exec("PRAGMA wal_checkpoint(RESTART)")
		return err
	}
	return nil
}

// initSchema creates the database tables if they don't exist
func (db *DB) initSchema() error {
	schema := `
	-- Sensor state changes
	CREATE TABLE IF NOT EXISTS sensor_changes (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		sensor_name TEXT NOT NULL,
		sensor_type TEXT NOT NULL,
		old_value TEXT,
		new_value TEXT,
		timestamp DATETIME DEFAULT CURRENT_TIMESTAMP
	);

	-- Tunnel lifecycle events
	CREATE TABLE IF NOT EXISTS tunnel_events (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		tunnel_alias TEXT NOT NULL,
		event_type TEXT NOT NULL,
		details TEXT,
		timestamp DATETIME DEFAULT CURRENT_TIMESTAMP
	);

	-- Daemon lifecycle events
	CREATE TABLE IF NOT EXISTS daemon_events (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		event_type TEXT NOT NULL,
		details TEXT,
		timestamp DATETIME DEFAULT CURRENT_TIMESTAMP
	);

	-- Indexes for common queries
	CREATE INDEX IF NOT EXISTS idx_sensor_changes_timestamp ON sensor_changes(timestamp);
	CREATE INDEX IF NOT EXISTS idx_sensor_changes_name ON sensor_changes(sensor_name);
	CREATE INDEX IF NOT EXISTS idx_tunnel_events_timestamp ON tunnel_events(timestamp);
	CREATE INDEX IF NOT EXISTS idx_tunnel_events_alias ON tunnel_events(tunnel_alias);
	CREATE INDEX IF NOT EXISTS idx_daemon_events_timestamp ON daemon_events(timestamp);
	`

	_, err := db.conn.Exec(schema)
	return err
}

// SensorChange represents a sensor state change
type SensorChange struct {
	ID         int64
	SensorName string
	SensorType string
	OldValue   string
	NewValue   string
	Timestamp  time.Time
}

// LogSensorChange logs a sensor state change to the database
func (db *DB) LogSensorChange(sensorName, sensorType, oldValue, newValue string) error {
	_, err := db.conn.Exec(
		`INSERT INTO sensor_changes (sensor_name, sensor_type, old_value, new_value, timestamp)
		 VALUES (?, ?, ?, ?, ?)`,
		sensorName, sensorType, oldValue, newValue, time.Now(),
	)
	return err
}

// TunnelEvent represents a tunnel lifecycle event
type TunnelEvent struct {
	ID          int64
	TunnelAlias string
	EventType   string
	Details     string
	Timestamp   time.Time
}

// LogTunnelEvent logs a tunnel lifecycle event to the database
func (db *DB) LogTunnelEvent(tunnelAlias, eventType, details string) error {
	// Retry briefly if database is locked (3 attempts, 5ms between)
	// This is best-effort - we don't want to block daemon shutdown
	maxRetries := 3
	for i := 0; i < maxRetries; i++ {
		_, err := db.conn.Exec(
			`INSERT INTO tunnel_events (tunnel_alias, event_type, details, timestamp)
			 VALUES (?, ?, ?, ?)`,
			tunnelAlias, eventType, details, time.Now(),
		)
		if err == nil {
			return nil
		}
		// Check if error is SQLITE_BUSY
		if strings.Contains(err.Error(), "database is locked") || strings.Contains(err.Error(), "SQLITE_BUSY") {
			// Wait briefly and retry
			time.Sleep(5 * time.Millisecond)
			continue
		}
		// Other error, return immediately
		return err
	}
	return fmt.Errorf("failed to log tunnel event after %d retries: database locked", maxRetries)
}

// DaemonEvent represents a daemon lifecycle event
type DaemonEvent struct {
	ID        int64
	EventType string
	Details   string
	Timestamp time.Time
}

// LogDaemonEvent logs a daemon lifecycle event to the database
func (db *DB) LogDaemonEvent(eventType, details string) error {
	_, err := db.conn.Exec(
		`INSERT INTO daemon_events (event_type, details, timestamp)
		 VALUES (?, ?, ?)`,
		eventType, details, time.Now(),
	)
	return err
}

// GetRecentSensorChanges retrieves recent sensor changes
func (db *DB) GetRecentSensorChanges(limit int) ([]SensorChange, error) {
	rows, err := db.conn.Query(
		`SELECT id, sensor_name, sensor_type, old_value, new_value, timestamp
		 FROM sensor_changes
		 ORDER BY timestamp DESC
		 LIMIT ?`,
		limit,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var changes []SensorChange
	for rows.Next() {
		var c SensorChange
		if err := rows.Scan(&c.ID, &c.SensorName, &c.SensorType, &c.OldValue, &c.NewValue, &c.Timestamp); err != nil {
			return nil, err
		}
		changes = append(changes, c)
	}
	return changes, rows.Err()
}

// GetRecentTunnelEvents retrieves recent tunnel events
func (db *DB) GetRecentTunnelEvents(limit int) ([]TunnelEvent, error) {
	rows, err := db.conn.Query(
		`SELECT id, tunnel_alias, event_type, details, timestamp
		 FROM tunnel_events
		 ORDER BY timestamp DESC
		 LIMIT ?`,
		limit,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var events []TunnelEvent
	for rows.Next() {
		var e TunnelEvent
		if err := rows.Scan(&e.ID, &e.TunnelAlias, &e.EventType, &e.Details, &e.Timestamp); err != nil {
			return nil, err
		}
		events = append(events, e)
	}
	return events, rows.Err()
}

// GetRecentDaemonEvents retrieves recent daemon events
func (db *DB) GetRecentDaemonEvents(limit int) ([]DaemonEvent, error) {
	rows, err := db.conn.Query(
		`SELECT id, event_type, details, timestamp
		 FROM daemon_events
		 ORDER BY timestamp DESC
		 LIMIT ?`,
		limit,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var events []DaemonEvent
	for rows.Next() {
		var e DaemonEvent
		if err := rows.Scan(&e.ID, &e.EventType, &e.Details, &e.Timestamp); err != nil {
			return nil, err
		}
		events = append(events, e)
	}
	return events, rows.Err()
}

// GetLastTunnelEventPerAlias retrieves the most recent event for each tunnel alias
func (db *DB) GetLastTunnelEventPerAlias() ([]TunnelEvent, error) {
	rows, err := db.conn.Query(
		`SELECT id, tunnel_alias, event_type, details, timestamp
		 FROM tunnel_events
		 WHERE id IN (
			 SELECT MAX(id)
			 FROM tunnel_events
			 GROUP BY tunnel_alias
		 )
		 ORDER BY timestamp DESC`,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var events []TunnelEvent
	for rows.Next() {
		var e TunnelEvent
		if err := rows.Scan(&e.ID, &e.TunnelAlias, &e.EventType, &e.Details, &e.Timestamp); err != nil {
			return nil, err
		}
		events = append(events, e)
	}
	return events, rows.Err()
}
