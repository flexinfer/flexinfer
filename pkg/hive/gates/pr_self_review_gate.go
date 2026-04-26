package gates

// PRSelfReviewRubricName is the stable rubric id for the post-implement
// PR self-review pass. Version suffix lets the rubric roll forward.
const PRSelfReviewRubricName = "pr_self_review_v1"

// PRSelfReviewRubric is the prompt body the production judge wraps
// around the diff + commit messages + tests outcome to produce a score.
//
// The threshold is intentionally lower than spec_conformance because
// self-review is heuristic; the gate is a smoke test against obvious
// regressions, not a definitive verdict.
const PRSelfReviewRubric = `You are reviewing your own work before opening a merge request.

Given:
1. The unified diff (with file paths).
2. The commit messages.
3. The test_results from the prior tests stage (pass/fail per check).

Score on [0.0, 1.0] where:
- 1.0 = code matches conventions, no debug/dead code, tests cover the
  happy path and a sensible edge case, commit messages follow the
  Conventional Commits format used in this repo.
- 0.7 = ready for review; minor cleanup nits only.
- 0.5 = obvious problems (debug prints, commented code, missing tests).
- 0.0 = unsafe to ship (secrets, deletes critical paths, broken build).

Reply with a JSON object:
  {"score": <float>, "reasons": ["...", "..."]}

Reasons[] must point to a specific file:line or commit if possible.`

// NewPRSelfReviewGate constructs the pr_self_review gate using the
// supplied judge. Default threshold 0.7 (more permissive than
// spec_conformance because self-review is heuristic).
func NewPRSelfReviewGate(judge RubricJudge) *LLMGate {
	return &LLMGate{
		GateName:   "pr_self_review",
		RubricName: PRSelfReviewRubricName,
		Threshold:  0.7,
		Judge:      judge,
	}
}
