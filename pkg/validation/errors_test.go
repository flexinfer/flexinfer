package validation

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestWriteAdmissionError_StatusAndBody(t *testing.T) {
	w := httptest.NewRecorder()
	details := AdmissionDetails{
		Model:             "gemma4-26b-a4b-gptq",
		TokensBudget:      30000,
		TokensSubmitted:   32500,
		TokensOver:        2500,
		SuggestTruncateTo: 29744,
		ContextWindow:     32768,
	}
	WriteAdmissionError(w, "over budget by 2500 tokens", "context_window_exceeded", details)

	if w.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("status = %d, want 413", w.Code)
	}
	if got := w.Header().Get("Content-Type"); got != "application/json" {
		t.Errorf("content-type = %q, want application/json", got)
	}

	var resp OpenAIErrorResponse
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal: %v; body=%q", err, w.Body.String())
	}
	if resp.Error.Code != "context_window_exceeded" {
		t.Errorf("error.code = %q, want context_window_exceeded", resp.Error.Code)
	}
	if resp.Error.Type != ErrorTypeInvalidRequest {
		t.Errorf("error.type = %q, want %q", resp.Error.Type, ErrorTypeInvalidRequest)
	}
	if resp.Admission == nil {
		t.Fatalf("admission extension missing")
	}
	if *resp.Admission != details {
		t.Errorf("admission = %+v, want %+v", *resp.Admission, details)
	}
}

// TestWriteError_NoAdmissionField confirms backwards compatibility: a plain
// WriteError must NOT emit the `admission` key, so existing clients that
// parse `error` alone see no shape change.
func TestWriteError_NoAdmissionField(t *testing.T) {
	w := httptest.NewRecorder()
	WriteError(w, http.StatusBadRequest, "bad", ErrorTypeInvalidRequest, CodeInvalidRequestError)

	var raw map[string]json.RawMessage
	if err := json.Unmarshal(w.Body.Bytes(), &raw); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if _, ok := raw["admission"]; ok {
		t.Errorf("plain error response should not include admission field; body=%q", w.Body.String())
	}
}
