package security

import (
	"context"
	"fmt"
	"net"
	"strings"
	"time"
)

// IPSensor checks the public IP address using DNS queries to OpenDNS
type IPSensor struct {
	*BaseSensor
	resolvers []string
	hostname  string
	timeout   time.Duration
}

// NewIPSensor creates a new public IP sensor using OpenDNS
func NewIPSensor() *IPSensor {
	return &IPSensor{
		BaseSensor: NewBaseSensor("public_ip", SensorTypeString),
		resolvers: []string{
			"resolver1.opendns.com:53",
			"resolver2.opendns.com:53",
		},
		hostname: "myip.opendns.com",
		timeout:  15 * time.Second,
	}
}

// Check performs a DNS lookup to determine the public IP address
// Returns 169.254.0.0 (link-local) when the lookup fails
// Tries multiple OpenDNS resolvers for robustness
func (s *IPSensor) Check(ctx context.Context) (SensorValue, error) {
	// Create a context with timeout
	ctx, cancel := context.WithTimeout(ctx, s.timeout)
	defer cancel()

	// Try each resolver in order until one succeeds
	for _, resolverAddr := range s.resolvers {
		// Use custom resolver
		resolver := &net.Resolver{
			PreferGo: true,
			Dial: func(ctx context.Context, network, address string) (net.Conn, error) {
				d := net.Dialer{
					Timeout: s.timeout,
				}
				return d.DialContext(ctx, "udp", resolverAddr)
			},
		}

		// Lookup the hostname using the custom resolver
		ips, err := resolver.LookupHost(ctx, s.hostname)
		if err != nil {
			// Try next resolver
			continue
		}

		if len(ips) == 0 {
			// Try next resolver
			continue
		}

		// Success! Return the first IP address (OpenDNS returns our public IP)
		publicIP := strings.TrimSpace(ips[0])
		newValue := NewSensorValue(s.Name(), s.Type(), publicIP)

		// Notify listeners if value changed
		oldValue := s.GetLastValue()
		if oldValue == nil || !oldValue.Equals(newValue) {
			// If this is the first value (oldValue is nil), create a default old value
			if oldValue == nil {
				defaultOld := NewSensorValue(s.Name(), s.Type(), "")
				oldValue = &defaultOld
			}
			s.NotifyListeners(s, *oldValue, newValue)
			s.SetLastValue(newValue)
		}

		return newValue, nil
	}

	// All resolvers failed - return link-local address to indicate no network connectivity
	newValue := NewSensorValue(s.Name(), s.Type(), "169.254.0.0")

	// Notify listeners if value changed
	oldValue := s.GetLastValue()
	if oldValue == nil || !oldValue.Equals(newValue) {
		// If this is the first value (oldValue is nil), create a default old value
		if oldValue == nil {
			defaultOld := NewSensorValue(s.Name(), s.Type(), "")
			oldValue = &defaultOld
		}
		s.NotifyListeners(s, *oldValue, newValue)
		s.SetLastValue(newValue)
	}

	return newValue, nil
}

// SetValue is not supported for active sensors
func (s *IPSensor) SetValue(value interface{}) error {
	return fmt.Errorf("cannot set value on active sensor %s", s.Name())
}
