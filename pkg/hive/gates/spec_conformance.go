package gates

// SpecConformanceRubricName is the stable rubric id the FlexInfer-backed
// RubricJudge resolves to a prompt template. The version suffix lets us
// roll the rubric forward without changing the gate name.
const SpecConformanceRubricName = "spec_conformance_v1"

// SpecConformanceRubric is the prompt body the production judge wraps
// around a stage diff + spec doc to produce a numeric score in [0,1].
//
// The exact text is part of the persisted contract — changing it must
// rev SpecConformanceRubricName so eval rows stay comparable.
const SpecConformanceRubric = `You are a strict reviewer for a software engineering pipeline.

Given:
1. The spec document (or anchor) the change is supposed to implement.
2. The unified diff produced by the implementation stage.
3. The list of files changed and their slice scope.

Score how well the diff conforms to the spec on a [0.0, 1.0] scale where:
- 1.0 = every spec requirement has matching code; no extra unrelated changes.
- 0.8 = all critical requirements satisfied; minor scope deviations.
- 0.5 = partial implementation; major requirement(s) unmet.
- 0.0 = unrelated change, breaking change, or contradicts the spec.

Reply with a JSON object:
  {"score": <float>, "reasons": ["...", "..."]}

Be terse in reasons[]. List concrete deviations only — do not restate
what the spec says.`

// NewSpecConformanceGate constructs the spec_conformance gate using the
// supplied judge. Default threshold 0.8 — verdicts at or above are pass.
//
// The spec is explicit that LLM-judged gates always run on FlexInfer;
// production callers wire a FlexInfer-backed RubricJudge here.
func NewSpecConformanceGate(judge RubricJudge) *LLMGate {
	return &LLMGate{
		GateName:   "spec_conformance",
		RubricName: SpecConformanceRubricName,
		Threshold:  0.8,
		Judge:      judge,
	}
}
