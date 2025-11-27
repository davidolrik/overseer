package security

import (
	"context"
	"fmt"
)

// OnlineSensor is a boolean sensor that indicates network connectivity
// It subscribes to both tcp and public_ipv4 sensors, with tcp taking precedence
type OnlineSensor struct {
	*BaseSensor
	// Track the last TCP result - tcp takes precedence over public_ipv4
	lastTCPResult *bool
}

// NewOnlineSensor creates a new online sensor
func NewOnlineSensor() *OnlineSensor {
	return &OnlineSensor{
		BaseSensor: NewBaseSensor("online", SensorTypeBoolean),
	}
}

// Check returns the current online status
// The actual value is set by listening to tcp and public_ipv4 sensor changes
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

// OnSensorChange implements SensorListener to react to tcp and public_ipv4 changes
// TCP sensor takes precedence over public_ipv4 for determining online status
func (s *OnlineSensor) OnSensorChange(sensor Sensor, oldValue, newValue SensorValue) {
	switch sensor.Name() {
	case "tcp":
		// TCP sensor takes precedence - directly use its result
		isOnline := newValue.Bool()
		s.mu.Lock()
		s.lastTCPResult = &isOnline
		s.mu.Unlock()
		s.SetValue(isOnline)

	case "public_ipv4":
		// Only use public_ipv4 if tcp hasn't been checked or tcp is unavailable
		s.mu.RLock()
		hasTCPResult := s.lastTCPResult != nil
		s.mu.RUnlock()

		if hasTCPResult {
			// TCP sensor is active and has reported a result - ignore public_ipv4
			return
		}

		// Fallback: determine online status from public_ipv4
		// If IP is 169.254.0.0 (link-local offline indicator) or empty, we're offline
		// Otherwise, we're online
		ip := newValue.String()
		isOnline := ip != "169.254.0.0" && ip != ""
		s.SetValue(isOnline)
	}
}
