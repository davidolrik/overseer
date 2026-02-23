package state

import (
	"fmt"
	"net"
	"testing"
	"time"
)

func boolPtr(b bool) *bool { return &b }

func TestTCPPriorityPolicyName(t *testing.T) {
	p := NewTCPPriorityPolicy()
	if p.Name() != "tcp_priority" {
		t.Errorf("Name() = %q, want %q", p.Name(), "tcp_priority")
	}
}

func TestTCPPriorityPolicyTCPOnline(t *testing.T) {
	p := NewTCPPriorityPolicy()
	readings := map[string]SensorReading{
		"tcp": {
			Sensor:    "tcp",
			Timestamp: time.Now(),
			Online:    boolPtr(true),
		},
	}

	online, source := p.Evaluate(readings)
	if !online {
		t.Error("Expected online=true when TCP reports online")
	}
	if source != "tcp" {
		t.Errorf("Expected source=%q, got %q", "tcp", source)
	}
}

func TestTCPPriorityPolicyTCPOffline(t *testing.T) {
	p := NewTCPPriorityPolicy()
	readings := map[string]SensorReading{
		"tcp": {
			Sensor:    "tcp",
			Timestamp: time.Now(),
			Online:    boolPtr(false),
		},
	}

	online, source := p.Evaluate(readings)
	if online {
		t.Error("Expected online=false when TCP reports offline")
	}
	if source != "tcp" {
		t.Errorf("Expected source=%q, got %q", "tcp", source)
	}
}

func TestTCPPriorityPolicyFallbackToIPv4(t *testing.T) {
	p := NewTCPPriorityPolicy()
	readings := map[string]SensorReading{
		"public_ipv4": {
			Sensor:    "public_ipv4",
			Timestamp: time.Now(),
			IP:        net.ParseIP("8.8.8.8"),
		},
	}

	online, source := p.Evaluate(readings)
	if !online {
		t.Error("Expected online=true with valid public IPv4")
	}
	if source != "public_ipv4" {
		t.Errorf("Expected source=%q, got %q", "public_ipv4", source)
	}
}

func TestTCPPriorityPolicyLinkLocalIPv4Offline(t *testing.T) {
	p := NewTCPPriorityPolicy()
	readings := map[string]SensorReading{
		"public_ipv4": {
			Sensor:    "public_ipv4",
			Timestamp: time.Now(),
			IP:        net.ParseIP("169.254.0.0"),
		},
	}

	online, _ := p.Evaluate(readings)
	if online {
		t.Error("Expected online=false with link-local IPv4 (169.254.0.0)")
	}
}

func TestTCPPriorityPolicyFallbackToIPv6(t *testing.T) {
	p := NewTCPPriorityPolicy()
	readings := map[string]SensorReading{
		"public_ipv6": {
			Sensor:    "public_ipv6",
			Timestamp: time.Now(),
			IP:        net.ParseIP("2001:db8::1"),
		},
	}

	online, source := p.Evaluate(readings)
	if !online {
		t.Error("Expected online=true with valid public IPv6")
	}
	if source != "public_ipv6" {
		t.Errorf("Expected source=%q, got %q", "public_ipv6", source)
	}
}

func TestTCPPriorityPolicyNoSensors(t *testing.T) {
	p := NewTCPPriorityPolicy()
	readings := map[string]SensorReading{}

	online, source := p.Evaluate(readings)
	if online {
		t.Error("Expected online=false with no sensor readings")
	}
	if source != "none" {
		t.Errorf("Expected source=%q, got %q", "none", source)
	}
}

func TestTCPPriorityPolicyStaleTCPFallback(t *testing.T) {
	p := NewTCPPriorityPolicy()
	p.TCPTimeout = 1 * time.Second

	readings := map[string]SensorReading{
		"tcp": {
			Sensor:    "tcp",
			Timestamp: time.Now().Add(-2 * time.Second), // Stale
			Online:    boolPtr(true),
		},
		"public_ipv4": {
			Sensor:    "public_ipv4",
			Timestamp: time.Now(),
			IP:        net.ParseIP("8.8.8.8"),
		},
	}

	online, source := p.Evaluate(readings)
	if !online {
		t.Error("Expected online=true from IPv4 fallback")
	}
	if source != "public_ipv4" {
		t.Errorf("Expected source=%q (stale TCP should fall through), got %q", "public_ipv4", source)
	}
}

func TestTCPPriorityPolicyIPv4FromValue(t *testing.T) {
	p := NewTCPPriorityPolicy()
	readings := map[string]SensorReading{
		"public_ipv4": {
			Sensor:    "public_ipv4",
			Timestamp: time.Now(),
			Value:     "8.8.8.8",
		},
	}

	online, source := p.Evaluate(readings)
	if !online {
		t.Error("Expected online=true with IPv4 from Value field")
	}
	if source != "public_ipv4" {
		t.Errorf("Expected source=%q, got %q", "public_ipv4", source)
	}
}

func TestTCPPriorityPolicyTCPWithErrorIgnored(t *testing.T) {
	p := NewTCPPriorityPolicy()
	readings := map[string]SensorReading{
		"tcp": {
			Sensor:    "tcp",
			Timestamp: time.Now(),
			Online:    boolPtr(true),
			Error:     fmt.Errorf("connection failed"),
		},
		"public_ipv4": {
			Sensor:    "public_ipv4",
			Timestamp: time.Now(),
			IP:        net.ParseIP("8.8.8.8"),
		},
	}

	online, source := p.Evaluate(readings)
	if !online {
		t.Error("Expected online=true from IPv4 (TCP has error)")
	}
	if source != "public_ipv4" {
		t.Errorf("Expected source=%q (TCP with error should be skipped), got %q", "public_ipv4", source)
	}
}

func TestAnyOnlinePolicyName(t *testing.T) {
	p := NewAnyOnlinePolicy()
	if p.Name() != "any_online" {
		t.Errorf("Name() = %q, want %q", p.Name(), "any_online")
	}
}

func TestAnyOnlinePolicyTCPOnline(t *testing.T) {
	p := NewAnyOnlinePolicy()
	readings := map[string]SensorReading{
		"tcp": {
			Sensor:    "tcp",
			Timestamp: time.Now(),
			Online:    boolPtr(true),
		},
	}

	online, source := p.Evaluate(readings)
	if !online {
		t.Error("Expected online=true when TCP reports online")
	}
	if source != "tcp" {
		t.Errorf("Expected source=%q, got %q", "tcp", source)
	}
}

func TestAnyOnlinePolicyAllOffline(t *testing.T) {
	p := NewAnyOnlinePolicy()
	readings := map[string]SensorReading{
		"tcp": {
			Sensor:    "tcp",
			Timestamp: time.Now(),
			Online:    boolPtr(false),
		},
		"public_ipv4": {
			Sensor:    "public_ipv4",
			Timestamp: time.Now(),
			IP:        net.ParseIP("169.254.0.0"),
		},
	}

	online, _ := p.Evaluate(readings)
	if online {
		t.Error("Expected online=false when all sensors report offline")
	}
}

func TestAnyOnlinePolicyIPv4Online(t *testing.T) {
	p := NewAnyOnlinePolicy()
	readings := map[string]SensorReading{
		"tcp": {
			Sensor:    "tcp",
			Timestamp: time.Now(),
			Online:    boolPtr(false),
		},
		"public_ipv4": {
			Sensor:    "public_ipv4",
			Timestamp: time.Now(),
			IP:        net.ParseIP("1.2.3.4"),
		},
	}

	online, source := p.Evaluate(readings)
	if !online {
		t.Error("Expected online=true when IPv4 has valid public IP")
	}
	if source != "public_ipv4" {
		t.Errorf("Expected source=%q, got %q", "public_ipv4", source)
	}
}

func TestAnyOnlinePolicyIPv6Online(t *testing.T) {
	p := NewAnyOnlinePolicy()
	readings := map[string]SensorReading{
		"public_ipv6": {
			Sensor:    "public_ipv6",
			Timestamp: time.Now(),
			IP:        net.ParseIP("2001:db8::1"),
		},
	}

	online, source := p.Evaluate(readings)
	if !online {
		t.Error("Expected online=true when IPv6 has valid address")
	}
	if source != "public_ipv6" {
		t.Errorf("Expected source=%q, got %q", "public_ipv6", source)
	}
}

func TestHysteresisName(t *testing.T) {
	inner := NewTCPPriorityPolicy()
	p := NewHysteresisPolicy(inner, 3, 2)
	want := "hysteresis(tcp_priority)"
	if p.Name() != want {
		t.Errorf("Name() = %q, want %q", p.Name(), want)
	}
}

func TestHysteresisRequiresConsecutiveOnline(t *testing.T) {
	inner := NewTCPPriorityPolicy()
	p := NewHysteresisPolicy(inner, 3, 2)

	onlineReadings := map[string]SensorReading{
		"tcp": {Sensor: "tcp", Timestamp: time.Now(), Online: boolPtr(true)},
	}

	// First online reading: not enough yet (threshold=2)
	online, _ := p.Evaluate(onlineReadings)
	if online {
		t.Error("Should not be online after 1 consecutive reading (threshold=2)")
	}

	// Second online reading: reaches threshold
	online, _ = p.Evaluate(onlineReadings)
	if !online {
		t.Error("Should be online after 2 consecutive readings (threshold=2)")
	}
}

func TestHysteresisRequiresConsecutiveOffline(t *testing.T) {
	inner := NewTCPPriorityPolicy()
	p := NewHysteresisPolicy(inner, 3, 1)

	onlineReadings := map[string]SensorReading{
		"tcp": {Sensor: "tcp", Timestamp: time.Now(), Online: boolPtr(true)},
	}
	offlineReadings := map[string]SensorReading{
		"tcp": {Sensor: "tcp", Timestamp: time.Now(), Online: boolPtr(false)},
	}

	// Go online first (threshold=1)
	p.Evaluate(onlineReadings)

	// First offline reading: not enough (threshold=3)
	online, _ := p.Evaluate(offlineReadings)
	if !online {
		t.Error("Should still be online after 1 offline reading (threshold=3)")
	}

	// Second offline reading: still not enough
	online, _ = p.Evaluate(offlineReadings)
	if !online {
		t.Error("Should still be online after 2 offline readings (threshold=3)")
	}

	// Third offline reading: reaches threshold
	online, _ = p.Evaluate(offlineReadings)
	if online {
		t.Error("Should be offline after 3 consecutive offline readings (threshold=3)")
	}
}

func TestHysteresisOnlineReadingResetsOfflineCounter(t *testing.T) {
	inner := NewTCPPriorityPolicy()
	p := NewHysteresisPolicy(inner, 3, 1)

	onlineReadings := map[string]SensorReading{
		"tcp": {Sensor: "tcp", Timestamp: time.Now(), Online: boolPtr(true)},
	}
	offlineReadings := map[string]SensorReading{
		"tcp": {Sensor: "tcp", Timestamp: time.Now(), Online: boolPtr(false)},
	}

	// Go online first
	p.Evaluate(onlineReadings)

	// Two offline readings (threshold=3)
	p.Evaluate(offlineReadings)
	p.Evaluate(offlineReadings)

	// One online reading resets counter
	p.Evaluate(onlineReadings)

	// Two more offline readings: should NOT go offline (counter was reset)
	online, _ := p.Evaluate(offlineReadings)
	if !online {
		t.Error("Counter should have been reset by online reading")
	}

	online, _ = p.Evaluate(offlineReadings)
	if !online {
		t.Error("Should need 3 consecutive offline readings, but counter was reset")
	}

	// Third consecutive offline reading from reset point
	online, _ = p.Evaluate(offlineReadings)
	if online {
		t.Error("Should be offline after 3 consecutive offline readings")
	}
}

func TestHysteresisReset(t *testing.T) {
	inner := NewTCPPriorityPolicy()
	p := NewHysteresisPolicy(inner, 3, 2)

	onlineReadings := map[string]SensorReading{
		"tcp": {Sensor: "tcp", Timestamp: time.Now(), Online: boolPtr(true)},
	}

	// One online reading (threshold=2)
	p.Evaluate(onlineReadings)

	// Reset clears counters
	p.Reset()

	// Need 2 more consecutive readings after reset
	online, _ := p.Evaluate(onlineReadings)
	if online {
		t.Error("Should not be online after reset + 1 reading")
	}

	online, _ = p.Evaluate(onlineReadings)
	if !online {
		t.Error("Should be online after reset + 2 consecutive readings")
	}
}
