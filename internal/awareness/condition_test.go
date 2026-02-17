package awareness

import (
	"context"
	"testing"
)

func TestSensorCondition_BooleanSensor(t *testing.T) {
	tests := []struct {
		name      string
		condition *SensorCondition
		sensors   map[string]Sensor
		want      bool
		wantErr   bool
	}{
		{
			name: "Online sensor - true",
			condition: &SensorCondition{
				SensorName: "online",
				BoolValue:  boolPtr(true),
			},
			sensors: map[string]Sensor{
				"online": &MockSensor{
					name:       "online",
					sensorType: SensorTypeBoolean,
					value:      true,
				},
			},
			want:    true,
			wantErr: false,
		},
		{
			name: "Online sensor - false",
			condition: &SensorCondition{
				SensorName: "online",
				BoolValue:  boolPtr(false),
			},
			sensors: map[string]Sensor{
				"online": &MockSensor{
					name:       "online",
					sensorType: SensorTypeBoolean,
					value:      false,
				},
			},
			want:    true,
			wantErr: false,
		},
		{
			name: "Online sensor - mismatch",
			condition: &SensorCondition{
				SensorName: "online",
				BoolValue:  boolPtr(true),
			},
			sensors: map[string]Sensor{
				"online": &MockSensor{
					name:       "online",
					sensorType: SensorTypeBoolean,
					value:      false,
				},
			},
			want:    false,
			wantErr: false,
		},
		{
			name: "Sensor not found",
			condition: &SensorCondition{
				SensorName: "online",
				BoolValue:  boolPtr(true),
			},
			sensors: map[string]Sensor{},
			want:    false,
			wantErr: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := tt.condition.Evaluate(context.Background(), tt.sensors)
			if (err != nil) != tt.wantErr {
				t.Errorf("Evaluate() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if got != tt.want {
				t.Errorf("Evaluate() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestSensorCondition_StringPattern(t *testing.T) {
	tests := []struct {
		name      string
		condition *SensorCondition
		sensors   map[string]Sensor
		want      bool
	}{
		{
			name: "Exact IP match",
			condition: &SensorCondition{
				SensorName: "public_ip",
				Pattern:    "192.168.1.100",
			},
			sensors: map[string]Sensor{
				"public_ip": &MockSensor{
					name:       "public_ip",
					sensorType: SensorTypeString,
					value:      "192.168.1.100",
				},
			},
			want: true,
		},
		{
			name: "CIDR match",
			condition: &SensorCondition{
				SensorName: "public_ip",
				Pattern:    "192.168.1.0/24",
			},
			sensors: map[string]Sensor{
				"public_ip": &MockSensor{
					name:       "public_ip",
					sensorType: SensorTypeString,
					value:      "192.168.1.50",
				},
			},
			want: true,
		},
		{
			name: "Wildcard match",
			condition: &SensorCondition{
				SensorName: "public_ip",
				Pattern:    "192.168.*",
			},
			sensors: map[string]Sensor{
				"public_ip": &MockSensor{
					name:       "public_ip",
					sensorType: SensorTypeString,
					value:      "192.168.100.200",
				},
			},
			want: true,
		},
		{
			name: "No match",
			condition: &SensorCondition{
				SensorName: "public_ip",
				Pattern:    "10.0.0.0/8",
			},
			sensors: map[string]Sensor{
				"public_ip": &MockSensor{
					name:       "public_ip",
					sensorType: SensorTypeString,
					value:      "192.168.1.1",
				},
			},
			want: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := tt.condition.Evaluate(context.Background(), tt.sensors)
			if err != nil {
				t.Errorf("Evaluate() error = %v", err)
				return
			}
			if got != tt.want {
				t.Errorf("Evaluate() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestGroupCondition_All(t *testing.T) {
	tests := []struct {
		name      string
		condition *GroupCondition
		sensors   map[string]Sensor
		want      bool
	}{
		{
			name: "All conditions true",
			condition: NewAllCondition(
				&SensorCondition{
					SensorName: "online",
					BoolValue:  boolPtr(true),
				},
				&SensorCondition{
					SensorName: "public_ip",
					Pattern:    "192.168.1.0/24",
				},
			),
			sensors: map[string]Sensor{
				"online": &MockSensor{
					name:       "online",
					sensorType: SensorTypeBoolean,
					value:      true,
				},
				"public_ip": &MockSensor{
					name:       "public_ip",
					sensorType: SensorTypeString,
					value:      "192.168.1.100",
				},
			},
			want: true,
		},
		{
			name: "One condition false",
			condition: NewAllCondition(
				&SensorCondition{
					SensorName: "online",
					BoolValue:  boolPtr(true),
				},
				&SensorCondition{
					SensorName: "public_ip",
					Pattern:    "192.168.1.0/24",
				},
			),
			sensors: map[string]Sensor{
				"online": &MockSensor{
					name:       "online",
					sensorType: SensorTypeBoolean,
					value:      false, // This is false!
				},
				"public_ip": &MockSensor{
					name:       "public_ip",
					sensorType: SensorTypeString,
					value:      "192.168.1.100",
				},
			},
			want: false,
		},
		{
			name: "Empty all group (should be true)",
			condition: NewAllCondition(),
			sensors:   map[string]Sensor{},
			want:      true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := tt.condition.Evaluate(context.Background(), tt.sensors)
			if err != nil {
				t.Errorf("Evaluate() error = %v", err)
				return
			}
			if got != tt.want {
				t.Errorf("Evaluate() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestGroupCondition_Any(t *testing.T) {
	tests := []struct {
		name      string
		condition *GroupCondition
		sensors   map[string]Sensor
		want      bool
	}{
		{
			name: "First condition true",
			condition: NewAnyCondition(
				&SensorCondition{
					SensorName: "public_ip",
					Pattern:    "192.168.1.0/24",
				},
				&SensorCondition{
					SensorName: "public_ip",
					Pattern:    "10.0.0.0/8",
				},
			),
			sensors: map[string]Sensor{
				"public_ip": &MockSensor{
					name:       "public_ip",
					sensorType: SensorTypeString,
					value:      "192.168.1.100",
				},
			},
			want: true,
		},
		{
			name: "Second condition true",
			condition: NewAnyCondition(
				&SensorCondition{
					SensorName: "public_ip",
					Pattern:    "192.168.1.0/24",
				},
				&SensorCondition{
					SensorName: "public_ip",
					Pattern:    "10.0.0.0/8",
				},
			),
			sensors: map[string]Sensor{
				"public_ip": &MockSensor{
					name:       "public_ip",
					sensorType: SensorTypeString,
					value:      "10.0.1.50",
				},
			},
			want: true,
		},
		{
			name: "No conditions true",
			condition: NewAnyCondition(
				&SensorCondition{
					SensorName: "public_ip",
					Pattern:    "192.168.1.0/24",
				},
				&SensorCondition{
					SensorName: "public_ip",
					Pattern:    "10.0.0.0/8",
				},
			),
			sensors: map[string]Sensor{
				"public_ip": &MockSensor{
					name:       "public_ip",
					sensorType: SensorTypeString,
					value:      "172.16.0.1",
				},
			},
			want: false,
		},
		{
			name: "Empty any group (should be false)",
			condition: NewAnyCondition(),
			sensors:   map[string]Sensor{},
			want:      false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := tt.condition.Evaluate(context.Background(), tt.sensors)
			if err != nil {
				t.Errorf("Evaluate() error = %v", err)
				return
			}
			if got != tt.want {
				t.Errorf("Evaluate() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestGroupCondition_Nested(t *testing.T) {
	// Test: all { online true, any { ip1, ip2 } }
	// This should match when online AND (ip1 OR ip2)
	condition := NewAllCondition(
		&SensorCondition{
			SensorName: "online",
			BoolValue:  boolPtr(true),
		},
		NewAnyCondition(
			&SensorCondition{
				SensorName: "public_ip",
				Pattern:    "192.168.1.0/24",
			},
			&SensorCondition{
				SensorName: "public_ip",
				Pattern:    "10.0.0.0/8",
			},
		),
	)

	tests := []struct {
		name    string
		sensors map[string]Sensor
		want    bool
	}{
		{
			name: "Online and first IP range",
			sensors: map[string]Sensor{
				"online": &MockSensor{
					name:       "online",
					sensorType: SensorTypeBoolean,
					value:      true,
				},
				"public_ip": &MockSensor{
					name:       "public_ip",
					sensorType: SensorTypeString,
					value:      "192.168.1.100",
				},
			},
			want: true,
		},
		{
			name: "Online and second IP range",
			sensors: map[string]Sensor{
				"online": &MockSensor{
					name:       "online",
					sensorType: SensorTypeBoolean,
					value:      true,
				},
				"public_ip": &MockSensor{
					name:       "public_ip",
					sensorType: SensorTypeString,
					value:      "10.0.1.50",
				},
			},
			want: true,
		},
		{
			name: "Offline (online=false)",
			sensors: map[string]Sensor{
				"online": &MockSensor{
					name:       "online",
					sensorType: SensorTypeBoolean,
					value:      false,
				},
				"public_ip": &MockSensor{
					name:       "public_ip",
					sensorType: SensorTypeString,
					value:      "192.168.1.100",
				},
			},
			want: false,
		},
		{
			name: "Online but wrong IP",
			sensors: map[string]Sensor{
				"online": &MockSensor{
					name:       "online",
					sensorType: SensorTypeBoolean,
					value:      true,
				},
				"public_ip": &MockSensor{
					name:       "public_ip",
					sensorType: SensorTypeString,
					value:      "172.16.0.1",
				},
			},
			want: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := condition.Evaluate(context.Background(), tt.sensors)
			if err != nil {
				t.Errorf("Evaluate() error = %v", err)
				return
			}
			if got != tt.want {
				t.Errorf("Evaluate() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestConditionFromMap(t *testing.T) {
	tests := []struct {
		name       string
		conditions map[string][]string
		sensors    map[string]Sensor
		want       bool
	}{
		{
			name: "Single IP condition",
			conditions: map[string][]string{
				"public_ip": {"192.168.1.100"},
			},
			sensors: map[string]Sensor{
				"public_ipv4": &MockSensor{
					name:       "public_ipv4",
					sensorType: SensorTypeString,
					value:      "192.168.1.100",
				},
			},
			want: true,
		},
		{
			name: "Multiple IP conditions (OR)",
			conditions: map[string][]string{
				"public_ip": {"192.168.1.100", "10.0.0.1"},
			},
			sensors: map[string]Sensor{
				"public_ipv4": &MockSensor{
					name:       "public_ipv4",
					sensorType: SensorTypeString,
					value:      "10.0.0.1",
				},
			},
			want: true,
		},
		{
			name: "Multiple sensor types (AND)",
			conditions: map[string][]string{
				"public_ip": {"192.168.1.0/24"},
				"env:HOME":  {"yes"},
			},
			sensors: map[string]Sensor{
				"public_ipv4": &MockSensor{
					name:       "public_ipv4",
					sensorType: SensorTypeString,
					value:      "192.168.1.50",
				},
				"env:HOME": &MockSensor{
					name:       "env:HOME",
					sensorType: SensorTypeString,
					value:      "yes",
				},
			},
			want: true,
		},
		{
			name: "Empty conditions (always true)",
			conditions: map[string][]string{},
			sensors: map[string]Sensor{
				"public_ipv4": &MockSensor{
					name:       "public_ipv4",
					sensorType: SensorTypeString,
					value:      "1.2.3.4",
				},
			},
			want: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			condition := ConditionFromMap(tt.conditions)
			got, err := condition.Evaluate(context.Background(), tt.sensors)
			if err != nil {
				t.Errorf("Evaluate() error = %v", err)
				return
			}
			if got != tt.want {
				t.Errorf("Evaluate() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestNewBooleanCondition(t *testing.T) {
	t.Run("true value", func(t *testing.T) {
		cond := NewBooleanCondition("online", true)
		if cond.SensorName != "online" {
			t.Errorf("SensorName = %q, want %q", cond.SensorName, "online")
		}
		if cond.BoolValue == nil || *cond.BoolValue != true {
			t.Errorf("BoolValue = %v, want true", cond.BoolValue)
		}
		if cond.Pattern != "" {
			t.Errorf("Pattern = %q, want empty", cond.Pattern)
		}
	})

	t.Run("false value", func(t *testing.T) {
		cond := NewBooleanCondition("online", false)
		if cond.BoolValue == nil || *cond.BoolValue != false {
			t.Errorf("BoolValue = %v, want false", cond.BoolValue)
		}
	})
}

func TestSensorCondition_String(t *testing.T) {
	tests := []struct {
		name string
		cond *SensorCondition
		want string
	}{
		{
			name: "boolean condition",
			cond: NewBooleanCondition("online", true),
			want: "online=true",
		},
		{
			name: "boolean condition false",
			cond: NewBooleanCondition("online", false),
			want: "online=false",
		},
		{
			name: "pattern condition",
			cond: NewSensorCondition("public_ip", "192.168.*"),
			want: "public_ip~192.168.*",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := tt.cond.String()
			if got != tt.want {
				t.Errorf("String() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestGroupCondition_String(t *testing.T) {
	tests := []struct {
		name string
		cond *GroupCondition
		want string
	}{
		{
			name: "all group",
			cond: NewAllCondition(
				NewSensorCondition("ip", "1.2.3.4"),
				NewBooleanCondition("online", true),
			),
			want: "all{ip~1.2.3.4, online=true}",
		},
		{
			name: "any group",
			cond: NewAnyCondition(
				NewSensorCondition("ip", "1.0.0.0/8"),
				NewSensorCondition("ip", "2.0.0.0/8"),
			),
			want: "any{ip~1.0.0.0/8, ip~2.0.0.0/8}",
		},
		{
			name: "empty group",
			cond: NewAllCondition(),
			want: "all{}",
		},
		{
			name: "nested group",
			cond: NewAllCondition(
				NewBooleanCondition("online", true),
				NewAnyCondition(
					NewSensorCondition("ip", "10.*"),
					NewSensorCondition("ip", "172.*"),
				),
			),
			want: "all{online=true, any{ip~10.*, ip~172.*}}",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := tt.cond.String()
			if got != tt.want {
				t.Errorf("String() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestSensorCondition_Evaluate_ErrorCases(t *testing.T) {
	ctx := context.Background()

	t.Run("bool condition on string sensor", func(t *testing.T) {
		cond := NewBooleanCondition("sensor", true)
		sensors := map[string]Sensor{
			"sensor": &MockSensor{
				name:       "sensor",
				sensorType: SensorTypeString,
				value:      "not-a-bool",
			},
		}
		_, err := cond.Evaluate(ctx, sensors)
		if err == nil {
			t.Error("expected error for bool condition on string sensor")
		}
	})

	t.Run("string condition on bool sensor", func(t *testing.T) {
		cond := NewSensorCondition("sensor", "pattern")
		sensors := map[string]Sensor{
			"sensor": &MockSensor{
				name:       "sensor",
				sensorType: SensorTypeBoolean,
				value:      true,
			},
		}
		_, err := cond.Evaluate(ctx, sensors)
		if err == nil {
			t.Error("expected error for string condition on bool sensor")
		}
	})

	t.Run("empty string value does not match wildcard", func(t *testing.T) {
		cond := NewSensorCondition("sensor", "*")
		sensors := map[string]Sensor{
			"sensor": &MockSensor{
				name:       "sensor",
				sensorType: SensorTypeString,
				value:      "",
			},
		}
		got, err := cond.Evaluate(ctx, sensors)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if got != false {
			t.Error("expected false for empty string value against wildcard")
		}
	})

	t.Run("no-cache sensor falls back to Check", func(t *testing.T) {
		cond := NewSensorCondition("sensor", "hello")
		sensors := map[string]Sensor{
			"sensor": &MockSensorNoCache{
				MockSensor: MockSensor{
					name:       "sensor",
					sensorType: SensorTypeString,
					value:      "hello",
				},
			},
		}
		got, err := cond.Evaluate(ctx, sensors)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if got != true {
			t.Error("expected true after fallback to Check()")
		}
	})
}

func TestGroupCondition_Evaluate_UnknownOperator(t *testing.T) {
	cond := &GroupCondition{
		Operator: "xor",
		Conditions: []Condition{
			NewBooleanCondition("online", true),
		},
	}
	sensors := map[string]Sensor{
		"online": &MockSensor{
			name:       "online",
			sensorType: SensorTypeBoolean,
			value:      true,
		},
	}

	_, err := cond.Evaluate(context.Background(), sensors)
	if err == nil {
		t.Error("expected error for unknown operator")
	}
}

func TestExtractRequiredSensors(t *testing.T) {
	tests := []struct {
		name string
		cond Condition
		want map[string]bool
	}{
		{
			name: "nil condition",
			cond: nil,
			want: nil,
		},
		{
			name: "single sensor",
			cond: NewSensorCondition("public_ip", "1.2.3.4"),
			want: map[string]bool{"public_ip": true},
		},
		{
			name: "group with multiple sensors",
			cond: NewAllCondition(
				NewBooleanCondition("online", true),
				NewSensorCondition("public_ip", "1.2.3.4"),
			),
			want: map[string]bool{"online": true, "public_ip": true},
		},
		{
			name: "nested with dedup",
			cond: NewAllCondition(
				NewSensorCondition("public_ip", "1.2.3.4"),
				NewAnyCondition(
					NewSensorCondition("public_ip", "10.0.0.0/8"),
					NewSensorCondition("ssid", "HomeNet"),
				),
			),
			want: map[string]bool{"public_ip": true, "ssid": true},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := ExtractRequiredSensors(tt.cond)
			if tt.want == nil {
				if got != nil {
					t.Errorf("expected nil, got %v", got)
				}
				return
			}
			gotMap := make(map[string]bool)
			for _, s := range got {
				gotMap[s] = true
			}
			for k := range tt.want {
				if !gotMap[k] {
					t.Errorf("missing sensor %q", k)
				}
			}
			if len(gotMap) != len(tt.want) {
				t.Errorf("got %d sensors, want %d", len(gotMap), len(tt.want))
			}
		})
	}
}

func TestExtractPatternsForSensor(t *testing.T) {
	tests := []struct {
		name       string
		cond       Condition
		sensorName string
		want       map[string]bool
	}{
		{
			name:       "matching sensor",
			cond:       NewSensorCondition("public_ip", "1.2.3.4"),
			sensorName: "public_ip",
			want:       map[string]bool{"1.2.3.4": true},
		},
		{
			name:       "non-matching sensor",
			cond:       NewSensorCondition("public_ip", "1.2.3.4"),
			sensorName: "ssid",
			want:       map[string]bool{},
		},
		{
			name: "nested patterns with dedup",
			cond: NewAllCondition(
				NewSensorCondition("public_ip", "1.2.3.4"),
				NewAnyCondition(
					NewSensorCondition("public_ip", "10.0.0.0/8"),
					NewSensorCondition("public_ip", "1.2.3.4"), // duplicate
				),
			),
			sensorName: "public_ip",
			want:       map[string]bool{"1.2.3.4": true, "10.0.0.0/8": true},
		},
		{
			name:       "boolean condition excluded",
			cond:       NewBooleanCondition("online", true),
			sensorName: "online",
			want:       map[string]bool{},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := ExtractPatternsForSensor(tt.cond, tt.sensorName)
			gotMap := make(map[string]bool)
			for _, p := range got {
				gotMap[p] = true
			}
			for k := range tt.want {
				if !gotMap[k] {
					t.Errorf("missing pattern %q", k)
				}
			}
			if len(gotMap) != len(tt.want) {
				t.Errorf("got %d patterns, want %d", len(gotMap), len(tt.want))
			}
		})
	}
}

func TestConditionFromMap_EdgeCases(t *testing.T) {
	ctx := context.Background()

	t.Run("sensor with empty patterns is skipped", func(t *testing.T) {
		cond := ConditionFromMap(map[string][]string{
			"public_ip": {},
			"ssid":      {"HomeNet"},
		})
		sensors := map[string]Sensor{
			"ssid": &MockSensor{
				name:       "ssid",
				sensorType: SensorTypeString,
				value:      "HomeNet",
			},
		}
		got, err := cond.Evaluate(ctx, sensors)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if !got {
			t.Error("expected true: empty patterns should be skipped")
		}
	})

	t.Run("all sensors have empty patterns", func(t *testing.T) {
		cond := ConditionFromMap(map[string][]string{
			"public_ip": {},
			"ssid":      {},
		})
		// Should be vacuously true (like empty conditions)
		got, err := cond.Evaluate(ctx, map[string]Sensor{})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if !got {
			t.Error("expected true: all empty patterns should be vacuously true")
		}
	})
}

// Helper function to create bool pointers
func boolPtr(b bool) *bool {
	return &b
}
