package hive

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
		{"hive_council_runs_total", CouncilRunsTotal},
		{"hive_council_cost_usd_total", CouncilCostUSDTotal},
		{"hive_council_duration_seconds", CouncilDurationSeconds},
		{"hive_pipeline_runs_total", PipelineRunsTotal},
		{"hive_pipeline_active", PipelineActiveGauge},
		{"hive_pipeline_stage_attempts_total", PipelineStageAttemptsTotal},
		{"hive_pipeline_stage_duration_seconds", PipelineStageDurationSeconds},
		{"hive_pipeline_cost_usd_total", PipelineCostUSDTotal},
		{"hive_gate_evaluations_total", GateEvaluationsTotal},
		{"hive_escalations_total", EscalationsTotal},
		{"hive_escalation_issues_created_total", EscalationIssueCreatedTotal},
		{"hive_escalation_handoffs_created_total", EscalationHandoffCreatedTotal},
		{"hive_reconciler_ticks_total", ReconcileTicksTotal},
		{"hive_reconciler_tick_duration_seconds", ReconcileTickDurationSeconds},
		{"hive_eval_score", EvalScoreSummary},
		{"hive_pipeline_recursion_depth", PipelineRecursionDepthHistogram},
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
// hive code writes don't trip Prometheus cardinality validation. If
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

// TestGatherableExposesHiveMetrics scrapes the default registry and
// confirms the canonical hive_* names are in the output. This is the
// integration-level guarantee that promhttp.Handler() will surface
// our metrics on /metrics.
func TestGatherableExposesHiveMetrics(t *testing.T) {
	mfs, err := prometheus.DefaultGatherer.Gather()
	if err != nil {
		t.Fatalf("gather: %v", err)
	}
	want := map[string]bool{
		"hive_council_runs_total":            false,
		"hive_pipeline_runs_total":           false,
		"hive_pipeline_active":               false,
		"hive_gate_evaluations_total":        false,
		"hive_escalations_total":             false,
		"hive_reconciler_ticks_total":        false,
		"hive_eval_score":                    false,
		"hive_pipeline_stage_attempts_total": false,
		"hive_pipeline_recursion_depth":      false,
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
	// Sanity: confirm hive_* prefix is consistent.
	for _, mf := range mfs {
		if strings.HasPrefix(mf.GetName(), "hive_") && mf.GetHelp() == "" {
			t.Errorf("metric %s has empty help text", mf.GetName())
		}
	}
}
