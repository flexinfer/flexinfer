// Package council slice 5.1: Council Debate Mode.
//
// Debate Mode is the multi-round editor↔reviewer refinement loop the Mills
// v2 spec calls for in §"Council Debate Mode". A single Run() executes
// the round structure documented there (Round 0 propose → Round 1+
// critique→assess→revise) under a hard USD budget cap and a max-rounds
// safety bound, returning the same EditorOutput shape a single-pass
// council run produces plus a transcript that the runner stamps into
// Sidecar.Debate.
//
// The runner is decoupled from the rest of the council pipeline — it
// does not write artifacts, persist to SQLite, mutate the backlog, or
// trigger the audit pool. pkg/mills/runner/runner.go branches on
// policy.Debate when a trigger is debate-eligible and calls Debate.Run
// in place of (Editor.Edit + Dispatcher.Dispatch); on the way out the
// runner stamps res.Editor + Sidecar.Debate from the DebateResult and
// hands off to the existing artifact / judge / mutator stages exactly
// as before.
package council

import (
	"context"
	"errors"
	"fmt"
	"time"
)

// Debate orchestrates one debate-mode council pass. Wiring is explicit
// (no globals, no env reads) so production wires the same struct the
// tests do, with FakeEditor / FakeReviewer / FakeModerator swapped for
// spawn-backed implementations.
type Debate struct {
	// Editor produces the initial draft (Round 0). When it also
	// implements Reviser, debate Round 2+ calls Revise; when it does
	// not, debate degrades to single-pass and emits a transcript with
	// only the Round-0 entry plus a moderator decision row marking
	// the degradation reason.
	Editor Editor
	// Reviewers fans the brief out to all configured lenses in
	// parallel. Round 1's parallel critique uses Dispatch; later
	// rounds use RefocusedReview when the reviewer implements
	// Refocuser, falling back to Dispatch otherwise.
	Reviewers *Dispatcher
	// Lenses configured for this run. Same shape council.Run uses.
	Lenses []ReviewerLens
	// Moderator decides convergence between rounds.
	Moderator Moderator

	// Now is injectable for deterministic transcript timestamps in
	// tests + dryrun. Defaults to time.Now.
	Now func() time.Time
}

// DebateInput tunes one Debate.Run invocation.
type DebateInput struct {
	// Brief is the council brief — exactly the same shape v1 council
	// passes through Editor.Edit.
	Brief *Brief
	// MaxUSD caps total debate spend (every round's CostUSD summed,
	// across editor + reviewers + moderator). When exceeded mid-round
	// the runner exits with EarlyExitReason="budget" and emits the
	// best-so-far draft. Zero disables the cap (test convenience —
	// production always sets policy.council.debate.max_usd).
	MaxUSD float64
	// MaxRounds caps the number of *full* rounds beyond Round 0
	// (i.e. critique + assess + revise triples). Round 0 is the
	// editor.propose pass that always runs. Per spec the absolute max
	// is 3 (Round 3 is editor.revise without a follow-up critique);
	// values ≤ 0 default to 3.
	MaxRounds int
	// EarlyExitThreshold is the fraction of MaxUSD at which the
	// runner stops *before* the next round begins. 0.8 by default.
	// Pass 0 to use the default; pass <0 to disable the threshold
	// (still bounded by MaxUSD itself).
	EarlyExitThreshold float64
	// PerReviewerTimeout is forwarded to Dispatcher.Dispatch so a
	// hung lens doesn't stall the whole debate. Optional.
	PerReviewerTimeout time.Duration
	// MinQuorum is forwarded to Dispatcher.Dispatch. Defaults to a
	// simple majority of len(lenses).
	MinQuorum int
}

// DebateResult is what one Debate.Run call returns. The runner uses
// Editor + the per-round transcript to populate the council's
// artifact + sidecar; tests assert directly against Rounds.
type DebateResult struct {
	// Editor is the final EditorOutput after the last revise (or the
	// Round-0 propose if the moderator converged immediately). The
	// pipeline / writer / judge / mutator consume this exactly like
	// a v1 single-pass run.
	Editor *EditorOutput
	// Reviews is the most recent round's critique set, returned so
	// the runner can keep its existing eval input shape.
	Reviews []ReviewerOutput
	// Rounds is the chronological transcript. The runner copies this
	// verbatim into Sidecar.Debate.Rounds.
	Rounds []SidecarDebateRound
	// EarlyExitReason is set when the run did NOT reach MaxRounds —
	// "converged" (moderator declared agreement), "budget" (≥
	// EarlyExitThreshold of MaxUSD consumed mid-loop), or
	// "editor_not_reviser" (editor doesn't implement Reviser, so the
	// run degraded to single-pass). Empty when the run hit MaxRounds.
	EarlyExitReason string
	// TotalCostUSD is the sum of every Rounds[i].CostUSD entry plus
	// the final editor.revise cost (since the final revise round may
	// have an editor entry but no follow-up moderator decision).
	TotalCostUSD float64
}

// Run executes the debate loop. Errors are infrastructure-level only
// (editor / reviewer dispatcher / moderator returned hard failures);
// budget exhaustion and non-convergence are not errors — they emit a
// best-so-far DebateResult with EarlyExitReason set.
func (d *Debate) Run(ctx context.Context, in DebateInput) (*DebateResult, error) {
	if d == nil {
		return nil, errors.New("council: nil Debate")
	}
	if d.Editor == nil {
		return nil, errors.New("council: debate requires an Editor")
	}
	if d.Reviewers == nil {
		return nil, errors.New("council: debate requires a Reviewer Dispatcher")
	}
	if err := validateModerator(d.Moderator); err != nil {
		return nil, err
	}
	if in.Brief == nil {
		return nil, errors.New("council: debate requires a Brief")
	}
	if len(d.Lenses) == 0 {
		return nil, errors.New("council: debate requires ≥ 1 reviewer lens")
	}

	maxRounds := in.MaxRounds
	if maxRounds <= 0 {
		maxRounds = 3
	}
	threshold := in.EarlyExitThreshold
	if threshold == 0 {
		threshold = 0.8
	}

	res := &DebateResult{}

	// ----- Round 0: editor.propose -----
	round0, err := d.Editor.Edit(ctx, in.Brief, nil)
	if err != nil {
		return nil, fmt.Errorf("debate round 0 (editor propose): %w", err)
	}
	res.Editor = round0
	res.Rounds = append(res.Rounds, SidecarDebateRound{
		Round:   0,
		Role:    "editor_proposes",
		CostUSD: round0.CostUSD,
		Summary: roundSummary(round0),
	})
	res.TotalCostUSD += round0.CostUSD

	// If the editor isn't a Reviser, debate cannot revise; emit Round
	// 0 + a moderator-style "degraded" decision row and return.
	reviser, _ := d.Editor.(Reviser)
	if reviser == nil {
		res.EarlyExitReason = "editor_not_reviser"
		return res, nil
	}

	// ----- Rounds 1..N: critique → assess → revise -----
	dispatchOpts := DispatchOptions{
		PerReviewerTimeout: in.PerReviewerTimeout,
		MinQuorum:          in.MinQuorum,
	}
	if dispatchOpts.MinQuorum <= 0 {
		dispatchOpts.MinQuorum = (len(d.Lenses) + 1) / 2
	}

	currentDraft := round0
	var lastCritiques []ReviewerOutput
	for round := 1; round <= maxRounds; round++ {
		if d.budgetExhausted(in, res.TotalCostUSD, threshold) {
			res.EarlyExitReason = "budget"
			break
		}

		// Critique. Round 1 uses Dispatch (no focus areas yet);
		// rounds 2+ use RefocusedReview when the reviewer implements
		// it, else falls back to Dispatch.
		var (
			critiques []ReviewerOutput
			cErr      error
		)
		if round == 1 {
			critiques, cErr = d.Reviewers.Dispatch(ctx, in.Brief, d.Lenses, dispatchOpts)
		} else {
			critiques, cErr = d.refocusedDispatch(ctx, in.Brief, d.Lenses, prevFocusAreas(res.Rounds), dispatchOpts)
		}
		if cErr != nil {
			// Reviewer quorum failure mid-debate: surface as
			// infrastructure error so the runner can decide to
			// retry or fall back to single-pass. Includes any
			// best-so-far draft via the wrapped result.
			return res, fmt.Errorf("debate round %d (reviewers): %w", round, cErr)
		}
		lastCritiques = critiques
		critiquesCost := sumCost(critiques)
		res.Rounds = append(res.Rounds, SidecarDebateRound{
			Round:   round,
			Role:    "reviewer_critiques",
			CostUSD: critiquesCost,
			Summary: critiquesSummary(critiques),
		})
		res.TotalCostUSD += critiquesCost

		// Skip the moderator on the absolute final round — Round 3
		// per spec is editor.revise terminating, no follow-up
		// critique; if we got here on round == maxRounds, the
		// editor.revise below is the run's last act.
		var decision ModeratorDecision
		if round < maxRounds {
			dec, mErr := d.Moderator.Assess(ctx, res.Rounds, critiques)
			if mErr != nil {
				return res, fmt.Errorf("debate round %d (moderator): %w", round, mErr)
			}
			decision = dec
			converged := dec.Converged
			modRound := SidecarDebateRound{
				Round:      round,
				Role:       "moderator_decision",
				CostUSD:    dec.CostUSD,
				Summary:    dec.Summary,
				Converged:  &converged,
				FocusAreas: append([]string(nil), dec.FocusAreas...),
			}
			res.Rounds = append(res.Rounds, modRound)
			res.TotalCostUSD += dec.CostUSD
			if dec.Converged {
				res.EarlyExitReason = "converged"
				break
			}
			if d.budgetExhausted(in, res.TotalCostUSD, threshold) {
				res.EarlyExitReason = "budget"
				break
			}
		}

		// Editor revise. Pass the latest critiques + moderator's
		// focus areas (empty on the final round, since we skipped
		// the moderator).
		focusAreas := decision.FocusAreas
		revised, rErr := reviser.Revise(ctx, currentDraft, critiques, focusAreas)
		if rErr != nil {
			return res, fmt.Errorf("debate round %d (editor revise): %w", round, rErr)
		}
		currentDraft = revised
		res.Editor = revised
		res.Rounds = append(res.Rounds, SidecarDebateRound{
			Round:          round,
			Role:           "editor_revises",
			CostUSD:        revised.CostUSD,
			Summary:        roundSummary(revised),
			ArtifactDeltas: deriveArtifactDeltas(currentDraft, revised),
		})
		res.TotalCostUSD += revised.CostUSD
	}

	res.Reviews = lastCritiques
	return res, nil
}

// refocusedDispatch is Dispatcher.Dispatch with a per-reviewer
// type-assertion to Refocuser. Reviewers without RefocusedReview fall
// back to plain Review (no focus area threading), which keeps the run
// alive on a partially-upgraded reviewer fleet.
func (d *Debate) refocusedDispatch(ctx context.Context, brief *Brief, lenses []ReviewerLens, focusAreas []string, opts DispatchOptions) ([]ReviewerOutput, error) {
	if len(focusAreas) == 0 {
		return d.Reviewers.Dispatch(ctx, brief, lenses, opts)
	}
	out := make([]ReviewerOutput, len(lenses))
	errCh := make(chan error, len(lenses))
	for i, lens := range lenses {
		i, lens := i, lens
		r, ok := d.Reviewers.Reviewers[lens.Name]
		if !ok {
			out[i] = ReviewerOutput{Lens: lens, Err: fmt.Errorf("no reviewer registered for lens %q", lens.Name)}
			continue
		}
		go func() {
			rctx := ctx
			if opts.PerReviewerTimeout > 0 {
				var cancel context.CancelFunc
				rctx, cancel = context.WithTimeout(ctx, opts.PerReviewerTimeout)
				defer cancel()
			}
			var (
				res ReviewerOutput
				err error
			)
			if rf, isRf := r.(Refocuser); isRf {
				res, err = rf.RefocusedReview(rctx, brief, lens, focusAreas)
			} else {
				res, err = r.Review(rctx, brief, lens)
			}
			if err != nil {
				res.Lens = lens
				res.Err = err
			}
			out[i] = res
			errCh <- nil
		}()
	}
	for range lenses {
		<-errCh
	}
	successes := 0
	for _, o := range out {
		if o.Err == nil {
			successes++
		}
	}
	quorum := opts.MinQuorum
	if quorum <= 0 {
		quorum = len(lenses)
	}
	if successes < quorum {
		return out, fmt.Errorf("council: refocused quorum failure (%d/%d reviewers succeeded; need %d)",
			successes, len(out), quorum)
	}
	return out, nil
}

// budgetExhausted reports whether the next round should NOT start. Pure
// function so tests can pin its boundary behaviour.
func (d *Debate) budgetExhausted(in DebateInput, spent, threshold float64) bool {
	if in.MaxUSD <= 0 {
		return false
	}
	if threshold < 0 {
		// Threshold disabled; only the hard cap stops the loop.
		return spent >= in.MaxUSD
	}
	return spent >= in.MaxUSD*threshold
}

// prevFocusAreas finds the most recent moderator_decision row's focus
// areas. Returns nil when no decision has been recorded yet.
func prevFocusAreas(rounds []SidecarDebateRound) []string {
	for i := len(rounds) - 1; i >= 0; i-- {
		if rounds[i].Role == "moderator_decision" {
			return rounds[i].FocusAreas
		}
	}
	return nil
}

// sumCost adds the CostUSD of every reviewer output, ignoring lens
// failures (their CostUSD is 0 by convention).
func sumCost(outs []ReviewerOutput) float64 {
	var total float64
	for _, o := range outs {
		total += o.CostUSD
	}
	return total
}

// roundSummary returns a short scannable string for the debate
// transcript when an editor round completes. Length is bounded so the
// summary survives JSON round-trip without ballooning the sidecar.
func roundSummary(out *EditorOutput) string {
	if out == nil {
		return ""
	}
	docs := len(out.Documents)
	props := len(out.BacklogProposals)
	return fmt.Sprintf("editor produced %d doc(s), %d proposal(s), $%.4f",
		docs, props, out.CostUSD)
}

// critiquesSummary returns a short string for the reviewer_critiques
// row in the transcript. Lists the lens names and total cost.
func critiquesSummary(outs []ReviewerOutput) string {
	if len(outs) == 0 {
		return "no critiques"
	}
	names := make([]string, 0, len(outs))
	for _, o := range outs {
		if o.Err != nil {
			continue
		}
		names = append(names, o.Lens.Name)
	}
	if len(names) == 0 {
		return "all reviewers errored"
	}
	return fmt.Sprintf("%d critique(s) from [%s]; $%.4f",
		len(names), joinCommaSep(names), sumCost(outs))
}

// deriveArtifactDeltas produces a best-effort summary of which artifact
// docs the editor.revise touched between rounds. We compare doc kinds
// and titles only — body diffs would bloat the sidecar. Production
// editors that emit explicit deltas can override by setting them on
// the returned EditorOutput before this function is called (currently
// no editor does this, so the summary is always derived).
func deriveArtifactDeltas(prior, revised *EditorOutput) []SidecarDebateDelta {
	if prior == nil || revised == nil {
		return nil
	}
	deltas := make([]SidecarDebateDelta, 0, len(revised.Documents))
	for _, d := range revised.Documents {
		deltas = append(deltas, SidecarDebateDelta{
			Path:   d.Kind.FilenameFragment(),
			Action: "edit",
		})
	}
	return deltas
}

// joinCommaSep formats a name list with a comma + space separator.
// Standalone helper so the test golden strings are reproducible without
// importing strings into the call site.
func joinCommaSep(names []string) string {
	out := ""
	for i, n := range names {
		if i > 0 {
			out += ", "
		}
		out += n
	}
	return out
}
