package state

import (
	"net"
	"testing"
	"time"
)

// --- Pattern matching ---

func TestMatchesPatternExact(t *testing.T) {
	if !matchesPattern("hello", "hello") {
		t.Error("Expected exact match")
	}
	if matchesPattern("hello", "world") {
		t.Error("Expected no match for different strings")
	}
}

func TestMatchesCIDR(t *testing.T) {
	tests := []struct {
		ip    string
		cidr  string
		match bool
	}{
		{"192.168.1.50", "192.168.1.0/24", true},
		{"192.168.2.50", "192.168.1.0/24", false},
		{"10.0.0.1", "10.0.0.0/8", true},
		{"8.8.8.8", "10.0.0.0/8", false},
		{"invalid", "10.0.0.0/8", false},
		{"10.0.0.1", "invalid/cidr", false},
	}

	for _, tt := range tests {
		t.Run(tt.ip+"_"+tt.cidr, func(t *testing.T) {
			got := matchesCIDR(tt.ip, tt.cidr)
			if got != tt.match {
				t.Errorf("matchesCIDR(%q, %q) = %v, want %v", tt.ip, tt.cidr, got, tt.match)
			}
		})
	}
}

func TestMatchesWildcard(t *testing.T) {
	tests := []struct {
		value   string
		pattern string
		match   bool
	}{
		{"hello world", "hello*", true},
		{"hello world", "*world", true},
		{"hello world", "hello*world", true},
		{"hello cruel world", "hello*world", true},
		{"hello", "world*", false},
		{"", "hello*", false},
		{"test.example.com", "*.example.com", true},
		{"test.other.com", "*.example.com", false},
	}

	for _, tt := range tests {
		t.Run(tt.value+"_"+tt.pattern, func(t *testing.T) {
			got := matchesWildcard(tt.value, tt.pattern)
			if got != tt.match {
				t.Errorf("matchesWildcard(%q, %q) = %v, want %v", tt.value, tt.pattern, got, tt.match)
			}
		})
	}
}

func TestMatchesPatternDispatch(t *testing.T) {
	// CIDR patterns (contain /)
	if !matchesPattern("10.0.0.1", "10.0.0.0/24") {
		t.Error("Expected CIDR match")
	}

	// Wildcard patterns (contain *)
	if !matchesPattern("hello world", "hello*") {
		t.Error("Expected wildcard match")
	}

	// Exact match
	if !matchesPattern("exact", "exact") {
		t.Error("Expected exact match")
	}

	// No match
	if matchesPattern("foo", "bar") {
		t.Error("Expected no match")
	}
}

// --- isNetworkSensor ---

func TestIsNetworkSensor(t *testing.T) {
	tests := []struct {
		name string
		want bool
	}{
		{"public_ipv4", true},
		{"public_ipv6", true},
		{"local_ipv4", true},
		{"tcp", false},
		{"env:HOME", false},
		{"online", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := isNetworkSensor(tt.name)
			if got != tt.want {
				t.Errorf("isNetworkSensor(%q) = %v, want %v", tt.name, got, tt.want)
			}
		})
	}
}

// --- mapConditionKey ---

func TestMapConditionKey(t *testing.T) {
	if mapConditionKey("public_ip") != "public_ipv4" {
		t.Error("Expected public_ip to map to public_ipv4")
	}
	if mapConditionKey("tcp") != "tcp" {
		t.Error("Expected other keys to pass through unchanged")
	}
}

// --- SensorCondition ---

func TestSensorConditionOnlineSensor(t *testing.T) {
	// "online" sensor uses computed online state
	cond := NewBooleanCondition("online", true)
	readings := map[string]SensorReading{}

	if !cond.Evaluate(readings, true) {
		t.Error("Expected true when online=true and condition expects true")
	}
	if cond.Evaluate(readings, false) {
		t.Error("Expected false when online=false and condition expects true")
	}
}

func TestSensorConditionNetworkSensorOffline(t *testing.T) {
	// Network sensors should not match when offline
	cond := NewSensorCondition("public_ipv4", "8.8.8.8")
	readings := map[string]SensorReading{
		"public_ipv4": {
			Sensor: "public_ipv4",
			IP:     net.ParseIP("8.8.8.8"),
		},
	}

	if cond.Evaluate(readings, false) {
		t.Error("Network sensor should not match when offline")
	}
	if !cond.Evaluate(readings, true) {
		t.Error("Network sensor should match when online")
	}
}

func TestSensorConditionStringMatch(t *testing.T) {
	cond := NewSensorCondition("env:HOSTNAME", "my-laptop")
	readings := map[string]SensorReading{
		"env:HOSTNAME": {
			Sensor: "env:HOSTNAME",
			Value:  "my-laptop",
		},
	}

	if !cond.Evaluate(readings, true) {
		t.Error("Expected string match")
	}
}

func TestSensorConditionIPMatch(t *testing.T) {
	cond := NewSensorCondition("public_ipv4", "192.168.1.0/24")
	readings := map[string]SensorReading{
		"public_ipv4": {
			Sensor: "public_ipv4",
			IP:     net.ParseIP("192.168.1.50"),
		},
	}

	if !cond.Evaluate(readings, true) {
		t.Error("Expected IP CIDR match")
	}
}

func TestSensorConditionMissingSensor(t *testing.T) {
	cond := NewSensorCondition("nonexistent", "value")
	readings := map[string]SensorReading{}

	if cond.Evaluate(readings, true) {
		t.Error("Expected false for missing sensor")
	}
}

func TestSensorConditionEmptyValue(t *testing.T) {
	cond := NewSensorCondition("env:EMPTY", "something")
	readings := map[string]SensorReading{
		"env:EMPTY": {Sensor: "env:EMPTY"},
	}

	if cond.Evaluate(readings, true) {
		t.Error("Expected false for empty value")
	}
}

func TestSensorConditionBooleanWithOnlineField(t *testing.T) {
	cond := NewBooleanCondition("tcp", true)
	readings := map[string]SensorReading{
		"tcp": {
			Sensor: "tcp",
			Online: boolPtr(true),
		},
	}

	if !cond.Evaluate(readings, true) {
		t.Error("Expected boolean condition to match Online field")
	}
}

// --- GroupCondition ---

func TestGroupConditionAllEmpty(t *testing.T) {
	cond := NewAllCondition()
	if !cond.Evaluate(nil, true) {
		t.Error("Empty ALL condition should return true")
	}
}

func TestGroupConditionAnyEmpty(t *testing.T) {
	cond := NewAnyCondition()
	if cond.Evaluate(nil, true) {
		t.Error("Empty ANY condition should return false")
	}
}

func TestGroupConditionAll(t *testing.T) {
	readings := map[string]SensorReading{
		"env:A": {Sensor: "env:A", Value: "x"},
		"env:B": {Sensor: "env:B", Value: "y"},
	}

	// All match
	cond := NewAllCondition(
		NewSensorCondition("env:A", "x"),
		NewSensorCondition("env:B", "y"),
	)
	if !cond.Evaluate(readings, true) {
		t.Error("ALL should pass when all conditions match")
	}

	// One doesn't match
	cond = NewAllCondition(
		NewSensorCondition("env:A", "x"),
		NewSensorCondition("env:B", "z"),
	)
	if cond.Evaluate(readings, true) {
		t.Error("ALL should fail when any condition fails")
	}
}

func TestGroupConditionAny(t *testing.T) {
	readings := map[string]SensorReading{
		"env:A": {Sensor: "env:A", Value: "x"},
	}

	// One matches
	cond := NewAnyCondition(
		NewSensorCondition("env:A", "x"),
		NewSensorCondition("env:A", "y"),
	)
	if !cond.Evaluate(readings, true) {
		t.Error("ANY should pass when at least one condition matches")
	}

	// None match
	cond = NewAnyCondition(
		NewSensorCondition("env:A", "y"),
		NewSensorCondition("env:A", "z"),
	)
	if cond.Evaluate(readings, true) {
		t.Error("ANY should fail when no conditions match")
	}
}

func TestGroupConditionUnknownOperator(t *testing.T) {
	cond := &GroupCondition{
		Operator:   "invalid",
		Conditions: []Condition{NewSensorCondition("env:A", "x")},
	}

	if cond.Evaluate(nil, true) {
		t.Error("Unknown operator should return false")
	}
}

// --- ConditionFromMap ---

func TestConditionFromMapEmpty(t *testing.T) {
	cond := ConditionFromMap(nil)
	// Empty = always matches (returns AllCondition with no children)
	if !cond.Evaluate(nil, true) {
		t.Error("Empty condition map should always match")
	}
}

func TestConditionFromMapSingle(t *testing.T) {
	cond := ConditionFromMap(map[string][]string{
		"env:HOST": {"my-host"},
	})

	readings := map[string]SensorReading{
		"env:HOST": {Sensor: "env:HOST", Value: "my-host"},
	}

	if !cond.Evaluate(readings, true) {
		t.Error("Single pattern condition should match")
	}
}

func TestConditionFromMapMultiplePatterns(t *testing.T) {
	// Multiple patterns for same key = OR
	cond := ConditionFromMap(map[string][]string{
		"public_ip": {"1.2.3.4", "5.6.7.8"},
	})

	// Note: public_ip maps to public_ipv4
	readings := map[string]SensorReading{
		"public_ipv4": {Sensor: "public_ipv4", IP: net.ParseIP("5.6.7.8")},
	}

	if !cond.Evaluate(readings, true) {
		t.Error("Should match when any of the OR patterns match")
	}
}

func TestConditionFromMapEmptyPatterns(t *testing.T) {
	cond := ConditionFromMap(map[string][]string{
		"env:HOST": {},
	})
	// Empty patterns should be treated as always matches
	if !cond.Evaluate(nil, true) {
		t.Error("Empty patterns should be treated as always matches")
	}
}

// --- RuleEngine ---

func TestRuleEngineLocationMatch(t *testing.T) {
	locations := map[string]Location{
		"home": {
			Name:        "home",
			DisplayName: "Home Network",
			Conditions: map[string][]string{
				"env:HOST": {"my-laptop"},
			},
		},
	}

	rules := []Rule{
		{
			Name:        "trusted",
			DisplayName: "Trusted",
			Locations:   []string{"home"},
		},
	}

	engine := NewRuleEngine(rules, locations, nil)
	readings := map[string]SensorReading{
		"env:HOST": {Sensor: "env:HOST", Value: "my-laptop"},
	}

	result := engine.Evaluate(readings, true)
	if result.Context != "trusted" {
		t.Errorf("Expected context %q, got %q", "trusted", result.Context)
	}
	if result.Location != "home" {
		t.Errorf("Expected location %q, got %q", "home", result.Location)
	}
	if result.ContextDisplayName != "Trusted" {
		t.Errorf("Expected display name %q, got %q", "Trusted", result.ContextDisplayName)
	}
}

func TestRuleEngineFallbackRule(t *testing.T) {
	// A rule with no conditions and no locations = fallback
	rules := []Rule{
		{
			Name:        "default",
			DisplayName: "Default Context",
		},
	}

	engine := NewRuleEngine(rules, nil, nil)
	result := engine.Evaluate(nil, true)

	if result.Context != "default" {
		t.Errorf("Expected context %q, got %q", "default", result.Context)
	}
}

func TestRuleEngineNoMatch(t *testing.T) {
	rules := []Rule{
		{
			Name: "specific",
			Conditions: map[string][]string{
				"env:HOST": {"something-specific"},
			},
		},
	}

	engine := NewRuleEngine(rules, nil, nil)
	readings := map[string]SensorReading{
		"env:HOST": {Sensor: "env:HOST", Value: "different"},
	}

	result := engine.Evaluate(readings, true)
	if result.Context != "unknown" {
		t.Errorf("Expected context %q when no rule matches, got %q", "unknown", result.Context)
	}
	if result.MatchedRule != "none" {
		t.Errorf("Expected matched rule %q, got %q", "none", result.MatchedRule)
	}
}

func TestRuleEngineFirstMatchWins(t *testing.T) {
	rules := []Rule{
		{
			Name: "first",
			Conditions: map[string][]string{
				"env:HOST": {"laptop"},
			},
		},
		{
			Name: "second",
			Conditions: map[string][]string{
				"env:HOST": {"laptop"},
			},
		},
	}

	engine := NewRuleEngine(rules, nil, nil)
	readings := map[string]SensorReading{
		"env:HOST": {Sensor: "env:HOST", Value: "laptop"},
	}

	result := engine.Evaluate(readings, true)
	if result.Context != "first" {
		t.Errorf("Expected first matching rule, got %q", result.Context)
	}
}

func TestRuleEngineEnvironmentMerge(t *testing.T) {
	locations := map[string]Location{
		"home": {
			Name: "home",
			Conditions: map[string][]string{
				"env:HOST": {"laptop"},
			},
			Environment: map[string]string{
				"FROM_LOCATION": "loc-value",
				"SHARED":        "from-location",
			},
		},
	}

	rules := []Rule{
		{
			Name:      "trusted",
			Locations: []string{"home"},
			Environment: map[string]string{
				"FROM_RULE": "rule-value",
				"SHARED":    "from-rule",
			},
		},
	}

	engine := NewRuleEngine(rules, locations, nil)
	readings := map[string]SensorReading{
		"env:HOST": {Sensor: "env:HOST", Value: "laptop"},
	}

	result := engine.Evaluate(readings, true)
	if result.Environment["FROM_LOCATION"] != "loc-value" {
		t.Errorf("Expected location env var, got %q", result.Environment["FROM_LOCATION"])
	}
	if result.Environment["FROM_RULE"] != "rule-value" {
		t.Errorf("Expected rule env var, got %q", result.Environment["FROM_RULE"])
	}
	// Rule overrides location for shared keys
	if result.Environment["SHARED"] != "from-rule" {
		t.Errorf("Expected rule to override location env var, got %q", result.Environment["SHARED"])
	}
}

func TestRuleEngineGetLocation(t *testing.T) {
	locations := map[string]Location{
		"home": {Name: "home", DisplayName: "Home"},
	}

	engine := NewRuleEngine(nil, locations, nil)

	loc := engine.GetLocation("home")
	if loc == nil {
		t.Fatal("Expected to find location 'home'")
	}
	if loc.DisplayName != "Home" {
		t.Errorf("Expected display name %q, got %q", "Home", loc.DisplayName)
	}

	if engine.GetLocation("nonexistent") != nil {
		t.Error("Expected nil for nonexistent location")
	}
}

func TestRuleEngineGetRules(t *testing.T) {
	rules := []Rule{{Name: "a"}, {Name: "b"}}
	engine := NewRuleEngine(rules, nil, nil)

	got := engine.GetRules()
	if len(got) != 2 {
		t.Fatalf("Expected 2 rules, got %d", len(got))
	}
}

func TestRuleEngineRuleWithStructuredCondition(t *testing.T) {
	rules := []Rule{
		{
			Name: "structured",
			Condition: NewAllCondition(
				NewSensorCondition("env:HOST", "laptop"),
				NewBooleanCondition("online", true),
			),
		},
	}

	engine := NewRuleEngine(rules, nil, nil)
	readings := map[string]SensorReading{
		"env:HOST": {Sensor: "env:HOST", Value: "laptop"},
	}

	result := engine.Evaluate(readings, true)
	if result.Context != "structured" {
		t.Errorf("Expected context %q, got %q", "structured", result.Context)
	}
}

// --- ExtractRequiredSensors ---

func TestExtractRequiredSensors(t *testing.T) {
	cond := NewAllCondition(
		NewSensorCondition("public_ipv4", "1.2.3.4"),
		NewAnyCondition(
			NewSensorCondition("env:HOST", "laptop"),
			NewBooleanCondition("tcp", true),
		),
	)

	sensors := ExtractRequiredSensors(cond)
	if len(sensors) != 3 {
		t.Fatalf("Expected 3 sensors, got %d: %v", len(sensors), sensors)
	}

	// Check all sensors are present (order not guaranteed)
	sensorMap := make(map[string]bool)
	for _, s := range sensors {
		sensorMap[s] = true
	}
	for _, expected := range []string{"public_ipv4", "env:HOST", "tcp"} {
		if !sensorMap[expected] {
			t.Errorf("Expected sensor %q in result", expected)
		}
	}
}

func TestExtractRequiredSensorsNil(t *testing.T) {
	sensors := ExtractRequiredSensors(nil)
	if sensors != nil {
		t.Errorf("Expected nil for nil condition, got %v", sensors)
	}
}

// --- CollectEnvSensors ---

func TestCollectEnvSensors(t *testing.T) {
	rules := []Rule{
		{
			Name: "test",
			Condition: NewAllCondition(
				NewSensorCondition("env:HOST", "laptop"),
				NewSensorCondition("public_ipv4", "1.2.3.4"),
			),
		},
	}
	locations := map[string]Location{
		"home": {
			Name: "home",
			Condition: NewSensorCondition("env:NETWORK", "home-wifi"),
		},
	}

	envSensors := CollectEnvSensors(rules, locations)

	// Should only return env vars (without env: prefix), not other sensors
	sensorMap := make(map[string]bool)
	for _, s := range envSensors {
		sensorMap[s] = true
	}

	if !sensorMap["HOST"] {
		t.Error("Expected HOST in env sensors")
	}
	if !sensorMap["NETWORK"] {
		t.Error("Expected NETWORK in env sensors")
	}
	if len(envSensors) != 2 {
		t.Errorf("Expected 2 env sensors, got %d: %v", len(envSensors), envSensors)
	}
}

func TestCollectEnvSensorsFromMap(t *testing.T) {
	rules := []Rule{
		{
			Name: "test",
			Conditions: map[string][]string{
				"env:DOMAIN": {"example.com"},
			},
		},
	}

	envSensors := CollectEnvSensors(rules, nil)

	found := false
	for _, s := range envSensors {
		if s == "DOMAIN" {
			found = true
		}
	}
	if !found {
		t.Errorf("Expected DOMAIN in env sensors from map conditions, got %v", envSensors)
	}
}

// --- RuleEngine.determineLocation ---

func TestRuleEngineDetermineLocationOffline(t *testing.T) {
	locations := map[string]Location{
		"offline": {
			Name:      "offline",
			Condition: NewBooleanCondition("online", false),
		},
		"home": {
			Name: "home",
			Conditions: map[string][]string{
				"env:HOST": {"laptop"},
			},
		},
	}

	rules := []Rule{
		{Name: "default"}, // fallback
	}

	engine := NewRuleEngine(rules, locations, nil)
	readings := map[string]SensorReading{
		"env:HOST": {
			Sensor:    "env:HOST",
			Value:     "laptop",
			Timestamp: time.Now(),
		},
	}

	// When offline, "offline" location should take priority
	result := engine.Evaluate(readings, false)
	if result.Location != "offline" {
		t.Errorf("Expected location %q when offline, got %q", "offline", result.Location)
	}
}

func TestRuleEngineGlobalEnvironmentMerge(t *testing.T) {
	t.Run("global env used when no location or rule env", func(t *testing.T) {
		globalEnv := map[string]string{
			"GLOBAL_VAR": "global-value",
		}

		rules := []Rule{
			{Name: "fallback"}, // fallback with no env
		}

		engine := NewRuleEngine(rules, nil, globalEnv)
		result := engine.Evaluate(nil, true)

		if result.Environment["GLOBAL_VAR"] != "global-value" {
			t.Errorf("Expected global env var, got %q", result.Environment["GLOBAL_VAR"])
		}
	})

	t.Run("location overrides global", func(t *testing.T) {
		globalEnv := map[string]string{
			"SHARED":     "from-global",
			"GLOBAL_ONLY": "global-value",
		}

		locations := map[string]Location{
			"home": {
				Name: "home",
				Conditions: map[string][]string{
					"env:HOST": {"laptop"},
				},
				Environment: map[string]string{
					"SHARED":   "from-location",
					"LOC_ONLY": "loc-value",
				},
			},
		}

		rules := []Rule{
			{
				Name:      "trusted",
				Locations: []string{"home"},
			},
		}

		engine := NewRuleEngine(rules, locations, globalEnv)
		readings := map[string]SensorReading{
			"env:HOST": {Sensor: "env:HOST", Value: "laptop"},
		}

		result := engine.Evaluate(readings, true)
		if result.Environment["GLOBAL_ONLY"] != "global-value" {
			t.Errorf("Expected global-only var, got %q", result.Environment["GLOBAL_ONLY"])
		}
		if result.Environment["LOC_ONLY"] != "loc-value" {
			t.Errorf("Expected location-only var, got %q", result.Environment["LOC_ONLY"])
		}
		if result.Environment["SHARED"] != "from-location" {
			t.Errorf("Expected location to override global, got %q", result.Environment["SHARED"])
		}
	})

	t.Run("context overrides location overrides global", func(t *testing.T) {
		globalEnv := map[string]string{
			"SHARED":      "from-global",
			"GLOBAL_ONLY": "global-value",
		}

		locations := map[string]Location{
			"home": {
				Name: "home",
				Conditions: map[string][]string{
					"env:HOST": {"laptop"},
				},
				Environment: map[string]string{
					"SHARED":   "from-location",
					"LOC_ONLY": "loc-value",
				},
			},
		}

		rules := []Rule{
			{
				Name:      "trusted",
				Locations: []string{"home"},
				Environment: map[string]string{
					"SHARED":    "from-context",
					"CTX_ONLY":  "ctx-value",
				},
			},
		}

		engine := NewRuleEngine(rules, locations, globalEnv)
		readings := map[string]SensorReading{
			"env:HOST": {Sensor: "env:HOST", Value: "laptop"},
		}

		result := engine.Evaluate(readings, true)
		if result.Environment["GLOBAL_ONLY"] != "global-value" {
			t.Errorf("Expected global-only var, got %q", result.Environment["GLOBAL_ONLY"])
		}
		if result.Environment["LOC_ONLY"] != "loc-value" {
			t.Errorf("Expected location-only var, got %q", result.Environment["LOC_ONLY"])
		}
		if result.Environment["CTX_ONLY"] != "ctx-value" {
			t.Errorf("Expected context-only var, got %q", result.Environment["CTX_ONLY"])
		}
		if result.Environment["SHARED"] != "from-context" {
			t.Errorf("Expected context to override all for shared var, got %q", result.Environment["SHARED"])
		}
	})

	t.Run("nil global env works fine", func(t *testing.T) {
		locations := map[string]Location{
			"home": {
				Name: "home",
				Conditions: map[string][]string{
					"env:HOST": {"laptop"},
				},
				Environment: map[string]string{
					"LOC_VAR": "loc-value",
				},
			},
		}

		rules := []Rule{
			{
				Name:      "trusted",
				Locations: []string{"home"},
			},
		}

		engine := NewRuleEngine(rules, locations, nil)
		readings := map[string]SensorReading{
			"env:HOST": {Sensor: "env:HOST", Value: "laptop"},
		}

		result := engine.Evaluate(readings, true)
		if result.Environment["LOC_VAR"] != "loc-value" {
			t.Errorf("Expected location var, got %q", result.Environment["LOC_VAR"])
		}
	})
}
