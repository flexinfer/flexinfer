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
	"strings"
	"time"

	appsv1 "k8s.io/api/apps/v1"
	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/tools/record"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/log"

	aiv1alpha2 "github.com/flexinfer/flexinfer/api/v1alpha2"
	"github.com/flexinfer/flexinfer/backend"
	"github.com/flexinfer/flexinfer/pkg/observability"
	pkgrt "github.com/flexinfer/flexinfer/pkg/runtime"
)

// ---------------------------------------------------------------------------
// Error types for GPU detection
// ---------------------------------------------------------------------------

type noMatchingNodesError struct {
	reason string
}

func (e *noMatchingNodesError) Error() string { return e.reason }

func isNoMatchingNodesError(err error) bool {
	var e *noMatchingNodesError
	return stderrors.As(err, &e)
}

type ambiguousGPUVendorError struct {
	reason string
}

func (e *ambiguousGPUVendorError) Error() string { return e.reason }

func isAmbiguousGPUVendorError(err error) bool {
	var e *ambiguousGPUVendorError
	return stderrors.As(err, &e)
}

// ---------------------------------------------------------------------------
// ModelReconciler
// ---------------------------------------------------------------------------

// ModelReconciler reconciles a Model object
type ModelReconciler struct {
	client.Client
	Scheme      *runtime.Scheme
	Recorder    record.EventRecorder
	APIReader   client.Reader
	GPUProfiles *GPUProfileReconciler
	// Runtime provides runtime pod discovery for direct model loading.
	// When non-nil and a runtime pod exists on the target node, models
	// are loaded via the runtime API instead of creating Deployments.
	Runtime *RuntimeReconciler
}

//+kubebuilder:rbac:groups=ai.flexinfer,resources=models,verbs=get;list;watch;create;update;patch;delete
//+kubebuilder:rbac:groups=ai.flexinfer,resources=models/status,verbs=get;update;patch
//+kubebuilder:rbac:groups=ai.flexinfer,resources=models/finalizers,verbs=update
//+kubebuilder:rbac:groups=apps,resources=deployments,verbs=get;list;watch;create;update;patch;delete
//+kubebuilder:rbac:groups=core,resources=services,verbs=get;list;watch;create;update;patch;delete
//+kubebuilder:rbac:groups=core,resources=persistentvolumeclaims,verbs=get;list;watch;create;update;patch;delete
//+kubebuilder:rbac:groups=batch,resources=jobs,verbs=get;list;watch;create;update;patch;delete
//+kubebuilder:rbac:groups="",resources=nodes,verbs=get;list;watch

// Reconcile is the main reconciliation loop for Model resources.
func (r *ModelReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	ctx, recordErr, endSpan := observability.StartReconcileSpan(ctx, "model", req.Namespace, req.Name)
	defer endSpan()

	log := log.FromContext(ctx)

	// Fetch the Model instance
	model := &aiv1alpha2.Model{}
	err := r.Get(ctx, req.NamespacedName, model)
	if err != nil {
		if errors.IsNotFound(err) {
			log.Info("Model resource not found, ignoring since object must be deleted")
			return ctrl.Result{}, nil
		}
		recordErr(err)
		log.Error(err, "Failed to get Model")
		return ctrl.Result{}, err
	}

	// Handle finalizer
	if model.DeletionTimestamp.IsZero() {
		if !containsString(model.GetFinalizers(), aiv1alpha2.ModelFinalizer) {
			model.SetFinalizers(append(model.GetFinalizers(), aiv1alpha2.ModelFinalizer))
			if err := r.Update(ctx, model); err != nil {
				log.Error(err, "Failed to add finalizer")
				return ctrl.Result{}, err
			}
			return ctrl.Result{Requeue: true}, nil
		}
	} else {
		// Object is being deleted
		if containsString(model.GetFinalizers(), aiv1alpha2.ModelFinalizer) {
			if err := r.cleanupModel(ctx, model); err != nil {
				log.Error(err, "Failed to cleanup Model resources")
				return ctrl.Result{}, err
			}
			model.SetFinalizers(removeString(model.GetFinalizers(), aiv1alpha2.ModelFinalizer))
			if err := r.Update(ctx, model); err != nil {
				log.Error(err, "Failed to remove finalizer")
				return ctrl.Result{}, err
			}
		}
		return ctrl.Result{}, nil
	}

	// Validate backend
	b, ok := backend.Get(model.Spec.Backend)
	if !ok {
		err := fmt.Errorf("%w: %s", backend.ErrUnknownBackend, model.Spec.Backend)
		log.Error(err, "Backend validation failed")
		r.Recorder.Event(model, corev1.EventTypeWarning, "ValidationFailed", err.Error())
		return ctrl.Result{}, r.updatePhase(ctx, model, aiv1alpha2.ModelPhaseFailed)
	}

	// Check for litellm alias conflicts across all models in the namespace.
	// This is a warning condition, not a blocker — the model still reconciles.
	r.checkAliasConflicts(ctx, model)

	desiredReplicas := r.desiredReplicasForContext(ctx, model, b)
	requeueAfter := requeueLong

	// Initialize status based on desired state.
	if model.Status.Phase == "" {
		phase := aiv1alpha2.ModelPhasePending
		if desiredReplicas == 0 {
			phase = aiv1alpha2.ModelPhaseIdle
		}
		if err := r.updatePhase(ctx, model, phase); err != nil {
			log.Error(err, "Failed to update initial status")
			return ctrl.Result{}, err
		}
		return ctrl.Result{Requeue: true}, nil
	}

	// Handle shared GPU scheduling
	if model.Spec.IsShared() {
		result, err := r.handleSharedGPU(ctx, model)
		if err != nil {
			log.Error(err, "Failed to handle shared GPU scheduling")
			return result, err
		}
		// Continue reconciliation even if queued/preempted; desiredReplicas will keep it at 0.
		desiredReplicas = r.desiredReplicasForContext(ctx, model, b)
		if result.Requeue {
			return result, nil
		}
		if result.RequeueAfter > 0 && result.RequeueAfter < requeueAfter {
			requeueAfter = result.RequeueAfter
		}
	}

	// Detect GPU info from node
	gpuVendor, gpuArch, err := r.detectGPU(ctx, model)
	if err != nil {
		log.Error(err, "Failed to detect GPU")
		r.Recorder.Event(model, corev1.EventTypeWarning, "GPUDetectionFailed", err.Error())
		if isNoMatchingNodesError(err) {
			setModelCondition(model, aiv1alpha2.ConditionModelSchedulable, false, aiv1alpha2.ReasonNoMatchingNodes, err.Error())
			model.Status.Phase = aiv1alpha2.ModelPhaseFailed
			if err := r.Status().Update(ctx, model); err != nil {
				return ctrl.Result{}, err
			}
			return ctrl.Result{RequeueAfter: requeueAfter}, nil
		}
		if isAmbiguousGPUVendorError(err) {
			setModelCondition(model, aiv1alpha2.ConditionModelSchedulable, false, aiv1alpha2.ReasonAmbiguousGPUVendor, err.Error())
			model.Status.Phase = aiv1alpha2.ModelPhaseFailed
			if err := r.Status().Update(ctx, model); err != nil {
				return ctrl.Result{}, err
			}
			return ctrl.Result{RequeueAfter: requeueAfter}, nil
		}
		return ctrl.Result{}, err
	}

	// Validate backend against selected vendor (especially CPU-only mode).
	if !b.SupportsGPUVendor(gpuVendor) {
		err := fmt.Errorf("backend %q does not support vendor %q", b.Name(), gpuVendor)
		log.Error(err, "Backend vendor validation failed")
		r.Recorder.Event(model, corev1.EventTypeWarning, "ValidationFailed", err.Error())
		setModelCondition(model, aiv1alpha2.ConditionModelSchedulable, false, aiv1alpha2.ReasonBackendUnsupported, err.Error())
		model.Status.Phase = aiv1alpha2.ModelPhaseFailed
		return ctrl.Result{}, r.Status().Update(ctx, model)
	}

	// Validate backend against selected GPU arch (e.g., vLLM does not support Maxwell).
	if err := r.validateBackendGPUCompatibility(model, b, gpuVendor, gpuArch); err != nil {
		log.Error(err, "Backend GPU compatibility validation failed")
		r.Recorder.Event(model, corev1.EventTypeWarning, "ValidationFailed", err.Error())
		setModelCondition(model, aiv1alpha2.ConditionModelSchedulable, false, aiv1alpha2.ReasonBackendUnsupported, err.Error())
		model.Status.Phase = aiv1alpha2.ModelPhaseFailed
		return ctrl.Result{}, r.Status().Update(ctx, model)
	}

	// Validate VRAM fit if the model declares an estimate.
	if err := r.validateVRAMFit(model, b, gpuArch); err != nil {
		log.Error(err, "VRAM fit validation failed")
		r.Recorder.Event(model, corev1.EventTypeWarning, "VRAMInsufficient", err.Error())
		setModelCondition(model, aiv1alpha2.ConditionModelSchedulable, false, aiv1alpha2.ReasonVRAMInsufficient, err.Error())
		model.Status.Phase = aiv1alpha2.ModelPhaseFailed
		return ctrl.Result{}, r.Status().Update(ctx, model)
	}

	// Emit informational events for experimental opt-ins (vLLM V1 engine, flash attention).
	if b.Name() == "vllm" || b.Name() == "vllm-omni" {
		r.emitVLLMOptInEvents(model)
	}

	// Backend-specific validation: llama.cpp needs an actual GGUF file path.
	if b.Name() == "llamacpp" {
		if strings.HasPrefix(model.Spec.Source, "ollama://") {
			err := fmt.Errorf("llamacpp backend does not support ollama:// sources; use HF:// (with config.ggufFile), pvc://, or file:// instead")
			log.Error(err, "Backend source validation failed")
			r.Recorder.Event(model, corev1.EventTypeWarning, "ValidationFailed", err.Error())
			setModelCondition(model, aiv1alpha2.ConditionModelSchedulable, false, aiv1alpha2.ReasonBackendUnsupported, err.Error())
			model.Status.Phase = aiv1alpha2.ModelPhaseFailed
			return ctrl.Result{}, r.Status().Update(ctx, model)
		}
		if strings.HasPrefix(model.Spec.Source, "HF://") {
			cfg := model.Spec.GetConfigMap()
			ggufFile := ""
			if cfg != nil {
				if v, ok := cfg["ggufFile"]; ok {
					if s, ok := v.(string); ok {
						ggufFile = s
					}
				}
				if strings.TrimSpace(ggufFile) == "" {
					if v, ok := cfg["modelFile"]; ok {
						if s, ok := v.(string); ok {
							ggufFile = s
						}
					}
				}
			}
			ggufFile = strings.TrimSpace(ggufFile)
			if ggufFile == "" {
				err := fmt.Errorf("llamacpp with HF:// source requires spec.config.ggufFile (a .gguf filename within the repo)")
				log.Error(err, "Backend source validation failed")
				r.Recorder.Event(model, corev1.EventTypeWarning, "ValidationFailed", err.Error())
				setModelCondition(model, aiv1alpha2.ConditionModelSchedulable, false, aiv1alpha2.ReasonBackendUnsupported, err.Error())
				model.Status.Phase = aiv1alpha2.ModelPhaseFailed
				return ctrl.Result{}, r.Status().Update(ctx, model)
			}
			if !strings.HasSuffix(strings.ToLower(ggufFile), ".gguf") {
				err := fmt.Errorf("spec.config.ggufFile must end with .gguf (got %q)", ggufFile)
				log.Error(err, "Backend source validation failed")
				r.Recorder.Event(model, corev1.EventTypeWarning, "ValidationFailed", err.Error())
				setModelCondition(model, aiv1alpha2.ConditionModelSchedulable, false, aiv1alpha2.ReasonBackendUnsupported, err.Error())
				model.Status.Phase = aiv1alpha2.ModelPhaseFailed
				return ctrl.Result{}, r.Status().Update(ctx, model)
			}
		}
	}

	// GPU detection succeeded - model is schedulable
	setModelCondition(model, aiv1alpha2.ConditionModelSchedulable, true, aiv1alpha2.ReasonSchedulable, "Model can be scheduled on available nodes")

	// Ensure Service exists early (needed for stable endpoints even while cache is warming).
	if err := r.ensureService(ctx, model, b); err != nil {
		log.Error(err, "Failed to ensure Service")
		return ctrl.Result{}, err
	}

	// Ensure cache resources exist (PVC provisioning) before creating the Deployment.
	cacheReady, err := r.ensureCache(ctx, model, b)
	if err != nil {
		log.Error(err, "Failed to ensure cache resources")
		r.Recorder.Event(model, corev1.EventTypeWarning, "CacheFailed", err.Error())
		return ctrl.Result{}, err
	}

	// Gate activation on cache readiness: keep replicas at 0 while a prefetch job is running/failed.
	if !cacheReady {
		desiredReplicas = 0
		if model.Status.Phase != aiv1alpha2.ModelPhasePreempted && model.Status.Phase != aiv1alpha2.ModelPhaseFailed {
			if err := r.updatePhase(ctx, model, aiv1alpha2.ModelPhasePending); err != nil {
				// Non-fatal: this is a best-effort status update while cache warms.
				log.Error(err, "Failed to update Model phase to Pending while cache is warming")
			}
		}
	}

	// Check if a persistent runtime exists on the target node.
	// If so, manage the model via the runtime API instead of creating a Deployment.
	// The runtime pod already holds the GPU device, so Deployment creation is skipped.
	if r.Runtime != nil {
		if ok, reason := pkgrt.DirectRuntimeLoadEligibility(model); !ok {
			log.V(1).Info("Skipping runtime-managed flow for model", "reason", reason)
		} else {
			runtimeEndpoint, err := r.Runtime.FindRuntimeForNode(ctx, model.Namespace, model.Spec.NodeSelector)
			if err != nil {
				log.V(1).Info("Runtime discovery failed, falling back to Deployment", "error", err)
			}
			if runtimeEndpoint != nil {
				if runtimeEndpoint.Ready {
					return r.reconcileViaRuntime(ctx, model, b, gpuVendor, gpuArch, runtimeEndpoint, desiredReplicas, cacheReady, requeueAfter)
				}
				// Runtime pod exists but isn't ready yet (starting up). Wait instead
				// of falling back to Deployment, which would claim the GPU and prevent
				// the runtime pod from scheduling (deadlock).
				log.Info("Runtime pod exists but not ready, waiting for it to start",
					"model", model.Name,
					"runtimePod", runtimeEndpoint.PodName,
				)
				return ctrl.Result{RequeueAfter: requeueMedium}, nil
			}
		}
	}

	// No runtime available — use Deployment-based flow.
	// Ensure Deployment exists with correct spec
	if err := r.ensureDeployment(ctx, model, b, gpuVendor, gpuArch, desiredReplicas); err != nil {
		log.Error(err, "Failed to ensure Deployment")
		return ctrl.Result{}, err
	}

	// Update status based on deployment state
	if err := r.updateStatusFromDeployment(ctx, model); err != nil {
		log.Error(err, "Failed to update status")
		return ctrl.Result{}, err
	}

	// Reconcile KV-cache pressure if the model is running and has a KVCache policy.
	if model.Status.Phase == aiv1alpha2.ModelPhaseReady {
		r.reconcileKVCachePressure(ctx, model)
	}
	// Best-effort cleanup: prune old failed pods from this model's ReplicaSet so
	// operational status stays readable after transient device/plugin failures.
	if err := r.pruneFailedModelPods(ctx, model); err != nil {
		log.Error(err, "Failed to prune old failed model pods")
	}

	return ctrl.Result{RequeueAfter: requeueAfter}, nil
}

// pruneFailedModelPods removes old failed pods for this model to keep
// operational status readable after transient device/plugin failures.
func (r *ModelReconciler) pruneFailedModelPods(ctx context.Context, model *aiv1alpha2.Model) error {
	podList := &corev1.PodList{}
	if err := r.List(ctx, podList, client.InNamespace(model.Namespace), client.MatchingLabels(r.selectorLabelsForModel(model))); err != nil {
		return err
	}

	cutoff := time.Now().Add(-failedPodCutoff)
	for i := range podList.Items {
		pod := &podList.Items[i]
		if pod.DeletionTimestamp != nil || pod.Status.Phase != corev1.PodFailed {
			continue
		}
		if pod.CreationTimestamp.Time.After(cutoff) {
			continue
		}
		if err := r.Delete(ctx, pod, client.PropagationPolicy(metav1.DeletePropagationBackground)); err != nil && !errors.IsNotFound(err) {
			return err
		}
	}
	return nil
}

// SetupWithManager sets up the controller with the Manager.
func (r *ModelReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&aiv1alpha2.Model{}).
		Owns(&appsv1.Deployment{}).
		Owns(&corev1.Service{}).
		Owns(&corev1.PersistentVolumeClaim{}).
		Owns(&batchv1.Job{}).
		Watches(&corev1.Pod{}, handler.EnqueueRequestsFromMapFunc(r.requestsForRuntimePod)).
		Complete(r)
}

func (r *ModelReconciler) requestsForRuntimePod(ctx context.Context, obj client.Object) []ctrl.Request {
	pod, ok := obj.(*corev1.Pod)
	if !ok {
		return nil
	}
	if pod.Labels["app.kubernetes.io/component"] != runtimeComponentLabel {
		return nil
	}

	models := &aiv1alpha2.ModelList{}
	if err := r.List(ctx, models, client.InNamespace(pod.Namespace)); err != nil {
		log.FromContext(ctx).Error(err, "Failed to list models for runtime pod event", "pod", pod.Name)
		return nil
	}

	requests := make([]ctrl.Request, 0, len(models.Items))
	for i := range models.Items {
		model := &models.Items[i]
		if !runtimePodTargetsModel(ctx, r.Client, pod, model) {
			continue
		}
		requests = append(requests, ctrl.Request{NamespacedName: client.ObjectKeyFromObject(model)})
	}
	return requests
}

func runtimePodTargetsModel(ctx context.Context, c client.Client, pod *corev1.Pod, model *aiv1alpha2.Model) bool {
	if pod == nil || model == nil {
		return false
	}
	if len(model.Spec.NodeSelector) == 0 {
		return true
	}
	if pod.Spec.NodeName != "" {
		return nodeMatchesSelector(ctx, c, pod.Spec.NodeName, model.Spec.NodeSelector)
	}
	return podNodeSelectorMatches(pod.Spec.NodeSelector, model.Spec.NodeSelector)
}
