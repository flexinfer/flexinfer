/*
Copyright 2025.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package controllers

import (
	"context"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/tools/record"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/metrics"

	aiv1alpha1 "github.com/flexinfer/flexinfer/api/v1alpha1"
)

var (
	// GPUGroup metrics for Grafana dashboard
	gpuGroupActiveModel = prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Name: "flexinfer_gpugroup_active_model",
			Help: "Indicates which model is active in a GPUGroup (1=active, 0=inactive).",
		},
		[]string{"gpugroup", "model", "namespace"},
	)

	gpuGroupModelRunDuration = prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Name: "flexinfer_gpugroup_model_run_duration_seconds",
			Help: "How long the current model has been active in seconds.",
		},
		[]string{"gpugroup", "model", "namespace"},
	)

	gpuGroupSwapCooldown = prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Name: "flexinfer_gpugroup_swap_cooldown_seconds",
			Help: "Seconds remaining until next swap is allowed (anti-thrashing cooldown).",
		},
		[]string{"gpugroup", "namespace"},
	)

	gpuGroupSwapsTotal = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "flexinfer_gpugroup_swaps_total",
			Help: "Total number of model swaps performed.",
		},
		[]string{"gpugroup", "from_model", "to_model", "namespace"},
	)

	gpuGroupSwapBlockedAntithrashing = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "flexinfer_gpugroup_swap_blocked_antithrashing_total",
			Help: "Total swaps blocked due to minimum run time (anti-thrashing).",
		},
		[]string{"gpugroup", "namespace"},
	)

	gpuGroupSwapBlockedCooldown = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "flexinfer_gpugroup_swap_blocked_cooldown_total",
			Help: "Total swaps blocked due to cooldown period.",
		},
		[]string{"gpugroup", "namespace"},
	)

	gpuGroupModelQueueDepth = prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Name: "flexinfer_gpugroup_model_queue_depth",
			Help: "Current queue depth for each model in a GPUGroup.",
		},
		[]string{"gpugroup", "model", "namespace"},
	)
)

func init() {
	// Register GPUGroup metrics with controller-runtime's registry
	metrics.Registry.MustRegister(gpuGroupActiveModel)
	metrics.Registry.MustRegister(gpuGroupModelRunDuration)
	metrics.Registry.MustRegister(gpuGroupSwapCooldown)
	metrics.Registry.MustRegister(gpuGroupSwapsTotal)
	metrics.Registry.MustRegister(gpuGroupSwapBlockedAntithrashing)
	metrics.Registry.MustRegister(gpuGroupSwapBlockedCooldown)
	metrics.Registry.MustRegister(gpuGroupModelQueueDepth)
}

// GPUGroupReconciler reconciles a GPUGroup object
type GPUGroupReconciler struct {
	client.Client
	Scheme    *runtime.Scheme
	Recorder  record.EventRecorder
	APIReader client.Reader // Uncached reader for critical sections
}

// Annotation keys for proxy queue signaling
const (
	// AnnotationQueueDepth is set by proxy: flexinfer.ai/queue.{modelName}
	AnnotationQueueDepthPrefix = "flexinfer.ai/queue."
	// AnnotationQueueSince is when requests started queueing
	AnnotationQueueSincePrefix = "flexinfer.ai/queue-since."
	// AnnotationActiveServiceLabels contains comma-separated service labels for this active model
	AnnotationActiveServiceLabels = "ai.flexinfer/active-services"
)

// +kubebuilder:rbac:groups=ai.flexinfer,resources=gpugroups,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=ai.flexinfer,resources=gpugroups/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=ai.flexinfer,resources=gpugroups/finalizers,verbs=update
// +kubebuilder:rbac:groups=ai.flexinfer,resources=modeldeployments,verbs=get;list;watch;update;patch
// +kubebuilder:rbac:groups=ai.flexinfer,resources=modelcaches,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups="",resources=services,verbs=get;list;watch;update;patch

func (r *GPUGroupReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	log := log.FromContext(ctx)

	// Fetch the GPUGroup
	gpuGroup := &aiv1alpha1.GPUGroup{}
	if err := r.Get(ctx, req.NamespacedName, gpuGroup); err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}

	log.Info("Reconciling GPUGroup", "name", gpuGroup.Name, "phase", gpuGroup.Status.Phase)

	// Initialize status if needed
	if gpuGroup.Status.Phase == "" {
		gpuGroup.Status.Phase = aiv1alpha1.GPUGroupPhasePending
	}

	// Fetch all member ModelDeployments
	members, err := r.getMemberDeployments(ctx, gpuGroup)
	if err != nil {
		log.Error(err, "Failed to fetch member ModelDeployments")
		return ctrl.Result{}, err
	}

	// Update model statuses from queue annotations
	r.updateModelStatuses(ctx, gpuGroup, members)

	// Determine which model should be active based on demand and priority
	desiredActive, reason := r.determineActiveModel(ctx, gpuGroup, members)

	// Check if we need to swap models
	currentActive := gpuGroup.Status.ActiveModel
	if desiredActive != currentActive {
		// Check anti-thrashing rules
		blockReason := r.getSwapBlockReason(gpuGroup, desiredActive)
		if blockReason != "" {
			log.Info("Swap blocked by anti-thrashing rules",
				"current", currentActive, "desired", desiredActive, "reason", blockReason)
			// Record blocked swap metrics
			switch blockReason {
			case "minimum_run_time":
				gpuGroupSwapBlockedAntithrashing.WithLabelValues(gpuGroup.Name, gpuGroup.Namespace).Inc()
			case "cooldown":
				gpuGroupSwapBlockedCooldown.WithLabelValues(gpuGroup.Name, gpuGroup.Namespace).Inc()
			}
			// Update metrics before returning
			r.updateMetrics(gpuGroup, members)
			// Requeue to check again after cooldown
			return ctrl.Result{RequeueAfter: 10 * time.Second}, r.updateStatus(ctx, gpuGroup)
		}

		// Perform the swap
		if err := r.performModelSwap(ctx, gpuGroup, members, currentActive, desiredActive, reason); err != nil {
			log.Error(err, "Failed to perform model swap")
			return ctrl.Result{RequeueAfter: 5 * time.Second}, err
		}

		// Record successful swap metric
		gpuGroupSwapsTotal.WithLabelValues(gpuGroup.Name, currentActive, desiredActive, gpuGroup.Namespace).Inc()
	} else if currentActive != "" {
		// No swap needed, but ensure the current active model is scaled up
		// This handles the case where the model was marked active but never scaled up
		// (e.g., after a failed swap attempt, or when proxy is waiting for model to be ready)
		if md, ok := members[currentActive]; ok {
			if md.Spec.Replicas == nil || *md.Spec.Replicas == 0 {
				log.Info("Ensuring active model is scaled up", "model", currentActive)
				one := int32(1)
				md.Spec.Replicas = &one
				if err := r.Update(ctx, md); err != nil {
					log.Error(err, "Failed to scale up active model", "model", currentActive)
					return ctrl.Result{RequeueAfter: 5 * time.Second}, err
				}
				r.Recorder.Eventf(gpuGroup, "Normal", "ModelScaledUp",
					"Ensured model %s is scaled up", currentActive)
			}
			// Always sync service labels for the active model
			// This handles serviceLabels changes that occur while the model is running
			if err := r.updateServiceLabels(ctx, md, true); err != nil {
				log.Error(err, "Failed to sync service labels", "model", currentActive)
			}
		}
	}

	// Enforce Exclusive strategy: ensure only the active model is running
	// This handles cases where non-active models have replicas > 0 (e.g., from GitOps)
	strategy := gpuGroup.Spec.ScalingPolicy.Strategy
	if strategy == "" || strategy == aiv1alpha1.GPUShareStrategyExclusive {
		activeModel := gpuGroup.Status.ActiveModel
		for name, md := range members {
			if name != activeModel {
				if md.Spec.Replicas != nil && *md.Spec.Replicas > 0 {
					log.Info("Scaling down non-active model in Exclusive mode", "model", name, "activeModel", activeModel)
					zero := int32(0)
					md.Spec.Replicas = &zero
					if err := r.Update(ctx, md); err != nil {
						log.Error(err, "Failed to scale down non-active model", "model", name)
						// Continue trying other models
					} else {
						r.Recorder.Eventf(gpuGroup, "Normal", "ModelScaledDown",
							"Scaled down non-active model %s (Exclusive strategy)", name)
						// Remove service labels from scaled-down model
						if err := r.updateServiceLabels(ctx, md, false); err != nil {
							log.Error(err, "Failed to remove service labels", "model", name)
						}
					}
				}
			}
		}
	}

	// Update phase based on current state
	r.updatePhase(gpuGroup, members)

	// Update Prometheus metrics
	r.updateMetrics(gpuGroup, members)

	// Save status
	if err := r.updateStatus(ctx, gpuGroup); err != nil {
		return ctrl.Result{}, err
	}

	// Requeue periodically to check for demand changes
	return ctrl.Result{RequeueAfter: 5 * time.Second}, nil
}

// getMemberDeployments fetches all ModelDeployments referenced by the GPUGroup
func (r *GPUGroupReconciler) getMemberDeployments(ctx context.Context, gpuGroup *aiv1alpha1.GPUGroup) (map[string]*aiv1alpha1.ModelDeployment, error) {
	members := make(map[string]*aiv1alpha1.ModelDeployment)

	for _, member := range gpuGroup.Spec.Models {
		namespace := member.Namespace
		if namespace == "" {
			namespace = gpuGroup.Namespace
		}

		md := &aiv1alpha1.ModelDeployment{}
		if err := r.Get(ctx, client.ObjectKey{Name: member.Name, Namespace: namespace}, md); err != nil {
			if client.IgnoreNotFound(err) != nil {
				return nil, fmt.Errorf("failed to get ModelDeployment %s: %w", member.Name, err)
			}
			// ModelDeployment doesn't exist yet, skip
			continue
		}

		members[member.Name] = md
	}

	return members, nil
}

// updateModelStatuses updates GPUGroup status with current model states
func (r *GPUGroupReconciler) updateModelStatuses(ctx context.Context, gpuGroup *aiv1alpha1.GPUGroup, members map[string]*aiv1alpha1.ModelDeployment) {
	statuses := make([]aiv1alpha1.GPUGroupModelStatus, 0, len(gpuGroup.Spec.Models))

	for _, member := range gpuGroup.Spec.Models {
		status := aiv1alpha1.GPUGroupModelStatus{
			Name: member.Name,
		}

		md, exists := members[member.Name]
		if !exists {
			status.State = aiv1alpha1.ModelGroupStateIdle
			statuses = append(statuses, status)
			continue
		}

		// Check if this model is currently active (has running replicas)
		if md.Spec.Replicas != nil && *md.Spec.Replicas > 0 {
			status.State = aiv1alpha1.ModelGroupStateActive
			if md.Status.LastAccessTime != nil {
				status.LastActiveTime = md.Status.LastAccessTime
			}
		} else {
			status.State = aiv1alpha1.ModelGroupStateIdle
		}

		// Check for queued requests from GPUGroup annotations
		queueKey := AnnotationQueueDepthPrefix + member.Name
		if gpuGroup.Annotations != nil {
			if queueStr, ok := gpuGroup.Annotations[queueKey]; ok {
				if queue, err := strconv.ParseInt(queueStr, 10, 32); err == nil && queue > 0 {
					status.QueuedRequests = int32(queue)
					if status.State == aiv1alpha1.ModelGroupStateIdle {
						status.State = aiv1alpha1.ModelGroupStateQueued
					}
				}
			}

			// Check queue since time
			sinceKey := AnnotationQueueSincePrefix + member.Name
			if sinceStr, ok := gpuGroup.Annotations[sinceKey]; ok {
				if t, err := time.Parse(time.RFC3339, sinceStr); err == nil {
					sinceTime := metav1.NewTime(t)
					status.QueuedSince = &sinceTime
				}
			}
		}

		// Preserve preemption info from existing status
		for _, existing := range gpuGroup.Status.ModelStatuses {
			if existing.Name == member.Name {
				if status.State != aiv1alpha1.ModelGroupStateActive {
					status.PreemptedAt = existing.PreemptedAt
					status.PreemptedBy = existing.PreemptedBy
				}
				break
			}
		}

		statuses = append(statuses, status)
	}

	gpuGroup.Status.ModelStatuses = statuses
}

// determineActiveModel decides which model should be running based on demand and priority
func (r *GPUGroupReconciler) determineActiveModel(ctx context.Context, gpuGroup *aiv1alpha1.GPUGroup, members map[string]*aiv1alpha1.ModelDeployment) (string, string) {
	log := log.FromContext(ctx)

	// Build list of models with demand (queued requests)
	type modelDemand struct {
		name        string
		priority    int32
		queueDepth  int32
		queuedSince time.Time
		hasQueue    bool
	}

	demands := make([]modelDemand, 0)

	for _, member := range gpuGroup.Spec.Models {
		demand := modelDemand{
			name:     member.Name,
			priority: member.Priority,
		}

		// Check for queued requests
		for _, status := range gpuGroup.Status.ModelStatuses {
			if status.Name == member.Name {
				demand.queueDepth = status.QueuedRequests
				demand.hasQueue = status.QueuedRequests > 0
				if status.QueuedSince != nil {
					demand.queuedSince = status.QueuedSince.Time
				}
				break
			}
		}

		demands = append(demands, demand)
	}

	// Sort by: hasQueue desc, priority desc, queuedSince asc
	sort.Slice(demands, func(i, j int) bool {
		// Prioritize models with queued requests
		if demands[i].hasQueue != demands[j].hasQueue {
			return demands[i].hasQueue
		}
		// Then by priority (higher first)
		if demands[i].priority != demands[j].priority {
			return demands[i].priority > demands[j].priority
		}
		// Then by how long they've been waiting (older first)
		return demands[i].queuedSince.Before(demands[j].queuedSince)
	})

	// If no model has demand, check if current active should stay
	currentActive := gpuGroup.Status.ActiveModel
	if len(demands) == 0 || !demands[0].hasQueue {
		// Keep current active if it exists and has recent activity
		if currentActive != "" {
			if md, ok := members[currentActive]; ok {
				if md.Status.LastAccessTime != nil {
					// Check idle timeout
					idleTimeout := 300 * time.Second // default
					if md.Spec.IdleTimeoutSeconds != nil {
						idleTimeout = time.Duration(*md.Spec.IdleTimeoutSeconds) * time.Second
					}
					idleDuration := time.Since(md.Status.LastAccessTime.Time)
					if idleDuration < idleTimeout {
						return currentActive, "still active"
					}
				}
			}
		}

		// No demand and current model is idle - scale everything to zero
		log.Info("No demand detected, all models idle")
		return "", "no demand"
	}

	winner := demands[0]

	if gpuGroup.Spec.AntiThrashing.Enabled {
		// Check if queue threshold is met for anti-thrashing
		threshold := int32(3) // default
		if gpuGroup.Spec.AntiThrashing.RequestQueueThreshold > 0 {
			threshold = gpuGroup.Spec.AntiThrashing.RequestQueueThreshold
		}

		if winner.queueDepth < threshold && winner.name != currentActive {
			// Not enough queued requests to justify a swap
			log.Info("Queue threshold not met for swap",
				"model", winner.name, "queue", winner.queueDepth, "threshold", threshold)
			if currentActive != "" {
				return currentActive, "queue threshold not met"
			}
			return "", "queue threshold not met"
		}

		// Check hysteresis window (skip if set to 0 to disable)
		if gpuGroup.Spec.AntiThrashing.HysteresisWindowSeconds > 0 {
			hysteresisDuration := time.Duration(gpuGroup.Spec.AntiThrashing.HysteresisWindowSeconds) * time.Second
			if currentActive != "" && !winner.queuedSince.IsZero() && time.Since(winner.queuedSince) < hysteresisDuration && winner.name != currentActive {
				log.Info("Hysteresis window not elapsed",
					"model", winner.name, "queuedSince", winner.queuedSince, "window", hysteresisDuration)
				return currentActive, "hysteresis window"
			}
		}

		// Check demand trend for the winning model if it's not the current active
		if winner.name != currentActive {
			// Find the model status for trend analysis
			for i := range gpuGroup.Status.ModelStatuses {
				if gpuGroup.Status.ModelStatuses[i].Name == winner.name {
					// Minimum stability of 50% required for trend-based decisions
					if !r.shouldSwapBasedOnTrend(&gpuGroup.Status.ModelStatuses[i], 50) {
						log.Info("Trend analysis does not support swap",
							"model", winner.name, "trend", gpuGroup.Status.ModelStatuses[i].TrendDirection,
							"stability", gpuGroup.Status.ModelStatuses[i].TrendStability)
						if currentActive != "" {
							return currentActive, "trend analysis"
						}
						return "", "trend analysis"
					}
					break
				}
			}
		}
	}

	return winner.name, fmt.Sprintf("demand: queue=%d, priority=%d", winner.queueDepth, winner.priority)
}

// shouldBlockSwap checks anti-thrashing rules to see if swap should be blocked
// getSwapBlockReason returns the reason for blocking a swap, or empty string if swap is allowed.
// Returns "minimum_run_time" if blocked due to anti-thrashing min run time.
// Returns "cooldown" if blocked due to preemption cooldown.
func (r *GPUGroupReconciler) getSwapBlockReason(gpuGroup *aiv1alpha1.GPUGroup, desiredActive string) string {
	if !gpuGroup.Spec.AntiThrashing.Enabled {
		return ""
	}

	// Check minimum run duration for current active model
	if gpuGroup.Status.ActiveModel != "" && gpuGroup.Status.LastSwapTime != nil {
		minRunDuration := 30 * time.Second // default
		if gpuGroup.Spec.AntiThrashing.MinimumRunDurationSeconds > 0 {
			minRunDuration = time.Duration(gpuGroup.Spec.AntiThrashing.MinimumRunDurationSeconds) * time.Second
		}

		runDuration := time.Since(gpuGroup.Status.LastSwapTime.Time)
		if runDuration < minRunDuration {
			return "minimum_run_time"
		}
	}

	// Check cooldown for the model we want to activate (was it recently preempted?)
	cooldown := 60 * time.Second // default
	if gpuGroup.Spec.AntiThrashing.CooldownAfterPreemptionSeconds > 0 {
		cooldown = time.Duration(gpuGroup.Spec.AntiThrashing.CooldownAfterPreemptionSeconds) * time.Second
	}

	for _, status := range gpuGroup.Status.ModelStatuses {
		if status.Name == desiredActive && status.PreemptedAt != nil {
			if time.Since(status.PreemptedAt.Time) < cooldown {
				return "cooldown"
			}
		}
	}

	return ""
}

// performModelSwap orchestrates the scale-down of current model and scale-up of new model
func (r *GPUGroupReconciler) performModelSwap(ctx context.Context, gpuGroup *aiv1alpha1.GPUGroup,
	members map[string]*aiv1alpha1.ModelDeployment, currentActive, newActive, reason string) error {
	log := log.FromContext(ctx)

	log.Info("Performing model swap", "from", currentActive, "to", newActive, "reason", reason)

	// Update phase to draining/swapping
	if currentActive != "" {
		gpuGroup.Status.Phase = aiv1alpha1.GPUGroupPhaseDraining
	} else {
		gpuGroup.Status.Phase = aiv1alpha1.GPUGroupPhaseSwapping
	}

	// Scale down current active model (if any)
	if currentActive != "" {
		if md, ok := members[currentActive]; ok {
			log.Info("Scaling down current model", "model", currentActive)

			zero := int32(0)
			md.Spec.Replicas = &zero

			if err := r.Update(ctx, md); err != nil {
				return fmt.Errorf("failed to scale down %s: %w", currentActive, err)
			}

			// Record preemption in status
			now := metav1.Now()
			for i := range gpuGroup.Status.ModelStatuses {
				if gpuGroup.Status.ModelStatuses[i].Name == currentActive {
					gpuGroup.Status.ModelStatuses[i].State = aiv1alpha1.ModelGroupStatePreempted
					gpuGroup.Status.ModelStatuses[i].PreemptedAt = &now
					gpuGroup.Status.ModelStatuses[i].PreemptedBy = newActive
					break
				}
			}

			// Wait for pod termination if graceful preemption
			if gpuGroup.Spec.ScalingPolicy.PreemptionPolicy == aiv1alpha1.PreemptionPolicyGraceful {
				drainTimeout := 60 * time.Second
				if gpuGroup.Spec.ScalingPolicy.DrainTimeoutSeconds > 0 {
					drainTimeout = time.Duration(gpuGroup.Spec.ScalingPolicy.DrainTimeoutSeconds) * time.Second
				}

				// Poll for deployment to have 0 ready replicas
				preemptResult, err := r.waitForPodTermination(ctx, md, drainTimeout)
				if err != nil {
					log.Error(err, "Error waiting for pod termination", "model", currentActive)
					// Record the error but continue - we don't want to block indefinitely
					preemptResult.Error = err.Error()
				}

				// Record preemption result in model status
				for i := range gpuGroup.Status.ModelStatuses {
					if gpuGroup.Status.ModelStatuses[i].Name == currentActive {
						gpuGroup.Status.ModelStatuses[i].LastPreemptionResult = preemptResult
						break
					}
				}

				if preemptResult.TimedOut {
					r.Recorder.Eventf(gpuGroup, corev1.EventTypeWarning, "PreemptionTimeout",
						"Preemption of %s timed out after %v, proceeding anyway", currentActive, drainTimeout)
				} else if preemptResult.Success {
					r.Recorder.Eventf(gpuGroup, corev1.EventTypeNormal, "PreemptionComplete",
						"Model %s preempted successfully in %ds", currentActive, preemptResult.DrainDurationSeconds)
				}
			}

			r.Recorder.Eventf(gpuGroup, "Normal", "ModelScaledDown",
				"Scaled down model %s for swap to %s", currentActive, newActive)

			// Remove service labels from the scaled-down model's Service
			if err := r.updateServiceLabels(ctx, md, false); err != nil {
				log.Error(err, "Failed to remove service labels from scaled-down model", "model", currentActive)
				// Non-fatal: continue with the swap
			}
		}
	}

	// Scale up new active model (if any)
	if newActive != "" {
		if md, ok := members[newActive]; ok {
			log.Info("Scaling up new model", "model", newActive)

			one := int32(1)
			md.Spec.Replicas = &one

			if err := r.Update(ctx, md); err != nil {
				return fmt.Errorf("failed to scale up %s: %w", newActive, err)
			}

			// Update model status
			for i := range gpuGroup.Status.ModelStatuses {
				if gpuGroup.Status.ModelStatuses[i].Name == newActive {
					gpuGroup.Status.ModelStatuses[i].State = aiv1alpha1.ModelGroupStateActive
					// Clear preemption info since we're now active
					gpuGroup.Status.ModelStatuses[i].PreemptedAt = nil
					gpuGroup.Status.ModelStatuses[i].PreemptedBy = ""
					break
				}
			}

			r.Recorder.Eventf(gpuGroup, "Normal", "ModelScaledUp",
				"Scaled up model %s (reason: %s)", newActive, reason)

			// Add service labels to the scaled-up model's Service
			if err := r.updateServiceLabels(ctx, md, true); err != nil {
				log.Error(err, "Failed to add service labels to scaled-up model", "model", newActive)
				// Non-fatal: continue with the swap
			}
		}
	}

	// Update GPUGroup status
	gpuGroup.Status.ActiveModel = newActive
	now := metav1.Now()
	gpuGroup.Status.LastSwapTime = &now
	gpuGroup.Status.LastSwapReason = reason
	gpuGroup.Status.SwapCount++

	return nil
}

// updatePhase updates the GPUGroup phase based on current state
func (r *GPUGroupReconciler) updatePhase(gpuGroup *aiv1alpha1.GPUGroup, members map[string]*aiv1alpha1.ModelDeployment) {
	activeModel := gpuGroup.Status.ActiveModel

	if activeModel == "" {
		gpuGroup.Status.Phase = aiv1alpha1.GPUGroupPhaseIdle
		return
	}

	// Check if active model is actually running
	if md, ok := members[activeModel]; ok {
		if md.Status.Phase == aiv1alpha1.ModelDeploymentPhaseRunning {
			gpuGroup.Status.Phase = aiv1alpha1.GPUGroupPhaseActive
			return
		}
	}

	// Active model exists but not yet running - still swapping
	gpuGroup.Status.Phase = aiv1alpha1.GPUGroupPhaseSwapping
}

// updateStatus saves the GPUGroup status
func (r *GPUGroupReconciler) updateStatus(ctx context.Context, gpuGroup *aiv1alpha1.GPUGroup) error {
	// Use APIReader to get fresh version to avoid conflicts
	fresh := &aiv1alpha1.GPUGroup{}
	if err := r.APIReader.Get(ctx, client.ObjectKeyFromObject(gpuGroup), fresh); err != nil {
		return err
	}

	// Copy our status to fresh object
	fresh.Status = gpuGroup.Status

	return r.Status().Update(ctx, fresh)
}

// updateServiceLabels updates the service annotations for a model.
// When active is true, it adds the model's serviceLabels to the Service annotations.
// When active is false, it removes the annotation.
func (r *GPUGroupReconciler) updateServiceLabels(ctx context.Context, md *aiv1alpha1.ModelDeployment, active bool) error {
	log := log.FromContext(ctx)

	// Get the model's Service (same name as ModelDeployment)
	svc := &corev1.Service{}
	if err := r.Get(ctx, types.NamespacedName{Name: md.Name, Namespace: md.Namespace}, svc); err != nil {
		if apierrors.IsNotFound(err) {
			// Service doesn't exist yet, nothing to update
			log.V(1).Info("Service not found for model, skipping label update", "model", md.Name)
			return nil
		}
		return fmt.Errorf("failed to get service for %s: %w", md.Name, err)
	}

	// Initialize annotations map if nil
	if svc.Annotations == nil {
		svc.Annotations = make(map[string]string)
	}

	needsUpdate := false
	if active && len(md.Spec.ServiceLabels) > 0 {
		// Add service labels annotation
		newLabels := strings.Join(md.Spec.ServiceLabels, ",")
		if svc.Annotations[AnnotationActiveServiceLabels] != newLabels {
			svc.Annotations[AnnotationActiveServiceLabels] = newLabels
			needsUpdate = true
			log.Info("Adding service labels to model Service",
				"model", md.Name,
				"labels", newLabels)
		}
	} else {
		// Remove service labels annotation
		if _, exists := svc.Annotations[AnnotationActiveServiceLabels]; exists {
			delete(svc.Annotations, AnnotationActiveServiceLabels)
			needsUpdate = true
			log.Info("Removing service labels from model Service", "model", md.Name)
		}
	}

	if needsUpdate {
		if err := r.Update(ctx, svc); err != nil {
			return fmt.Errorf("failed to update service labels for %s: %w", md.Name, err)
		}
	}

	return nil
}

// === Preemption Verification ===

// waitForPodTermination polls until the deployment has 0 ready replicas or timeout.
func (r *GPUGroupReconciler) waitForPodTermination(ctx context.Context, md *aiv1alpha1.ModelDeployment, timeout time.Duration) (*aiv1alpha1.PreemptionResult, error) {
	log := log.FromContext(ctx)

	result := &aiv1alpha1.PreemptionResult{
		StartTime: metav1.Now(),
		Success:   false,
	}

	// Get the deployment name for this ModelDeployment
	deploymentName := md.Name
	deploymentNamespace := md.Namespace

	// Create a context with timeout
	pollCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	ticker := time.NewTicker(2 * time.Second)
	defer ticker.Stop()

	initialReplicas := int32(0)
	drainComplete := false

	for !drainComplete {
		select {
		case <-pollCtx.Done():
			// Timeout reached
			result.TimedOut = true
			now := metav1.Now()
			result.EndTime = &now
			result.DrainDurationSeconds = int32(time.Since(result.StartTime.Time).Seconds())
			log.Info("Preemption timed out", "model", md.Name, "timeout", timeout)
			return result, nil

		case <-ticker.C:
			// Poll the deployment status using the uncached APIReader
			deployment := &appsv1.Deployment{}
			err := r.APIReader.Get(ctx, types.NamespacedName{
				Name:      deploymentName,
				Namespace: deploymentNamespace,
			}, deployment)

			if err != nil {
				if apierrors.IsNotFound(err) {
					// Deployment doesn't exist, consider drain complete
					drainComplete = true
					break
				}
				// Non-fatal error, continue polling
				log.V(1).Info("Error checking deployment status", "error", err)
				continue
			}

			// Track initial replicas for metrics
			if initialReplicas == 0 && deployment.Status.ReadyReplicas > 0 {
				initialReplicas = deployment.Status.ReadyReplicas
			}

			// Check if all replicas are terminated
			if deployment.Status.ReadyReplicas == 0 && deployment.Status.Replicas == 0 {
				drainComplete = true
				result.PodsTerminated = initialReplicas
				log.Info("All pods terminated", "model", md.Name, "podsTerminated", initialReplicas)
			} else {
				log.V(1).Info("Waiting for pods to terminate",
					"model", md.Name,
					"readyReplicas", deployment.Status.ReadyReplicas,
					"replicas", deployment.Status.Replicas)
			}
		}
	}

	// Success
	result.Success = true
	now := metav1.Now()
	result.EndTime = &now
	result.DrainDurationSeconds = int32(time.Since(result.StartTime.Time).Seconds())

	return result, nil
}

// === Demand Trend Analysis ===

const (
	// MaxQueueHistorySize limits how many queue samples to retain
	MaxQueueHistorySize = 20
	// TrendWindowDuration is how far back to analyze for trends
	TrendWindowDuration = 5 * time.Minute
)

// recordQueueSample adds a queue depth sample to the model's history
func (r *GPUGroupReconciler) recordQueueSample(modelStatus *aiv1alpha1.GPUGroupModelStatus) {
	sample := aiv1alpha1.QueueSample{
		Timestamp: metav1.Now(),
		Depth:     modelStatus.QueuedRequests,
	}

	// Append sample
	modelStatus.QueueHistory = append(modelStatus.QueueHistory, sample)

	// Trim old samples
	cutoff := time.Now().Add(-TrendWindowDuration)
	var trimmed []aiv1alpha1.QueueSample
	for _, s := range modelStatus.QueueHistory {
		if s.Timestamp.After(cutoff) {
			trimmed = append(trimmed, s)
		}
	}
	modelStatus.QueueHistory = trimmed

	// Also enforce max size
	if len(modelStatus.QueueHistory) > MaxQueueHistorySize {
		modelStatus.QueueHistory = modelStatus.QueueHistory[len(modelStatus.QueueHistory)-MaxQueueHistorySize:]
	}
}

// calculateTrend analyzes queue history and updates trend fields
func (r *GPUGroupReconciler) calculateTrend(modelStatus *aiv1alpha1.GPUGroupModelStatus) {
	history := modelStatus.QueueHistory
	if len(history) < 3 {
		modelStatus.TrendDirection = "stable"
		modelStatus.TrendStability = 0
		return
	}

	// Calculate simple linear trend using differences
	var increases, decreases, stable int
	for i := 1; i < len(history); i++ {
		diff := history[i].Depth - history[i-1].Depth
		if diff > 0 {
			increases++
		} else if diff < 0 {
			decreases++
		} else {
			stable++
		}
	}

	total := increases + decreases + stable
	if total == 0 {
		modelStatus.TrendDirection = "stable"
		modelStatus.TrendStability = 100
		return
	}

	// Determine direction based on majority
	if increases > decreases && increases > stable {
		modelStatus.TrendDirection = "increasing"
		modelStatus.TrendStability = int32((increases * 100) / total)
	} else if decreases > increases && decreases > stable {
		modelStatus.TrendDirection = "decreasing"
		modelStatus.TrendStability = int32((decreases * 100) / total)
	} else {
		modelStatus.TrendDirection = "stable"
		modelStatus.TrendStability = int32((stable * 100) / total)
	}
}

// shouldSwapBasedOnTrend checks if trend analysis supports a swap decision
func (r *GPUGroupReconciler) shouldSwapBasedOnTrend(modelStatus *aiv1alpha1.GPUGroupModelStatus, minStability int32) bool {
	// Record current sample and recalculate trend
	r.recordQueueSample(modelStatus)
	r.calculateTrend(modelStatus)

	// For swap-in decisions, we want increasing or stable high demand
	if modelStatus.TrendDirection == "decreasing" && modelStatus.TrendStability > minStability {
		// Demand is clearly dropping, don't swap for this model
		return false
	}

	// For sustained demand, allow swap
	if modelStatus.TrendDirection == "increasing" && modelStatus.TrendStability > minStability {
		return true
	}

	// Stable with queued requests - allow swap
	if modelStatus.TrendDirection == "stable" && modelStatus.QueuedRequests > 0 {
		return true
	}

	// Low stability means unstable demand - be conservative
	return modelStatus.TrendStability < minStability
}

// updateMetrics updates Prometheus gauge metrics for Grafana dashboard.
func (r *GPUGroupReconciler) updateMetrics(gpuGroup *aiv1alpha1.GPUGroup, members map[string]*aiv1alpha1.ModelDeployment) {
	ns := gpuGroup.Namespace
	name := gpuGroup.Name
	activeModel := gpuGroup.Status.ActiveModel

	// Update active model gauge - set 1 for active, 0 for inactive
	for modelName := range members {
		if modelName == activeModel {
			gpuGroupActiveModel.WithLabelValues(name, modelName, ns).Set(1)
		} else {
			gpuGroupActiveModel.WithLabelValues(name, modelName, ns).Set(0)
		}
	}

	// Update model run duration for active model
	if activeModel != "" && gpuGroup.Status.LastSwapTime != nil {
		duration := time.Since(gpuGroup.Status.LastSwapTime.Time).Seconds()
		gpuGroupModelRunDuration.WithLabelValues(name, activeModel, ns).Set(duration)
	}

	// Update swap cooldown (time remaining)
	if gpuGroup.Spec.AntiThrashing.Enabled && gpuGroup.Status.LastSwapTime != nil {
		minRunDuration := float64(30) // default
		if gpuGroup.Spec.AntiThrashing.MinimumRunDurationSeconds > 0 {
			minRunDuration = float64(gpuGroup.Spec.AntiThrashing.MinimumRunDurationSeconds)
		}
		elapsed := time.Since(gpuGroup.Status.LastSwapTime.Time).Seconds()
		remaining := minRunDuration - elapsed
		if remaining < 0 {
			remaining = 0
		}
		gpuGroupSwapCooldown.WithLabelValues(name, ns).Set(remaining)
	} else {
		gpuGroupSwapCooldown.WithLabelValues(name, ns).Set(0)
	}

	// Update queue depth for each model from status
	for _, status := range gpuGroup.Status.ModelStatuses {
		gpuGroupModelQueueDepth.WithLabelValues(name, status.Name, ns).Set(float64(status.QueuedRequests))
	}
}

// SetupWithManager sets up the controller with the Manager.
func (r *GPUGroupReconciler) SetupWithManager(mgr ctrl.Manager) error {
	r.Recorder = mgr.GetEventRecorderFor("gpugroup-controller")
	r.APIReader = mgr.GetAPIReader()

	return ctrl.NewControllerManagedBy(mgr).
		For(&aiv1alpha1.GPUGroup{}).
		// Watch ModelDeployments that reference a GPUGroup
		Owns(&aiv1alpha1.ModelDeployment{}).
		Complete(r)
}
