package gates

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/crb2nu/loom/pkg/mills"
	"github.com/crb2nu/loom/pkg/mills/store"
)

// DefaultRegressionWindow is the look-back the gate uses when
// correlating an Alertmanager fire with a recent mills merge. Spec
// (.loom/91- slice 6.3) defines this as 30 minutes.
const DefaultRegressionWindow = 30 * time.Minute

// AlertEvent is the minimal shape the gate consumes from an
// Alertmanager webhook payload. The webhook handler unpacks the rich
// alertmanager.PostableAlerts into one AlertEvent per alert so the gate
// stays decoupled from the wire format.
type AlertEvent struct {
	Name        string    // labels["alertname"]
	Severity    string    // labels["severity"], lowercased; defaults to "unknown"
	StartsAt    time.Time // when the alert started firing
	Status      string    // "firing" | "resolved" — gate ignores resolved
	Labels      map[string]string
	Annotations map[string]string
}

// RegressionResult is what OnAlert returns: the set of pipeline runs the
// gate correlated against the alert plus a flag for whether the
// auto-revert path was triggered. The webhook handler uses this to
// shape its response body and the audit event payload.
type RegressionResult struct {
	AlertName         string
	Severity          string
	Window            time.Duration
	Correlated        []string // pipeline run ids
	AutoRevert        bool     // true when the policy flag opted in
	AutoRevertPending bool     // gate would have opened MR but feature not shipped
}

// RegressionGate correlates Alertmanager firings with recent mills
// auto-merges. It is invoked from the operator's Alertmanager webhook
// handler — never from the gate library's StageInput evaluator chain,
// because the trigger is asynchronous (a post-merge prod alert) rather
// than a synchronous pipeline stage transition.
type RegressionGate struct {
	Store  *store.Store
	Policy *mills.PolicyManager
	// Window is the look-back. Zero falls back to DefaultRegressionWindow.
	Window time.Duration
	// Now is injectable for deterministic tests. Defaults to time.Now.
	Now func() time.Time
}

// OnAlert is the gate's entry point. The handler converts each
// Alertmanager alert into one AlertEvent and calls OnAlert. The gate:
//  1. Skips resolved alerts (we only care about new fires).
//  2. Lists mills-merged pipeline runs whose EndedAt falls inside
//     [now-Window, now]. The merge stamp lives on the pipeline_runs
//     row; the BacklogItem state transition lags behind it slightly
//     so we use the run row, not the backlog row.
//  3. For each hit: increments RegressionCountTotal{alert,severity},
//     writes an audit event, and (if policy opts in) records that
//     auto-revert would have fired.
//
// Returns the populated result so the handler can surface it back to
// Alertmanager (visible in the alert annotation history) without re-
// querying the store.
func (g *RegressionGate) OnAlert(ctx context.Context, alert AlertEvent) (*RegressionResult, error) {
	if g == nil || g.Store == nil {
		return nil, errors.New("regression: gate not configured")
	}
	res := &RegressionResult{
		AlertName: defaultStr(alert.Name, "unknown"),
		Severity:  normalizeSeverity(alert.Severity),
		Window:    g.window(),
	}
	// Resolved-alert webhooks fire on cleardown. Counting those would
	// double-attribute the regression and falsely inflate the metric.
	if strings.EqualFold(alert.Status, "resolved") {
		return res, nil
	}
	cutoff := g.now().Add(-res.Window)
	// PipelineDone is the terminal "merged successfully to main" state.
	merged, err := g.Store.Pipeline.ListByState(ctx, store.PipelineDone)
	if err != nil {
		return res, fmt.Errorf("regression: list merged: %w", err)
	}
	for _, run := range merged {
		if run.EndedAt == nil || run.EndedAt.Before(cutoff) {
			continue
		}
		res.Correlated = append(res.Correlated, run.ID)
		mills.RegressionCountTotal.WithLabelValues(res.AlertName, res.Severity).Inc()
		g.appendEvent(ctx, run, res)
	}
	if len(res.Correlated) > 0 && g.autoRevertEnabled() {
		res.AutoRevert = true
		// Auto-revert MR-opener ships in a follow-up slice; surface a
		// dedicated counter so the dashboard can show "this would have
		// opened N revert MRs if the feature were live".
		res.AutoRevertPending = true
		mills.RegressionAutoRevertPendingTotal.Add(float64(len(res.Correlated)))
	}
	return res, nil
}

// appendEvent best-effort writes one row into the events table per
// correlated run so the audit log shows which alert triggered which
// regression record. Failures are not propagated — the metric and the
// caller's response already capture the signal.
func (g *RegressionGate) appendEvent(ctx context.Context, run *store.PipelineRun, res *RegressionResult) {
	if g.Store == nil || g.Store.Events == nil {
		return
	}
	payload := map[string]any{
		"alert":          res.AlertName,
		"severity":       res.Severity,
		"window_seconds": int(res.Window.Seconds()),
		"run":            run.ID,
		"backlog":        run.BacklogID,
		"auto_revert":    res.AutoRevert,
	}
	_ = g.Store.Events.Append(ctx, &store.Event{
		Actor:   "regression_gate",
		Kind:    "regression.correlated",
		Payload: payload,
	})
}

func (g *RegressionGate) window() time.Duration {
	if g.Window > 0 {
		return g.Window
	}
	return DefaultRegressionWindow
}

func (g *RegressionGate) now() time.Time {
	if g.Now != nil {
		return g.Now().UTC()
	}
	return time.Now().UTC()
}

func (g *RegressionGate) autoRevertEnabled() bool {
	if g.Policy == nil {
		return false
	}
	p := g.Policy.Current()
	if p == nil {
		return false
	}
	return p.Pipeline.AutoRevertOnRegression
}

func defaultStr(s, fallback string) string {
	if s == "" {
		return fallback
	}
	return s
}

// normalizeSeverity lower-cases severity and collapses unknown values
// to "unknown" so RegressionCountTotal cardinality stays bounded.
// Alertmanager convention emits critical/warning/info/none.
func normalizeSeverity(s string) string {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "critical":
		return "critical"
	case "warning":
		return "warning"
	case "info":
		return "info"
	case "none", "":
		return "none"
	default:
		return "unknown"
	}
}
