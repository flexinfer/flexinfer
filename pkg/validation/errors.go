// Package validation provides OpenAI-compatible error handling and request validation.
package validation

import (
	"encoding/json"
	"fmt"
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
//
// Admission is a flexinfer extension populated on 413 admission rejections so
// clients can render a structured affordance ("your prompt is N tokens over
// budget — truncate to M?") instead of a generic error. The field is
// omitempty so existing clients that only parse `error` see no shape change.
// See docs/planning/context-bounded-admission-spec.md and the F4 brainstorm
// (F4-413-as-feature).
type OpenAIErrorResponse struct {
	Error     OpenAIError       `json:"error"`
	Admission *AdmissionDetails `json:"admission,omitempty"`
}

// AdmissionDetails is the structured payload attached to a 413 admission
// rejection. All token counts are estimator outputs from the proxy admission
// filter; clients should treat them as conservative upper bounds, not exact
// runtime tokenizer results.
type AdmissionDetails struct {
	// Model is the lane the request targeted.
	Model string `json:"model"`

	// TokensBudget is the effective ceiling (raw context window with the
	// configured safety margin applied) the request was compared against.
	TokensBudget int `json:"tokens_budget"`

	// TokensSubmitted is estimated_prompt_tokens + max_tokens — the total
	// budget the request asked for.
	TokensSubmitted int `json:"tokens_submitted"`

	// TokensOver is TokensSubmitted - TokensBudget. Always positive on a
	// rejection.
	TokensOver int `json:"tokens_over"`

	// SuggestTruncateTo is the largest prompt size (in estimated tokens) that
	// would have fit given the current max_tokens reservation. Zero when
	// max_tokens alone meets or exceeds the budget — in that case the client
	// should reduce max_tokens, not the prompt.
	SuggestTruncateTo int `json:"suggest_truncate_to"`

	// ContextWindow is the lane's raw declared context window before the
	// safety margin was applied. Surfaced so clients can show "X of Y" rather
	// than just the post-margin budget.
	ContextWindow int `json:"context_window"`
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

// WriteAdmissionError writes a 413 Payload Too Large response with the
// OpenAI-format error body AND the flexinfer-specific admission extension
// populated. The status code, error type, and error code are fixed because
// this helper exists solely for the context-bounded admission filter.
func WriteAdmissionError(w http.ResponseWriter, message, code string, details AdmissionDetails) {
	resp := &OpenAIErrorResponse{
		Error: OpenAIError{
			Message: message,
			Type:    ErrorTypeInvalidRequest,
			Code:    code,
		},
		Admission: &details,
	}
	writeJSONError(w, http.StatusRequestEntityTooLarge, resp)
}

// writeJSONError marshals and writes the error response.
func writeJSONError(w http.ResponseWriter, statusCode int, resp *OpenAIErrorResponse) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(statusCode)
	// Best-effort: if we can't write the error response, the client likely disconnected.
	if err := json.NewEncoder(w).Encode(resp); err != nil {
		// Nothing else to do here; headers are already written.
		return
	}
}

// Common error responses as convenience functions

// errorClass bundles the fixed (status, type, code) triple that a convenience
// writer emits. Declaring the triples as a table keeps the OpenAI error
// contract — the exact status/type/code each helper produces — auditable in
// one place and removes the per-helper repetition of the WriteError call.
type errorClass struct {
	status  int
	errType string
	code    string
}

// write emits the class's fixed status/type/code with the given message.
func (c errorClass) write(w http.ResponseWriter, message string) {
	WriteError(w, c.status, message, c.errType, c.code)
}

// writeParam is write with an OpenAI `param` field attached.
func (c errorClass) writeParam(w http.ResponseWriter, message, param string) {
	WriteErrorWithParam(w, c.status, message, c.errType, c.code, param)
}

// The set of error classes the convenience writers map onto. Each entry is the
// (status, type, code) contract for a category of failure.
var (
	classBadRequest         = errorClass{http.StatusBadRequest, ErrorTypeInvalidRequest, CodeInvalidRequestError}
	classMissingField       = errorClass{http.StatusBadRequest, ErrorTypeInvalidRequest, CodeMissingRequiredField}
	classInvalidFieldValue  = errorClass{http.StatusBadRequest, ErrorTypeInvalidRequest, CodeInvalidFieldValue}
	classNotFound           = errorClass{http.StatusNotFound, ErrorTypeNotFound, CodeModelNotFound}
	classMethodNotAllowed   = errorClass{http.StatusMethodNotAllowed, ErrorTypeInvalidRequest, CodeMethodNotAllowed}
	classInternal           = errorClass{http.StatusInternalServerError, ErrorTypeServer, CodeServerError}
	classServiceUnavailable = errorClass{http.StatusServiceUnavailable, ErrorTypeServiceUnavailable, CodeServiceUnavailable}
	classQueueFull          = errorClass{http.StatusServiceUnavailable, ErrorTypeServiceUnavailable, CodeQueueFull}
	classActivationFailed   = errorClass{http.StatusServiceUnavailable, ErrorTypeServiceUnavailable, CodeActivationFailed}
	classTimeout            = errorClass{http.StatusGatewayTimeout, ErrorTypeTimeout, CodeTimeout}
	classRateLimit          = errorClass{http.StatusTooManyRequests, ErrorTypeRateLimit, CodeRateLimitExceeded}
	classUnauthorized       = errorClass{http.StatusUnauthorized, ErrorTypeInvalidRequest, CodeInvalidAPIKey}
)

// WriteBadRequest writes a 400 Bad Request error.
func WriteBadRequest(w http.ResponseWriter, message string) {
	classBadRequest.write(w, message)
}

// WriteBadRequestWithCode writes a 400 Bad Request error with a specific code.
func WriteBadRequestWithCode(w http.ResponseWriter, message, code string) {
	WriteError(w, classBadRequest.status, message, classBadRequest.errType, code)
}

// WriteMissingField writes a 400 Bad Request error for a missing required field.
func WriteMissingField(w http.ResponseWriter, fieldName string) {
	classMissingField.writeParam(w, "Missing required field: "+fieldName, fieldName)
}

// WriteInvalidFieldValue writes a 400 Bad Request error for an invalid field value.
func WriteInvalidFieldValue(w http.ResponseWriter, fieldName, reason string) {
	classInvalidFieldValue.writeParam(w, "Invalid value for field '"+fieldName+"': "+reason, fieldName)
}

// WriteNotFound writes a 404 Not Found error.
func WriteNotFound(w http.ResponseWriter, message string) {
	classNotFound.write(w, message)
}

// WriteModelNotFound writes a 404 Not Found error for a missing model.
func WriteModelNotFound(w http.ResponseWriter, modelName string) {
	classNotFound.writeParam(w, "Model '"+modelName+"' not found", "model")
}

// WriteMethodNotAllowed writes a 405 Method Not Allowed error.
func WriteMethodNotAllowed(w http.ResponseWriter, method string) {
	classMethodNotAllowed.write(w, "Method "+method+" not allowed")
}

// WriteInternalError writes a 500 Internal Server Error.
func WriteInternalError(w http.ResponseWriter, message string) {
	classInternal.write(w, message)
}

// WriteServiceUnavailable writes a 503 Service Unavailable error.
func WriteServiceUnavailable(w http.ResponseWriter, message string) {
	classServiceUnavailable.write(w, message)
}

// WriteQueueFull writes a 503 Service Unavailable error for queue overflow.
func WriteQueueFull(w http.ResponseWriter) {
	classQueueFull.write(w, "Service overloaded, please retry")
}

// WriteActivationFailed writes a 503 Service Unavailable error for failed model activation.
func WriteActivationFailed(w http.ResponseWriter, message string) {
	classActivationFailed.write(w, message)
}

// WriteTimeout writes a 504 Gateway Timeout error.
func WriteTimeout(w http.ResponseWriter, message string) {
	classTimeout.write(w, message)
}

// WriteColdStartTimeout writes a 504 Gateway Timeout error for cold start timeout.
func WriteColdStartTimeout(w http.ResponseWriter, waitedDuration string) {
	classTimeout.write(w, "Timeout waiting for model to become ready (waited "+waitedDuration+")")
}

// WriteGPUGroupTimeout writes a 504 Gateway Timeout error for GPUGroup activation timeout.
func WriteGPUGroupTimeout(w http.ResponseWriter, waitedDuration string) {
	classTimeout.write(w, "Timeout waiting for model to become active (waited "+waitedDuration+")")
}

// WriteRateLimited writes a 429 Too Many Requests error with Retry-After header.
func WriteRateLimited(w http.ResponseWriter, retryAfterSeconds int) {
	w.Header().Set("Retry-After", fmt.Sprintf("%d", retryAfterSeconds))
	classRateLimit.write(w, "Rate limit exceeded, please retry later")
}

// WriteStalledLoad writes a 503 Service Unavailable for a model whose cold-start
// load has stopped making progress. Sets Retry-After so clients back off instead
// of retrying into a queue that is about to keep building.
func WriteStalledLoad(w http.ResponseWriter, message string, retryAfterSeconds int) {
	if retryAfterSeconds > 0 {
		w.Header().Set("Retry-After", fmt.Sprintf("%d", retryAfterSeconds))
	}
	classActivationFailed.write(w, message)
}

// WriteUnauthorized writes a 401 Unauthorized error.
func WriteUnauthorized(w http.ResponseWriter, message string) {
	classUnauthorized.write(w, message)
}
