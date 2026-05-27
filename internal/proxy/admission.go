package proxy

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"

	"github.com/flexinfer/flexinfer/pkg/modelmeta"
	"github.com/flexinfer/flexinfer/pkg/validation"
)

// admissionAnnotation gates the context-bounded admission filter per Model.
// Default behavior when the annotation is unset is "do not enforce" so the
// feature is opt-in. See docs/planning/context-bounded-admission-spec.md.
const admissionAnnotation = "flexinfer.ai/admission"

// admissionAnnotationValue is the only currently recognised value.
const admissionAnnotationValue = "context-bounded"

// admissionRejectCode is the OpenAI-style error code returned with the 413
// response when a request fails admission.
const admissionRejectCode = "context_window_exceeded"

// defaultAdmissionMaxTokens is the assumed generation budget when a request
// omits max_tokens. Chat completions clients commonly omit it; choosing a
// modest default keeps the admission decision conservative without
// pessimistically rejecting short prompts.
const defaultAdmissionMaxTokens = 256

// admissionDecision is the result of evaluating one request against one
// lane's context ceiling. It is returned by checkAdmission so callers can
// decide how to respond (refuse, log, increment metrics).
type admissionDecision struct {
	// Enforced is true when admission was checked at all. False means the
	// filter was disabled or insufficient signal was available; the caller
	// MUST forward the request.
	Enforced bool

	// Allow is true when the request fits the ceiling. Only meaningful when
	// Enforced is true.
	Allow bool

	// Reason is a short, machine-stable string suitable for logs and
	// metrics. Examples: "feature_disabled", "no_ceiling", "estimator_skipped",
	// "in_budget", "over_budget".
	Reason string

	// EstimatedPromptTokens is the estimator's output. Zero when the
	// estimator was not run.
	EstimatedPromptTokens int

	// MaxTokens is the requested or defaulted completion budget.
	MaxTokens int

	// Ceiling is the per-lane budget the request was compared against (after
	// applying the safety margin). Zero when no enforcement happened.
	Ceiling int

	// RawContextWindow is the lane's declared context window before the
	// safety margin was applied. Useful for the operator-facing error
	// message.
	RawContextWindow int
}

// admissionFilter holds the configuration for the context-bounded admission
// filter. It is a plain value because the per-Model lookup (annotation +
// TokenLimits) is done at request time against the proxy's informer cache.
type admissionFilter struct {
	// Enabled is the global feature flag. When false, the filter is a
	// no-op and the existing forward-everything behavior is preserved.
	Enabled bool

	// SafetyMarginPercent is applied to the declared context window so the
	// effective ceiling is `window × (1 - margin/100)`. A value of 5 means
	// admission refuses when estimated + max_tokens > 0.95 × window.
	SafetyMarginPercent int

	// DefaultMaxTokens is the assumed completion budget when the request
	// omits max_tokens. Defaults to defaultAdmissionMaxTokens.
	DefaultMaxTokens int
}

// shouldEnforce reports whether this Model has opted in to the admission
// filter via the `flexinfer.ai/admission: context-bounded` annotation.
func (f *admissionFilter) shouldEnforce(annotations map[string]string) bool {
	if !f.Enabled {
		return false
	}
	if v, ok := annotations[admissionAnnotation]; ok {
		return v == admissionAnnotationValue
	}
	return false
}

// effectiveCeiling returns the ceiling to compare against, applying the
// safety margin. Returns 0 when no ceiling is known.
func (f *admissionFilter) effectiveCeiling(window int) int {
	if window <= 0 {
		return 0
	}
	margin := f.SafetyMarginPercent
	if margin < 0 {
		margin = 0
	}
	if margin > 50 {
		margin = 50
	}
	// effective = window × (100 - margin) / 100
	return window * (100 - margin) / 100
}

// checkAdmission applies the filter to one request. Returns an
// admissionDecision the caller can use to refuse, log, or forward.
//
// This is a PURE function relative to the inputs; callers should pass in the
// already-fetched Model annotations and TokenLimits so this function is
// trivially testable without a k8s client.
func (f *admissionFilter) checkAdmission(
	modelAnnotations map[string]string,
	limits modelmeta.TokenLimits,
	body []byte,
) admissionDecision {
	if f == nil || !f.shouldEnforce(modelAnnotations) {
		return admissionDecision{Enforced: false, Reason: "feature_disabled"}
	}
	if limits.ContextWindow <= 0 {
		return admissionDecision{Enforced: false, Reason: "no_ceiling"}
	}

	defaultMax := f.DefaultMaxTokens
	if defaultMax <= 0 {
		defaultMax = defaultAdmissionMaxTokens
	}

	maxTokens, _ := extractMaxTokensFromBody(body, defaultMax)
	estimated, ok := estimatePromptTokensFromBody(body)
	if !ok {
		// Estimator declined (body too big, not JSON, no recognised fields).
		// Letting the runtime decide is the safe default.
		return admissionDecision{
			Enforced:  false,
			Reason:    "estimator_skipped",
			MaxTokens: maxTokens,
		}
	}

	ceiling := f.effectiveCeiling(limits.ContextWindow)
	allow := estimated+maxTokens <= ceiling
	reason := "in_budget"
	if !allow {
		reason = "over_budget"
	}
	return admissionDecision{
		Enforced:              true,
		Allow:                 allow,
		Reason:                reason,
		EstimatedPromptTokens: estimated,
		MaxTokens:             maxTokens,
		Ceiling:               ceiling,
		RawContextWindow:      limits.ContextWindow,
	}
}

// writeAdmissionRejection writes a 413 Payload Too Large response with a
// structured OpenAI-style error body naming the lane, ceiling, and estimated
// breach amount, plus a flexinfer `admission` extension carrying numeric
// fields a client can use to render an affordance (e.g. "trim by N tokens").
// See docs/planning/context-bounded-admission-spec.md (F4-413-as-feature).
func writeAdmissionRejection(w http.ResponseWriter, modelName string, d admissionDecision) {
	submitted := d.EstimatedPromptTokens + d.MaxTokens
	over := submitted - d.Ceiling
	if over < 0 {
		// Defensive: this helper is only called on a reject path where
		// submitted > ceiling, but clamp so the wire contract never lies.
		over = 0
	}
	truncateTo := d.Ceiling - d.MaxTokens
	if truncateTo < 0 {
		// max_tokens alone meets or exceeds the budget — the client must
		// shrink max_tokens, not the prompt. Zero communicates "no truncation
		// of the prompt will help."
		truncateTo = 0
	}
	msg := fmt.Sprintf(
		"prompt + max_tokens (%d + %d = %d) exceeds %q context budget %d (window %d, safety margin applied)",
		d.EstimatedPromptTokens, d.MaxTokens, submitted,
		modelName, d.Ceiling, d.RawContextWindow,
	)
	validation.WriteAdmissionError(w, msg, admissionRejectCode, validation.AdmissionDetails{
		Model:             modelName,
		TokensBudget:      d.Ceiling,
		TokensSubmitted:   submitted,
		TokensOver:        over,
		SuggestTruncateTo: truncateTo,
		ContextWindow:     d.RawContextWindow,
	})
}

// boolLabel returns the Prometheus-friendly label form of a bool ("true" or
// "false"). Used to label admission decisions by allow/deny.
func boolLabel(b bool) string {
	if b {
		return "true"
	}
	return "false"
}

// logAdmission emits a structured log line for the decision when enforcement
// happened. Allow decisions are logged at DEBUG so they don't drown logs in
// steady-state; reject decisions are logged at INFO so operators can spot
// admission storms.
func logAdmission(ctx context.Context, modelName string, d admissionDecision) {
	if !d.Enforced {
		return
	}
	level := slog.LevelDebug
	if !d.Allow {
		level = slog.LevelInfo
	}
	slog.Log(ctx, level, "admission_decision",
		"model", modelName,
		"allow", d.Allow,
		"reason", d.Reason,
		"estimated_prompt_tokens", d.EstimatedPromptTokens,
		"max_tokens", d.MaxTokens,
		"ceiling", d.Ceiling,
		"context_window", d.RawContextWindow,
	)
}
