// Package validate provides input validation helpers for MCP tool arguments.
package validate

import (
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
)

// Error represents a validation error with field context.
type Error struct {
	Field   string
	Message string
}

func (e Error) Error() string {
	return fmt.Sprintf("%s: %s", e.Field, e.Message)
}

// Errors is a collection of validation errors.
type Errors []Error

func (e Errors) Error() string {
	if len(e) == 0 {
		return ""
	}
	var msgs []string
	for _, err := range e {
		msgs = append(msgs, err.Error())
	}
	return strings.Join(msgs, "; ")
}

// HasErrors returns true if there are any validation errors.
func (e Errors) HasErrors() bool {
	return len(e) > 0
}

// Args wraps a map of tool arguments for validation.
type Args struct {
	args   map[string]any
	errors Errors
}

// NewArgs creates a new Args validator.
func NewArgs(args map[string]any) *Args {
	if args == nil {
		args = make(map[string]any)
	}
	return &Args{args: args}
}

// Required checks that a string field is present and non-empty.
func (a *Args) Required(field string) string {
	v, ok := a.args[field].(string)
	if !ok || v == "" {
		a.errors = append(a.errors, Error{Field: field, Message: "is required"})
		return ""
	}
	return v
}

// RequiredInt checks that an integer field is present.
func (a *Args) RequiredInt(field string) int {
	v, ok := asInt(a.args[field])
	if !ok {
		a.errors = append(a.errors, Error{Field: field, Message: "is required"})
		return 0
	}
	return v
}

// String gets an optional string field with a default value.
func (a *Args) String(field, defaultVal string) string {
	if v, ok := a.args[field].(string); ok {
		return v
	}
	return defaultVal
}

// Int gets an optional integer field with a default value.
func (a *Args) Int(field string, defaultVal int) int {
	if v, ok := asInt(a.args[field]); ok {
		return v
	}
	return defaultVal
}

func asInt(v any) (int, bool) {
	switch n := v.(type) {
	case float64:
		return int(n), true
	case float32:
		return int(n), true
	case int:
		return n, true
	case int64:
		return int(n), true
	case int32:
		return int(n), true
	case uint:
		return int(n), true
	case uint64:
		return int(n), true
	case uint32:
		return int(n), true
	case json.Number:
		i, err := n.Int64()
		if err != nil {
			return 0, false
		}
		return int(i), true
	default:
		return 0, false
	}
}

// IntRange validates an integer is within a range.
func (a *Args) IntRange(field string, defaultVal, min, max int) int {
	v := a.Int(field, defaultVal)
	if v < min || v > max {
		a.errors = append(a.errors, Error{
			Field:   field,
			Message: fmt.Sprintf("must be between %d and %d", min, max),
		})
	}
	return v
}

// Bool gets an optional boolean field with a default value.
func (a *Args) Bool(field string, defaultVal bool) bool {
	if v, ok := a.args[field].(bool); ok {
		return v
	}
	return defaultVal
}

// StringSlice gets an optional string slice field.
func (a *Args) StringSlice(field string) []string {
	if v, ok := a.args[field].([]any); ok {
		result := make([]string, 0, len(v))
		for _, item := range v {
			if s, ok := item.(string); ok {
				result = append(result, s)
			}
		}
		return result
	}
	return nil
}

// RequiredStringSlice checks that a string slice field is present and non-empty.
func (a *Args) RequiredStringSlice(field string) []string {
	v := a.StringSlice(field)
	if len(v) == 0 {
		a.errors = append(a.errors, Error{Field: field, Message: "is required"})
		return nil
	}
	return v
}

// Float gets an optional float64 field with a default value.
func (a *Args) Float(field string, defaultVal float64) float64 {
	if v, ok := a.args[field].(float64); ok {
		return v
	}
	return defaultVal
}

// RequiredBool checks that a boolean field is present and true.
// Useful for confirmation flags that must be explicitly set to true.
func (a *Args) RequiredBool(field string) bool {
	v, ok := a.args[field].(bool)
	if !ok {
		a.errors = append(a.errors, Error{Field: field, Message: "is required"})
		return false
	}
	return v
}

// Any gets a raw field value (useful for arrays and objects that need custom processing).
func (a *Args) Any(field string) any {
	return a.args[field]
}

// RequiredAny checks that a field is present (any type).
func (a *Args) RequiredAny(field string) any {
	v, ok := a.args[field]
	if !ok || v == nil {
		a.errors = append(a.errors, Error{Field: field, Message: "is required"})
		return nil
	}
	return v
}

// Enum validates a string is one of the allowed values.
func (a *Args) Enum(field string, defaultVal string, allowed ...string) string {
	v := a.String(field, defaultVal)
	if v == "" && defaultVal == "" {
		return ""
	}
	for _, av := range allowed {
		if v == av {
			return v
		}
	}
	a.errors = append(a.errors, Error{
		Field:   field,
		Message: fmt.Sprintf("must be one of: %s", strings.Join(allowed, ", ")),
	})
	return defaultVal
}

// Pattern validates a string matches a regex pattern.
func (a *Args) Pattern(field, pattern string) string {
	v := a.String(field, "")
	if v == "" {
		return ""
	}
	re, err := regexp.Compile(pattern)
	if err != nil {
		a.errors = append(a.errors, Error{
			Field:   field,
			Message: fmt.Sprintf("invalid pattern: %s", pattern),
		})
		return v
	}
	if !re.MatchString(v) {
		a.errors = append(a.errors, Error{
			Field:   field,
			Message: fmt.Sprintf("must match pattern: %s", pattern),
		})
	}
	return v
}

// MinLength validates a string has minimum length.
func (a *Args) MinLength(field string, minLen int) string {
	v := a.String(field, "")
	if len(v) < minLen {
		a.errors = append(a.errors, Error{
			Field:   field,
			Message: fmt.Sprintf("must be at least %d characters", minLen),
		})
	}
	return v
}

// MaxLength validates a string has maximum length.
func (a *Args) MaxLength(field string, maxLen int) string {
	v := a.String(field, "")
	if len(v) > maxLen {
		a.errors = append(a.errors, Error{
			Field:   field,
			Message: fmt.Sprintf("must be at most %d characters", maxLen),
		})
	}
	return v
}

// Errors returns all validation errors.
func (a *Args) Errors() Errors {
	return a.errors
}

// Validate returns an error if there are any validation errors.
func (a *Args) Validate() error {
	if a.errors.HasErrors() {
		return a.errors
	}
	return nil
}

// Common validation patterns
var (
	// UUIDPattern matches UUID format
	UUIDPattern = `^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$`

	// K8sNamePattern matches valid Kubernetes resource names
	K8sNamePattern = `^[a-z0-9]([-a-z0-9]*[a-z0-9])?$`

	// K8sNamespacePattern matches valid Kubernetes namespace names
	K8sNamespacePattern = K8sNamePattern

	// DNSNamePattern matches valid DNS names
	DNSNamePattern = `^[a-zA-Z0-9]([a-zA-Z0-9-]*[a-zA-Z0-9])?(\.[a-zA-Z0-9]([a-zA-Z0-9-]*[a-zA-Z0-9])?)*$`
)
