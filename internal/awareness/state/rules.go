package state

import (
	"net"
	"strings"
)

// Location represents a physical or network location
type Location struct {
	Name        string              // Location name (e.g., "hq", "home")
	DisplayName string              // Human-friendly display name
	Conditions  map[string][]string // Simple sensor conditions
	Condition   Condition           // Structured condition (supports nesting)
	Environment map[string]string   // Custom environment variables
}

// Rule represents a context rule that maps conditions to actions
type Rule struct {
	Name        string              // Context name (e.g., "home", "office")
	DisplayName string              // Human-friendly display name
	Locations   []string            // Location names this context can match
	Conditions  map[string][]string // Simple sensor conditions
	Condition   Condition           // Structured condition (supports nesting)
	Actions     RuleActions         // Actions to take when matched
	Environment map[string]string   // Custom environment variables
}

// RuleActions defines what to do when a rule matches
type RuleActions struct {
	Connect    []string // Tunnels to connect
	Disconnect []string // Tunnels to disconnect
}

// RuleResult contains the result of rule evaluation
type RuleResult struct {
	Context             string
	ContextDisplayName  string
	Location            string
	LocationDisplayName string
	MatchedRule         string
	Environment         map[string]string
}

// Condition represents a rule condition that can be evaluated
type Condition interface {
	// Evaluate checks if this condition is satisfied
	Evaluate(readings map[string]SensorReading, online bool) bool
}

// SensorCondition checks a single sensor value
type SensorCondition struct {
	SensorName string // Name of the sensor to check
	Pattern    string // Pattern to match for string sensors
	BoolValue  *bool  // Expected value for boolean sensors
}

// Evaluate checks if the sensor value matches the condition
func (c *SensorCondition) Evaluate(readings map[string]SensorReading, online bool) bool {
	// Special handling for "online" sensor - use the computed online state
	if c.SensorName == "online" {
		if c.BoolValue != nil {
			return online == *c.BoolValue
		}
		return false
	}

	// When offline, network-based sensors should not match
	// because we can't verify those values without connectivity
	if !online && isNetworkSensor(c.SensorName) {
		return false
	}

	reading, exists := readings[c.SensorName]
	if !exists {
		return false
	}

	// Handle boolean conditions
	if c.BoolValue != nil {
		if reading.Online != nil {
			return *reading.Online == *c.BoolValue
		}
		return false
	}

	// Handle string/IP conditions
	value := ""
	if reading.IP != nil {
		value = reading.IP.String()
	} else if reading.Value != "" {
		value = reading.Value
	}

	if value == "" {
		return false
	}

	return matchesPattern(value, c.Pattern)
}

// GroupCondition combines multiple conditions with AND or OR logic
type GroupCondition struct {
	Operator   string      // "all" (AND) or "any" (OR)
	Conditions []Condition // Nested conditions
}

// Evaluate checks if the group condition is satisfied
func (c *GroupCondition) Evaluate(readings map[string]SensorReading, online bool) bool {
	if len(c.Conditions) == 0 {
		// Empty "all" is true, empty "any" is false
		return c.Operator != "any"
	}

	switch c.Operator {
	case "all":
		for _, cond := range c.Conditions {
			if !cond.Evaluate(readings, online) {
				return false
			}
		}
		return true

	case "any":
		for _, cond := range c.Conditions {
			if cond.Evaluate(readings, online) {
				return true
			}
		}
		return false

	default:
		return false
	}
}

// NewSensorCondition creates a condition for a string sensor
func NewSensorCondition(sensorName, pattern string) *SensorCondition {
	return &SensorCondition{
		SensorName: sensorName,
		Pattern:    pattern,
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

// ConditionFromMap creates conditions from simple map format
func ConditionFromMap(conditions map[string][]string) Condition {
	if len(conditions) == 0 {
		return NewAllCondition() // Empty = always matches
	}

	var allConditions []Condition

	for conditionKey, patterns := range conditions {
		if len(patterns) == 0 {
			continue
		}

		sensorName := mapConditionKey(conditionKey)

		if len(patterns) == 1 {
			allConditions = append(allConditions, NewSensorCondition(sensorName, patterns[0]))
		} else {
			// Multiple patterns = OR
			anyConditions := make([]Condition, len(patterns))
			for i, pattern := range patterns {
				anyConditions[i] = NewSensorCondition(sensorName, pattern)
			}
			allConditions = append(allConditions, NewAnyCondition(anyConditions...))
		}
	}

	if len(allConditions) == 0 {
		return NewAllCondition()
	}
	if len(allConditions) == 1 {
		return allConditions[0]
	}

	return NewAllCondition(allConditions...)
}

// mapConditionKey maps config keys to sensor names
func mapConditionKey(key string) string {
	if key == "public_ip" {
		return "public_ipv4"
	}
	return key
}

// RuleEngine evaluates rules against sensor readings
type RuleEngine struct {
	rules     []Rule
	locations map[string]Location
}

// NewRuleEngine creates a new rule engine
func NewRuleEngine(rules []Rule, locations map[string]Location) *RuleEngine {
	return &RuleEngine{
		rules:     rules,
		locations: locations,
	}
}

// Evaluate implements RuleEvaluator interface
func (re *RuleEngine) Evaluate(readings map[string]SensorReading, online bool) RuleResult {
	// Try each rule in order (first match wins)
	for i := range re.rules {
		rule := &re.rules[i]

		// Check if any locations match
		for _, locationName := range rule.Locations {
			location, exists := re.locations[locationName]
			if !exists {
				continue
			}

			if re.locationMatches(&location, readings, online) {
				return RuleResult{
					Context:             rule.Name,
					ContextDisplayName:  rule.DisplayName,
					Location:            location.Name,
					LocationDisplayName: location.DisplayName,
					MatchedRule:         rule.Name + " (location: " + location.Name + ")",
					Environment:         re.mergeEnvironment(rule, &location),
				}
			}
		}

		// Check if rule is a fallback (no conditions)
		if rule.Condition == nil && len(rule.Conditions) == 0 && len(rule.Locations) == 0 {
			location := re.determineLocation(readings, online)
			return RuleResult{
				Context:             rule.Name,
				ContextDisplayName:  rule.DisplayName,
				Location:            location,
				LocationDisplayName: re.getLocationDisplayName(location),
				MatchedRule:         rule.Name + " (fallback)",
				Environment:         re.mergeEnvironment(rule, re.getLocation(location)),
			}
		}

		// Check rule's own conditions
		if re.ruleMatches(rule, readings, online) {
			location := re.determineLocation(readings, online)
			return RuleResult{
				Context:             rule.Name,
				ContextDisplayName:  rule.DisplayName,
				Location:            location,
				LocationDisplayName: re.getLocationDisplayName(location),
				MatchedRule:         rule.Name + " (conditions)",
				Environment:         re.mergeEnvironment(rule, re.getLocation(location)),
			}
		}
	}

	// No rule matched
	location := re.determineLocation(readings, online)
	return RuleResult{
		Context:             "unknown",
		Location:            location,
		LocationDisplayName: re.getLocationDisplayName(location),
		MatchedRule:         "none",
	}
}

// locationMatches checks if a location's conditions are satisfied
func (re *RuleEngine) locationMatches(loc *Location, readings map[string]SensorReading, online bool) bool {
	if loc.Condition != nil {
		return loc.Condition.Evaluate(readings, online)
	}
	if len(loc.Conditions) > 0 {
		cond := ConditionFromMap(loc.Conditions)
		return cond.Evaluate(readings, online)
	}
	return false
}

// ruleMatches checks if a rule's conditions are satisfied
func (re *RuleEngine) ruleMatches(rule *Rule, readings map[string]SensorReading, online bool) bool {
	if rule.Condition != nil {
		return rule.Condition.Evaluate(readings, online)
	}
	if len(rule.Conditions) > 0 {
		cond := ConditionFromMap(rule.Conditions)
		return cond.Evaluate(readings, online)
	}
	return false
}

// determineLocation finds the matching location based on readings
func (re *RuleEngine) determineLocation(readings map[string]SensorReading, online bool) string {
	// Check offline first
	if offlineLocation, exists := re.locations["offline"]; exists {
		if re.locationMatches(&offlineLocation, readings, online) {
			return "offline"
		}
	}

	// Check all other locations
	for name, location := range re.locations {
		if name == "offline" || name == "unknown" {
			continue
		}
		if re.locationMatches(&location, readings, online) {
			return name
		}
	}

	return "unknown"
}

// getLocation returns a location by name
func (re *RuleEngine) getLocation(name string) *Location {
	if loc, exists := re.locations[name]; exists {
		return &loc
	}
	return nil
}

// getLocationDisplayName returns the display name for a location
func (re *RuleEngine) getLocationDisplayName(name string) string {
	if loc, exists := re.locations[name]; exists && loc.DisplayName != "" {
		return loc.DisplayName
	}
	return name
}

// mergeEnvironment merges rule and location environment variables
func (re *RuleEngine) mergeEnvironment(rule *Rule, location *Location) map[string]string {
	env := make(map[string]string)

	// Location environment first
	if location != nil && location.Environment != nil {
		for k, v := range location.Environment {
			env[k] = v
		}
	}

	// Rule environment overrides location
	if rule != nil && rule.Environment != nil {
		for k, v := range rule.Environment {
			env[k] = v
		}
	}

	return env
}

// GetLocation returns a location by name (for external access)
func (re *RuleEngine) GetLocation(name string) *Location {
	return re.getLocation(name)
}

// GetRules returns all rules
func (re *RuleEngine) GetRules() []Rule {
	return re.rules
}

// Pattern matching functions

func matchesPattern(value, pattern string) bool {
	if value == pattern {
		return true
	}

	// CIDR matching
	if strings.Contains(pattern, "/") {
		return matchesCIDR(value, pattern)
	}

	// Wildcard matching
	if strings.Contains(pattern, "*") {
		return matchesWildcard(value, pattern)
	}

	return false
}

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

func matchesWildcard(value, pattern string) bool {
	if value == "" {
		return false
	}

	parts := strings.Split(pattern, "*")

	if !strings.HasPrefix(value, parts[0]) {
		return false
	}
	value = value[len(parts[0]):]

	for i := 1; i < len(parts)-1; i++ {
		idx := strings.Index(value, parts[i])
		if idx == -1 {
			return false
		}
		value = value[idx+len(parts[i]):]
	}

	if len(parts) > 1 {
		return strings.HasSuffix(value, parts[len(parts)-1])
	}

	return true
}

// ExtractRequiredSensors extracts all sensor names from conditions
func ExtractRequiredSensors(cond Condition) []string {
	if cond == nil {
		return nil
	}

	sensors := make(map[string]bool)
	extractSensorsRecursive(cond, sensors)

	result := make([]string, 0, len(sensors))
	for sensor := range sensors {
		result = append(result, sensor)
	}
	return result
}

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

// isNetworkSensor returns true for sensors that require network connectivity
// to provide valid readings. When offline, these sensors' cached values
// should not be trusted for location/context matching.
func isNetworkSensor(name string) bool {
	switch name {
	case "public_ipv4", "public_ipv6", "local_ipv4":
		return true
	default:
		return false
	}
}
