// Package validator provides MCP configuration validation.
package validator

import (
	"fmt"
	"strings"
)

// ValidationSeverity indicates error severity.
type ValidationSeverity int

const (
	// SeverityError indicates a validation failure that should block operations.
	SeverityError ValidationSeverity = iota
	// SeverityWarning indicates a potential issue that doesn't block operations.
	SeverityWarning
)

func (s ValidationSeverity) String() string {
	switch s {
	case SeverityError:
		return "error"
	case SeverityWarning:
		return "warning"
	default:
		return "unknown"
	}
}

// Error codes for validation failures.
const (
	// Schema errors
	CodeMissingRootKey  = "MISSING_ROOT_KEY"
	CodeMissingCommand  = "MISSING_COMMAND"
	CodeInvalidArgsType = "INVALID_ARGS_TYPE"
	CodeInvalidEnvType  = "INVALID_ENV_TYPE"
	CodeInvalidSchema   = "INVALID_SCHEMA"

	// Runtime errors
	CodeCommandNotFound      = "COMMAND_NOT_FOUND"
	CodeCommandNotExecutable = "COMMAND_NOT_EXECUTABLE"
	CodeInvalidEnvName       = "INVALID_ENV_NAME"
	CodeInvalidTimeout       = "INVALID_TIMEOUT"
	CodeUnresolvedToken      = "UNRESOLVED_TOKEN"

	// Security errors
	CodePlaintextSecret = "PLAINTEXT_SECRET"
)

// ValidationError represents a single validation issue.
type ValidationError struct {
	File     string             // File path being validated
	Line     int                // Line number (0 if unknown)
	Column   int                // Column number (0 if unknown)
	Field    string             // JSON path or TOML key (e.g., "mcpServers.tavily.command")
	Message  string             // Human-readable error message
	Code     string             // Machine-readable error code
	Severity ValidationSeverity // Error or Warning
}

// Error implements the error interface.
func (e ValidationError) Error() string {
	var location string
	if e.Line > 0 {
		location = fmt.Sprintf("%s:%d", e.File, e.Line)
	} else {
		location = e.File
	}

	if e.Field != "" {
		return fmt.Sprintf("[%s] %s: %s - %s", e.Severity, location, e.Field, e.Message)
	}
	return fmt.Sprintf("[%s] %s: %s", e.Severity, location, e.Message)
}

// ValidationResult holds all validation outcomes for a single file.
type ValidationResult struct {
	Target string            // Target name (claude, codex, etc.)
	File   string            // Config file path
	Valid  bool              // True if no errors (warnings allowed)
	Errors []ValidationError // All validation issues
}

// HasErrors returns true if there are any error-severity issues.
func (r *ValidationResult) HasErrors() bool {
	for _, err := range r.Errors {
		if err.Severity == SeverityError {
			return true
		}
	}
	return false
}

// HasWarnings returns true if there are any warning-severity issues.
func (r *ValidationResult) HasWarnings() bool {
	for _, err := range r.Errors {
		if err.Severity == SeverityWarning {
			return true
		}
	}
	return false
}

// ErrorCount returns the number of error-severity issues.
func (r *ValidationResult) ErrorCount() int {
	count := 0
	for _, err := range r.Errors {
		if err.Severity == SeverityError {
			count++
		}
	}
	return count
}

// WarningCount returns the number of warning-severity issues.
func (r *ValidationResult) WarningCount() int {
	count := 0
	for _, err := range r.Errors {
		if err.Severity == SeverityWarning {
			count++
		}
	}
	return count
}

// String returns a summary of the validation result.
func (r *ValidationResult) String() string {
	var sb strings.Builder

	if r.Valid {
		sb.WriteString(fmt.Sprintf("%s: valid", r.Target))
	} else {
		sb.WriteString(fmt.Sprintf("%s: %d errors, %d warnings\n", r.Target, r.ErrorCount(), r.WarningCount()))
		for _, err := range r.Errors {
			sb.WriteString(fmt.Sprintf("  %s\n", err.Error()))
		}
	}

	return sb.String()
}

// AddError adds an error-severity validation issue.
func (r *ValidationResult) AddError(code, field, message string) {
	r.Errors = append(r.Errors, ValidationError{
		File:     r.File,
		Field:    field,
		Message:  message,
		Code:     code,
		Severity: SeverityError,
	})
}

// AddWarning adds a warning-severity validation issue.
func (r *ValidationResult) AddWarning(code, field, message string) {
	r.Errors = append(r.Errors, ValidationError{
		File:     r.File,
		Field:    field,
		Message:  message,
		Code:     code,
		Severity: SeverityWarning,
	})
}

// AddErrorWithLine adds an error with line number information.
func (r *ValidationResult) AddErrorWithLine(code, field, message string, line int) {
	r.Errors = append(r.Errors, ValidationError{
		File:     r.File,
		Line:     line,
		Field:    field,
		Message:  message,
		Code:     code,
		Severity: SeverityError,
	})
}
