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

// ValidateChatCompletionRequest validates a chat completion request body.
func ValidateChatCompletionRequest(body []byte) *ValidationResult {
	result := &ValidationResult{Valid: true}

	if len(body) == 0 {
		result.Valid = false
		result.Errors = append(result.Errors, ValidationError{
			Field:   "",
			Message: "Request body is empty",
		})
		return result
	}

	var req ChatCompletionRequest
	if err := json.Unmarshal(body, &req); err != nil {
		result.Valid = false
		result.Errors = append(result.Errors, ValidationError{
			Field:   "",
			Message: fmt.Sprintf("Invalid JSON: %v", err),
		})
		return result
	}

	// Validate required fields
	if req.Model == "" {
		result.Valid = false
		result.Errors = append(result.Errors, ValidationError{
			Field:   "model",
			Message: "model is required",
		})
	}

	if len(req.Messages) == 0 {
		result.Valid = false
		result.Errors = append(result.Errors, ValidationError{
			Field:   "messages",
			Message: "messages is required and must not be empty",
		})
	}

	// Validate messages
	for i, msg := range req.Messages {
		if msg.Role == "" {
			result.Valid = false
			result.Errors = append(result.Errors, ValidationError{
				Field:   fmt.Sprintf("messages[%d].role", i),
				Message: "role is required",
			})
		} else if msg.Role != "system" && msg.Role != "user" && msg.Role != "assistant" && msg.Role != "tool" && msg.Role != "function" {
			result.Valid = false
			result.Errors = append(result.Errors, ValidationError{
				Field:   fmt.Sprintf("messages[%d].role", i),
				Message: fmt.Sprintf("role must be one of: system, user, assistant, tool, function (got '%s')", msg.Role),
			})
		}
	}

	// Validate optional numeric fields
	if req.Temperature != nil && (*req.Temperature < 0 || *req.Temperature > 2) {
		result.Valid = false
		result.Errors = append(result.Errors, ValidationError{
			Field:   "temperature",
			Message: "temperature must be between 0 and 2",
		})
	}

	if req.TopP != nil && (*req.TopP < 0 || *req.TopP > 1) {
		result.Valid = false
		result.Errors = append(result.Errors, ValidationError{
			Field:   "top_p",
			Message: "top_p must be between 0 and 1",
		})
	}

	if req.N != nil && *req.N < 1 {
		result.Valid = false
		result.Errors = append(result.Errors, ValidationError{
			Field:   "n",
			Message: "n must be at least 1",
		})
	}

	if req.MaxTokens != nil && *req.MaxTokens < 1 {
		result.Valid = false
		result.Errors = append(result.Errors, ValidationError{
			Field:   "max_tokens",
			Message: "max_tokens must be at least 1",
		})
	}

	if req.PresencePenalty != nil && (*req.PresencePenalty < -2 || *req.PresencePenalty > 2) {
		result.Valid = false
		result.Errors = append(result.Errors, ValidationError{
			Field:   "presence_penalty",
			Message: "presence_penalty must be between -2 and 2",
		})
	}

	if req.FrequencyPenalty != nil && (*req.FrequencyPenalty < -2 || *req.FrequencyPenalty > 2) {
		result.Valid = false
		result.Errors = append(result.Errors, ValidationError{
			Field:   "frequency_penalty",
			Message: "frequency_penalty must be between -2 and 2",
		})
	}

	return result
}

// ValidateCompletionRequest validates a completion request body.
func ValidateCompletionRequest(body []byte) *ValidationResult {
	result := &ValidationResult{Valid: true}

	if len(body) == 0 {
		result.Valid = false
		result.Errors = append(result.Errors, ValidationError{
			Field:   "",
			Message: "Request body is empty",
		})
		return result
	}

	var req CompletionRequest
	if err := json.Unmarshal(body, &req); err != nil {
		result.Valid = false
		result.Errors = append(result.Errors, ValidationError{
			Field:   "",
			Message: fmt.Sprintf("Invalid JSON: %v", err),
		})
		return result
	}

	// Validate required fields
	if req.Model == "" {
		result.Valid = false
		result.Errors = append(result.Errors, ValidationError{
			Field:   "model",
			Message: "model is required",
		})
	}

	// Prompt can be empty for some backends, but validate if provided
	// The type can be string, []string, []int, or [][]int

	// Validate optional numeric fields (same as chat completion)
	if req.Temperature != nil && (*req.Temperature < 0 || *req.Temperature > 2) {
		result.Valid = false
		result.Errors = append(result.Errors, ValidationError{
			Field:   "temperature",
			Message: "temperature must be between 0 and 2",
		})
	}

	if req.TopP != nil && (*req.TopP < 0 || *req.TopP > 1) {
		result.Valid = false
		result.Errors = append(result.Errors, ValidationError{
			Field:   "top_p",
			Message: "top_p must be between 0 and 1",
		})
	}

	if req.N != nil && *req.N < 1 {
		result.Valid = false
		result.Errors = append(result.Errors, ValidationError{
			Field:   "n",
			Message: "n must be at least 1",
		})
	}

	if req.MaxTokens != nil && *req.MaxTokens < 1 {
		result.Valid = false
		result.Errors = append(result.Errors, ValidationError{
			Field:   "max_tokens",
			Message: "max_tokens must be at least 1",
		})
	}

	if req.BestOf != nil && *req.BestOf < 1 {
		result.Valid = false
		result.Errors = append(result.Errors, ValidationError{
			Field:   "best_of",
			Message: "best_of must be at least 1",
		})
	}

	if req.Logprobs != nil && *req.Logprobs < 0 {
		result.Valid = false
		result.Errors = append(result.Errors, ValidationError{
			Field:   "logprobs",
			Message: "logprobs must be non-negative",
		})
	}

	return result
}

// ValidateEmbeddingRequest validates an embedding request body.
func ValidateEmbeddingRequest(body []byte) *ValidationResult {
	result := &ValidationResult{Valid: true}

	if len(body) == 0 {
		result.Valid = false
		result.Errors = append(result.Errors, ValidationError{
			Field:   "",
			Message: "Request body is empty",
		})
		return result
	}

	var req EmbeddingRequest
	if err := json.Unmarshal(body, &req); err != nil {
		result.Valid = false
		result.Errors = append(result.Errors, ValidationError{
			Field:   "",
			Message: fmt.Sprintf("Invalid JSON: %v", err),
		})
		return result
	}

	// Validate required fields
	if req.Model == "" {
		result.Valid = false
		result.Errors = append(result.Errors, ValidationError{
			Field:   "model",
			Message: "model is required",
		})
	}

	if req.Input == nil {
		result.Valid = false
		result.Errors = append(result.Errors, ValidationError{
			Field:   "input",
			Message: "input is required",
		})
	}

	// Validate encoding_format if provided
	if req.EncodingFormat != "" && req.EncodingFormat != "float" && req.EncodingFormat != "base64" {
		result.Valid = false
		result.Errors = append(result.Errors, ValidationError{
			Field:   "encoding_format",
			Message: "encoding_format must be 'float' or 'base64'",
		})
	}

	// Validate dimensions if provided
	if req.Dimensions != nil && *req.Dimensions < 1 {
		result.Valid = false
		result.Errors = append(result.Errors, ValidationError{
			Field:   "dimensions",
			Message: "dimensions must be at least 1",
		})
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
