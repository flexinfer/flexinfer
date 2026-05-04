// Package budget provides the cost-preview estimator used by Phase 7 of the
// Mills v2 rollout. It composes historical squad outcomes (path-class median),
// per-backlog sidecar slice counts, ensemble caps, and the recursion plan
// into a read-only USD estimate that the operator HUD and CLI surface before
// a pipeline run is started.
//
// The estimator is intentionally read-only: it never writes to the canonical
// store and never spawns work. It is safe to call from open (no-token) HTTP
// handlers because it only reads policy + historical aggregates.
package budget

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"

	"github.com/crb2nu/loom/pkg/mills"
	"github.com/crb2nu/loom/pkg/mills/store"
)

// sidecarCostShare is the placeholder factor used to weight each detected
// sidecar slice against the historical median. The exact value is a v1
// approximation — Phase 7 only needs a stable signal so the HUD can show a
// "preview is cheaper / more expensive than typical" affordance. A tighter
// model (e.g. per-slice path-class median) lands once the per-slice outcome
// schema is in place.
const sidecarCostShare = 0.25

// defaultPathClass is the fallback bucket when a backlog item carries no
// `path_class:` label. Its median is computed across every outcome row;
// callers see a wide confidence band when the bucket is empty.
const defaultPathClass = "default"

// pathClassLabelPrefix is the label-key convention for recording the
// path-class on a backlog item. The router writes squad outcomes keyed
// by path_class strings like "go-svc/internal/**", so the same string
// is what the median lookup expects.
const pathClassLabelPrefix = "path_class:"

// sidecarLabelPrefix is the label-key convention for "this backlog item
// carries N sidecar slices". The number of distinct labels with this
// prefix bumps the estimate by sidecarCostShare * median.
const sidecarLabelPrefix = "sidecar:"

// minHighConfidence is the sample-size threshold above which the estimate
// is reported as "high" confidence. <3 samples is "low"; 3..10 is "medium".
const minHighConfidence = 10

// minMediumConfidence is the lower bound for "medium" confidence. Below
// this the estimate is "low"-confidence and callers should treat the
// estimate as a wide prior, not a tight prediction.
const minMediumConfidence = 3

// fallbackEstimateUSD is what the estimator returns when no history exists
// and the policy provides no per-run cap. It is intentionally small so a
// missing-history HUD doesn't claim "this will cost $0".
const fallbackEstimateUSD = 0.5

// CostEstimate is the read-only preview returned by /api/mills/cost-preview.
// Every monetary field is in USD; integer counts are exact.
type CostEstimate struct {
	BacklogID            string  `json:"backlog_id"`
	PathClass            string  `json:"path_class"`
	MedianHistoricalUSD  float64 `json:"median_historical_usd"`
	SidecarSliceCount    int     `json:"sidecar_slice_count"`
	SidecarOverheadUSD   float64 `json:"sidecar_overhead_usd"`
	RecursionOverheadUSD float64 `json:"recursion_overhead_usd"`
	// EstimateUSD is the sum of median + sidecar overhead + recursion
	// overhead, capped at the policy's pipeline.max_usd_per_run so the
	// preview never proposes a value the budget gate would reject.
	EstimateUSD    float64 `json:"estimate_usd"`
	EnsembleCapUSD float64 `json:"ensemble_cap_usd"`
	// CappedByPolicy is true when EstimateUSD was clamped to
	// EnsembleCapUSD. Callers may surface this to operators as a
	// "preview hit the cap" hint.
	CappedByPolicy bool `json:"capped_by_policy"`
	// Confidence is "low", "medium", or "high" depending on sample size.
	Confidence string `json:"confidence"`
	// SampleSize is the number of historical squad_outcomes rows that
	// contributed to the median.
	SampleSize int `json:"sample_size"`
}

// Estimator builds CostEstimates from the canonical store + the live policy.
// PolicyFunc is consulted on every Preview() so hot-reloads of the policy
// file flow through to the next preview without restart.
//
// Both fields must be set before calling Preview; otherwise Preview returns
// an error.
type Estimator struct {
	Store      *store.Store
	PolicyFunc func() *mills.Policy
}

// Preview returns the cost estimate for one backlog item. It is read-only
// and never mutates the store.
//
// Errors:
//   - returns store.ErrNotFound when backlogID is unknown.
//   - returns a wrapped error when the underlying SQL query fails.
//   - returns an error when the estimator is not configured (nil store or
//     nil policy func).
func (e *Estimator) Preview(ctx context.Context, backlogID string) (*CostEstimate, error) {
	if e == nil || e.Store == nil || e.PolicyFunc == nil {
		return nil, errors.New("budget: estimator not configured")
	}
	if strings.TrimSpace(backlogID) == "" {
		return nil, errors.New("budget: backlog id required")
	}

	item, err := e.Store.Backlog.Get(ctx, backlogID)
	if err != nil {
		return nil, err
	}
	policy := e.PolicyFunc()
	pathClass := pathClassFromItem(item)

	median, sample, err := medianCostForPathClass(ctx, e.Store, pathClass)
	if err != nil {
		return nil, fmt.Errorf("budget: median cost: %w", err)
	}

	confidence := classifyConfidence(sample)
	if sample == 0 {
		// No history: seed the median with the per-run cap or a small
		// fallback so the preview is meaningfully non-zero.
		median = fallbackMedian(policy)
	}

	sidecars := countSidecarLabels(item.Labels)
	sidecarOverhead := float64(sidecars) * sidecarCostShare * median

	recursionOverhead := recursionOverheadUSD(policy, median)

	cap := ensembleCapUSD(policy)
	raw := median + sidecarOverhead + recursionOverhead
	estimate, capped := applyCap(raw, cap)

	return &CostEstimate{
		BacklogID:            backlogID,
		PathClass:            pathClass,
		MedianHistoricalUSD:  median,
		SidecarSliceCount:    sidecars,
		SidecarOverheadUSD:   sidecarOverhead,
		RecursionOverheadUSD: recursionOverhead,
		EstimateUSD:          estimate,
		EnsembleCapUSD:       cap,
		CappedByPolicy:       capped,
		Confidence:           confidence,
		SampleSize:           sample,
	}, nil
}

// pathClassFromItem extracts the path_class label from a backlog item, or
// returns defaultPathClass when no such label is present. The match is
// case-insensitive on the prefix and preserves the right-hand value as-is.
func pathClassFromItem(item *store.BacklogItem) string {
	if item == nil {
		return defaultPathClass
	}
	for _, l := range item.Labels {
		trimmed := strings.TrimSpace(l)
		if len(trimmed) <= len(pathClassLabelPrefix) {
			continue
		}
		if strings.EqualFold(trimmed[:len(pathClassLabelPrefix)], pathClassLabelPrefix) {
			val := strings.TrimSpace(trimmed[len(pathClassLabelPrefix):])
			if val != "" {
				return val
			}
		}
	}
	return defaultPathClass
}

// countSidecarLabels returns the number of labels carrying the sidecar:
// prefix. Each one contributes sidecarCostShare * median to the estimate.
func countSidecarLabels(labels []string) int {
	count := 0
	for _, l := range labels {
		trimmed := strings.TrimSpace(l)
		if len(trimmed) <= len(sidecarLabelPrefix) {
			continue
		}
		if strings.EqualFold(trimmed[:len(sidecarLabelPrefix)], sidecarLabelPrefix) {
			count++
		}
	}
	return count
}

// medianCostForPathClass reads every squad_outcomes row matching the path
// class, sorts the cost_usd column, and returns the median + sample size.
// Returns (0, 0, nil) when no rows match.
func medianCostForPathClass(ctx context.Context, st *store.Store, pathClass string) (float64, int, error) {
	rows, err := st.DB().QueryContext(ctx, `
		SELECT cost_usd FROM squad_outcomes
		WHERE path_class = ?
		ORDER BY cost_usd ASC
	`, pathClass)
	if err != nil {
		return 0, 0, err
	}
	defer rows.Close()

	var costs []float64
	for rows.Next() {
		var c float64
		if err := rows.Scan(&c); err != nil {
			return 0, 0, err
		}
		costs = append(costs, c)
	}
	if err := rows.Err(); err != nil {
		return 0, 0, err
	}
	if len(costs) == 0 {
		return 0, 0, nil
	}
	// Already ORDER BY ASC, but sort explicitly to keep the helper
	// honest if the SQL ever changes.
	sort.Float64s(costs)
	return median(costs), len(costs), nil
}

// median returns the middle value of a sorted slice, or the average of the
// two middle values when len is even. Panics on empty slices — callers
// guard with len(costs) == 0.
func median(sorted []float64) float64 {
	n := len(sorted)
	if n == 0 {
		return 0
	}
	mid := n / 2
	if n%2 == 1 {
		return sorted[mid]
	}
	return (sorted[mid-1] + sorted[mid]) / 2.0
}

// classifyConfidence buckets a sample size into "low", "medium", or "high".
// Boundaries are inclusive on the lower end: 0..2 = low, 3..10 = medium,
// >10 = high.
func classifyConfidence(sample int) string {
	switch {
	case sample < minMediumConfidence:
		return "low"
	case sample <= minHighConfidence:
		return "medium"
	default:
		return "high"
	}
}

// fallbackMedian returns a stand-in median when no history exists. We use
// the per-run cap when one is set so the preview is bounded by the same
// rule the budget gate enforces; otherwise a small constant.
func fallbackMedian(p *mills.Policy) float64 {
	if p == nil {
		return fallbackEstimateUSD
	}
	cap := p.Budgets.Pipeline.MaxUSDPerRun
	if cap > 0 {
		// Halve the cap so a no-history preview reads as "below the
		// cap" rather than "at the cap" — leaves headroom for the
		// real run to spend without immediate reject.
		return cap / 2.0
	}
	return fallbackEstimateUSD
}

// recursionOverheadUSD is the worst-case extra spend the recursion plan
// adds. When recursion is disabled or the policy is nil the overhead is
// zero. The formula is intentionally pessimistic: max_depth full sub-runs,
// each at subrun_max_budget_share of the median.
func recursionOverheadUSD(p *mills.Policy, median float64) float64 {
	if p == nil {
		return 0
	}
	if !p.Recursion.Enabled {
		return 0
	}
	depth := float64(p.Recursion.MaxDepth)
	share := p.Recursion.SubrunMaxBudgetShare
	if depth <= 0 || share <= 0 {
		return 0
	}
	return depth * share * median
}

// ensembleCapUSD is the per-run pipeline cap from the policy. Zero when
// no cap is set; applyCap treats zero as "no cap".
func ensembleCapUSD(p *mills.Policy) float64 {
	if p == nil {
		return 0
	}
	return p.Budgets.Pipeline.MaxUSDPerRun
}

// applyCap clamps raw to cap when cap > 0. Returns the clamped value plus
// a bool indicating whether clamping occurred. cap <= 0 is a no-op
// (returns raw, false).
func applyCap(raw, cap float64) (float64, bool) {
	if cap <= 0 {
		return raw, false
	}
	if raw > cap {
		return cap, true
	}
	return raw, false
}
