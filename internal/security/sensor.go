package security

import (
	"context"
	"time"
)

// Sensor represents a source of context information
type Sensor interface {
	// Name returns the unique identifier for this sensor
	Name() string

	// Check performs a sensor reading and returns the current value
	Check(ctx context.Context) (SensorValue, error)
}

// SensorValue represents a single reading from a sensor
type SensorValue struct {
	Key       string      // Sensor key (e.g., "public_ip")
	Value     interface{} // The actual value
	Timestamp time.Time   // When this reading was taken
}

// NewSensorValue creates a new sensor value with the current timestamp
func NewSensorValue(key string, value interface{}) SensorValue {
	return SensorValue{
		Key:       key,
		Value:     value,
		Timestamp: time.Now(),
	}
}

// String returns the string representation of the sensor value
func (sv SensorValue) String() string {
	if sv.Value == nil {
		return ""
	}
	if s, ok := sv.Value.(string); ok {
		return s
	}
	return ""
}
