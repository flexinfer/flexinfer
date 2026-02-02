package main

import (
	"bytes"
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"math/rand"
	"net/http"
	"net/http/httputil"
	"net/url"
	"os"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	aiv1alpha1 "github.com/flexinfer/flexinfer/api/v1alpha1"
	aiv1alpha2 "github.com/flexinfer/flexinfer/api/v1alpha2"
	"github.com/flexinfer/flexinfer/backend"
	"github.com/flexinfer/flexinfer/internal/routing"
	"github.com/flexinfer/flexinfer/pkg/validation"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"golang.org/x/sync/singleflight"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/config"
)

var (
	scheme = runtime.NewScheme()

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

	// GPUGroup metrics
	gpuGroupSwapSignalsTotal = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "flexinfer_proxy_gpugroup_swap_signals_total",
			Help: "Total number of swap signals sent to GPUGroup controller.",
		},
		[]string{"gpugroup", "model"},
	)

	gpuGroupQueuedRequestsTotal = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "flexinfer_proxy_gpugroup_queued_requests_total",
			Help: "Total requests queued waiting for GPUGroup model swap.",
		},
		[]string{"gpugroup", "model"},
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
)

// GPUGroup annotation constants (must match gpugroup_controller.go)
const (
	// AnnotationQueueDepthPrefix is set by proxy: flexinfer.ai/queue.{modelName}
	AnnotationQueueDepthPrefix = "flexinfer.ai/queue."
	// AnnotationQueueSincePrefix is when requests started queueing
	AnnotationQueueSincePrefix = "flexinfer.ai/queue-since."
	// AnnotationActiveServiceLabels contains comma-separated service labels for an active model
	AnnotationActiveServiceLabels = "ai.flexinfer/active-services"
	// AnnotationServiceLabels contains comma-separated service labels (static claim)
	AnnotationServiceLabels = "flexinfer.ai/service-labels"
)

func init() {
	utilruntime.Must(clientgoscheme.AddToScheme(scheme))
	utilruntime.Must(aiv1alpha1.AddToScheme(scheme))
	utilruntime.Must(aiv1alpha2.AddToScheme(scheme))

	// Register metrics
	prometheus.MustRegister(requestsTotal)
	prometheus.MustRegister(scaleUpsTotal)
	prometheus.MustRegister(requestDuration)
	prometheus.MustRegister(queuedRequestsTotal)
	prometheus.MustRegister(queueRejectedTotal)
	prometheus.MustRegister(queueWaitDuration)
	prometheus.MustRegister(activeConnections)
	prometheus.MustRegister(queueDepth)
	prometheus.MustRegister(gpuGroupSwapSignalsTotal)
	prometheus.MustRegister(gpuGroupQueuedRequestsTotal)
	prometheus.MustRegister(endpointChangesTotal)
	prometheus.MustRegister(endpointCount)
	prometheus.MustRegister(endpointRefreshDuration)
	prometheus.MustRegister(activationRetriesTotal)
	prometheus.MustRegister(activationRetryWaitDuration)
}

// QueuedRequest represents a request waiting in queue during cold start
type QueuedRequest struct {
	w          http.ResponseWriter
	r          *http.Request
	modelName  string
	done       chan struct{}
	err        error
	enqueuedAt time.Time
	responded  atomic.Bool
}

// RequestQueue holds requests for a model during cold start
type RequestQueue struct {
	model        string
	gpuGroupName string // non-empty if this is a GPUGroup-managed model
	items        chan *QueuedRequest
	created      time.Time
	draining     atomic.Bool
}

type Proxy struct {
	client       client.Client
	namespace    string
	proxyMap     sync.Map           // cache of httputil.ReverseProxy by model name
	requestGroup singleflight.Group // coalescing activation requests

	// Request queues per model during cold start
	queues   sync.Map // map[string]*RequestQueue
	queuesMu sync.Mutex

	// Service label to model name cache
	serviceLabelCache   sync.Map // map[string]string: service label -> model name
	serviceLabelCacheMu sync.Mutex
	lastCacheRefresh    time.Time

	// Configuration (can be overridden by env vars)
	maxQueueSize       int           // Default: 100
	queueTimeout       time.Duration // Default: 60s (how long request can wait in queue)
	coldStartTimeout   time.Duration // Default: 60s (how long to wait for model to become ready)
	connectionTracking sync.Map      // map[string]*int64 for tracking active connections per model

	// Routing for multi-replica models
	router             *routing.Router
	routingEnabled     bool     // Enable advanced routing (session affinity, prefix-based)
	podConnectionCount sync.Map // map[string]*int64 for tracking connections per pod address

	// Request validation
	validateRequests bool // Enable OpenAI request schema validation

	// Endpoint tracking for metrics
	endpointCache sync.Map // map[string][]string - model name -> list of endpoint addresses

	// Backoff configuration for failed activations
	backoffEnabled     bool          // Enable exponential backoff for failed activations
	backoffMaxRetries  int           // Maximum retry attempts (default: 3)
	backoffInitialWait time.Duration // Initial wait time (default: 5s)
	backoffMaxWait     time.Duration // Maximum wait time (default: 30s)
}

func main() {
	var port int
	var logLevel string
	flag.IntVar(&port, "port", 8080, "Port to listen on")
	flag.StringVar(&logLevel, "log-level", "info", "Log level (debug, info, warn, error)")
	flag.Parse()

	// Initialize structured logging
	var level slog.Level
	switch strings.ToLower(logLevel) {
	case "debug":
		level = slog.LevelDebug
	case "warn":
		level = slog.LevelWarn
	case "error":
		level = slog.LevelError
	default:
		level = slog.LevelInfo
	}
	logger := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: level}))
	slog.SetDefault(logger)

	namespace := os.Getenv("POD_NAMESPACE")
	if namespace == "" {
		namespace = "default"
		slog.Warn("POD_NAMESPACE not set, using 'default' namespace (set POD_NAMESPACE env var in production)")
	}

	cfg, err := config.GetConfig()
	if err != nil {
		slog.Error("unable to get kubeconfig", "error", err)
		os.Exit(1)
	}

	k8sClient, err := client.New(cfg, client.Options{Scheme: scheme})
	if err != nil {
		slog.Error("unable to create k8s client", "error", err)
		os.Exit(1)
	}

	// Load configuration from environment variables
	maxQueueSize := getEnvInt("PROXY_MAX_QUEUE_SIZE", 100)
	queueTimeout := getEnvDuration("PROXY_QUEUE_TIMEOUT", 60*time.Second)
	coldStartTimeout := getEnvDuration("PROXY_COLD_START_TIMEOUT", 60*time.Second)
	routingEnabled := getEnvBool("PROXY_ROUTING_ENABLED", true)
	validateRequests := getEnvBool("PROXY_VALIDATE_REQUESTS", false)
	backoffEnabled := getEnvBool("PROXY_BACKOFF_ENABLED", false)
	backoffMaxRetries := getEnvInt("PROXY_BACKOFF_MAX_RETRIES", 3)
	backoffInitialWait := getEnvDuration("PROXY_BACKOFF_INITIAL_WAIT", 5*time.Second)
	backoffMaxWait := getEnvDuration("PROXY_BACKOFF_MAX_WAIT", 30*time.Second)

	p := &Proxy{
		client:             k8sClient,
		namespace:          namespace,
		maxQueueSize:       maxQueueSize,
		queueTimeout:       queueTimeout,
		coldStartTimeout:   coldStartTimeout,
		router:             routing.NewRouter(),
		routingEnabled:     routingEnabled,
		validateRequests:   validateRequests,
		backoffEnabled:     backoffEnabled,
		backoffMaxRetries:  backoffMaxRetries,
		backoffInitialWait: backoffInitialWait,
		backoffMaxWait:     backoffMaxWait,
	}

	// Start queue cleanup goroutine
	go p.cleanupStaleQueues()

	// Start endpoint watcher for routing
	if routingEnabled {
		go p.watchEndpoints(context.Background())
	}

	http.Handle("/metrics", promhttp.Handler())
	http.HandleFunc("/v1/models", p.handleModels)
	http.HandleFunc("/", p.handleRequest)
	http.HandleFunc("/healthz", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		if _, err := w.Write([]byte("ok")); err != nil {
			slog.Warn("healthz write failed", "error", err)
		}
	})

	slog.Info("starting proxy",
		"port", port,
		"namespace", namespace,
		"queue_size", maxQueueSize,
		"queue_timeout", queueTimeout.String(),
		"cold_start_timeout", coldStartTimeout.String(),
		"validate_requests", validateRequests,
		"backoff_enabled", backoffEnabled)
	if err := http.ListenAndServe(fmt.Sprintf(":%d", port), nil); err != nil {
		slog.Error("server failed", "error", err)
		os.Exit(1)
	}
}

// getEnvInt returns an integer from environment variable or default
func getEnvInt(key string, defaultVal int) int {
	if val := os.Getenv(key); val != "" {
		if i, err := strconv.Atoi(val); err == nil {
			return i
		}
	}
	return defaultVal
}

// getEnvDuration returns a duration from environment variable or default
func getEnvDuration(key string, defaultVal time.Duration) time.Duration {
	if val := os.Getenv(key); val != "" {
		if d, err := time.ParseDuration(val); err == nil {
			return d
		}
	}
	return defaultVal
}

// getEnvBool returns a boolean from environment variable or default
func getEnvBool(key string, defaultVal bool) bool {
	if val := os.Getenv(key); val != "" {
		switch strings.ToLower(val) {
		case "true", "1", "yes", "on":
			return true
		case "false", "0", "no", "off":
			return false
		}
	}
	return defaultVal
}

func (p *Proxy) handleRequest(w http.ResponseWriter, r *http.Request) {
	start := time.Now()

	// 1. Extract model name and body from request
	modelName, bodyBytes := p.extractModelNameAndBody(r)
	if modelName == "" {
		validation.WriteBadRequestWithCode(w, "X-Model-ID header, /model/<name> path, or 'model' field in request body required", validation.CodeMissingRequiredField)
		return
	}

	// 2. Validate request if enabled
	if p.validateRequests && len(bodyBytes) > 0 {
		if result := validation.ValidateRequest(r.URL.Path, bodyBytes); result != nil && !result.Valid {
			validation.WriteValidationErrors(w, result)
			return
		}
	}

	ctx := r.Context()

	// 2. Try to resolve service labels (e.g., "textgen" -> "qwen3-8b-fast")
	resolvedName := p.resolveServiceLabel(ctx, modelName)
	if resolvedName != modelName {
		slog.Debug("resolved service label", "label", modelName, "model", resolvedName)
		modelName = resolvedName
	}

	// 3. Fetch the model deployment
	md, err := p.getModelDeployment(ctx, modelName)
	if err == nil {
		// 4. Check if model belongs to a GPUGroup (v1alpha1 only)
		if md.Spec.GPUGroupRef != nil && *md.Spec.GPUGroupRef != "" {
			if err := p.handleGPUGroupRequest(ctx, w, r, modelName, md, start); err != nil {
				slog.Error("GPUGroup request failed", "model", modelName, "error", err)
				requestsTotal.WithLabelValues(modelName, "error").Inc()
			}
			return
		}

		// 5. If model is ready, serve directly (non-GPUGroup path)
		if isReady(md) && (md.Spec.Replicas != nil && *md.Spec.Replicas > 0) {
			p.trackAndServe(w, r, modelName, start)
			return
		}

		// 6. Model is scaled to zero or not ready - use queue
		if err := p.handleColdStart(ctx, w, r, modelName, start); err != nil {
			slog.Error("cold start failed", "model", modelName, "error", err)
			requestsTotal.WithLabelValues(modelName, "error").Inc()
			// Error response already sent by handleColdStart
		}
		return
	}

	if !errors.IsNotFound(err) {
		slog.Error("error fetching model deployment", "model", modelName, "error", err)
		validation.WriteInternalError(w, "Internal error fetching model deployment")
		requestsTotal.WithLabelValues(modelName, "error").Inc()
		return
	}

	// v1alpha2 fallback
	m, err := p.getModel(ctx, modelName)
	if err != nil {
		if errors.IsNotFound(err) {
			validation.WriteModelNotFound(w, modelName)
		} else {
			slog.Error("error fetching model", "model", modelName, "error", err)
			validation.WriteInternalError(w, "Internal error fetching model")
		}
		requestsTotal.WithLabelValues(modelName, "error").Inc()
		return
	}

	// If model is ready, serve directly.
	if m.Status.Phase == aiv1alpha2.ModelPhaseReady {
		p.trackAndServe(w, r, modelName, start)
		return
	}

	// Model is scaled to zero or not ready - use queue.
	if err := p.handleColdStart(ctx, w, r, modelName, start); err != nil {
		slog.Error("cold start failed", "model", modelName, "error", err)
		requestsTotal.WithLabelValues(modelName, "error").Inc()
	}
}

// extractModelName extracts the model name from request headers, path, or body
// extractModelNameAndBody extracts the model name from a request and returns the body bytes.
// The body is restored to the request for downstream handlers.
func (p *Proxy) extractModelNameAndBody(r *http.Request) (string, []byte) {
	var bodyBytes []byte

	// Check X-Model-ID header first
	modelName := r.Header.Get("X-Model-ID")
	if modelName != "" {
		// Still need to read body for validation
		if r.Method == http.MethodPost && strings.Contains(r.Header.Get("Content-Type"), "application/json") {
			bodyBytes, _ = io.ReadAll(r.Body)
			r.Body = io.NopCloser(bytes.NewBuffer(bodyBytes))
			r.ContentLength = int64(len(bodyBytes))
		}
		return modelName, bodyBytes
	}

	// Fallback: Use path prefix /model/<name>/...
	pathParts := strings.Split(strings.TrimPrefix(r.URL.Path, "/"), "/")
	if len(pathParts) > 1 && pathParts[0] == "model" {
		modelName = pathParts[1]
		// Strip the /model/<name> prefix for upstream
		r.URL.Path = "/" + strings.Join(pathParts[2:], "/")
		// Still need to read body for validation
		if r.Method == http.MethodPost && strings.Contains(r.Header.Get("Content-Type"), "application/json") {
			bodyBytes, _ = io.ReadAll(r.Body)
			r.Body = io.NopCloser(bytes.NewBuffer(bodyBytes))
			r.ContentLength = int64(len(bodyBytes))
		}
		return modelName, bodyBytes
	}

	// Fallback: Check JSON Body (OpenAI Standard)
	if r.Method == http.MethodPost && strings.Contains(r.Header.Get("Content-Type"), "application/json") {
		bodyBytes, _ = io.ReadAll(r.Body)
		// Restore body immediately so the proxy can upstream it
		r.Body = io.NopCloser(bytes.NewBuffer(bodyBytes))
		r.ContentLength = int64(len(bodyBytes)) // Update ContentLength for downstream handlers

		// Parse partial JSON to find "model" field
		var payload struct {
			Model string `json:"model"`
		}
		if err := json.Unmarshal(bodyBytes, &payload); err == nil && payload.Model != "" {
			return payload.Model, bodyBytes
		}
	}

	return "", bodyBytes
}

// extractModelName extracts the model name from a request (for backward compatibility).
func (p *Proxy) extractModelName(r *http.Request) string {
	modelName, _ := p.extractModelNameAndBody(r)
	return modelName
}

// getModelDeployment fetches the ModelDeployment resource
func (p *Proxy) getModelDeployment(ctx context.Context, modelName string) (*aiv1alpha1.ModelDeployment, error) {
	md := &aiv1alpha1.ModelDeployment{}
	err := p.client.Get(ctx, client.ObjectKey{Name: modelName, Namespace: p.namespace}, md)
	return md, err
}

// getModel fetches the v1alpha2 Model resource.
func (p *Proxy) getModel(ctx context.Context, modelName string) (*aiv1alpha2.Model, error) {
	m := &aiv1alpha2.Model{}
	err := p.client.Get(ctx, client.ObjectKey{Name: modelName, Namespace: p.namespace}, m)
	return m, err
}

// handleColdStart handles requests when model is scaled to zero
func (p *Proxy) handleColdStart(ctx context.Context, w http.ResponseWriter, r *http.Request, modelName string, start time.Time) error {
	// Get or create queue for this model
	queue := p.getOrCreateQueue(modelName)

	// Create queued request
	qr := &QueuedRequest{
		w:          w,
		r:          r,
		modelName:  modelName,
		done:       make(chan struct{}),
		enqueuedAt: time.Now(),
	}

	// Try to add to queue (non-blocking)
	select {
	case queue.items <- qr:
		// Successfully queued
		queuedRequestsTotal.WithLabelValues(modelName).Inc()
		queueDepth.WithLabelValues(modelName).Inc()
		slog.Debug("request queued", "model", modelName, "queue_depth", len(queue.items))
	default:
		// Queue is full
		queueRejectedTotal.WithLabelValues(modelName).Inc()
		validation.WriteQueueFull(w)
		return fmt.Errorf("queue full for model %s", modelName)
	}

	// Wait for request to be processed with timeout
	timeout := p.queueTimeout
	if coldStartTimeout := p.getColdStartTimeout(ctx, modelName); coldStartTimeout > timeout {
		timeout = coldStartTimeout
	}

	queueCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	select {
	case <-qr.done:
		// Request was processed
		queueWaitDuration.WithLabelValues(modelName).Observe(time.Since(qr.enqueuedAt).Seconds())
		if qr.err != nil {
			return qr.err
		}
		// Success metrics recorded by the processing goroutine
		return nil
	case <-queueCtx.Done():
		// Timeout waiting in queue
		queueWaitDuration.WithLabelValues(modelName).Observe(time.Since(qr.enqueuedAt).Seconds())
		if qr.responded.CompareAndSwap(false, true) {
			validation.WriteColdStartTimeout(w, timeout.String())
		}
		return fmt.Errorf("queue timeout for model %s", modelName)
	}
}

// getOrCreateQueue returns an existing queue or creates a new one
func (p *Proxy) getOrCreateQueue(modelName string) *RequestQueue {
	// Fast path: check if queue exists
	if val, ok := p.queues.Load(modelName); ok {
		return val.(*RequestQueue)
	}

	// Slow path: create new queue (with lock to prevent duplicates)
	p.queuesMu.Lock()
	defer p.queuesMu.Unlock()

	// Double-check after acquiring lock
	if val, ok := p.queues.Load(modelName); ok {
		return val.(*RequestQueue)
	}

	// Create new queue
	queue := &RequestQueue{
		model:   modelName,
		items:   make(chan *QueuedRequest, p.maxQueueSize),
		created: time.Now(),
	}
	p.queues.Store(modelName, queue)

	// Start queue processor (handles scale-up and draining)
	go p.processQueue(queue)

	slog.Info("created request queue", "model", modelName)
	return queue
}

// getOrCreateGPUGroupQueue returns a queue for a GPUGroup-managed model.
// Unlike regular queues, these don't trigger scale-up directly - they wait for the
// GPUGroup controller to make the model active.
func (p *Proxy) getOrCreateGPUGroupQueue(modelName, gpuGroupName string) *RequestQueue {
	// Fast path: check if queue exists
	if val, ok := p.queues.Load(modelName); ok {
		return val.(*RequestQueue)
	}

	// Slow path: create new queue (with lock to prevent duplicates)
	p.queuesMu.Lock()
	defer p.queuesMu.Unlock()

	// Double-check after acquiring lock
	if val, ok := p.queues.Load(modelName); ok {
		return val.(*RequestQueue)
	}

	// Create new GPUGroup-aware queue
	queue := &RequestQueue{
		model:        modelName,
		gpuGroupName: gpuGroupName, // Mark as GPUGroup-managed
		items:        make(chan *QueuedRequest, p.maxQueueSize),
		created:      time.Now(),
	}
	p.queues.Store(modelName, queue)

	// Start GPUGroup queue processor (waits for model to become active)
	go p.processGPUGroupQueue(queue)

	slog.Info("created GPUGroup request queue", "model", modelName, "gpugroup", gpuGroupName)
	return queue
}

// processQueue handles scale-up and drains the queue when model is ready
func (p *Proxy) processQueue(queue *RequestQueue) {
	modelName := queue.model
	ctx := context.Background()

	slog.Info("starting queue processor", "model", modelName)

	// Attempt activation with optional backoff
	var lastErr error
	maxAttempts := 1
	if p.backoffEnabled {
		maxAttempts = p.backoffMaxRetries + 1 // Initial attempt + retries
	}

	for attempt := 0; attempt < maxAttempts; attempt++ {
		if attempt > 0 {
			// Calculate backoff with jitter
			waitTime := p.calculateBackoff(attempt)
			slog.Info("retrying activation with backoff",
				"model", modelName,
				"attempt", attempt+1,
				"max_attempts", maxAttempts,
				"wait", waitTime.String())

			activationRetriesTotal.WithLabelValues(modelName).Inc()
			activationRetryWaitDuration.WithLabelValues(modelName).Observe(waitTime.Seconds())

			time.Sleep(waitTime)
		}

		// Trigger scale-up using singleflight to deduplicate
		_, err, _ := p.requestGroup.Do(modelName+"-scaleup", func() (interface{}, error) {
			return nil, p.triggerScaleUp(ctx, modelName)
		})

		if err != nil {
			lastErr = fmt.Errorf("scale-up failed: %v", err)
			slog.Warn("activation attempt failed", "model", modelName, "attempt", attempt+1, "error", err)
			continue
		}

		// Wait for model to become ready
		if err := p.waitForReady(ctx, modelName); err != nil {
			lastErr = err
			slog.Warn("model failed to become ready", "model", modelName, "attempt", attempt+1, "error", err)
			continue
		}

		// Success!
		lastErr = nil
		break
	}

	if lastErr != nil {
		slog.Error("all activation attempts failed", "model", modelName, "attempts", maxAttempts, "error", lastErr)
		p.drainQueueWithError(queue, lastErr)
		return
	}

	slog.Info("model ready, draining queue", "model", modelName)

	// Mark queue as draining (prevents new requests from being queued here)
	queue.draining.Store(true)

	// Drain queue - serve all pending requests
	p.drainQueue(queue)

	// Clean up queue
	p.queues.Delete(modelName)
	slog.Info("queue processor finished", "model", modelName)
}

// calculateBackoff returns the wait time for a given retry attempt using exponential backoff with jitter.
func (p *Proxy) calculateBackoff(attempt int) time.Duration {
	// Exponential backoff: initialWait * 2^attempt
	backoff := p.backoffInitialWait * time.Duration(1<<uint(attempt))

	// Cap at max wait
	if backoff > p.backoffMaxWait {
		backoff = p.backoffMaxWait
	}

	// Add jitter: random value between 0.5x and 1.5x of the backoff
	jitter := float64(backoff) * (0.5 + rand.Float64())
	return time.Duration(jitter)
}

// processGPUGroupQueue handles queue processing for GPUGroup-managed models.
// Unlike regular queues, it doesn't trigger scale-up directly. Instead:
// 1. The proxy has already signaled demand via GPUGroup annotations
// 2. This processor waits for the GPUGroup controller to make the model active
// 3. Once active and ready, drains the queue
func (p *Proxy) processGPUGroupQueue(queue *RequestQueue) {
	modelName := queue.model
	gpuGroupName := queue.gpuGroupName
	ctx := context.Background()

	slog.Info("starting GPUGroup queue processor", "model", modelName, "gpugroup", gpuGroupName)

	// Wait for model to become active in the GPUGroup and ready
	if err := p.waitForGPUGroupActive(ctx, modelName, gpuGroupName); err != nil {
		slog.Error("model failed to become active in GPUGroup", "model", modelName, "gpugroup", gpuGroupName, "error", err)
		p.drainQueueWithError(queue, err)
		return
	}

	slog.Info("model active in GPUGroup, draining queue", "model", modelName, "gpugroup", gpuGroupName)

	// Mark queue as draining
	queue.draining.Store(true)

	// Drain queue - serve all pending requests
	p.drainQueue(queue)

	// Clear the demand signal since queue is now drained
	go p.clearGPUGroupDemand(context.Background(), gpuGroupName, modelName)

	// Clean up queue
	p.queues.Delete(modelName)
	slog.Info("GPUGroup queue processor finished", "model", modelName)
}

// waitForGPUGroupActive polls until the model is active in the GPUGroup and ready
func (p *Proxy) waitForGPUGroupActive(ctx context.Context, modelName, gpuGroupName string) error {
	// Use longer timeout for GPUGroup swaps (model swap + cold start)
	timeout := p.coldStartTimeout * 2
	if timeout < 120*time.Second {
		timeout = 120 * time.Second
	}

	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	ticker := time.NewTicker(1 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return fmt.Errorf("timeout waiting for model %s to become active in GPUGroup %s (after %v)", modelName, gpuGroupName, timeout)
		case <-ticker.C:
			// Check if model is active in GPUGroup
			gpuGroup, err := p.getGPUGroup(ctx, gpuGroupName)
			if err != nil {
				slog.Warn("error checking GPUGroup", "gpugroup", gpuGroupName, "error", err)
				continue
			}

			if gpuGroup.Status.ActiveModel != modelName {
				// Model is not yet active, keep waiting
				slog.Debug("waiting for model to become active", "model", modelName, "gpugroup", gpuGroupName, "current_active", gpuGroup.Status.ActiveModel)
				continue
			}

			// Model is active, now check if it's ready
			md, err := p.getModelDeployment(ctx, modelName)
			if err != nil {
				slog.Warn("error checking model readiness", "model", modelName, "error", err)
				continue
			}

			if isReady(md) {
				return nil // Model is active and ready
			}

			slog.Debug("model active but not ready", "model", modelName)
		}
	}
}

// triggerScaleUp scales the model to 1 replica
func (p *Proxy) triggerScaleUp(ctx context.Context, modelName string) error {
	md, err := p.getModelDeployment(ctx, modelName)
	if err != nil {
		if !errors.IsNotFound(err) {
			return err
		}

		// v1alpha2: update LastActiveTime to trigger controller scale-up.
		m, err := p.getModel(ctx, modelName)
		if err != nil {
			return err
		}

		now := metav1.Now()
		m.Status.LastActiveTime = &now
		if err := p.client.Status().Update(ctx, m); err != nil {
			if errors.IsConflict(err) {
				return nil
			}
			return fmt.Errorf("failed to update Model lastActiveTime: %w", err)
		}
		return nil
	}

	// Already scaled up?
	if md.Spec.Replicas != nil && *md.Spec.Replicas > 0 {
		return nil
	}

	slog.Info("scaling up model", "model", modelName, "from", 0, "to", 1)
	scaleUpsTotal.WithLabelValues(modelName).Inc()

	// First, update LastAccessTime to prevent the controller from immediately
	// scaling back down due to stale idle timeout.
	// We need to update status first, then spec, to avoid race with controller.
	now := metav1.Now()
	slog.Debug("setting lastAccessTime", "model", modelName, "time", now.Time, "resourceVersion", md.ResourceVersion)
	md.Status.LastAccessTime = &now
	if err := p.client.Status().Update(ctx, md); err != nil {
		slog.Warn("failed to update LastAccessTime before scale-up", "model", modelName, "error", err)
		// Continue anyway - scale-up is more important
	} else {
		slog.Debug("updated lastAccessTime", "model", modelName)
	}

	// Re-fetch to get latest version after status update
	md, err = p.getModelDeployment(ctx, modelName)
	if err != nil {
		return err
	}

	// Check again in case someone else scaled it up
	if md.Spec.Replicas != nil && *md.Spec.Replicas > 0 {
		return nil
	}

	one := int32(1)
	md.Spec.Replicas = &one
	if err := p.client.Update(ctx, md); err != nil {
		if errors.IsConflict(err) {
			// Someone else updated it, that's fine
			return nil
		}
		return fmt.Errorf("failed to scale up: %w", err)
	}

	return nil
}

// waitForReady polls until the model is ready or timeout
func (p *Proxy) waitForReady(ctx context.Context, modelName string) error {
	// Get the cold start timeout, preferring per-model configuration
	timeout := p.getColdStartTimeout(ctx, modelName)

	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	ticker := time.NewTicker(1 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return fmt.Errorf("timeout waiting for model to become ready (after %v)", timeout)
		case <-ticker.C:
			md, err := p.getModelDeployment(ctx, modelName)
			if err == nil {
				if isReady(md) {
					return nil
				}
				continue
			}
			if !errors.IsNotFound(err) {
				slog.Warn("error checking model deployment readiness", "model", modelName, "error", err)
				continue
			}

			m, err := p.getModel(ctx, modelName)
			if err != nil {
				slog.Warn("error checking model readiness", "model", modelName, "error", err)
				continue
			}
			if m.Status.Phase == aiv1alpha2.ModelPhaseReady {
				return nil
			}
		}
	}
}

// getColdStartTimeout returns the cold start timeout for a model.
// Uses per-model ColdStartTimeoutSeconds if specified, otherwise falls back to proxy default.
func (p *Proxy) getColdStartTimeout(ctx context.Context, modelName string) time.Duration {
	md, err := p.getModelDeployment(ctx, modelName)
	if err == nil && md.Spec.ColdStartTimeoutSeconds != nil {
		return time.Duration(*md.Spec.ColdStartTimeoutSeconds) * time.Second
	}
	if err == nil {
		return p.coldStartTimeout
	}
	if !errors.IsNotFound(err) {
		return p.coldStartTimeout
	}

	m, err := p.getModel(ctx, modelName)
	if err == nil &&
		m.Spec.Serverless != nil &&
		m.Spec.Serverless.ColdStartTimeout != nil {
		return m.Spec.Serverless.ColdStartTimeout.Duration
	}
	return p.coldStartTimeout
}

// drainQueue processes all pending requests
func (p *Proxy) drainQueue(queue *RequestQueue) {
	for {
		select {
		case qr := <-queue.items:
			queueDepth.WithLabelValues(queue.model).Dec()
			// Only one goroutine may write a response for a queued request.
			if qr.responded.CompareAndSwap(false, true) {
				start := time.Now()
				p.trackAndServe(qr.w, qr.r, qr.modelName, start)
			}
			close(qr.done)
		default:
			// Queue is empty
			return
		}
	}
}

// drainQueueWithError rejects all pending requests with an error
func (p *Proxy) drainQueueWithError(queue *RequestQueue, err error) {
	queue.draining.Store(true)
	for {
		select {
		case qr := <-queue.items:
			queueDepth.WithLabelValues(queue.model).Dec()
			qr.err = err
			if qr.responded.CompareAndSwap(false, true) {
				validation.WriteActivationFailed(qr.w, fmt.Sprintf("Failed to activate model: %v", err))
			}
			close(qr.done)
		default:
			// Queue is empty
			p.queues.Delete(queue.model)
			return
		}
	}
}

// trackAndServe serves a request while tracking active connections
func (p *Proxy) trackAndServe(w http.ResponseWriter, r *http.Request, modelName string, start time.Time) {
	// Track connection
	p.incrementConnections(modelName)
	defer p.decrementConnections(modelName)

	// Update LastAccessTime (Async)
	go p.updateLastAccess(context.Background(), modelName)

	// Forward Request
	p.serveProxy(w, r, modelName)

	// Metrics update
	requestsTotal.WithLabelValues(modelName, "success").Inc()
	requestDuration.WithLabelValues(modelName).Observe(time.Since(start).Seconds())
}

// incrementConnections atomically increments the connection count
func (p *Proxy) incrementConnections(modelName string) {
	val, _ := p.connectionTracking.LoadOrStore(modelName, new(int64))
	count := val.(*int64)
	atomic.AddInt64(count, 1)
	activeConnections.WithLabelValues(modelName).Inc()
}

// decrementConnections atomically decrements the connection count
func (p *Proxy) decrementConnections(modelName string) {
	if val, ok := p.connectionTracking.Load(modelName); ok {
		count := val.(*int64)
		atomic.AddInt64(count, -1)
		activeConnections.WithLabelValues(modelName).Dec()
	}
}

// GetActiveConnections returns the current connection count for a model
func (p *Proxy) GetActiveConnections(modelName string) int64 {
	if val, ok := p.connectionTracking.Load(modelName); ok {
		return atomic.LoadInt64(val.(*int64))
	}
	return 0
}

// incrementPodConnections atomically increments the connection count for a pod
func (p *Proxy) incrementPodConnections(podAddr string) {
	val, _ := p.podConnectionCount.LoadOrStore(podAddr, new(int64))
	count := val.(*int64)
	atomic.AddInt64(count, 1)
}

// decrementPodConnections atomically decrements the connection count for a pod
func (p *Proxy) decrementPodConnections(podAddr string) {
	if val, ok := p.podConnectionCount.Load(podAddr); ok {
		count := val.(*int64)
		atomic.AddInt64(count, -1)
	}
}

// getPodConnections returns the current connection count for a pod address
func (p *Proxy) getPodConnections(podAddr string) int64 {
	if val, ok := p.podConnectionCount.Load(podAddr); ok {
		return atomic.LoadInt64(val.(*int64))
	}
	return 0
}

// cleanupStaleQueues periodically removes stale queues
func (p *Proxy) cleanupStaleQueues() {
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()

	for range ticker.C {
		now := time.Now()
		p.queues.Range(func(key, value interface{}) bool {
			queue := value.(*RequestQueue)
			// Remove queues older than 2x queue timeout that are empty
			if now.Sub(queue.created) > 2*p.queueTimeout && len(queue.items) == 0 {
				slog.Debug("cleaning up stale queue", "model", key.(string))
				p.queues.Delete(key)
			}
			return true
		})
	}
}

func (p *Proxy) updateLastAccess(ctx context.Context, modelName string) {
	// Optimization: Don't update on every request to avoid API spam.
	// Only update if current LastAccessTime is old (> 1 minute ago).
	// We'll need to fetch the object first.

	md := &aiv1alpha1.ModelDeployment{}
	if err := p.client.Get(ctx, client.ObjectKey{Name: modelName, Namespace: p.namespace}, md); err != nil {
		if !errors.IsNotFound(err) {
			slog.Warn("error fetching modeldeployment for stats update", "model", modelName, "error", err)
			return
		}

		// v1alpha2 fallback: update LastActiveTime
		m := &aiv1alpha2.Model{}
		if err := p.client.Get(ctx, client.ObjectKey{Name: modelName, Namespace: p.namespace}, m); err != nil {
			slog.Warn("error fetching model for stats update", "model", modelName, "error", err)
			return
		}

		if m.Status.LastActiveTime != nil && time.Since(m.Status.LastActiveTime.Time) < 1*time.Minute {
			return
		}

		now := metav1.Now()
		m.Status.LastActiveTime = &now
		if err := p.client.Status().Update(ctx, m); err != nil {
			slog.Debug("failed to update LastActiveTime", "model", modelName, "error", err)
		}
		return
	}

	if md.Status.LastAccessTime != nil && time.Since(md.Status.LastAccessTime.Time) < 1*time.Minute {
		return
	}

	now := metav1.Now()
	// Update status directly
	md.Status.LastAccessTime = &now
	if err := p.client.Status().Update(ctx, md); err != nil {
		// Log but don't fail, it's just stats
		slog.Debug("failed to update LastAccessTime", "model", modelName, "error", err)
	}
}

func (p *Proxy) serveProxy(w http.ResponseWriter, r *http.Request, modelName string) {
	ctx := r.Context()

	// Get the backend port for this model (defaults to 8000 if not found)
	port := p.getBackendPort(ctx, modelName)

	// Get the actual backend model name (e.g., HuggingFace model ID)
	backendModelName := p.getBackendModelName(ctx, modelName)

	// Read body for routing decision and model rewriting
	var bodyBytes []byte
	if r.Body != nil && r.Body != http.NoBody &&
		(r.Method == http.MethodPost || r.Method == http.MethodPut) {
		var err error
		bodyBytes, err = io.ReadAll(r.Body)
		_ = r.Body.Close()
		if err != nil {
			bodyBytes = nil
		}
	}

	// Determine target URL - try routing first, fall back to Service DNS
	var targetURL string
	var targetPod string // Track if we routed to a specific pod
	strategy := p.getRoutingStrategy(ctx, modelName)

	if strategy != routing.StrategyDefault && p.router != nil {
		// Try to route to a specific pod
		// Use RouteWithLoad to support least-loaded routing
		targetPod = p.router.RouteWithLoad(modelName, strategy, r, bodyBytes, p.getPodConnections)
		if targetPod != "" {
			targetURL = fmt.Sprintf("http://%s", targetPod)
			slog.Debug("routing to pod", "model", modelName, "strategy", strategy, "target", targetPod)
		}
	}

	// Fall back to Service DNS if no routing target
	if targetURL == "" {
		targetURL = fmt.Sprintf("http://%s.%s.svc.cluster.local:%d", modelName, p.namespace, port)
	}

	// Rewrite model name in request body if needed
	if backendModelName != "" && len(bodyBytes) > 0 {
		bodyBytes = p.rewriteModelInBody(bodyBytes, backendModelName)
	}

	// Restore body if we read it
	if len(bodyBytes) > 0 {
		r.Body = io.NopCloser(bytes.NewReader(bodyBytes))
		r.ContentLength = int64(len(bodyBytes))
	}

	// Track per-pod connections for least-loaded routing
	if targetPod != "" {
		p.incrementPodConnections(targetPod)
		defer p.decrementPodConnections(targetPod)
	}

	// Create or get cached proxy for this target
	var rp *httputil.ReverseProxy
	proxyKey := targetURL // Use full URL as key for pod-specific proxies
	if val, ok := p.proxyMap.Load(proxyKey); ok {
		rp = val.(*httputil.ReverseProxy)
	} else {
		// Create new proxy
		u, _ := url.Parse(targetURL)
		rp = httputil.NewSingleHostReverseProxy(u)
		p.proxyMap.Store(proxyKey, rp)
	}

	rp.ServeHTTP(w, r)
}

// getBackendModelName returns the actual model identifier used by the backend (e.g., HuggingFace model ID).
// This allows the proxy to rewrite model names in requests before forwarding.
func (p *Proxy) getBackendModelName(ctx context.Context, modelName string) string {
	md := &aiv1alpha1.ModelDeployment{}
	if err := p.client.Get(ctx, client.ObjectKey{Name: modelName, Namespace: p.namespace}, md); err == nil {
		// Return the model spec (e.g., "Qwen/Qwen2.5-7B-Instruct")
		return md.Spec.Model
	} else if !errors.IsNotFound(err) {
		return ""
	}

	// v1alpha2 fallback
	m := &aiv1alpha2.Model{}
	if err := p.client.Get(ctx, client.ObjectKey{Name: modelName, Namespace: p.namespace}, m); err != nil {
		return ""
	}
	return extractModelFromSource(m.Spec.Source)
}

func extractModelFromSource(source string) string {
	switch {
	case strings.HasPrefix(source, "HF://"):
		return strings.TrimPrefix(source, "HF://")
	case strings.HasPrefix(source, "ollama://"):
		return strings.TrimPrefix(source, "ollama://")
	case strings.HasPrefix(source, "file://"):
		return strings.TrimPrefix(source, "file://")
	case strings.HasPrefix(source, "pvc://"):
		rest := strings.TrimPrefix(source, "pvc://")
		parts := strings.SplitN(rest, "/", 2)
		if len(parts) == 2 {
			return "/" + parts[1]
		}
		return ""
	default:
		return source
	}
}

// getBackendPort returns the port for a model's backend service.
// Returns the backend-specific port based on model spec, or 8000 as default.
func (p *Proxy) getBackendPort(ctx context.Context, modelName string) int32 {
	md := &aiv1alpha1.ModelDeployment{}
	if err := p.client.Get(ctx, client.ObjectKey{Name: modelName, Namespace: p.namespace}, md); err == nil {
		if b, ok := backend.Get(md.Spec.Backend); ok {
			return b.Port()
		}
		return 8000
	} else if !errors.IsNotFound(err) {
		return 8000
	}

	m := &aiv1alpha2.Model{}
	if err := p.client.Get(ctx, client.ObjectKey{Name: modelName, Namespace: p.namespace}, m); err != nil {
		return 8000
	}
	if b, ok := backend.Get(m.Spec.Backend); ok {
		return b.Port()
	}
	return 8000
}

// rewriteModelInBody replaces the "model" field in a JSON request body with the backend model name.
// This allows clients to use FlexInfer model names/aliases while backends receive their native model IDs.
func (p *Proxy) rewriteModelInBody(body []byte, backendModelName string) []byte {
	// Parse the JSON
	var data map[string]interface{}
	if err := json.Unmarshal(body, &data); err != nil {
		// Not valid JSON or parse error, return original
		return body
	}

	// Check if there's a model field
	if _, ok := data["model"]; !ok {
		return body
	}

	// Replace the model field with the backend model name
	data["model"] = backendModelName

	// Re-marshal
	modified, err := json.Marshal(data)
	if err != nil {
		return body
	}

	return modified
}

func isReady(md *aiv1alpha1.ModelDeployment) bool {
	for _, cond := range md.Status.Conditions {
		if cond.Type == aiv1alpha1.ConditionTypeReady && cond.Status == metav1.ConditionTrue {
			return true
		}
	}
	return false
}

// handleGPUGroupRequest handles requests for models managed by a GPUGroup.
// If the model is active and ready, serves directly. Otherwise queues and signals demand.
func (p *Proxy) handleGPUGroupRequest(ctx context.Context, w http.ResponseWriter, r *http.Request,
	modelName string, md *aiv1alpha1.ModelDeployment, start time.Time) error {

	gpuGroupName := *md.Spec.GPUGroupRef

	// Fetch the GPUGroup
	gpuGroup, err := p.getGPUGroup(ctx, gpuGroupName)
	if err != nil {
		if errors.IsNotFound(err) {
			// GPUGroup not found - fall back to normal cold start behavior
			slog.Warn("GPUGroup not found, falling back to direct handling", "gpugroup", gpuGroupName, "model", modelName)
			return p.handleColdStart(ctx, w, r, modelName, start)
		}
		validation.WriteInternalError(w, fmt.Sprintf("Error fetching GPUGroup: %v", err))
		return err
	}

	// Check if this model is the active model and ready
	if gpuGroup.Status.ActiveModel == modelName && isReady(md) {
		// Model is active in the group and ready - serve directly
		p.trackAndServe(w, r, modelName, start)
		return nil
	}

	// Model is not active OR not ready - queue and signal demand
	slog.Debug("model not active in GPUGroup, queuing request",
		"model", modelName, "gpugroup", gpuGroupName, "active_model", gpuGroup.Status.ActiveModel)

	gpuGroupQueuedRequestsTotal.WithLabelValues(gpuGroupName, modelName).Inc()

	// Queue the request (similar to cold start, but with GPUGroup signaling)
	if err := p.handleGPUGroupColdStart(ctx, w, r, modelName, gpuGroupName, start); err != nil {
		return err
	}

	return nil
}

// getGPUGroup fetches a GPUGroup resource
func (p *Proxy) getGPUGroup(ctx context.Context, name string) (*aiv1alpha1.GPUGroup, error) {
	gpuGroup := &aiv1alpha1.GPUGroup{}
	err := p.client.Get(ctx, client.ObjectKey{Name: name, Namespace: p.namespace}, gpuGroup)
	return gpuGroup, err
}

// handleGPUGroupColdStart handles cold start for GPUGroup-managed models.
// It queues the request and signals demand to the GPUGroup controller via annotations.
func (p *Proxy) handleGPUGroupColdStart(ctx context.Context, w http.ResponseWriter, r *http.Request,
	modelName string, gpuGroupName string, start time.Time) error {

	// Get or create queue for this model (GPUGroup-aware)
	queue := p.getOrCreateGPUGroupQueue(modelName, gpuGroupName)

	// Create queued request
	qr := &QueuedRequest{
		w:          w,
		r:          r,
		modelName:  modelName,
		done:       make(chan struct{}),
		enqueuedAt: time.Now(),
	}

	// Try to add to queue (non-blocking)
	select {
	case queue.items <- qr:
		// Successfully queued
		queuedRequestsTotal.WithLabelValues(modelName).Inc()
		queueDepth.WithLabelValues(modelName).Inc()
		currentDepth := len(queue.items)
		slog.Debug("request queued for GPUGroup model", "model", modelName, "queue_depth", currentDepth)

		// Signal demand to GPUGroup controller via annotations
		go p.signalGPUGroupDemand(context.Background(), gpuGroupName, modelName, currentDepth)
	default:
		// Queue is full
		queueRejectedTotal.WithLabelValues(modelName).Inc()
		validation.WriteQueueFull(w)
		return fmt.Errorf("queue full for model %s", modelName)
	}

	// Wait for request to be processed with timeout
	queueCtx, cancel := context.WithTimeout(ctx, p.queueTimeout)
	defer cancel()

	select {
	case <-qr.done:
		// Request was processed
		queueWaitDuration.WithLabelValues(modelName).Observe(time.Since(qr.enqueuedAt).Seconds())
		if qr.err != nil {
			return qr.err
		}
		return nil
	case <-queueCtx.Done():
		// Timeout waiting in queue
		queueWaitDuration.WithLabelValues(modelName).Observe(time.Since(qr.enqueuedAt).Seconds())
		if qr.responded.CompareAndSwap(false, true) {
			validation.WriteGPUGroupTimeout(w, p.queueTimeout.String())
		}
		return fmt.Errorf("queue timeout for GPUGroup model %s", modelName)
	}
}

// signalGPUGroupDemand updates GPUGroup annotations to signal demand for a model.
// The GPUGroup controller watches these annotations to trigger model swaps.
func (p *Proxy) signalGPUGroupDemand(ctx context.Context, gpuGroupName, modelName string, queueDepthVal int) {
	queueKey := AnnotationQueueDepthPrefix + modelName
	sinceKey := AnnotationQueueSincePrefix + modelName

	// Use patch to avoid conflicts
	patch := client.RawPatch(types.MergePatchType, []byte(fmt.Sprintf(`{
		"metadata": {
			"annotations": {
				%q: %q,
				%q: %q
			}
		}
	}`, queueKey, strconv.Itoa(queueDepthVal), sinceKey, time.Now().Format(time.RFC3339))))

	gpuGroup := &aiv1alpha1.GPUGroup{
		ObjectMeta: metav1.ObjectMeta{
			Name:      gpuGroupName,
			Namespace: p.namespace,
		},
	}

	if err := p.client.Patch(ctx, gpuGroup, patch); err != nil {
		slog.Warn("failed to signal GPUGroup demand", "gpugroup", gpuGroupName, "model", modelName, "error", err)
		return
	}

	gpuGroupSwapSignalsTotal.WithLabelValues(gpuGroupName, modelName).Inc()
	slog.Debug("signaled demand to GPUGroup", "gpugroup", gpuGroupName, "model", modelName, "queue_depth", queueDepthVal)
}

// clearGPUGroupDemand clears the queue annotations when queue is drained
func (p *Proxy) clearGPUGroupDemand(ctx context.Context, gpuGroupName, modelName string) {
	queueKey := AnnotationQueueDepthPrefix + modelName
	sinceKey := AnnotationQueueSincePrefix + modelName

	// Use patch to remove annotations (set to null)
	patch := client.RawPatch(types.MergePatchType, []byte(fmt.Sprintf(`{
		"metadata": {
			"annotations": {
				%q: null,
				%q: null
			}
		}
	}`, queueKey, sinceKey)))

	gpuGroup := &aiv1alpha1.GPUGroup{
		ObjectMeta: metav1.ObjectMeta{
			Name:      gpuGroupName,
			Namespace: p.namespace,
		},
	}

	if err := p.client.Patch(ctx, gpuGroup, patch); err != nil {
		slog.Warn("failed to clear GPUGroup demand", "gpugroup", gpuGroupName, "model", modelName, "error", err)
		return
	}

	slog.Debug("cleared demand signal from GPUGroup", "gpugroup", gpuGroupName, "model", modelName)
}

// resolveServiceLabel resolves a service label to an actual model name.
// Returns the model name if the label was resolved, or the original input if no mapping found.
func (p *Proxy) resolveServiceLabel(ctx context.Context, labelOrModelName string) string {
	// First check cache
	if modelName, ok := p.serviceLabelCache.Load(labelOrModelName); ok {
		return modelName.(string)
	}

	// Refresh cache if stale (>5 seconds old) or first time
	p.refreshServiceLabelCache(ctx)

	// Check cache again after refresh
	if modelName, ok := p.serviceLabelCache.Load(labelOrModelName); ok {
		return modelName.(string)
	}

	// Not a service label, return as-is (it's probably a model name)
	return labelOrModelName
}

// refreshServiceLabelCache updates the service label to model name mapping.
// It scans all Services in the namespace for the AnnotationActiveServiceLabels annotation.
// Detects and warns about conflicts when multiple services claim the same label.
func (p *Proxy) refreshServiceLabelCache(ctx context.Context) {
	p.serviceLabelCacheMu.Lock()
	defer p.serviceLabelCacheMu.Unlock()

	// Skip if recently refreshed
	if time.Since(p.lastCacheRefresh) < 5*time.Second {
		return
	}

	// List all Services in the namespace
	var services corev1.ServiceList
	if err := p.client.List(ctx, &services, client.InNamespace(p.namespace)); err != nil {
		slog.Warn("failed to list services for label cache refresh", "error", err)
		return
	}

	// First pass: collect all label claims to detect conflicts
	// Prefer AnnotationActiveServiceLabels when present (GPUGroup active model),
	// otherwise fall back to AnnotationServiceLabels (static claim).
	labelClaims := make(map[string][]string) // label -> []serviceName
	for _, svc := range services.Items {
		labels := ""
		if svc.Annotations != nil {
			if active, ok := svc.Annotations[AnnotationActiveServiceLabels]; ok && active != "" {
				labels = active
			} else if static, ok := svc.Annotations[AnnotationServiceLabels]; ok && static != "" {
				labels = static
			}
		}
		if labels == "" {
			continue
		}
		for _, label := range strings.Split(labels, ",") {
			label = strings.TrimSpace(label)
			if label != "" {
				labelClaims[label] = append(labelClaims[label], svc.Name)
			}
		}
	}

	// Clear the cache
	p.serviceLabelCache = sync.Map{}

	// Second pass: build cache and warn on conflicts
	for label, claimants := range labelClaims {
		if len(claimants) > 1 {
			slog.Warn("serviceLabel claimed by multiple services",
				"label", label, "services", claimants, "using", claimants[0])
		}
		// Use first claimant (deterministic based on k8s list order)
		p.serviceLabelCache.Store(label, claimants[0])
		slog.Debug("service label cache updated", "label", label, "model", claimants[0])
	}

	p.lastCacheRefresh = time.Now()
}

// Routing annotation for models
const (
	AnnotationRouting = "flexinfer.ai/routing"
)

// watchEndpoints periodically updates the router with current pod endpoints for each model.
// This enables direct pod routing for session affinity and prefix-based routing.
func (p *Proxy) watchEndpoints(ctx context.Context) {
	ticker := time.NewTicker(10 * time.Second)
	defer ticker.Stop()

	slog.Info("starting endpoint watcher for routing")

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			p.refreshEndpoints(ctx)
		}
	}
}

// refreshEndpoints updates the router with current endpoints for models that have routing enabled.
// Only models with the flexinfer.ai/routing annotation will get direct pod routing;
// others will use Kubernetes Service DNS for load balancing.
func (p *Proxy) refreshEndpoints(ctx context.Context) {
	start := time.Now()
	defer func() {
		endpointRefreshDuration.Observe(time.Since(start).Seconds())
	}()

	// List all Services in namespace (each model has a service)
	var services corev1.ServiceList
	if err := p.client.List(ctx, &services, client.InNamespace(p.namespace)); err != nil {
		slog.Warn("failed to list services for endpoint refresh", "error", err)
		return
	}

	for _, svc := range services.Items {
		// Only process services that look like model services
		if svc.Spec.Selector == nil {
			continue
		}
		modelName, hasModelLabel := svc.Spec.Selector["flexinfer.ai/model"]
		if !hasModelLabel {
			continue
		}

		// Check if this model has routing annotation enabled
		// Only models with explicit routing strategy will get direct pod routing
		hasRoutingAnnotation := p.modelHasRoutingAnnotation(ctx, modelName)
		if !hasRoutingAnnotation {
			// Remove from router if previously added, so it falls back to Service DNS
			p.router.RemoveModel(modelName)
			// Clear endpoint cache and metrics for this model
			if _, existed := p.endpointCache.LoadAndDelete(modelName); existed {
				endpointCount.DeleteLabelValues(modelName)
			}
			continue
		}

		// List endpoints for this service
		var endpoints corev1.Endpoints
		if err := p.client.Get(ctx, client.ObjectKey{Name: svc.Name, Namespace: p.namespace}, &endpoints); err != nil {
			continue
		}

		// Collect ready pod addresses
		var podAddresses []string
		for _, subset := range endpoints.Subsets {
			port := int32(8000) // default
			for _, p := range subset.Ports {
				port = p.Port
				break
			}
			for _, addr := range subset.Addresses {
				podAddresses = append(podAddresses, fmt.Sprintf("%s:%d", addr.IP, port))
			}
		}

		// Track endpoint changes for metrics
		p.trackEndpointChanges(modelName, podAddresses)

		// Update router if we have endpoints
		if len(podAddresses) > 0 {
			p.router.UpdateEndpoints(modelName, podAddresses)
			slog.Debug("updated routing endpoints", "model", modelName, "endpoints", len(podAddresses))
		}
	}
}

// trackEndpointChanges compares current endpoints with cached ones and updates metrics.
func (p *Proxy) trackEndpointChanges(modelName string, newEndpoints []string) {
	// Update endpoint count gauge
	endpointCount.WithLabelValues(modelName).Set(float64(len(newEndpoints)))

	// Get previous endpoints from cache
	var oldEndpoints []string
	if cached, ok := p.endpointCache.Load(modelName); ok {
		oldEndpoints = cached.([]string)
	}

	// Create sets for comparison
	oldSet := make(map[string]bool, len(oldEndpoints))
	for _, ep := range oldEndpoints {
		oldSet[ep] = true
	}
	newSet := make(map[string]bool, len(newEndpoints))
	for _, ep := range newEndpoints {
		newSet[ep] = true
	}

	// Count additions
	for ep := range newSet {
		if !oldSet[ep] {
			endpointChangesTotal.WithLabelValues(modelName, "added").Inc()
		}
	}

	// Count removals
	for ep := range oldSet {
		if !newSet[ep] {
			endpointChangesTotal.WithLabelValues(modelName, "removed").Inc()
		}
	}

	// Store updated cache
	p.endpointCache.Store(modelName, newEndpoints)
}

// modelHasRoutingAnnotation checks if a model has the flexinfer.ai/routing annotation set.
func (p *Proxy) modelHasRoutingAnnotation(ctx context.Context, modelName string) bool {
	// Check v1alpha1 ModelDeployment
	md := &aiv1alpha1.ModelDeployment{}
	if err := p.client.Get(ctx, client.ObjectKey{Name: modelName, Namespace: p.namespace}, md); err == nil {
		if md.Annotations != nil {
			if _, ok := md.Annotations[AnnotationRouting]; ok {
				return true
			}
		}
		return false
	}

	// Check v1alpha2 Model
	m := &aiv1alpha2.Model{}
	if err := p.client.Get(ctx, client.ObjectKey{Name: modelName, Namespace: p.namespace}, m); err == nil {
		if m.Annotations != nil {
			if _, ok := m.Annotations[AnnotationRouting]; ok {
				return true
			}
		}
	}

	return false
}

// getRoutingStrategy returns the routing strategy for a model from its annotation.
func (p *Proxy) getRoutingStrategy(ctx context.Context, modelName string) routing.Strategy {
	if !p.routingEnabled {
		return routing.StrategyDefault
	}

	// Check v1alpha1 ModelDeployment
	md := &aiv1alpha1.ModelDeployment{}
	if err := p.client.Get(ctx, client.ObjectKey{Name: modelName, Namespace: p.namespace}, md); err == nil {
		if md.Annotations != nil {
			if strategy, ok := md.Annotations[AnnotationRouting]; ok {
				return routing.Strategy(strategy)
			}
		}
		return routing.StrategyDefault
	}

	// Check v1alpha2 Model
	m := &aiv1alpha2.Model{}
	if err := p.client.Get(ctx, client.ObjectKey{Name: modelName, Namespace: p.namespace}, m); err == nil {
		if m.Annotations != nil {
			if strategy, ok := m.Annotations[AnnotationRouting]; ok {
				return routing.Strategy(strategy)
			}
		}
	}

	return routing.StrategyDefault
}

// OpenAI-compatible model types for /v1/models endpoint

// OpenAIModel represents a model in OpenAI API format
type OpenAIModel struct {
	ID       string                 `json:"id"`
	Object   string                 `json:"object"`
	Created  int64                  `json:"created"`
	OwnedBy  string                 `json:"owned_by"`
	Metadata map[string]interface{} `json:"metadata,omitempty"`
}

// OpenAIModelsResponse is the response format for /v1/models
type OpenAIModelsResponse struct {
	Object string        `json:"object"`
	Data   []OpenAIModel `json:"data"`
}

// handleModels returns OpenAI-compatible list of available models
func (p *Proxy) handleModels(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		validation.WriteMethodNotAllowed(w, r.Method)
		return
	}

	ctx := r.Context()
	var models []OpenAIModel

	// List ModelDeployments (v1alpha1)
	var mds aiv1alpha1.ModelDeploymentList
	if err := p.client.List(ctx, &mds, client.InNamespace(p.namespace)); err != nil {
		slog.Warn("error listing ModelDeployments", "error", err)
	} else {
		for _, md := range mds.Items {
			models = append(models, p.modelDeploymentToOpenAI(&md))
		}
	}

	// List Models (v1alpha2)
	var ms aiv1alpha2.ModelList
	if err := p.client.List(ctx, &ms, client.InNamespace(p.namespace)); err != nil {
		slog.Warn("error listing Models", "error", err)
	} else {
		for _, m := range ms.Items {
			models = append(models, p.modelToOpenAI(&m))
		}
	}

	response := OpenAIModelsResponse{
		Object: "list",
		Data:   models,
	}

	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(response); err != nil {
		slog.Warn("error encoding models response", "error", err)
	}
}

// modelDeploymentToOpenAI converts a ModelDeployment to OpenAI model format
func (p *Proxy) modelDeploymentToOpenAI(md *aiv1alpha1.ModelDeployment) OpenAIModel {
	// Determine readiness
	ready := isReady(md)
	replicas := int32(0)
	if md.Spec.Replicas != nil {
		replicas = *md.Spec.Replicas
	}

	// Build metadata
	metadata := map[string]interface{}{
		"backend": md.Spec.Backend,
		"ready":   ready,
		"scaled":  replicas > 0,
	}

	if md.Status.Phase != "" {
		metadata["phase"] = string(md.Status.Phase)
	}

	if md.Spec.GPUGroupRef != nil && *md.Spec.GPUGroupRef != "" {
		metadata["gpu_group"] = *md.Spec.GPUGroupRef
	}

	// Add aliases from LiteLLM spec
	if md.Spec.LiteLLM != nil {
		if md.Spec.LiteLLM.ServedModelName != "" {
			metadata["served_model_name"] = md.Spec.LiteLLM.ServedModelName
		}
		if len(md.Spec.LiteLLM.Aliases) > 0 {
			metadata["aliases"] = md.Spec.LiteLLM.Aliases
		}
	}

	// Add service labels
	if len(md.Spec.ServiceLabels) > 0 {
		metadata["service_labels"] = md.Spec.ServiceLabels
	}

	return OpenAIModel{
		ID:       md.Name,
		Object:   "model",
		Created:  md.CreationTimestamp.Unix(),
		OwnedBy:  "flexinfer",
		Metadata: metadata,
	}
}

// modelToOpenAI converts a v1alpha2 Model to OpenAI model format
func (p *Proxy) modelToOpenAI(m *aiv1alpha2.Model) OpenAIModel {
	// Determine readiness from status
	ready := m.Status.Phase == aiv1alpha2.ModelPhaseReady

	// Build metadata
	metadata := map[string]interface{}{
		"backend": m.Spec.Backend,
		"source":  m.Spec.Source,
		"ready":   ready,
		"version": "v1alpha2",
	}

	if m.Status.Phase != "" {
		metadata["phase"] = string(m.Status.Phase)
	}

	// GPU sharing info
	if m.Spec.GPU != nil {
		if m.Spec.GPU.Shared != "" {
			metadata["gpu_shared"] = m.Spec.GPU.Shared
		}
		if m.Spec.GPU.Priority != nil {
			metadata["gpu_priority"] = *m.Spec.GPU.Priority
		}
	}

	// Add aliases from LiteLLM spec
	if m.Spec.LiteLLM != nil {
		if m.Spec.LiteLLM.ServedModelName != "" {
			metadata["served_model_name"] = m.Spec.LiteLLM.ServedModelName
		}
		if len(m.Spec.LiteLLM.Aliases) > 0 {
			metadata["aliases"] = m.Spec.LiteLLM.Aliases
		}
	}

	// Add service labels
	if len(m.Spec.ServiceLabels) > 0 {
		metadata["service_labels"] = m.Spec.ServiceLabels
	}

	return OpenAIModel{
		ID:       m.Name,
		Object:   "model",
		Created:  m.CreationTimestamp.Unix(),
		OwnedBy:  "flexinfer",
		Metadata: metadata,
	}
}
