package security

import (
	"context"
	"fmt"
	"log/slog"
	"net"
	"sync"
	"time"
)

// TCPTarget defines a host to check for connectivity via TCP
type TCPTarget struct {
	Host    string // Host address (IP or hostname)
	Port    string // Port to connect to (e.g., "443", "80")
	Network string // Network type: "tcp", "tcp4", "tcp6"
}

// TCPSensor checks network connectivity by attempting TCP connections to known hosts
// This takes precedence over the public_ip sensor for determining online status.
// The sensor continuously polls connectivity on its own interval, independent of
// the network monitor, to reliably detect online/offline transitions.
type TCPSensor struct {
	*BaseSensor
	targets       []TCPTarget
	timeout       time.Duration
	pollInterval  time.Duration
	lastCheckTime time.Time   // When we last performed a check
	checkMu       sync.Mutex  // Protects lastCheckTime and prevents concurrent checks
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
		timeout:      5 * time.Second,
		pollInterval: 10 * time.Second, // Check connectivity every 10 seconds
	}
}

// Start begins continuous connectivity monitoring
func (s *TCPSensor) Start(ctx context.Context) {
	go s.pollLoop(ctx)
	slog.Info("TCP sensor started continuous monitoring", "interval", s.pollInterval)
}

// pollLoop continuously checks connectivity and notifies on changes
func (s *TCPSensor) pollLoop(ctx context.Context) {
	ticker := time.NewTicker(s.pollInterval)
	defer ticker.Stop()

	// Do an initial check immediately
	s.Check(ctx)

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			s.Check(ctx)
		}
	}
}

// Check performs TCP connection tests to determine network connectivity
// Returns true if any target is reachable, false otherwise
func (s *TCPSensor) Check(ctx context.Context) (SensorValue, error) {
	// Prevent concurrent checks and deduplicate rapid successive checks
	s.checkMu.Lock()

	// If we checked very recently (within 2 seconds), return cached value
	// This prevents double-checking when both poll loop and checkContext trigger
	minCheckInterval := 2 * time.Second
	if !s.lastCheckTime.IsZero() && time.Since(s.lastCheckTime) < minCheckInterval {
		s.checkMu.Unlock()
		if lastValue := s.GetLastValue(); lastValue != nil {
			return *lastValue, nil
		}
		// No cached value, need to actually check
		s.checkMu.Lock()
	}

	s.lastCheckTime = time.Now()
	s.checkMu.Unlock()

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
		s.SetLastValue(newValue)
		s.NotifyListeners(s, *oldValue, newValue)
	}

	return newValue, nil
}

// SetValue is not supported for active sensors
func (s *TCPSensor) SetValue(value interface{}) error {
	return fmt.Errorf("cannot set value on active sensor %s", s.Name())
}
