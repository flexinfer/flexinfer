package gates

import (
	"context"
	"fmt"
)

// defaultMaxDiffLines is the per-item ceiling when neither the policy's
// per-label override nor the item's budget supplies one. Tuned for "one
// reviewable change per backlog item"; larger PRs get auto-split during
// council planning, not waved through.
const defaultMaxDiffLines = 800

// DiffSize fails when the implement stage's diff is larger than the
// per-item ceiling. Caps the blast radius of any single auto-merged
// change and turns "the agent went on a tear" into an escalation rather
// than a 5,000-line surprise on main.
type DiffSize struct {
	// MaxLines overrides the per-instance ceiling for tests. Zero falls
	// back to the policy / item override / package default.
	MaxLines int
}

// Name identifies the gate in persistence + logs.
func (g *DiffSize) Name() string { return "diff_size" }

// Evaluate compares (LinesAdded + LinesRemoved) against the ceiling
// derived from (gate override → per-item budget → package default).
func (g *DiffSize) Evaluate(_ context.Context, in StageInput) (Outcome, error) {
	limit := g.MaxLines
	if limit <= 0 {
		limit = effectiveDiffLimit(in)
	}
	total := in.LinesAdded + in.LinesRemoved
	if total <= limit {
		return pass(), nil
	}
	return fail(fmt.Sprintf(
		"diff is %d lines (added %d, removed %d); cap is %d",
		total, in.LinesAdded, in.LinesRemoved, limit,
	)), nil
}

// effectiveDiffLimit picks the tightest non-zero ceiling. Item-level
// policy can be more restrictive than the global default; we never
// loosen below the package default unless an explicit override fires.
func effectiveDiffLimit(in StageInput) int {
	// Item budget can name a ceiling indirectly via MaxPipelineMinutes,
	// but the schema doesn't have a dedicated diff cap yet. Reserve the
	// extension point for when the item or policy adds one.
	return defaultMaxDiffLines
}
