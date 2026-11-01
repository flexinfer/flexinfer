package proxy

import (
	"sync"

	"github.com/prometheus/client_golang/prometheus"
)

// flexinfer_jobs_* metrics on the proxy, where jobs are submitted and run.
// The manager exports the per-phase job count and TTL deletions
// (pkg/metrics). model is the job's requested model.
var (
	jobsSubmittedTotal = prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "flexinfer_jobs_submitted_total",
		Help: "Async jobs accepted, by requested model and path.",
	}, []string{"model", "path"})
	jobsFinishedTotal = prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "flexinfer_jobs_finished_total",
		Help: "Async jobs that reached a terminal phase on this replica, by model and phase.",
	}, []string{"model", "phase"})
	jobsRunning = prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Name: "flexinfer_jobs_running",
		Help: "Async jobs this replica is running, by concurrency key (model/<name> or gpu/<shared group>).",
	}, []string{"key"})
	jobsQueueWaitSeconds = prometheus.NewHistogramVec(prometheus.HistogramOpts{
		Name:    "flexinfer_jobs_queue_wait_seconds",
		Help:    "Time from submission to a runner's first claim, by model.",
		Buckets: []float64{0.1, 0.5, 1, 5, 15, 30, 60, 120, 300, 600, 1800, 3600},
	}, []string{"model"})
	jobsRunSeconds = prometheus.NewHistogramVec(prometheus.HistogramOpts{
		Name:    "flexinfer_jobs_run_seconds",
		Help:    "Time from a job's first claim to its terminal phase, including lane cold starts and retries, by model and phase.",
		Buckets: []float64{1, 5, 15, 30, 60, 120, 300, 600, 900, 1800, 3600, 7200},
	}, []string{"model", "phase"})
	jobsRetriesTotal = prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "flexinfer_jobs_retries_total",
		Help: "Job attempts that waited to retry their lane, by model and the error code that made them wait.",
	}, []string{"model", "code"})
	jobsWebhookDeliveriesTotal = prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "flexinfer_jobs_webhook_deliveries_total",
		Help: "Job webhook delivery attempts, by event and result (delivered, failed).",
	}, []string{"event", "result"})

	jobMetricsOnce sync.Once
)

func registerJobMetrics() {
	jobMetricsOnce.Do(func() {
		prometheus.MustRegister(jobsSubmittedTotal, jobsFinishedTotal, jobsRunning, jobsQueueWaitSeconds,
			jobsRunSeconds, jobsRetriesTotal, jobsWebhookDeliveriesTotal)
	})
}
