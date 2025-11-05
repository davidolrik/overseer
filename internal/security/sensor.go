package security

import (
	"context"
	"fmt"
	"sync"
	"time"
)

// SensorType represents the type of value a sensor produces
type SensorType string

const (
	SensorTypeBoolean SensorType = "boolean"
	SensorTypeString  SensorType = "string"
)

// Sensor represents a source of context information
type Sensor interface {
	// Name returns the unique identifier for this sensor
	Name() string

	// Type returns the type of value this sensor produces
	Type() SensorType

	// Check performs a sensor reading and returns the current value
	Check(ctx context.Context) (SensorValue, error)

	// Subscribe adds a listener for sensor changes
	Subscribe(listener SensorListener)

	// Unsubscribe removes a listener
	Unsubscribe(listener SensorListener)

	// SetValue sets the sensor value (for passive sensors)
	SetValue(value interface{}) error
}

// SensorListener receives notifications when a sensor value changes
type SensorListener interface {
	OnSensorChange(sensor Sensor, oldValue, newValue SensorValue)
}

// SensorValue represents a single reading from a sensor
type SensorValue struct {
	Key       string      // Sensor key (e.g., "public_ip")
	Type      SensorType  // The type of this value
	Value     interface{} // The actual value
	Timestamp time.Time   // When this reading was taken
}

// NewSensorValue creates a new sensor value with the current timestamp
func NewSensorValue(key string, sensorType SensorType, value interface{}) SensorValue {
	return SensorValue{
		Key:       key,
		Type:      sensorType,
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
	return fmt.Sprintf("%v", sv.Value)
}

// Bool returns the boolean value of the sensor
func (sv SensorValue) Bool() bool {
	if sv.Value == nil {
		return false
	}
	if b, ok := sv.Value.(bool); ok {
		return b
	}
	return false
}

// Equals checks if two sensor values are equal
func (sv SensorValue) Equals(other SensorValue) bool {
	if sv.Type != other.Type {
		return false
	}

	switch sv.Type {
	case SensorTypeBoolean:
		return sv.Bool() == other.Bool()
	case SensorTypeString:
		return sv.String() == other.String()
	default:
		return sv.Value == other.Value
	}
}

// BaseSensor provides common functionality for all sensors
type BaseSensor struct {
	name      string
	sensorType SensorType
	listeners []SensorListener
	mu        sync.RWMutex
	lastValue *SensorValue
}

// NewBaseSensor creates a new base sensor
func NewBaseSensor(name string, sensorType SensorType) *BaseSensor {
	return &BaseSensor{
		name:       name,
		sensorType: sensorType,
		listeners:  make([]SensorListener, 0),
	}
}

// Name returns the sensor name
func (bs *BaseSensor) Name() string {
	return bs.name
}

// Type returns the sensor type
func (bs *BaseSensor) Type() SensorType {
	return bs.sensorType
}

// Subscribe adds a listener
func (bs *BaseSensor) Subscribe(listener SensorListener) {
	bs.mu.Lock()
	defer bs.mu.Unlock()
	bs.listeners = append(bs.listeners, listener)
}

// Unsubscribe removes a listener
func (bs *BaseSensor) Unsubscribe(listener SensorListener) {
	bs.mu.Lock()
	defer bs.mu.Unlock()
	for i, l := range bs.listeners {
		if l == listener {
			bs.listeners = append(bs.listeners[:i], bs.listeners[i+1:]...)
			break
		}
	}
}

// NotifyListeners notifies all listeners of a value change
func (bs *BaseSensor) NotifyListeners(sensor Sensor, oldValue, newValue SensorValue) {
	bs.mu.RLock()
	listeners := make([]SensorListener, len(bs.listeners))
	copy(listeners, bs.listeners)
	bs.mu.RUnlock()

	for _, listener := range listeners {
		listener.OnSensorChange(sensor, oldValue, newValue)
	}
}

// GetLastValue returns the last recorded value
func (bs *BaseSensor) GetLastValue() *SensorValue {
	bs.mu.RLock()
	defer bs.mu.RUnlock()
	return bs.lastValue
}

// SetLastValue sets the last recorded value
func (bs *BaseSensor) SetLastValue(value SensorValue) {
	bs.mu.Lock()
	defer bs.mu.Unlock()
	bs.lastValue = &value
}
