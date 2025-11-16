package security

import (
	"context"
	"fmt"
	"log/slog"
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
		// Use OpenDNS resolvers with FQDN (trailing dot prevents search domain expansion)
		// If DNS resolution fails, we're offline anyway and will return 169.254.0.0
		resolvers: []string{
			"resolver1.opendns.com.:53",
			"resolver2.opendns.com.:53",
		},
		// IMPORTANT: Use FQDN with trailing dot to prevent search domain expansion
		// Without the dot, "myip.opendns.com" could be expanded to "myip.opendns.com.<search-domain>"
		// which might match wildcard DNS records, which could give the wrong ip
		hostname: "myip.opendns.com.",
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
	for i, resolverAddr := range s.resolvers {
		slog.Debug("Querying OpenDNS resolver for public IP",
			"resolver", resolverAddr,
			"resolver_num", i+1,
			"total_resolvers", len(s.resolvers))

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
			slog.Debug("OpenDNS resolver query failed, trying next",
				"resolver", resolverAddr,
				"error", err)
			// Try next resolver
			continue
		}

		if len(ips) == 0 {
			slog.Debug("OpenDNS resolver returned no IPs, trying next",
				"resolver", resolverAddr)
			// Try next resolver
			continue
		}

		// Success! Return the first IP address (OpenDNS returns our public IP)
		publicIP := strings.TrimSpace(ips[0])
		slog.Debug("Successfully retrieved public IP from OpenDNS",
			"resolver", resolverAddr,
			"public_ip", publicIP)
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
	slog.Debug("All OpenDNS resolvers failed, returning link-local address (offline)",
		"total_resolvers", len(s.resolvers),
		"link_local_ip", "169.254.0.0")
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
