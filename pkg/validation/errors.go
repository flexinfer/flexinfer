// Package validation provides OpenAI-compatible error handling and request validation.
package validation

import (
	"encoding/json"
	"net/http"
)

// OpenAI error types
const (
	// ErrorTypeInvalidRequest indicates a malformed or invalid request.
	ErrorTypeInvalidRequest = "invalid_request_error"

	// ErrorTypeNotFound indicates a requested resource was not found.
	ErrorTypeNotFound = "not_found_error"

	// ErrorTypeRateLimit indicates rate limiting has been exceeded.
	ErrorTypeRateLimit = "rate_limit_error"

	// ErrorTypeServer indicates an internal server error.
	ErrorTypeServer = "server_error"

	// ErrorTypeServiceUnavailable indicates the service is temporarily unavailable.
	ErrorTypeServiceUnavailable = "service_unavailable_error"

	// ErrorTypeTimeout indicates a request timeout.
	ErrorTypeTimeout = "timeout_error"
)

// OpenAI error codes
const (
	CodeInvalidRequestError  = "invalid_request_error"
	CodeModelNotFound        = "model_not_found"
	CodeInvalidModel         = "invalid_model"
	CodeInvalidAPIKey        = "invalid_api_key"
	CodeRateLimitExceeded    = "rate_limit_exceeded"
	CodeServerError          = "server_error"
	CodeServiceUnavailable   = "service_unavailable"
	CodeTimeout              = "timeout"
	CodeQueueFull            = "queue_full"
	CodeActivationFailed     = "activation_failed"
	CodeMethodNotAllowed     = "method_not_allowed"
	CodeMissingRequiredField = "missing_required_field"
	CodeInvalidFieldValue    = "invalid_field_value"
	CodeValidationError      = "validation_error"
)

// OpenAIError represents an error in OpenAI API format.
type OpenAIError struct {
	Message string  `json:"message"`
	Type    string  `json:"type"`
	Param   *string `json:"param,omitempty"`
	Code    string  `json:"code"`
}

// OpenAIErrorResponse is the top-level error response structure.
type OpenAIErrorResponse struct {
	Error OpenAIError `json:"error"`
}

// NewError creates a new OpenAI error with the given parameters.
func NewError(message, errorType, code string) *OpenAIErrorResponse {
	return &OpenAIErrorResponse{
		Error: OpenAIError{
			Message: message,
			Type:    errorType,
			Code:    code,
		},
	}
}

// NewErrorWithParam creates a new OpenAI error with a parameter field.
func NewErrorWithParam(message, errorType, code, param string) *OpenAIErrorResponse {
	return &OpenAIErrorResponse{
		Error: OpenAIError{
			Message: message,
			Type:    errorType,
			Param:   &param,
			Code:    code,
		},
	}
}

// WriteError writes an OpenAI-format error response to the http.ResponseWriter.
func WriteError(w http.ResponseWriter, statusCode int, message, errorType, code string) {
	resp := NewError(message, errorType, code)
	writeJSONError(w, statusCode, resp)
}

// WriteErrorWithParam writes an OpenAI-format error response with a param field.
func WriteErrorWithParam(w http.ResponseWriter, statusCode int, message, errorType, code, param string) {
	resp := NewErrorWithParam(message, errorType, code, param)
	writeJSONError(w, statusCode, resp)
}

// writeJSONError marshals and writes the error response.
func writeJSONError(w http.ResponseWriter, statusCode int, resp *OpenAIErrorResponse) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(statusCode)
	// Ignore encoding errors - if we can't write the error response, there's nothing we can do
	_ = json.NewEncoder(w).Encode(resp)
}

// Common error responses as convenience functions

// WriteBadRequest writes a 400 Bad Request error.
func WriteBadRequest(w http.ResponseWriter, message string) {
	WriteError(w, http.StatusBadRequest, message, ErrorTypeInvalidRequest, CodeInvalidRequestError)
}

// WriteBadRequestWithCode writes a 400 Bad Request error with a specific code.
func WriteBadRequestWithCode(w http.ResponseWriter, message, code string) {
	WriteError(w, http.StatusBadRequest, message, ErrorTypeInvalidRequest, code)
}

// WriteMissingField writes a 400 Bad Request error for a missing required field.
func WriteMissingField(w http.ResponseWriter, fieldName string) {
	WriteErrorWithParam(w, http.StatusBadRequest, "Missing required field: "+fieldName, ErrorTypeInvalidRequest, CodeMissingRequiredField, fieldName)
}

// WriteInvalidFieldValue writes a 400 Bad Request error for an invalid field value.
func WriteInvalidFieldValue(w http.ResponseWriter, fieldName, reason string) {
	WriteErrorWithParam(w, http.StatusBadRequest, "Invalid value for field '"+fieldName+"': "+reason, ErrorTypeInvalidRequest, CodeInvalidFieldValue, fieldName)
}

// WriteNotFound writes a 404 Not Found error.
func WriteNotFound(w http.ResponseWriter, message string) {
	WriteError(w, http.StatusNotFound, message, ErrorTypeNotFound, CodeModelNotFound)
}

// WriteModelNotFound writes a 404 Not Found error for a missing model.
func WriteModelNotFound(w http.ResponseWriter, modelName string) {
	WriteErrorWithParam(w, http.StatusNotFound, "Model '"+modelName+"' not found", ErrorTypeNotFound, CodeModelNotFound, "model")
}

// WriteMethodNotAllowed writes a 405 Method Not Allowed error.
func WriteMethodNotAllowed(w http.ResponseWriter, method string) {
	WriteError(w, http.StatusMethodNotAllowed, "Method "+method+" not allowed", ErrorTypeInvalidRequest, CodeMethodNotAllowed)
}

// WriteInternalError writes a 500 Internal Server Error.
func WriteInternalError(w http.ResponseWriter, message string) {
	WriteError(w, http.StatusInternalServerError, message, ErrorTypeServer, CodeServerError)
}

// WriteServiceUnavailable writes a 503 Service Unavailable error.
func WriteServiceUnavailable(w http.ResponseWriter, message string) {
	WriteError(w, http.StatusServiceUnavailable, message, ErrorTypeServiceUnavailable, CodeServiceUnavailable)
}

// WriteQueueFull writes a 503 Service Unavailable error for queue overflow.
func WriteQueueFull(w http.ResponseWriter) {
	WriteError(w, http.StatusServiceUnavailable, "Service overloaded, please retry", ErrorTypeServiceUnavailable, CodeQueueFull)
}

// WriteActivationFailed writes a 503 Service Unavailable error for failed model activation.
func WriteActivationFailed(w http.ResponseWriter, message string) {
	WriteError(w, http.StatusServiceUnavailable, message, ErrorTypeServiceUnavailable, CodeActivationFailed)
}

// WriteTimeout writes a 504 Gateway Timeout error.
func WriteTimeout(w http.ResponseWriter, message string) {
	WriteError(w, http.StatusGatewayTimeout, message, ErrorTypeTimeout, CodeTimeout)
}

// WriteColdStartTimeout writes a 504 Gateway Timeout error for cold start timeout.
func WriteColdStartTimeout(w http.ResponseWriter, waitedDuration string) {
	WriteError(w, http.StatusGatewayTimeout, "Timeout waiting for model to become ready (waited "+waitedDuration+")", ErrorTypeTimeout, CodeTimeout)
}

// WriteGPUGroupTimeout writes a 504 Gateway Timeout error for GPUGroup activation timeout.
func WriteGPUGroupTimeout(w http.ResponseWriter, waitedDuration string) {
	WriteError(w, http.StatusGatewayTimeout, "Timeout waiting for model to become active (waited "+waitedDuration+")", ErrorTypeTimeout, CodeTimeout)
}
