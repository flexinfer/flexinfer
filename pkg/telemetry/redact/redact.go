package redact

import (
	"fmt"
	"path/filepath"
	"strings"
)

// Tier names a privacy level. Wire format is the bare string ("public", etc.)
// so tiers serialize naturally over JSON event payloads.
type Tier string

const (
	TierPublic   Tier = "public"
	TierRedacted Tier = "redacted"
	TierPrivate  Tier = "private"
)

// ParseTier returns the Tier for s or an error if s is not a known tier.
func ParseTier(s string) (Tier, error) {
	switch Tier(s) {
	case TierPublic, TierRedacted, TierPrivate:
		return Tier(s), nil
	default:
		return "", fmt.Errorf("redact: unknown tier %q", s)
	}
}

// Redact applies privacy filtering to a tool's args at the requested tier.
//
// Returns a new map; never mutates the input. Pass toolName="" to use only
// the tier's generic defaults (no per-tool policy lookup). Nil args → empty
// result for public/redacted; nil for private.
func Redact(toolName string, args map[string]any, tier Tier) map[string]any {
	if tier == TierPrivate {
		return args
	}
	if args == nil {
		return map[string]any{}
	}

	policy := lookupPolicy(toolName, tier)
	out := make(map[string]any, len(args))

	for key, val := range args {
		rule, hasRule := policy.Args[key]
		if !hasRule {
			rule = policy.Default
		}
		if applied, keep := applyRule(val, rule); keep {
			out[key] = applied
		}
	}
	return out
}

// Summary returns a one-line, secret-safe preview of a tool's result for the
// requested tier. Returns "" when the tier disallows any disclosure for this
// tool (the default for public on unknown tools).
func Summary(toolName string, result any, tier Tier) string {
	if result == nil {
		return ""
	}
	if tier == TierPrivate {
		return toString(result)
	}
	rule := lookupPolicy(toolName, tier).Result
	out, keep := applyRule(result, rule)
	if !keep {
		return ""
	}
	return toString(out)
}

// applyRule executes a FieldRule against a value, returning the transformed
// value and whether to keep the field (false = drop).
func applyRule(val any, rule FieldRule) (any, bool) {
	switch rule.Action {
	case ActionDrop:
		return nil, false
	case ActionPass:
		return val, true
	case ActionMask:
		return MaskSecrets(toString(val)), true
	case ActionPathOnly:
		return pathOnly(toString(val)), true
	case ActionSizeOnly:
		s := toString(val)
		return map[string]any{"size_bytes": len(s)}, true
	case ActionTruncMask:
		s := toString(val)
		s = strings.TrimSuffix(s, "…") // normalize prior truncation marker for idempotency
		n := rule.MaxLen
		didTrunc := false
		if n > 0 && n < len(s) {
			s = s[:n]
			didTrunc = true
		}
		masked := MaskSecrets(s)
		if didTrunc {
			masked += "…"
		}
		return masked, true
	default:
		return nil, false
	}
}

func toString(v any) string {
	switch x := v.(type) {
	case nil:
		return ""
	case string:
		return x
	case fmt.Stringer:
		return x.String()
	default:
		return fmt.Sprintf("%v", x)
	}
}

// pathOnly returns the basename of a file path (or "***" if the path is empty
// or contains no separators and looks like an opaque token rather than a path).
func pathOnly(s string) string {
	s = strings.TrimSpace(s)
	if s == "" {
		return ""
	}
	if !strings.ContainsAny(s, "/\\") {
		return s
	}
	return filepath.Base(s)
}
