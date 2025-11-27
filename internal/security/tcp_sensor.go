package security

import (
	"context"
	"fmt"
	"log/slog"
	"net"
	"time"
)

// TCPTarget defines a host to check for connectivity via TCP
type TCPTarget struct {
	Host    string // Host address (IP or hostname)
	Port    string // Port to connect to (e.g., "443", "80")
	Network string // Network type: "tcp", "tcp4", "tcp6"
}

// TCPSensor checks network connectivity by attempting TCP connections to known hosts
// This takes precedence over the public_ip sensor for determining online status
type TCPSensor struct {
	*BaseSensor
	targets []TCPTarget
	timeout time.Duration
}

// NewTCPSensor creates a new TCP sensor with default targets
func NewTCPSensor() *TCPSensor {
	return &TCPSensor{
		BaseSensor: NewBaseSensor("tcp", SensorTypeBoolean),
		// Use multiple reliable targets for redundancy (IPv4 and IPv6)
		targets: []TCPTarget{
			// Cloudflare DNS IPv4 - very reliable, low latency
			{Host: "1.1.1.1", Port: "443", Network: "tcp4"},
			{Host: "1.0.0.1", Port: "443", Network: "tcp4"},
			// Cloudflare DNS IPv6
			{Host: "2606:4700:4700::1111", Port: "443", Network: "tcp6"},
			{Host: "2606:4700:4700::1001", Port: "443", Network: "tcp6"},
			// Google DNS IPv4 - widely available
			{Host: "8.8.8.8", Port: "443", Network: "tcp4"},
			{Host: "8.8.4.4", Port: "443", Network: "tcp4"},
			// Google DNS IPv6
			{Host: "2001:4860:4860::8888", Port: "443", Network: "tcp6"},
			{Host: "2001:4860:4860::8844", Port: "443", Network: "tcp6"},
		},
		timeout: 5 * time.Second,
	}
}

// Check performs TCP connection tests to determine network connectivity
// Returns true if any target is reachable, false otherwise
func (s *TCPSensor) Check(ctx context.Context) (SensorValue, error) {
	// Create a context with timeout
	ctx, cancel := context.WithTimeout(ctx, s.timeout)
	defer cancel()

	isOnline := false

	// Try each target until one succeeds
	for i, target := range s.targets {
		slog.Debug("Attempting TCP connection",
			"host", target.Host,
			"port", target.Port,
			"network", target.Network,
			"target_num", i+1,
			"total_targets", len(s.targets))

		dialer := net.Dialer{
			Timeout: s.timeout / time.Duration(len(s.targets)), // Divide timeout among targets
		}

		addr := net.JoinHostPort(target.Host, target.Port)
		conn, err := dialer.DialContext(ctx, target.Network, addr)
		if err != nil {
			slog.Debug("TCP connection failed, trying next target",
				"host", target.Host,
				"port", target.Port,
				"error", err)
			continue
		}

		// Success - close connection and mark as online
		conn.Close()
		slog.Debug("TCP connection successful",
			"host", target.Host,
			"port", target.Port)
		isOnline = true
		break
	}

	if !isOnline {
		slog.Debug("All TCP targets failed, marking as offline",
			"total_targets", len(s.targets))
	}

	newValue := NewSensorValue(s.Name(), s.Type(), isOnline)

	// Notify listeners if value changed
	oldValue := s.GetLastValue()
	if oldValue == nil || !oldValue.Equals(newValue) {
		// If this is the first value (oldValue is nil), create a default old value
		if oldValue == nil {
			defaultOld := NewSensorValue(s.Name(), s.Type(), false)
			oldValue = &defaultOld
		}
		s.NotifyListeners(s, *oldValue, newValue)
		s.SetLastValue(newValue)
	}

	return newValue, nil
}

// SetValue is not supported for active sensors
func (s *TCPSensor) SetValue(value interface{}) error {
	return fmt.Errorf("cannot set value on active sensor %s", s.Name())
}
