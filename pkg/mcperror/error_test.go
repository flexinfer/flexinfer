package mcperror

import (
	"errors"
	"strings"
	"testing"
)

func TestNew(t *testing.T) {
	err := New(CodeInvalidInput, "test message")
	if err.Code != CodeInvalidInput {
		t.Errorf("Code = %q, want %q", err.Code, CodeInvalidInput)
	}
	if err.Message != "test message" {
		t.Errorf("Message = %q, want %q", err.Message, "test message")
	}
}

func TestError_Error(t *testing.T) {
	err := New(CodeNotFound, "resource not found")
	got := err.Error()
	if !strings.Contains(got, "NOT_FOUND") {
		t.Errorf("Error() = %q, want to contain code", got)
	}
	if !strings.Contains(got, "resource not found") {
		t.Errorf("Error() = %q, want to contain message", got)
	}
}

func TestWithDetails(t *testing.T) {
	err := New(CodeInvalidInput, "test")
	errWithDetails := err.WithDetails(map[string]string{"key": "value"})

	if errWithDetails.Details == nil {
		t.Error("Details should not be nil")
	}
	details, ok := errWithDetails.Details.(map[string]string)
	if !ok {
		t.Error("Details should be map[string]string")
	}
	if details["key"] != "value" {
		t.Errorf("Details[key] = %q, want 'value'", details["key"])
	}
}

func TestWrap(t *testing.T) {
	original := errors.New("original error")
	wrapped := Wrap(CodeServerError, original)

	if wrapped.Code != CodeServerError {
		t.Errorf("Code = %q, want %q", wrapped.Code, CodeServerError)
	}
	if wrapped.Message != "original error" {
		t.Errorf("Message = %q, want 'original error'", wrapped.Message)
	}

	// Test nil error
	nilWrapped := Wrap(CodeServerError, nil)
	if nilWrapped != nil {
		t.Error("Wrap(nil) should return nil")
	}
}

func TestInvalidInput(t *testing.T) {
	err := InvalidInput("field is required")
	if err.Code != CodeInvalidInput {
		t.Errorf("Code = %q, want %q", err.Code, CodeInvalidInput)
	}
}

func TestNotFound(t *testing.T) {
	err := NotFound("user", "john")
	if err.Code != CodeNotFound {
		t.Errorf("Code = %q, want %q", err.Code, CodeNotFound)
	}
	if !strings.Contains(err.Message, "user") {
		t.Errorf("Message should contain resource type")
	}
	if !strings.Contains(err.Message, "john") {
		t.Errorf("Message should contain resource name")
	}
}

func TestTimeout(t *testing.T) {
	err := Timeout("database query")
	if err.Code != CodeTimeout {
		t.Errorf("Code = %q, want %q", err.Code, CodeTimeout)
	}
	if !strings.Contains(err.Message, "timed out") {
		t.Errorf("Message should contain 'timed out'")
	}
}

func TestConnectionError(t *testing.T) {
	err := ConnectionError("api.example.com", errors.New("connection refused"))
	if err.Code != CodeConnectionError {
		t.Errorf("Code = %q, want %q", err.Code, CodeConnectionError)
	}
	if !strings.Contains(err.Message, "api.example.com") {
		t.Errorf("Message should contain host")
	}
	if err.Details == nil {
		t.Error("Details should not be nil")
	}
}

func TestRateLimited(t *testing.T) {
	err := RateLimited("60")
	if err.Code != CodeRateLimited {
		t.Errorf("Code = %q, want %q", err.Code, CodeRateLimited)
	}
}

func TestValidation(t *testing.T) {
	errs := map[string]string{
		"name":  "is required",
		"email": "is invalid",
	}
	err := Validation(errs)
	if err.Code != CodeValidation {
		t.Errorf("Code = %q, want %q", err.Code, CodeValidation)
	}
}

func TestRequiredParam(t *testing.T) {
	err := RequiredParam("owner")
	if err.Code != CodeInvalidInput {
		t.Errorf("Code = %q, want %q", err.Code, CodeInvalidInput)
	}
	if !strings.Contains(err.Message, "owner") {
		t.Errorf("Message should contain parameter name")
	}
	if !strings.Contains(err.Message, "required") {
		t.Errorf("Message should contain 'required'")
	}
}

func TestInvalidParam(t *testing.T) {
	err := InvalidParam("count", "must be positive")
	if err.Code != CodeInvalidInput {
		t.Errorf("Code = %q, want %q", err.Code, CodeInvalidInput)
	}
	if !strings.Contains(err.Message, "count") {
		t.Errorf("Message should contain parameter name")
	}
	if !strings.Contains(err.Message, "must be positive") {
		t.Errorf("Message should contain reason")
	}
}

func TestAPIError(t *testing.T) {
	tests := []struct {
		statusCode int
		wantMsg    string
	}{
		{401, "authentication"},
		{403, "forbidden"},
		{404, "not found"},
		{429, "rate limit"},
		{500, "unavailable"},
		{502, "unavailable"},
		{503, "unavailable"},
		{504, "unavailable"},
		{400, "http 400"}, // Generic error (lowercased for comparison)
	}

	for _, tt := range tests {
		t.Run(string(rune(tt.statusCode)), func(t *testing.T) {
			err := APIError("GitHub", tt.statusCode, "error body")
			if !strings.Contains(strings.ToLower(err.Message), tt.wantMsg) {
				t.Errorf("APIError(%d).Message = %q, want to contain %q", tt.statusCode, err.Message, tt.wantMsg)
			}
		})
	}
}

func TestWrapAPI(t *testing.T) {
	err := WrapAPI("GitHub", errors.New("timeout"))
	if err.Code != CodeConnectionError {
		t.Errorf("Code = %q, want %q", err.Code, CodeConnectionError)
	}
	if !strings.Contains(err.Message, "GitHub") {
		t.Errorf("Message should contain service name")
	}
	if !strings.Contains(err.Message, "timeout") {
		t.Errorf("Message should contain error")
	}

	// Test nil error
	nilErr := WrapAPI("GitHub", nil)
	if nilErr != nil {
		t.Error("WrapAPI(nil) should return nil")
	}
}

func TestServiceUnavailable(t *testing.T) {
	err := ServiceUnavailable("Qdrant", "connection refused")
	if err.Code != CodeServerError {
		t.Errorf("Code = %q, want %q", err.Code, CodeServerError)
	}
	if !strings.Contains(err.Message, "Qdrant") {
		t.Errorf("Message should contain service name")
	}
	if !strings.Contains(err.Message, "unavailable") {
		t.Errorf("Message should contain 'unavailable'")
	}
}

func TestNotConfigured(t *testing.T) {
	err := NotConfigured("GITHUB_TOKEN", "set via environment variable")
	if err.Code != CodeServerError {
		t.Errorf("Code = %q, want %q", err.Code, CodeServerError)
	}
	if !strings.Contains(err.Message, "GITHUB_TOKEN") {
		t.Errorf("Message should contain config item")
	}
	if !strings.Contains(err.Message, "environment") {
		t.Errorf("Message should contain hint")
	}
}

func TestOperationFailed(t *testing.T) {
	err := OperationFailed("database query", errors.New("connection lost"))
	if err.Code != CodeServerError {
		t.Errorf("Code = %q, want %q", err.Code, CodeServerError)
	}
	if !strings.Contains(err.Message, "database query") {
		t.Errorf("Message should contain operation")
	}
	if !strings.Contains(err.Message, "connection lost") {
		t.Errorf("Message should contain error")
	}

	// Test nil error
	nilErr := OperationFailed("op", nil)
	if nilErr != nil {
		t.Error("OperationFailed(nil) should return nil")
	}
}

func TestParseError(t *testing.T) {
	err := ParseError("JSON response", errors.New("unexpected token"))
	if err.Code != CodeInvalidInput {
		t.Errorf("Code = %q, want %q", err.Code, CodeInvalidInput)
	}
	if !strings.Contains(err.Message, "JSON response") {
		t.Errorf("Message should contain what was being parsed")
	}
}

func TestTruncateString(t *testing.T) {
	tests := []struct {
		input   string
		maxLen  int
		wantLen int
	}{
		{"short", 10, 5},
		{"hello world", 5, 5},
		{"hello world", 8, 8},
		{"", 10, 0},
	}

	for _, tt := range tests {
		got := truncateString(tt.input, tt.maxLen)
		if len(got) > tt.maxLen {
			t.Errorf("truncateString(%q, %d) = %q (len %d), want len <= %d",
				tt.input, tt.maxLen, got, len(got), tt.maxLen)
		}
	}
}

func TestResult(t *testing.T) {
	// Test success
	success := Success(map[string]string{"key": "value"})
	if !success.OK {
		t.Error("Success().OK should be true")
	}
	if success.Data == nil {
		t.Error("Success().Data should not be nil")
	}

	// Test failure
	failure := Failure(InvalidInput("bad input"))
	if failure.OK {
		t.Error("Failure().OK should be false")
	}
	if failure.Error == nil {
		t.Error("Failure().Error should not be nil")
	}

	// Test JSON
	json := success.JSON()
	if json["ok"] != true {
		t.Error("JSON()['ok'] should be true")
	}
}
