package security

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
				"public_ip": &MockSensor{
					name:       "public_ip",
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
				"public_ip": &MockSensor{
					name:       "public_ip",
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
				"public_ip": &MockSensor{
					name:       "public_ip",
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
				"public_ip": &MockSensor{
					name:       "public_ip",
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

// Helper function to create bool pointers
func boolPtr(b bool) *bool {
	return &b
}
