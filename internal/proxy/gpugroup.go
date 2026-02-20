package proxy

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"strconv"
	"time"

	aiv1alpha1 "github.com/flexinfer/flexinfer/api/v1alpha1"
	"github.com/flexinfer/flexinfer/pkg/validation"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
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

	// Wait for request to be processed with timeout.
	// Use the larger of the global queue timeout and the per-model cold start timeout
	// so large models (e.g. 18GB GGUF) have enough time to load from storage.
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
		return nil
	case <-queueCtx.Done():
		// Timeout waiting in queue
		queueWaitDuration.WithLabelValues(modelName).Observe(time.Since(qr.enqueuedAt).Seconds())
		if qr.responded.CompareAndSwap(false, true) {
			validation.WriteGPUGroupTimeout(w, timeout.String())
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

// clearGPUGroupDemand clears the queue annotations when queue is drained.
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

// waitForGPUGroupActive polls until the model is active in the GPUGroup and ready.
func (p *Proxy) waitForGPUGroupActive(ctx context.Context, modelName, gpuGroupName string) error {
	// Use longer timeout for GPUGroup swaps (model swap + cold start)
	timeout := p.coldStartTimeout * 2
	if timeout < gpuGroupMinTimeout {
		timeout = gpuGroupMinTimeout
	}

	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	ticker := time.NewTicker(gpuGroupPollInterval)
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

// getGPUGroup fetches a GPUGroup resource.
func (p *Proxy) getGPUGroup(ctx context.Context, name string) (*aiv1alpha1.GPUGroup, error) {
	gpuGroup := &aiv1alpha1.GPUGroup{}
	err := p.client.Get(ctx, client.ObjectKey{Name: name, Namespace: p.namespace}, gpuGroup)
	return gpuGroup, err
}
