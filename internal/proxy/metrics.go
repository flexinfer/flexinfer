package proxy

import (
	"sync"

	"github.com/prometheus/client_golang/prometheus"
)

var (
	requestsTotal = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "flexinfer_proxy_requests_total",
			Help: "Total number of requests processed by the proxy.",
		},
		[]string{"model", "status"},
	)

	scaleUpsTotal = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "flexinfer_proxy_scale_ups_total",
			Help: "Total number of scale-up operations triggered.",
		},
		[]string{"model"},
	)

	// upstreamRetriesTotal counts upstream forward retries triggered by a
	// dial-class failure (stale direct-load target, rolling backend pod). A
	// rising rate for a model points at backend churn or a stale fast-path
	// target the proxy is self-healing.
	upstreamRetriesTotal = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "flexinfer_proxy_upstream_retries_total",
			Help: "Total upstream forward retries after a dial-class failure, by model and reason.",
		},
		[]string{"model", "reason"},
	)

	requestDuration = prometheus.NewHistogramVec(
		prometheus.HistogramOpts{
			Name:    "flexinfer_proxy_request_duration_seconds",
			Help:    "Histogram of request processing duration.",
			Buckets: prometheus.DefBuckets,
		},
		[]string{"model"},
	)

	// Request queue metrics
	queuedRequestsTotal = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "flexinfer_proxy_queued_requests_total",
			Help: "Total number of requests queued during cold start.",
		},
		[]string{"model"},
	)

	queueRejectedTotal = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "flexinfer_proxy_queue_rejected_total",
			Help: "Total number of requests rejected due to full queue.",
		},
		[]string{"model"},
	)

	queueWaitDuration = prometheus.NewHistogramVec(
		prometheus.HistogramOpts{
			Name:    "flexinfer_proxy_queue_wait_duration_seconds",
			Help:    "Time requests spent waiting in queue during cold start.",
			Buckets: []float64{0.1, 0.5, 1, 2, 5, 10, 20, 30, 60},
		},
		[]string{"model"},
	)

	activeConnections = prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Name: "flexinfer_proxy_active_connections",
			Help: "Number of active connections per model.",
		},
		[]string{"model"},
	)

	queueDepth = prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Name: "flexinfer_proxy_queue_depth",
			Help: "Current number of requests waiting in queue per model.",
		},
		[]string{"model"},
	)

	// Endpoint routing metrics
	endpointChangesTotal = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "flexinfer_proxy_endpoint_changes_total",
			Help: "Total number of endpoint changes detected per model and change type.",
		},
		[]string{"model", "change_type"},
	)

	endpointCount = prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Name: "flexinfer_proxy_endpoint_count",
			Help: "Current number of endpoints per model.",
		},
		[]string{"model"},
	)

	endpointRefreshDuration = prometheus.NewHistogram(
		prometheus.HistogramOpts{
			Name:    "flexinfer_proxy_endpoint_refresh_duration_seconds",
			Help:    "Time spent refreshing endpoints.",
			Buckets: []float64{0.001, 0.005, 0.01, 0.05, 0.1, 0.5, 1},
		},
	)

	// Context-aware routing observability metrics
	routingDecisionsTotal = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "flexinfer_proxy_routing_decisions_total",
			Help: "Total routing decisions by model, strategy, key source, and outcome.",
		},
		[]string{"model", "strategy", "key_source", "outcome"},
	)

	routingTargetHitsTotal = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "flexinfer_proxy_routing_target_hits_total",
			Help: "Total routed request hits per model, strategy, and selected target.",
		},
		[]string{"model", "strategy", "target"},
	)

	routingKeyCardinality = prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Name: "flexinfer_proxy_routing_key_cardinality",
			Help: "Approximate unique routing key count observed per model/strategy/source (bounded in-memory set).",
		},
		[]string{"model", "strategy", "key_source"},
	)

	routingKeyCardinalityOverflowTotal = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "flexinfer_proxy_routing_key_cardinality_overflow_total",
			Help: "Total times routing key cardinality tracking reached its in-memory cap per model/strategy/source.",
		},
		[]string{"model", "strategy", "key_source"},
	)

	// Rate limiting metrics
	rateLimitedTotal = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "flexinfer_proxy_rate_limited_total",
			Help: "Total number of requests rejected due to rate limiting.",
		},
		[]string{"model", "scope"},
	)

	proxyShutdownsTotal = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "flexinfer_proxy_shutdowns_total",
			Help: "Total proxy graceful shutdown lifecycle events by result.",
		},
		[]string{"result"},
	)

	proxyShutdownDuration = prometheus.NewHistogram(
		prometheus.HistogramOpts{
			Name:    "flexinfer_proxy_shutdown_duration_seconds",
			Help:    "Duration of proxy graceful shutdown drain attempts.",
			Buckets: []float64{0.1, 0.5, 1, 2, 5, 10, 30, 60, 120, 300, 600},
		},
	)

	// Backoff metrics
	activationRetriesTotal = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "flexinfer_proxy_activation_retries_total",
			Help: "Total number of activation retries per model.",
		},
		[]string{"model"},
	)

	activationRetryWaitDuration = prometheus.NewHistogramVec(
		prometheus.HistogramOpts{
			Name:    "flexinfer_proxy_activation_retry_wait_duration_seconds",
			Help:    "Time spent waiting between activation retries.",
			Buckets: []float64{1, 2, 5, 10, 15, 20, 30, 45, 60},
		},
		[]string{"model"},
	)

	// Activation lifecycle metrics
	activationDurationSeconds = prometheus.NewHistogramVec(
		prometheus.HistogramOpts{
			Name:    "flexinfer_proxy_activation_duration_seconds",
			Help:    "Total time from activation trigger to model Ready.",
			Buckets: []float64{1, 5, 10, 30, 60, 120, 180, 300, 600, 900},
		},
		[]string{"model", "backend", "result"},
	)

	maxTokensClampedTotal = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "flexinfer_proxy_max_tokens_clamped_total",
			Help: "Total inbound requests whose max_tokens was reduced to fit the model's context window.",
		},
		[]string{"model", "reason"},
	)

	// Per-request token-shape histograms. Observed in logUpstreamUsage from the
	// upstream `usage` block, labeled by resolved model so the traffic mix can be
	// read per serving lane. This is the only place the proxy surfaces request
	// shape to Prometheus — the per-request usage *log* line is unreachable in
	// the aggregator (the proxy is pinned to a control-plane node whose pod logs
	// are not scraped), so these metrics are the durable, scrape-reliable path
	// for grounding workload-conditional decisions (e.g. blanket n-gram SD,
	// which is a win on short Q/A but a tax on long-form generation).
	//
	// COVERAGE: non-streaming JSON completions always carry a parseable usage
	// block. Streaming (SSE) completions are observed too, but only when the
	// client requested stream_options.include_usage (so the engine emits a
	// terminal usage chunk; usageSniffingBody captures it as it flows past).
	// Streaming requests without that opt-in are still unobserved — the
	// completionsTotal{stream} counter is the coverage denominator that
	// quantifies any remaining blind spot per lane.
	//
	// Buckets span the LLM token range (16 → 32768) so both short Q/A and
	// long-context lanes land in distinct buckets.
	llmTokenBuckets = []float64{16, 32, 64, 128, 256, 512, 1024, 2048, 4096, 8192, 16384, 32768}

	requestPromptTokens = prometheus.NewHistogramVec(
		prometheus.HistogramOpts{
			Name:    "flexinfer_proxy_request_prompt_tokens",
			Help:    "Histogram of upstream-reported prompt_tokens per request, by resolved model. Non-streaming completions, plus streaming completions that include a terminal usage chunk.",
			Buckets: llmTokenBuckets,
		},
		[]string{"model"},
	)

	requestCompletionTokens = prometheus.NewHistogramVec(
		prometheus.HistogramOpts{
			Name:    "flexinfer_proxy_request_completion_tokens",
			Help:    "Histogram of upstream-reported completion_tokens per request, by resolved model. Non-streaming completions, plus streaming completions that include a terminal usage chunk.",
			Buckets: llmTokenBuckets,
		},
		[]string{"model"},
	)

	// completionsTotal counts successful completion responses by resolved model
	// and whether the client streamed. It is the **coverage denominator** for the
	// token-shape histograms above. Those observe all non-streaming completions
	// plus streaming completions that carry a terminal usage chunk
	// (stream_options.include_usage); the remaining blind spot is streaming
	// requests that omit it. The stream=true share here bounds that gap — a high
	// streaming share whose usage chunks are absent means the histogram-based
	// workload verdict (e.g. blanket n-gram SD) must be read with caution.
	completionsTotal = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "flexinfer_proxy_completions_total",
			Help: "Total successful completion responses by resolved model and stream flag. The stream=true share is the blind spot of the request_*_tokens histograms (non-streaming only).",
		},
		[]string{"model", "stream"},
	)

	// Prefix-cache (APC) effectiveness, in tokens, by resolved model.
	//
	//	rate(flexinfer_proxy_cached_prompt_tokens_total[5m])
	//	  / rate(flexinfer_proxy_prompt_tokens_total[5m])
	//
	// is the windowed share of prompt tokens served from the prefix cache —
	// the durable, app-visible answer to "is APC actually working on this
	// lane". It replaces eyeballing the per-request
	// X-Flexinfer-Prefix-Cache-Hit-Rate header, which carries the engine's
	// *lifetime* ratio and so cannot show a recent regression.
	//
	// Both counters advance only when the engine actually reported
	// prompt_tokens_details.cached_tokens. Keeping the denominator on the
	// same population as the numerator means engines that never report the
	// field (llama.cpp) stay absent rather than pinning the ratio to zero.
	//
	// Recorded on non-streaming completions AND on streaming completions
	// that carry a terminal usage chunk (stream_options.include_usage) —
	// streamed traffic is the majority shape for chat clients, and before
	// this it contributed nothing to prefix-cache observability.
	//
	// NOTE: vLLM reports cached_tokens in whole KV blocks. On hybrid
	// GDN/Mamba lanes the attention block is aligned up to the mamba page
	// size (qwen35-9b: 544 tokens), so prompts shorter than one block
	// always report zero cached tokens. A low ratio on a short-prompt lane
	// is expected, not a fault.
	observedPromptTokensTotal = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "flexinfer_proxy_prompt_tokens_total",
			Help: "Total prompt tokens on completions where the engine reported prefix-cache detail. Denominator for flexinfer_proxy_cached_prompt_tokens_total.",
		},
		[]string{"model"},
	)

	cachedPromptTokensTotal = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "flexinfer_proxy_cached_prompt_tokens_total",
			Help: "Total prompt tokens served from the engine's prefix cache (usage.prompt_tokens_details.cached_tokens), by resolved model. Divide by flexinfer_proxy_prompt_tokens_total for the APC hit share.",
		},
		[]string{"model"},
	)

	stalledLoadTotal = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "flexinfer_proxy_stalled_load_total",
			Help: "Total times the proxy observed a Model's cold-start load as stalled (LoadingSubstage + no LoadingProgressAt advance within threshold).",
		},
		[]string{"model", "substage"},
	)

	admissionDecisionsTotal = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "flexinfer_proxy_admission_decisions_total",
			Help: "Total admission decisions. reason=in_budget|over_budget; allow=true|false. Only present when context-bounded admission is enforced for the model.",
		},
		[]string{"model", "reason", "allow"},
	)

	activationFailuresTotal = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "flexinfer_proxy_activation_failures_total",
			Help: "Total activation failures by model and reason.",
		},
		[]string{"model", "reason"},
	)

	// Label-group routing observability (F4-proxy-prefix-pinning). One counter
	// increment per pickReadyMemberRouted call. `strategy` is the configured
	// mode (round-robin|least-loaded|prefix-or-rr|session-or-rr|
	// prefix-session-or-rr). `outcome` is what actually happened on this call:
	// least_loaded, hashed_prefix, hashed_session, fallback_no_key (mode tried
	// but no key extractable), fallback_single (only one Ready candidate so no
	// choice to make), or default_rr (mode was round-robin or empty).
	labelGroupRouteDecisionsTotal = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "flexinfer_proxy_label_group_route_decisions_total",
			Help: "Label-group routing decisions by label, configured strategy, and per-call outcome.",
		},
		[]string{"label", "strategy", "outcome"},
	)

	labelGroupRouteTargetHitsTotal = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "flexinfer_proxy_label_group_route_target_hits_total",
			Help: "Label-group route selections by label, configured strategy, and selected model.",
		},
		[]string{"label", "strategy", "model"},
	)

	// Least-loaded reservation ledger observability. A reservation is recorded
	// each time pickLeastLoaded steers a request to a member; it counts as load
	// until a real connection consumes it or its TTL
	// (PROXY_LEAST_LOADED_RESERVATION_TTL) elapses. A rising _total with a flat
	// active_connections rate marks a burst the ledger spread; a rising
	// _expired_total marks picks that never became served connections (dropped
	// callers, rejected queue slots, cold-start timeouts).
	leastLoadedReservationsTotal = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "flexinfer_proxy_least_loaded_reservations_total",
			Help: "Total least-loaded pick reservations recorded per model (burst-spread placeholders ahead of the connection gauge).",
		},
		[]string{"model"},
	)

	leastLoadedReservationsExpiredTotal = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "flexinfer_proxy_least_loaded_reservations_expired_total",
			Help: "Total least-loaded reservations that expired before a real connection consumed them, per model.",
		},
		[]string{"model"},
	)
)

var metricsOnce sync.Once

// InitModelMetrics pre-initializes all per-model proxy metrics with zero values
// so Grafana displays "0" instead of "No data" for idle panels.
// Safe to call repeatedly for the same model.
func InitModelMetrics(model string) {
	// Counters: calling WithLabelValues creates the series at 0
	requestsTotal.WithLabelValues(model, "success")
	requestsTotal.WithLabelValues(model, "error")
	scaleUpsTotal.WithLabelValues(model)
	queuedRequestsTotal.WithLabelValues(model)
	queueRejectedTotal.WithLabelValues(model)
	activationRetriesTotal.WithLabelValues(model)

	// Gauges: explicitly set to 0
	activeConnections.WithLabelValues(model).Add(0)
	queueDepth.WithLabelValues(model).Add(0)
	endpointCount.WithLabelValues(model).Add(0)
}

// RegisterMetrics registers all proxy Prometheus metrics. Safe to call multiple times.
func RegisterMetrics() {
	metricsOnce.Do(func() {
		prometheus.MustRegister(requestsTotal)
		prometheus.MustRegister(scaleUpsTotal)
		prometheus.MustRegister(upstreamRetriesTotal)
		prometheus.MustRegister(requestDuration)
		prometheus.MustRegister(queuedRequestsTotal)
		prometheus.MustRegister(queueRejectedTotal)
		prometheus.MustRegister(queueWaitDuration)
		prometheus.MustRegister(activeConnections)
		prometheus.MustRegister(queueDepth)
		prometheus.MustRegister(endpointChangesTotal)
		prometheus.MustRegister(endpointCount)
		prometheus.MustRegister(endpointRefreshDuration)
		prometheus.MustRegister(routingDecisionsTotal)
		prometheus.MustRegister(routingTargetHitsTotal)
		prometheus.MustRegister(routingKeyCardinality)
		prometheus.MustRegister(routingKeyCardinalityOverflowTotal)
		prometheus.MustRegister(activationRetriesTotal)
		prometheus.MustRegister(activationRetryWaitDuration)
		prometheus.MustRegister(activationDurationSeconds)
		prometheus.MustRegister(activationFailuresTotal)
		prometheus.MustRegister(rateLimitedTotal)
		prometheus.MustRegister(proxyShutdownsTotal)
		prometheus.MustRegister(proxyShutdownDuration)
		prometheus.MustRegister(maxTokensClampedTotal)
		prometheus.MustRegister(requestPromptTokens)
		prometheus.MustRegister(requestCompletionTokens)
		prometheus.MustRegister(completionsTotal)
		prometheus.MustRegister(observedPromptTokensTotal)
		prometheus.MustRegister(cachedPromptTokensTotal)
		prometheus.MustRegister(stalledLoadTotal)
		prometheus.MustRegister(admissionDecisionsTotal)
		prometheus.MustRegister(labelGroupRouteDecisionsTotal)
		prometheus.MustRegister(labelGroupRouteTargetHitsTotal)
		prometheus.MustRegister(leastLoadedReservationsTotal)
		prometheus.MustRegister(leastLoadedReservationsExpiredTotal)
	})
}
