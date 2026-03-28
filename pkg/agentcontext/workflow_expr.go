// workflow_expr.go — condition evaluation, variable resolution, DAG validation, and utility helpers.
package agentcontext

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"time"
)

func validateDAG(steps []WorkflowStep) error {
	// Build adjacency list and step map
	stepMap := make(map[string]bool)
	adjacency := make(map[string][]string)
	for _, s := range steps {
		stepMap[s.ID] = true
		adjacency[s.ID] = s.DependsOn
	}

	// Check all dependencies exist
	for _, step := range steps {
		for _, dep := range step.DependsOn {
			if !stepMap[dep] {
				return fmt.Errorf("step %s depends on non-existent step %s", step.ID, dep)
			}
		}
	}

	// Full cycle detection with DFS
	// States: 0 = unvisited, 1 = visiting, 2 = visited
	state := make(map[string]int)
	var path []string

	var dfs func(node string) error
	dfs = func(node string) error {
		if state[node] == 1 {
			// Found cycle - build cycle path
			cycleStart := -1
			for i, n := range path {
				if n == node {
					cycleStart = i
					break
				}
			}
			cyclePath := append(path[cycleStart:], node)
			return fmt.Errorf("cycle detected: %s", formatCycle(cyclePath))
		}
		if state[node] == 2 {
			return nil // Already visited
		}

		state[node] = 1 // Visiting
		path = append(path, node)

		for _, dep := range adjacency[node] {
			if err := dfs(dep); err != nil {
				return err
			}
		}

		path = path[:len(path)-1]
		state[node] = 2 // Visited
		return nil
	}

	// Run DFS from each unvisited node
	for stepID := range stepMap {
		if state[stepID] == 0 {
			if err := dfs(stepID); err != nil {
				return err
			}
		}
	}

	return nil
}

func formatCycle(cycle []string) string {
	result := ""
	for i, node := range cycle {
		if i > 0 {
			result += " -> "
		}
		result += node
	}
	return result
}

// calculateBackoffDelay returns the delay for a retry attempt using exponential backoff.
// baseDelay is in milliseconds. Returns time.Duration.
// Formula: min(baseDelay * 2^(attempt-1), maxDelay) with jitter
func calculateBackoffDelay(attempt int, baseDelayMs int) time.Duration {
	if baseDelayMs <= 0 {
		baseDelayMs = 1000 // Default 1 second
	}

	const maxDelayMs = 60000 // Max 1 minute

	// Calculate exponential delay: base * 2^(attempt-1)
	delayMs := baseDelayMs
	for i := 1; i < attempt; i++ {
		delayMs *= 2
		if delayMs > maxDelayMs {
			delayMs = maxDelayMs
			break
		}
	}

	// Add jitter (up to 25% of delay)
	jitterMs := delayMs / 4
	if jitterMs > 0 {
		// Simple pseudo-random jitter using time
		jitter := int(time.Now().UnixNano() % int64(jitterMs))
		delayMs += jitter
	}

	return time.Duration(delayMs) * time.Millisecond
}

func resolveVariables(args map[string]any, input, context map[string]any) map[string]any {
	if args == nil {
		return nil
	}

	result := make(map[string]any)
	for k, v := range args {
		switch val := v.(type) {
		case string:
			// Check for variable reference like "${input.key}" or "${step_id.result}"
			result[k] = resolveString(val, input, context)
		case map[string]any:
			result[k] = resolveVariables(val, input, context)
		default:
			result[k] = v
		}
	}
	return result
}

func resolveString(s string, input, context map[string]any) any {
	// Simple variable resolution: ${input.key} or ${step_id.key}
	if len(s) < 4 || s[0:2] != "${" || s[len(s)-1] != '}' {
		return s
	}

	ref := s[2 : len(s)-1]
	if len(ref) > 6 && ref[:6] == "input." {
		key := ref[6:]
		if val, ok := input[key]; ok {
			return val
		}
	} else if dotIdx := indexOf(ref, '.'); dotIdx > 0 {
		stepID := ref[:dotIdx]
		key := ref[dotIdx+1:]
		if stepResult, ok := context[stepID].(map[string]any); ok {
			if val, ok := stepResult[key]; ok {
				return val
			}
		}
	}

	return s // Return original if not resolved
}

func indexOf(s string, c byte) int {
	for i := range s {
		if s[i] == c {
			return i
		}
	}
	return -1
}

func evaluateCondition(cond string, input, context map[string]any) bool {
	cond = strings.TrimSpace(cond)
	if cond == "" {
		return true
	}

	// Handle boolean operators: split on AND/OR (left-to-right, no precedence)
	if parts := splitBoolOp(cond, " AND "); len(parts) > 1 {
		for _, p := range parts {
			if !evaluateCondition(p, input, context) {
				return false
			}
		}
		return true
	}
	if parts := splitBoolOp(cond, " OR "); len(parts) > 1 {
		for _, p := range parts {
			if evaluateCondition(p, input, context) {
				return true
			}
		}
		return false
	}

	// Handle EXISTS operator: "<ref> EXISTS"
	if strings.HasSuffix(cond, " EXISTS") {
		ref := strings.TrimSuffix(cond, " EXISTS")
		ref = strings.TrimSpace(ref)
		val := resolveRef(ref, input, context)
		return val != nil
	}

	// Handle comparison operators: >=, <=, !=, >, <, ==
	for _, op := range []string{">=", "<=", "!=", ">", "<", "=="} {
		if idx := strings.Index(cond, " "+op+" "); idx >= 0 {
			left := strings.TrimSpace(cond[:idx])
			right := strings.TrimSpace(cond[idx+len(op)+2:])
			lval := resolveRef(left, input, context)
			rval := parseCondValue(right)
			return compareValues(lval, rval, op)
		}
	}

	// Fallback: simple truthy check on resolved reference
	val := resolveRef(cond, input, context)
	return isTruthy(val)
}

// resolveRef resolves a dotted reference like "step_id.field" or "input.key"
// against the input and context maps. Returns nil if unresolved.
func resolveRef(ref string, input, context map[string]any) any {
	ref = strings.TrimSpace(ref)
	val := resolveString("${"+ref+"}", input, context)
	if s, ok := val.(string); ok && s == "${"+ref+"}" {
		return nil // unresolved
	}
	return val
}

// parseCondValue parses a literal value from a condition expression.
func parseCondValue(s string) any {
	s = strings.TrimSpace(s)
	if s == "true" {
		return true
	}
	if s == "false" {
		return false
	}
	if len(s) >= 2 && s[0] == '\'' && s[len(s)-1] == '\'' {
		return s[1 : len(s)-1]
	}
	if len(s) >= 2 && s[0] == '"' && s[len(s)-1] == '"' {
		return s[1 : len(s)-1]
	}
	if i, err := strconv.ParseInt(s, 10, 64); err == nil {
		return i
	}
	if f, err := strconv.ParseFloat(s, 64); err == nil {
		return f
	}
	return s
}

// compareValues compares two values using the given operator.
func compareValues(left, right any, op string) bool {
	lf, lok := condToFloat64(left)
	rf, rok := condToFloat64(right)

	if lok && rok {
		switch op {
		case ">":
			return lf > rf
		case ">=":
			return lf >= rf
		case "<":
			return lf < rf
		case "<=":
			return lf <= rf
		case "==":
			return lf == rf
		case "!=":
			return lf != rf
		}
	}

	ls := fmt.Sprintf("%v", left)
	rs := fmt.Sprintf("%v", right)
	switch op {
	case "==":
		return ls == rs
	case "!=":
		return ls != rs
	default:
		return false
	}
}

// condToFloat64 attempts to convert any to float64 for condition comparisons.
func condToFloat64(v any) (float64, bool) {
	switch n := v.(type) {
	case float64:
		return n, true
	case float32:
		return float64(n), true
	case int:
		return float64(n), true
	case int64:
		return float64(n), true
	case int32:
		return float64(n), true
	case json.Number:
		f, err := n.Float64()
		return f, err == nil
	}
	return 0, false
}

// isTruthy checks if a value is truthy.
func isTruthy(val any) bool {
	if val == nil {
		return false
	}
	switch v := val.(type) {
	case bool:
		return v
	case string:
		return v != "" && v != "false" && v != "0"
	case int:
		return v != 0
	case int64:
		return v != 0
	case float64:
		return v != 0
	default:
		return true
	}
}

// splitBoolOp splits a condition string on a boolean operator while respecting
// single-quoted string boundaries. This prevents splitting on operators that
// appear inside string literals (e.g., "name == 'CONNECT AND PLAY' AND ok").
func splitBoolOp(cond, op string) []string {
	var parts []string
	inQuote := false
	start := 0
	for i := 0; i < len(cond); i++ {
		if cond[i] == '\'' {
			inQuote = !inQuote
			continue
		}
		if !inQuote && i+len(op) <= len(cond) && cond[i:i+len(op)] == op {
			if part := strings.TrimSpace(cond[start:i]); part != "" {
				parts = append(parts, part)
			}
			start = i + len(op)
			i += len(op) - 1
		}
	}
	if last := strings.TrimSpace(cond[start:]); last != "" {
		parts = append(parts, last)
	}
	return parts
}

func mapKeys(m map[string]any) []string {
	if m == nil {
		return nil
	}
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	return keys
}

func errStr(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}
