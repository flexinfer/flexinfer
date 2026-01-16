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
