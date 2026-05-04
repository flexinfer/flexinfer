package council

import (
	"context"
	"errors"
)

// Moderator decides between debate rounds whether the reviewers + editor
// have converged or whether another revise round is warranted. When not
// converged, it issues a list of focus areas — short tags the next
// editor.revise call should narrow on (e.g. "auth.middleware",
// "test.coverage.gaps", "spec.exit-criteria").
//
// The moderator is itself an agent in production (a small frontier model
// per .loom/93- §"Council Debate Mode"), but the contract here is plain
// Go so debate tests + dryrun runs can assert convergence behavior
// without live agent calls.
type Moderator interface {
	// Assess weighs the latest critiques against the prior transcript
	// and returns a decision. Implementations may inspect ALL prior
	// rounds (so they can detect "ping-pong" disagreement and force
	// convergence) but must focus their reasoning on latestCritiques.
	Assess(ctx context.Context, prior []SidecarDebateRound, latestCritiques []ReviewerOutput) (ModeratorDecision, error)
}

// ModeratorDecision is the structured output of one Assess call. The
// debate runner consumes it to decide whether to emit the current draft
// or fan out another revise round.
type ModeratorDecision struct {
	// Converged is true when the moderator judges that further rounds
	// would not materially improve the artifact. The runner emits the
	// current best draft and exits.
	Converged bool
	// FocusAreas is the moderator's directive to the next
	// editor.revise + reviewer.RefocusedReview pair. Empty when
	// Converged == true.
	FocusAreas []string
	// Summary is a short markdown blurb persisted to
	// council_debate_rounds.summary so the HUD's "Debate Rounds"
	// expander can show the moderator's reasoning at a glance.
	Summary string
	// CostUSD attributes the moderator's spend back to the council
	// run's debate budget cap.
	CostUSD float64
}

// FakeModerator is the deterministic test double + dryrun fallback. It
// converges after a configurable round index (0-based, counted as
// moderator decision rounds — i.e. ConvergeAfterRound=0 converges on
// the first decision; ConvergeAfterRound=1 converges on the second).
//
// FocusAreas is returned verbatim when the moderator does NOT converge,
// so debate tests can assert focus-area threading through the
// editor.revise + reviewer.RefocusedReview path.
type FakeModerator struct {
	// ConvergeAfterRound: number of non-converged decisions to emit
	// before declaring convergence. 0 = always converge first call.
	ConvergeAfterRound int
	// FocusAreas is the focus tag list returned on non-converged
	// decisions. Defaults to ["focus_default"] when empty so the
	// debate runner has something to thread.
	FocusAreas []string
	// Summary, when set, replaces the default "fake moderator: ..."
	// blurb so tests can pin specific summary text in the transcript.
	Summary string
	// CostUSD is reported per Assess call. Default 0.05.
	CostUSD float64
	// ReturnErr surfaces a hard failure so error-path tests don't
	// need a separate stub.
	ReturnErr error

	// internal: count of Assess calls so far (used to compare
	// against ConvergeAfterRound).
	calls int
}

// Assess implements Moderator.
func (f *FakeModerator) Assess(ctx context.Context, prior []SidecarDebateRound, latestCritiques []ReviewerOutput) (ModeratorDecision, error) {
	if err := ctx.Err(); err != nil {
		return ModeratorDecision{}, err
	}
	if f.ReturnErr != nil {
		return ModeratorDecision{}, f.ReturnErr
	}
	cost := f.CostUSD
	if cost == 0 {
		cost = 0.05
	}
	converged := f.calls >= f.ConvergeAfterRound
	f.calls++
	dec := ModeratorDecision{
		Converged: converged,
		Summary:   f.Summary,
		CostUSD:   cost,
	}
	if dec.Summary == "" {
		if converged {
			dec.Summary = "fake moderator: converged"
		} else {
			dec.Summary = "fake moderator: needs another round"
		}
	}
	if !converged {
		focus := f.FocusAreas
		if len(focus) == 0 {
			focus = []string{"focus_default"}
		}
		dec.FocusAreas = focus
	}
	return dec, nil
}

// validateModerator centralises the nil-check + zero-call sanity check
// the debate runner needs before kicking off any rounds.
func validateModerator(m Moderator) error {
	if m == nil {
		return errors.New("council: debate requires a Moderator")
	}
	return nil
}
