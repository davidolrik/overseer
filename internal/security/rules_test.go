package security

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
			// Create mock sensor
			mockSensor := &MockSensor{
				name:      "public_ip",
				sensorType: SensorTypeString,
				value:     tt.sensorIP,
			}

			sensors := map[string]Sensor{
				"public_ip": mockSensor,
			}

			result := re.Evaluate(context.Background(), sensors)
			if result.Context != tt.wantContext {
				t.Errorf("Evaluate() context = %v, want %v", result.Context, tt.wantContext)
			}
		})
	}
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
