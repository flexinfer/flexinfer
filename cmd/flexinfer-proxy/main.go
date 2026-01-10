package main

import (
	"bytes"
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"log"
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
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"golang.org/x/sync/singleflight"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/config"
)

var (
	scheme = runtime.NewScheme()

	requestsTotal = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "proxy_requests_total",
			Help: "Total number of requests processed by the proxy.",
		},
		[]string{"model", "status"},
	)

	scaleUpsTotal = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "proxy_scale_ups_total",
			Help: "Total number of scale-up operations triggered.",
		},
		[]string{"model"},
	)

	requestDuration = prometheus.NewHistogramVec(
		prometheus.HistogramOpts{
			Name:    "proxy_request_duration_seconds",
			Help:    "Histogram of request processing duration.",
			Buckets: prometheus.DefBuckets,
		},
		[]string{"model"},
	)

	// Request queue metrics
	queuedRequestsTotal = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "proxy_queued_requests_total",
			Help: "Total number of requests queued during cold start.",
		},
		[]string{"model"},
	)

	queueRejectedTotal = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "proxy_queue_rejected_total",
			Help: "Total number of requests rejected due to full queue.",
		},
		[]string{"model"},
	)

	queueWaitDuration = prometheus.NewHistogramVec(
		prometheus.HistogramOpts{
			Name:    "proxy_queue_wait_seconds",
			Help:    "Time requests spent waiting in queue during cold start.",
			Buckets: []float64{0.1, 0.5, 1, 2, 5, 10, 20, 30, 60},
		},
		[]string{"model"},
	)

	activeConnections = prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Name: "proxy_active_connections",
			Help: "Number of active connections per model.",
		},
		[]string{"model"},
	)

	queueDepth = prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Name: "proxy_queue_depth",
			Help: "Current number of requests waiting in queue per model.",
		},
		[]string{"model"},
	)
)

func init() {
	utilruntime.Must(clientgoscheme.AddToScheme(scheme))
	utilruntime.Must(aiv1alpha1.AddToScheme(scheme))

	// Register metrics
	prometheus.MustRegister(requestsTotal)
	prometheus.MustRegister(scaleUpsTotal)
	prometheus.MustRegister(requestDuration)
	prometheus.MustRegister(queuedRequestsTotal)
	prometheus.MustRegister(queueRejectedTotal)
	prometheus.MustRegister(queueWaitDuration)
	prometheus.MustRegister(activeConnections)
	prometheus.MustRegister(queueDepth)
}

// QueuedRequest represents a request waiting in queue during cold start
type QueuedRequest struct {
	w          http.ResponseWriter
	r          *http.Request
	modelName  string
	done       chan struct{}
	err        error
	enqueuedAt time.Time
}

// RequestQueue holds requests for a model during cold start
type RequestQueue struct {
	model    string
	items    chan *QueuedRequest
	created  time.Time
	draining atomic.Bool
}

type Proxy struct {
	client       client.Client
	namespace    string
	proxyMap     sync.Map           // cache of httputil.ReverseProxy by model name
	requestGroup singleflight.Group // coalescing activation requests

	// Request queues per model during cold start
	queues   sync.Map // map[string]*RequestQueue
	queuesMu sync.Mutex

	// Configuration (can be overridden by env vars)
	maxQueueSize       int           // Default: 100
	queueTimeout       time.Duration // Default: 60s (how long request can wait in queue)
	coldStartTimeout   time.Duration // Default: 60s (how long to wait for model to become ready)
	connectionTracking sync.Map      // map[string]*int64 for tracking active connections per model
}

func main() {
	var port int
	flag.IntVar(&port, "port", 8080, "Port to listen on")
	flag.Parse()

	namespace := os.Getenv("POD_NAMESPACE")
	if namespace == "" {
		namespace = "default"
	}

	cfg, err := config.GetConfig()
	if err != nil {
		log.Fatalf("unable to get kubeconfig: %v", err)
	}

	k8sClient, err := client.New(cfg, client.Options{Scheme: scheme})
	if err != nil {
		log.Fatalf("unable to create k8s client: %v", err)
	}

	// Load configuration from environment variables
	maxQueueSize := getEnvInt("PROXY_MAX_QUEUE_SIZE", 100)
	queueTimeout := getEnvDuration("PROXY_QUEUE_TIMEOUT", 60*time.Second)
	coldStartTimeout := getEnvDuration("PROXY_COLD_START_TIMEOUT", 60*time.Second)

	p := &Proxy{
		client:           k8sClient,
		namespace:        namespace,
		maxQueueSize:     maxQueueSize,
		queueTimeout:     queueTimeout,
		coldStartTimeout: coldStartTimeout,
	}

	// Start queue cleanup goroutine
	go p.cleanupStaleQueues()

	http.Handle("/metrics", promhttp.Handler())
	http.HandleFunc("/", p.handleRequest)
	http.HandleFunc("/healthz", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		if _, err := w.Write([]byte("ok")); err != nil {
			log.Printf("healthz write failed: %v", err)
		}
	})

	log.Printf("Starting proxy on :%d in namespace %s (queue_size=%d, queue_timeout=%s, cold_start_timeout=%s)",
		port, namespace, maxQueueSize, queueTimeout, coldStartTimeout)
	if err := http.ListenAndServe(fmt.Sprintf(":%d", port), nil); err != nil {
		log.Fatalf("server failed: %v", err)
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

func (p *Proxy) handleRequest(w http.ResponseWriter, r *http.Request) {
	start := time.Now()

	// 1. Extract model name from request
	modelName := p.extractModelName(r)
	if modelName == "" {
		http.Error(w, "X-Model-ID header or /model/<name> path required", http.StatusBadRequest)
		return
	}

	ctx := r.Context()

	// 2. Check if model is scaled to zero
	md, err := p.getModelDeployment(ctx, modelName)
	if err != nil {
		if errors.IsNotFound(err) {
			http.Error(w, fmt.Sprintf("Model deployment %s not found", modelName), http.StatusNotFound)
		} else {
			log.Printf("Error fetching model %s: %v", modelName, err)
			http.Error(w, "Internal error", http.StatusInternalServerError)
		}
		requestsTotal.WithLabelValues(modelName, "error").Inc()
		return
	}

	// 3. If model is ready, serve directly
	if isReady(md) && (md.Spec.Replicas != nil && *md.Spec.Replicas > 0) {
		p.trackAndServe(w, r, modelName, start)
		return
	}

	// 4. Model is scaled to zero or not ready - use queue
	if err := p.handleColdStart(ctx, w, r, modelName, start); err != nil {
		log.Printf("Cold start failed for model %s: %v", modelName, err)
		requestsTotal.WithLabelValues(modelName, "error").Inc()
		// Error response already sent by handleColdStart
	}
}

// extractModelName extracts the model name from request headers, path, or body
func (p *Proxy) extractModelName(r *http.Request) string {
	// Check X-Model-ID header first
	modelName := r.Header.Get("X-Model-ID")
	if modelName != "" {
		return modelName
	}

	// Fallback: Use path prefix /model/<name>/...
	pathParts := strings.Split(strings.TrimPrefix(r.URL.Path, "/"), "/")
	if len(pathParts) > 1 && pathParts[0] == "model" {
		modelName = pathParts[1]
		// Strip the /model/<name> prefix for upstream
		r.URL.Path = "/" + strings.Join(pathParts[2:], "/")
		return modelName
	}

	// Fallback: Check JSON Body (OpenAI Standard)
	if r.Method == http.MethodPost && strings.Contains(r.Header.Get("Content-Type"), "application/json") {
		bodyBytes, err := io.ReadAll(r.Body)
		if err == nil {
			// Restore body immediately so the proxy can upstream it
			r.Body = io.NopCloser(bytes.NewBuffer(bodyBytes))

			// Parse partial JSON to find "model" field
			var payload struct {
				Model string `json:"model"`
			}
			if err := json.Unmarshal(bodyBytes, &payload); err == nil && payload.Model != "" {
				return payload.Model
			}
		}
	}

	return ""
}

// getModelDeployment fetches the ModelDeployment resource
func (p *Proxy) getModelDeployment(ctx context.Context, modelName string) (*aiv1alpha1.ModelDeployment, error) {
	md := &aiv1alpha1.ModelDeployment{}
	err := p.client.Get(ctx, client.ObjectKey{Name: modelName, Namespace: p.namespace}, md)
	return md, err
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
		log.Printf("Request queued for model %s (queue depth: %d)", modelName, len(queue.items))
	default:
		// Queue is full
		queueRejectedTotal.WithLabelValues(modelName).Inc()
		http.Error(w, "Service overloaded, please retry", http.StatusServiceUnavailable)
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
		// Success metrics recorded by the processing goroutine
		return nil
	case <-queueCtx.Done():
		// Timeout waiting in queue
		queueWaitDuration.WithLabelValues(modelName).Observe(time.Since(qr.enqueuedAt).Seconds())
		http.Error(w, fmt.Sprintf("Timeout waiting for model to become ready (waited %s)", p.queueTimeout), http.StatusGatewayTimeout)
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

	log.Printf("Created new request queue for model %s", modelName)
	return queue
}

// processQueue handles scale-up and drains the queue when model is ready
func (p *Proxy) processQueue(queue *RequestQueue) {
	modelName := queue.model
	ctx := context.Background()

	log.Printf("Starting queue processor for model %s", modelName)

	// Trigger scale-up using singleflight to deduplicate
	_, err, _ := p.requestGroup.Do(modelName+"-scaleup", func() (interface{}, error) {
		return nil, p.triggerScaleUp(ctx, modelName)
	})

	if err != nil {
		log.Printf("Failed to scale up model %s: %v", modelName, err)
		// Drain queue with errors
		p.drainQueueWithError(queue, fmt.Errorf("scale-up failed: %v", err))
		return
	}

	// Wait for model to become ready
	if err := p.waitForReady(ctx, modelName); err != nil {
		log.Printf("Model %s failed to become ready: %v", modelName, err)
		p.drainQueueWithError(queue, err)
		return
	}

	log.Printf("Model %s is ready, draining queue", modelName)

	// Mark queue as draining (prevents new requests from being queued here)
	queue.draining.Store(true)

	// Drain queue - serve all pending requests
	p.drainQueue(queue)

	// Clean up queue
	p.queues.Delete(modelName)
	log.Printf("Queue processor for model %s finished", modelName)
}

// triggerScaleUp scales the model to 1 replica
func (p *Proxy) triggerScaleUp(ctx context.Context, modelName string) error {
	md, err := p.getModelDeployment(ctx, modelName)
	if err != nil {
		return err
	}

	// Already scaled up?
	if md.Spec.Replicas != nil && *md.Spec.Replicas > 0 {
		return nil
	}

	log.Printf("Scaling up model %s from 0 to 1", modelName)
	scaleUpsTotal.WithLabelValues(modelName).Inc()

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
			if err != nil {
				log.Printf("Error checking model %s readiness: %v", modelName, err)
				continue
			}
			if isReady(md) {
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
	return p.coldStartTimeout
}

// drainQueue processes all pending requests
func (p *Proxy) drainQueue(queue *RequestQueue) {
	for {
		select {
		case qr := <-queue.items:
			queueDepth.WithLabelValues(queue.model).Dec()
			// Process request
			start := time.Now()
			p.trackAndServe(qr.w, qr.r, qr.modelName, start)
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
			http.Error(qr.w, fmt.Sprintf("Failed to activate model: %v", err), http.StatusServiceUnavailable)
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
				log.Printf("Cleaning up stale queue for model %s", key.(string))
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
		log.Printf("Error fetching model for stats update: %v", err)
		return
	}

	now := metav1.Now()
	// Update status directly
	md.Status.LastAccessTime = &now
	if err := p.client.Status().Update(ctx, md); err != nil {
		// Log but don't fail, it's just stats
		log.Printf("Failed to update LastAccessTime: %v", err)
	}
}

func (p *Proxy) serveProxy(w http.ResponseWriter, r *http.Request, modelName string) {
	targetURL := fmt.Sprintf("http://%s.%s.svc.cluster.local:80", modelName, p.namespace) // Assuming backend is on port 80 of the Service

	// Check if we have a proxy for this already
	var rp *httputil.ReverseProxy
	if val, ok := p.proxyMap.Load(modelName); ok {
		rp = val.(*httputil.ReverseProxy)
	} else {
		// Create new proxy
		u, _ := url.Parse(targetURL)
		rp = httputil.NewSingleHostReverseProxy(u)
		p.proxyMap.Store(modelName, rp)
	}

	rp.ServeHTTP(w, r)
}

func isReady(md *aiv1alpha1.ModelDeployment) bool {
	for _, cond := range md.Status.Conditions {
		if cond.Type == aiv1alpha1.ConditionTypeReady && cond.Status == metav1.ConditionTrue {
			return true
		}
	}
	return false
}
