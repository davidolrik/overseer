package state

import (
	"context"
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
