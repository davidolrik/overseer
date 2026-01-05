package state

import (
	"net"
	"time"
)

// OnlinePolicy determines the online state from sensor readings.
// Different policies can implement different strategies for combining
// multiple sensor inputs into a single online/offline decision.
type OnlinePolicy interface {
	// Evaluate takes all current sensor readings and returns the online state
	// along with the source sensor that determined this state.
	Evaluate(readings map[string]SensorReading) (online bool, source string)

	// Name returns the policy name for logging
	Name() string
}

// TCPPriorityPolicy determines online status with TCP sensor taking precedence.
// This is the default policy that mirrors the current overseer behavior:
// - If TCP sensor has reported, use its result
// - Fall back to IP sensor if TCP hasn't reported
// - Consider offline if IP is link-local (169.254.x.x or fe80::)
type TCPPriorityPolicy struct {
	// TCPTimeout is how long to wait for TCP before falling back to IP
	// If zero, TCP always takes precedence when available
	TCPTimeout time.Duration

	// OfflineIPv4 is the IP that indicates offline status (default: 169.254.0.0)
	OfflineIPv4 string

	// OfflineIPv6 is the IPv6 that indicates offline status (default: fe80::)
	OfflineIPv6 string
}

// NewTCPPriorityPolicy creates a new TCP priority policy with defaults
func NewTCPPriorityPolicy() *TCPPriorityPolicy {
	return &TCPPriorityPolicy{
		OfflineIPv4: "169.254.0.0",
		OfflineIPv6: "fe80::",
	}
}

func (p *TCPPriorityPolicy) Name() string {
	return "tcp_priority"
}

func (p *TCPPriorityPolicy) Evaluate(readings map[string]SensorReading) (online bool, source string) {
	// Check TCP sensor first (highest priority)
	if tcp, ok := readings["tcp"]; ok && tcp.Online != nil && tcp.Error == nil {
		// If TCPTimeout is set, check if TCP reading is fresh enough
		if p.TCPTimeout > 0 && time.Since(tcp.Timestamp) > p.TCPTimeout {
			// TCP reading is stale, fall through to IP check
		} else {
			return *tcp.Online, "tcp"
		}
	}

	// Fall back to IPv4 sensor
	if ipv4, ok := readings["public_ipv4"]; ok && ipv4.Error == nil {
		ip := ipv4.IP
		if ip == nil && ipv4.Value != "" {
			ip = net.ParseIP(ipv4.Value)
		}
		if ip != nil {
			isOnline := ip.String() != p.OfflineIPv4 && !ip.IsLinkLocalUnicast()
			return isOnline, "public_ipv4"
		}
	}

	// Fall back to IPv6 sensor
	if ipv6, ok := readings["public_ipv6"]; ok && ipv6.Error == nil {
		ip := ipv6.IP
		if ip == nil && ipv6.Value != "" {
			ip = net.ParseIP(ipv6.Value)
		}
		if ip != nil {
			isOnline := ip.String() != p.OfflineIPv6 && !ip.IsLinkLocalUnicast()
			return isOnline, "public_ipv6"
		}
	}

	// No sensors have reported - assume offline
	return false, "none"
}

// AnyOnlinePolicy considers the system online if ANY sensor reports online.
// Useful for environments with intermittent connectivity where any
// successful probe should be considered a positive signal.
type AnyOnlinePolicy struct {
	OfflineIPv4 string
	OfflineIPv6 string
}

func NewAnyOnlinePolicy() *AnyOnlinePolicy {
	return &AnyOnlinePolicy{
		OfflineIPv4: "169.254.0.0",
		OfflineIPv6: "fe80::",
	}
}

func (p *AnyOnlinePolicy) Name() string {
	return "any_online"
}

func (p *AnyOnlinePolicy) Evaluate(readings map[string]SensorReading) (online bool, source string) {
	// Check TCP sensor
	if tcp, ok := readings["tcp"]; ok && tcp.Online != nil && tcp.Error == nil && *tcp.Online {
		return true, "tcp"
	}

	// Check IPv4 sensor
	if ipv4, ok := readings["public_ipv4"]; ok && ipv4.Error == nil {
		ip := ipv4.IP
		if ip == nil && ipv4.Value != "" {
			ip = net.ParseIP(ipv4.Value)
		}
		if ip != nil && ip.String() != p.OfflineIPv4 && !ip.IsLinkLocalUnicast() {
			return true, "public_ipv4"
		}
	}

	// Check IPv6 sensor
	if ipv6, ok := readings["public_ipv6"]; ok && ipv6.Error == nil {
		ip := ipv6.IP
		if ip == nil && ipv6.Value != "" {
			ip = net.ParseIP(ipv6.Value)
		}
		if ip != nil && ip.String() != p.OfflineIPv6 && !ip.IsLinkLocalUnicast() {
			return true, "public_ipv6"
		}
	}

	return false, "none"
}

// HysteresisPolicy wraps another policy and requires N consecutive
// readings before changing state. This prevents flapping on transient
// network issues.
type HysteresisPolicy struct {
	// Inner is the underlying policy to use for evaluation
	Inner OnlinePolicy

	// OfflineThreshold is how many consecutive offline readings required
	// before transitioning from online to offline
	OfflineThreshold int

	// OnlineThreshold is how many consecutive online readings required
	// before transitioning from offline to online
	OnlineThreshold int

	// State tracking
	currentState       bool
	consecutiveOnline  int
	consecutiveOffline int
}

func NewHysteresisPolicy(inner OnlinePolicy, offlineThreshold, onlineThreshold int) *HysteresisPolicy {
	return &HysteresisPolicy{
		Inner:            inner,
		OfflineThreshold: offlineThreshold,
		OnlineThreshold:  onlineThreshold,
	}
}

func (p *HysteresisPolicy) Name() string {
	return "hysteresis(" + p.Inner.Name() + ")"
}

func (p *HysteresisPolicy) Evaluate(readings map[string]SensorReading) (online bool, source string) {
	// Get the inner policy's evaluation
	innerOnline, innerSource := p.Inner.Evaluate(readings)

	// Update consecutive counters
	if innerOnline {
		p.consecutiveOnline++
		p.consecutiveOffline = 0
	} else {
		p.consecutiveOffline++
		p.consecutiveOnline = 0
	}

	// Check if we should transition
	if p.currentState {
		// Currently online - need OfflineThreshold consecutive offline readings to go offline
		if p.consecutiveOffline >= p.OfflineThreshold {
			p.currentState = false
		}
	} else {
		// Currently offline - need OnlineThreshold consecutive online readings to go online
		if p.consecutiveOnline >= p.OnlineThreshold {
			p.currentState = true
		}
	}

	return p.currentState, innerSource
}

// Reset resets the hysteresis state (useful after config reload)
func (p *HysteresisPolicy) Reset() {
	p.consecutiveOnline = 0
	p.consecutiveOffline = 0
}
