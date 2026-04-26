// Package hive's metrics file registers the Prometheus instrumentation
// every observable hive surface emits. The operator's metrics listener
// at /metrics already surfaces Go runtime gauges; this module layers
// hive-specific counters / gauges / histograms on top.
//
// Naming follows Prometheus conventions: hive_<subsystem>_<unit>.
// All metrics live in the default registry so promhttp.Handler() picks
// them up automatically — no operator-side wiring required beyond
// importing this package once.
//
// Counters track cumulative event counts (council runs by outcome,
// pipeline state transitions, gate evaluations, escalations). Gauges
// track current state (active runs by state). Histograms track latency
// (council duration, pipeline duration). The Grafana dashboard in
// platform/gitops/k3s/monitoring/dashboards/loom-hive.json reads these.
//
// Wiring: the runner / reconciler / escalator / council runner each
// import this package and call the Inc/Set/Observe helpers at the
// instrumentation points. The metric definitions live here so a
// dashboard refactor doesn't require touching call sites.
package hive

import (
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
)

// ----- Council metrics -----

var (
	// CouncilRunsTotal counts every council run that reached a terminal
	// state, partitioned by trigger (cron/roadmap/incident/manual) and
	// outcome (success/partial/error/conflict).
	CouncilRunsTotal = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "hive_council_runs_total",
		Help: "Total council runs that reached terminal state, by trigger and outcome.",
	}, []string{"trigger", "outcome"})

	// CouncilCostUSDTotal counts cumulative council cost so dashboards
	// can render $/day or $/run by dividing into CouncilRunsTotal.
	// Separate gauges for frontier vs local would be over-cardinality;
	// per-run cost is on the eval row already.
	CouncilCostUSDTotal = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "hive_council_cost_usd_total",
		Help: "Cumulative council run cost in USD, by trigger.",
	}, []string{"trigger"})

	// CouncilDurationSeconds histograms the wall-clock time per council
	// run. Buckets are tuned for typical council ensembles (10s–10min).
	CouncilDurationSeconds = promauto.NewHistogramVec(prometheus.HistogramOpts{
		Name:    "hive_council_duration_seconds",
		Help:    "Council run wall-clock duration, by trigger.",
		Buckets: []float64{5, 15, 30, 60, 120, 300, 600, 1800, 3600},
	}, []string{"trigger"})
)

// ----- Pipeline metrics -----

var (
	// PipelineRunsTotal counts every pipeline run that reached a
	// terminal state, partitioned by terminal state (done/escalated).
	// Active-runs are tracked by PipelineActiveGauge.
	PipelineRunsTotal = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "hive_pipeline_runs_total",
		Help: "Total pipeline runs that reached terminal state, by terminal state.",
	}, []string{"state"})

	// PipelineActiveGauge is the current count of pipeline runs in any
	// non-terminal state, by state. Useful to render a live "what's
	// happening right now" panel without scanning the events table.
	PipelineActiveGauge = promauto.NewGaugeVec(prometheus.GaugeOpts{
		Name: "hive_pipeline_active",
		Help: "Current pipeline runs in a non-terminal state, by state.",
	}, []string{"state"})

	// PipelineStageAttemptsTotal counts every stage attempt the runner
	// dispatched, partitioned by stage and outcome. Retry rate per
	// stage is the fail/(success+fail) ratio in the dashboard.
	PipelineStageAttemptsTotal = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "hive_pipeline_stage_attempts_total",
		Help: "Pipeline stage attempts, by stage and outcome (success/error/gate_fail).",
	}, []string{"stage", "outcome"})

	// PipelineStageDurationSeconds histograms per-stage wall-clock time.
	// Stage names go in a label so a dashboard can render heatmaps per
	// stage without rolling up.
	PipelineStageDurationSeconds = promauto.NewHistogramVec(prometheus.HistogramOpts{
		Name:    "hive_pipeline_stage_duration_seconds",
		Help:    "Pipeline stage attempt wall-clock duration, by stage.",
		Buckets: []float64{1, 5, 15, 30, 60, 300, 600, 1800, 3600, 7200},
	}, []string{"stage"})

	// PipelineCostUSDTotal counts cumulative spend across all pipeline
	// runs, partitioned by terminal state so dashboards can render
	// "cost per merged item" by dividing.
	PipelineCostUSDTotal = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "hive_pipeline_cost_usd_total",
		Help: "Cumulative pipeline run cost in USD, by terminal state.",
	}, []string{"state"})
)

// ----- Gate metrics -----

var (
	// GateEvaluationsTotal counts every gate evaluation, partitioned by
	// gate name and verdict. Pass-rate per gate is the success ratio in
	// Grafana.
	GateEvaluationsTotal = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "hive_gate_evaluations_total",
		Help: "Gate evaluations, by gate name and outcome (pass/fail/skip).",
	}, []string{"gate", "outcome"})
)

// ----- Escalation metrics -----

var (
	// EscalationsTotal counts every pipeline run that escalated. The
	// label is the escalation reason classification — gate_cap_exceeded,
	// stage_error, integrator_conflict, integrator_alloc_fail. The
	// runner / integrator pass these in.
	EscalationsTotal = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "hive_escalations_total",
		Help: "Pipeline escalations, by classification reason.",
	}, []string{"reason"})

	// EscalationIssueCreatedTotal counts successful GitLab issue
	// creations from the escalator. Dashboards alert on the ratio
	// (escalations - issues_created) to surface escalator outages.
	EscalationIssueCreatedTotal = promauto.NewCounter(prometheus.CounterOpts{
		Name: "hive_escalation_issues_created_total",
		Help: "Successful GitLab issue creations from escalation handler.",
	})

	// EscalationHandoffCreatedTotal counts successful agent_handoff
	// creates. Same alerting pattern as the issue counter.
	EscalationHandoffCreatedTotal = promauto.NewCounter(prometheus.CounterOpts{
		Name: "hive_escalation_handoffs_created_total",
		Help: "Successful agent_handoff_create calls from escalation handler.",
	})
)

// ----- Reconciler metrics -----

var (
	// ReconcileTicksTotal counts reconciler ticks, partitioned by the
	// outcome of the tick (started_one / deferred / skipped / errored
	// / no_op). Helps spot a stuck loop where every tick is "errored".
	ReconcileTicksTotal = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "hive_reconciler_ticks_total",
		Help: "Reconciler ticks, by aggregate outcome.",
	}, []string{"outcome"})

	// ReconcileTickDurationSeconds histograms how long each tick takes.
	// Tail latency above ~5s suggests slow store queries — useful early
	// signal before the operator falls behind.
	ReconcileTickDurationSeconds = promauto.NewHistogram(prometheus.HistogramOpts{
		Name:    "hive_reconciler_tick_duration_seconds",
		Help:    "Reconciler tick wall-clock duration.",
		Buckets: []float64{0.1, 0.25, 0.5, 1, 2, 5, 10, 30, 60},
	})
)

// ----- Eval metrics -----

var (
	// EvalScoreSummary publishes the latest eval score per
	// (subject_kind, rubric) as a summary so Grafana can render the
	// distribution. The runner / attributor calls Observe with the
	// score (range [0,1]) on every score record.
	EvalScoreSummary = promauto.NewSummaryVec(prometheus.SummaryOpts{
		Name:       "hive_eval_score",
		Help:       "Eval score distribution, by subject kind and rubric.",
		Objectives: map[float64]float64{0.5: 0.05, 0.9: 0.01, 0.99: 0.001},
	}, []string{"subject_kind", "rubric"})
)
