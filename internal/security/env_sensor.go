package security

import (
	"context"
	"fmt"
	"os"
)

// EnvSensor checks the value of an environment variable
type EnvSensor struct {
	*BaseSensor
	varName string
}

// NewEnvSensor creates a new environment variable sensor
func NewEnvSensor(varName string) *EnvSensor {
	return &EnvSensor{
		BaseSensor: NewBaseSensor("env:"+varName, SensorTypeString),
		varName:    varName,
	}
}

// Check retrieves the current value of the environment variable
// Returns an empty string if the variable is not set
func (s *EnvSensor) Check(ctx context.Context) (SensorValue, error) {
	value := os.Getenv(s.varName)
	newValue := NewSensorValue(s.Name(), s.Type(), value)

	// Notify listeners if value changed
	oldValue := s.GetLastValue()
	if oldValue == nil || !oldValue.Equals(newValue) {
		if oldValue != nil {
			s.NotifyListeners(s, *oldValue, newValue)
		}
		s.SetLastValue(newValue)
	}

	return newValue, nil
}

// SetValue is not supported for active sensors
func (s *EnvSensor) SetValue(value interface{}) error {
	return fmt.Errorf("cannot set value on active sensor %s", s.Name())
}
