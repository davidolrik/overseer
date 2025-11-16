package security

import (
	"context"
	"fmt"
	"strings"
)

// Condition represents a rule condition that can be evaluated
type Condition interface {
	// Evaluate checks if this condition is satisfied
	Evaluate(ctx context.Context, sensors map[string]Sensor) (bool, error)
}

// SensorCondition checks a single sensor value
type SensorCondition struct {
	SensorName string      // Name of the sensor to check
	Pattern    string      // Pattern to match for string sensors
	BoolValue  *bool       // Expected value for boolean sensors (nil if not a boolean condition)
}

// Evaluate checks if the sensor value matches the condition
func (c *SensorCondition) Evaluate(ctx context.Context, sensors map[string]Sensor) (bool, error) {
	sensor, exists := sensors[c.SensorName]
	if !exists {
		return false, nil // Sensor not found, condition fails
	}

	// Use cached sensor value instead of calling Check() again
	// The manager already called Check() on all active sensors before evaluation
	lastValue := sensor.GetLastValue()
	if lastValue == nil {
		// No value yet, try checking once
		var err error
		value, err := sensor.Check(ctx)
		if err != nil {
			return false, fmt.Errorf("failed to check sensor %s: %w", c.SensorName, err)
		}
		lastValue = &value
	}
	value := *lastValue

	// Handle boolean sensors
	if c.BoolValue != nil {
		if value.Type != SensorTypeBoolean {
			return false, fmt.Errorf("expected boolean sensor but got %s", value.Type)
		}
		return value.Bool() == *c.BoolValue, nil
	}

	// Handle string sensors with pattern matching
	if value.Type != SensorTypeString {
		return false, fmt.Errorf("expected string sensor but got %s", value.Type)
	}

	// Empty sensor values don't match any pattern (including wildcards)
	strValue := value.String()
	if strValue == "" {
		return false, nil
	}

	return matchesPattern(strValue, c.Pattern), nil
}

// String returns a string representation of the condition
func (c *SensorCondition) String() string {
	if c.BoolValue != nil {
		return fmt.Sprintf("%s=%v", c.SensorName, *c.BoolValue)
	}
	return fmt.Sprintf("%s~%s", c.SensorName, c.Pattern)
}

// GroupCondition combines multiple conditions with AND or OR logic
type GroupCondition struct {
	Operator   string      // "all" (AND) or "any" (OR)
	Conditions []Condition // Nested conditions
}

// Evaluate checks if the group condition is satisfied
func (c *GroupCondition) Evaluate(ctx context.Context, sensors map[string]Sensor) (bool, error) {
	if len(c.Conditions) == 0 {
		// Empty "all" group is vacuously true (all zero conditions are satisfied)
		// Empty "any" group is false (there are no conditions to satisfy)
		if c.Operator == "any" {
			return false, nil
		}
		return true, nil
	}

	switch c.Operator {
	case "all":
		// All conditions must be true (AND logic)
		for _, cond := range c.Conditions {
			match, err := cond.Evaluate(ctx, sensors)
			if err != nil {
				return false, err
			}
			if !match {
				return false, nil // One failed, entire group fails
			}
		}
		return true, nil

	case "any":
		// At least one condition must be true (OR logic)
		for _, cond := range c.Conditions {
			match, err := cond.Evaluate(ctx, sensors)
			if err != nil {
				return false, err
			}
			if match {
				return true, nil // One succeeded, entire group succeeds
			}
		}
		return false, nil // None matched

	default:
		return false, fmt.Errorf("unknown group operator: %s", c.Operator)
	}
}

// String returns a string representation of the condition
func (c *GroupCondition) String() string {
	parts := make([]string, len(c.Conditions))
	for i, cond := range c.Conditions {
		parts[i] = fmt.Sprintf("%v", cond)
	}
	return fmt.Sprintf("%s{%s}", c.Operator, strings.Join(parts, ", "))
}

// NewSensorCondition creates a condition for a string sensor with pattern matching
func NewSensorCondition(sensorName, pattern string) *SensorCondition {
	return &SensorCondition{
		SensorName: sensorName,
		Pattern:    pattern,
		BoolValue:  nil,
	}
}

// NewBooleanCondition creates a condition for a boolean sensor
func NewBooleanCondition(sensorName string, value bool) *SensorCondition {
	return &SensorCondition{
		SensorName: sensorName,
		BoolValue:  &value,
	}
}

// NewAllCondition creates an AND group condition
func NewAllCondition(conditions ...Condition) *GroupCondition {
	return &GroupCondition{
		Operator:   "all",
		Conditions: conditions,
	}
}

// NewAnyCondition creates an OR group condition
func NewAnyCondition(conditions ...Condition) *GroupCondition {
	return &GroupCondition{
		Operator:   "any",
		Conditions: conditions,
	}
}

// ConditionFromMap creates conditions from a map[string][]string format
// This provides backward compatibility with simple condition format
func ConditionFromMap(conditions map[string][]string) Condition {
	if len(conditions) == 0 {
		// Empty conditions always match (fallback rule)
		return NewAllCondition()
	}

	// Convert map to conditions
	var allConditions []Condition

	for sensorName, patterns := range conditions {
		if len(patterns) == 0 {
			continue
		}

		if len(patterns) == 1 {
			// Single pattern, create sensor condition directly
			allConditions = append(allConditions, NewSensorCondition(sensorName, patterns[0]))
		} else {
			// Multiple patterns for same sensor = OR (any)
			anyConditions := make([]Condition, len(patterns))
			for i, pattern := range patterns {
				anyConditions[i] = NewSensorCondition(sensorName, pattern)
			}
			allConditions = append(allConditions, NewAnyCondition(anyConditions...))
		}
	}

	if len(allConditions) == 0 {
		// All sensors had empty patterns, treat as always matching
		return NewAllCondition()
	}

	if len(allConditions) == 1 {
		return allConditions[0]
	}

	// Multiple sensors = AND (all)
	return NewAllCondition(allConditions...)
}

// ExtractRequiredSensors recursively extracts all sensor names from a condition tree
// This is used to determine which sensors need to be created
func ExtractRequiredSensors(cond Condition) []string {
	if cond == nil {
		return nil
	}

	sensors := make(map[string]bool) // Use map to deduplicate
	extractSensorsRecursive(cond, sensors)

	// Convert map to slice
	result := make([]string, 0, len(sensors))
	for sensor := range sensors {
		result = append(result, sensor)
	}
	return result
}

// extractSensorsRecursive is the internal recursive implementation
func extractSensorsRecursive(cond Condition, sensors map[string]bool) {
	switch c := cond.(type) {
	case *SensorCondition:
		sensors[c.SensorName] = true
	case *GroupCondition:
		for _, child := range c.Conditions {
			extractSensorsRecursive(child, sensors)
		}
	}
}
