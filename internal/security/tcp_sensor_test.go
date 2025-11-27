package security

import (
	"context"
	"testing"
)

func TestTCPSensor_Type(t *testing.T) {
	sensor := NewTCPSensor()

	if sensor.Type() != SensorTypeBoolean {
		t.Errorf("Expected type=%v, got %v", SensorTypeBoolean, sensor.Type())
	}

	if sensor.Name() != "tcp" {
		t.Errorf("Expected name='tcp', got '%v'", sensor.Name())
	}
}

func TestTCPSensor_CannotSetValue(t *testing.T) {
	sensor := NewTCPSensor()

	err := sensor.SetValue(true)
	if err == nil {
		t.Error("Expected error when setting value on active sensor")
	}
}

func TestTCPSensor_HasDefaultTargets(t *testing.T) {
	sensor := NewTCPSensor()

	if len(sensor.targets) == 0 {
		t.Error("Expected default TCP targets")
	}

	// Check that we have IPv4 targets
	hasIPv4 := false
	for _, target := range sensor.targets {
		if target.Network == "tcp4" {
			hasIPv4 = true
			break
		}
	}

	if !hasIPv4 {
		t.Error("Expected at least one IPv4 TCP target")
	}
}

func TestTCPSensor_NotifiesListeners(t *testing.T) {
	sensor := NewTCPSensor()

	// Create a mock listener
	listener := &MockListener{
		changes: []SensorValue{},
	}
	sensor.Subscribe(listener)

	// Check the sensor (this will make an actual network call)
	// We can't control the result, but we can verify the sensor works
	ctx := context.Background()
	value, err := sensor.Check(ctx)
	if err != nil {
		t.Fatalf("Check() error = %v", err)
	}

	// Value should be boolean
	if value.Type != SensorTypeBoolean {
		t.Errorf("Expected boolean value type, got %v", value.Type)
	}

	// First check should always notify (value changed from nil)
	if len(listener.changes) == 0 {
		t.Error("Expected listener to be notified on first check")
	}
}

func TestOnlineSensor_TCPTakesPrecedence(t *testing.T) {
	// Create sensors
	tcpSensor := &MockSensor{
		name:       "tcp",
		sensorType: SensorTypeBoolean,
		value:      true,
	}

	ipSensor := &MockSensor{
		name:       "public_ipv4",
		sensorType: SensorTypeString,
		value:      "192.168.1.100",
	}

	onlineSensor := NewOnlineSensor()

	// Subscribe online sensor to both
	tcpSensor.Subscribe(onlineSensor)
	ipSensor.Subscribe(onlineSensor)

	// First, tcp reports online
	tcpSensor.SetValue(true)
	tcpOld := NewSensorValue(tcpSensor.Name(), tcpSensor.Type(), false)
	tcpNew := NewSensorValue(tcpSensor.Name(), tcpSensor.Type(), true)
	onlineSensor.OnSensorChange(tcpSensor, tcpOld, tcpNew)

	// Verify online
	value, _ := onlineSensor.Check(context.Background())
	if !value.Bool() {
		t.Error("Expected online=true after tcp success")
	}

	// Now IP sensor reports offline IP
	ipSensor.SetValue("169.254.0.0")
	ipOld := NewSensorValue(ipSensor.Name(), ipSensor.Type(), "192.168.1.100")
	ipNew := NewSensorValue(ipSensor.Name(), ipSensor.Type(), "169.254.0.0")
	onlineSensor.OnSensorChange(ipSensor, ipOld, ipNew)

	// Online should STILL be true because tcp takes precedence
	value, _ = onlineSensor.Check(context.Background())
	if !value.Bool() {
		t.Error("Expected online=true (tcp takes precedence over public_ipv4)")
	}

	// Now tcp reports offline
	tcpSensor.SetValue(false)
	tcpOld = NewSensorValue(tcpSensor.Name(), tcpSensor.Type(), true)
	tcpNew = NewSensorValue(tcpSensor.Name(), tcpSensor.Type(), false)
	onlineSensor.OnSensorChange(tcpSensor, tcpOld, tcpNew)

	// Now we should be offline
	value, _ = onlineSensor.Check(context.Background())
	if value.Bool() {
		t.Error("Expected online=false after tcp failure")
	}
}

func TestOnlineSensor_FallsBackToIPWhenNoTCP(t *testing.T) {
	// Create only IP sensor (no tcp)
	ipSensor := &MockSensor{
		name:       "public_ipv4",
		sensorType: SensorTypeString,
		value:      "192.168.1.100",
	}

	onlineSensor := NewOnlineSensor()
	ipSensor.Subscribe(onlineSensor)

	// IP sensor reports valid IP
	ipOld := NewSensorValue(ipSensor.Name(), ipSensor.Type(), "")
	ipNew := NewSensorValue(ipSensor.Name(), ipSensor.Type(), "192.168.1.100")
	onlineSensor.OnSensorChange(ipSensor, ipOld, ipNew)

	// Should be online based on IP
	value, _ := onlineSensor.Check(context.Background())
	if !value.Bool() {
		t.Error("Expected online=true from IP sensor when no tcp available")
	}

	// IP sensor reports offline (link-local address)
	ipOld = NewSensorValue(ipSensor.Name(), ipSensor.Type(), "192.168.1.100")
	ipNew = NewSensorValue(ipSensor.Name(), ipSensor.Type(), "169.254.0.0")
	onlineSensor.OnSensorChange(ipSensor, ipOld, ipNew)

	// Should be offline based on IP
	value, _ = onlineSensor.Check(context.Background())
	if value.Bool() {
		t.Error("Expected online=false from IP sensor (offline IP)")
	}
}
