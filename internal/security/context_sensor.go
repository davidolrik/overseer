package security

import (
	"context"
	"fmt"
)

// ContextSensor is a passive string sensor that holds the current security context
// Its value is set by the rule evaluation engine
type ContextSensor struct {
	*BaseSensor
}

// NewContextSensor creates a new context sensor
func NewContextSensor() *ContextSensor {
	return &ContextSensor{
		BaseSensor: NewBaseSensor("context", SensorTypeString),
	}
}

// Check returns the current context value
func (s *ContextSensor) Check(ctx context.Context) (SensorValue, error) {
	// If we don't have a value yet, default to empty string
	lastValue := s.GetLastValue()
	if lastValue == nil {
		defaultValue := NewSensorValue(s.Name(), s.Type(), "")
		s.SetLastValue(defaultValue)
		return defaultValue, nil
	}
	return *lastValue, nil
}

// SetValue sets the context value (called by rule evaluation engine)
func (s *ContextSensor) SetValue(value interface{}) error {
	// Convert to string
	var strValue string
	switch v := value.(type) {
	case string:
		strValue = v
	default:
		return fmt.Errorf("context sensor requires string value, got %T", value)
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
