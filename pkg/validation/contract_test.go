package validation

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

// These tests pin the OpenAI-compatibility error contract to byte-exact
// bodies, status codes, and headers. They exist to guard the
// refactor/validation-dedup change: the convenience error writers and the
// three endpoint validators were deduplicated, and these assertions prove no
// observable behavior drifted. If you intentionally change an error body,
// update the golden string here AND confirm downstream clients tolerate it.

// errWriterCase exercises one convenience writer and pins its full output.
type errWriterCase struct {
	name        string
	call        func(w http.ResponseWriter)
	wantStatus  int
	wantBody    string
	wantHeaders map[string]string
}

func TestErrorWriters_Contract(t *testing.T) {
	cases := []errWriterCase{
		{
			name:       "WriteBadRequest",
			call:       func(w http.ResponseWriter) { WriteBadRequest(w, "bad input") },
			wantStatus: http.StatusBadRequest,
			wantBody:   `{"error":{"message":"bad input","type":"invalid_request_error","code":"invalid_request_error"}}` + "\n",
		},
		{
			name:       "WriteBadRequestWithCode",
			call:       func(w http.ResponseWriter) { WriteBadRequestWithCode(w, "bad input", "custom_code") },
			wantStatus: http.StatusBadRequest,
			wantBody:   `{"error":{"message":"bad input","type":"invalid_request_error","code":"custom_code"}}` + "\n",
		},
		{
			name:       "WriteMissingField",
			call:       func(w http.ResponseWriter) { WriteMissingField(w, "model") },
			wantStatus: http.StatusBadRequest,
			wantBody:   `{"error":{"message":"Missing required field: model","type":"invalid_request_error","param":"model","code":"missing_required_field"}}` + "\n",
		},
		{
			name:       "WriteInvalidFieldValue",
			call:       func(w http.ResponseWriter) { WriteInvalidFieldValue(w, "temperature", "must be between 0 and 2") },
			wantStatus: http.StatusBadRequest,
			wantBody:   `{"error":{"message":"Invalid value for field 'temperature': must be between 0 and 2","type":"invalid_request_error","param":"temperature","code":"invalid_field_value"}}` + "\n",
		},
		{
			name:       "WriteNotFound",
			call:       func(w http.ResponseWriter) { WriteNotFound(w, "nope") },
			wantStatus: http.StatusNotFound,
			wantBody:   `{"error":{"message":"nope","type":"not_found_error","code":"model_not_found"}}` + "\n",
		},
		{
			name:       "WriteModelNotFound",
			call:       func(w http.ResponseWriter) { WriteModelNotFound(w, "gpt-99") },
			wantStatus: http.StatusNotFound,
			wantBody:   `{"error":{"message":"Model 'gpt-99' not found","type":"not_found_error","param":"model","code":"model_not_found"}}` + "\n",
		},
		{
			name:       "WriteMethodNotAllowed",
			call:       func(w http.ResponseWriter) { WriteMethodNotAllowed(w, "DELETE") },
			wantStatus: http.StatusMethodNotAllowed,
			wantBody:   `{"error":{"message":"Method DELETE not allowed","type":"invalid_request_error","code":"method_not_allowed"}}` + "\n",
		},
		{
			name:       "WriteInternalError",
			call:       func(w http.ResponseWriter) { WriteInternalError(w, "boom") },
			wantStatus: http.StatusInternalServerError,
			wantBody:   `{"error":{"message":"boom","type":"server_error","code":"server_error"}}` + "\n",
		},
		{
			name:       "WriteServiceUnavailable",
			call:       func(w http.ResponseWriter) { WriteServiceUnavailable(w, "down") },
			wantStatus: http.StatusServiceUnavailable,
			wantBody:   `{"error":{"message":"down","type":"service_unavailable_error","code":"service_unavailable"}}` + "\n",
		},
		{
			name:       "WriteQueueFull",
			call:       func(w http.ResponseWriter) { WriteQueueFull(w) },
			wantStatus: http.StatusServiceUnavailable,
			wantBody:   `{"error":{"message":"Service overloaded, please retry","type":"service_unavailable_error","code":"queue_full"}}` + "\n",
		},
		{
			name:       "WriteActivationFailed",
			call:       func(w http.ResponseWriter) { WriteActivationFailed(w, "could not activate") },
			wantStatus: http.StatusServiceUnavailable,
			wantBody:   `{"error":{"message":"could not activate","type":"service_unavailable_error","code":"activation_failed"}}` + "\n",
		},
		{
			name:       "WriteTimeout",
			call:       func(w http.ResponseWriter) { WriteTimeout(w, "too slow") },
			wantStatus: http.StatusGatewayTimeout,
			wantBody:   `{"error":{"message":"too slow","type":"timeout_error","code":"timeout"}}` + "\n",
		},
		{
			name:       "WriteColdStartTimeout",
			call:       func(w http.ResponseWriter) { WriteColdStartTimeout(w, "30s") },
			wantStatus: http.StatusGatewayTimeout,
			wantBody:   `{"error":{"message":"Timeout waiting for model to become ready (waited 30s)","type":"timeout_error","code":"timeout"}}` + "\n",
		},
		{
			name:       "WriteGPUGroupTimeout",
			call:       func(w http.ResponseWriter) { WriteGPUGroupTimeout(w, "45s") },
			wantStatus: http.StatusGatewayTimeout,
			wantBody:   `{"error":{"message":"Timeout waiting for model to become active (waited 45s)","type":"timeout_error","code":"timeout"}}` + "\n",
		},
		{
			name:        "WriteRateLimited",
			call:        func(w http.ResponseWriter) { WriteRateLimited(w, 12) },
			wantStatus:  http.StatusTooManyRequests,
			wantBody:    `{"error":{"message":"Rate limit exceeded, please retry later","type":"rate_limit_error","code":"rate_limit_exceeded"}}` + "\n",
			wantHeaders: map[string]string{"Retry-After": "12"},
		},
		{
			name:        "WriteStalledLoad_withRetryAfter",
			call:        func(w http.ResponseWriter) { WriteStalledLoad(w, "stalled", 7) },
			wantStatus:  http.StatusServiceUnavailable,
			wantBody:    `{"error":{"message":"stalled","type":"service_unavailable_error","code":"activation_failed"}}` + "\n",
			wantHeaders: map[string]string{"Retry-After": "7"},
		},
		{
			name:        "WriteStalledLoad_noRetryAfter",
			call:        func(w http.ResponseWriter) { WriteStalledLoad(w, "stalled", 0) },
			wantStatus:  http.StatusServiceUnavailable,
			wantBody:    `{"error":{"message":"stalled","type":"service_unavailable_error","code":"activation_failed"}}` + "\n",
			wantHeaders: map[string]string{"Retry-After": ""},
		},
		{
			name:       "WriteUnauthorized",
			call:       func(w http.ResponseWriter) { WriteUnauthorized(w, "no key") },
			wantStatus: http.StatusUnauthorized,
			wantBody:   `{"error":{"message":"no key","type":"invalid_request_error","code":"invalid_api_key"}}` + "\n",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			w := httptest.NewRecorder()
			tc.call(w)

			if w.Code != tc.wantStatus {
				t.Errorf("status = %d, want %d", w.Code, tc.wantStatus)
			}
			if got := w.Body.String(); got != tc.wantBody {
				t.Errorf("body mismatch\n got: %q\nwant: %q", got, tc.wantBody)
			}
			if got := w.Header().Get("Content-Type"); got != "application/json" {
				t.Errorf("content-type = %q, want application/json", got)
			}
			for k, want := range tc.wantHeaders {
				if got := w.Header().Get(k); got != want {
					t.Errorf("header %q = %q, want %q", k, got, want)
				}
			}
		})
	}
}

// TestValidateThenWrite_Contract pins the full validate→WriteValidationErrors
// path: a representative bad request for each endpoint must produce a
// byte-identical 400 body. This is the contract clients depend on.
func TestValidateThenWrite_Contract(t *testing.T) {
	cases := []struct {
		name     string
		path     string
		body     string
		wantBody string
	}{
		{
			name:     "chat_missing_model",
			path:     "/v1/chat/completions",
			body:     `{"messages":[{"role":"user","content":"hi"}]}`,
			wantBody: `{"error":{"message":"model is required","type":"invalid_request_error","param":"model","code":"validation_error"}}` + "\n",
		},
		{
			name:     "chat_invalid_temperature",
			path:     "/v1/chat/completions",
			body:     `{"model":"m","messages":[{"role":"user","content":"hi"}],"temperature":5}`,
			wantBody: `{"error":{"message":"temperature must be between 0 and 2","type":"invalid_request_error","param":"temperature","code":"validation_error"}}` + "\n",
		},
		{
			name:     "chat_invalid_role",
			path:     "/v1/chat/completions",
			body:     `{"model":"m","messages":[{"role":"bogus","content":"hi"}]}`,
			wantBody: `{"error":{"message":"role must be one of: system, user, assistant, tool, function (got 'bogus')","type":"invalid_request_error","param":"messages[0].role","code":"validation_error"}}` + "\n",
		},
		{
			name:     "chat_empty_body",
			path:     "/v1/chat/completions",
			body:     ``,
			wantBody: `{"error":{"message":"Request body is empty","type":"invalid_request_error","code":"validation_error"}}` + "\n",
		},
		{
			name:     "chat_multi_error",
			path:     "/v1/chat/completions",
			body:     `{"top_p":9}`,
			wantBody: `{"error":{"message":"model is required (and 2 more errors)","type":"invalid_request_error","param":"model","code":"validation_error"}}` + "\n",
		},
		{
			name:     "completion_missing_model",
			path:     "/v1/completions",
			body:     `{"prompt":"hi"}`,
			wantBody: `{"error":{"message":"model is required","type":"invalid_request_error","param":"model","code":"validation_error"}}` + "\n",
		},
		{
			name:     "completion_invalid_best_of",
			path:     "/v1/completions",
			body:     `{"model":"m","prompt":"hi","best_of":0}`,
			wantBody: `{"error":{"message":"best_of must be at least 1","type":"invalid_request_error","param":"best_of","code":"validation_error"}}` + "\n",
		},
		{
			name:     "completion_invalid_logprobs",
			path:     "/v1/completions",
			body:     `{"model":"m","prompt":"hi","logprobs":-1}`,
			wantBody: `{"error":{"message":"logprobs must be non-negative","type":"invalid_request_error","param":"logprobs","code":"validation_error"}}` + "\n",
		},
		{
			name:     "embedding_missing_input",
			path:     "/v1/embeddings",
			body:     `{"model":"m"}`,
			wantBody: `{"error":{"message":"input is required","type":"invalid_request_error","param":"input","code":"validation_error"}}` + "\n",
		},
		{
			name:     "embedding_invalid_encoding",
			path:     "/v1/embeddings",
			body:     `{"model":"m","input":"x","encoding_format":"bogus"}`,
			wantBody: `{"error":{"message":"encoding_format must be 'float' or 'base64'","type":"invalid_request_error","param":"encoding_format","code":"validation_error"}}` + "\n",
		},
		{
			name:     "embedding_invalid_dimensions",
			path:     "/v1/embeddings",
			body:     `{"model":"m","input":"x","dimensions":0}`,
			wantBody: `{"error":{"message":"dimensions must be at least 1","type":"invalid_request_error","param":"dimensions","code":"validation_error"}}` + "\n",
		},
		{
			name:     "invalid_json",
			path:     "/v1/chat/completions",
			body:     `{nope}`,
			wantBody: `{"error":{"message":"Invalid JSON: invalid character 'n' looking for beginning of object key string","type":"invalid_request_error","code":"validation_error"}}` + "\n",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			result := ValidateRequest(tc.path, []byte(tc.body))
			if result == nil {
				t.Fatalf("ValidateRequest returned nil for path %q", tc.path)
			}
			w := httptest.NewRecorder()
			WriteValidationErrors(w, result)

			if w.Code != http.StatusBadRequest {
				t.Errorf("status = %d, want 400", w.Code)
			}
			if got := w.Body.String(); got != tc.wantBody {
				t.Errorf("body mismatch\n got: %q\nwant: %q", got, tc.wantBody)
			}
		})
	}
}
