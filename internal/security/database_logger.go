package security

import (
	"log/slog"
)

// DatabaseLogger is a sensor listener that logs all sensor changes to the database
type DatabaseLogger struct {
	db     DatabaseInterface
	logger *slog.Logger
}

// DatabaseInterface defines the methods needed from the database
type DatabaseInterface interface {
	LogSensorChange(sensorName, sensorType, oldValue, newValue string) error
}

// NewDatabaseLogger creates a new database logger
func NewDatabaseLogger(db DatabaseInterface, logger *slog.Logger) *DatabaseLogger {
	return &DatabaseLogger{
		db:     db,
		logger: logger,
	}
}

// OnSensorChange implements SensorListener to log sensor changes to database
func (dl *DatabaseLogger) OnSensorChange(sensor Sensor, oldValue, newValue SensorValue) {
	if dl.db == nil {
		return
	}

	err := dl.db.LogSensorChange(
		sensor.Name(),
		string(sensor.Type()),
		oldValue.String(),
		newValue.String(),
	)

	if err != nil {
		dl.logger.Error("Failed to log sensor change to database",
			"sensor", sensor.Name(),
			"error", err)
	} else {
		dl.logger.Debug("Sensor change logged to database",
			"sensor", sensor.Name(),
			"old", oldValue.String(),
			"new", newValue.String())
	}
}
