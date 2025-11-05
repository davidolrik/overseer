package security

import (
	"context"
	"fmt"
)

// OnlineSensor is a boolean sensor that indicates network connectivity
// It subscribes to the public_ip sensor and determines online status based on IP resolution
type OnlineSensor struct {
	*BaseSensor
}

// NewOnlineSensor creates a new online sensor
func NewOnlineSensor() *OnlineSensor {
	return &OnlineSensor{
		BaseSensor: NewBaseSensor("online", SensorTypeBoolean),
	}
}

// Check returns the current online status
// The actual value is set by listening to public_ip sensor changes
func (s *OnlineSensor) Check(ctx context.Context) (SensorValue, error) {
	// If we don't have a value yet, default to false (offline)
	lastValue := s.GetLastValue()
	if lastValue == nil {
		defaultValue := NewSensorValue(s.Name(), s.Type(), false)
		s.SetLastValue(defaultValue)
		return defaultValue, nil
	}
	return *lastValue, nil
}

// SetValue sets the online status (called by pub/sub system or manually)
func (s *OnlineSensor) SetValue(value interface{}) error {
	// Convert to boolean
	var boolValue bool
	switch v := value.(type) {
	case bool:
		boolValue = v
	default:
		return fmt.Errorf("online sensor requires boolean value, got %T", value)
	}

	newValue := NewSensorValue(s.Name(), s.Type(), boolValue)
	oldValue := s.GetLastValue()

	// Only notify if value changed
	if oldValue == nil || !oldValue.Equals(newValue) {
		// Create a default old value if this is the first change
		if oldValue == nil {
			defaultOld := NewSensorValue(s.Name(), s.Type(), false)
			oldValue = &defaultOld
		}
		s.NotifyListeners(s, *oldValue, newValue)
		s.SetLastValue(newValue)
	}

	return nil
}

// OnSensorChange implements SensorListener to react to public_ip changes
func (s *OnlineSensor) OnSensorChange(sensor Sensor, oldValue, newValue SensorValue) {
	// Only react to public_ip sensor
	if sensor.Name() != "public_ip" {
		return
	}

	// Determine if we're online based on the IP address
	// If IP is 169.254.0.0 (link-local), we're offline
	// Otherwise, we're online
	ip := newValue.String()
	isOnline := ip != "169.254.0.0" && ip != ""

	// Update our own value
	s.SetValue(isOnline)
}
