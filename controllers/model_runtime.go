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
	stderrors "errors"
	"fmt"
	"net"
	"strings"
	"time"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	apiequality "k8s.io/apimachinery/pkg/api/equality"
	k8serrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/log"

	aiv1alpha2 "github.com/flexinfer/flexinfer/api/v1alpha2"
	"github.com/flexinfer/flexinfer/backend"
	"github.com/flexinfer/flexinfer/pkg/k8surl"
	pkgrt "github.com/flexinfer/flexinfer/pkg/runtime"
)

// loadViaRuntime sends a model load request to a runtime pod instead of
// creating a Deployment. This path is used when a persistent runtime
// container exists on the target GPU node.
func (r *ModelReconciler) loadViaRuntime(ctx context.Context, model *aiv1alpha2.Model, b backend.Backend, gpuVendor backend.GPUVendor, endpoint *RuntimeEndpoint, gpuArch string) error {
	runtimeLog := log.FromContext(ctx)

	var profile *aiv1alpha2.GPUProfileSpec
	if r.GPUProfiles != nil {
		if cached, ok := r.GPUProfiles.Lookup(gpuArch); ok {
			profile = cached
		}
	}

	// LoRA support flows through the payload config so the runtime launches vLLM
	// with the same --enable-lora flags a dedicated Deployment would get. Without
	// this, a runtime-managed model can never hot-load an adapter.
	var loraCount, loraMaxRank int
	if ls, ok := b.(backend.LoRASupporter); ok && ls.SupportsLoRA() {
		loraCount, loraMaxRank = r.loraRuntimeConfig(ctx, model)
	}

	// Build the load request payload using the shared runtime launch builder.
	// This keeps the reconcile path aligned with the proxy fast path.
	data, err := pkgrt.BuildLoadPayloadForModel(model, b, pkgrt.BuildLoadOptions{
		ModelBasePath: "/models",
		GPUVendor:     gpuVendor,
		GPUProfile:    profile,
		LoRAAdapters:  loraCount,
		LoRAMaxRank:   loraMaxRank,
	})
	if err != nil {
		return err
	}

	runtimeLog.Info("Sending load request to runtime",
		"model", model.Name,
		"backend", b.Name(),
		"runtimeURL", endpoint.URL(),
	)

	return r.Runtime.LoadModel(ctx, endpoint, model.Name, data)
}

// reconcileViaRuntime manages a model's lifecycle via the runtime API.
// When a runtime pod exists on the target node, this completely replaces
// the Deployment-based flow: the runtime pod already holds the GPU device.
func (r *ModelReconciler) reconcileViaRuntime(
	ctx context.Context,
	model *aiv1alpha2.Model,
	b backend.Backend,
	gpuVendor backend.GPUVendor,
	gpuArch string,
	endpoint *RuntimeEndpoint,
	desiredReplicas int32,
	cacheReady bool,
	requeueAfter time.Duration,
) (ctrl.Result, error) {
	log := log.FromContext(ctx)

	log.Info("Reconciling model via runtime",
		"node", endpoint.NodeName,
		"runtimePod", endpoint.PodName,
		"desiredReplicas", desiredReplicas,
		"cacheReady", cacheReady,
	)

	runtimeBackendPort := pkgrt.RuntimePortForBackend(b)
	if err := r.ensureRuntimeNetworking(ctx, model, b, runtimeBackendPort); err != nil {
		log.Error(err, "Failed to ensure runtime networking for model")
		return ctrl.Result{}, err
	}

	// Drop any Endpoints left pointing at a former runtime pod. The only valid
	// address for a runtime-served model is the current runtime pod; an address
	// mismatch means the runtime pod was replaced (restart/reschedule) and the
	// stale Endpoints now route traffic to a dead pod IP — every request 502s
	// (or hangs) until the model reloads. Clearing here is always safe: a
	// cross-pod address is unreachable by definition, and the Ready path below
	// re-creates the correct Endpoints once the model is confirmed loaded on the
	// current pod. Guarded on an IP mismatch so a transient health-check blip
	// (which leaves the pod IP unchanged) never clears a live endpoint.
	r.clearStaleRuntimeEndpoints(ctx, model, endpoint.PodIP)

	if err := r.deleteLegacyDeploymentForRuntime(ctx, model); err != nil {
		log.Error(err, "Failed to remove legacy Deployment for runtime-managed model")
		return ctrl.Result{}, err
	}

	// Record placement (status.gpu): the runtime pod on endpoint.NodeName holds
	// the GPU this model serves from. Every status write below persists it, so
	// consumers can attribute the model to a node without parsing spec
	// scheduling hints.
	setGPUStatus(model, endpoint.NodeName, gpuVendor, gpuArch)

	// Preserve in-flight runtime loads unless the model has already been
	// preempted. This avoids tearing down a load that the runtime is already
	// progressing through because cache state or desired replicas briefly
	// changed under us.
	if status, err := r.Runtime.CheckModelHealth(ctx, endpoint, model.Name); err == nil && status != nil && status.State == "Loading" && model.Status.Phase != aiv1alpha2.ModelPhasePreempted {
		if err := r.updateRuntimeStatus(ctx, model, aiv1alpha2.ModelPhaseLoading, false, "RuntimeLoading", "Model is loading via runtime"); err != nil {
			log.Error(err, "Failed to preserve loading phase for runtime-managed model")
		}
		return ctrl.Result{RequeueAfter: requeueShort}, nil
	}

	// If we don't want the model running, unload it from the runtime.
	if desiredReplicas == 0 || !cacheReady {
		r.unloadFromRuntime(ctx, model, endpoint)
		if model.Status.Phase == aiv1alpha2.ModelPhasePreempted {
			model.Status.Endpoint = ""
			// Preserve the lease-vs-preempt distinction the election recorded in
			// SharedGroup.PreemptedBy; otherwise this later status write would
			// clobber it back to a generic "Preempted" on the same reconcile.
			reason, message := preemptedConditionReasonMessage(model)
			if !cacheReady {
				reason = "CacheNotReady"
				message = "Waiting for cache to be ready"
			}
			setModelCondition(model, aiv1alpha2.ConditionModelReady, false, reason, message)
			r.removeRuntimeEndpoints(ctx, model)
			if err := r.Status().Update(ctx, model); err != nil {
				log.Error(err, "Failed to update preempted runtime status")
			}
			return ctrl.Result{RequeueAfter: requeueAfter}, nil
		} else {
			phase := aiv1alpha2.ModelPhaseIdle
			reason := aiv1alpha2.ReasonWaitingForActivation
			message := "Model is idle, waiting for traffic"
			if !cacheReady {
				phase = aiv1alpha2.ModelPhasePending
				reason = "CacheNotReady"
				message = "Waiting for cache to be ready"
			}
			if err := r.updateRuntimeStatus(ctx, model, phase, false, reason, message); err != nil {
				log.Error(err, "Failed to update phase")
			}
		}
		// Clean up any Endpoints that pointed to a previous runtime session.
		r.removeRuntimeEndpoints(ctx, model)
		return ctrl.Result{RequeueAfter: requeueAfter}, nil
	}

	// desiredReplicas > 0 and cache is ready — check if model is loaded.
	status, err := r.Runtime.CheckModelHealth(ctx, endpoint, model.Name)
	if err != nil {
		log.Error(err, "Failed to check runtime model health")
		status = nil // Can't determine state — try to load anyway.
	}

	if status == nil {
		if shouldDeferRuntimeLoadRetry(model) {
			log.V(1).Info("Deferring duplicate runtime load request while prior load is still settling",
				"model", model.Name,
				"backoff", runtimeLoadRetryBackoff,
			)
			return ctrl.Result{RequeueAfter: requeueShort}, nil
		}
		if deferLoad, activeName, err := r.shouldDeferRuntimeLoadForActivePeer(ctx, model, endpoint, time.Now()); err != nil {
			log.V(1).Info("Could not evaluate active runtime peer before load", "model", model.Name, "error", err)
		} else if deferLoad {
			message := fmt.Sprintf("Runtime is currently serving or loading %s", activeName)
			log.Info("Deferring runtime load while active peer is protected",
				"model", model.Name,
				"activePeer", activeName,
			)
			if err := r.updateRuntimeStatus(ctx, model, aiv1alpha2.ModelPhasePending, false, "RuntimeBusy", message); err != nil {
				log.Error(err, "Failed to update phase while deferring runtime load")
			}
			return ctrl.Result{RequeueAfter: requeueShort}, nil
		}

		// Model not loaded — send load request.
		if err := r.loadViaRuntime(ctx, model, b, gpuVendor, endpoint, gpuArch); err != nil {
			log.Error(err, "Failed to load model via runtime")
			r.Recorder.Event(model, corev1.EventTypeWarning, "RuntimeLoadFailed", err.Error())
			if isTransientRuntimeLoadError(err) {
				if err := r.updateRuntimeStatus(ctx, model, aiv1alpha2.ModelPhaseLoading, false, "RuntimeStarting", "Runtime pod is starting; retrying model load"); err != nil {
					log.Error(err, "Failed to update phase after transient runtime load failure")
				}
				return ctrl.Result{RequeueAfter: requeueShort}, nil
			}
			if err := r.updateRuntimeStatus(ctx, model, aiv1alpha2.ModelPhaseFailed, false, "RuntimeLoadFailed", err.Error()); err != nil {
				log.Error(err, "failed to update Model status", "model", model.Name)
			}
			return ctrl.Result{RequeueAfter: requeueAfter}, nil
		}
		if err := r.updateRuntimeStatus(ctx, model, aiv1alpha2.ModelPhaseLoading, false, "RuntimeLoading", "Model is loading via runtime"); err != nil {
			log.Error(err, "Failed to update phase after runtime load")
		}
		// Requeue quickly to poll for readiness.
		return ctrl.Result{RequeueAfter: requeueShort}, nil
	}

	// Model is loaded — update status based on runtime state.
	switch status.State {
	case "Ready":
		// Update Endpoints to point to the runtime pod's backend port.
		port := status.Port
		if port == 0 {
			port = runtimeBackendPort
		}
		if err := r.ensureRuntimeEndpoints(ctx, model, endpoint.PodIP, port); err != nil {
			log.Error(err, "Failed to ensure runtime endpoints")
			return ctrl.Result{}, err
		}
		model.Status.Endpoint = k8surl.ServiceURL(model.Name, model.Namespace, port, false)
		setModelCondition(model, aiv1alpha2.ConditionModelReady, true, "RuntimeReady", "Model ready via runtime")
		if model.Status.Phase != aiv1alpha2.ModelPhaseReady {
			r.Recorder.Event(model, corev1.EventTypeNormal, "RuntimeReady", "Model is ready via runtime")
		}
		model.Status.Phase = aiv1alpha2.ModelPhaseReady
		clearLoadingStatus(model)
		if err := r.Status().Update(ctx, model); err != nil {
			log.Error(err, "Failed to update status")
			return ctrl.Result{}, err
		}
	case "Loading":
		if err := r.updateRuntimeStatus(ctx, model, aiv1alpha2.ModelPhaseLoading, false, "RuntimeLoading", "Model is loading via runtime"); err != nil {
			log.Error(err, "Failed to update phase")
		}
		return ctrl.Result{RequeueAfter: requeueShort}, nil
	case "Failed":
		r.removeRuntimeEndpoints(ctx, model)
		model.Status.Endpoint = ""
		model.Status.Phase = aiv1alpha2.ModelPhaseFailed
		clearLoadingStatus(model)
		setModelCondition(model, aiv1alpha2.ConditionModelReady, false, "RuntimeFailed", status.Error)
		r.Recorder.Event(model, corev1.EventTypeWarning, "RuntimeFailed", status.Error)
		if err := r.Status().Update(ctx, model); err != nil {
			log.Error(err, "Failed to update status")
			return ctrl.Result{}, err
		}
	default:
		log.Info("Unknown runtime model state", "state", status.State)
	}

	return ctrl.Result{RequeueAfter: requeueAfter}, nil
}

func (r *ModelReconciler) shouldDeferRuntimeLoadForActivePeer(
	ctx context.Context,
	model *aiv1alpha2.Model,
	endpoint *RuntimeEndpoint,
	now time.Time,
) (bool, string, error) {
	if model == nil || model.Spec.IsForcePromoted() || r.Runtime == nil {
		return false, "", nil
	}

	status, err := r.Runtime.GetStatus(ctx, endpoint)
	if err != nil {
		return false, "", err
	}
	if status == nil || status.ActiveModel == nil || status.ActiveModel.Name == "" || status.ActiveModel.Name == model.Name {
		return false, "", nil
	}

	active := &aiv1alpha2.Model{}
	key := types.NamespacedName{Name: status.ActiveModel.Name, Namespace: model.Namespace}
	if err := r.Get(ctx, key, active); err != nil {
		if k8serrors.IsNotFound(err) {
			return false, status.ActiveModel.Name, nil
		}
		return false, status.ActiveModel.Name, err
	}

	if runtimeActivePeerProtected(active, now) && !runtimeModelHasRecentDemand(model, now) {
		return true, active.Name, nil
	}

	return false, active.Name, nil
}

func runtimeActivePeerProtected(model *aiv1alpha2.Model, now time.Time) bool {
	if model == nil {
		return false
	}
	if isActiveSharedModelLoading(model) && withinSharedActivationWindow(model, now) {
		return true
	}
	return runtimeModelHasRecentDemand(model, now)
}

func runtimeModelHasRecentDemand(model *aiv1alpha2.Model, now time.Time) bool {
	return model != nil &&
		model.Status.LastActiveTime != nil &&
		now.Sub(model.Status.LastActiveTime.Time) < sharedDemandWindow
}

func isTransientRuntimeLoadError(err error) bool {
	if err == nil {
		return false
	}
	if stderrors.Is(err, context.DeadlineExceeded) || stderrors.Is(err, context.Canceled) {
		return true
	}
	var netErr net.Error
	if stderrors.As(err, &netErr) {
		return true
	}
	msg := err.Error()
	return strings.Contains(msg, "connection refused") ||
		strings.Contains(msg, "Client.Timeout exceeded") ||
		strings.Contains(msg, "EOF")
}

func shouldDeferRuntimeLoadRetry(model *aiv1alpha2.Model) bool {
	if model == nil || model.Status.Phase != aiv1alpha2.ModelPhaseLoading {
		return false
	}

	ready := modelCondition(model.Status.Conditions, aiv1alpha2.ConditionModelReady)
	if ready == nil || ready.Status != metav1.ConditionFalse {
		return false
	}
	if ready.ObservedGeneration != model.Generation {
		return false
	}
	if ready.Reason != "RuntimeLoading" && ready.Reason != "RuntimeStarting" {
		return false
	}
	if ready.LastTransitionTime.IsZero() {
		return false
	}

	return time.Since(ready.LastTransitionTime.Time) < runtimeLoadRetryBackoff
}

func (r *ModelReconciler) updateRuntimeStatus(
	ctx context.Context,
	model *aiv1alpha2.Model,
	phase aiv1alpha2.ModelPhase,
	ready bool,
	reason, message string,
) error {
	oldPhase := model.Status.Phase
	model.Status.Phase = phase
	model.Status.Endpoint = ""
	if phase == aiv1alpha2.ModelPhaseLoading {
		if model.Status.LoadingSubstage != aiv1alpha2.LoadingSubstageInitializing || model.Status.Message != message {
			now := metav1.Now()
			model.Status.LoadingProgressAt = &now
		}
		model.Status.LoadingSubstage = aiv1alpha2.LoadingSubstageInitializing
		model.Status.Message = message
	} else {
		clearLoadingStatus(model)
	}
	setModelCondition(model, aiv1alpha2.ConditionModelReady, ready, reason, message)
	r.recordPhaseMetrics(model, oldPhase, phase)
	r.removeRuntimeEndpoints(ctx, model)
	return r.Status().Update(ctx, model)
}

func (r *ModelReconciler) ensureRuntimeNetworking(ctx context.Context, model *aiv1alpha2.Model, b backend.Backend, port int32) error {
	// enforcePort=true: the runtime path is authoritative over the Service port
	// for runtime-managed models. The early ensureService call (enforcePort=false)
	// must not fight this once the selector has been cleared below.
	if err := r.ensureServiceWithPort(ctx, model, b, port, true); err != nil {
		return err
	}
	return r.removeRuntimeServiceSelector(ctx, model)
}

func (r *ModelReconciler) removeRuntimeServiceSelector(ctx context.Context, model *aiv1alpha2.Model) error {
	log := log.FromContext(ctx)

	svc := &corev1.Service{}
	if err := r.Get(ctx, types.NamespacedName{Name: model.Name, Namespace: model.Namespace}, svc); err != nil {
		return fmt.Errorf("getting service for runtime management: %w", err)
	}
	if svc.Spec.Selector == nil {
		return nil
	}

	svc.Spec.Selector = nil
	if err := r.Update(ctx, svc); err != nil {
		return fmt.Errorf("removing selector from service: %w", err)
	}
	log.Info("Removed selector from Service for runtime management", "service", svc.Name)
	return nil
}

func (r *ModelReconciler) deleteLegacyDeploymentForRuntime(ctx context.Context, model *aiv1alpha2.Model) error {
	log := log.FromContext(ctx)

	deployment := &appsv1.Deployment{}
	key := types.NamespacedName{Name: model.Name, Namespace: model.Namespace}
	if err := r.Get(ctx, key, deployment); err != nil {
		if k8serrors.IsNotFound(err) {
			return nil
		}
		return fmt.Errorf("getting legacy deployment: %w", err)
	}

	if err := r.Delete(ctx, deployment); err != nil && !k8serrors.IsNotFound(err) {
		return fmt.Errorf("deleting legacy deployment: %w", err)
	}
	log.Info("Deleted legacy Deployment for runtime-managed model", "deployment", deployment.Name)
	return nil
}

// unloadFromRuntime sends an unload request to the runtime if the model is loaded.
func (r *ModelReconciler) unloadFromRuntime(ctx context.Context, model *aiv1alpha2.Model, endpoint *RuntimeEndpoint) {
	log := log.FromContext(ctx)

	// Check if the model is actually loaded.
	status, err := r.Runtime.CheckModelHealth(ctx, endpoint, model.Name)
	if err != nil {
		log.V(1).Info("Could not check runtime model health for unload", "error", err)
	}
	if status == nil {
		return // Not loaded — nothing to do.
	}

	log.Info("Unloading model from runtime", "model", model.Name)
	if err := r.Runtime.UnloadModel(ctx, endpoint, model.Name); err != nil {
		log.Error(err, "Failed to unload model from runtime")
	}
}

// ensureRuntimeEndpoints creates or updates an Endpoints resource for a
// runtime-managed model, pointing to the runtime pod's backend port.
// The model's Service must not have a selector for this to take effect.
func (r *ModelReconciler) ensureRuntimeEndpoints(ctx context.Context, model *aiv1alpha2.Model, podIP string, port int32) error {
	log := log.FromContext(ctx)

	// First, ensure the Service has no selector (runtime manages endpoints manually).
	if err := r.removeRuntimeServiceSelector(ctx, model); err != nil {
		return fmt.Errorf("ensuring runtime service selector removed: %w", err)
	}

	// Build desired Endpoints.
	desired := &corev1.Endpoints{
		ObjectMeta: metav1.ObjectMeta{
			Name:      model.Name,
			Namespace: model.Namespace,
			Labels:    r.labelsForModel(model),
		},
		Subsets: []corev1.EndpointSubset{
			{
				Addresses: []corev1.EndpointAddress{
					{IP: podIP},
				},
				Ports: []corev1.EndpointPort{
					{
						Name:     "http",
						Port:     port,
						Protocol: corev1.ProtocolTCP,
					},
				},
			},
		},
	}

	existing := &corev1.Endpoints{}
	err := r.Get(ctx, types.NamespacedName{Name: model.Name, Namespace: model.Namespace}, existing)
	if k8serrors.IsNotFound(err) {
		log.Info("Creating runtime Endpoints", "name", model.Name, "podIP", podIP, "port", port)
		return r.Create(ctx, desired)
	}
	if err != nil {
		return err
	}

	// Update if subsets differ.
	if !apiequality.Semantic.DeepEqual(existing.Subsets, desired.Subsets) {
		existing.Subsets = desired.Subsets
		log.Info("Updating runtime Endpoints", "name", model.Name, "podIP", podIP)
		return r.Update(ctx, existing)
	}

	return nil
}

// clearStaleRuntimeEndpoints removes Endpoints subsets that point at any pod IP
// other than the current runtime pod. A runtime-served model can only be served
// from the current runtime pod, so any other address is a leftover from a former
// pod (restart / reschedule) that now routes to a dead IP. Subsets that already
// match currentPodIP are left untouched, so a transient health-check failure
// (which does not change the pod IP) never tears down a live endpoint. The
// caller's Ready path re-creates the correct Endpoints once the model is
// confirmed loaded on the current pod.
func (r *ModelReconciler) clearStaleRuntimeEndpoints(ctx context.Context, model *aiv1alpha2.Model, currentPodIP string) {
	log := log.FromContext(ctx)

	if currentPodIP == "" {
		return // Can't classify staleness without a current pod IP.
	}

	ep := &corev1.Endpoints{}
	if err := r.Get(ctx, types.NamespacedName{Name: model.Name, Namespace: model.Namespace}, ep); err != nil {
		return // Not found or error — nothing to clean up.
	}

	hasStale := false
	for _, ss := range ep.Subsets {
		for _, addr := range ss.Addresses {
			if addr.IP != currentPodIP {
				hasStale = true
			}
		}
	}
	if !hasStale {
		return
	}

	log.Info("Clearing stale runtime Endpoints pointing at a former runtime pod",
		"model", model.Name, "currentPodIP", currentPodIP)
	ep.Subsets = nil
	if err := r.Update(ctx, ep); err != nil {
		log.Error(err, "Failed to clear stale runtime endpoints", "model", model.Name)
	}
}

// removeRuntimeEndpoints clears the Endpoints subsets for a runtime-managed
// model so the proxy stops routing to it.
func (r *ModelReconciler) removeRuntimeEndpoints(ctx context.Context, model *aiv1alpha2.Model) {
	log := log.FromContext(ctx)

	ep := &corev1.Endpoints{}
	if err := r.Get(ctx, types.NamespacedName{Name: model.Name, Namespace: model.Namespace}, ep); err != nil {
		return // Not found or error — nothing to clean up.
	}

	if len(ep.Subsets) > 0 {
		ep.Subsets = nil
		if err := r.Update(ctx, ep); err != nil {
			log.Error(err, "Failed to clear runtime endpoints", "model", model.Name)
		}
	}
}
