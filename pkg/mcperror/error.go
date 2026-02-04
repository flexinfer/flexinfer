// Package mcperror provides standardized error types for MCP servers.
package mcperror

import (
	"encoding/json"
	"fmt"
)

// Error represents a structured MCP error response.
type Error struct {
	Code    string `json:"code"`
	Message string `json:"message"`
	Details any    `json:"details,omitempty"`
}

// Error implements the error interface.
func (e *Error) Error() string {
	return fmt.Sprintf("%s: %s", e.Code, e.Message)
}

// JSON returns the error as a JSON string.
func (e *Error) JSON() string {
	b, _ := json.Marshal(e)
	return string(b)
}

// Common error codes
const (
	CodeInvalidInput    = "INVALID_INPUT"
	CodeNotFound        = "NOT_FOUND"
	CodeUnauthorized    = "UNAUTHORIZED"
	CodeForbidden       = "FORBIDDEN"
	CodeTimeout         = "TIMEOUT"
	CodeServerError     = "SERVER_ERROR"
	CodeConnectionError = "CONNECTION_ERROR"
	CodeRateLimited     = "RATE_LIMITED"
	CodeValidation      = "VALIDATION_ERROR"
)

// New creates a new Error with the given code and message.
func New(code, message string) *Error {
	return &Error{
		Code:    code,
		Message: message,
	}
}

// WithDetails returns a copy of the error with additional details.
func (e *Error) WithDetails(details any) *Error {
	return &Error{
		Code:    e.Code,
		Message: e.Message,
		Details: details,
	}
}

// Wrap wraps an existing error into an MCP Error.
func Wrap(code string, err error) *Error {
	if err == nil {
		return nil
	}
	return &Error{
		Code:    code,
		Message: err.Error(),
	}
}

// Convenience constructors for common error types

// InvalidInput returns a validation error.
func InvalidInput(message string) *Error {
	return New(CodeInvalidInput, message)
}

// NotFound returns a not found error.
func NotFound(resource, name string) *Error {
	return New(CodeNotFound, fmt.Sprintf("%s '%s' not found", resource, name))
}

// Unauthorized returns an authentication error.
func Unauthorized(message string) *Error {
	return New(CodeUnauthorized, message)
}

// Forbidden returns an authorization error.
func Forbidden(message string) *Error {
	return New(CodeForbidden, message)
}

// Timeout returns a timeout error.
func Timeout(operation string) *Error {
	return New(CodeTimeout, fmt.Sprintf("%s timed out", operation))
}

// ServerError returns a server error.
func ServerError(message string) *Error {
	return New(CodeServerError, message)
}

// ConnectionError returns a connection error.
func ConnectionError(host string, err error) *Error {
	return &Error{
		Code:    CodeConnectionError,
		Message: fmt.Sprintf("failed to connect to %s", host),
		Details: map[string]string{"error": err.Error()},
	}
}

// RateLimited returns a rate limit error.
func RateLimited(retryAfter string) *Error {
	return &Error{
		Code:    CodeRateLimited,
		Message: "rate limit exceeded",
		Details: map[string]string{"retry_after": retryAfter},
	}
}

// Validation returns a validation error with field details.
func Validation(errors map[string]string) *Error {
	return &Error{
		Code:    CodeValidation,
		Message: "validation failed",
		Details: errors,
	}
}

// Result wraps a successful result or error for JSON responses.
type Result struct {
	OK    bool   `json:"ok"`
	Data  any    `json:"data,omitempty"`
	Error *Error `json:"error,omitempty"`
}

// Success creates a successful result.
func Success(data any) *Result {
	return &Result{
		OK:   true,
		Data: data,
	}
}

// Failure creates a failed result.
func Failure(err *Error) *Result {
	return &Result{
		OK:    false,
		Error: err,
	}
}

// JSON returns the result as a JSON map for MCP responses.
func (r *Result) JSON() map[string]any {
	result := map[string]any{"ok": r.OK}
	if r.Data != nil {
		result["data"] = r.Data
	}
	if r.Error != nil {
		result["error"] = r.Error
	}
	return result
}

// =============================================================================
// MCP-Specific Helpers
// =============================================================================

// RequiredParam returns an error for a missing required parameter.
func RequiredParam(param string) *Error {
	return &Error{
		Code:    CodeInvalidInput,
		Message: fmt.Sprintf("missing required parameter: %s", param),
		Details: map[string]string{"parameter": param},
	}
}

// InvalidParam returns an error for an invalid parameter value.
func InvalidParam(param, reason string) *Error {
	return &Error{
		Code:    CodeInvalidInput,
		Message: fmt.Sprintf("invalid parameter '%s': %s", param, reason),
		Details: map[string]string{"parameter": param, "reason": reason},
	}
}

// APIError returns an error for external API failures.
// The error code is set based on the HTTP status code:
//   - 401: CodeUnauthorized
//   - 403: CodeForbidden
//   - 404: CodeNotFound
//   - 429: CodeRateLimited
//   - 5xx: CodeServerError
//   - others: CodeServerError
func APIError(service string, statusCode int, body string) *Error {
	msg := fmt.Sprintf("%s API error (HTTP %d)", service, statusCode)
	code := CodeServerError
	// Add helpful context for common errors
	switch statusCode {
	case 401:
		msg = fmt.Sprintf("%s: authentication failed - check your API token", service)
		code = CodeUnauthorized
	case 403:
		msg = fmt.Sprintf("%s: access forbidden - check permissions", service)
		code = CodeForbidden
	case 404:
		msg = fmt.Sprintf("%s: resource not found", service)
		code = CodeNotFound
	case 429:
		msg = fmt.Sprintf("%s: rate limit exceeded - try again later", service)
		code = CodeRateLimited
	case 500, 502, 503, 504:
		msg = fmt.Sprintf("%s: service unavailable - try again later", service)
		code = CodeServerError
	}
	return &Error{
		Code:    code,
		Message: msg,
		Details: map[string]any{
			"service":     service,
			"status_code": statusCode,
			"body":        truncateString(body, 500),
		},
	}
}

// IsNotFound returns true if the error is a not found error (HTTP 404).
func IsNotFound(err error) bool {
	if mcpErr, ok := err.(*Error); ok {
		return mcpErr.Code == CodeNotFound
	}
	return false
}

// IsServerError returns true if the error is a server error (HTTP 5xx).
func IsServerError(err error) bool {
	if mcpErr, ok := err.(*Error); ok {
		return mcpErr.Code == CodeServerError
	}
	return false
}

// WrapAPI wraps an API error with context.
func WrapAPI(service string, err error) *Error {
	if err == nil {
		return nil
	}
	return &Error{
		Code:    CodeConnectionError,
		Message: fmt.Sprintf("%s: %s", service, err.Error()),
		Details: map[string]string{"service": service},
	}
}

// ServiceUnavailable returns an error when a required service is not available.
func ServiceUnavailable(service, reason string) *Error {
	return &Error{
		Code:    CodeServerError,
		Message: fmt.Sprintf("%s is unavailable: %s", service, reason),
		Details: map[string]string{"service": service},
	}
}

// NotConfigured returns an error when required configuration is missing.
func NotConfigured(configItem, hint string) *Error {
	msg := fmt.Sprintf("%s is not configured", configItem)
	if hint != "" {
		msg = msg + ": " + hint
	}
	return &Error{
		Code:    CodeServerError,
		Message: msg,
		Details: map[string]string{"config": configItem, "hint": hint},
	}
}

// OperationFailed returns an error when an operation fails.
func OperationFailed(operation string, err error) *Error {
	if err == nil {
		return nil
	}
	return &Error{
		Code:    CodeServerError,
		Message: fmt.Sprintf("%s failed: %s", operation, err.Error()),
	}
}

// ParseError returns an error for parsing failures.
func ParseError(what string, err error) *Error {
	return &Error{
		Code:    CodeInvalidInput,
		Message: fmt.Sprintf("failed to parse %s: %s", what, err.Error()),
	}
}

// truncateString truncates a string to maxLen, adding "..." if truncated.
func truncateString(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	if maxLen <= 3 {
		return s[:maxLen]
	}
	return s[:maxLen-3] + "..."
}
