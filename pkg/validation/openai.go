// Package validation provides OpenAI-compatible request validation.
package validation

import (
	"encoding/json"
	"fmt"
	"net/http"
)

// ChatCompletionRequest represents the OpenAI chat completion request schema.
type ChatCompletionRequest struct {
	Model            string         `json:"model"`
	Messages         []ChatMessage  `json:"messages"`
	Temperature      *float64       `json:"temperature,omitempty"`
	TopP             *float64       `json:"top_p,omitempty"`
	N                *int           `json:"n,omitempty"`
	Stream           *bool          `json:"stream,omitempty"`
	Stop             any            `json:"stop,omitempty"` // string or []string
	MaxTokens        *int           `json:"max_tokens,omitempty"`
	PresencePenalty  *float64       `json:"presence_penalty,omitempty"`
	FrequencyPenalty *float64       `json:"frequency_penalty,omitempty"`
	LogitBias        map[string]int `json:"logit_bias,omitempty"`
	User             string         `json:"user,omitempty"`
	// Extended fields supported by various backends
	MaxCompletionTokens *int     `json:"max_completion_tokens,omitempty"`
	TopK                *int     `json:"top_k,omitempty"`
	RepetitionPenalty   *float64 `json:"repetition_penalty,omitempty"`
}

// ChatMessage represents a message in a chat completion request.
type ChatMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
	Name    string `json:"name,omitempty"`
}

// CompletionRequest represents the OpenAI completion request schema.
type CompletionRequest struct {
	Model            string         `json:"model"`
	Prompt           any            `json:"prompt"` // string or []string or []int or [][]int
	Suffix           string         `json:"suffix,omitempty"`
	MaxTokens        *int           `json:"max_tokens,omitempty"`
	Temperature      *float64       `json:"temperature,omitempty"`
	TopP             *float64       `json:"top_p,omitempty"`
	N                *int           `json:"n,omitempty"`
	Stream           *bool          `json:"stream,omitempty"`
	Logprobs         *int           `json:"logprobs,omitempty"`
	Echo             *bool          `json:"echo,omitempty"`
	Stop             any            `json:"stop,omitempty"` // string or []string
	PresencePenalty  *float64       `json:"presence_penalty,omitempty"`
	FrequencyPenalty *float64       `json:"frequency_penalty,omitempty"`
	BestOf           *int           `json:"best_of,omitempty"`
	LogitBias        map[string]int `json:"logit_bias,omitempty"`
	User             string         `json:"user,omitempty"`
}

// EmbeddingRequest represents the OpenAI embedding request schema.
type EmbeddingRequest struct {
	Model          string `json:"model"`
	Input          any    `json:"input"` // string or []string or []int or [][]int
	EncodingFormat string `json:"encoding_format,omitempty"`
	Dimensions     *int   `json:"dimensions,omitempty"`
	User           string `json:"user,omitempty"`
}

// ValidationResult contains the result of request validation.
type ValidationResult struct {
	Valid  bool
	Errors []ValidationError
}

// ValidationError represents a single validation error.
type ValidationError struct {
	Field   string
	Message string
}

// add records a single validation failure and flips the result invalid. Every
// endpoint validator funnels through this so the error ordering — which
// WriteValidationErrors depends on for the surfaced param and the
// "(and N more errors)" count — stays consistent.
func (r *ValidationResult) add(field, message string) {
	r.Valid = false
	r.Errors = append(r.Errors, ValidationError{Field: field, Message: message})
}

// decodeRequest runs the empty-body and JSON-unmarshal phases shared by every
// endpoint validator. It returns ok=false with the fatal error already
// recorded when the body cannot be decoded, signalling the caller to return
// the result immediately.
func decodeRequest[T any](body []byte, req *T) (*ValidationResult, bool) {
	result := &ValidationResult{Valid: true}
	if len(body) == 0 {
		result.add("", "Request body is empty")
		return result, false
	}
	if err := json.Unmarshal(body, req); err != nil {
		result.add("", fmt.Sprintf("Invalid JSON: %v", err))
		return result, false
	}
	return result, true
}

// requireModel records the shared "model is required" error.
func (r *ValidationResult) requireModel(model string) {
	if model == "" {
		r.add("model", "model is required")
	}
}

// checkSamplingCommon applies the numeric-range rules shared by the chat and
// completion endpoints, in their canonical order (temperature, top_p, n,
// max_tokens). A nil pointer means the field was omitted and is skipped.
func (r *ValidationResult) checkSamplingCommon(temperature, topP *float64, n, maxTokens *int) {
	if temperature != nil && (*temperature < 0 || *temperature > 2) {
		r.add("temperature", "temperature must be between 0 and 2")
	}
	if topP != nil && (*topP < 0 || *topP > 1) {
		r.add("top_p", "top_p must be between 0 and 1")
	}
	if n != nil && *n < 1 {
		r.add("n", "n must be at least 1")
	}
	if maxTokens != nil && *maxTokens < 1 {
		r.add("max_tokens", "max_tokens must be at least 1")
	}
}

// checkPenalty applies the shared [-2, 2] range rule for presence/frequency
// penalties. field is the JSON field name used in both the param and message.
func (r *ValidationResult) checkPenalty(field string, v *float64) {
	if v != nil && (*v < -2 || *v > 2) {
		r.add(field, field+" must be between -2 and 2")
	}
}

// ValidateChatCompletionRequest validates a chat completion request body.
func ValidateChatCompletionRequest(body []byte) *ValidationResult {
	var req ChatCompletionRequest
	result, ok := decodeRequest(body, &req)
	if !ok {
		return result
	}

	result.requireModel(req.Model)

	if len(req.Messages) == 0 {
		result.add("messages", "messages is required and must not be empty")
	}

	// Validate messages
	for i, msg := range req.Messages {
		if msg.Role == "" {
			result.add(fmt.Sprintf("messages[%d].role", i), "role is required")
		} else if msg.Role != "system" && msg.Role != "user" && msg.Role != "assistant" && msg.Role != "tool" && msg.Role != "function" {
			result.add(
				fmt.Sprintf("messages[%d].role", i),
				fmt.Sprintf("role must be one of: system, user, assistant, tool, function (got '%s')", msg.Role),
			)
		}
	}

	// Validate optional numeric fields
	result.checkSamplingCommon(req.Temperature, req.TopP, req.N, req.MaxTokens)
	result.checkPenalty("presence_penalty", req.PresencePenalty)
	result.checkPenalty("frequency_penalty", req.FrequencyPenalty)

	return result
}

// ValidateCompletionRequest validates a completion request body.
func ValidateCompletionRequest(body []byte) *ValidationResult {
	var req CompletionRequest
	result, ok := decodeRequest(body, &req)
	if !ok {
		return result
	}

	result.requireModel(req.Model)

	// Prompt can be empty for some backends, but validate if provided
	// The type can be string, []string, []int, or [][]int

	// Validate optional numeric fields (same as chat completion)
	result.checkSamplingCommon(req.Temperature, req.TopP, req.N, req.MaxTokens)

	if req.BestOf != nil && *req.BestOf < 1 {
		result.add("best_of", "best_of must be at least 1")
	}

	if req.Logprobs != nil && *req.Logprobs < 0 {
		result.add("logprobs", "logprobs must be non-negative")
	}

	return result
}

// ValidateEmbeddingRequest validates an embedding request body.
func ValidateEmbeddingRequest(body []byte) *ValidationResult {
	var req EmbeddingRequest
	result, ok := decodeRequest(body, &req)
	if !ok {
		return result
	}

	result.requireModel(req.Model)

	if req.Input == nil {
		result.add("input", "input is required")
	}

	// Validate encoding_format if provided
	if req.EncodingFormat != "" && req.EncodingFormat != "float" && req.EncodingFormat != "base64" {
		result.add("encoding_format", "encoding_format must be 'float' or 'base64'")
	}

	// Validate dimensions if provided
	if req.Dimensions != nil && *req.Dimensions < 1 {
		result.add("dimensions", "dimensions must be at least 1")
	}

	return result
}

// WriteValidationErrors writes validation errors as an OpenAI error response.
func WriteValidationErrors(w http.ResponseWriter, result *ValidationResult) {
	if result.Valid || len(result.Errors) == 0 {
		return
	}

	// Format all errors into a single message
	msg := result.Errors[0].Message
	param := result.Errors[0].Field
	if len(result.Errors) > 1 {
		msg = fmt.Sprintf("%s (and %d more errors)", msg, len(result.Errors)-1)
	}

	if param != "" {
		WriteErrorWithParam(w, http.StatusBadRequest, msg, ErrorTypeInvalidRequest, CodeValidationError, param)
	} else {
		WriteError(w, http.StatusBadRequest, msg, ErrorTypeInvalidRequest, CodeValidationError)
	}
}

// ValidateRequest validates a request body based on the endpoint path.
// Returns nil if validation passes or is not applicable for the endpoint.
func ValidateRequest(path string, body []byte) *ValidationResult {
	switch {
	case path == "/v1/chat/completions" || pathEndsWith(path, "/v1/chat/completions"):
		return ValidateChatCompletionRequest(body)
	case path == "/v1/completions" || pathEndsWith(path, "/v1/completions"):
		return ValidateCompletionRequest(body)
	case path == "/v1/embeddings" || pathEndsWith(path, "/v1/embeddings"):
		return ValidateEmbeddingRequest(body)
	default:
		// No validation for unknown endpoints
		return nil
	}
}

// pathEndsWith checks if a path ends with the given suffix.
func pathEndsWith(path, suffix string) bool {
	if len(path) < len(suffix) {
		return false
	}
	return path[len(path)-len(suffix):] == suffix
}
