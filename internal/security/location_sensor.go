package security

import (
	"context"
	"fmt"
)

// LocationSensor is a passive string sensor that holds the current location
// Its value is set by the rule evaluation engine
type LocationSensor struct {
	*BaseSensor
}

// NewLocationSensor creates a new location sensor
func NewLocationSensor() *LocationSensor {
	return &LocationSensor{
		BaseSensor: NewBaseSensor("location", SensorTypeString),
	}
}

// Check returns the current location value
func (s *LocationSensor) Check(ctx context.Context) (SensorValue, error) {
	// If we don't have a value yet, default to "unknown"
	lastValue := s.GetLastValue()
	if lastValue == nil {
		defaultValue := NewSensorValue(s.Name(), s.Type(), "unknown")
		s.SetLastValue(defaultValue)
		return defaultValue, nil
	}
	return *lastValue, nil
}

// SetValue sets the location value (called by rule evaluation engine)
func (s *LocationSensor) SetValue(value interface{}) error {
	// Convert to string
	var strValue string
	switch v := value.(type) {
	case string:
		strValue = v
	default:
		return fmt.Errorf("location sensor requires string value, got %T", value)
	}

	newValue := NewSensorValue(s.Name(), s.Type(), strValue)
	oldValue := s.GetLastValue()

	// Only notify if value changed
	if oldValue == nil || !oldValue.Equals(newValue) {
		if oldValue != nil {
			s.NotifyListeners(s, *oldValue, newValue)
		}
		s.SetLastValue(newValue)
	}

	return nil
}
