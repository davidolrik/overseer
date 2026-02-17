package awareness

import (
	"context"
	"testing"
)

func TestMatchesConditions_MultipleIPAddresses(t *testing.T) {
	tests := []struct {
		name       string
		conditions map[string][]string
		sensors    map[string]SensorValue
		want       bool
	}{
		{
			name: "Single IP exact match",
			conditions: map[string][]string{
				"public_ip": {"123.45.67.89"},
			},
			sensors: map[string]SensorValue{
				"public_ip": NewSensorValue("public_ip", SensorTypeString, "123.45.67.89"),
			},
			want: true,
		},
		{
			name: "Multiple IPs - first matches",
			conditions: map[string][]string{
				"public_ip": {"123.45.67.89", "123.45.67.90", "123.45.67.91"},
			},
			sensors: map[string]SensorValue{
				"public_ip": NewSensorValue("public_ip", SensorTypeString, "123.45.67.89"),
			},
			want: true,
		},
		{
			name: "Multiple IPs - middle matches",
			conditions: map[string][]string{
				"public_ip": {"123.45.67.89", "123.45.67.90", "123.45.67.91"},
			},
			sensors: map[string]SensorValue{
				"public_ip": NewSensorValue("public_ip", SensorTypeString, "123.45.67.90"),
			},
			want: true,
		},
		{
			name: "Multiple IPs - last matches",
			conditions: map[string][]string{
				"public_ip": {"123.45.67.89", "123.45.67.90", "123.45.67.91"},
			},
			sensors: map[string]SensorValue{
				"public_ip": NewSensorValue("public_ip", SensorTypeString, "123.45.67.91"),
			},
			want: true,
		},
		{
			name: "Multiple IPs - no match",
			conditions: map[string][]string{
				"public_ip": {"123.45.67.89", "123.45.67.90", "123.45.67.91"},
			},
			sensors: map[string]SensorValue{
				"public_ip": NewSensorValue("public_ip", SensorTypeString, "98.76.54.32"),
			},
			want: false,
		},
		{
			name: "Multiple IPs with CIDR",
			conditions: map[string][]string{
				"public_ip": {"123.45.67.89", "192.168.1.0/24"},
			},
			sensors: map[string]SensorValue{
				"public_ip": NewSensorValue("public_ip", SensorTypeString, "192.168.1.50"),
			},
			want: true,
		},
		{
			name: "Multiple IPs with wildcards",
			conditions: map[string][]string{
				"public_ip": {"123.45.67.89", "192.168.*"},
			},
			sensors: map[string]SensorValue{
				"public_ip": NewSensorValue("public_ip", SensorTypeString, "192.168.100.200"),
			},
			want: true,
		},
		{
			name: "Multiple CIDR ranges",
			conditions: map[string][]string{
				"public_ip": {"10.0.0.0/8", "172.16.0.0/12", "192.168.0.0/16"},
			},
			sensors: map[string]SensorValue{
				"public_ip": NewSensorValue("public_ip", SensorTypeString, "172.16.50.100"),
			},
			want: true,
		},
		{
			name: "Empty conditions (fallback rule)",
			conditions: map[string][]string{},
			sensors: map[string]SensorValue{
				"public_ip": NewSensorValue("public_ip", SensorTypeString, "1.2.3.4"),
			},
			want: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			re := &RuleEngine{}
			got := re.matchesConditions(tt.conditions, tt.sensors)
			if got != tt.want {
				t.Errorf("matchesConditions() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestRuleEngine_EvaluateWithMultipleIPs(t *testing.T) {
	// Rules are evaluated in order (first match wins)
	rules := []Rule{
		{
			Name: "home",
			Conditions: map[string][]string{
				"public_ip": {"123.45.67.89", "123.45.67.90"},
			},
			Actions: RuleActions{
				Connect: []string{"homelab"},
			},
		},
		{
			Name: "office",
			Conditions: map[string][]string{
				"public_ip": {"98.76.54.0/24"},
			},
			Actions: RuleActions{
				Connect: []string{"office-vpn"},
			},
		},
		{
			Name:       "untrusted",
			Conditions: map[string][]string{},
			Actions: RuleActions{
				Disconnect: []string{},
			},
		},
	}

	re := NewRuleEngine(rules, map[string]Location{})

	tests := []struct {
		name         string
		sensorIP     string
		wantContext  string
	}{
		{
			name:        "Home IP #1",
			sensorIP:    "123.45.67.89",
			wantContext: "home",
		},
		{
			name:        "Home IP #2",
			sensorIP:    "123.45.67.90",
			wantContext: "home",
		},
		{
			name:        "Office IP in CIDR",
			sensorIP:    "98.76.54.100",
			wantContext: "office",
		},
		{
			name:        "Unknown IP falls back to untrusted",
			sensorIP:    "1.2.3.4",
			wantContext: "untrusted",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Create mock sensor - config uses "public_ip" but sensor is "public_ipv4"
			mockSensor := &MockSensor{
				name:       "public_ipv4",
				sensorType: SensorTypeString,
				value:      tt.sensorIP,
			}

			sensors := map[string]Sensor{
				"public_ipv4": mockSensor,
			}

			result := re.Evaluate(context.Background(), sensors)
			if result.Context != tt.wantContext {
				t.Errorf("Evaluate() context = %v, want %v", result.Context, tt.wantContext)
			}
		})
	}
}

// MockSensorNoCache wraps MockSensor but returns nil from GetLastValue,
// forcing condition evaluation to fall back to Check()
type MockSensorNoCache struct {
	MockSensor
}

func (m *MockSensorNoCache) GetLastValue() *SensorValue {
	return nil
}

// MockSensor is a simple sensor implementation for testing
type MockSensor struct {
	name       string
	sensorType SensorType
	value      interface{}
	listeners  []SensorListener
}

func (m *MockSensor) Name() string {
	return m.name
}

func (m *MockSensor) Type() SensorType {
	return m.sensorType
}

func (m *MockSensor) Check(ctx context.Context) (SensorValue, error) {
	return NewSensorValue(m.name, m.sensorType, m.value), nil
}

func (m *MockSensor) GetLastValue() *SensorValue {
	v := NewSensorValue(m.name, m.sensorType, m.value)
	return &v
}

func (m *MockSensor) Subscribe(listener SensorListener) {
	m.listeners = append(m.listeners, listener)
}

func (m *MockSensor) Unsubscribe(listener SensorListener) {
	for i, l := range m.listeners {
		if l == listener {
			m.listeners = append(m.listeners[:i], m.listeners[i+1:]...)
			return
		}
	}
}

func (m *MockSensor) SetValue(value interface{}) error {
	m.value = value
	return nil
}

func TestGetRuleByName(t *testing.T) {
	rules := []Rule{
		{Name: "alpha"},
		{Name: "beta"},
		{Name: "gamma"},
	}
	re := NewRuleEngine(rules, nil)

	tests := []struct {
		name    string
		lookup  string
		want    string
		wantErr bool
	}{
		{"first rule", "alpha", "alpha", false},
		{"middle rule", "beta", "beta", false},
		{"last rule", "gamma", "gamma", false},
		{"not found", "delta", "", true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := re.GetRuleByName(tt.lookup)
			if (err != nil) != tt.wantErr {
				t.Fatalf("error = %v, wantErr %v", err, tt.wantErr)
			}
			if err == nil && got.Name != tt.want {
				t.Errorf("Name = %q, want %q", got.Name, tt.want)
			}
		})
	}
}

func TestGetLocation(t *testing.T) {
	locations := map[string]Location{
		"hq":   {Name: "hq", DisplayName: "Headquarters"},
		"home": {Name: "home", DisplayName: "Home"},
	}
	re := NewRuleEngine(nil, locations)

	t.Run("found", func(t *testing.T) {
		loc := re.GetLocation("hq")
		if loc == nil {
			t.Fatal("expected non-nil")
		}
		if loc.DisplayName != "Headquarters" {
			t.Errorf("DisplayName = %q, want %q", loc.DisplayName, "Headquarters")
		}
	})

	t.Run("not found", func(t *testing.T) {
		loc := re.GetLocation("mars")
		if loc != nil {
			t.Errorf("expected nil, got %+v", loc)
		}
	})
}

func TestGetAllRules(t *testing.T) {
	t.Run("returns copy", func(t *testing.T) {
		rules := []Rule{
			{Name: "a"},
			{Name: "b"},
		}
		re := NewRuleEngine(rules, nil)
		got := re.GetAllRules()

		if len(got) != 2 {
			t.Fatalf("len = %d, want 2", len(got))
		}

		// Mutate the copy and verify original is unchanged
		got[0].Name = "mutated"
		original, _ := re.GetRuleByName("a")
		if original == nil {
			t.Fatal("original rule 'a' should still exist")
		}
	})

	t.Run("empty engine", func(t *testing.T) {
		re := NewRuleEngine(nil, nil)
		got := re.GetAllRules()
		if len(got) != 0 {
			t.Errorf("len = %d, want 0", len(got))
		}
	})
}

func TestMatchesPattern_EdgeCases(t *testing.T) {
	tests := []struct {
		name    string
		value   string
		pattern string
		want    bool
	}{
		{
			name:    "invalid CIDR",
			value:   "1.2.3.4",
			pattern: "not-a-cidr/99",
			want:    false,
		},
		{
			name:    "non-IP value against valid CIDR",
			value:   "hello",
			pattern: "10.0.0.0/8",
			want:    false,
		},
		{
			name:    "empty value does not match wildcard",
			value:   "",
			pattern: "*",
			want:    false,
		},
		{
			name:    "multi-star pattern",
			value:   "192.168.1.100",
			pattern: "192.*.1.*",
			want:    true,
		},
		{
			name:    "prefix wildcard",
			value:   "hello.example.com",
			pattern: "*.example.com",
			want:    true,
		},
		{
			name:    "suffix wildcard",
			value:   "192.168.1.100",
			pattern: "192.168.*",
			want:    true,
		},
		{
			name:    "wildcard no match",
			value:   "10.0.0.1",
			pattern: "192.*",
			want:    false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := matchesPattern(tt.value, tt.pattern)
			if got != tt.want {
				t.Errorf("matchesPattern(%q, %q) = %v, want %v", tt.value, tt.pattern, got, tt.want)
			}
		})
	}
}

func TestMatchesConditions_EdgeCases(t *testing.T) {
	re := &RuleEngine{}

	t.Run("missing sensor", func(t *testing.T) {
		got := re.matchesConditions(
			map[string][]string{"missing": {"value"}},
			map[string]SensorValue{},
		)
		if got {
			t.Error("expected false for missing sensor")
		}
	})

	t.Run("empty sensor value", func(t *testing.T) {
		got := re.matchesConditions(
			map[string][]string{"s": {"*"}},
			map[string]SensorValue{
				"s": NewSensorValue("s", SensorTypeString, ""),
			},
		)
		if got {
			t.Error("expected false for empty sensor value")
		}
	})
}

func TestDetermineLocation(t *testing.T) {
	ctx := context.Background()

	t.Run("offline via Condition", func(t *testing.T) {
		locations := map[string]Location{
			"offline": {
				Name:      "offline",
				Condition: NewBooleanCondition("online", false),
			},
			"hq": {
				Name:      "hq",
				Condition: NewSensorCondition("public_ipv4", "1.2.3.4"),
			},
		}
		re := NewRuleEngine(nil, locations)
		sensors := map[string]Sensor{
			"online": &MockSensor{name: "online", sensorType: SensorTypeBoolean, value: false},
		}
		got := re.determineLocation(ctx, sensors)
		if got != "offline" {
			t.Errorf("got %q, want %q", got, "offline")
		}
	})

	t.Run("offline via Conditions map", func(t *testing.T) {
		locations := map[string]Location{
			"offline": {
				Name: "offline",
				Conditions: map[string][]string{
					"public_ip": {"0.0.0.0"},
				},
			},
		}
		re := NewRuleEngine(nil, locations)
		sensors := map[string]Sensor{
			"public_ipv4": &MockSensor{name: "public_ipv4", sensorType: SensorTypeString, value: "0.0.0.0"},
		}
		got := re.determineLocation(ctx, sensors)
		if got != "offline" {
			t.Errorf("got %q, want %q", got, "offline")
		}
	})

	t.Run("matching location via Condition", func(t *testing.T) {
		locations := map[string]Location{
			"hq": {
				Name:      "hq",
				Condition: NewSensorCondition("public_ipv4", "1.2.3.4"),
			},
		}
		re := NewRuleEngine(nil, locations)
		sensors := map[string]Sensor{
			"public_ipv4": &MockSensor{name: "public_ipv4", sensorType: SensorTypeString, value: "1.2.3.4"},
		}
		got := re.determineLocation(ctx, sensors)
		if got != "hq" {
			t.Errorf("got %q, want %q", got, "hq")
		}
	})

	t.Run("matching location via Conditions map", func(t *testing.T) {
		locations := map[string]Location{
			"home": {
				Name: "home",
				Conditions: map[string][]string{
					"public_ip": {"5.6.7.8"},
				},
			},
		}
		re := NewRuleEngine(nil, locations)
		sensors := map[string]Sensor{
			"public_ipv4": &MockSensor{name: "public_ipv4", sensorType: SensorTypeString, value: "5.6.7.8"},
		}
		got := re.determineLocation(ctx, sensors)
		if got != "home" {
			t.Errorf("got %q, want %q", got, "home")
		}
	})

	t.Run("no match returns unknown", func(t *testing.T) {
		locations := map[string]Location{
			"hq": {
				Name:      "hq",
				Condition: NewSensorCondition("public_ipv4", "1.2.3.4"),
			},
		}
		re := NewRuleEngine(nil, locations)
		sensors := map[string]Sensor{
			"public_ipv4": &MockSensor{name: "public_ipv4", sensorType: SensorTypeString, value: "99.99.99.99"},
		}
		got := re.determineLocation(ctx, sensors)
		if got != "unknown" {
			t.Errorf("got %q, want %q", got, "unknown")
		}
	})

	t.Run("offline takes priority over other locations", func(t *testing.T) {
		locations := map[string]Location{
			"offline": {
				Name:      "offline",
				Condition: NewBooleanCondition("online", false),
			},
			"hq": {
				Name:      "hq",
				Condition: NewSensorCondition("public_ipv4", "1.2.3.4"),
			},
		}
		re := NewRuleEngine(nil, locations)
		sensors := map[string]Sensor{
			"online":      &MockSensor{name: "online", sensorType: SensorTypeBoolean, value: false},
			"public_ipv4": &MockSensor{name: "public_ipv4", sensorType: SensorTypeString, value: "1.2.3.4"},
		}
		got := re.determineLocation(ctx, sensors)
		if got != "offline" {
			t.Errorf("got %q, want %q (offline should take priority)", got, "offline")
		}
	})
}

func TestRuleEngine_Evaluate_LocationBasedRules(t *testing.T) {
	ctx := context.Background()

	t.Run("match via location Condition", func(t *testing.T) {
		locations := map[string]Location{
			"hq": {
				Name:      "hq",
				Condition: NewSensorCondition("public_ipv4", "1.2.3.4"),
			},
		}
		rules := []Rule{
			{Name: "office", Locations: []string{"hq"}, Actions: RuleActions{Connect: []string{"vpn"}}},
		}
		re := NewRuleEngine(rules, locations)
		sensors := map[string]Sensor{
			"public_ipv4": &MockSensor{name: "public_ipv4", sensorType: SensorTypeString, value: "1.2.3.4"},
		}
		result := re.Evaluate(ctx, sensors)
		if result.Context != "office" {
			t.Errorf("Context = %q, want %q", result.Context, "office")
		}
		if result.MatchedBy != "location" {
			t.Errorf("MatchedBy = %q, want %q", result.MatchedBy, "location")
		}
	})

	t.Run("match via location Conditions map", func(t *testing.T) {
		locations := map[string]Location{
			"home": {
				Name: "home",
				Conditions: map[string][]string{
					"public_ip": {"5.6.7.8"},
				},
			},
		}
		rules := []Rule{
			{Name: "home-ctx", Locations: []string{"home"}},
		}
		re := NewRuleEngine(rules, locations)
		sensors := map[string]Sensor{
			"public_ipv4": &MockSensor{name: "public_ipv4", sensorType: SensorTypeString, value: "5.6.7.8"},
		}
		result := re.Evaluate(ctx, sensors)
		if result.Context != "home-ctx" {
			t.Errorf("Context = %q, want %q", result.Context, "home-ctx")
		}
		if result.MatchedBy != "location" {
			t.Errorf("MatchedBy = %q, want %q", result.MatchedBy, "location")
		}
	})

	t.Run("skip non-existent location", func(t *testing.T) {
		rules := []Rule{
			{Name: "ctx", Locations: []string{"nonexistent"}},
		}
		re := NewRuleEngine(rules, map[string]Location{})
		sensors := map[string]Sensor{}
		result := re.Evaluate(ctx, sensors)
		if result.Context != "unknown" {
			t.Errorf("Context = %q, want %q", result.Context, "unknown")
		}
	})

	t.Run("first match wins", func(t *testing.T) {
		locations := map[string]Location{
			"loc": {
				Name:      "loc",
				Condition: NewSensorCondition("public_ipv4", "1.2.3.4"),
			},
		}
		rules := []Rule{
			{Name: "first", Locations: []string{"loc"}},
			{Name: "second", Locations: []string{"loc"}},
		}
		re := NewRuleEngine(rules, locations)
		sensors := map[string]Sensor{
			"public_ipv4": &MockSensor{name: "public_ipv4", sensorType: SensorTypeString, value: "1.2.3.4"},
		}
		result := re.Evaluate(ctx, sensors)
		if result.Context != "first" {
			t.Errorf("Context = %q, want %q (first match wins)", result.Context, "first")
		}
	})

	t.Run("fallback rule matches when no conditions", func(t *testing.T) {
		rules := []Rule{
			{Name: "specific", Condition: NewSensorCondition("public_ipv4", "1.2.3.4")},
			{Name: "fallback"}, // no conditions, no locations = fallback
		}
		re := NewRuleEngine(rules, map[string]Location{})
		sensors := map[string]Sensor{
			"public_ipv4": &MockSensor{name: "public_ipv4", sensorType: SensorTypeString, value: "99.99.99.99"},
		}
		result := re.Evaluate(ctx, sensors)
		if result.Context != "fallback" {
			t.Errorf("Context = %q, want %q", result.Context, "fallback")
		}
		if result.MatchedBy != "fallback" {
			t.Errorf("MatchedBy = %q, want %q", result.MatchedBy, "fallback")
		}
	})

	t.Run("match via rule Condition field", func(t *testing.T) {
		rules := []Rule{
			{
				Name:      "direct",
				Condition: NewSensorCondition("public_ipv4", "1.2.3.4"),
			},
		}
		re := NewRuleEngine(rules, map[string]Location{})
		sensors := map[string]Sensor{
			"public_ipv4": &MockSensor{name: "public_ipv4", sensorType: SensorTypeString, value: "1.2.3.4"},
		}
		result := re.Evaluate(ctx, sensors)
		if result.Context != "direct" {
			t.Errorf("Context = %q, want %q", result.Context, "direct")
		}
		if result.MatchedBy != "conditions" {
			t.Errorf("MatchedBy = %q, want %q", result.MatchedBy, "conditions")
		}
	})

	t.Run("no match returns unknown", func(t *testing.T) {
		rules := []Rule{
			{Name: "specific", Condition: NewSensorCondition("public_ipv4", "1.2.3.4")},
		}
		re := NewRuleEngine(rules, map[string]Location{})
		sensors := map[string]Sensor{
			"public_ipv4": &MockSensor{name: "public_ipv4", sensorType: SensorTypeString, value: "99.99.99.99"},
		}
		result := re.Evaluate(ctx, sensors)
		if result.Context != "unknown" {
			t.Errorf("Context = %q, want %q", result.Context, "unknown")
		}
		if result.MatchedBy != "none" {
			t.Errorf("MatchedBy = %q, want %q", result.MatchedBy, "none")
		}
	})
}
