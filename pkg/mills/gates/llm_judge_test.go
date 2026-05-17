package gates

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/crb2nu/loom/pkg/mills/store"
)

func TestSpecConformanceGate_PassAtOrAboveThreshold(t *testing.T) {
	g := NewSpecConformanceGate(&FakeRubricJudge{Default: RubricVerdict{Score: 0.85, Model: "qwen-3-8b"}})
	out, err := g.Evaluate(context.Background(), StageInput{})
	if err != nil {
		t.Fatalf("evaluate: %v", err)
	}
	if !out.Pass {
		t.Errorf("expected pass at score 0.85 (threshold 0.8)")
	}
	if !strings.HasPrefix(out.JudgedBy, "flexinfer:") {
		t.Errorf("JudgedBy = %q, want flexinfer:* prefix", out.JudgedBy)
	}
	if !strings.Contains(out.JudgedBy, "qwen") {
		t.Errorf("model id should be in JudgedBy: %q", out.JudgedBy)
	}
}

func TestSpecConformanceGate_FailBelowThreshold(t *testing.T) {
	g := NewSpecConformanceGate(&FakeRubricJudge{Default: RubricVerdict{
		Score: 0.5, Reasons: []string{"missing the api docs update"}, Model: "qwen-3-8b",
	}})
	out, err := g.Evaluate(context.Background(), StageInput{})
	if err != nil {
		t.Fatalf("evaluate: %v", err)
	}
	if out.Pass {
		t.Errorf("expected fail at score 0.5")
	}
	if len(out.Reasons) < 2 {
		t.Errorf("expected score line + judge reasons, got %v", out.Reasons)
	}
	if !strings.Contains(out.Reasons[0], "score=") {
		t.Errorf("first reason should expose the score: %q", out.Reasons[0])
	}
	if !strings.Contains(strings.Join(out.Reasons, " "), "api docs") {
		t.Errorf("judge reasons not propagated: %v", out.Reasons)
	}
}

func TestPRSelfReviewGate_LowerThreshold(t *testing.T) {
	g := NewPRSelfReviewGate(&FakeRubricJudge{Default: RubricVerdict{Score: 0.72, Model: "qwen"}})
	out, err := g.Evaluate(context.Background(), StageInput{})
	if err != nil {
		t.Fatalf("evaluate: %v", err)
	}
	if !out.Pass {
		t.Errorf("expected pass at 0.72 (pr_self_review threshold 0.7)")
	}
}

func TestLLMGate_DisabledShortCircuits(t *testing.T) {
	g := NewSpecConformanceGate(&FakeRubricJudge{Err: errors.New("would fire if not disabled")})
	g.Disabled = true
	out, err := g.Evaluate(context.Background(), StageInput{})
	if err != nil {
		t.Fatalf("disabled should not surface judge errors: %v", err)
	}
	if !out.Pass || out.JudgedBy != "flexinfer:disabled" {
		t.Errorf("disabled outcome wrong: %+v", out)
	}
}

func TestLLMGate_NilJudgeErrors(t *testing.T) {
	g := &LLMGate{GateName: "x", RubricName: "x_v1"}
	if _, err := g.Evaluate(context.Background(), StageInput{}); err == nil {
		t.Error("expected error when judge is nil")
	}
}

func TestLLMGate_JudgeErrorBubbles(t *testing.T) {
	g := NewSpecConformanceGate(&FakeRubricJudge{Err: errors.New("flexinfer down")})
	if _, err := g.Evaluate(context.Background(), StageInput{}); err == nil {
		t.Error("expected error when judge fails")
	}
}

func TestRegisterLLMGates_AddsBothToRegistry(t *testing.T) {
	r := NewRegistry()
	RegisterLLMGates(r, &FakeRubricJudge{Default: RubricVerdict{Score: 0.9}})
	if _, err := r.Get("spec_conformance"); err != nil {
		t.Errorf("spec_conformance not registered: %v", err)
	}
	if _, err := r.Get("pr_self_review"); err != nil {
		t.Errorf("pr_self_review not registered: %v", err)
	}
}

func TestFakeRubricJudge_PerRubricOverride(t *testing.T) {
	f := &FakeRubricJudge{
		ByRubric: map[string]RubricVerdict{
			"spec_conformance_v1": {Score: 0.9},
			"pr_self_review_v1":   {Score: 0.4},
		},
	}
	specOK, _ := NewSpecConformanceGate(f).Evaluate(context.Background(), StageInput{})
	if !specOK.Pass {
		t.Errorf("spec_conformance should pass with score 0.9")
	}
	prOut, _ := NewPRSelfReviewGate(f).Evaluate(context.Background(), StageInput{})
	if prOut.Pass {
		t.Errorf("pr_self_review should fail with score 0.4")
	}
}

func TestRubricPromptsAreNonEmpty(t *testing.T) {
	if SpecConformanceRubric == "" {
		t.Error("spec conformance rubric prompt is empty")
	}
	if PRSelfReviewRubric == "" {
		t.Error("pr_self_review rubric prompt is empty")
	}
}

// --- M2.5: unparseable-judge soft-fail path ---

// fakeUnparseableJudge returns an error wrapping gates.ErrJudgeUnparseable.
// In production, the clients package returns its own ErrRubricUnparseable
// wrapper that satisfies the same duck-typed predicate; these in-package
// tests use the gates sentinel directly so they don't need a cross-package
// import.
type fakeUnparseableJudge struct{ wrapped error }

func (f *fakeUnparseableJudge) Judge(_ context.Context, _ string, _ StageInput) (RubricVerdict, error) {
	return RubricVerdict{}, f.wrapped
}

// fakeDuckUnparseableJudge returns a custom error type that exposes
// IsRubricUnparseable() bool but does NOT wrap ErrJudgeUnparseable. This
// mirrors the production cross-package contract where pkg/mills/clients'
// ErrRubricUnparseable wrapper is detected without an import cycle.
type duckUnparseableErr struct{ msg string }

func (e *duckUnparseableErr) Error() string             { return e.msg }
func (e *duckUnparseableErr) IsRubricUnparseable() bool { return true }

type fakeDuckUnparseableJudge struct{}

func (f *fakeDuckUnparseableJudge) Judge(_ context.Context, _ string, _ StageInput) (RubricVerdict, error) {
	return RubricVerdict{}, &duckUnparseableErr{msg: "free-text instead of JSON envelope"}
}

func TestLLMGate_UnparseableJudgeReturnsSoftFailNotError(t *testing.T) {
	wrapped := fmt.Errorf("rubric judge: parse: %w; raw=%q", ErrJudgeUnparseable, "please provide the diff…")
	g := NewSpecConformanceGate(&fakeUnparseableJudge{wrapped: wrapped})
	out, err := g.Evaluate(context.Background(), StageInput{})
	if err != nil {
		t.Fatalf("unparseable judge must NOT return err (got %v); runner needs err=nil to take retry path", err)
	}
	if out.Pass {
		t.Errorf("unparseable judge must produce Pass=false outcome, got %+v", out)
	}
	if out.JudgedBy != "flexinfer:unparseable" {
		t.Errorf("JudgedBy = %q, want %q", out.JudgedBy, "flexinfer:unparseable")
	}
	if len(out.Reasons) == 0 {
		t.Errorf("expected at least one reason explaining the parse miss, got %v", out.Reasons)
	}
}

func TestLLMGate_DuckTypedUnparseableErrorAlsoSoftFails(t *testing.T) {
	// Production wires through pkg/mills/clients.ErrRubricUnparseable
	// (custom type with IsRubricUnparseable() bool). Verify the gate
	// detects it without a sentinel-Is match.
	g := NewSpecConformanceGate(&fakeDuckUnparseableJudge{})
	out, err := g.Evaluate(context.Background(), StageInput{})
	if err != nil {
		t.Fatalf("duck-typed unparseable error must soft-fail, got err=%v", err)
	}
	if out.Pass || out.JudgedBy != "flexinfer:unparseable" {
		t.Errorf("outcome = %+v, want Pass=false + JudgedBy=flexinfer:unparseable", out)
	}
}

func TestLLMGate_TransportErrorStillEscalates(t *testing.T) {
	// 5xx, timeout, network errors are infrastructure failures and must
	// still surface as err (runner escalates the run).
	transportErr := errors.New("flexinfer chat: status 500: model overloaded")
	g := NewSpecConformanceGate(&fakeUnparseableJudge{wrapped: transportErr})
	_, err := g.Evaluate(context.Background(), StageInput{})
	if err == nil {
		t.Fatalf("transport error must return non-nil err so runner escalates")
	}
	if !strings.Contains(err.Error(), "status 500") {
		t.Errorf("err should preserve underlying transport message, got %v", err)
	}
}

func TestLLMGate_SuccessfulLowScorePreservesExistingBehavior(t *testing.T) {
	// Regression guard: legacy soft-fail path (score < threshold, no
	// parse error) must keep its score-based JudgedBy and reasons.
	g := NewSpecConformanceGate(&FakeRubricJudge{Default: RubricVerdict{
		Score:   0.3,
		Reasons: []string{"missing tests"},
		Model:   "qwen-3-8b",
	}})
	out, err := g.Evaluate(context.Background(), StageInput{})
	if err != nil {
		t.Fatalf("evaluate: %v", err)
	}
	if out.Pass {
		t.Errorf("expected fail at score 0.3")
	}
	if out.JudgedBy == "flexinfer:unparseable" {
		t.Errorf("low-score path must not collide with unparseable JudgedBy")
	}
	if !strings.Contains(out.JudgedBy, "qwen") {
		t.Errorf("JudgedBy should reflect the judging model: %q", out.JudgedBy)
	}
}

func TestIsJudgeUnparseable_NilReturnsFalse(t *testing.T) {
	if isJudgeUnparseable(nil) {
		t.Error("nil error must not be classified as unparseable")
	}
}

func TestIsJudgeUnparseable_PlainErrorReturnsFalse(t *testing.T) {
	if isJudgeUnparseable(errors.New("network down")) {
		t.Error("plain non-wrapping error must not be classified as unparseable")
	}
}

func TestIsJudgeUnparseable_SentinelWrapReturnsTrue(t *testing.T) {
	wrapped := fmt.Errorf("rubric judge: parse: %w", ErrJudgeUnparseable)
	if !isJudgeUnparseable(wrapped) {
		t.Error("errors wrapping ErrJudgeUnparseable must be classified")
	}
}

func TestIsJudgeUnparseable_DuckTypedReturnsTrue(t *testing.T) {
	if !isJudgeUnparseable(&duckUnparseableErr{msg: "x"}) {
		t.Error("duck-typed predicate must classify cross-package wrappers")
	}
}

// --- M8: mills-canary label short-circuits LLM-judged gates ---

// recordingJudge counts Judge invocations. The M8 short-circuit must
// fire before the judge is asked anything; the canary tests assert
// Calls remains 0 when the canary label is set.
type recordingJudge struct {
	calls    int
	response RubricVerdict
}

func (r *recordingJudge) Judge(_ context.Context, _ string, _ StageInput) (RubricVerdict, error) {
	r.calls++
	return r.response, nil
}

// TestLLMGate_CanaryLabelShortCircuitsJudge pins the M8 contract: when
// the backlog item carries the canary label, the gate passes immediately
// with JudgedBy="skipped:canary" and the underlying judge is never
// consulted. Live evidence from PIPE-MILLS-CANARY-M6-164007-1779036007
// (2026-05-17): gemma4-26b returned a fabricated "file.py:10 - debug
// print found" verdict on a markdown-only diff. The skip removes the
// judge from the canary path entirely so model-quality hallucination
// can never bound canary pipeline completion again.
func TestLLMGate_CanaryLabelShortCircuitsJudge(t *testing.T) {
	judge := &recordingJudge{response: RubricVerdict{Score: 0.0, Model: "should-not-be-called"}}
	g := NewSpecConformanceGate(judge)
	in := StageInput{
		Item: &store.BacklogItem{
			ID:     "PIPE-CANARY-M8",
			Labels: []string{CanaryLabel, "safe-fixture"},
		},
	}
	out, err := g.Evaluate(context.Background(), in)
	if err != nil {
		t.Fatalf("evaluate: %v", err)
	}
	if !out.Pass {
		t.Errorf("canary item must pass LLM gate; got %+v", out)
	}
	if out.JudgedBy != CanarySkipJudgedBy {
		t.Errorf("JudgedBy = %q, want %q (operators grep for this token in gate_outcomes)", out.JudgedBy, CanarySkipJudgedBy)
	}
	if judge.calls != 0 {
		t.Errorf("judge.calls = %d, want 0 (canary skip must avoid the FlexInfer roundtrip)", judge.calls)
	}
	if len(out.Reasons) == 0 {
		t.Errorf("expected at least one reason describing the canary skip, got %v", out.Reasons)
	}
}

// TestLLMGate_CanaryLabelShortCircuitsPRSelfReviewToo ensures both
// LLM-judged gates share the skip path. The runner's post_review_gate
// stage lists both spec_conformance and pr_self_review; if only one
// skipped the canary would still escalate on the other.
func TestLLMGate_CanaryLabelShortCircuitsPRSelfReviewToo(t *testing.T) {
	judge := &recordingJudge{response: RubricVerdict{Score: 0.0}}
	g := NewPRSelfReviewGate(judge)
	in := StageInput{Item: &store.BacklogItem{ID: "X", Labels: []string{CanaryLabel}}}
	out, err := g.Evaluate(context.Background(), in)
	if err != nil {
		t.Fatalf("evaluate: %v", err)
	}
	if !out.Pass || out.JudgedBy != CanarySkipJudgedBy {
		t.Errorf("pr_self_review canary outcome = %+v, want Pass=true JudgedBy=%q", out, CanarySkipJudgedBy)
	}
	if judge.calls != 0 {
		t.Errorf("pr_self_review judge.calls = %d, want 0", judge.calls)
	}
}

// TestLLMGate_NonCanaryLabelsDoNotShortCircuit guards against a typo
// or label-overload regression: only the exact "mills-canary" label
// triggers the skip. Real backlog items with other labels (debt, docs,
// tech-debt, etc.) must continue to go through the judge.
func TestLLMGate_NonCanaryLabelsDoNotShortCircuit(t *testing.T) {
	judge := &recordingJudge{response: RubricVerdict{Score: 0.9, Model: "qwen-3-8b"}}
	g := NewSpecConformanceGate(judge)
	in := StageInput{
		Item: &store.BacklogItem{
			ID:     "BL-REAL-WORK",
			Labels: []string{"debt", "tech-debt", "mills-canary-but-not-quite", "safe-fixture"},
		},
	}
	out, err := g.Evaluate(context.Background(), in)
	if err != nil {
		t.Fatalf("evaluate: %v", err)
	}
	if judge.calls != 1 {
		t.Errorf("judge.calls = %d, want 1 (non-canary item must still go through the judge)", judge.calls)
	}
	if !out.Pass {
		t.Errorf("expected pass at score 0.9; got %+v", out)
	}
	if out.JudgedBy == CanarySkipJudgedBy {
		t.Errorf("non-canary item must NOT carry %q JudgedBy", CanarySkipJudgedBy)
	}
	if !strings.HasPrefix(out.JudgedBy, "flexinfer:") {
		t.Errorf("JudgedBy = %q, want flexinfer:* prefix for real LLM-judged path", out.JudgedBy)
	}
}

// TestLLMGate_NilItemDoesNotShortCircuit is the belt-and-suspenders
// case: a test or buggy caller passing StageInput{} (no Item) must not
// accidentally trigger the skip. The judge must run as configured.
func TestLLMGate_NilItemDoesNotShortCircuit(t *testing.T) {
	judge := &recordingJudge{response: RubricVerdict{Score: 0.9, Model: "qwen-3-8b"}}
	g := NewSpecConformanceGate(judge)
	out, err := g.Evaluate(context.Background(), StageInput{})
	if err != nil {
		t.Fatalf("evaluate: %v", err)
	}
	if judge.calls != 1 {
		t.Errorf("judge.calls = %d, want 1 (nil item must fall through to judge)", judge.calls)
	}
	if out.JudgedBy == CanarySkipJudgedBy {
		t.Errorf("nil item must not produce canary skip; got %+v", out)
	}
}

// TestLLMGate_EmptyLabelsDoNotShortCircuit pins the empty-slice case:
// Item present but Labels=nil or []string{} must not trigger the skip.
func TestLLMGate_EmptyLabelsDoNotShortCircuit(t *testing.T) {
	judge := &recordingJudge{response: RubricVerdict{Score: 0.85, Model: "qwen-3-8b"}}
	g := NewSpecConformanceGate(judge)
	in := StageInput{Item: &store.BacklogItem{ID: "X", Labels: nil}}
	out, err := g.Evaluate(context.Background(), in)
	if err != nil {
		t.Fatalf("evaluate: %v", err)
	}
	if judge.calls != 1 {
		t.Errorf("judge.calls = %d, want 1 (empty labels must fall through to judge)", judge.calls)
	}
	if out.JudgedBy == CanarySkipJudgedBy {
		t.Errorf("empty labels must not produce canary skip; got %+v", out)
	}
}

// TestItemHasCanaryLabel_PureFunction guards the helper's nil-safety
// and exact-match semantics.
func TestItemHasCanaryLabel_PureFunction(t *testing.T) {
	t.Run("nil item is false", func(t *testing.T) {
		if itemHasCanaryLabel(nil) {
			t.Error("nil item must not be classified as canary")
		}
	})
	t.Run("empty labels is false", func(t *testing.T) {
		if itemHasCanaryLabel(&store.BacklogItem{}) {
			t.Error("empty Labels must not match")
		}
	})
	t.Run("exact match is true", func(t *testing.T) {
		if !itemHasCanaryLabel(&store.BacklogItem{Labels: []string{CanaryLabel}}) {
			t.Error("exact mills-canary label must match")
		}
	})
	t.Run("substring match is false", func(t *testing.T) {
		if itemHasCanaryLabel(&store.BacklogItem{Labels: []string{"mills-canary-but-not-quite"}}) {
			t.Error("substring match must not trigger skip")
		}
	})
	t.Run("multi-label with canary among others is true", func(t *testing.T) {
		if !itemHasCanaryLabel(&store.BacklogItem{Labels: []string{"safe-fixture", CanaryLabel, "auto"}}) {
			t.Error("canary label found at any position must match")
		}
	})
}
