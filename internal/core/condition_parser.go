package core

import (
	"fmt"

	"github.com/sblinch/kdl-go/document"
	"overseer.olrik.dev/internal/security"
)

// parseConditionNode recursively parses a KDL node into a Condition structure
// Supports: any { }, all { }, sensor conditions, and nested groups
func parseConditionNode(node *document.Node) (security.Condition, error) {
	if node == nil || node.Name == nil {
		return nil, fmt.Errorf("invalid condition node")
	}

	nodeName := node.Name.Value.(string)

	switch nodeName {
	case "any":
		return parseGroupCondition(node, "any")
	case "all":
		return parseGroupCondition(node, "all")
	case "public_ip":
		return parseSensorCondition(node, "public_ip")
	case "env":
		return parseEnvCondition(node)
	case "online":
		return parseBooleanCondition(node, "online")
	case "context":
		return parseSensorCondition(node, "context")
	case "location":
		return parseSensorCondition(node, "location")
	default:
		return nil, fmt.Errorf("unknown condition type: %s", nodeName)
	}
}

// parseGroupCondition parses an "any" or "all" group node
func parseGroupCondition(node *document.Node, operator string) (security.Condition, error) {
	if node.Children == nil || len(node.Children) == 0 {
		return nil, fmt.Errorf("%s group must have child conditions", operator)
	}

	conditions := make([]security.Condition, 0)

	for _, child := range node.Children {
		cond, err := parseConditionNode(child)
		if err != nil {
			return nil, fmt.Errorf("failed to parse condition in %s group: %w", operator, err)
		}
		conditions = append(conditions, cond)
	}

	if operator == "any" {
		return security.NewAnyCondition(conditions...), nil
	}
	return security.NewAllCondition(conditions...), nil
}

// parseSensorCondition parses a string sensor condition node
// Format: sensor_name "pattern1" "pattern2" ...
func parseSensorCondition(node *document.Node, sensorName string) (security.Condition, error) {
	if len(node.Arguments) == 0 {
		return nil, fmt.Errorf("%s condition requires at least one pattern", sensorName)
	}

	// If multiple patterns, create an "any" group
	if len(node.Arguments) > 1 {
		conditions := make([]security.Condition, len(node.Arguments))
		for i, arg := range node.Arguments {
			pattern, ok := arg.Value.(string)
			if !ok {
				return nil, fmt.Errorf("%s pattern must be a string", sensorName)
			}
			conditions[i] = security.NewSensorCondition(sensorName, pattern)
		}
		return security.NewAnyCondition(conditions...), nil
	}

	// Single pattern
	pattern, ok := node.Arguments[0].Value.(string)
	if !ok {
		return nil, fmt.Errorf("%s pattern must be a string", sensorName)
	}

	return security.NewSensorCondition(sensorName, pattern), nil
}

// parseEnvCondition parses an environment variable condition
// Format: env "VAR_NAME" "pattern1" "pattern2" ...
func parseEnvCondition(node *document.Node) (security.Condition, error) {
	if len(node.Arguments) < 2 {
		return nil, fmt.Errorf("env condition requires variable name and at least one pattern")
	}

	varName, ok := node.Arguments[0].Value.(string)
	if !ok {
		return nil, fmt.Errorf("env variable name must be a string")
	}

	sensorName := "env:" + varName

	// If multiple patterns, create an "any" group
	if len(node.Arguments) > 2 {
		conditions := make([]security.Condition, len(node.Arguments)-1)
		for i := 1; i < len(node.Arguments); i++ {
			pattern, ok := node.Arguments[i].Value.(string)
			if !ok {
				return nil, fmt.Errorf("env pattern must be a string")
			}
			conditions[i-1] = security.NewSensorCondition(sensorName, pattern)
		}
		return security.NewAnyCondition(conditions...), nil
	}

	// Single pattern
	pattern, ok := node.Arguments[1].Value.(string)
	if !ok {
		return nil, fmt.Errorf("env pattern must be a string")
	}

	return security.NewSensorCondition(sensorName, pattern), nil
}

// parseBooleanCondition parses a boolean sensor condition
// Format: sensor_name true|false
func parseBooleanCondition(node *document.Node, sensorName string) (security.Condition, error) {
	if len(node.Arguments) != 1 {
		return nil, fmt.Errorf("%s condition requires exactly one boolean value", sensorName)
	}

	value, ok := node.Arguments[0].Value.(bool)
	if !ok {
		return nil, fmt.Errorf("%s value must be a boolean", sensorName)
	}

	return security.NewBooleanCondition(sensorName, value), nil
}

// parseConditionsBlock parses a "conditions" block that may contain any/all groups
// or simple flat conditions
func parseConditionsBlock(node *document.Node) (security.Condition, error) {
	if node == nil || node.Children == nil {
		return nil, nil
	}

	// Look for the conditions child node
	var conditionsNode *document.Node
	for _, child := range node.Children {
		if child.Name != nil && child.Name.Value == "conditions" {
			conditionsNode = child
			break
		}
	}

	if conditionsNode == nil || conditionsNode.Children == nil {
		return nil, nil
	}

	// Parse all child conditions and group by sensor name
	// Multiple conditions with the same sensor name should use OR logic
	// Multiple different sensors should use AND logic
	sensorConditions := make(map[string][]security.Condition)
	var conditionOrder []string // Track order of first occurrence

	for _, child := range conditionsNode.Children {
		if child.Name == nil {
			continue
		}

		sensorName := child.Name.Value.(string)
		cond, err := parseConditionNode(child)
		if err != nil {
			return nil, err
		}

		// Track first occurrence order
		if _, exists := sensorConditions[sensorName]; !exists {
			conditionOrder = append(conditionOrder, sensorName)
		}

		sensorConditions[sensorName] = append(sensorConditions[sensorName], cond)
	}

	if len(sensorConditions) == 0 {
		return nil, nil
	}

	// Build final condition list with OR groups for same-sensor conditions
	finalConditions := make([]security.Condition, 0)

	for _, sensorName := range conditionOrder {
		conds := sensorConditions[sensorName]
		if len(conds) == 1 {
			finalConditions = append(finalConditions, conds[0])
		} else {
			// Multiple conditions for same sensor = OR (any)
			finalConditions = append(finalConditions, security.NewAnyCondition(conds...))
		}
	}

	// If only one final condition, return it directly
	if len(finalConditions) == 1 {
		return finalConditions[0], nil
	}

	// Multiple sensors = OR (any) by default
	return security.NewAnyCondition(finalConditions...), nil
}
