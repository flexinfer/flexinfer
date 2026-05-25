package proxy

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/flexinfer/flexinfer/pkg/modelmeta"
)

func TestAdmissionFilter_DisabledNoEnforcement(t *testing.T) {
	f := &admissionFilter{Enabled: false, SafetyMarginPercent: 5}
	d := f.checkAdmission(
		map[string]string{admissionAnnotation: admissionAnnotationValue},
		modelmeta.TokenLimits{ContextWindow: 4096},
		[]byte(`{"messages":[{"role":"user","content":"hi"}],"max_tokens":64}`),
	)
	if d.Enforced {
		t.Fatalf("expected no enforcement when filter disabled")
	}
}

func TestAdmissionFilter_NoAnnotationNoEnforcement(t *testing.T) {
	f := &admissionFilter{Enabled: true, SafetyMarginPercent: 5}
	d := f.checkAdmission(
		nil, // no annotations
		modelmeta.TokenLimits{ContextWindow: 4096},
		[]byte(`{"messages":[{"role":"user","content":"hi"}]}`),
	)
	if d.Enforced {
		t.Fatalf("expected no enforcement when annotation absent")
	}
}

func TestAdmissionFilter_WrongAnnotationValue(t *testing.T) {
	f := &admissionFilter{Enabled: true, SafetyMarginPercent: 5}
	d := f.checkAdmission(
		map[string]string{admissionAnnotation: "something-else"},
		modelmeta.TokenLimits{ContextWindow: 4096},
		[]byte(`{"messages":[{"role":"user","content":"hi"}]}`),
	)
	if d.Enforced {
		t.Fatalf("expected no enforcement for unknown annotation value")
	}
}

func TestAdmissionFilter_NoCeilingNoEnforcement(t *testing.T) {
	f := &admissionFilter{Enabled: true, SafetyMarginPercent: 5}
	d := f.checkAdmission(
		map[string]string{admissionAnnotation: admissionAnnotationValue},
		modelmeta.TokenLimits{ContextWindow: 0}, // unknown
		[]byte(`{"messages":[{"role":"user","content":"hi"}]}`),
	)
	if d.Enforced {
		t.Fatalf("expected no enforcement when ceiling is 0")
	}
	if d.Reason != "no_ceiling" {
		t.Errorf("reason = %q, want no_ceiling", d.Reason)
	}
}

func TestAdmissionFilter_InBudgetAllows(t *testing.T) {
	f := &admissionFilter{Enabled: true, SafetyMarginPercent: 5}
	body := []byte(`{
		"messages": [{"role":"user","content":"hi"}],
		"max_tokens": 64
	}`)
	d := f.checkAdmission(
		map[string]string{admissionAnnotation: admissionAnnotationValue},
		modelmeta.TokenLimits{ContextWindow: 4096},
		body,
	)
	if !d.Enforced || !d.Allow {
		t.Fatalf("expected enforced+allow; got %+v", d)
	}
	if d.Reason != "in_budget" {
		t.Errorf("reason = %q, want in_budget", d.Reason)
	}
}

func TestAdmissionFilter_OverBudgetRejects(t *testing.T) {
	f := &admissionFilter{Enabled: true, SafetyMarginPercent: 5}
	// 2048 × "hello world " ≈ 24KB, estimator should report ~7k tokens.
	big := strings.Repeat("hello world ", 2048)
	body := []byte(`{"messages":[{"role":"user","content":"` + big + `"}],"max_tokens":2048}`)
	d := f.checkAdmission(
		map[string]string{admissionAnnotation: admissionAnnotationValue},
		modelmeta.TokenLimits{ContextWindow: 4096},
		body,
	)
	if !d.Enforced || d.Allow {
		t.Fatalf("expected enforced+deny on over-budget; got %+v", d)
	}
	if d.Reason != "over_budget" {
		t.Errorf("reason = %q, want over_budget", d.Reason)
	}
	if d.EstimatedPromptTokens == 0 {
		t.Errorf("expected non-zero estimated prompt tokens, got %d", d.EstimatedPromptTokens)
	}
	if d.Ceiling >= 4096 {
		t.Errorf("expected ceiling < window after safety margin, got ceiling=%d window=%d",
			d.Ceiling, d.RawContextWindow)
	}
}

func TestAdmissionFilter_DefaultMaxTokensApplied(t *testing.T) {
	f := &admissionFilter{
		Enabled:             true,
		SafetyMarginPercent: 5,
		DefaultMaxTokens:    1000, // unusually large default
	}
	// Body omits max_tokens. The default of 1000 should be added to the
	// estimated prompt tokens. With a 4096-token window and ~50 prompt
	// tokens, request fits (50 + 1000 = 1050 < 3891 = ceiling).
	body := []byte(`{"messages":[{"role":"user","content":"hello world test prompt"}]}`)
	d := f.checkAdmission(
		map[string]string{admissionAnnotation: admissionAnnotationValue},
		modelmeta.TokenLimits{ContextWindow: 4096},
		body,
	)
	if !d.Enforced {
		t.Fatalf("expected enforced; got %+v", d)
	}
	if d.MaxTokens != 1000 {
		t.Errorf("expected default max_tokens=1000; got %d", d.MaxTokens)
	}
}

func TestAdmissionFilter_BodyTooBigSkipped(t *testing.T) {
	f := &admissionFilter{Enabled: true, SafetyMarginPercent: 5}
	// 300 KB body exceeds admissionMaxBodyBytes (256 KB). The estimator
	// returns ok=false and the filter should NOT enforce — forwarding to
	// the runtime is the safe default for un-parseable bodies.
	body := []byte(`{"messages":[{"role":"user","content":"` + strings.Repeat("x", 300*1024) + `"}]}`)
	d := f.checkAdmission(
		map[string]string{admissionAnnotation: admissionAnnotationValue},
		modelmeta.TokenLimits{ContextWindow: 4096},
		body,
	)
	if d.Enforced {
		t.Fatalf("expected estimator skip for oversized body, got enforced=%v", d.Enforced)
	}
	if d.Reason != "estimator_skipped" {
		t.Errorf("reason = %q, want estimator_skipped", d.Reason)
	}
}

func TestEffectiveCeiling(t *testing.T) {
	cases := []struct {
		window, margin, want int
	}{
		{4096, 0, 4096},
		{4096, 5, 3891},
		{4096, 50, 2048},
		{4096, 100, 2048}, // clamp at 50%
		{4096, -10, 4096}, // negative clamps to 0
		{0, 5, 0},
	}
	for _, tc := range cases {
		f := &admissionFilter{SafetyMarginPercent: tc.margin}
		got := f.effectiveCeiling(tc.window)
		if got != tc.want {
			t.Errorf("effectiveCeiling(window=%d, margin=%d) = %d, want %d",
				tc.window, tc.margin, got, tc.want)
		}
	}
}

func TestWriteAdmissionRejection_413WithCode(t *testing.T) {
	w := httptest.NewRecorder()
	d := admissionDecision{
		Enforced:              true,
		Allow:                 false,
		Reason:                "over_budget",
		EstimatedPromptTokens: 5000,
		MaxTokens:             1000,
		Ceiling:               4000,
		RawContextWindow:      4096,
	}
	writeAdmissionRejection(w, "gemma4-26b-a4b-gptq", d)
	if w.Code != http.StatusRequestEntityTooLarge {
		t.Errorf("status = %d, want 413", w.Code)
	}
	body := w.Body.String()
	if !strings.Contains(body, "context_window_exceeded") {
		t.Errorf("body missing error code: %q", body)
	}
	if !strings.Contains(body, "gemma4-26b-a4b-gptq") {
		t.Errorf("body missing model name: %q", body)
	}
	if !strings.Contains(body, "5000") {
		t.Errorf("body missing estimated tokens: %q", body)
	}
}

// Discard log output so tests don't pollute terminal.
func TestLogAdmission_DoesNotPanic(t *testing.T) {
	d := admissionDecision{Enforced: true, Allow: false, Reason: "over_budget"}
	logAdmission(context.Background(), "test", d)
	// Sanity: skipped decisions are no-ops.
	logAdmission(context.Background(), "test", admissionDecision{Enforced: false})
}
