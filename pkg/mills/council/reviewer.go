package council

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/crb2nu/loom/pkg/mills"
)

// Reviewer is the per-lens contract every reviewer agent satisfies. The
// dispatcher fans out one Review call per configured lens, gathers the
// outputs, and hands them to the editor.
//
// Implementations:
//   - FlexInfer-backed (in-cluster) for the local-cost reviewers
//   - spawn-backed (Claude/Codex headless) for the frontier reviewers
//
// Both land in the operator's wiring layer (Phase 3 slice 3.7+); the
// interface here lets the dispatcher be tested with deterministic
// fakes and lets the council dryrun produce structured output without
// any live agent calls.
type Reviewer interface {
	Review(ctx context.Context, brief *Brief, lens ReviewerLens) (ReviewerOutput, error)
}

// Refocuser is the optional Mills v2 extension to Reviewer that supports
// re-running a critique narrowed to specific focus areas (issued by the
// debate moderator after a non-converged round). Reviewers that don't
// implement Refocuser cause the debate runner to fall back to a plain
// Review call, so production wiring can adopt incrementally.
type Refocuser interface {
	Reviewer
	RefocusedReview(ctx context.Context, brief *Brief, lens ReviewerLens, focusAreas []string) (ReviewerOutput, error)
}

// ReviewerLens names the brief addendum + system prompt the reviewer
// runs under (e.g. "security", "tech-debt", "user-impact"). Carries the
// model + backend so audit + cost attribution stay precise.
type ReviewerLens struct {
	Name    string
	Model   string
	Backend string
}

// ReviewerOutput is what one Reviewer returns. Markdown is the reviewer's
// note (consumed by the editor as a tool-result-style turn input);
// CostUSD attributes spend back to the council_runs.cost_*_usd fields.
type ReviewerOutput struct {
	Lens     ReviewerLens
	Markdown string
	CostUSD  float64
	Err      error // when non-nil, the dispatcher records the failure but the run can still meet quorum
}

// IsLocal reports whether the lens runs on the FlexInfer / local tier
// rather than a frontier backend. Used by the dispatcher to apportion
// the run-level USD cap (frontier vs local).
func (l ReviewerLens) IsLocal() bool { return l.Backend == "flexinfer" }

// DispatchOptions tunes a single dispatch call. All fields optional.
type DispatchOptions struct {
	// PerReviewerTimeout caps each reviewer call independently. Zero =
	// no per-call deadline beyond the parent ctx.
	PerReviewerTimeout time.Duration
	// MinQuorum is the smallest number of successful reviewer outputs
	// the dispatcher will accept; below this the call fails. Defaults
	// to len(lenses) (every reviewer must succeed). Set to a smaller
	// value to allow degraded runs (e.g. one frontier reviewer down).
	MinQuorum int
}

// Dispatcher fans Brief out to a slice of Reviewers in parallel, gathers
// their outputs, enforces quorum, and returns a deterministically-ordered
// slice (by lens name) so the editor's prompt is reproducible across
// retries.
type Dispatcher struct {
	Reviewers map[string]Reviewer // keyed by lens name
}

// Dispatch runs one Review call per configured lens. Errors from the
// reviewers themselves are captured in ReviewerOutput.Err; an error
// returned from Dispatch itself means the call failed before reviewer
// dispatch (no policy, quorum impossible, etc.).
func (d *Dispatcher) Dispatch(ctx context.Context, brief *Brief, lenses []ReviewerLens, opts DispatchOptions) ([]ReviewerOutput, error) {
	if d == nil || d.Reviewers == nil {
		return nil, errors.New("council: reviewer dispatcher not configured")
	}
	if brief == nil {
		return nil, errors.New("council: dispatch requires a brief")
	}
	if len(lenses) == 0 {
		return nil, errors.New("council: dispatch needs ≥ 1 lens")
	}
	quorum := opts.MinQuorum
	if quorum <= 0 {
		quorum = len(lenses)
	}
	if quorum > len(lenses) {
		return nil, fmt.Errorf("council: quorum %d > available lenses %d", quorum, len(lenses))
	}

	out := make([]ReviewerOutput, len(lenses))
	var wg sync.WaitGroup
	for i, lens := range lenses {
		i, lens := i, lens
		r, ok := d.Reviewers[lens.Name]
		if !ok {
			out[i] = ReviewerOutput{Lens: lens, Err: fmt.Errorf("no reviewer registered for lens %q", lens.Name)}
			continue
		}
		wg.Add(1)
		go func() {
			defer wg.Done()
			rctx := ctx
			if opts.PerReviewerTimeout > 0 {
				var cancel context.CancelFunc
				rctx, cancel = context.WithTimeout(ctx, opts.PerReviewerTimeout)
				defer cancel()
			}
			res, err := r.Review(rctx, brief, lens)
			if err != nil {
				res.Lens = lens
				res.Err = err
			}
			out[i] = res
		}()
	}
	wg.Wait()

	successes := 0
	for _, o := range out {
		if o.Err == nil {
			successes++
		}
	}
	if successes < quorum {
		return out, fmt.Errorf("council: quorum failure (%d/%d reviewers succeeded; need %d)",
			successes, len(out), quorum)
	}

	// Deterministic order so the editor's prompt is reproducible.
	sort.Slice(out, func(i, j int) bool { return out[i].Lens.Name < out[j].Lens.Name })
	return out, nil
}

// LensesFromPolicy adapts the policy's CouncilEnsemble.Reviewers into
// the dispatcher's lens shape. Skips entries with empty model/backend so
// a half-configured policy fails fast at validate time, not dispatch.
func LensesFromPolicy(p *mills.Policy) []ReviewerLens {
	if p == nil {
		return nil
	}
	out := make([]ReviewerLens, 0, len(p.Council.Ensemble.Reviewers))
	for _, r := range p.Council.Ensemble.Reviewers {
		if r.Model == "" || r.Backend == "" {
			continue
		}
		out = append(out, ReviewerLens{Name: r.Name, Model: r.Model, Backend: r.Backend})
	}
	return out
}

// FakeReviewer is the test double the dispatcher's tests use and the
// dryrun CLI consumes when no real reviewers are wired. Returns a
// canned markdown payload so the editor stage can be exercised without
// any live agent calls.
type FakeReviewer struct {
	Notes      string  // returned as Markdown
	CostUSD    float64 // attributed to ReviewerOutput.CostUSD
	ReturnErr  error   // when non-nil, surfaced via ReviewerOutput.Err
	SimulateMS int     // optional sleep so timeout tests have a target

	// RefocusedCostUSD is the cost FakeReviewer reports per
	// RefocusedReview call (debate rounds 2+). Defaults to CostUSD
	// when zero; override to model cheaper refocused passes in tests.
	RefocusedCostUSD float64
}

// Review implements Reviewer.
func (f *FakeReviewer) Review(ctx context.Context, brief *Brief, lens ReviewerLens) (ReviewerOutput, error) {
	if f.SimulateMS > 0 {
		select {
		case <-time.After(time.Duration(f.SimulateMS) * time.Millisecond):
		case <-ctx.Done():
			return ReviewerOutput{Lens: lens}, ctx.Err()
		}
	}
	if f.ReturnErr != nil {
		return ReviewerOutput{Lens: lens}, f.ReturnErr
	}
	return ReviewerOutput{
		Lens:     lens,
		Markdown: fmt.Sprintf("# %s lens review\n\n%s\n", lens.Name, f.Notes),
		CostUSD:  f.CostUSD,
	}, nil
}

// RefocusedReview implements Refocuser. Echoes the focus areas back in
// the markdown so debate-mode tests can pin per-round payloads.
func (f *FakeReviewer) RefocusedReview(ctx context.Context, brief *Brief, lens ReviewerLens, focusAreas []string) (ReviewerOutput, error) {
	if err := ctx.Err(); err != nil {
		return ReviewerOutput{Lens: lens}, err
	}
	if f.ReturnErr != nil {
		return ReviewerOutput{Lens: lens}, f.ReturnErr
	}
	cost := f.RefocusedCostUSD
	if cost == 0 {
		cost = f.CostUSD
	}
	return ReviewerOutput{
		Lens:     lens,
		Markdown: fmt.Sprintf("# %s lens (refocused: %s)\n\n%s\n", lens.Name, strings.Join(focusAreas, ", "), f.Notes),
		CostUSD:  cost,
	}, nil
}
