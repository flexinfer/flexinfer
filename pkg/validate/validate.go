// Package validate provides input validation helpers for MCP tool arguments.
package validate

import (
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
	v, ok := a.args[field].(float64) // JSON numbers are float64
	if !ok {
		a.errors = append(a.errors, Error{Field: field, Message: "is required"})
		return 0
	}
	return int(v)
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
	if v, ok := a.args[field].(float64); ok {
		return int(v)
	}
	return defaultVal
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
