package redact

// Action names a transformation applied to a tool arg or result value.
type Action string

const (
	// ActionDrop omits the field entirely from the output map.
	ActionDrop Action = "drop"

	// ActionPass keeps the field unchanged.
	ActionPass Action = "pass"

	// ActionMask runs the value's string form through MaskSecrets, keeping
	// non-secret content but substituting RedactionMarker for known secret
	// patterns.
	ActionMask Action = "mask"

	// ActionPathOnly keeps a file-path's basename, dropping the directory.
	// For non-path values it returns the value unchanged.
	ActionPathOnly Action = "path_only"

	// ActionSizeOnly replaces the value with {"size_bytes": N} where N is
	// the byte length of the value's string form.
	ActionSizeOnly Action = "size_only"

	// ActionTruncMask truncates the value's string form to MaxLen runes and
	// runs MaskSecrets over the result. Adds "…" suffix when truncated.
	ActionTruncMask Action = "trunc_mask"
)

// FieldRule is a single redaction directive for an arg field or a result.
type FieldRule struct {
	Action Action
	// MaxLen applies only to ActionTruncMask. Zero/negative → no truncation
	// (mask only).
	MaxLen int
}

// resolvePolicy is the per-tier rule set for one tool's args + result.
type resolvePolicy struct {
	// Args is the per-arg-field rule. Fields not listed fall back to Default.
	Args map[string]FieldRule
	// Default applies to any arg field not in Args.
	Default FieldRule
	// Result is the rule for the tool's return value.
	Result FieldRule
}

// rule is a static convenience for building FieldRule values inline.
func rule(a Action) FieldRule { return FieldRule{Action: a} }
func trunc(n int) FieldRule   { return FieldRule{Action: ActionTruncMask, MaxLen: n} }

// toolPolicies maps tool names → per-tier rules. A tool not present here gets
// the tier-default policy from defaultPolicy().
var toolPolicies = map[string]map[Tier]resolvePolicy{
	"Read": {
		TierPublic: {
			Args:    map[string]FieldRule{"file_path": rule(ActionPathOnly)},
			Default: rule(ActionDrop),
			Result:  rule(ActionSizeOnly),
		},
		TierRedacted: {
			Args:    map[string]FieldRule{"file_path": rule(ActionPathOnly)},
			Default: rule(ActionMask),
			Result:  trunc(200),
		},
	},
	"Write": {
		TierPublic: {
			Args: map[string]FieldRule{
				"file_path": rule(ActionPathOnly),
				"content":   rule(ActionSizeOnly),
			},
			Default: rule(ActionDrop),
			Result:  rule(ActionSizeOnly),
		},
		TierRedacted: {
			Args: map[string]FieldRule{
				"file_path": rule(ActionPathOnly),
				"content":   trunc(200),
			},
			Default: rule(ActionMask),
			Result:  rule(ActionSizeOnly),
		},
	},
	"Edit": {
		TierPublic: {
			Args: map[string]FieldRule{
				"file_path":  rule(ActionPathOnly),
				"old_string": rule(ActionSizeOnly),
				"new_string": rule(ActionSizeOnly),
			},
			Default: rule(ActionDrop),
			Result:  rule(ActionSizeOnly),
		},
		TierRedacted: {
			Args: map[string]FieldRule{
				"file_path":  rule(ActionPathOnly),
				"old_string": trunc(200),
				"new_string": trunc(200),
			},
			Default: rule(ActionMask),
			Result:  rule(ActionSizeOnly),
		},
	},
	"Bash": {
		TierPublic: {
			Args:    map[string]FieldRule{"command": trunc(60)},
			Default: rule(ActionDrop),
			Result:  rule(ActionSizeOnly),
		},
		TierRedacted: {
			Args:    map[string]FieldRule{"command": rule(ActionMask)},
			Default: rule(ActionMask),
			Result:  trunc(200),
		},
	},
	"agent_context_add": {
		TierPublic: {
			Args:    nil,
			Default: rule(ActionDrop),
			Result:  rule(ActionSizeOnly),
		},
		TierRedacted: {
			Args:    nil,
			Default: rule(ActionDrop),
			Result:  rule(ActionSizeOnly),
		},
	},
}

// defaultPolicy returns the fallback rule set for an unknown tool at the
// requested tier. Conservative defaults: public drops everything; redacted
// masks everything.
func defaultPolicy(tier Tier) resolvePolicy {
	switch tier {
	case TierPublic:
		return resolvePolicy{
			Default: rule(ActionDrop),
			Result:  rule(ActionDrop),
		}
	case TierRedacted:
		return resolvePolicy{
			Default: rule(ActionMask),
			Result:  trunc(200),
		}
	default:
		return resolvePolicy{
			Default: rule(ActionPass),
			Result:  rule(ActionPass),
		}
	}
}

// lookupPolicy returns the rule set for (toolName, tier), falling back to the
// tier default when the tool is not registered.
func lookupPolicy(toolName string, tier Tier) resolvePolicy {
	if perTier, ok := toolPolicies[toolName]; ok {
		if p, ok := perTier[tier]; ok {
			return p
		}
	}
	return defaultPolicy(tier)
}
