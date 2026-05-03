package hive

import (
	"context"
	"fmt"
	"time"
)

// Tier names the spend / concurrency bucket a budget check is against.
type Tier string

const (
	TierCouncil  Tier = "council"
	TierPipeline Tier = "pipeline"
)

// BudgetReader is the read-side surface the budget enforcer needs from the
// canonical store. Defined as an interface so tests can drive the budget
// without spinning up SQLite. The store package's CouncilDAO + PipelineDAO
// each provide the concrete methods; pkg/hive.NewStoreBudgetReader wires
// them up.
type BudgetReader interface {
	CouncilCostSince(ctx context.Context, since time.Time) (float64, error)
	PipelineCostSince(ctx context.Context, since time.Time) (float64, error)
	CouncilRunsSince(ctx context.Context, since time.Time) (int, error)
	PipelineRunsSince(ctx context.Context, since time.Time) (int, error)
	PipelineActiveRuns(ctx context.Context) (int, error)

	// DebateCostSince exposes the *debate-only* slice of spend so
	// future tier-aware caps + HUD telemetry can answer "how much
	// did we spend on Council Debate Mode in the last 24h" without
	// joining through council_runs. Council Debate spend is also
	// rolled into council_runs.cost_*_usd at run completion (via the
	// runner's post-debate stamp), so CouncilCostSince already
	// includes it; DebateCostSince is the narrower projection.
	DebateCostSince(ctx context.Context, since time.Time) (float64, error)
}

// Budget enforces the spend, concurrency, and per-day-count caps captured in
// Policy.Budgets. It is stateless; every Allow call re-queries the store.
//
// Construction takes a *PolicyManager so hot-reloads of the policy file
// immediately tighten or relax caps without restarting callers. For tests,
// pass a manager built with PolicyManagerOptions{SkipWatch:true} or wire a
// fixed *Policy via the PolicyFunc field.
type Budget struct {
	// PolicyFunc is consulted on every check to fetch the current policy.
	// Provided so tests can vary the policy without an fs-backed manager.
	PolicyFunc func() *Policy

	// Reader exposes the canonical store's spend/count queries.
	Reader BudgetReader

	// Now is injectable for deterministic test windows. Defaults to time.Now.
	Now func() time.Time
}

// Decision is the result of a budget check.
type Decision struct {
	Allowed bool
	// Reasons explains every cap that contributed to the verdict. For an
	// allowed decision Reasons is empty unless a soft warning was attached.
	Reasons []string
	// SpentUSD is what the canonical store reported for the rolling day.
	SpentUSD float64
	// RemainingUSD is the day-cap minus spent, floored at zero. Zero when
	// no day cap is configured.
	RemainingUSD float64
}

// NewBudget builds a Budget from a PolicyManager and a BudgetReader.
func NewBudget(mgr *PolicyManager, r BudgetReader) *Budget {
	return &Budget{
		PolicyFunc: func() *Policy { return mgr.Current() },
		Reader:     r,
		Now:        time.Now,
	}
}

// Allow returns whether a new run of the given tier with the given estimated
// USD cost is permitted under the active policy. Costs and run-count caps
// are evaluated against the rolling 24h window ending at b.Now().
func (b *Budget) Allow(ctx context.Context, tier Tier, estimateUSD float64) (Decision, error) {
	if b == nil || b.PolicyFunc == nil || b.Reader == nil {
		return Decision{}, fmt.Errorf("budget: not configured")
	}
	if estimateUSD < 0 {
		return Decision{}, fmt.Errorf("budget: estimateUSD must be >= 0")
	}
	policy := b.PolicyFunc()
	if !policy.IsEnabled() {
		return Decision{Reasons: []string{"hive disabled"}}, nil
	}
	limits, err := tierLimits(policy, tier)
	if err != nil {
		return Decision{}, err
	}

	now := b.now()
	since := now.Add(-24 * time.Hour)

	d := Decision{Allowed: true}

	// (a) per-run cap
	if limits.MaxUSDPerRun > 0 && estimateUSD > limits.MaxUSDPerRun {
		d.Allowed = false
		d.Reasons = append(d.Reasons, fmt.Sprintf("estimate %.2f exceeds %s.max_usd_per_run %.2f",
			estimateUSD, tier, limits.MaxUSDPerRun))
	}

	// (b) per-day spend cap
	spent, err := b.spentSince(ctx, tier, since)
	if err != nil {
		return Decision{}, err
	}
	d.SpentUSD = spent
	if limits.MaxUSDPerDay > 0 {
		d.RemainingUSD = max0(limits.MaxUSDPerDay - spent)
		if spent+estimateUSD > limits.MaxUSDPerDay {
			d.Allowed = false
			d.Reasons = append(d.Reasons, fmt.Sprintf(
				"%s daily cap %.2f reached: spent %.2f + estimate %.2f",
				tier, limits.MaxUSDPerDay, spent, estimateUSD,
			))
		}
	}

	// (c) per-day run-count cap (only meaningful for the pipeline tier today,
	//     but applied consistently for both via the same policy field).
	if limits.MaxRunsPerDay > 0 {
		count, err := b.runsSince(ctx, tier, since)
		if err != nil {
			return Decision{}, err
		}
		if count >= limits.MaxRunsPerDay {
			d.Allowed = false
			d.Reasons = append(d.Reasons, fmt.Sprintf(
				"%s daily run count %d reached cap %d",
				tier, count, limits.MaxRunsPerDay,
			))
		}
	}

	// (d) concurrency cap (pipeline only — the council is single-flight).
	if tier == TierPipeline && limits.MaxConcurrentRuns > 0 {
		active, err := b.Reader.PipelineActiveRuns(ctx)
		if err != nil {
			return Decision{}, err
		}
		if active >= limits.MaxConcurrentRuns {
			d.Allowed = false
			d.Reasons = append(d.Reasons, fmt.Sprintf(
				"pipeline active runs %d reached max_concurrent_runs %d",
				active, limits.MaxConcurrentRuns,
			))
		}
	}

	return d, nil
}

// DebateSpentSince returns the total debate spend across the rolling
// 24h window ending at b.Now(). Informational helper for HUD widgets
// and slice 5.3's "Debate Rounds" panel; does not enforce any cap on
// its own. Returns 0 with nil error when no debate has run yet.
func (b *Budget) DebateSpentSince(ctx context.Context) (float64, error) {
	if b == nil || b.Reader == nil {
		return 0, fmt.Errorf("budget: not configured")
	}
	since := b.now().Add(-24 * time.Hour)
	return b.Reader.DebateCostSince(ctx, since)
}

// Remaining returns the daily USD remaining for the tier, clamped at zero.
// Zero is returned if no day cap is configured. This is informational —
// callers must still consult Allow() before spawning work.
func (b *Budget) Remaining(ctx context.Context, tier Tier) (float64, error) {
	policy := b.PolicyFunc()
	limits, err := tierLimits(policy, tier)
	if err != nil {
		return 0, err
	}
	if limits.MaxUSDPerDay <= 0 {
		return 0, nil
	}
	since := b.now().Add(-24 * time.Hour)
	spent, err := b.spentSince(ctx, tier, since)
	if err != nil {
		return 0, err
	}
	return max0(limits.MaxUSDPerDay - spent), nil
}

func (b *Budget) now() time.Time {
	if b.Now != nil {
		return b.Now()
	}
	return time.Now()
}

func (b *Budget) spentSince(ctx context.Context, tier Tier, since time.Time) (float64, error) {
	switch tier {
	case TierCouncil:
		return b.Reader.CouncilCostSince(ctx, since)
	case TierPipeline:
		return b.Reader.PipelineCostSince(ctx, since)
	}
	return 0, fmt.Errorf("budget: unknown tier %q", tier)
}

func (b *Budget) runsSince(ctx context.Context, tier Tier, since time.Time) (int, error) {
	switch tier {
	case TierCouncil:
		return b.Reader.CouncilRunsSince(ctx, since)
	case TierPipeline:
		return b.Reader.PipelineRunsSince(ctx, since)
	}
	return 0, fmt.Errorf("budget: unknown tier %q", tier)
}

func tierLimits(p *Policy, tier Tier) (BudgetLimits, error) {
	if p == nil {
		return BudgetLimits{}, fmt.Errorf("budget: policy is nil")
	}
	switch tier {
	case TierCouncil:
		return p.Budgets.Council, nil
	case TierPipeline:
		return p.Budgets.Pipeline, nil
	}
	return BudgetLimits{}, fmt.Errorf("budget: unknown tier %q", tier)
}

func max0(x float64) float64 {
	if x < 0 {
		return 0
	}
	return x
}
