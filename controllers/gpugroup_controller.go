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

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/tools/record"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log"

	aiv1alpha1 "github.com/flexinfer/flexinfer/api/v1alpha1"
)

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
		if r.shouldBlockSwap(gpuGroup, desiredActive) {
			log.Info("Swap blocked by anti-thrashing rules",
				"current", currentActive, "desired", desiredActive)
			// Requeue to check again after cooldown
			return ctrl.Result{RequeueAfter: 10 * time.Second}, r.updateStatus(ctx, gpuGroup)
		}

		// Perform the swap
		if err := r.performModelSwap(ctx, gpuGroup, members, currentActive, desiredActive, reason); err != nil {
			log.Error(err, "Failed to perform model swap")
			return ctrl.Result{RequeueAfter: 5 * time.Second}, err
		}
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

				// Add service labels if not already present
				if err := r.updateServiceLabels(ctx, md, true); err != nil {
					log.Error(err, "Failed to add service labels", "model", currentActive)
				}
			}
		}
	}

	// Update phase based on current state
	r.updatePhase(gpuGroup, members)

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

		// Check hysteresis window
		hysteresisWindow := 10 * time.Second // default
		if gpuGroup.Spec.AntiThrashing.HysteresisWindowSeconds > 0 {
			hysteresisWindow = time.Duration(gpuGroup.Spec.AntiThrashing.HysteresisWindowSeconds) * time.Second
		}

		if currentActive != "" && !winner.queuedSince.IsZero() && time.Since(winner.queuedSince) < hysteresisWindow && winner.name != currentActive {
			log.Info("Hysteresis window not elapsed",
				"model", winner.name, "queuedSince", winner.queuedSince, "window", hysteresisWindow)
			if currentActive != "" {
				return currentActive, "hysteresis window"
			}
			return "", "hysteresis window"
		}
	}

	return winner.name, fmt.Sprintf("demand: queue=%d, priority=%d", winner.queueDepth, winner.priority)
}

// shouldBlockSwap checks anti-thrashing rules to see if swap should be blocked
func (r *GPUGroupReconciler) shouldBlockSwap(gpuGroup *aiv1alpha1.GPUGroup, desiredActive string) bool {
	if !gpuGroup.Spec.AntiThrashing.Enabled {
		return false
	}

	// Check minimum run duration for current active model
	if gpuGroup.Status.ActiveModel != "" && gpuGroup.Status.LastSwapTime != nil {
		minRunDuration := 30 * time.Second // default
		if gpuGroup.Spec.AntiThrashing.MinimumRunDurationSeconds > 0 {
			minRunDuration = time.Duration(gpuGroup.Spec.AntiThrashing.MinimumRunDurationSeconds) * time.Second
		}

		runDuration := time.Since(gpuGroup.Status.LastSwapTime.Time)
		if runDuration < minRunDuration {
			return true
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
				return true
			}
		}
	}

	return false
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

				// Check if deployment has terminated
				// In a real implementation, we'd watch for the deployment to have 0 ready replicas
				// For now, we'll proceed and let the next reconciliation verify
				_ = drainTimeout
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
