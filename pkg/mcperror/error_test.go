package mcperror

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"
)

// ---------------------------------------------------------------------------
// New
// ---------------------------------------------------------------------------

func TestNew(t *testing.T) {
	t.Parallel()
	err := New("CUSTOM_CODE", "custom message")
	if err.Code != "CUSTOM_CODE" {
		t.Errorf("Code = %q, want %q", err.Code, "CUSTOM_CODE")
	}
	if err.Message != "custom message" {
		t.Errorf("Message = %q, want %q", err.Message, "custom message")
	}
	if err.Details != nil {
		t.Errorf("Details = %v, want nil", err.Details)
	}
}

// ---------------------------------------------------------------------------
// Error.Error()
// ---------------------------------------------------------------------------

func TestError_Error(t *testing.T) {
	t.Parallel()
	err := New(CodeNotFound, "resource not found")
	got := err.Error()
	want := "NOT_FOUND: resource not found"
	if got != want {
		t.Errorf("Error() = %q, want %q", got, want)
	}
}

// ---------------------------------------------------------------------------
// Error.JSON()
// ---------------------------------------------------------------------------

func TestError_JSON(t *testing.T) {
	t.Parallel()
	t.Run("without details", func(t *testing.T) {
		t.Parallel()
		err := New(CodeInvalidInput, "bad input")
		raw := err.JSON()
		var parsed map[string]any
		if e := json.Unmarshal([]byte(raw), &parsed); e != nil {
			t.Fatalf("JSON() produced invalid JSON: %v", e)
		}
		if parsed["code"] != CodeInvalidInput {
			t.Errorf("code = %v, want %q", parsed["code"], CodeInvalidInput)
		}
		if parsed["message"] != "bad input" {
			t.Errorf("message = %v, want %q", parsed["message"], "bad input")
		}
	})
	t.Run("with details", func(t *testing.T) {
		t.Parallel()
		err := New(CodeInvalidInput, "bad").WithDetails(map[string]string{"field": "name"})
		raw := err.JSON()
		var parsed map[string]any
		if e := json.Unmarshal([]byte(raw), &parsed); e != nil {
			t.Fatalf("JSON() produced invalid JSON: %v", e)
		}
		details, ok := parsed["details"].(map[string]any)
		if !ok {
			t.Fatalf("details is not a map: %T", parsed["details"])
		}
		if details["field"] != "name" {
			t.Errorf("details.field = %v, want %q", details["field"], "name")
		}
	})
}

// ---------------------------------------------------------------------------
// WithDetails
// ---------------------------------------------------------------------------

func TestError_WithDetails(t *testing.T) {
	t.Parallel()
	original := New(CodeInvalidInput, "test")
	detailed := original.WithDetails(map[string]string{"key": "value"})

	// Returns a new copy, not mutating original.
	if original.Details != nil {
		t.Error("original Details should remain nil")
	}
	if detailed.Details == nil {
		t.Fatal("detailed.Details should not be nil")
	}
	if detailed.Code != original.Code {
		t.Errorf("Code changed: %q vs %q", detailed.Code, original.Code)
	}
	if detailed.Message != original.Message {
		t.Errorf("Message changed: %q vs %q", detailed.Message, original.Message)
	}
	d, ok := detailed.Details.(map[string]string)
	if !ok {
		t.Fatalf("Details type = %T, want map[string]string", detailed.Details)
	}
	if d["key"] != "value" {
		t.Errorf("Details[key] = %q, want %q", d["key"], "value")
	}
}

// ---------------------------------------------------------------------------
// Wrap
// ---------------------------------------------------------------------------

func TestWrap(t *testing.T) {
	t.Parallel()
	t.Run("wraps error", func(t *testing.T) {
		t.Parallel()
		original := errors.New("original error")
		wrapped := Wrap(CodeServerError, original)
		if wrapped.Code != CodeServerError {
			t.Errorf("Code = %q, want %q", wrapped.Code, CodeServerError)
		}
		if wrapped.Message != "original error" {
			t.Errorf("Message = %q, want %q", wrapped.Message, "original error")
		}
	})
	t.Run("nil returns nil", func(t *testing.T) {
		t.Parallel()
		if got := Wrap(CodeServerError, nil); got != nil {
			t.Errorf("Wrap(nil) = %v, want nil", got)
		}
	})
}

// ---------------------------------------------------------------------------
// Convenience constructors
// ---------------------------------------------------------------------------

func TestInvalidInput(t *testing.T) {
	t.Parallel()
	err := InvalidInput("field is required")
	if err.Code != CodeInvalidInput {
		t.Errorf("Code = %q, want %q", err.Code, CodeInvalidInput)
	}
	if err.Message != "field is required" {
		t.Errorf("Message = %q, want %q", err.Message, "field is required")
	}
}

func TestNotFound(t *testing.T) {
	t.Parallel()
	err := NotFound("user", "john")
	if err.Code != CodeNotFound {
		t.Errorf("Code = %q, want %q", err.Code, CodeNotFound)
	}
	if !strings.Contains(err.Message, "user") {
		t.Error("Message should contain resource type")
	}
	if !strings.Contains(err.Message, "'john'") {
		t.Error("Message should contain quoted resource name")
	}
}

func TestUnauthorized(t *testing.T) {
	t.Parallel()
	err := Unauthorized("invalid token")
	if err.Code != CodeUnauthorized {
		t.Errorf("Code = %q, want %q", err.Code, CodeUnauthorized)
	}
	if err.Message != "invalid token" {
		t.Errorf("Message = %q, want %q", err.Message, "invalid token")
	}
}

func TestForbidden(t *testing.T) {
	t.Parallel()
	err := Forbidden("access denied")
	if err.Code != CodeForbidden {
		t.Errorf("Code = %q, want %q", err.Code, CodeForbidden)
	}
	if err.Message != "access denied" {
		t.Errorf("Message = %q, want %q", err.Message, "access denied")
	}
}

func TestTimeout(t *testing.T) {
	t.Parallel()
	err := Timeout("database query")
	if err.Code != CodeTimeout {
		t.Errorf("Code = %q, want %q", err.Code, CodeTimeout)
	}
	if !strings.Contains(err.Message, "database query") {
		t.Error("Message should contain operation name")
	}
	if !strings.Contains(err.Message, "timed out") {
		t.Error("Message should contain 'timed out'")
	}
}

func TestServerError(t *testing.T) {
	t.Parallel()
	err := ServerError("internal failure")
	if err.Code != CodeServerError {
		t.Errorf("Code = %q, want %q", err.Code, CodeServerError)
	}
	if err.Message != "internal failure" {
		t.Errorf("Message = %q, want %q", err.Message, "internal failure")
	}
}

// ---------------------------------------------------------------------------
// ConnectionError
// ---------------------------------------------------------------------------

func TestConnectionError(t *testing.T) {
	t.Parallel()
	err := ConnectionError("api.example.com", errors.New("connection refused"))
	if err.Code != CodeConnectionError {
		t.Errorf("Code = %q, want %q", err.Code, CodeConnectionError)
	}
	if !strings.Contains(err.Message, "api.example.com") {
		t.Error("Message should contain host")
	}
	details, ok := err.Details.(map[string]string)
	if !ok {
		t.Fatalf("Details type = %T, want map[string]string", err.Details)
	}
	if details["error"] != "connection refused" {
		t.Errorf("Details[error] = %q, want %q", details["error"], "connection refused")
	}
}

// ---------------------------------------------------------------------------
// RateLimited
// ---------------------------------------------------------------------------

func TestRateLimited(t *testing.T) {
	t.Parallel()
	err := RateLimited("60")
	if err.Code != CodeRateLimited {
		t.Errorf("Code = %q, want %q", err.Code, CodeRateLimited)
	}
	if !strings.Contains(err.Message, "rate limit") {
		t.Error("Message should mention rate limit")
	}
	details, ok := err.Details.(map[string]string)
	if !ok {
		t.Fatalf("Details type = %T, want map[string]string", err.Details)
	}
	if details["retry_after"] != "60" {
		t.Errorf("Details[retry_after] = %q, want %q", details["retry_after"], "60")
	}
}

// ---------------------------------------------------------------------------
// Validation
// ---------------------------------------------------------------------------

func TestValidation(t *testing.T) {
	t.Parallel()
	fieldErrors := map[string]string{
		"name":  "is required",
		"email": "is invalid",
	}
	err := Validation(fieldErrors)
	if err.Code != CodeValidation {
		t.Errorf("Code = %q, want %q", err.Code, CodeValidation)
	}
	if !strings.Contains(err.Message, "validation") {
		t.Error("Message should mention validation")
	}
	details, ok := err.Details.(map[string]string)
	if !ok {
		t.Fatalf("Details type = %T, want map[string]string", err.Details)
	}
	if details["name"] != "is required" {
		t.Errorf("Details[name] = %q, want %q", details["name"], "is required")
	}
	if details["email"] != "is invalid" {
		t.Errorf("Details[email] = %q, want %q", details["email"], "is invalid")
	}
}

// ---------------------------------------------------------------------------
// Success / Failure / Result.JSON()
// ---------------------------------------------------------------------------

func TestSuccess(t *testing.T) {
	t.Parallel()
	r := Success(map[string]string{"id": "123"})
	if !r.OK {
		t.Error("OK should be true")
	}
	if r.Data == nil {
		t.Error("Data should not be nil")
	}
	if r.Error != nil {
		t.Error("Error should be nil for success")
	}
}

func TestFailure(t *testing.T) {
	t.Parallel()
	err := InvalidInput("bad")
	r := Failure(err)
	if r.OK {
		t.Error("OK should be false")
	}
	if r.Error == nil {
		t.Fatal("Error should not be nil for failure")
	}
	if r.Error.Code != CodeInvalidInput {
		t.Errorf("Error.Code = %q, want %q", r.Error.Code, CodeInvalidInput)
	}
	if r.Data != nil {
		t.Error("Data should be nil for failure")
	}
}

func TestResult_JSON(t *testing.T) {
	t.Parallel()
	t.Run("success", func(t *testing.T) {
		t.Parallel()
		r := Success("hello")
		j := r.JSON()
		if j["ok"] != true {
			t.Errorf("ok = %v, want true", j["ok"])
		}
		if j["data"] != "hello" {
			t.Errorf("data = %v, want %q", j["data"], "hello")
		}
		if _, exists := j["error"]; exists {
			t.Error("error key should not be present in success result")
		}
	})
	t.Run("failure", func(t *testing.T) {
		t.Parallel()
		r := Failure(NotFound("item", "42"))
		j := r.JSON()
		if j["ok"] != false {
			t.Errorf("ok = %v, want false", j["ok"])
		}
		if _, exists := j["data"]; exists {
			t.Error("data key should not be present in failure result")
		}
		if j["error"] == nil {
			t.Error("error should be present in failure result")
		}
	})
	t.Run("success with nil data", func(t *testing.T) {
		t.Parallel()
		r := Success(nil)
		j := r.JSON()
		if j["ok"] != true {
			t.Errorf("ok = %v, want true", j["ok"])
		}
		if _, exists := j["data"]; exists {
			t.Error("data key should not be present when data is nil")
		}
	})
}

// ---------------------------------------------------------------------------
// RequiredParam
// ---------------------------------------------------------------------------

func TestRequiredParam(t *testing.T) {
	t.Parallel()
	err := RequiredParam("owner")
	if err.Code != CodeInvalidInput {
		t.Errorf("Code = %q, want %q", err.Code, CodeInvalidInput)
	}
	if !strings.Contains(err.Message, "owner") {
		t.Error("Message should contain parameter name")
	}
	if !strings.Contains(err.Message, "required") {
		t.Error("Message should contain 'required'")
	}
	details, ok := err.Details.(map[string]string)
	if !ok {
		t.Fatalf("Details type = %T, want map[string]string", err.Details)
	}
	if details["parameter"] != "owner" {
		t.Errorf("Details[parameter] = %q, want %q", details["parameter"], "owner")
	}
}

// ---------------------------------------------------------------------------
// InvalidParam
// ---------------------------------------------------------------------------

func TestInvalidParam(t *testing.T) {
	t.Parallel()
	err := InvalidParam("count", "must be positive")
	if err.Code != CodeInvalidInput {
		t.Errorf("Code = %q, want %q", err.Code, CodeInvalidInput)
	}
	if !strings.Contains(err.Message, "count") {
		t.Error("Message should contain parameter name")
	}
	if !strings.Contains(err.Message, "must be positive") {
		t.Error("Message should contain reason")
	}
	details, ok := err.Details.(map[string]string)
	if !ok {
		t.Fatalf("Details type = %T, want map[string]string", err.Details)
	}
	if details["parameter"] != "count" {
		t.Errorf("Details[parameter] = %q, want %q", details["parameter"], "count")
	}
	if details["reason"] != "must be positive" {
		t.Errorf("Details[reason] = %q, want %q", details["reason"], "must be positive")
	}
}

// ---------------------------------------------------------------------------
// APIError
// ---------------------------------------------------------------------------

func TestAPIError(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name       string
		service    string
		statusCode int
		body       string
		wantCode   string
		wantSubstr string
	}{
		{"401 unauthorized", "GitHub", 401, "bad token", CodeUnauthorized, "authentication failed"},
		{"403 forbidden", "GitHub", 403, "no access", CodeForbidden, "forbidden"},
		{"404 not found", "GitHub", 404, "missing", CodeNotFound, "not found"},
		{"429 rate limited", "GitHub", 429, "slow down", CodeRateLimited, "rate limit"},
		{"500 server error", "GitHub", 500, "boom", CodeServerError, "unavailable"},
		{"502 bad gateway", "GitHub", 502, "bad gw", CodeServerError, "unavailable"},
		{"503 unavailable", "GitHub", 503, "down", CodeServerError, "unavailable"},
		{"504 timeout", "GitHub", 504, "timeout", CodeServerError, "unavailable"},
		{"400 generic", "GitHub", 400, "bad request", CodeServerError, "http 400"},
		{"418 generic", "Jira", 418, "teapot", CodeServerError, "http 418"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			err := APIError(tt.service, tt.statusCode, tt.body)
			if err.Code != tt.wantCode {
				t.Errorf("Code = %q, want %q", err.Code, tt.wantCode)
			}
			if !strings.Contains(strings.ToLower(err.Message), tt.wantSubstr) {
				t.Errorf("Message = %q, want substring %q", err.Message, tt.wantSubstr)
			}
			// Verify details contain service and status_code.
			details, ok := err.Details.(map[string]any)
			if !ok {
				t.Fatalf("Details type = %T, want map[string]any", err.Details)
			}
			if details["service"] != tt.service {
				t.Errorf("Details[service] = %v, want %q", details["service"], tt.service)
			}
			if details["status_code"] != tt.statusCode {
				t.Errorf("Details[status_code] = %v, want %d", details["status_code"], tt.statusCode)
			}
		})
	}

	t.Run("body truncation", func(t *testing.T) {
		t.Parallel()
		longBody := strings.Repeat("x", 1000)
		err := APIError("svc", 500, longBody)
		details := err.Details.(map[string]any)
		body := details["body"].(string)
		if len(body) > 500 {
			t.Errorf("body len = %d, want <= 500", len(body))
		}
		if !strings.HasSuffix(body, "...") {
			t.Error("truncated body should end with '...'")
		}
	})
}

// ---------------------------------------------------------------------------
// IsNotFound
// ---------------------------------------------------------------------------

func TestIsNotFound(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		err  error
		want bool
	}{
		{"not_found error", NotFound("user", "1"), true},
		{"server error", ServerError("boom"), false},
		{"nil error", nil, false},
		{"plain Go error", errors.New("not found"), false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if got := IsNotFound(tt.err); got != tt.want {
				t.Errorf("IsNotFound() = %v, want %v", got, tt.want)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// IsServerError
// ---------------------------------------------------------------------------

func TestIsServerError(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		err  error
		want bool
	}{
		{"server error", ServerError("boom"), true},
		{"not_found error", NotFound("x", "y"), false},
		{"nil error", nil, false},
		{"plain Go error", errors.New("server error"), false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if got := IsServerError(tt.err); got != tt.want {
				t.Errorf("IsServerError() = %v, want %v", got, tt.want)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// WrapAPI
// ---------------------------------------------------------------------------

func TestWrapAPI(t *testing.T) {
	t.Parallel()
	t.Run("wraps error", func(t *testing.T) {
		t.Parallel()
		err := WrapAPI("GitHub", errors.New("timeout"))
		if err.Code != CodeConnectionError {
			t.Errorf("Code = %q, want %q", err.Code, CodeConnectionError)
		}
		if !strings.Contains(err.Message, "GitHub") {
			t.Error("Message should contain service name")
		}
		if !strings.Contains(err.Message, "timeout") {
			t.Error("Message should contain error text")
		}
		details, ok := err.Details.(map[string]string)
		if !ok {
			t.Fatalf("Details type = %T, want map[string]string", err.Details)
		}
		if details["service"] != "GitHub" {
			t.Errorf("Details[service] = %q, want %q", details["service"], "GitHub")
		}
	})
	t.Run("nil returns nil", func(t *testing.T) {
		t.Parallel()
		if got := WrapAPI("GitHub", nil); got != nil {
			t.Errorf("WrapAPI(nil) = %v, want nil", got)
		}
	})
}

// ---------------------------------------------------------------------------
// ServiceUnavailable
// ---------------------------------------------------------------------------

func TestServiceUnavailable(t *testing.T) {
	t.Parallel()
	err := ServiceUnavailable("Qdrant", "connection refused")
	if err.Code != CodeServerError {
		t.Errorf("Code = %q, want %q", err.Code, CodeServerError)
	}
	if !strings.Contains(err.Message, "Qdrant") {
		t.Error("Message should contain service name")
	}
	if !strings.Contains(err.Message, "unavailable") {
		t.Error("Message should contain 'unavailable'")
	}
	if !strings.Contains(err.Message, "connection refused") {
		t.Error("Message should contain reason")
	}
	details, ok := err.Details.(map[string]string)
	if !ok {
		t.Fatalf("Details type = %T, want map[string]string", err.Details)
	}
	if details["service"] != "Qdrant" {
		t.Errorf("Details[service] = %q, want %q", details["service"], "Qdrant")
	}
}

// ---------------------------------------------------------------------------
// NotConfigured
// ---------------------------------------------------------------------------

func TestNotConfigured(t *testing.T) {
	t.Parallel()
	t.Run("with hint", func(t *testing.T) {
		t.Parallel()
		err := NotConfigured("GITHUB_TOKEN", "set via environment variable")
		if err.Code != CodeServerError {
			t.Errorf("Code = %q, want %q", err.Code, CodeServerError)
		}
		if !strings.Contains(err.Message, "GITHUB_TOKEN") {
			t.Error("Message should contain config item")
		}
		if !strings.Contains(err.Message, "set via environment variable") {
			t.Error("Message should contain hint")
		}
		details, ok := err.Details.(map[string]string)
		if !ok {
			t.Fatalf("Details type = %T, want map[string]string", err.Details)
		}
		if details["config"] != "GITHUB_TOKEN" {
			t.Errorf("Details[config] = %q, want %q", details["config"], "GITHUB_TOKEN")
		}
	})
	t.Run("empty hint", func(t *testing.T) {
		t.Parallel()
		err := NotConfigured("API_KEY", "")
		if strings.HasSuffix(err.Message, ": ") {
			t.Error("Message should not have trailing ': ' when hint is empty")
		}
		want := "API_KEY is not configured"
		if err.Message != want {
			t.Errorf("Message = %q, want %q", err.Message, want)
		}
	})
}

// ---------------------------------------------------------------------------
// OperationFailed
// ---------------------------------------------------------------------------

func TestOperationFailed(t *testing.T) {
	t.Parallel()
	t.Run("wraps error", func(t *testing.T) {
		t.Parallel()
		err := OperationFailed("database query", errors.New("connection lost"))
		if err.Code != CodeServerError {
			t.Errorf("Code = %q, want %q", err.Code, CodeServerError)
		}
		if !strings.Contains(err.Message, "database query") {
			t.Error("Message should contain operation")
		}
		if !strings.Contains(err.Message, "failed") {
			t.Error("Message should contain 'failed'")
		}
		if !strings.Contains(err.Message, "connection lost") {
			t.Error("Message should contain error text")
		}
	})
	t.Run("nil returns nil", func(t *testing.T) {
		t.Parallel()
		if got := OperationFailed("op", nil); got != nil {
			t.Errorf("OperationFailed(nil) = %v, want nil", got)
		}
	})
}

// ---------------------------------------------------------------------------
// ParseError
// ---------------------------------------------------------------------------

func TestParseError(t *testing.T) {
	t.Parallel()
	err := ParseError("JSON response", fmt.Errorf("unexpected token"))
	if err.Code != CodeInvalidInput {
		t.Errorf("Code = %q, want %q", err.Code, CodeInvalidInput)
	}
	if !strings.Contains(err.Message, "JSON response") {
		t.Error("Message should contain what was being parsed")
	}
	if !strings.Contains(err.Message, "unexpected token") {
		t.Error("Message should contain parse error")
	}
	if !strings.Contains(err.Message, "failed to parse") {
		t.Error("Message should contain 'failed to parse'")
	}
}

// ---------------------------------------------------------------------------
// truncateString (unexported)
// ---------------------------------------------------------------------------

func TestTruncateString(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name   string
		input  string
		maxLen int
		want   string
	}{
		{"short unchanged", "hello", 10, "hello"},
		{"exact length unchanged", "hello", 5, "hello"},
		{"empty string", "", 10, ""},
		{"truncated with ellipsis", "hello world", 8, "hello..."},
		{"truncated to 5", "abcdefghij", 5, "ab..."},
		{"maxLen 3 no ellipsis", "abcdef", 3, "abc"},
		{"maxLen 2 no ellipsis", "abcdef", 2, "ab"},
		{"maxLen 1 no ellipsis", "abcdef", 1, "a"},
		{"maxLen 4 with ellipsis", "abcdef", 4, "a..."},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := truncateString(tt.input, tt.maxLen)
			if got != tt.want {
				t.Errorf("truncateString(%q, %d) = %q, want %q",
					tt.input, tt.maxLen, got, tt.want)
			}
			if len(got) > tt.maxLen {
				t.Errorf("result len = %d, exceeds maxLen %d", len(got), tt.maxLen)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// Error implements error interface (compile-time check)
// ---------------------------------------------------------------------------

var _ error = (*Error)(nil)
