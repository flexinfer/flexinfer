package gates

import (
	"context"
	"errors"
	"strings"
	"testing"
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
