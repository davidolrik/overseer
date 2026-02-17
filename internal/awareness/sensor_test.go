package awareness

import (
	"testing"
	"time"
)

// MockSensorListener records calls to OnSensorChange for test verification
type MockSensorListener struct {
	calls []sensorChangeCall
}

type sensorChangeCall struct {
	sensor   Sensor
	oldValue SensorValue
	newValue SensorValue
}

func (m *MockSensorListener) OnSensorChange(sensor Sensor, oldValue, newValue SensorValue) {
	m.calls = append(m.calls, sensorChangeCall{
		sensor:   sensor,
		oldValue: oldValue,
		newValue: newValue,
	})
}

func TestNewSensorValue(t *testing.T) {
	t.Run("string type", func(t *testing.T) {
		before := time.Now()
		sv := NewSensorValue("public_ip", SensorTypeString, "1.2.3.4")
		after := time.Now()

		if sv.Key != "public_ip" {
			t.Errorf("Key = %q, want %q", sv.Key, "public_ip")
		}
		if sv.Type != SensorTypeString {
			t.Errorf("Type = %q, want %q", sv.Type, SensorTypeString)
		}
		if sv.Value != "1.2.3.4" {
			t.Errorf("Value = %v, want %q", sv.Value, "1.2.3.4")
		}
		if sv.Timestamp.Before(before) || sv.Timestamp.After(after) {
			t.Errorf("Timestamp %v not between %v and %v", sv.Timestamp, before, after)
		}
	})

	t.Run("boolean type", func(t *testing.T) {
		sv := NewSensorValue("online", SensorTypeBoolean, true)

		if sv.Key != "online" {
			t.Errorf("Key = %q, want %q", sv.Key, "online")
		}
		if sv.Type != SensorTypeBoolean {
			t.Errorf("Type = %q, want %q", sv.Type, SensorTypeBoolean)
		}
		if sv.Value != true {
			t.Errorf("Value = %v, want true", sv.Value)
		}
	})
}

func TestSensorValue_String(t *testing.T) {
	tests := []struct {
		name  string
		value SensorValue
		want  string
	}{
		{
			name:  "nil value",
			value: SensorValue{Value: nil},
			want:  "",
		},
		{
			name:  "string value",
			value: SensorValue{Value: "hello"},
			want:  "hello",
		},
		{
			name:  "non-string value uses Sprintf",
			value: SensorValue{Value: 42},
			want:  "42",
		},
		{
			name:  "boolean value uses Sprintf",
			value: SensorValue{Value: true},
			want:  "true",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := tt.value.String()
			if got != tt.want {
				t.Errorf("String() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestSensorValue_Bool(t *testing.T) {
	tests := []struct {
		name  string
		value SensorValue
		want  bool
	}{
		{
			name:  "nil value",
			value: SensorValue{Value: nil},
			want:  false,
		},
		{
			name:  "true",
			value: SensorValue{Value: true},
			want:  true,
		},
		{
			name:  "false",
			value: SensorValue{Value: false},
			want:  false,
		},
		{
			name:  "non-bool returns false",
			value: SensorValue{Value: "not a bool"},
			want:  false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := tt.value.Bool()
			if got != tt.want {
				t.Errorf("Bool() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestSensorValue_Equals(t *testing.T) {
	tests := []struct {
		name string
		a    SensorValue
		b    SensorValue
		want bool
	}{
		{
			name: "same bool values",
			a:    SensorValue{Type: SensorTypeBoolean, Value: true},
			b:    SensorValue{Type: SensorTypeBoolean, Value: true},
			want: true,
		},
		{
			name: "different bool values",
			a:    SensorValue{Type: SensorTypeBoolean, Value: true},
			b:    SensorValue{Type: SensorTypeBoolean, Value: false},
			want: false,
		},
		{
			name: "same string values",
			a:    SensorValue{Type: SensorTypeString, Value: "hello"},
			b:    SensorValue{Type: SensorTypeString, Value: "hello"},
			want: true,
		},
		{
			name: "different string values",
			a:    SensorValue{Type: SensorTypeString, Value: "hello"},
			b:    SensorValue{Type: SensorTypeString, Value: "world"},
			want: false,
		},
		{
			name: "different types",
			a:    SensorValue{Type: SensorTypeBoolean, Value: true},
			b:    SensorValue{Type: SensorTypeString, Value: "true"},
			want: false,
		},
		{
			name: "unknown type uses direct comparison",
			a:    SensorValue{Type: "custom", Value: 42},
			b:    SensorValue{Type: "custom", Value: 42},
			want: true,
		},
		{
			name: "unknown type different values",
			a:    SensorValue{Type: "custom", Value: 42},
			b:    SensorValue{Type: "custom", Value: 99},
			want: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := tt.a.Equals(tt.b)
			if got != tt.want {
				t.Errorf("Equals() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestNewBaseSensor(t *testing.T) {
	bs := NewBaseSensor("test_sensor", SensorTypeString)

	if bs.Name() != "test_sensor" {
		t.Errorf("Name() = %q, want %q", bs.Name(), "test_sensor")
	}
	if bs.Type() != SensorTypeString {
		t.Errorf("Type() = %q, want %q", bs.Type(), SensorTypeString)
	}
	if bs.GetLastValue() != nil {
		t.Errorf("GetLastValue() = %v, want nil", bs.GetLastValue())
	}

	// NotifyListeners with zero listeners should not panic
	sv := NewSensorValue("test_sensor", SensorTypeString, "value")
	bs.NotifyListeners(nil, sv, sv)
}

func TestBaseSensor_GetSetLastValue(t *testing.T) {
	bs := NewBaseSensor("test", SensorTypeString)

	if bs.GetLastValue() != nil {
		t.Fatal("expected nil before any SetLastValue")
	}

	v1 := NewSensorValue("test", SensorTypeString, "first")
	bs.SetLastValue(v1)

	got := bs.GetLastValue()
	if got == nil {
		t.Fatal("expected non-nil after SetLastValue")
	}
	if got.String() != "first" {
		t.Errorf("GetLastValue().String() = %q, want %q", got.String(), "first")
	}

	v2 := NewSensorValue("test", SensorTypeString, "second")
	bs.SetLastValue(v2)

	got = bs.GetLastValue()
	if got.String() != "second" {
		t.Errorf("GetLastValue().String() = %q after overwrite, want %q", got.String(), "second")
	}
}

func TestBaseSensor_Subscribe(t *testing.T) {
	bs := NewBaseSensor("test", SensorTypeBoolean)
	oldVal := NewSensorValue("test", SensorTypeBoolean, false)
	newVal := NewSensorValue("test", SensorTypeBoolean, true)

	t.Run("single listener notified", func(t *testing.T) {
		listener := &MockSensorListener{}
		bs.Subscribe(listener)
		bs.NotifyListeners(nil, oldVal, newVal)

		if len(listener.calls) != 1 {
			t.Fatalf("expected 1 call, got %d", len(listener.calls))
		}
		if listener.calls[0].oldValue.Bool() != false {
			t.Error("expected oldValue false")
		}
		if listener.calls[0].newValue.Bool() != true {
			t.Error("expected newValue true")
		}
	})

	t.Run("multiple listeners notified", func(t *testing.T) {
		bs2 := NewBaseSensor("test2", SensorTypeString)
		l1 := &MockSensorListener{}
		l2 := &MockSensorListener{}
		bs2.Subscribe(l1)
		bs2.Subscribe(l2)

		o := NewSensorValue("test2", SensorTypeString, "old")
		n := NewSensorValue("test2", SensorTypeString, "new")
		bs2.NotifyListeners(nil, o, n)

		if len(l1.calls) != 1 {
			t.Errorf("l1: expected 1 call, got %d", len(l1.calls))
		}
		if len(l2.calls) != 1 {
			t.Errorf("l2: expected 1 call, got %d", len(l2.calls))
		}
	})
}

func TestBaseSensor_Unsubscribe(t *testing.T) {
	t.Run("removes listener", func(t *testing.T) {
		bs := NewBaseSensor("test", SensorTypeBoolean)
		listener := &MockSensorListener{}
		bs.Subscribe(listener)
		bs.Unsubscribe(listener)

		o := NewSensorValue("test", SensorTypeBoolean, false)
		n := NewSensorValue("test", SensorTypeBoolean, true)
		bs.NotifyListeners(nil, o, n)

		if len(listener.calls) != 0 {
			t.Errorf("expected 0 calls after unsubscribe, got %d", len(listener.calls))
		}
	})

	t.Run("no-op for unknown listener", func(t *testing.T) {
		bs := NewBaseSensor("test", SensorTypeBoolean)
		known := &MockSensorListener{}
		unknown := &MockSensorListener{}
		bs.Subscribe(known)
		bs.Unsubscribe(unknown) // should not panic or affect known

		o := NewSensorValue("test", SensorTypeBoolean, false)
		n := NewSensorValue("test", SensorTypeBoolean, true)
		bs.NotifyListeners(nil, o, n)

		if len(known.calls) != 1 {
			t.Errorf("known listener: expected 1 call, got %d", len(known.calls))
		}
	})

	t.Run("removes correct one from multiple", func(t *testing.T) {
		bs := NewBaseSensor("test", SensorTypeString)
		l1 := &MockSensorListener{}
		l2 := &MockSensorListener{}
		l3 := &MockSensorListener{}
		bs.Subscribe(l1)
		bs.Subscribe(l2)
		bs.Subscribe(l3)

		bs.Unsubscribe(l2) // remove the middle one

		o := NewSensorValue("test", SensorTypeString, "a")
		n := NewSensorValue("test", SensorTypeString, "b")
		bs.NotifyListeners(nil, o, n)

		if len(l1.calls) != 1 {
			t.Errorf("l1: expected 1 call, got %d", len(l1.calls))
		}
		if len(l2.calls) != 0 {
			t.Errorf("l2: expected 0 calls, got %d", len(l2.calls))
		}
		if len(l3.calls) != 1 {
			t.Errorf("l3: expected 1 call, got %d", len(l3.calls))
		}
	})
}

func TestBaseSensor_NotifyListeners(t *testing.T) {
	t.Run("all listeners called with correct args", func(t *testing.T) {
		bs := NewBaseSensor("notify_test", SensorTypeString)
		l1 := &MockSensorListener{}
		l2 := &MockSensorListener{}
		bs.Subscribe(l1)
		bs.Subscribe(l2)

		// Use a MockSensor as the sensor arg so we can verify identity
		sensorArg := &MockSensor{name: "notify_test", sensorType: SensorTypeString, value: "new"}
		oldVal := NewSensorValue("notify_test", SensorTypeString, "old")
		newVal := NewSensorValue("notify_test", SensorTypeString, "new")
		bs.NotifyListeners(sensorArg, oldVal, newVal)

		for i, l := range []*MockSensorListener{l1, l2} {
			if len(l.calls) != 1 {
				t.Fatalf("listener %d: expected 1 call, got %d", i, len(l.calls))
			}
			call := l.calls[0]
			if call.sensor != sensorArg {
				t.Errorf("listener %d: sensor mismatch", i)
			}
			if call.oldValue.String() != "old" {
				t.Errorf("listener %d: oldValue = %q, want %q", i, call.oldValue.String(), "old")
			}
			if call.newValue.String() != "new" {
				t.Errorf("listener %d: newValue = %q, want %q", i, call.newValue.String(), "new")
			}
		}
	})

	t.Run("no panic with zero listeners", func(t *testing.T) {
		bs := NewBaseSensor("empty", SensorTypeBoolean)
		o := NewSensorValue("empty", SensorTypeBoolean, false)
		n := NewSensorValue("empty", SensorTypeBoolean, true)
		// Should not panic
		bs.NotifyListeners(nil, o, n)
	})
}
