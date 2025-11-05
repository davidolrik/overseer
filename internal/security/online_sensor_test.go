package security

import (
	"context"
	"testing"
)

func TestOnlineSensor_ReactsToIPChanges(t *testing.T) {
	// Create sensors
	ipSensor := &MockSensor{
		name:       "public_ip",
		sensorType: SensorTypeString,
		value:      "192.168.1.100",
	}

	onlineSensor := NewOnlineSensor()

	// Subscribe online sensor to IP changes
	ipSensor.Subscribe(onlineSensor)

	tests := []struct {
		name         string
		newIPValue   string
		wantOnline   bool
	}{
		{
			name:       "Valid IP - should be online",
			newIPValue: "192.168.1.100",
			wantOnline: true,
		},
		{
			name:       "Different valid IP - still online",
			newIPValue: "10.0.0.1",
			wantOnline: true,
		},
		{
			name:       "Offline IP (169.254.0.0) - should be offline",
			newIPValue: "169.254.0.0",
			wantOnline: false,
		},
		{
			name:       "Empty IP - should be offline",
			newIPValue: "",
			wantOnline: false,
		},
		{
			name:       "Back online",
			newIPValue: "8.8.8.8",
			wantOnline: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Simulate IP change
			oldValue := NewSensorValue(ipSensor.Name(), ipSensor.Type(), ipSensor.value)
			ipSensor.SetValue(tt.newIPValue)
			newValue := NewSensorValue(ipSensor.Name(), ipSensor.Type(), tt.newIPValue)

			// Trigger the OnSensorChange callback
			onlineSensor.OnSensorChange(ipSensor, oldValue, newValue)

			// Check online sensor value
			value, err := onlineSensor.Check(context.Background())
			if err != nil {
				t.Fatalf("Check() error = %v", err)
			}

			if value.Bool() != tt.wantOnline {
				t.Errorf("Expected online=%v, got %v", tt.wantOnline, value.Bool())
			}
		})
	}
}

func TestOnlineSensor_Type(t *testing.T) {
	sensor := NewOnlineSensor()

	if sensor.Type() != SensorTypeBoolean {
		t.Errorf("Expected type=%v, got %v", SensorTypeBoolean, sensor.Type())
	}

	if sensor.Name() != "online" {
		t.Errorf("Expected name='online', got '%v'", sensor.Name())
	}
}

func TestOnlineSensor_IgnoresOtherSensors(t *testing.T) {
	onlineSensor := NewOnlineSensor()

	// Set initial value
	onlineSensor.SetValue(true)

	// Create a different sensor
	envSensor := &MockSensor{
		name:       "env:HOME",
		sensorType: SensorTypeString,
		value:      "yes",
	}

	// Simulate change from different sensor
	oldValue := NewSensorValue(envSensor.Name(), envSensor.Type(), "no")
	newValue := NewSensorValue(envSensor.Name(), envSensor.Type(), "yes")

	onlineSensor.OnSensorChange(envSensor, oldValue, newValue)

	// Online sensor should not change
	value, err := onlineSensor.Check(context.Background())
	if err != nil {
		t.Fatalf("Check() error = %v", err)
	}

	if !value.Bool() {
		t.Error("Online sensor should still be true (unchanged)")
	}
}

func TestOnlineSensor_NotifiesListeners(t *testing.T) {
	ipSensor := &MockSensor{
		name:       "public_ip",
		sensorType: SensorTypeString,
		value:      "192.168.1.100",
	}

	onlineSensor := NewOnlineSensor()

	// Subscribe online sensor to IP changes
	ipSensor.Subscribe(onlineSensor)

	// Create a mock listener to track changes
	listener := &MockListener{
		changes: []SensorValue{},
	}
	onlineSensor.Subscribe(listener)

	// Trigger IP change that should cause online to change
	oldValue := NewSensorValue(ipSensor.Name(), ipSensor.Type(), "192.168.1.100")
	ipSensor.SetValue("169.254.0.0") // Offline IP
	newValue := NewSensorValue(ipSensor.Name(), ipSensor.Type(), "169.254.0.0")

	onlineSensor.OnSensorChange(ipSensor, oldValue, newValue)

	// Check that listener was notified
	if len(listener.changes) == 0 {
		t.Error("Expected listener to be notified of online sensor change")
	}
}

// MockListener tracks sensor changes
type MockListener struct {
	changes []SensorValue
}

func (m *MockListener) OnSensorChange(sensor Sensor, oldValue, newValue SensorValue) {
	m.changes = append(m.changes, newValue)
}
