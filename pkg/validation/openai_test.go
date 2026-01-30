package validation

import (
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestValidateChatCompletionRequest_Valid(t *testing.T) {
	body := []byte(`{
		"model": "gpt-4",
		"messages": [
			{"role": "user", "content": "Hello"}
		]
	}`)

	result := ValidateChatCompletionRequest(body)
	assert.True(t, result.Valid)
	assert.Empty(t, result.Errors)
}

func TestValidateChatCompletionRequest_MissingModel(t *testing.T) {
	body := []byte(`{
		"messages": [
			{"role": "user", "content": "Hello"}
		]
	}`)

	result := ValidateChatCompletionRequest(body)
	assert.False(t, result.Valid)
	require.Len(t, result.Errors, 1)
	assert.Equal(t, "model", result.Errors[0].Field)
	assert.Contains(t, result.Errors[0].Message, "required")
}

func TestValidateChatCompletionRequest_MissingMessages(t *testing.T) {
	body := []byte(`{
		"model": "gpt-4"
	}`)

	result := ValidateChatCompletionRequest(body)
	assert.False(t, result.Valid)
	require.Len(t, result.Errors, 1)
	assert.Equal(t, "messages", result.Errors[0].Field)
}

func TestValidateChatCompletionRequest_EmptyMessages(t *testing.T) {
	body := []byte(`{
		"model": "gpt-4",
		"messages": []
	}`)

	result := ValidateChatCompletionRequest(body)
	assert.False(t, result.Valid)
	require.Len(t, result.Errors, 1)
	assert.Equal(t, "messages", result.Errors[0].Field)
}

func TestValidateChatCompletionRequest_InvalidRole(t *testing.T) {
	body := []byte(`{
		"model": "gpt-4",
		"messages": [
			{"role": "invalid_role", "content": "Hello"}
		]
	}`)

	result := ValidateChatCompletionRequest(body)
	assert.False(t, result.Valid)
	require.Len(t, result.Errors, 1)
	assert.Contains(t, result.Errors[0].Field, "messages[0].role")
}

func TestValidateChatCompletionRequest_ValidRoles(t *testing.T) {
	validRoles := []string{"system", "user", "assistant", "tool", "function"}
	for _, role := range validRoles {
		body := []byte(`{
			"model": "gpt-4",
			"messages": [
				{"role": "` + role + `", "content": "Hello"}
			]
		}`)

		result := ValidateChatCompletionRequest(body)
		assert.True(t, result.Valid, "Role %s should be valid", role)
	}
}

func TestValidateChatCompletionRequest_InvalidTemperature(t *testing.T) {
	body := []byte(`{
		"model": "gpt-4",
		"messages": [{"role": "user", "content": "Hello"}],
		"temperature": 3.0
	}`)

	result := ValidateChatCompletionRequest(body)
	assert.False(t, result.Valid)
	require.Len(t, result.Errors, 1)
	assert.Equal(t, "temperature", result.Errors[0].Field)
}

func TestValidateChatCompletionRequest_InvalidTopP(t *testing.T) {
	body := []byte(`{
		"model": "gpt-4",
		"messages": [{"role": "user", "content": "Hello"}],
		"top_p": 1.5
	}`)

	result := ValidateChatCompletionRequest(body)
	assert.False(t, result.Valid)
	require.Len(t, result.Errors, 1)
	assert.Equal(t, "top_p", result.Errors[0].Field)
}

func TestValidateChatCompletionRequest_InvalidN(t *testing.T) {
	body := []byte(`{
		"model": "gpt-4",
		"messages": [{"role": "user", "content": "Hello"}],
		"n": 0
	}`)

	result := ValidateChatCompletionRequest(body)
	assert.False(t, result.Valid)
	require.Len(t, result.Errors, 1)
	assert.Equal(t, "n", result.Errors[0].Field)
}

func TestValidateChatCompletionRequest_InvalidMaxTokens(t *testing.T) {
	body := []byte(`{
		"model": "gpt-4",
		"messages": [{"role": "user", "content": "Hello"}],
		"max_tokens": 0
	}`)

	result := ValidateChatCompletionRequest(body)
	assert.False(t, result.Valid)
	require.Len(t, result.Errors, 1)
	assert.Equal(t, "max_tokens", result.Errors[0].Field)
}

func TestValidateChatCompletionRequest_EmptyBody(t *testing.T) {
	result := ValidateChatCompletionRequest([]byte{})
	assert.False(t, result.Valid)
	require.Len(t, result.Errors, 1)
	assert.Contains(t, result.Errors[0].Message, "empty")
}

func TestValidateChatCompletionRequest_InvalidJSON(t *testing.T) {
	body := []byte(`{invalid json}`)

	result := ValidateChatCompletionRequest(body)
	assert.False(t, result.Valid)
	require.Len(t, result.Errors, 1)
	assert.Contains(t, result.Errors[0].Message, "Invalid JSON")
}

func TestValidateCompletionRequest_Valid(t *testing.T) {
	body := []byte(`{
		"model": "gpt-4",
		"prompt": "Hello"
	}`)

	result := ValidateCompletionRequest(body)
	assert.True(t, result.Valid)
	assert.Empty(t, result.Errors)
}

func TestValidateCompletionRequest_MissingModel(t *testing.T) {
	body := []byte(`{
		"prompt": "Hello"
	}`)

	result := ValidateCompletionRequest(body)
	assert.False(t, result.Valid)
	require.Len(t, result.Errors, 1)
	assert.Equal(t, "model", result.Errors[0].Field)
}

func TestValidateCompletionRequest_InvalidBestOf(t *testing.T) {
	body := []byte(`{
		"model": "gpt-4",
		"prompt": "Hello",
		"best_of": 0
	}`)

	result := ValidateCompletionRequest(body)
	assert.False(t, result.Valid)
	require.Len(t, result.Errors, 1)
	assert.Equal(t, "best_of", result.Errors[0].Field)
}

func TestValidateEmbeddingRequest_Valid(t *testing.T) {
	body := []byte(`{
		"model": "text-embedding-ada-002",
		"input": "Hello world"
	}`)

	result := ValidateEmbeddingRequest(body)
	assert.True(t, result.Valid)
	assert.Empty(t, result.Errors)
}

func TestValidateEmbeddingRequest_MissingModel(t *testing.T) {
	body := []byte(`{
		"input": "Hello world"
	}`)

	result := ValidateEmbeddingRequest(body)
	assert.False(t, result.Valid)
	require.Len(t, result.Errors, 1)
	assert.Equal(t, "model", result.Errors[0].Field)
}

func TestValidateEmbeddingRequest_MissingInput(t *testing.T) {
	body := []byte(`{
		"model": "text-embedding-ada-002"
	}`)

	result := ValidateEmbeddingRequest(body)
	assert.False(t, result.Valid)
	require.Len(t, result.Errors, 1)
	assert.Equal(t, "input", result.Errors[0].Field)
}

func TestValidateEmbeddingRequest_InvalidEncodingFormat(t *testing.T) {
	body := []byte(`{
		"model": "text-embedding-ada-002",
		"input": "Hello",
		"encoding_format": "invalid"
	}`)

	result := ValidateEmbeddingRequest(body)
	assert.False(t, result.Valid)
	require.Len(t, result.Errors, 1)
	assert.Equal(t, "encoding_format", result.Errors[0].Field)
}

func TestValidateEmbeddingRequest_ValidEncodingFormats(t *testing.T) {
	formats := []string{"float", "base64"}
	for _, format := range formats {
		body := []byte(`{
			"model": "text-embedding-ada-002",
			"input": "Hello",
			"encoding_format": "` + format + `"
		}`)

		result := ValidateEmbeddingRequest(body)
		assert.True(t, result.Valid, "Format %s should be valid", format)
	}
}

func TestValidateRequest_ChatCompletions(t *testing.T) {
	body := []byte(`{"model": "gpt-4", "messages": [{"role": "user", "content": "Hi"}]}`)

	result := ValidateRequest("/v1/chat/completions", body)
	require.NotNil(t, result)
	assert.True(t, result.Valid)

	// Also test with model prefix path
	result = ValidateRequest("/model/test/v1/chat/completions", body)
	require.NotNil(t, result)
	assert.True(t, result.Valid)
}

func TestValidateRequest_Completions(t *testing.T) {
	body := []byte(`{"model": "gpt-4", "prompt": "Hello"}`)

	result := ValidateRequest("/v1/completions", body)
	require.NotNil(t, result)
	assert.True(t, result.Valid)
}

func TestValidateRequest_Embeddings(t *testing.T) {
	body := []byte(`{"model": "ada", "input": "test"}`)

	result := ValidateRequest("/v1/embeddings", body)
	require.NotNil(t, result)
	assert.True(t, result.Valid)
}

func TestValidateRequest_UnknownEndpoint(t *testing.T) {
	body := []byte(`{}`)

	result := ValidateRequest("/v1/unknown", body)
	assert.Nil(t, result, "Unknown endpoints should return nil (no validation)")
}

func TestWriteValidationErrors(t *testing.T) {
	w := httptest.NewRecorder()

	result := &ValidationResult{
		Valid: false,
		Errors: []ValidationError{
			{Field: "model", Message: "model is required"},
		},
	}

	WriteValidationErrors(w, result)

	resp := w.Result()
	assert.Equal(t, 400, resp.StatusCode)
	assert.Equal(t, "application/json", resp.Header.Get("Content-Type"))
}

func TestWriteValidationErrors_MultipleErrors(t *testing.T) {
	w := httptest.NewRecorder()

	result := &ValidationResult{
		Valid: false,
		Errors: []ValidationError{
			{Field: "model", Message: "model is required"},
			{Field: "messages", Message: "messages is required"},
		},
	}

	WriteValidationErrors(w, result)

	resp := w.Result()
	assert.Equal(t, 400, resp.StatusCode)
}

func TestWriteValidationErrors_NoErrors(t *testing.T) {
	w := httptest.NewRecorder()

	result := &ValidationResult{Valid: true, Errors: nil}
	WriteValidationErrors(w, result)

	// Should not write anything for valid result
	assert.Equal(t, 200, w.Code) // Default status
	assert.Empty(t, w.Body.String())
}
