package gates

import (
	"context"
	"errors"
	"fmt"
)

// RubricJudge evaluates a strict rubric prompt against a stage input and
// returns a score in [0,1] plus human-readable reasons. The contract is
// deliberately narrow: no streaming, no tool calls, no chain-of-thought
// — gates run inside the operator and need to be cheap and deterministic.
//
// Production implementations always wrap a FlexInfer call (never a
// frontier model). The spec is explicit that LLM-judged gates use the
// local stack so per-pipeline-run cost stays bounded.
type RubricJudge interface {
	// Judge applies the named rubric prompt to in and returns score,
	// reasons[], model id (for JudgedBy tagging), and any error.
	Judge(ctx context.Context, rubric string, in StageInput) (RubricVerdict, error)
}

// RubricVerdict is the structured response from a RubricJudge call.
type RubricVerdict struct {
	Score   float64
	Reasons []string
	Model   string
}

// FakeRubricJudge returns canned verdicts. Used in tests + dryrun mode.
type FakeRubricJudge struct {
	// ByRubric maps rubric name → canned verdict. Keys not in the map
	// fall through to Default.
	ByRubric map[string]RubricVerdict
	Default  RubricVerdict
	Err      error
}

// Judge implements RubricJudge.
func (f *FakeRubricJudge) Judge(_ context.Context, rubric string, _ StageInput) (RubricVerdict, error) {
	if f.Err != nil {
		return RubricVerdict{}, f.Err
	}
	if v, ok := f.ByRubric[rubric]; ok {
		return v, nil
	}
	return f.Default, nil
}

// LLMGate is the shared base for spec_conformance and pr_self_review. It
// invokes a RubricJudge and converts the verdict into an Outcome.
type LLMGate struct {
	GateName   string
	RubricName string
	Threshold  float64 // verdicts with Score >= Threshold pass; default 0.8
	Judge      RubricJudge
	// Disabled, when true, returns Outcome{Pass:true, JudgedBy:"flexinfer:disabled"}.
	// Useful when policy.gates.llm_judged_disabled is on (cost spike, outage).
	Disabled bool
}

// Name satisfies the gates.Gate contract.
func (g *LLMGate) Name() string { return g.GateName }

// Evaluate satisfies the gates.Gate contract.
func (g *LLMGate) Evaluate(ctx context.Context, in StageInput) (Outcome, error) {
	if g.Disabled {
		return Outcome{Pass: true, JudgedBy: "flexinfer:disabled"}, nil
	}
	if g.Judge == nil {
		return Outcome{}, errors.New(g.GateName + ": rubric judge not configured")
	}
	threshold := g.Threshold
	if threshold <= 0 {
		threshold = 0.8
	}
	v, err := g.Judge.Judge(ctx, g.RubricName, in)
	if err != nil {
		return Outcome{}, fmt.Errorf("%s: judge: %w", g.GateName, err)
	}
	model := v.Model
	if model == "" {
		model = "flexinfer"
	}
	out := Outcome{
		Pass:     v.Score >= threshold,
		JudgedBy: "flexinfer:" + model,
	}
	if !out.Pass {
		out.Reasons = append([]string{fmt.Sprintf("score=%.2f below threshold=%.2f", v.Score, threshold)}, v.Reasons...)
	}
	return out, nil
}

// RegisterLLMGates wires the spec_conformance and pr_self_review gates
// onto a registry. The operator calls this at startup once a FlexInfer
// client is available; tests pass a FakeRubricJudge.
func RegisterLLMGates(r *Registry, judge RubricJudge) {
	r.Register(NewSpecConformanceGate(judge))
	r.Register(NewPRSelfReviewGate(judge))
}
