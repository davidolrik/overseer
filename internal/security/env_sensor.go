package security

import (
	"context"
	"os"
)

// EnvSensor checks the value of an environment variable
type EnvSensor struct {
	varName string
}

// NewEnvSensor creates a new environment variable sensor
func NewEnvSensor(varName string) *EnvSensor {
	return &EnvSensor{
		varName: varName,
	}
}

// Name returns the sensor identifier in the format "env:VAR_NAME"
func (s *EnvSensor) Name() string {
	return "env:" + s.varName
}

// Check retrieves the current value of the environment variable
// Returns an empty string if the variable is not set
func (s *EnvSensor) Check(ctx context.Context) (SensorValue, error) {
	value := os.Getenv(s.varName)
	return NewSensorValue(s.Name(), value), nil
}
