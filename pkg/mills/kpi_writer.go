package mills

import (
	"context"
	"database/sql"
	"fmt"
	"log/slog"
	"sort"
	"time"

	"github.com/crb2nu/loom/pkg/mills/store"
)

const (
	kpiWindow1d  = 24 * time.Hour
	kpiWindow7d  = 7 * 24 * time.Hour
	kpiWindow30d = 30 * 24 * time.Hour
)

// KPIWriter records rolling snapshots into the canonical kpi_snapshots table.
// It intentionally reads from the store, not Prometheus, so status/HUD/council
// briefs still have local KPI evidence if metrics scraping is degraded.
type KPIWriter struct {
	Store  *store.Store
	Policy *PolicyManager
	Logger *slog.Logger

	// Windows defaults to 1d, 7d, and 30d to match /api/mills/kpis.
	Windows []time.Duration

	// Clock makes snapshot timestamps deterministic in tests.
	Clock func() time.Time
}

// NewKPIWriter returns a writer configured for the REST-supported windows.
func NewKPIWriter(st *store.Store, pm *PolicyManager) *KPIWriter {
	return &KPIWriter{
		Store:   st,
		Policy:  pm,
		Logger:  slog.Default(),
		Windows: []time.Duration{kpiWindow1d, kpiWindow7d, kpiWindow30d},
		Clock:   time.Now,
	}
}

// Record appends one snapshot per configured window.
func (w *KPIWriter) Record(ctx context.Context) error {
	if w == nil || w.Store == nil || w.Store.KPI == nil {
		return fmt.Errorf("kpi writer: store not configured")
	}
	now := w.now().UTC()
	windows := w.Windows
	if len(windows) == 0 {
		windows = []time.Duration{kpiWindow1d, kpiWindow7d, kpiWindow30d}
	}
	for _, window := range windows {
		if window <= 0 {
			return fmt.Errorf("kpi writer: window must be positive")
		}
		snap, err := w.snapshot(ctx, now, window)
		if err != nil {
			return err
		}
		if err := w.Store.KPI.RecordSnapshot(ctx, snap); err != nil {
			return err
		}
	}
	return nil
}

func (w *KPIWriter) snapshot(ctx context.Context, now time.Time, window time.Duration) (*store.KPISnapshot, error) {
	since := now.Add(-window)

	queueDepth, err := countBacklogState(ctx, w.Store, store.BacklogQueued)
	if err != nil {
		return nil, err
	}
	active, err := w.Store.Pipeline.CountActive(ctx)
	if err != nil {
		return nil, err
	}
	councilRuns, err := w.Store.Council.CountSince(ctx, since)
	if err != nil {
		return nil, err
	}
	councilCost, err := w.Store.Council.SumCostSince(ctx, since)
	if err != nil {
		return nil, err
	}
	pipelineRuns, err := w.Store.Pipeline.CountSince(ctx, since)
	if err != nil {
		return nil, err
	}
	pipelineCost, err := w.Store.Pipeline.SumCostSince(ctx, since)
	if err != nil {
		return nil, err
	}
	mergedRuns, err := countPipelineStateSince(ctx, w.Store, store.PipelineDone, since)
	if err != nil {
		return nil, err
	}
	escalatedRuns, err := countPipelineStateSince(ctx, w.Store, store.PipelineEscalated, since)
	if err != nil {
		return nil, err
	}
	gatePass, gateTotal, err := countGateOutcomesSince(ctx, w.Store, since)
	if err != nil {
		return nil, err
	}
	avgEval, err := avgEvalScoreSince(ctx, w.Store, since)
	if err != nil {
		return nil, err
	}

	metrics := map[string]any{
		"policy_enabled":          w.policyEnabled(),
		"queue_depth":             queueDepth,
		"active_pipeline_runs":    active,
		"council_runs":            councilRuns,
		"council_cost_usd":        councilCost,
		"pipeline_runs":           pipelineRuns,
		"pipeline_cost_usd":       pipelineCost,
		"pipeline_merged_runs":    mergedRuns,
		"pipeline_escalated_runs": escalatedRuns,
		"gate_evaluations":        gateTotal,
		"gate_passes":             gatePass,
		"eval_average_score":      avgEval,
	}
	if gateTotal > 0 {
		metrics["gate_pass_rate"] = float64(gatePass) / float64(gateTotal)
	}
	if mergedRuns > 0 {
		// cost_per_merged_change_usd: today equivalent to per-pipeline
		// since Mills doesn't yet aggregate multiple pipeline_runs per
		// backlog item into a single "change". When backlog→change
		// attribution is added (Phase 8+) this should switch to
		// grouping by backlog_id; the metric key is the canonical
		// frontend contract either way.
		metrics["cost_per_merged_pipeline_usd"] = pipelineCost / float64(mergedRuns)
		metrics["cost_per_merged_change_usd"] = pipelineCost / float64(mergedRuns)
	}
	// auto_merge_rate: fraction of terminal runs in the window that
	// reached `done` (vs `escalated`). Mills always uses the
	// auto-merge path so this measures autonomous-success rate, not
	// human vs auto merge selection. Denominator is mergedRuns +
	// escalatedRuns so the metric stays bounded to [0,1] over actual
	// outcomes and complementary with regression_rate below.
	if denom := mergedRuns + escalatedRuns; denom > 0 {
		metrics["auto_merge_rate"] = float64(mergedRuns) / float64(denom)
		// regression_rate: best-effort proxy = escalation_rate. Mills
		// doesn't yet track post-merge regressions; until that signal
		// exists, "rate at which the autonomous pipeline can't carry
		// through" is the closest single-pass signal we have.
		metrics["regression_rate"] = float64(escalatedRuns) / float64(denom)
	}
	// council_roi: merged changes per dollar of council spend. A
	// value > 1 means each $1 of council deliberation produced > 1
	// merged change in the window. Omitted when councilCost <= 0 so
	// the frontend renders "—" rather than divide-by-zero infinity.
	if councilCost > 0 {
		metrics["council_roi"] = float64(mergedRuns) / councilCost
	}
	// slice_to_merge_p50_seconds: median wall-clock from started_at
	// to ended_at over runs that reached state=done in the window.
	// Computed in Go because SQLite lacks a built-in percentile
	// function.
	p50, err := mergedRunDurationP50(ctx, w.Store, since)
	if err != nil {
		return nil, err
	}
	if p50 > 0 {
		metrics["slice_to_merge_p50_seconds"] = p50
	}

	return &store.KPISnapshot{
		SnapshotAt:    now,
		WindowSeconds: int(window.Seconds()),
		Metrics:       metrics,
	}, nil
}

// mergedRunDurationP50 returns the median (started_at → ended_at)
// duration in seconds across runs that reached state=done in the
// window. Returns 0 when there are no completed runs (caller treats
// 0 as "omit the metric").
func mergedRunDurationP50(ctx context.Context, st *store.Store, since time.Time) (float64, error) {
	runs, err := st.Pipeline.ListByStateSince(ctx, store.PipelineDone, since)
	if err != nil {
		return 0, fmt.Errorf("kpi p50 list: %w", err)
	}
	durations := make([]float64, 0, len(runs))
	for _, r := range runs {
		if r == nil || r.EndedAt == nil {
			continue
		}
		d := r.EndedAt.Sub(r.StartedAt).Seconds()
		if d <= 0 {
			continue
		}
		durations = append(durations, d)
	}
	if len(durations) == 0 {
		return 0, nil
	}
	sort.Float64s(durations)
	n := len(durations)
	if n%2 == 1 {
		return durations[n/2], nil
	}
	mid := n / 2
	return (durations[mid-1] + durations[mid]) / 2, nil
}

func (w *KPIWriter) policyEnabled() bool {
	if w == nil || w.Policy == nil || w.Policy.Current() == nil {
		return false
	}
	return w.Policy.Current().IsEnabled()
}

func (w *KPIWriter) now() time.Time {
	if w.Clock != nil {
		return w.Clock()
	}
	return time.Now()
}

func countBacklogState(ctx context.Context, st *store.Store, state store.BacklogState) (int, error) {
	items, err := st.Backlog.ListByState(ctx, state)
	if err != nil {
		return 0, err
	}
	return len(items), nil
}

func countPipelineStateSince(ctx context.Context, st *store.Store, state store.PipelineState, since time.Time) (int, error) {
	row := st.DB().QueryRowContext(ctx, `
		SELECT COUNT(*)
		FROM pipeline_runs
		WHERE state = ? AND started_at >= ?
	`, string(state), kpiTime(since))
	var n int
	if err := row.Scan(&n); err != nil {
		return 0, fmt.Errorf("kpi pipeline state count: %w", err)
	}
	return n, nil
}

func countGateOutcomesSince(ctx context.Context, st *store.Store, since time.Time) (passes, total int, err error) {
	row := st.DB().QueryRowContext(ctx, `
		SELECT
			COALESCE(SUM(CASE WHEN outcome = 'pass' THEN 1 ELSE 0 END), 0),
			COUNT(*)
		FROM gate_outcomes
		WHERE evaluated_at >= ?
	`, kpiTime(since))
	if err := row.Scan(&passes, &total); err != nil {
		return 0, 0, fmt.Errorf("kpi gate outcome count: %w", err)
	}
	return passes, total, nil
}

func avgEvalScoreSince(ctx context.Context, st *store.Store, since time.Time) (any, error) {
	row := st.DB().QueryRowContext(ctx, `
		SELECT AVG(score)
		FROM eval_scores
		WHERE evaluated_at >= ?
	`, kpiTime(since))
	var avg sql.NullFloat64
	if err := row.Scan(&avg); err != nil {
		return nil, fmt.Errorf("kpi eval average: %w", err)
	}
	if !avg.Valid {
		return nil, nil
	}
	return avg.Float64, nil
}

func kpiTime(t time.Time) string {
	return t.UTC().Format(time.RFC3339Nano)
}
