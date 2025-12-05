package security

import (
	"context"
	"fmt"
	"log/slog"
	"net"
	"strings"
	"time"
)

// IPVersion represents the IP protocol version
type IPVersion string

const (
	IPv4 IPVersion = "ipv4"
	IPv6 IPVersion = "ipv6"
)

// IPResolverConfig defines a DNS resolver configuration for public IP detection
type IPResolverConfig struct {
	ResolverAddr string // DNS server address (FQDN with trailing dot to prevent search domain expansion)
	Hostname     string // Hostname to query (FQDN with trailing dot)
	QueryType    string // "A" for A records, "TXT" for TXT records, "AAAA" for IPv6
}

// IPSensor checks the public IP address using DNS queries to multiple providers
type IPSensor struct {
	*BaseSensor
	resolvers []IPResolverConfig
	timeout   time.Duration
	network   string    // "udp4" for IPv4, "udp6" for IPv6
	version   IPVersion // For logging/identification
	offlineIP string    // IP to return when offline (e.g., "169.254.0.0" for IPv4)
}

// NewIPv4Sensor creates a new public IPv4 sensor using multiple DNS providers
func NewIPv4Sensor() *IPSensor {
	return &IPSensor{
		BaseSensor: NewBaseSensor("public_ipv4", SensorTypeString),
		// Use multiple DNS providers with FQDN (trailing dot prevents search domain expansion)
		resolvers: []IPResolverConfig{
			// OpenDNS (returns A record with your public IP)
			{ResolverAddr: "resolver1.opendns.com.:53", Hostname: "myip.opendns.com.", QueryType: "A"},
			{ResolverAddr: "resolver2.opendns.com.:53", Hostname: "myip.opendns.com.", QueryType: "A"},
			// Google (returns TXT record with your public IP)
			{ResolverAddr: "ns1.google.com.:53", Hostname: "o-o.myaddr.l.google.com.", QueryType: "TXT"},
			// Akamai (returns A record with your public IP)
			{ResolverAddr: "ns1-1.akamaitech.net.:53", Hostname: "whoami.akamai.net.", QueryType: "A"},
		},
		timeout:   15 * time.Second,
		network:   "udp4",
		version:   IPv4,
		offlineIP: "169.254.0.0", // Link-local address indicates offline
	}
}

// NewIPv6Sensor creates a new public IPv6 sensor using multiple DNS providers
func NewIPv6Sensor() *IPSensor {
	return &IPSensor{
		BaseSensor: NewBaseSensor("public_ipv6", SensorTypeString),
		// Use DNS providers that support IPv6
		resolvers: []IPResolverConfig{
			// OpenDNS (supports IPv6)
			{ResolverAddr: "resolver1.opendns.com.:53", Hostname: "myip.opendns.com.", QueryType: "AAAA"},
			{ResolverAddr: "resolver2.opendns.com.:53", Hostname: "myip.opendns.com.", QueryType: "AAAA"},
			// Google (returns TXT record - works over IPv6 connection)
			{ResolverAddr: "ns1.google.com.:53", Hostname: "o-o.myaddr.l.google.com.", QueryType: "TXT"},
		},
		timeout:   15 * time.Second,
		network:   "udp6",
		version:   IPv6,
		offlineIP: "fe80::", // Link-local address indicates no IPv6 connectivity
	}
}

// Check performs a DNS lookup to determine the public IP address
// Returns link-local address (169.254.0.0 for IPv4, fe80:: for IPv6) when the lookup fails
// Tries multiple DNS resolvers for robustness
func (s *IPSensor) Check(ctx context.Context) (SensorValue, error) {
	// Create a context with timeout
	ctx, cancel := context.WithTimeout(ctx, s.timeout)
	defer cancel()

	// Capture network for closure
	network := s.network

	// Try each resolver in order until one succeeds
	for i, config := range s.resolvers {
		// Capture loop variables for closure
		resolverAddr := config.ResolverAddr

		slog.Debug("Querying DNS resolver for public IP",
			"resolver", config.ResolverAddr,
			"hostname", config.Hostname,
			"query_type", config.QueryType,
			"version", s.version,
			"resolver_num", i+1,
			"total_resolvers", len(s.resolvers))

		// Use custom resolver with the appropriate network type
		resolver := &net.Resolver{
			PreferGo: true,
			Dial: func(ctx context.Context, _, _ string) (net.Conn, error) {
				d := net.Dialer{
					Timeout: s.timeout,
				}
				return d.DialContext(ctx, network, resolverAddr)
			},
		}

		var publicIP string
		var err error

		// Query based on record type
		switch config.QueryType {
		case "TXT":
			// TXT record query (e.g., Google)
			var txtRecords []string
			txtRecords, err = resolver.LookupTXT(ctx, config.Hostname)
			if err == nil && len(txtRecords) > 0 {
				// TXT records return the IP as a string, possibly with quotes
				publicIP = strings.Trim(strings.TrimSpace(txtRecords[0]), "\"")
			}
		case "AAAA":
			// AAAA record query for IPv6
			var ips []net.IP
			ips, err = resolver.LookupIP(ctx, "ip6", config.Hostname)
			if err == nil && len(ips) > 0 {
				publicIP = ips[0].String()
			}
		default:
			// A record query (default, e.g., OpenDNS, Akamai)
			var ips []net.IP
			ips, err = resolver.LookupIP(ctx, "ip4", config.Hostname)
			if err == nil && len(ips) > 0 {
				publicIP = ips[0].String()
			}
		}

		if err != nil {
			slog.Debug("DNS resolver query failed, trying next",
				"resolver", config.ResolverAddr,
				"hostname", config.Hostname,
				"version", s.version,
				"error", err)
			continue
		}

		if publicIP == "" {
			slog.Debug("DNS resolver returned empty result, trying next",
				"resolver", config.ResolverAddr,
				"hostname", config.Hostname,
				"version", s.version)
			continue
		}

		// Success! Return the public IP
		slog.Debug("Successfully retrieved public IP",
			"resolver", config.ResolverAddr,
			"hostname", config.Hostname,
			"version", s.version,
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
			s.SetLastValue(newValue)
			s.NotifyListeners(s, *oldValue, newValue)
		}

		return newValue, nil
	}

	// All resolvers failed - return link-local address to indicate no connectivity
	slog.Debug("All DNS resolvers failed for IP version, returning link-local address",
		"version", s.version,
		"total_resolvers", len(s.resolvers),
		"offline_ip", s.offlineIP)
	newValue := NewSensorValue(s.Name(), s.Type(), s.offlineIP)

	// Notify listeners if value changed
	oldValue := s.GetLastValue()
	if oldValue == nil || !oldValue.Equals(newValue) {
		// If this is the first value (oldValue is nil), create a default old value
		if oldValue == nil {
			defaultOld := NewSensorValue(s.Name(), s.Type(), "")
			oldValue = &defaultOld
		}
		s.SetLastValue(newValue)
		s.NotifyListeners(s, *oldValue, newValue)
	}

	return newValue, nil
}

// SetValue is not supported for active sensors
func (s *IPSensor) SetValue(value interface{}) error {
	return fmt.Errorf("cannot set value on active sensor %s", s.Name())
}

// Version returns the IP version this sensor checks
func (s *IPSensor) Version() IPVersion {
	return s.version
}
