package awareness

import (
	"context"
	"fmt"
	"net"
	"strings"
)

// Location represents a physical or network location
type Location struct {
	Name        string              // Location name (e.g., "hq", "home")
	DisplayName string              // Human-friendly display name
	Conditions  map[string][]string // Simple sensor conditions (e.g., "public_ip": ["192.168.1.1", "10.0.0.0/24"])
	Condition   Condition           // New structured condition (supports nesting with any/all)
	Environment map[string]string   // Custom environment variables to export
}

// Rule represents a context rule that maps sensor conditions to actions
type Rule struct {
	Name        string              // Context name (e.g., "home", "office")
	DisplayName string              // Human-friendly display name
	Locations   []string            // Location names this context can match
	Conditions  map[string][]string // Simple sensor conditions (e.g., "public_ip": ["192.168.1.1", "10.0.0.0/24"])
	Condition   Condition           // New structured condition (supports nesting with any/all)
	Actions     RuleActions         // Actions to take when this rule matches
	Environment map[string]string   // Custom environment variables to export
}

// RuleActions defines what to do when a rule matches
type RuleActions struct {
	Connect    []string // Tunnels to connect
	Disconnect []string // Tunnels to disconnect
}

// EvaluationResult contains the result of rule evaluation
type EvaluationResult struct {
	Context      string  // Matched context name
	Location     string  // Matched location name (if any)
	Rule         *Rule   // The matched rule
	MatchedBy    string  // What matched: "location" or "conditions"
}

// RuleEngine evaluates rules against sensor values to determine context
type RuleEngine struct {
	rules     []Rule
	locations map[string]Location
}

// NewRuleEngine creates a new rule evaluation engine
// Rules are evaluated in the order they are provided (first match wins)
func NewRuleEngine(rules []Rule, locations map[string]Location) *RuleEngine {
	return &RuleEngine{
		rules:     rules,
		locations: locations,
	}
}

// determineLocation checks special locations (offline) first, then all other locations, then falls back to unknown
func (re *RuleEngine) determineLocation(ctx context.Context, sensorMap map[string]Sensor) string {
	// Check for special "offline" location first
	if offlineLocation, exists := re.locations["offline"]; exists {
		if offlineLocation.Condition != nil {
			if result, err := offlineLocation.Condition.Evaluate(ctx, sensorMap); err == nil && result {
				return "offline"
			}
		} else if len(offlineLocation.Conditions) > 0 {
			cond := ConditionFromMap(offlineLocation.Conditions)
			if cond != nil {
				if result, err := cond.Evaluate(ctx, sensorMap); err == nil && result {
					return "offline"
				}
			}
		}
	}

	// Check all other locations to find a match
	for locationName, location := range re.locations {
		// Skip "offline" and "unknown" - already handled
		if locationName == "offline" || locationName == "unknown" {
			continue
		}

		// Check if location conditions match
		matched := false
		if location.Condition != nil {
			result, err := location.Condition.Evaluate(ctx, sensorMap)
			if err == nil && result {
				matched = true
			}
		} else if len(location.Conditions) > 0 {
			cond := ConditionFromMap(location.Conditions)
			if cond != nil {
				result, err := cond.Evaluate(ctx, sensorMap)
				if err == nil && result {
					matched = true
				}
			}
		}

		if matched {
			return locationName
		}
	}

	// Default to unknown
	return "unknown"
}

// Evaluate determines which context matches the current sensor values
func (re *RuleEngine) Evaluate(ctx context.Context, sensorMap map[string]Sensor) *EvaluationResult {
	// Try each rule in order (first match wins)
	for i := range re.rules {
		rule := &re.rules[i]

		// Check if any locations match first
		for _, locationName := range rule.Locations {
			location, exists := re.locations[locationName]
			if !exists {
				continue
			}

			// Check if location conditions match (try new condition first, fallback to simple)
			matched := false
			if location.Condition != nil {
				result, err := location.Condition.Evaluate(ctx, sensorMap)
				if err == nil && result {
					matched = true
				}
			} else if len(location.Conditions) > 0 {
				// Simple format: convert to condition and evaluate
				cond := ConditionFromMap(location.Conditions)
				if cond != nil {
					result, err := cond.Evaluate(ctx, sensorMap)
					if err == nil && result {
						matched = true
					}
				}
			}

			if matched {
				return &EvaluationResult{
					Context:   rule.Name,
					Location:  location.Name,
					Rule:      rule,
					MatchedBy: "location",
				}
			}
		}

		// Empty conditions means this is a default/fallback rule
		if rule.Condition == nil && len(rule.Conditions) == 0 && len(rule.Locations) == 0 {
			return &EvaluationResult{
				Context:   rule.Name,
				Location:  re.determineLocation(ctx, sensorMap),
				Rule:      rule,
				MatchedBy: "fallback",
			}
		}

		// Check if rule conditions match directly (try new condition first, fallback to simple)
		if rule.Condition != nil {
			result, err := rule.Condition.Evaluate(ctx, sensorMap)
			if err == nil && result {
				return &EvaluationResult{
					Context:   rule.Name,
					Location:  re.determineLocation(ctx, sensorMap),
					Rule:      rule,
					MatchedBy: "conditions",
				}
			}
		} else if len(rule.Conditions) > 0 {
			// Simple format: convert to condition and evaluate
			cond := ConditionFromMap(rule.Conditions)
			if cond != nil {
				result, err := cond.Evaluate(ctx, sensorMap)
				if err == nil && result {
					return &EvaluationResult{
						Context:   rule.Name,
						Location:  re.determineLocation(ctx, sensorMap),
						Rule:      rule,
						MatchedBy: "conditions",
					}
				}
			}
		}
	}

	// No rule matched, return unknown context and location
	return &EvaluationResult{
		Context:   "unknown",
		Location:  re.determineLocation(ctx, sensorMap),
		Rule:      nil,
		MatchedBy: "none",
	}
}

// matchesConditions checks if sensor values satisfy all rule conditions
// For each sensor, at least one of the condition values must match (OR logic within a sensor)
// All sensors must match (AND logic between sensors)
func (re *RuleEngine) matchesConditions(conditions map[string][]string, sensors map[string]SensorValue) bool {
	for sensorKey, conditionValues := range conditions {
		sensor, exists := sensors[sensorKey]
		if !exists {
			return false // Required sensor not present
		}

		// Get sensor value as string
		sensorValueStr := sensor.String()
		if sensorValueStr == "" {
			return false
		}

		// Check if at least one condition value matches (OR logic)
		matched := false
		for _, conditionValue := range conditionValues {
			if matchesPattern(sensorValueStr, conditionValue) {
				matched = true
				break
			}
		}

		// If no condition value matched for this sensor, the rule doesn't match
		if !matched {
			return false
		}
	}

	return true // All sensors matched at least one condition
}

// matchesPattern checks if a value matches a pattern
// Supports exact match and CIDR notation for IPs
func matchesPattern(value, pattern string) bool {
	// Try exact match first
	if value == pattern {
		return true
	}

	// Check if pattern is CIDR notation
	if strings.Contains(pattern, "/") {
		return matchesCIDR(value, pattern)
	}

	// Check if pattern contains wildcards
	if strings.Contains(pattern, "*") {
		return matchesWildcard(value, pattern)
	}

	return false
}

// matchesCIDR checks if an IP address is within a CIDR range
func matchesCIDR(ip, cidr string) bool {
	_, network, err := net.ParseCIDR(cidr)
	if err != nil {
		return false
	}

	parsedIP := net.ParseIP(ip)
	if parsedIP == nil {
		return false
	}

	return network.Contains(parsedIP)
}

// matchesWildcard checks if a value matches a wildcard pattern
// Supports simple wildcards with * (e.g., "192.168.*", "*.example.com")
// Wildcards do not match empty strings
func matchesWildcard(value, pattern string) bool {
	// Wildcards should not match empty values
	if value == "" {
		return false
	}

	// Split pattern by *
	parts := strings.Split(pattern, "*")

	// Check if value starts with first part
	if !strings.HasPrefix(value, parts[0]) {
		return false
	}
	value = value[len(parts[0]):]

	// Check middle parts
	for i := 1; i < len(parts)-1; i++ {
		idx := strings.Index(value, parts[i])
		if idx == -1 {
			return false
		}
		value = value[idx+len(parts[i]):]
	}

	// Check if value ends with last part
	if len(parts) > 1 {
		return strings.HasSuffix(value, parts[len(parts)-1])
	}

	return true
}

// GetRuleByName returns a rule by its name
func (re *RuleEngine) GetRuleByName(name string) (*Rule, error) {
	for i := range re.rules {
		if re.rules[i].Name == name {
			return &re.rules[i], nil
		}
	}
	return nil, fmt.Errorf("rule not found: %s", name)
}

// GetLocation returns a location by its name
func (re *RuleEngine) GetLocation(name string) *Location {
	if loc, exists := re.locations[name]; exists {
		return &loc
	}
	return nil
}

// GetAllRules returns all configured rules
func (re *RuleEngine) GetAllRules() []Rule {
	rules := make([]Rule, len(re.rules))
	copy(rules, re.rules)
	return rules
}
