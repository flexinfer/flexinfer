package proxy

import (
	"bytes"
	"context"
	"encoding/json"
	stderrors "errors"
	"fmt"
	"io"
	"log/slog"
	"math/rand"
	"net/http"
	"strings"
	"sync/atomic"
	"time"

	aiv1alpha2 "github.com/flexinfer/flexinfer/api/v1alpha2"
	"github.com/flexinfer/flexinfer/backend"
	pkgrt "github.com/flexinfer/flexinfer/pkg/runtime"
	"github.com/flexinfer/flexinfer/pkg/validation"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"k8s.io/apimachinery/pkg/api/errors"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// QueuedRequest represents a request waiting in queue during cold start.
type QueuedRequest struct {
	w          http.ResponseWriter
	r          *http.Request
	modelName  string
	done       chan struct{}
	err        error
	enqueuedAt time.Time
	responded  atomic.Bool
}

// RequestQueue holds requests for a model during cold start.
type RequestQueue struct {
	model    string
	items    chan *QueuedRequest
	created  time.Time
	draining atomic.Bool
}

// handleColdStart handles requests when model is scaled to zero.
func (p *Proxy) handleColdStart(ctx context.Context, w http.ResponseWriter, r *http.Request, modelName string, start time.Time) error {
	ctx, coldSpan := otel.Tracer("flexinfer/proxy").Start(ctx, "proxy.cold_start")
	defer coldSpan.End()
	coldSpan.SetAttributes(attribute.String("flexinfer.model", modelName))

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
	if coldStartTimeout := p.activator.GetColdStartTimeout(ctx, modelName); coldStartTimeout > timeout {
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
		// Distinguish an actual queue timeout from caller cancellation.
		queueWaitDuration.WithLabelValues(modelName).Observe(time.Since(qr.enqueuedAt).Seconds())
		if stderrors.Is(queueCtx.Err(), context.DeadlineExceeded) && qr.responded.CompareAndSwap(false, true) {
			validation.WriteColdStartTimeout(w, timeout.String())
		}
		if stderrors.Is(queueCtx.Err(), context.Canceled) {
			return fmt.Errorf("queue canceled for model %s: %w", modelName, queueCtx.Err())
		}
		return fmt.Errorf("queue timeout for model %s", modelName)
	}
}

// getOrCreateQueue returns an existing queue or creates a new one.
func (p *Proxy) getOrCreateQueue(modelName string) *RequestQueue {
	// Fast path: check if queue exists
	if queue, ok := p.queues.Load(modelName); ok {
		return queue
	}

	// Slow path: create new queue (with lock to prevent duplicates)
	p.queuesMu.Lock()
	defer p.queuesMu.Unlock()

	// Double-check after acquiring lock
	if queue, ok := p.queues.Load(modelName); ok {
		return queue
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

// processQueue handles scale-up and drains the queue when model is ready.
func (p *Proxy) processQueue(queue *RequestQueue) {
	modelName := queue.model
	ctx := p.ctx
	if ctx == nil {
		ctx = context.Background()
	}

	slog.Info("starting queue processor", "model", modelName)

	activationStart := time.Now()

	// Record demand before attempting activation so the controller's
	// serverless reconcile loop does not immediately reap a direct-loaded model.
	p.activator.TouchLastActiveTime(ctx, modelName)

	// Fast path: try direct runtime load (bypasses controller reconcile loop).
	if p.directRuntimeEnabled && p.runtimeCache != nil {
		if p.tryDirectRuntimeLoad(ctx, modelName) {
			activationDurationSeconds.WithLabelValues(modelName, "direct", "success").Observe(time.Since(activationStart).Seconds())
			slog.Info("model ready via direct runtime load, draining queue", "model", modelName)
			queue.draining.Store(true)
			p.drainQueue(queue)
			p.queues.Delete(modelName)
			slog.Info("queue processor finished (direct)", "model", modelName)

			// Async: update LastActiveTime so controller backfills status.
			go p.activator.TouchLastActiveTime(ctx, modelName)
			return
		}
		slog.Debug("direct runtime load failed or unavailable, falling back to controller path", "model", modelName)
	}

	// Slow path: existing controller-based activation with optional backoff
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
		_, err, _ := p.requestGroup.Do(modelName+"-scaleup", func() (any, error) {
			return nil, p.activator.TriggerScaleUp(ctx, modelName)
		})

		if err != nil {
			lastErr = fmt.Errorf("scale-up failed: %v", err)
			slog.Warn("activation attempt failed", "model", modelName, "attempt", attempt+1, "error", err)
			activationFailuresTotal.WithLabelValues(modelName, "scale_up").Inc()
			continue
		}

		// Wait for model to become ready
		if err := p.waitForReady(ctx, modelName); err != nil {
			lastErr = err
			slog.Warn("model failed to become ready", "model", modelName, "attempt", attempt+1, "error", err)
			reason := "ready_timeout"
			if ctx.Err() != nil {
				reason = "context_cancelled"
			}
			activationFailuresTotal.WithLabelValues(modelName, reason).Inc()
			continue
		}

		// Success!
		lastErr = nil
		break
	}

	if lastErr != nil {
		slog.Error("all activation attempts failed", "model", modelName, "attempts", maxAttempts, "error", lastErr)
		activationDurationSeconds.WithLabelValues(modelName, "", "failure").Observe(time.Since(activationStart).Seconds())
		p.drainQueueWithError(queue, lastErr)
		return
	}

	activationDurationSeconds.WithLabelValues(modelName, "", "success").Observe(time.Since(activationStart).Seconds())

	slog.Info("model ready, draining queue", "model", modelName)

	// Mark queue as draining (prevents new requests from being queued here)
	queue.draining.Store(true)

	// Drain queue - serve all pending requests
	p.drainQueue(queue)

	// Clean up queue
	p.queues.Delete(modelName)
	slog.Info("queue processor finished", "model", modelName)
}

// drainQueue processes all pending requests.
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

// drainQueueWithError rejects all pending requests with an error.
func (p *Proxy) drainQueueWithError(queue *RequestQueue, err error) {
	queue.draining.Store(true)
	stall, stalled := isStalledLoadError(err)
	for {
		select {
		case qr := <-queue.items:
			queueDepth.WithLabelValues(queue.model).Dec()
			qr.err = err
			if qr.responded.CompareAndSwap(false, true) {
				if stalled {
					validation.WriteStalledLoad(qr.w, stall.Error(), defaultStalledLoadRetryAfter)
				} else {
					validation.WriteActivationFailed(qr.w, fmt.Sprintf("Failed to activate model: %v", err))
				}
			}
			close(qr.done)
		default:
			// Queue is empty
			p.queues.Delete(queue.model)
			return
		}
	}
}

// cleanupStaleQueues periodically removes stale queues.
func (p *Proxy) cleanupStaleQueues() {
	ticker := time.NewTicker(staleQueueCleanupInterval)
	defer ticker.Stop()

	ctx := p.ctx
	if ctx == nil {
		ctx = context.Background()
	}

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			now := time.Now()
			p.queues.Range(func(key string, queue *RequestQueue) bool {
				// Remove queues older than 2x queue timeout that are empty
				if now.Sub(queue.created) > 2*p.queueTimeout && len(queue.items) == 0 {
					slog.Debug("cleaning up stale queue", "model", key)
					p.queues.Delete(key)
				}
				return true
			})
		}
	}
}

// waitForReady polls until the model is ready or timeout.
func (p *Proxy) waitForReady(ctx context.Context, modelName string) error {
	// Get the cold start timeout, preferring per-model configuration
	timeout := p.activator.GetColdStartTimeout(ctx, modelName)

	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	ticker := time.NewTicker(readyPollInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return fmt.Errorf("timeout waiting for model to become ready (after %v)", timeout)
		case <-ticker.C:
			// Check v1alpha2 Model first
			m, err := p.getModel(ctx, modelName)
			if err == nil {
				if m.Status.Phase == aiv1alpha2.ModelPhaseReady {
					return nil
				}
				if failed := detectFailedModel(m); failed != nil {
					return failed
				}
				// Fail fast if the cold-start has obviously wedged on weight
				// loading (LoadingProgressAt timestamp hasn't advanced in long
				// enough that the proxy should stop queuing more work).
				if s := detectStalledLoad(m, defaultStalledLoadThreshold); s != nil {
					stalledLoadTotal.WithLabelValues(modelName, string(s.Substage)).Inc()
					return s
				}
				continue
			}
			if !errors.IsNotFound(err) {
				slog.Warn("error checking model readiness", "model", modelName, "error", err)
				continue
			}

			// Fallback: v1alpha1 ModelDeployment (deprecated)
			md, err := p.getModelDeployment(ctx, modelName)
			if err != nil {
				slog.Warn("error checking model deployment readiness", "model", modelName, "error", err)
				continue
			}
			if isReady(md) {
				return nil
			}
		}
	}
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

// ── Direct runtime fast path ──────────────────────────────────────

// tryDirectRuntimeLoad attempts to load a model directly on a runtime pod,
// bypassing the controller reconcile loop. Returns true on success.
func (p *Proxy) tryDirectRuntimeLoad(ctx context.Context, modelName string) bool {
	slog.Info("direct load: evaluating runtime fast path", "model", modelName)

	// Fetch the v1alpha2 Model CR to get backend/source/nodeSelector.
	m, err := p.getModel(ctx, modelName)
	if err != nil {
		slog.Info("direct load: cannot fetch model CR", "model", modelName, "error", err)
		return false
	}
	if ok, reason := pkgrt.DirectRuntimeLoadEligibility(m); !ok {
		slog.Info("direct load: skipping runtime path", "model", modelName, "reason", reason)
		return false
	}

	// Resolve backend.
	b, ok := backend.Get(m.Spec.Backend)
	if !ok {
		slog.Warn("direct load: unknown backend", "model", modelName, "backend", m.Spec.Backend)
		return false
	}

	// Find a ready runtime pod matching the model's nodeSelector.
	endpoint, err := p.runtimeCache.ForModel(ctx, m.Spec.NodeSelector)
	if err != nil || endpoint == nil {
		slog.Info("direct load: no runtime endpoint found", "model", modelName, "error", err)
		return false
	}
	slog.Info("direct load: matched runtime endpoint", "model", modelName, "runtimePod", endpoint.PodName, "node", endpoint.NodeName, "runtimeURL", endpoint.URL())

	// Build the load payload (shared with controller).
	// Pass /models as modelBasePath so PVC sources resolve correctly and
	// enrich the request with GPUProfile defaults plus compile-cache env.
	payload, err := pkgrt.BuildLoadPayloadForModel(m, b, pkgrt.BuildLoadOptions{
		ModelBasePath: "/models",
		GPUVendor:     backend.GPUVendor(m.Spec.GetGPUVendor()),
		GPUProfile:    p.lookupGPUProfile(ctx, endpoint.GPUArch),
	})
	if err != nil {
		slog.Warn("direct load: failed to build payload", "model", modelName, "error", err)
		return false
	}

	// Send load request to runtime pod.
	slog.Info("direct load: sending runtime load request", "model", modelName, "endpoint", endpoint.URL(), "backend", b.Name())
	if err := p.loadOnRuntime(ctx, endpoint, modelName, payload); err != nil {
		slog.Warn("direct load: runtime load request failed", "model", modelName, "endpoint", endpoint.URL(), "error", err)
		return false
	}

	// Poll runtime health until ready.
	slog.Info("direct load: waiting for runtime health", "model", modelName, "endpoint", endpoint.URL())
	if err := p.waitForRuntimeReady(ctx, endpoint, modelName); err != nil {
		slog.Warn("direct load: runtime health poll failed", "model", modelName, "error", err)
		return false
	}

	// Store the direct routing target so serveProxy routes to the runtime pod
	// instead of the (non-existent) K8s Service.
	backendPort := b.Port()
	targetURL := fmt.Sprintf("http://%s:%d", endpoint.PodIP, backendPort)
	p.directLoadTargets.Store(modelName, targetURL)
	slog.Info("direct load: registered routing target", "model", modelName, "target", targetURL)

	return true
}

func (p *Proxy) lookupGPUProfile(ctx context.Context, arch string) *aiv1alpha2.GPUProfileSpec {
	if arch == "" {
		return nil
	}

	list := &aiv1alpha2.GPUProfileList{}
	if err := p.client.List(ctx, list, client.InNamespace(p.namespace)); err != nil {
		slog.Debug("direct load: failed to list GPUProfiles", "arch", arch, "error", err)
		return nil
	}

	for i := range list.Items {
		if strings.EqualFold(list.Items[i].Spec.Architecture, arch) {
			profile := list.Items[i].Spec.DeepCopy()
			return profile
		}
	}
	return nil
}

// loadOnRuntime sends POST /api/v1/models/{name}/load to a runtime pod.
func (p *Proxy) loadOnRuntime(ctx context.Context, ep *pkgrt.RuntimeEndpoint, modelName string, payload []byte) error {
	url := fmt.Sprintf("%s/api/v1/models/%s/load", ep.URL(), modelName)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(payload))
	if err != nil {
		return fmt.Errorf("creating load request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	client := &http.Client{Timeout: 30 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("sending load request: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode >= 300 {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("runtime load failed (status %d): %s", resp.StatusCode, string(body))
	}
	return nil
}

// waitForRuntimeReady polls GET /api/v1/models/{name}/health on the runtime
// pod until the model reaches "Ready" state or a timeout is exceeded.
func (p *Proxy) waitForRuntimeReady(ctx context.Context, ep *pkgrt.RuntimeEndpoint, modelName string) error {
	timeout := p.coldStartTimeout
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	ticker := time.NewTicker(readyPollInterval)
	defer ticker.Stop()

	healthURL := fmt.Sprintf("%s/api/v1/models/%s/health", ep.URL(), modelName)
	client := &http.Client{Timeout: 5 * time.Second}

	for {
		select {
		case <-ctx.Done():
			return fmt.Errorf("timeout waiting for runtime model to become ready (after %v)", timeout)
		case <-ticker.C:
			req, err := http.NewRequestWithContext(ctx, http.MethodGet, healthURL, nil)
			if err != nil {
				continue
			}
			resp, err := client.Do(req)
			if err != nil {
				continue
			}
			var status struct {
				State string `json:"state"`
				Error string `json:"error"`
			}
			if err := json.NewDecoder(resp.Body).Decode(&status); err != nil {
				_ = resp.Body.Close()
				continue
			}
			_ = resp.Body.Close()

			switch status.State {
			case "Ready":
				return nil
			case "Failed":
				return fmt.Errorf("runtime model failed: %s", status.Error)
			}
			// "Loading" — keep polling
		}
	}
}
