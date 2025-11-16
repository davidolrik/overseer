package security

import (
	"context"
	"fmt"
	"log/slog"
	"net"
	"strings"
	"time"
)

// IPResolverConfig defines a DNS resolver configuration for public IP detection
type IPResolverConfig struct {
	ResolverAddr string // DNS server address (FQDN with trailing dot to prevent search domain expansion)
	Hostname     string // Hostname to query (FQDN with trailing dot)
	QueryType    string // "A" for A records, "TXT" for TXT records
}

// IPSensor checks the public IP address using DNS queries to multiple providers
type IPSensor struct {
	*BaseSensor
	resolvers []IPResolverConfig
	timeout   time.Duration
}

// NewIPSensor creates a new public IP sensor using multiple DNS providers
func NewIPSensor() *IPSensor {
	return &IPSensor{
		BaseSensor: NewBaseSensor("public_ip", SensorTypeString),
		// Use multiple DNS providers with FQDN (trailing dot prevents search domain expansion)
		// If DNS resolution fails, we're offline anyway and will return 169.254.0.0
		resolvers: []IPResolverConfig{
			// OpenDNS (returns A record with your public IP)
			{ResolverAddr: "resolver1.opendns.com.:53", Hostname: "myip.opendns.com.", QueryType: "A"},
			{ResolverAddr: "resolver2.opendns.com.:53", Hostname: "myip.opendns.com.", QueryType: "A"},
			// Google (returns TXT record with your public IP)
			{ResolverAddr: "ns1.google.com.:53", Hostname: "o-o.myaddr.l.google.com.", QueryType: "TXT"},
			// Akamai (returns A record with your public IP)
			{ResolverAddr: "ns1-1.akamaitech.net.:53", Hostname: "whoami.akamai.net.", QueryType: "A"},
		},
		timeout: 15 * time.Second,
	}
}

// Check performs a DNS lookup to determine the public IP address
// Returns 169.254.0.0 (link-local) when the lookup fails
// Tries multiple DNS resolvers for robustness
func (s *IPSensor) Check(ctx context.Context) (SensorValue, error) {
	// Create a context with timeout
	ctx, cancel := context.WithTimeout(ctx, s.timeout)
	defer cancel()

	// Try each resolver in order until one succeeds
	for i, config := range s.resolvers {
		slog.Debug("Querying DNS resolver for public IP",
			"resolver", config.ResolverAddr,
			"hostname", config.Hostname,
			"query_type", config.QueryType,
			"resolver_num", i+1,
			"total_resolvers", len(s.resolvers))

		// Use custom resolver
		resolver := &net.Resolver{
			PreferGo: true,
			Dial: func(ctx context.Context, network, address string) (net.Conn, error) {
				d := net.Dialer{
					Timeout: s.timeout,
				}
				return d.DialContext(ctx, "udp", config.ResolverAddr)
			},
		}

		var publicIP string
		var err error

		// Query based on record type
		if config.QueryType == "TXT" {
			// TXT record query (e.g., Google)
			var txtRecords []string
			txtRecords, err = resolver.LookupTXT(ctx, config.Hostname)
			if err == nil && len(txtRecords) > 0 {
				// TXT records return the IP as a string, possibly with quotes
				publicIP = strings.Trim(strings.TrimSpace(txtRecords[0]), "\"")
			}
		} else {
			// A record query (default, e.g., OpenDNS, Akamai)
			var ips []string
			ips, err = resolver.LookupHost(ctx, config.Hostname)
			if err == nil && len(ips) > 0 {
				publicIP = strings.TrimSpace(ips[0])
			}
		}

		if err != nil {
			slog.Debug("DNS resolver query failed, trying next",
				"resolver", config.ResolverAddr,
				"hostname", config.Hostname,
				"error", err)
			continue
		}

		if publicIP == "" {
			slog.Debug("DNS resolver returned empty result, trying next",
				"resolver", config.ResolverAddr,
				"hostname", config.Hostname)
			continue
		}

		// Success! Return the public IP
		slog.Debug("Successfully retrieved public IP",
			"resolver", config.ResolverAddr,
			"hostname", config.Hostname,
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
	slog.Debug("All DNS resolvers failed, returning link-local address (offline)",
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
