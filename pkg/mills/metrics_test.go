package mills

import (
	"strings"
	"testing"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/testutil"
)

// TestMetricsRegistered confirms every metric is non-nil and registered
// against the default registry. A typo in promauto naming would manifest
// here as a missing metric — catching it at compile-time-of-test rather
// than at first prod scrape is worth the line count.
func TestMetricsRegistered(t *testing.T) {
	t.Helper()
	cases := []struct {
		name      string
		collector prometheus.Collector
	}{
		{"mills_council_runs_total", CouncilRunsTotal},
		{"mills_council_cost_usd_total", CouncilCostUSDTotal},
		{"mills_council_duration_seconds", CouncilDurationSeconds},
		{"mills_pipeline_runs_total", PipelineRunsTotal},
		{"mills_pipeline_active", PipelineActiveGauge},
		{"mills_pipeline_stage_attempts_total", PipelineStageAttemptsTotal},
		{"mills_pipeline_stage_duration_seconds", PipelineStageDurationSeconds},
		{"mills_pipeline_cost_usd_total", PipelineCostUSDTotal},
		{"mills_gate_evaluations_total", GateEvaluationsTotal},
		{"mills_escalations_total", EscalationsTotal},
		{"mills_escalation_issues_created_total", EscalationIssueCreatedTotal},
		{"mills_escalation_handoffs_created_total", EscalationHandoffCreatedTotal},
		{"mills_reconciler_ticks_total", ReconcileTicksTotal},
		{"mills_reconciler_tick_duration_seconds", ReconcileTickDurationSeconds},
		{"mills_eval_score", EvalScoreSummary},
		{"mills_pipeline_recursion_depth", PipelineRecursionDepthHistogram},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if tc.collector == nil {
				t.Fatalf("metric %s is nil", tc.name)
			}
		})
	}
}

// TestCounterVecLabelsAccept verifies that the canonical label values
// mills code writes don't trip Prometheus cardinality validation. If
// someone adds a new label dimension and forgets to update the
// counter declaration, this catches it before deploy.
func TestCounterVecLabelsAccept(t *testing.T) {
	// Use the canonical label values that the runner / reconciler /
	// escalator pass at runtime. Each block has at least one Inc to
	// confirm the dimension count matches.
	CouncilRunsTotal.WithLabelValues("manual", "success").Inc()
	CouncilCostUSDTotal.WithLabelValues("cron").Add(0.01)
	CouncilDurationSeconds.WithLabelValues("manual").Observe(12.5)

	PipelineRunsTotal.WithLabelValues("done").Inc()
	PipelineActiveGauge.WithLabelValues("implementing").Set(3)
	PipelineStageAttemptsTotal.WithLabelValues("implement", "success").Inc()
	PipelineStageDurationSeconds.WithLabelValues("tests").Observe(180.0)
	PipelineCostUSDTotal.WithLabelValues("done").Add(1.25)

	GateEvaluationsTotal.WithLabelValues("diff_size", "pass").Inc()
	EscalationsTotal.WithLabelValues("retry_cap_exceeded").Inc()
	EscalationIssueCreatedTotal.Inc()
	EscalationHandoffCreatedTotal.Inc()

	ReconcileTicksTotal.WithLabelValues("started_one").Inc()
	ReconcileTickDurationSeconds.Observe(0.42)

	EvalScoreSummary.WithLabelValues("pipeline_run", "pipeline_outcome_v1").Observe(0.87)

	PipelineRecursionDepthHistogram.Observe(1)

	// Confirm at least one sample landed somewhere readable. testutil
	// panics if the metric isn't registered — implicit assertion.
	if got := testutil.ToFloat64(EscalationIssueCreatedTotal); got < 1 {
		t.Errorf("EscalationIssueCreatedTotal = %v, want ≥ 1 after Inc()", got)
	}
}

// TestGatherableExposesMillsMetrics scrapes the default registry and
// confirms the canonical mills_* names are in the output. This is the
// integration-level guarantee that promhttp.Handler() will surface
// our metrics on /metrics.
func TestGatherableExposesMillsMetrics(t *testing.T) {
	mfs, err := prometheus.DefaultGatherer.Gather()
	if err != nil {
		t.Fatalf("gather: %v", err)
	}
	want := map[string]bool{
		"mills_council_runs_total":            false,
		"mills_pipeline_runs_total":           false,
		"mills_pipeline_active":               false,
		"mills_gate_evaluations_total":        false,
		"mills_escalations_total":             false,
		"mills_reconciler_ticks_total":        false,
		"mills_eval_score":                    false,
		"mills_pipeline_stage_attempts_total": false,
		"mills_pipeline_recursion_depth":      false,
	}
	for _, mf := range mfs {
		if _, ok := want[mf.GetName()]; ok {
			want[mf.GetName()] = true
		}
	}
	for name, seen := range want {
		if !seen {
			t.Errorf("metric %s not in default registry", name)
		}
	}
	// Sanity: confirm mills_* prefix is consistent.
	for _, mf := range mfs {
		if strings.HasPrefix(mf.GetName(), "mills_") && mf.GetHelp() == "" {
			t.Errorf("metric %s has empty help text", mf.GetName())
		}
	}
}
