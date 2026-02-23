package state

import (
	"context"
	"log/slog"
	"os"
	"testing"
)

func TestEnvProbe_Check_ReadsSetEnvVar(t *testing.T) {
	// Set a test env var
	testVar := "TEST_ENV_PROBE_VAR"
	testValue := "test-value-123"
	os.Setenv(testVar, testValue)
	defer os.Unsetenv(testVar)

	probe := NewEnvProbe(testVar)

	if probe.Name() != "env:"+testVar {
		t.Errorf("expected name %q, got %q", "env:"+testVar, probe.Name())
	}

	reading := probe.Check(context.Background())

	if reading.Sensor != "env:"+testVar {
		t.Errorf("expected sensor %q, got %q", "env:"+testVar, reading.Sensor)
	}

	if reading.Value != testValue {
		t.Errorf("expected value %q, got %q", testValue, reading.Value)
	}

	if reading.Timestamp.IsZero() {
		t.Error("expected non-zero timestamp")
	}
}

func TestEnvProbe_Check_ReturnsEmptyForUnsetVar(t *testing.T) {
	// Use a var name that definitely doesn't exist
	testVar := "OVERSEER_TEST_NONEXISTENT_VAR_12345"
	os.Unsetenv(testVar)

	probe := NewEnvProbe(testVar)
	reading := probe.Check(context.Background())

	if reading.Value != "" {
		t.Errorf("expected empty value for unset var, got %q", reading.Value)
	}

	if reading.Sensor != "env:"+testVar {
		t.Errorf("expected sensor %q, got %q", "env:"+testVar, reading.Sensor)
	}
}

func TestCollectEnvSensors_FromRuleConditions(t *testing.T) {
	rules := []Rule{
		{
			Name: "test-rule",
			Condition: NewAllCondition(
				NewSensorCondition("env:SSH_CONNECTION", "*"),
				NewSensorCondition("public_ipv4", "1.2.3.4"),
			),
		},
	}
	locations := map[string]Location{}

	envVars := CollectEnvSensors(rules, locations)

	if len(envVars) != 1 {
		t.Fatalf("expected 1 env var, got %d", len(envVars))
	}

	if envVars[0] != "SSH_CONNECTION" {
		t.Errorf("expected SSH_CONNECTION, got %s", envVars[0])
	}
}

func TestCollectEnvSensors_FromLocationConditions(t *testing.T) {
	rules := []Rule{}
	locations := map[string]Location{
		"remote": {
			Name: "remote",
			Condition: NewSensorCondition("env:SSH_TTY", "*"),
		},
	}

	envVars := CollectEnvSensors(rules, locations)

	if len(envVars) != 1 {
		t.Fatalf("expected 1 env var, got %d", len(envVars))
	}

	if envVars[0] != "SSH_TTY" {
		t.Errorf("expected SSH_TTY, got %s", envVars[0])
	}
}

func TestCollectEnvSensors_FromConditionsMap(t *testing.T) {
	rules := []Rule{
		{
			Name: "test-rule",
			Conditions: map[string][]string{
				"env:DISPLAY": {"*"},
			},
		},
	}
	locations := map[string]Location{}

	envVars := CollectEnvSensors(rules, locations)

	if len(envVars) != 1 {
		t.Fatalf("expected 1 env var, got %d", len(envVars))
	}

	if envVars[0] != "DISPLAY" {
		t.Errorf("expected DISPLAY, got %s", envVars[0])
	}
}

func TestCollectEnvSensors_MultipleEnvVars(t *testing.T) {
	rules := []Rule{
		{
			Name: "rule1",
			Condition: NewSensorCondition("env:VAR1", "*"),
		},
	}
	locations := map[string]Location{
		"loc1": {
			Name: "loc1",
			Condition: NewAllCondition(
				NewSensorCondition("env:VAR2", "*"),
				NewSensorCondition("env:VAR3", "value"),
			),
		},
	}

	envVars := CollectEnvSensors(rules, locations)

	if len(envVars) != 3 {
		t.Fatalf("expected 3 env vars, got %d", len(envVars))
	}

	// Check all three are present (order not guaranteed)
	found := make(map[string]bool)
	for _, v := range envVars {
		found[v] = true
	}

	for _, expected := range []string{"VAR1", "VAR2", "VAR3"} {
		if !found[expected] {
			t.Errorf("expected to find %s in env vars", expected)
		}
	}
}

func TestCollectEnvSensors_NoEnvConditions(t *testing.T) {
	rules := []Rule{
		{
			Name: "test-rule",
			Condition: NewSensorCondition("public_ipv4", "1.2.3.4"),
		},
	}
	locations := map[string]Location{}

	envVars := CollectEnvSensors(rules, locations)

	if len(envVars) != 0 {
		t.Errorf("expected 0 env vars, got %d", len(envVars))
	}
}

func TestCollectEnvSensors_Deduplication(t *testing.T) {
	rules := []Rule{
		{
			Name: "rule1",
			Condition: NewSensorCondition("env:SAME_VAR", "*"),
		},
		{
			Name: "rule2",
			Condition: NewSensorCondition("env:SAME_VAR", "specific"),
		},
	}
	locations := map[string]Location{}

	envVars := CollectEnvSensors(rules, locations)

	if len(envVars) != 1 {
		t.Errorf("expected 1 unique env var, got %d", len(envVars))
	}
}

func TestDefaultTCPTargets(t *testing.T) {
	targets := DefaultTCPTargets()

	if len(targets) == 0 {
		t.Fatal("expected non-empty default TCP targets")
	}

	// Should include both IPv4 and IPv6 targets
	hasIPv4 := false
	hasIPv6 := false
	for _, target := range targets {
		if target.Network == "tcp4" {
			hasIPv4 = true
		}
		if target.Network == "tcp6" {
			hasIPv6 = true
		}
		// Every target should have host, port, and network
		if target.Host == "" {
			t.Error("target has empty Host")
		}
		if target.Port == "" {
			t.Error("target has empty Port")
		}
		if target.Network == "" {
			t.Error("target has empty Network")
		}
	}

	if !hasIPv4 {
		t.Error("expected at least one tcp4 target")
	}
	if !hasIPv6 {
		t.Error("expected at least one tcp6 target")
	}
}

func TestNewTCPProbe(t *testing.T) {
	t.Run("with nil logger", func(t *testing.T) {
		probe := NewTCPProbe(nil, nil)
		if probe == nil {
			t.Fatal("expected non-nil probe")
		}
		if probe.Name() != "tcp" {
			t.Errorf("expected name='tcp', got %q", probe.Name())
		}
		if len(probe.targets) == 0 {
			t.Error("expected default targets to be populated")
		}
	})

	t.Run("with logger and sleep monitor", func(t *testing.T) {
		logger := slog.Default()
		sm := NewSleepMonitor(logger, nil, nil)
		probe := NewTCPProbe(logger, sm)
		if probe.sleepMonitor != sm {
			t.Error("expected sleep monitor to be set")
		}
	})
}

func TestNewIPv4Probe(t *testing.T) {
	probe := NewIPv4Probe(nil)
	if probe == nil {
		t.Fatal("expected non-nil probe")
	}
	if probe.Name() != "public_ipv4" {
		t.Errorf("expected name='public_ipv4', got %q", probe.Name())
	}
	if probe.network != "udp4" {
		t.Errorf("expected network='udp4', got %q", probe.network)
	}
	if probe.offlineIP != "169.254.0.0" {
		t.Errorf("expected offlineIP='169.254.0.0', got %q", probe.offlineIP)
	}
	if len(probe.httpURLs) == 0 {
		t.Error("expected HTTP URLs to be populated")
	}
	if len(probe.resolvers) == 0 {
		t.Error("expected DNS resolvers to be populated")
	}
	if probe.prefixBits != 0 {
		t.Errorf("expected prefixBits=0 for IPv4, got %d", probe.prefixBits)
	}
}

func TestNewIPv6Probe(t *testing.T) {
	probe := NewIPv6Probe(nil)
	if probe == nil {
		t.Fatal("expected non-nil probe")
	}
	if probe.Name() != "public_ipv6" {
		t.Errorf("expected name='public_ipv6', got %q", probe.Name())
	}
	if probe.network != "udp6" {
		t.Errorf("expected network='udp6', got %q", probe.network)
	}
	if probe.offlineIP != "fe80::" {
		t.Errorf("expected offlineIP='fe80::', got %q", probe.offlineIP)
	}
	if probe.prefixBits != 64 {
		t.Errorf("expected prefixBits=64 for IPv6, got %d", probe.prefixBits)
	}
}

func TestIPProbe_normalizeToPrefix(t *testing.T) {
	t.Run("IPv6 with /64 prefix", func(t *testing.T) {
		probe := NewIPv6Probe(nil)
		result := probe.normalizeToPrefix("2a05:f6c3:dd4d:0:1234:5678:9abc:def0")
		// Should strip the host part and keep only the /64 prefix
		if result != "2a05:f6c3:dd4d::" {
			t.Errorf("expected '2a05:f6c3:dd4d::', got %q", result)
		}
	})

	t.Run("IPv4 with no prefix configured", func(t *testing.T) {
		probe := NewIPv4Probe(nil)
		// prefixBits is 0 for IPv4 probe, so it should return the IP unchanged
		result := probe.normalizeToPrefix("1.2.3.4")
		if result != "1.2.3.4" {
			t.Errorf("expected '1.2.3.4' (unchanged), got %q", result)
		}
	})

	t.Run("invalid IP returned as-is", func(t *testing.T) {
		probe := NewIPv6Probe(nil)
		result := probe.normalizeToPrefix("not-an-ip")
		if result != "not-an-ip" {
			t.Errorf("expected 'not-an-ip' (unchanged), got %q", result)
		}
	})

	t.Run("IPv6 loopback", func(t *testing.T) {
		probe := NewIPv6Probe(nil)
		result := probe.normalizeToPrefix("::1")
		if result != "::" {
			t.Errorf("expected '::', got %q", result)
		}
	})
}

func TestIPProbe_applyHysteresis(t *testing.T) {
	t.Run("first IP accepted immediately", func(t *testing.T) {
		probe := NewIPv4Probe(nil)
		result := probe.applyHysteresis("1.2.3.4")
		if result != "1.2.3.4" {
			t.Errorf("expected '1.2.3.4', got %q", result)
		}
	})

	t.Run("same IP returns stable", func(t *testing.T) {
		probe := NewIPv4Probe(nil)
		probe.applyHysteresis("1.2.3.4") // Set stable
		result := probe.applyHysteresis("1.2.3.4")
		if result != "1.2.3.4" {
			t.Errorf("expected '1.2.3.4', got %q", result)
		}
	})

	t.Run("single different reading returns old stable", func(t *testing.T) {
		probe := NewIPv4Probe(nil)
		probe.applyHysteresis("1.2.3.4") // Set stable
		result := probe.applyHysteresis("5.6.7.8")
		if result != "1.2.3.4" {
			t.Errorf("expected '1.2.3.4' (old stable), got %q", result)
		}
	})

	t.Run("consecutive identical readings trigger change", func(t *testing.T) {
		probe := NewIPv4Probe(nil)
		probe.applyHysteresis("1.2.3.4") // Set stable

		// stabilityCount for IPv4 is 2, so we need 2 consecutive readings
		probe.applyHysteresis("5.6.7.8") // pending count = 1
		result := probe.applyHysteresis("5.6.7.8") // pending count = 2 -> accept
		if result != "5.6.7.8" {
			t.Errorf("expected '5.6.7.8' after hysteresis threshold, got %q", result)
		}
	})

	t.Run("alternating IPs never reach threshold", func(t *testing.T) {
		probe := NewIPv4Probe(nil)
		probe.applyHysteresis("1.2.3.4") // Set stable

		// Alternate between two IPs - neither should reach threshold
		for i := 0; i < 10; i++ {
			probe.applyHysteresis("5.6.7.8")
			result := probe.applyHysteresis("9.0.0.1")
			if result != "1.2.3.4" {
				t.Errorf("iteration %d: expected '1.2.3.4' (stable), got %q", i, result)
			}
		}
	})

	t.Run("IPv6 requires higher stability count", func(t *testing.T) {
		probe := NewIPv6Probe(nil)
		probe.applyHysteresis("2001:db8::1") // Set stable

		// stabilityCount for IPv6 is 4
		probe.applyHysteresis("2001:db8::2") // count 1
		probe.applyHysteresis("2001:db8::2") // count 2
		result := probe.applyHysteresis("2001:db8::2") // count 3
		if result != "2001:db8::1" {
			t.Errorf("expected '2001:db8::1' (not enough readings), got %q", result)
		}

		result = probe.applyHysteresis("2001:db8::2") // count 4 -> accept
		if result != "2001:db8::2" {
			t.Errorf("expected '2001:db8::2' after threshold, got %q", result)
		}
	})
}

func TestNewLocalIPv4Probe(t *testing.T) {
	probe := NewLocalIPv4Probe(nil)
	if probe == nil {
		t.Fatal("expected non-nil probe")
	}
	if probe.Name() != "local_ipv4" {
		t.Errorf("expected name='local_ipv4', got %q", probe.Name())
	}
	if probe.network != "udp4" {
		t.Errorf("expected network='udp4', got %q", probe.network)
	}
}

func TestNewLocalIPv6Probe(t *testing.T) {
	probe := NewLocalIPv6Probe(nil)
	if probe == nil {
		t.Fatal("expected non-nil probe")
	}
	if probe.Name() != "local_ipv6" {
		t.Errorf("expected name='local_ipv6', got %q", probe.Name())
	}
	if probe.network != "udp6" {
		t.Errorf("expected network='udp6', got %q", probe.network)
	}
}

func TestEnvProbe_Start_IsNoop(t *testing.T) {
	probe := NewEnvProbe("TEST")
	// Start should not panic or block
	output := make(chan SensorReading, 1)
	probe.Start(context.Background(), output)

	// No reading should be emitted
	if len(output) != 0 {
		t.Error("expected no output from Start()")
	}
}

func TestDefaultIPv4HTTPURLs(t *testing.T) {
	urls := DefaultIPv4HTTPURLs()
	if len(urls) == 0 {
		t.Fatal("expected non-empty IPv4 HTTP URLs")
	}
	for _, url := range urls {
		if url == "" {
			t.Error("URL should not be empty")
		}
	}
}

func TestDefaultIPv6HTTPURLs(t *testing.T) {
	urls := DefaultIPv6HTTPURLs()
	if len(urls) == 0 {
		t.Fatal("expected non-empty IPv6 HTTP URLs")
	}
}

func TestDefaultIPv4Resolvers(t *testing.T) {
	resolvers := DefaultIPv4Resolvers()
	if len(resolvers) == 0 {
		t.Fatal("expected non-empty IPv4 resolvers")
	}
	for _, r := range resolvers {
		if r.ResolverAddr == "" {
			t.Error("resolver addr should not be empty")
		}
		if r.Hostname == "" {
			t.Error("hostname should not be empty")
		}
		if r.QueryType == "" {
			t.Error("query type should not be empty")
		}
	}
}

func TestDefaultIPv6Resolvers(t *testing.T) {
	resolvers := DefaultIPv6Resolvers()
	if len(resolvers) == 0 {
		t.Fatal("expected non-empty IPv6 resolvers")
	}
}

func TestNetworkMonitorProbe_Check_ReturnsEmptyReading(t *testing.T) {
	probe := NewNetworkMonitorProbe(nil, nil, nil, nil, nil)
	if probe.Name() != "network_monitor" {
		t.Errorf("expected name='network_monitor', got %q", probe.Name())
	}

	reading := probe.Check(context.Background())
	if reading.Sensor != "network_monitor" {
		t.Errorf("expected sensor='network_monitor', got %q", reading.Sensor)
	}
	if reading.Timestamp.IsZero() {
		t.Error("expected non-zero timestamp")
	}
}
