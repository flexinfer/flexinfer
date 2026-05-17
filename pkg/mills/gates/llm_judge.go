package gates

import (
	"context"
	"errors"
	"fmt"
	"log/slog"

	"github.com/crb2nu/loom/pkg/mills/store"
)

// ErrJudgeUnparseable is the gate-layer sentinel for "the judge ran but
// returned a response we couldn't grade". It mirrors
// pkg/mills/clients.ErrRubricUnparseable, exposed at the gates layer so
// the gate package has no import cycle on clients. The clients package
// wraps its sentinel; LLMGate.Evaluate translates that into this one
// (and a soft Outcome) so the runner takes the retry branch instead of
// the no-retry escalation branch.
//
// Tests can `errors.Is(out.Reasons-derived-err, gates.ErrJudgeUnparseable)`
// after fishing the wrap chain out, but the canonical contract is
// Outcome.JudgedBy == "flexinfer:unparseable" + Outcome.Pass=false +
// err=nil so the runner naturally rewinds to RetryFrom.
var ErrJudgeUnparseable = errors.New("gates: judge output unparseable")

// CanaryLabel marks a backlog item as a deterministic Mills canary
// (one-line edit to testdata/mills-canary/heartbeat.md, no real spec to
// conform to and nothing meaningful for a self-review rubric to grade).
// When an item carries this label, LLM-judged gates short-circuit with
// JudgedBy="skipped:canary" so the canary exercises the rest of the
// pipeline (mr → merge → cleanup) instead of being bounded by model
// quality. Pure-Go gates (path_policy, secret_scan, diff_size, scope,
// commit_format) still run — they're cheap and catch real things.
//
// This constant is the same string referenced by
// cmd/loom-mills-operator/handlers_backlog.go::canaryDedupeLabel and
// cmd/loom/cmd_mills_pipelines.go::canaryDedupeLabel; callers should
// converge here on follow-up if the strings drift.
const CanaryLabel = "mills-canary"

// CanarySkipJudgedBy is the audit-trail token persisted to
// gate_outcomes.judged_by when an LLM gate is short-circuited because
// the backlog item carries CanaryLabel. Operators reviewing a canary
// run grep for this exact token to see WHY the gate passed without an
// LLM call.
const CanarySkipJudgedBy = "skipped:canary"

// errJudgeUnparseable is the predicate the LLMGate uses to detect a
// soft-failure (judge returned but we couldn't grade). Pure-Go gates
// using FakeRubricJudge can return any error type wrapping
// ErrJudgeUnparseable to opt in; production wires through the clients
// package's ErrRubricUnparseable, which the gates package can't import
// directly without a cycle. RubricJudge implementations may set the
// optional UnparseableError interface to expose the predicate at the
// gate layer.
//
// We accept either:
//   - errors.Is(err, ErrJudgeUnparseable) — preferred for in-package tests
//   - an err whose chain contains an error with method
//     IsRubricUnparseable() bool returning true — used by the clients
//     package's ErrRubricUnparseable indirection
func isJudgeUnparseable(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, ErrJudgeUnparseable) {
		return true
	}
	// Duck-typed predicate: any wrapped error with this method opts in.
	// Implemented by the clients package's ErrRubricUnparseable wrapper
	// without creating an import cycle.
	type unparseable interface {
		IsRubricUnparseable() bool
	}
	var u unparseable
	if errors.As(err, &u) && u.IsRubricUnparseable() {
		return true
	}
	return false
}

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
	// Logger is optional; nil falls back to slog.Default().
	Logger *slog.Logger
}

// Name satisfies the gates.Gate contract.
func (g *LLMGate) Name() string { return g.GateName }

func (g *LLMGate) logger() *slog.Logger {
	if g.Logger != nil {
		return g.Logger
	}
	return slog.Default()
}

// Evaluate satisfies the gates.Gate contract.
//
// Error contract (post-M2.5):
//   - judge returns nil error + verdict: convert score → Outcome.Pass against
//     threshold (existing behavior).
//   - judge returns an error wrapping ErrJudgeUnparseable / a clients-layer
//     ErrRubricUnparseable: this is a *soft* failure — the LLM ran fine, the
//     operator just couldn't read the answer. Return Outcome{Pass:false,
//     JudgedBy:"flexinfer:unparseable"} and err=nil so the runner takes the
//     existing gate-retry path (rewind to RetryFrom, bump attempt counter)
//     instead of the no-retry escalation branch.
//   - judge returns any other error (network, timeout, 5xx): infrastructure
//     failure — propagate the error so the runner escalates.
//
// Background: 2026-05-16 canary PIPE-MILLS-CANARY-M1D-VERIFY-2 escalated on
// the first spec_conformance call because gemma4-26b returned free-text.
// See .loom/119-…2026-05-16.md for the diagnosis.
func (g *LLMGate) Evaluate(ctx context.Context, in StageInput) (Outcome, error) {
	// Canary short-circuit (M8): deterministic Mills canaries are one-line
	// markdown edits to testdata/mills-canary/heartbeat.md. There is no
	// spec to conform to and nothing meaningful for self-review to grade,
	// and gemma4-26b hallucinates fail verdicts on the trivial diff (live
	// evidence 2026-05-17: PIPE-MILLS-CANARY-M6-164007-1779036007
	// returned "score=0.00 below threshold=0.70 | Example:
	// file.py:10 - debug print found" — a Python file that exists nowhere
	// in the diff). Pass the gate with an honest audit token so operators
	// can grep gate_outcomes.judged_by="skipped:canary" to see WHY the
	// gate passed without an LLM call. Pure-Go gates (path_policy,
	// secret_scan, diff_size, scope, commit_format) still run.
	if itemHasCanaryLabel(in.Item) {
		g.logger().Info("llm gate: skipped for mills-canary deterministic fixture",
			"gate", g.GateName, "rubric", g.RubricName)
		return Outcome{
			Pass:     true,
			JudgedBy: CanarySkipJudgedBy,
			Reasons:  []string{"LLM gate skipped for mills-canary deterministic fixture"},
		}, nil
	}
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
		if isJudgeUnparseable(err) {
			g.logger().Warn("llm gate: judge output unparseable; soft-failing for retry",
				"gate", g.GateName, "rubric", g.RubricName, "error", err)
			return Outcome{
				Pass:     false,
				JudgedBy: "flexinfer:unparseable",
				Reasons: []string{
					fmt.Sprintf("judge response could not be parsed into a score envelope: %v", err),
				},
			}, nil
		}
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

// itemHasCanaryLabel reports whether the backlog item carries the
// CanaryLabel. Nil-safe: a missing item returns false (the runner
// composes StageInput.Item from the live backlog, but defensive code
// here lets unit tests pass StageInput{} without panicking).
func itemHasCanaryLabel(item *store.BacklogItem) bool {
	if item == nil {
		return false
	}
	for _, lbl := range item.Labels {
		if lbl == CanaryLabel {
			return true
		}
	}
	return false
}
