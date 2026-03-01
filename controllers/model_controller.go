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
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	stderrors "errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	appsv1 "k8s.io/api/apps/v1"
	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	apiequality "k8s.io/apimachinery/pkg/api/equality"
	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/intstr"
	"k8s.io/client-go/tools/record"
	"k8s.io/utils/ptr"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log"

	aiv1alpha1 "github.com/flexinfer/flexinfer/api/v1alpha1"
	aiv1alpha2 "github.com/flexinfer/flexinfer/api/v1alpha2"
	"github.com/flexinfer/flexinfer/backend"
	"github.com/flexinfer/flexinfer/pkg/k8surl"
	"github.com/flexinfer/flexinfer/pkg/metrics"
	"github.com/flexinfer/flexinfer/pkg/quantization"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
)

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

func isMaxwellGPUArch(gpuArch string) bool {
	return strings.HasPrefix(strings.TrimSpace(gpuArch), "sm_5")
}

func litellmEnabled(model *aiv1alpha2.Model) bool {
	if model.Spec.LiteLLM == nil {
		return false
	}
	if model.Spec.LiteLLM.Enabled == nil {
		return true
	}
	return *model.Spec.LiteLLM.Enabled
}

func litellmServedModel(model *aiv1alpha2.Model) string {
	if model.Spec.LiteLLM != nil && model.Spec.LiteLLM.ServedModelName != "" {
		return model.Spec.LiteLLM.ServedModelName
	}
	return model.Name
}

func litellmAliases(model *aiv1alpha2.Model, servedModel string) []string {
	unique := make(map[string]struct{})
	add := func(v string) {
		v = strings.TrimSpace(v)
		if v == "" || v == servedModel {
			return
		}
		unique[v] = struct{}{}
	}

	for _, label := range model.Spec.ServiceLabels {
		add(label)
	}
	if model.Spec.LiteLLM != nil {
		for _, alias := range model.Spec.LiteLLM.Aliases {
			add(alias)
		}
	}

	out := make([]string, 0, len(unique))
	for v := range unique {
		out = append(out, v)
	}
	sort.Strings(out)
	return out
}

// ResolvedCapabilities is the fully-resolved set of model capabilities,
// with auto-inference applied and explicit overrides merged.
type ResolvedCapabilities struct {
	ToolCalling     bool `json:"toolCalling"`
	Vision          bool `json:"vision"`
	ImageGeneration bool `json:"imageGeneration"`
}

// resolveCapabilities auto-infers capabilities from backend type and config,
// then applies explicit overrides from spec.capabilities.
func resolveCapabilities(model *aiv1alpha2.Model, b backend.Backend) ResolvedCapabilities {
	caps := ResolvedCapabilities{
		ImageGeneration: b.IsImageGeneration(),
	}

	switch model.Spec.Backend {
	case "vllm", "ollama":
		caps.ToolCalling = true
	case "llamacpp":
		caps.ToolCalling = model.Spec.ConfigBool("jinja", false)
		caps.Vision = model.Spec.ConfigString("mmproj", "") != ""
	}

	if oc := model.Spec.Capabilities; oc != nil {
		if oc.ToolCalling != nil {
			caps.ToolCalling = *oc.ToolCalling
		}
		if oc.Vision != nil {
			caps.Vision = *oc.Vision
		}
		if oc.ImageGeneration != nil {
			caps.ImageGeneration = *oc.ImageGeneration
		}
	}
	return caps
}

var managedModelAnnotations = []string{
	"litellm.flexinfer.ai/served-model",
	"litellm.flexinfer.ai/aliases",
	"litellm.flexinfer.ai/copilot-model",
	"litellm.flexinfer.ai/capabilities",
	"flexinfer.ai/service-labels",
}

var managedModelPodAnnotations = []string{
	"flexinfer.ai/model",
	"flexinfer.ai/backend",
	"flexinfer.ai/gpu.vram-estimate-mb",
}

func applyManagedAnnotations(existing map[string]string, desired map[string]string, managedKeys []string) map[string]string {
	out := make(map[string]string, len(existing)+len(desired))
	for k, v := range existing {
		out[k] = v
	}
	for _, k := range managedKeys {
		if desired != nil {
			if v, ok := desired[k]; ok && v != "" {
				out[k] = v
				continue
			}
		}
		delete(out, k)
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

func mergeStringMap(existing map[string]string, additional map[string]string) map[string]string {
	if len(existing) == 0 && len(additional) == 0 {
		return nil
	}
	out := make(map[string]string, len(existing)+len(additional))
	for k, v := range existing {
		out[k] = v
	}
	for k, v := range additional {
		out[k] = v
	}
	return out
}

// ModelReconciler reconciles a Model object
type ModelReconciler struct {
	client.Client
	Scheme    *runtime.Scheme
	Recorder  record.EventRecorder
	APIReader client.Reader
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
	ctx, span := otel.Tracer("flexinfer/controller").Start(ctx, "model.reconcile")
	defer span.End()
	span.SetAttributes(
		attribute.String("k8s.namespace", req.Namespace),
		attribute.String("k8s.name", req.Name),
	)

	log := log.FromContext(ctx)

	// Fetch the Model instance
	model := &aiv1alpha2.Model{}
	err := r.Get(ctx, req.NamespacedName, model)
	if err != nil {
		if errors.IsNotFound(err) {
			log.Info("Model resource not found, ignoring since object must be deleted")
			return ctrl.Result{}, nil
		}
		span.RecordError(err)
		log.Error(err, "Failed to get Model")
		return ctrl.Result{}, err
	}
	span.SetAttributes(
		attribute.String("model.name", model.Name),
		attribute.String("model.namespace", model.Namespace),
		attribute.String("model.backend", model.Spec.Backend),
	)

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
		err := fmt.Errorf("unknown backend: %s", model.Spec.Backend)
		log.Error(err, "Backend validation failed")
		r.Recorder.Event(model, corev1.EventTypeWarning, "ValidationFailed", err.Error())
		return ctrl.Result{}, r.updatePhase(ctx, model, aiv1alpha2.ModelPhaseFailed)
	}

	// Check for litellm alias conflicts across all models in the namespace.
	// This is a warning condition, not a blocker — the model still reconciles.
	r.checkAliasConflicts(ctx, model)

	desiredReplicas := r.desiredReplicas(model, b)
	requeueAfter := 30 * time.Second

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
		desiredReplicas = r.desiredReplicas(model, b)
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

func (r *ModelReconciler) pruneFailedModelPods(ctx context.Context, model *aiv1alpha2.Model) error {
	podList := &corev1.PodList{}
	if err := r.List(ctx, podList, client.InNamespace(model.Namespace), client.MatchingLabels(r.selectorLabelsForModel(model))); err != nil {
		return err
	}

	cutoff := time.Now().Add(-5 * time.Minute)
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

// validateVRAMFit checks whether the model's declared VRAM estimate fits the GPU capacity.
// Skips validation if no estimate is provided (backward compatible).
func (r *ModelReconciler) validateVRAMFit(model *aiv1alpha2.Model, b backend.Backend, gpuArch string) error {
	if model.Spec.GPU == nil || model.Spec.GPU.VRAMEstimateMB == nil || *model.Spec.GPU.VRAMEstimateMB <= 0 {
		return nil
	}

	support, found := backend.LookupGPUArchSupport(b.Name(), gpuArch)
	if !found || support.MaxVRAMMB <= 0 {
		return nil
	}

	estimateMB := *model.Spec.GPU.VRAMEstimateMB
	gpuCount := int64(1)
	if model.Spec.GPU.Count != nil && *model.Spec.GPU.Count > 1 {
		gpuCount = int64(*model.Spec.GPU.Count)
	}
	totalVRAMMB := int64(support.MaxVRAMMB) * gpuCount

	// Block if estimate exceeds 95% of total VRAM
	if estimateMB > totalVRAMMB*95/100 {
		return fmt.Errorf("model VRAM estimate (%dMB) exceeds 95%% of available GPU VRAM (%dMB across %d GPU(s)) for %s on %s",
			estimateMB, totalVRAMMB, gpuCount, b.Name(), gpuArch)
	}

	// Warn if estimate exceeds 80% of total VRAM
	if estimateMB > totalVRAMMB*80/100 {
		r.Recorder.Event(model, corev1.EventTypeWarning, "VRAMPressure",
			fmt.Sprintf("model VRAM estimate (%dMB) exceeds 80%% of GPU VRAM (%dMB); performance may be degraded",
				estimateMB, totalVRAMMB))
	}

	return nil
}

// validateBackendGPUCompatibility checks if the backend is compatible with the target GPU arch.
// Uses the GPU compatibility matrix for data-driven validation with fallback to architecture-specific checks.
func (r *ModelReconciler) validateBackendGPUCompatibility(model *aiv1alpha2.Model, b backend.Backend, gpuVendor backend.GPUVendor, gpuArch string) error {
	// Check the GPU compatibility matrix first.
	if support, found := backend.LookupGPUArchSupport(b.Name(), gpuArch); found {
		switch support.Level {
		case backend.SupportUnsupported:
			return fmt.Errorf("%s backend is not supported on %s GPUs. Use a compatible backend instead", b.Name(), gpuArch)
		case backend.SupportExperimental:
			// Only warn if the resolved image is generic (not arch-specific)
			img := b.Image(gpuVendor, gpuArch)
			isGenericImage := !strings.Contains(img, "gfx906") && !strings.Contains(img, "gfx110")
			if isGenericImage {
				r.Recorder.Event(model, corev1.EventTypeWarning, "ExperimentalGPUSupport",
					fmt.Sprintf("%s on %s is experimental: using generic image %s", b.Name(), gpuArch, img))
			}
		}
	}

	// --- Maxwell-specific validation (sm_5x) ---
	if err := r.validateMaxwellSpecifics(model, b, gpuVendor, gpuArch); err != nil {
		return err
	}

	return nil
}

// validateMaxwellSpecifics handles Maxwell GPU (sm_5x) specific validation:
// FP16 rejection and backend-specific library requirements.
func (r *ModelReconciler) validateMaxwellSpecifics(model *aiv1alpha2.Model, b backend.Backend, gpuVendor backend.GPUVendor, gpuArch string) error {
	if !isMaxwellGPUArch(gpuArch) || gpuVendor != backend.GPUVendorNVIDIA {
		return nil
	}

	// Reject FP16 models on Maxwell — Maxwell lacks native FP16 support.
	src := strings.ToLower(model.Spec.Source)
	if strings.Contains(src, "f16") || strings.Contains(src, "fp16") {
		return fmt.Errorf("FP16 models are not supported on Maxwell GPUs (no native FP16). Use q4f32_1, q0f32, or GGUF quantized models instead")
	}

	if b.Name() == "mlc-llm" {
		// MLC-LLM on Maxwell should use a pre-compiled model library and avoid JIT.
		cfg := model.Spec.GetConfigMap()
		if cfg != nil {
			if v, ok := cfg["modelLibPath"]; ok {
				if s, ok := v.(string); ok && strings.TrimSpace(s) != "" {
					return nil
				}
			}
		}

		spec := r.buildBackendModelSpec(model, b, gpuVendor)
		modelPath := spec.ModelPath
		if modelPath == "" {
			modelPath = spec.Model
		}
		modelPath = strings.TrimRight(modelPath, "/")
		if strings.HasPrefix(modelPath, "/models/") && modelPath != "/models" {
			return nil
		}

		return fmt.Errorf("mlc-llm on Maxwell GPUs requires config.modelLibPath (pre-compiled library). See docs/user/backends-maxwell.md")
	}

	return nil
}

// emitVLLMOptInEvents emits informational events when the user opts into
// experimental vLLM features (V1 engine, flash attention, AITER).
func (r *ModelReconciler) emitVLLMOptInEvents(model *aiv1alpha2.Model) {
	cfg := model.Spec.GetConfigMap()
	if cfg == nil {
		return
	}
	if v, ok := cfg["vllmEngineVersion"]; ok {
		if s, ok := v.(string); ok && s == "v1" {
			r.Recorder.Event(model, corev1.EventTypeNormal, "V1EngineOptIn",
				"vLLM V1 engine enabled via spec.config.vllmEngineVersion=v1 (experimental)")
		}
	}
	if v, ok := cfg["enableFlashAttention"]; ok {
		enabled := false
		switch val := v.(type) {
		case bool:
			enabled = val
		case string:
			enabled = val == "true" || val == "1"
		}
		if enabled {
			r.Recorder.Event(model, corev1.EventTypeNormal, "FlashAttentionOptIn",
				"Triton flash attention enabled via spec.config.enableFlashAttention=true (experimental)")
		}
	}
}

// desiredReplicas calculates the desired replica count for the model.
// For serverless models, this is driven by LastActiveTime (written by the proxy) and idle timeout.
func (r *ModelReconciler) desiredReplicas(model *aiv1alpha2.Model, b backend.Backend) int32 {
	// Shared GPU: only the active model should run.
	if model.Spec.IsShared() && model.Status.SharedGroup != nil {
		if model.Status.SharedGroup.State != "Active" {
			return 0
		}
	}

	if !model.Spec.IsServerless() {
		return 1
	}

	minReplicas := model.Spec.GetMinReplicas()
	if model.Status.LastActiveTime == nil {
		return minReplicas
	}

	// Never scale down a model that is still loading. The proxy sets
	// LastActiveTime once at request arrival; if loading takes longer than
	// idleTimeout the model would be incorrectly reaped mid-load.
	if model.Status.Phase == aiv1alpha2.ModelPhaseLoading {
		return 1
	}

	idleTimeout := getIdleTimeout(model, b)
	if time.Since(model.Status.LastActiveTime.Time) > idleTimeout {
		return minReplicas
	}

	if minReplicas < 1 {
		return 1
	}
	return minReplicas
}

// cleanupModel removes all resources created for the model.
func (r *ModelReconciler) cleanupModel(ctx context.Context, model *aiv1alpha2.Model) error {
	log := log.FromContext(ctx)

	// Delete deployment
	deployment := &appsv1.Deployment{}
	if err := r.Get(ctx, types.NamespacedName{Name: model.Name, Namespace: model.Namespace}, deployment); err == nil {
		if err := r.Delete(ctx, deployment); err != nil && !errors.IsNotFound(err) {
			return err
		}
		log.Info("Deleted deployment", "name", model.Name)
	}

	// Delete service
	service := &corev1.Service{}
	if err := r.Get(ctx, types.NamespacedName{Name: model.Name, Namespace: model.Namespace}, service); err == nil {
		if err := r.Delete(ctx, service); err != nil && !errors.IsNotFound(err) {
			return err
		}
		log.Info("Deleted service", "name", model.Name)
	}

	// Clean up persistent flash-tmpfs on the node for shared models.
	if err := r.cleanupFlashTmpfs(ctx, model); err != nil {
		log.Error(err, "Failed to create flash-tmpfs cleanup job (non-fatal)")
	}

	return nil
}

// cleanupFlashTmpfs creates a short-lived Job to remove the persistent
// /dev/shm/flexinfer/{ns}/{model} directory on the target node.
// Only applies to shared models that use hostPath-based flash-tmpfs.
func (r *ModelReconciler) cleanupFlashTmpfs(ctx context.Context, model *aiv1alpha2.Model) error {
	if !model.Spec.IsShared() {
		return nil
	}
	if len(model.Spec.NodeSelector) == 0 {
		return nil
	}

	log := log.FromContext(ctx)
	flashDir := filepath.Join("/dev/shm/flexinfer", model.Namespace, model.Name)

	job := &batchv1.Job{
		ObjectMeta: metav1.ObjectMeta{
			Name:      model.Name + "-tmpfs-cleanup",
			Namespace: model.Namespace,
			Labels:    r.labelsForModel(model),
		},
		Spec: batchv1.JobSpec{
			BackoffLimit:            ptr.To(int32(1)),
			TTLSecondsAfterFinished: ptr.To(int32(120)),
			Template: corev1.PodTemplateSpec{
				Spec: corev1.PodSpec{
					RestartPolicy:                corev1.RestartPolicyNever,
					NodeSelector:                 model.Spec.NodeSelector,
					AutomountServiceAccountToken: ptr.To(false),
					Containers: []corev1.Container{{
						Name:    "cleanup",
						Image:   "busybox:1.37",
						Command: []string{"rm", "-rf", flashDir},
						Resources: corev1.ResourceRequirements{
							Requests: corev1.ResourceList{
								corev1.ResourceCPU:    resource.MustParse("10m"),
								corev1.ResourceMemory: resource.MustParse("16Mi"),
							},
							Limits: corev1.ResourceList{
								corev1.ResourceMemory: resource.MustParse("32Mi"),
							},
						},
					}},
				},
			},
		},
	}

	// No owner reference — model is being deleted, so the Job must
	// outlive the model. TTLSecondsAfterFinished handles auto-cleanup.
	if err := r.Create(ctx, job); err != nil {
		if errors.IsAlreadyExists(err) {
			return nil
		}
		return fmt.Errorf("create flash-tmpfs cleanup job: %w", err)
	}
	log.Info("Created flash-tmpfs cleanup job", "path", flashDir)
	return nil
}

// ensureService creates or updates the Service for the model.
func (r *ModelReconciler) ensureService(ctx context.Context, model *aiv1alpha2.Model, b backend.Backend) error {
	log := log.FromContext(ctx)

	service := &corev1.Service{}
	err := r.Get(ctx, types.NamespacedName{Name: model.Name, Namespace: model.Namespace}, service)
	if err != nil && !errors.IsNotFound(err) {
		return err
	}

	port := b.Port()

	// Build annotations including LiteLLM and service labels
	annotations := make(map[string]string)
	if litellmEnabled(model) {
		servedModel := litellmServedModel(model)
		annotations["litellm.flexinfer.ai/served-model"] = servedModel
		if aliases := litellmAliases(model, servedModel); len(aliases) > 0 {
			annotations["litellm.flexinfer.ai/aliases"] = strings.Join(aliases, ",")
		}
		if model.Spec.LiteLLM != nil && model.Spec.LiteLLM.CopilotAlias != "" {
			annotations["litellm.flexinfer.ai/copilot-model"] = model.Spec.LiteLLM.CopilotAlias
		}
	}

	// Add service labels for routing
	if len(model.Spec.ServiceLabels) > 0 {
		annotations["flexinfer.ai/service-labels"] = strings.Join(model.Spec.ServiceLabels, ",")
	}

	desiredService := &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{
			Name:        model.Name,
			Namespace:   model.Namespace,
			Labels:      r.labelsForModel(model),
			Annotations: annotations,
			OwnerReferences: []metav1.OwnerReference{
				*metav1.NewControllerRef(model, aiv1alpha2.GroupVersion.WithKind("Model")),
			},
		},
		Spec: corev1.ServiceSpec{
			Selector: r.selectorLabelsForModel(model),
			Ports: []corev1.ServicePort{
				{
					Name:       "http",
					Port:       port,
					TargetPort: intstr.FromInt32(port),
					Protocol:   corev1.ProtocolTCP,
				},
			},
			Type: corev1.ServiceTypeClusterIP,
		},
	}

	if errors.IsNotFound(err) {
		log.Info("Creating Service", "name", model.Name)
		return r.Create(ctx, desiredService)
	}

	// Update service. Avoid clobbering immutable fields (e.g., clusterIP/clusterIPs).
	service.Spec.Ports = desiredService.Spec.Ports
	service.Spec.Selector = desiredService.Spec.Selector
	service.Labels = desiredService.Labels
	service.Annotations = applyManagedAnnotations(service.Annotations, annotations, managedModelAnnotations)
	log.Info("Updating Service", "name", model.Name)
	return r.Update(ctx, service)
}

// ensureDeployment creates or updates the Deployment for the model.
func (r *ModelReconciler) ensureDeployment(ctx context.Context, model *aiv1alpha2.Model, b backend.Backend, gpuVendor backend.GPUVendor, gpuArch string, desiredReplicas int32) error {
	log := log.FromContext(ctx)

	deployment := &appsv1.Deployment{}
	err := r.Get(ctx, types.NamespacedName{Name: model.Name, Namespace: model.Namespace}, deployment)
	if err != nil && !errors.IsNotFound(err) {
		return err
	}

	// Build ModelSpec for backend
	spec := r.buildBackendModelSpec(model, b, gpuVendor)
	spec.GPUArch = gpuArch
	storagePlan := resolveBackendStoragePlan(model, b, spec.Config)

	// Get container configuration from backend
	image := b.Image(gpuVendor, gpuArch)
	port := b.Port()
	command := b.Command()
	args := b.Args(spec)
	env := b.Env(spec)
	probe := b.ReadinessProbe()
	startupProbe := b.StartupProbe()

	// Append KV-cache tuning args if the backend supports it.
	if model.Spec.KVCache != nil {
		if kvc, ok := b.(backend.KVCacheConfigurer); ok {
			var swapGiB *float64
			if model.Spec.KVCache.SwapSpace != nil {
				v := model.Spec.KVCache.SwapSpace.AsApproximateFloat64()
				swapGiB = &v
			}
			if extra := kvc.KVCacheArgs(model.Spec.KVCache.MaxBlockSize, swapGiB); len(extra) > 0 {
				args = append(args, extra...)
			}
		}
	}

	// Append LoRA base args if the model has LoRA adapters and backend supports it.
	if ls, ok := b.(backend.LoRASupporter); ok && ls.SupportsLoRA() {
		// Check for LoRA adapter CRs referencing this model.
		loraList := &aiv1alpha2.LoRAAdapterList{}
		if err := r.List(ctx, loraList, client.InNamespace(model.Namespace)); err == nil {
			count := 0
			for _, la := range loraList.Items {
				if la.Spec.ModelRef == model.Name {
					count++
				}
			}
			if count > 0 {
				maxAdapters := count
				if maxAdapters < 4 {
					maxAdapters = 4 // minimum headroom
				}
				args = append(args, ls.LoRABaseArgs(maxAdapters)...)
			}
		}
	}

	// Store HuggingFace cache metadata on the model volume when available.
	if storagePlan.HFCacheBasePath != "" {
		env = mergeEnv(env, hfCacheEnvVars(storagePlan.HFCacheBasePath))
	}

	// Build resource requirements
	resources := model.Spec.Resources
	if resources.Limits == nil {
		resources.Limits = corev1.ResourceList{}
	}
	gpuCount := model.Spec.GetGPUCount()
	if gpuCount > 0 {
		gpuResourceName := gpuVendor.ResourceName()
		if gpuResourceName != "" {
			resources.Limits[gpuResourceName] = *resource.NewQuantity(int64(gpuCount), resource.DecimalSI)
		}
	}

	// Build node selector
	nodeSelector := model.Spec.NodeSelector
	if nodeSelector == nil {
		nodeSelector = make(map[string]string)
	}

	// Tolerate dedicated GPU nodes when requesting GPUs.
	var tolerations []corev1.Toleration
	if gpuCount > 0 {
		tolerations = append(tolerations, corev1.Toleration{
			Key:      "dedicated",
			Operator: corev1.TolerationOpEqual,
			Value:    "gpu",
			Effect:   corev1.TaintEffectNoSchedule,
		})
	}

	// Build container
	container := corev1.Container{
		Name:            "model",
		Image:           image,
		ImagePullPolicy: corev1.PullIfNotPresent,
		Command:         command,
		Args:            args,
		Env:             env,
		Ports: []corev1.ContainerPort{
			{
				Name:          "http",
				ContainerPort: port,
				Protocol:      corev1.ProtocolTCP,
			},
		},
		Resources:      resources,
		ReadinessProbe: probe,
		StartupProbe:   startupProbe,
		// Set K8s defaults explicitly to prevent reconcile loops.
		// The API server adds these on write; without them, every read-back
		// differs from the generated spec, causing continuous updates.
		TerminationMessagePath:   "/dev/termination-log",
		TerminationMessagePolicy: corev1.TerminationMessageReadFile,
	}

	// Add volume mounts if backend needs volume
	var volumes []corev1.Volume
	if b.NeedsVolume() {
		// Add model volume mount
		volumeMount := corev1.VolumeMount{
			Name:      "model",
			MountPath: "/models",
		}
		// Backends can require a subpath view of the mounted model volume.
		if storagePlan.ModelVolumeSubPath != "" {
			volumeMount.SubPath = storagePlan.ModelVolumeSubPath
		}
		container.VolumeMounts = append(container.VolumeMounts, volumeMount)

		// Determine volume source based on cache strategy
		volumeSource := r.getVolumeSource(model)
		volumes = append(volumes, corev1.Volume{
			Name:         "model",
			VolumeSource: volumeSource,
		})
	}

	// Add shared memory volume for ML workloads
	shmSizeLimit := defaultSHMSizeLimit()
	container.VolumeMounts = append(container.VolumeMounts, corev1.VolumeMount{
		Name:      "shm",
		MountPath: "/dev/shm",
	})
	volumes = append(volumes, corev1.Volume{
		Name: "shm",
		VolumeSource: corev1.VolumeSource{
			EmptyDir: &corev1.EmptyDirVolumeSource{
				Medium:    corev1.StorageMediumMemory,
				SizeLimit: &shmSizeLimit,
			},
		},
	})

	// Flash-loader init container: when enabled, copy model files from PVC to tmpfs
	// before starting the backend for lower cold-start/swap latency.
	var initContainers []corev1.Container
	flashCfg := r.resolveFlashLoaderConfig(ctx, model)
	if flashCfg.Enabled {
		flashVerify := "false"
		if flashCfg.VerifyIntegrity {
			flashVerify = "true"
		}
		// Derive FLASH_VARIANT from model's useFp16 config — when fp16 is enabled,
		// flash-loader skips fp32 safetensors files that have fp16 counterparts.
		flashVariant := ""
		if model.Spec.ConfigString("useFp16", "") == "1" {
			flashVariant = "fp16"
		}

		flashContainer := corev1.Container{
			Name:            "flash-loader",
			Image:           flashCfg.Image,
			ImagePullPolicy: corev1.PullIfNotPresent,
			Env: []corev1.EnvVar{
				{Name: "FLASH_SRC", Value: "/src"},
				{Name: "FLASH_DST", Value: "/models"},
				{Name: "FLASH_CONCURRENCY", Value: strconv.Itoa(flashCfg.Concurrency)},
				{Name: "FLASH_BUFFER_KB", Value: strconv.Itoa(flashCfg.BufferSizeKB)},
				{Name: "FLASH_VERIFY", Value: flashVerify},
				{Name: "FLASH_EXCLUDE", Value: flashCfg.ExcludePatterns},
				{Name: "FLASH_VARIANT", Value: flashVariant},
			},
			VolumeMounts: []corev1.VolumeMount{
				{Name: "model", MountPath: "/src", ReadOnly: true},
				{Name: "flash-tmpfs", MountPath: "/models"},
			},
			// Set K8s defaults explicitly to prevent reconcile loops.
			// The API server adds these on write; without them, every read-back
			// differs from the generated spec, causing continuous updates.
			TerminationMessagePath:   "/dev/termination-log",
			TerminationMessagePolicy: corev1.TerminationMessageReadFile,
		}
		initContainers = append(initContainers, flashContainer)

		// Replace the model volume mount on the main container to use tmpfs
		for i := range container.VolumeMounts {
			if container.VolumeMounts[i].Name == "model" {
				container.VolumeMounts[i].Name = "flash-tmpfs"
			}
		}

		// Persistent flash-tmpfs for shared models: use hostPath on /dev/shm
		// so model weights survive pod restarts. Flash-loader's shouldCopy()
		// skips files with matching sizes, making subsequent swaps near-instant.
		if model.Spec.IsShared() {
			flashDir := filepath.Join("/dev/shm/flexinfer", model.Namespace, model.Name)
			volumes = append(volumes, corev1.Volume{
				Name: "flash-tmpfs",
				VolumeSource: corev1.VolumeSource{
					HostPath: &corev1.HostPathVolumeSource{
						Path: flashDir,
						Type: hostPathTypePtr(corev1.HostPathDirectoryOrCreate),
					},
				},
			})
		} else {
			// Non-shared: ephemeral emptyDir (existing behavior)
			flashTmpfs := &corev1.EmptyDirVolumeSource{
				Medium: corev1.StorageMediumMemory,
			}
			if flashCfg.TmpfsSizeLimit != nil {
				sizeLimit := flashCfg.TmpfsSizeLimit.DeepCopy()
				flashTmpfs.SizeLimit = &sizeLimit
			}
			volumes = append(volumes, corev1.Volume{
				Name: "flash-tmpfs",
				VolumeSource: corev1.VolumeSource{
					EmptyDir: flashTmpfs,
				},
			})
		}
	}

	// Compilation cache: persistent hostPath for MIOpen/PyTorch/Triton caches.
	// Survives pod restarts to avoid recompilation on GPU swaps.
	if hostDir, ccEnabled := resolveCompilationCache(model); ccEnabled {
		if ccConfigurer, ok := b.(backend.CompilationCacheConfigurer); ok {
			volumes = append(volumes, corev1.Volume{
				Name: "compile-cache",
				VolumeSource: corev1.VolumeSource{
					HostPath: &corev1.HostPathVolumeSource{
						Path: hostDir,
						Type: hostPathTypePtr(corev1.HostPathDirectoryOrCreate),
					},
				},
			})
			container.VolumeMounts = append(container.VolumeMounts, corev1.VolumeMount{
				Name:      "compile-cache",
				MountPath: compilationCacheMountPath,
			})
			container.Env = append(container.Env, ccConfigurer.CompilationCacheEnvVars(compilationCacheMountPath)...)
		}
	}

	desiredDeployment := &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{
			Name:      model.Name,
			Namespace: model.Namespace,
			Labels:    r.labelsForModel(model),
			Annotations: func() map[string]string {
				annotations := make(map[string]string)
				if litellmEnabled(model) {
					servedModel := litellmServedModel(model)
					annotations["litellm.flexinfer.ai/served-model"] = servedModel
					if aliases := litellmAliases(model, servedModel); len(aliases) > 0 {
						annotations["litellm.flexinfer.ai/aliases"] = strings.Join(aliases, ",")
					}
					if model.Spec.LiteLLM != nil && model.Spec.LiteLLM.CopilotAlias != "" {
						annotations["litellm.flexinfer.ai/copilot-model"] = model.Spec.LiteLLM.CopilotAlias
					}
					capsJSON, _ := json.Marshal(resolveCapabilities(model, b))
					annotations["litellm.flexinfer.ai/capabilities"] = string(capsJSON)
				}
				if len(model.Spec.ServiceLabels) > 0 {
					annotations["flexinfer.ai/service-labels"] = strings.Join(model.Spec.ServiceLabels, ",")
				}
				if len(annotations) == 0 {
					return nil
				}
				return annotations
			}(),
			OwnerReferences: []metav1.OwnerReference{
				*metav1.NewControllerRef(model, aiv1alpha2.GroupVersion.WithKind("Model")),
			},
		},
		Spec: appsv1.DeploymentSpec{
			Replicas: ptr.To(desiredReplicas),
			Strategy: func() appsv1.DeploymentStrategy {
				// GPU workloads frequently run on tightly-constrained nodes where a second
				// pod cannot be scheduled (e.g., 1x GPU nodes, or multi-GPU nodes where
				// we allocate all GPUs). Avoid rolling update deadlocks by disabling surge.
				if gpuCount > 0 {
					maxSurge := intstr.FromInt(0)
					maxUnavailable := intstr.FromInt(1)
					return appsv1.DeploymentStrategy{
						Type: appsv1.RollingUpdateDeploymentStrategyType,
						RollingUpdate: &appsv1.RollingUpdateDeployment{
							MaxSurge:       &maxSurge,
							MaxUnavailable: &maxUnavailable,
						},
					}
				}
				return appsv1.DeploymentStrategy{}
			}(),
			Selector: &metav1.LabelSelector{
				MatchLabels: r.selectorLabelsForModel(model),
			},
			Template: corev1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{
					Labels:      r.labelsForModel(model),
					Annotations: r.podAnnotationsForModel(model),
				},
				Spec: corev1.PodSpec{
					NodeSelector: nodeSelector,
					Tolerations:  tolerations,
					// ROCm devices (/dev/kfd, /dev/dri/renderD*) are typically 0660 root:render.
					// Add the render group GID (992 on most systems) to supplementalGroups so
					// non-root users can access GPU devices without running as root.
					SecurityContext: func() *corev1.PodSecurityContext {
						if gpuVendor != backend.GPUVendorAMD || gpuCount == 0 {
							return nil
						}
						return &corev1.PodSecurityContext{
							// Render group GID varies by distro: 992 (Arch), 109 (Debian/Ubuntu).
							// Include both so GPU device access works on either.
							SupplementalGroups: []int64{109, 992},
						}
					}(),
					Affinity: func() *corev1.Affinity {
						// For multi-replica models, enforce one pod per node (best-effort load balancing
						// across identical GPU nodes, and avoids accidentally packing both replicas onto
						// a single multi-GPU node).
						if desiredReplicas <= 1 {
							return nil
						}
						return &corev1.Affinity{
							PodAntiAffinity: &corev1.PodAntiAffinity{
								RequiredDuringSchedulingIgnoredDuringExecution: []corev1.PodAffinityTerm{
									{
										LabelSelector: &metav1.LabelSelector{
											MatchLabels: r.selectorLabelsForModel(model),
										},
										TopologyKey: "kubernetes.io/hostname",
									},
								},
							},
						}
					}(),
					TopologySpreadConstraints: func() []corev1.TopologySpreadConstraint {
						// If we run multiple replicas (e.g., active-active on two GPU nodes),
						// spread them across nodes when possible, but allow co-location as fallback.
						if desiredReplicas <= 1 {
							return nil
						}
						return []corev1.TopologySpreadConstraint{
							{
								MaxSkew:           1,
								TopologyKey:       "kubernetes.io/hostname",
								WhenUnsatisfiable: corev1.ScheduleAnyway,
								LabelSelector: &metav1.LabelSelector{
									MatchLabels: r.selectorLabelsForModel(model),
								},
							},
						}
					}(),
					InitContainers: initContainers,
					Containers:     []corev1.Container{container},
					Volumes:        volumes,
					RuntimeClassName: func() *string {
						// NVIDIA GPUs require the "nvidia" runtime to inject /dev/nvidia* and driver libs.
						// Without this, pods may schedule with nvidia.com/gpu but have no CUDA devices.
						if gpuVendor == backend.GPUVendorNVIDIA && gpuCount > 0 {
							runtime := "nvidia"
							return &runtime
						}
						return nil
					}(),
					// Model pods do not need to talk to the Kubernetes API. Avoid mounting a service
					// account token by default to reduce blast radius if a backend container is compromised.
					AutomountServiceAccountToken: ptr.To(false),
					RestartPolicy:                corev1.RestartPolicyAlways,
					ServiceAccountName:           "default",
				},
			},
		},
	}

	if errors.IsNotFound(err) {
		log.Info("Creating Deployment", "name", model.Name, "replicas", desiredReplicas)
		return r.Create(ctx, desiredDeployment)
	}

	// Snapshot existing state for change detection.
	existingSpec := deployment.Spec.DeepCopy()
	existingLabels := deployment.Labels
	existingAnnotations := deployment.Annotations

	// Apply desired state onto the existing deployment.
	existingPodAnnotations := deployment.Spec.Template.Annotations
	desiredSpec := desiredDeployment.Spec
	// Deployment selectors are immutable. Preserve the existing selector on updates to avoid
	// deadlocking reconciliation when labels change (e.g., shared GPU group assignment).
	if deployment.Spec.Selector != nil {
		desiredSpec.Selector = deployment.Spec.Selector.DeepCopy()
		if desiredSpec.Selector.MatchLabels != nil {
			desiredSpec.Template.Labels = mergeStringMap(desiredSpec.Template.Labels, desiredSpec.Selector.MatchLabels)
		}
	}
	deployment.Spec = desiredSpec
	deployment.Labels = desiredDeployment.Labels
	deployment.Annotations = applyManagedAnnotations(deployment.Annotations, desiredDeployment.Annotations, managedModelAnnotations)
	deployment.Spec.Template.Annotations = applyManagedAnnotations(existingPodAnnotations, desiredDeployment.Spec.Template.Annotations, managedModelPodAnnotations)

	// Skip update if nothing changed.
	if apiequality.Semantic.DeepEqual(&deployment.Spec, existingSpec) &&
		apiequality.Semantic.DeepEqual(deployment.Labels, existingLabels) &&
		apiequality.Semantic.DeepEqual(deployment.Annotations, existingAnnotations) {
		return nil
	}

	// Log which fields changed to aid debugging.
	changed := deploymentChangedFields(existingSpec, &deployment.Spec)
	log.Info("Updating Deployment", "name", model.Name, "changedFields", changed)

	return r.Update(ctx, deployment)
}

// deploymentChangedFields returns a human-readable summary of what changed
// between two deployment specs. Compares the most operationally relevant fields.
func deploymentChangedFields(old, new *appsv1.DeploymentSpec) []string {
	var fields []string

	if !ptr.Equal(old.Replicas, new.Replicas) {
		fields = append(fields, fmt.Sprintf("replicas(%d→%d)",
			ptr.Deref(old.Replicas, 1), ptr.Deref(new.Replicas, 1)))
	}

	oldC, newC := firstContainer(old), firstContainer(new)
	if oldC != nil && newC != nil {
		if oldC.Image != newC.Image {
			fields = append(fields, fmt.Sprintf("image(%s→%s)", oldC.Image, newC.Image))
		}
		if !apiequality.Semantic.DeepEqual(oldC.Args, newC.Args) {
			fields = append(fields, "args")
		}
		if !apiequality.Semantic.DeepEqual(oldC.Env, newC.Env) {
			fields = append(fields, "env")
		}
		if !apiequality.Semantic.DeepEqual(oldC.Resources, newC.Resources) {
			fields = append(fields, "resources")
		}
		if !apiequality.Semantic.DeepEqual(oldC.VolumeMounts, newC.VolumeMounts) {
			fields = append(fields, "volumeMounts")
		}
	}

	if !apiequality.Semantic.DeepEqual(old.Template.Spec.NodeSelector, new.Template.Spec.NodeSelector) {
		fields = append(fields, "nodeSelector")
	}
	if !apiequality.Semantic.DeepEqual(old.Template.Spec.Volumes, new.Template.Spec.Volumes) {
		fields = append(fields, "volumes")
	}
	if !apiequality.Semantic.DeepEqual(old.Template.Spec.InitContainers, new.Template.Spec.InitContainers) {
		fields = append(fields, "initContainers")
	}
	if !apiequality.Semantic.DeepEqual(old.Template.Annotations, new.Template.Annotations) {
		fields = append(fields, "podAnnotations")
	}

	if len(fields) == 0 {
		fields = append(fields, "metadata")
	}
	return fields
}

func firstContainer(spec *appsv1.DeploymentSpec) *corev1.Container {
	if len(spec.Template.Spec.Containers) > 0 {
		return &spec.Template.Spec.Containers[0]
	}
	return nil
}

// buildBackendModelSpec converts Model spec to backend.ModelSpec.
func (r *ModelReconciler) buildBackendModelSpec(model *aiv1alpha2.Model, b backend.Backend, gpuVendor backend.GPUVendor) *backend.ModelSpec {
	modelValue := extractModelFromSource(model.Spec.Source)
	spec := &backend.ModelSpec{
		Model:     modelValue,
		ModelPath: "",
		GPUVendor: gpuVendor,
	}

	// Parse config into the spec
	if model.Spec.Config != nil {
		spec.Config = model.Spec.GetConfigMap()
	}

	storagePlan := resolveBackendStoragePlan(model, b, spec.Config)
	spec.ModelPath = storagePlan.ModelPath

	return spec
}

type backendStoragePlan struct {
	ModelPath          string
	ModelVolumeSubPath string
	HFCacheBasePath    string
}

// resolveBackendStoragePlan centralizes cache/storage path decisions so backend
// and source quirks are handled in one place.
func resolveBackendStoragePlan(model *aiv1alpha2.Model, b backend.Backend, config map[string]interface{}) backendStoragePlan {
	plan := backendStoragePlan{}
	source := model.Spec.Source
	modelValue := extractModelFromSource(source)
	strategy := cacheStrategy(model)

	backendName := ""
	needsVolume := false
	if b != nil {
		backendName = b.Name()
		needsVolume = b.NeedsVolume()
	}

	// HF sources can use the mounted model volume as a persistent hub cache.
	if strings.HasPrefix(source, "HF://") && needsVolume {
		plan.HFCacheBasePath = "/models/.cache/huggingface"
	}

	// SharedPVC + HF sources are prefetched into /models/<modelName>.
	if strings.HasPrefix(source, "HF://") && strategy == "SharedPVC" && model.Status.Cache != nil && model.Status.Cache.PVCName != "" {
		plan.ModelPath = "/models/" + model.Name
		// diffusers expects model_index.json at mount root.
		if backendName == "diffusers" {
			plan.ModelVolumeSubPath = model.Name
		}
	}

	// pvc://<pvc>/<subpath> is mounted at /models.
	if strings.HasPrefix(source, "pvc://") {
		if strings.HasPrefix(modelValue, "/") {
			plan.ModelPath = "/models" + modelValue
		} else {
			plan.ModelPath = "/models"
		}
	}

	// file:// paths are already in-container paths.
	if strings.HasPrefix(source, "file://") {
		plan.ModelPath = modelValue
	}

	// Backends that load a single GGUF file need a concrete file path under the
	// staged HF directory. llama.cpp always requires it; vLLM uses it when the
	// user specifies ggufFile to select a specific variant from multi-GGUF repos.
	if (backendName == "llamacpp" || backendName == "vllm") &&
		strings.HasPrefix(source, "HF://") &&
		strategy == "SharedPVC" &&
		model.Status.Cache != nil &&
		model.Status.Cache.PVCName != "" {
		if ggufFile := resolveGGUFFile(config); ggufFile != "" {
			plan.ModelPath = "/models/" + model.Name + "/" + ggufFile
		}
	}

	// When quantization completed, redirect model path to the quantized output subdirectory.
	if model.Spec.Quantize != nil &&
		model.Status.Cache != nil &&
		model.Status.Cache.Quantization != nil &&
		model.Status.Cache.Quantization.Type != "" {
		quantizedSubdir := quantizedOutputDir(model.Spec.Quantize)
		if quantizedSubdir != "" {
			plan.ModelPath = "/models/" + model.Name + "/" + quantizedSubdir
		}
	}

	return plan
}

// quantizedOutputDir returns the output subdirectory name for a given quantization spec.
func quantizedOutputDir(spec *aiv1alpha1.QuantizationSpec) string {
	if spec == nil {
		return ""
	}
	switch spec.Format {
	case aiv1alpha1.QuantizationFormatAWQ:
		bits := int32(quantization.DefaultAWQBits)
		if spec.Bits != nil {
			bits = *spec.Bits
		}
		groupSize := int32(quantization.DefaultQuantizationGroupSize)
		if spec.GroupSize != nil {
			groupSize = *spec.GroupSize
		}
		return fmt.Sprintf("awq-w%d-g%d", bits, groupSize)
	case aiv1alpha1.QuantizationFormatGPTQ:
		bits := int32(quantization.DefaultGPTQBits)
		if spec.Bits != nil {
			bits = *spec.Bits
		}
		groupSize := int32(quantization.DefaultQuantizationGroupSize)
		if spec.GroupSize != nil {
			groupSize = *spec.GroupSize
		}
		return fmt.Sprintf("gptq-w%d-g%d", bits, groupSize)
	default:
		return ""
	}
}

func resolveGGUFFile(config map[string]interface{}) string {
	if config == nil {
		return ""
	}

	ggufFile := ""
	if v, ok := config["ggufFile"]; ok {
		if s, ok := v.(string); ok {
			ggufFile = s
		}
	}
	if strings.TrimSpace(ggufFile) == "" {
		if v, ok := config["modelFile"]; ok {
			if s, ok := v.(string); ok {
				ggufFile = s
			}
		}
	}

	ggufFile = strings.TrimLeft(strings.TrimSpace(ggufFile), "/")
	// Best-effort safety: ignore traversal attempts.
	if ggufFile != "" && !strings.Contains(ggufFile, "..") {
		return ggufFile
	}
	return ""
}

// extractModelFromSource parses the model name from the source URI.
func extractModelFromSource(source string) string {
	// Handle different source formats:
	// HF://org/model -> org/model
	// ollama://model:tag -> model:tag
	// file:///path/to/model -> /path/to/model
	// pvc://name/path -> /path

	if strings.HasPrefix(source, "HF://") {
		return strings.TrimPrefix(source, "HF://")
	}
	if strings.HasPrefix(source, "ollama://") {
		return strings.TrimPrefix(source, "ollama://")
	}
	if strings.HasPrefix(source, "file://") {
		return strings.TrimPrefix(source, "file://")
	}
	if strings.HasPrefix(source, "pvc://") {
		parts := strings.SplitN(strings.TrimPrefix(source, "pvc://"), "/", 2)
		if len(parts) == 2 {
			return "/" + parts[1]
		}
	}
	return source
}

// getVolumeSource returns the appropriate volume source based on cache strategy.
func (r *ModelReconciler) getVolumeSource(model *aiv1alpha2.Model) corev1.VolumeSource {
	if pvcName, _, ok := parsePVCSource(model.Spec.Source); ok {
		if shouldStagePVCSourceToCache(model) {
			cacheName, _ := cachePVCName(model)
			return corev1.VolumeSource{
				PersistentVolumeClaim: &corev1.PersistentVolumeClaimVolumeSource{
					ClaimName: cacheName,
				},
			}
		}
		return corev1.VolumeSource{
			PersistentVolumeClaim: &corev1.PersistentVolumeClaimVolumeSource{
				ClaimName: pvcName,
			},
		}
	}

	strategy := "SharedPVC" // default
	if model.Spec.Cache != nil && model.Spec.Cache.Strategy != "" {
		strategy = model.Spec.Cache.Strategy
	} else if model.Spec.IsShared() {
		strategy = "Memory" // Use RAM cache for shared GPU to enable fast swapping
	}

	switch strategy {
	case "Memory":
		// Use emptyDir with memory medium for fast model swapping
		return corev1.VolumeSource{
			EmptyDir: &corev1.EmptyDirVolumeSource{
				Medium: corev1.StorageMediumMemory,
			},
		}
	case "Local":
		// Use hostPath for NVMe-backed local model storage
		return corev1.VolumeSource{
			HostPath: &corev1.HostPathVolumeSource{
				Path: resolveLocalCachePath(model),
				Type: hostPathTypePtr(corev1.HostPathDirectoryOrCreate),
			},
		}
	case "None":
		// No persistent volume, download each time
		return corev1.VolumeSource{
			EmptyDir: &corev1.EmptyDirVolumeSource{},
		}
	default: // SharedPVC
		pvcName := model.Name + "-cache"
		if model.Spec.Cache != nil && model.Spec.Cache.PVCName != "" {
			pvcName = model.Spec.Cache.PVCName
		}
		return corev1.VolumeSource{
			PersistentVolumeClaim: &corev1.PersistentVolumeClaimVolumeSource{
				ClaimName: pvcName,
			},
		}
	}
}

func cacheStrategy(model *aiv1alpha2.Model) string {
	if model.Spec.Cache != nil && model.Spec.Cache.Strategy != "" {
		return model.Spec.Cache.Strategy
	}
	if model.Spec.IsShared() {
		return "Memory"
	}
	return "SharedPVC"
}

func shouldStagePVCSourceToCache(model *aiv1alpha2.Model) bool {
	if !strings.HasPrefix(model.Spec.Source, "pvc://") {
		return false
	}
	if model.Spec.Cache == nil {
		return false
	}
	// If cache is specified for a pvc:// source, treat it as a request to stage/copy
	// the artifact into the cache PVC (typically NVMe-backed) before starting.
	if model.Spec.Cache.Strategy == "" {
		return true
	}
	return model.Spec.Cache.Strategy == "SharedPVC"
}

func cachePVCName(model *aiv1alpha2.Model) (string, bool) {
	if model.Spec.Cache != nil && model.Spec.Cache.PVCName != "" {
		return model.Spec.Cache.PVCName, false
	}
	return model.Name + "-cache", true
}

func cacheStorageClass(model *aiv1alpha2.Model) string {
	if model.Spec.Cache != nil && model.Spec.Cache.StorageClass != "" {
		return model.Spec.Cache.StorageClass
	}
	return "longhorn"
}

func cacheSize(model *aiv1alpha2.Model) string {
	if model.Spec.Cache != nil && model.Spec.Cache.Size != "" {
		return model.Spec.Cache.Size
	}
	return "50Gi"
}

// compilationCacheMountPath is where compilation caches are mounted inside the container.
const compilationCacheMountPath = "/cache/compile"

// resolveCompilationCache determines whether compilation cache should be injected
// and returns the hostPath directory for this model. Returns ("", false) if disabled.
func resolveCompilationCache(model *aiv1alpha2.Model) (hostPath string, enabled bool) {
	// Check explicit CRD configuration
	if model.Spec.Cache != nil && model.Spec.Cache.CompilationCache != nil {
		cc := model.Spec.Cache.CompilationCache
		if cc.Enabled != nil && !*cc.Enabled {
			return "", false
		}
		basePath := "/var/lib/flexinfer/compile-cache"
		if cc.HostPath != "" {
			basePath = cc.HostPath
		}
		return filepath.Join(basePath, model.Namespace, model.Name), true
	}

	// Auto-enable for shared AMD GPU models (the common swap case)
	if model.Spec.IsShared() && model.Spec.GPU != nil &&
		(model.Spec.GPU.Vendor == "amd" || model.Spec.GPU.Vendor == "auto") {
		return filepath.Join("/var/lib/flexinfer/compile-cache", model.Namespace, model.Name), true
	}

	return "", false
}

// resolveLocalCachePath returns the hostPath directory for this model's local cache.
func resolveLocalCachePath(model *aiv1alpha2.Model) string {
	basePath := "/var/lib/flexinfer/models"
	if model.Spec.Cache != nil && model.Spec.Cache.HostPath != "" {
		basePath = model.Spec.Cache.HostPath
	}
	return filepath.Join(basePath, model.Namespace, model.Name)
}

func parsePVCSource(source string) (pvcName string, subPath string, ok bool) {
	if !strings.HasPrefix(source, "pvc://") {
		return "", "", false
	}
	rest := strings.TrimPrefix(source, "pvc://")
	if rest == "" {
		return "", "", false
	}
	parts := strings.SplitN(rest, "/", 2)
	if parts[0] == "" {
		return "", "", false
	}
	pvcName = parts[0]
	if len(parts) == 2 {
		subPath = parts[1]
	}
	return pvcName, subPath, true
}

func hfCacheEnvVars(basePath string) []corev1.EnvVar {
	basePath = strings.TrimRight(basePath, "/")
	return []corev1.EnvVar{
		{Name: "HF_HOME", Value: basePath},
		{Name: "HF_HUB_CACHE", Value: basePath + "/hub"},
		{Name: "HUGGINGFACE_HUB_CACHE", Value: basePath + "/hub"},
		{Name: "TRANSFORMERS_CACHE", Value: basePath + "/transformers"},
	}
}

func mergeEnv(existing []corev1.EnvVar, additional []corev1.EnvVar) []corev1.EnvVar {
	if len(additional) == 0 {
		return existing
	}
	out := make([]corev1.EnvVar, 0, len(existing)+len(additional))
	indexByName := make(map[string]int, len(existing))
	for _, e := range existing {
		indexByName[e.Name] = len(out)
		out = append(out, e)
	}
	for _, e := range additional {
		if idx, ok := indexByName[e.Name]; ok {
			out[idx] = e
			continue
		}
		indexByName[e.Name] = len(out)
		out = append(out, e)
	}
	return out
}

func (r *ModelReconciler) ensureCache(ctx context.Context, model *aiv1alpha2.Model, b backend.Backend) (bool, error) {
	if !b.NeedsVolume() {
		return true, nil
	}

	original := model.DeepCopy()

	if pvcName, _, ok := parsePVCSource(model.Spec.Source); ok {
		sourcePVC := &corev1.PersistentVolumeClaim{}
		if err := r.Get(ctx, types.NamespacedName{Name: pvcName, Namespace: model.Namespace}, sourcePVC); err != nil {
			return false, err
		}

		// If cache is requested, stage/copy the source path into the cache PVC before starting.
		if shouldStagePVCSourceToCache(model) {
			cachePVCName, autoCreate := cachePVCName(model)
			cachePVC := &corev1.PersistentVolumeClaim{}
			cacheErr := r.Get(ctx, types.NamespacedName{Name: cachePVCName, Namespace: model.Namespace}, cachePVC)
			if cacheErr != nil && !errors.IsNotFound(cacheErr) {
				return false, cacheErr
			}
			if errors.IsNotFound(cacheErr) {
				if !autoCreate {
					return false, fmt.Errorf("pvc %q not found for model %s (spec.cache.pvcName is set)", cachePVCName, model.Name)
				}

				qty, err := resource.ParseQuantity(cacheSize(model))
				if err != nil {
					return false, fmt.Errorf("invalid cache size %q: %w", cacheSize(model), err)
				}

				storageClass := cacheStorageClass(model)
				cachePVC = &corev1.PersistentVolumeClaim{
					ObjectMeta: metav1.ObjectMeta{
						Name:      cachePVCName,
						Namespace: model.Namespace,
						Labels:    r.labelsForModel(model),
						OwnerReferences: []metav1.OwnerReference{
							*metav1.NewControllerRef(model, aiv1alpha2.GroupVersion.WithKind("Model")),
						},
					},
					Spec: corev1.PersistentVolumeClaimSpec{
						AccessModes: []corev1.PersistentVolumeAccessMode{corev1.ReadWriteOnce},
						Resources: corev1.VolumeResourceRequirements{
							Requests: corev1.ResourceList{
								corev1.ResourceStorage: qty,
							},
						},
					},
				}
				if storageClass != "" {
					cachePVC.Spec.StorageClassName = ptr.To(storageClass)
				}
				if err := r.Create(ctx, cachePVC); err != nil {
					return false, err
				}
			}

			jobName := model.Name + "-cache-copy"
			ready := false
			jobPhase := ""
			message := ""

			if sourcePVC.Status.Phase != corev1.ClaimBound {
				jobPhase = "Pending"
				message = "waiting for source PVC to bind"
			} else {
				job := &batchv1.Job{}
				err := r.Get(ctx, types.NamespacedName{Name: jobName, Namespace: model.Namespace}, job)
				if err != nil && !errors.IsNotFound(err) {
					return false, err
				}
				if err == nil && job.Annotations != nil && job.Annotations["flexinfer.ai/source"] != model.Spec.Source {
					if delErr := r.Delete(ctx, job); delErr != nil && !errors.IsNotFound(delErr) {
						return false, delErr
					}
					err = errors.NewNotFound(schema.GroupResource{Group: "batch", Resource: "jobs"}, jobName)
				}
				if errors.IsNotFound(err) {
					subPath := ""
					if _, sp, ok := parsePVCSource(model.Spec.Source); ok {
						subPath = sp
					}
					newJob, err := r.jobForCacheCopy(model, pvcName, cachePVCName, subPath)
					if err != nil {
						return false, err
					}
					if err := r.Create(ctx, newJob); err != nil {
						return false, err
					}
					jobPhase = "Running"
					message = "cache copy job started"
				} else {
					if job.Status.Succeeded > 0 {
						ready = true
						jobPhase = "Succeeded"
						message = "artifact copied to cache PVC"
					} else if job.Status.Failed > 0 {
						jobPhase = "Failed"
						message = "cache copy job failed"
					} else if job.Status.Active > 0 {
						jobPhase = "Running"
						message = "cache copy job running"
					} else {
						jobPhase = "Pending"
						message = "cache copy job pending"
					}
				}
			}

			if jobPhase == "" {
				jobPhase = "Pending"
			}
			if message == "" {
				message = "waiting for cache copy job"
			}

			model.Status.Cache = &aiv1alpha2.CacheStatus{
				Strategy:  "SharedPVC",
				PVCName:   cachePVCName,
				JobName:   jobName,
				JobPhase:  jobPhase,
				Message:   message,
				Ready:     ready,
				SizeBytes: 0,
			}
			setModelCondition(model, aiv1alpha2.ConditionModelCached, ready, "CacheCopy", message)

			if err := r.Status().Patch(ctx, model, client.MergeFrom(original)); err != nil {
				return false, err
			}
			return ready, nil
		}

		// Default pvc:// behavior: mount the source PVC directly, but run a check job so
		// status.cache.ready means "artifact present", not just "PVC bound".
		jobName := model.Name + "-cache-check"
		ready := false
		jobPhase := "Pending"
		message := "waiting for cache check job"

		job := &batchv1.Job{}
		err := r.Get(ctx, types.NamespacedName{Name: jobName, Namespace: model.Namespace}, job)
		if err != nil && !errors.IsNotFound(err) {
			return false, err
		}
		if err == nil && job.Annotations != nil && job.Annotations["flexinfer.ai/source"] != model.Spec.Source {
			if delErr := r.Delete(ctx, job); delErr != nil && !errors.IsNotFound(delErr) {
				return false, delErr
			}
			err = errors.NewNotFound(schema.GroupResource{Group: "batch", Resource: "jobs"}, jobName)
		}
		if errors.IsNotFound(err) {
			if sourcePVC.Status.Phase != corev1.ClaimBound {
				jobPhase = "Pending"
				message = "waiting for PVC to bind"
			} else {
				subPath := ""
				if _, sp, ok := parsePVCSource(model.Spec.Source); ok {
					subPath = sp
				}
				newJob, err := r.jobForCacheCheck(model, pvcName, subPath)
				if err != nil {
					return false, err
				}
				if err := r.Create(ctx, newJob); err != nil {
					return false, err
				}
				jobPhase = "Running"
				message = "cache check job started"
			}
		} else {
			if job.Status.Succeeded > 0 {
				ready = true
				jobPhase = "Succeeded"
				message = "artifact verified on PVC source"
			} else if job.Status.Failed > 0 {
				jobPhase = "Failed"
				message = "cache check job failed"
			} else if job.Status.Active > 0 {
				jobPhase = "Running"
				message = "cache check job running"
			}
		}

		model.Status.Cache = &aiv1alpha2.CacheStatus{
			Strategy:  "SharedPVC",
			PVCName:   pvcName,
			JobName:   jobName,
			JobPhase:  jobPhase,
			Message:   message,
			Ready:     ready,
			SizeBytes: 0,
		}
		setModelCondition(model, aiv1alpha2.ConditionModelCached, ready, "CacheCheck", message)

		if err := r.Status().Patch(ctx, model, client.MergeFrom(original)); err != nil {
			return false, err
		}
		return ready, nil
	}

	strategy := cacheStrategy(model)
	model.Status.Cache = &aiv1alpha2.CacheStatus{
		Strategy: strategy,
		Ready:    true,
	}

	if strategy != "SharedPVC" {
		setModelCondition(model, aiv1alpha2.ConditionModelCached, true, "NoCacheJob", "no cache job required")
		if err := r.Status().Patch(ctx, model, client.MergeFrom(original)); err != nil {
			return false, err
		}
		return true, nil
	}

	pvcName, autoCreate := cachePVCName(model)
	model.Status.Cache.PVCName = pvcName
	model.Status.Cache.Ready = false

	pvc := &corev1.PersistentVolumeClaim{}
	err := r.Get(ctx, types.NamespacedName{Name: pvcName, Namespace: model.Namespace}, pvc)
	if err != nil && !errors.IsNotFound(err) {
		return false, err
	}

	if errors.IsNotFound(err) {
		if !autoCreate {
			return false, fmt.Errorf("pvc %q not found for model %s (spec.cache.pvcName is set)", pvcName, model.Name)
		}

		qty, err := resource.ParseQuantity(cacheSize(model))
		if err != nil {
			return false, fmt.Errorf("invalid cache size %q: %w", cacheSize(model), err)
		}

		storageClass := cacheStorageClass(model)
		pvc = &corev1.PersistentVolumeClaim{
			ObjectMeta: metav1.ObjectMeta{
				Name:      pvcName,
				Namespace: model.Namespace,
				Labels:    r.labelsForModel(model),
				OwnerReferences: []metav1.OwnerReference{
					*metav1.NewControllerRef(model, aiv1alpha2.GroupVersion.WithKind("Model")),
				},
			},
			Spec: corev1.PersistentVolumeClaimSpec{
				AccessModes: []corev1.PersistentVolumeAccessMode{corev1.ReadWriteOnce},
				Resources: corev1.VolumeResourceRequirements{
					Requests: corev1.ResourceList{
						corev1.ResourceStorage: qty,
					},
				},
			},
		}
		if storageClass != "" {
			pvc.Spec.StorageClassName = ptr.To(storageClass)
		}

		if err := r.Create(ctx, pvc); err != nil {
			return false, err
		}
	}

	// For HF sources with SharedPVC, run a prefetch job that materializes the artifact directory.
	if strings.HasPrefix(model.Spec.Source, "HF://") {
		jobName := model.Name + "-cache-prefetch"
		model.Status.Cache.JobName = jobName

		job := &batchv1.Job{}
		err := r.Get(ctx, types.NamespacedName{Name: jobName, Namespace: model.Namespace}, job)
		if err != nil && !errors.IsNotFound(err) {
			return false, err
		}
		if err == nil && job.Annotations != nil && job.Annotations["flexinfer.ai/source"] != model.Spec.Source {
			if delErr := r.Delete(ctx, job); delErr != nil && !errors.IsNotFound(delErr) {
				return false, delErr
			}
			err = errors.NewNotFound(schema.GroupResource{Group: "batch", Resource: "jobs"}, jobName)
		}
		if errors.IsNotFound(err) {
			newJob, err := r.jobForPrefetch(model, pvcName, model.Name)
			if err != nil {
				return false, err
			}
			if err := r.Create(ctx, newJob); err != nil {
				return false, err
			}
			model.Status.Cache.JobPhase = "Running"
			model.Status.Cache.Message = "prefetch job started"
			setModelCondition(model, aiv1alpha2.ConditionModelCached, false, "PrefetchRunning", model.Status.Cache.Message)
			if err := r.Status().Patch(ctx, model, client.MergeFrom(original)); err != nil {
				return false, err
			}
			return false, nil
		}

		if job.Status.Succeeded > 0 {
			// Prefetch done — run quantization if requested.
			if model.Spec.Quantize != nil {
				quantizeReady, qErr := r.ensureQuantization(ctx, model, pvcName, original)
				if qErr != nil {
					return false, qErr
				}
				if !quantizeReady {
					return false, nil
				}
				// Quantization done — cache is ready.
			}
			model.Status.Cache.Ready = true
			model.Status.Cache.JobPhase = "Succeeded"
			model.Status.Cache.Message = "artifact prefetched"
			if model.Spec.Quantize != nil {
				model.Status.Cache.Message = "artifact prefetched and quantized"
			}
			setModelCondition(model, aiv1alpha2.ConditionModelCached, true, "PrefetchSucceeded", model.Status.Cache.Message)
		} else if job.Status.Failed > 0 {
			model.Status.Cache.Ready = false
			model.Status.Cache.JobPhase = "Failed"
			model.Status.Cache.Message = "prefetch job failed"
			setModelCondition(model, aiv1alpha2.ConditionModelCached, false, "PrefetchFailed", model.Status.Cache.Message)
		} else if job.Status.Active > 0 {
			model.Status.Cache.Ready = false
			model.Status.Cache.JobPhase = "Running"
			model.Status.Cache.Message = "prefetch job running"
			setModelCondition(model, aiv1alpha2.ConditionModelCached, false, "PrefetchRunning", model.Status.Cache.Message)
		} else {
			model.Status.Cache.Ready = false
			model.Status.Cache.JobPhase = "Pending"
			if pvc.Status.Phase != corev1.ClaimBound {
				model.Status.Cache.Message = "prefetch job pending (waiting for PVC bind/schedule)"
			} else {
				model.Status.Cache.Message = "prefetch job pending"
			}
			setModelCondition(model, aiv1alpha2.ConditionModelCached, false, "PrefetchPending", model.Status.Cache.Message)
		}
	} else {
		model.Status.Cache.Ready = true
		model.Status.Cache.JobPhase = "Succeeded"
		if pvc.Status.Phase == corev1.ClaimBound {
			model.Status.Cache.Message = "cache PVC is bound"
			setModelCondition(model, aiv1alpha2.ConditionModelCached, true, "PVCBound", model.Status.Cache.Message)
		} else {
			model.Status.Cache.Message = "cache PVC will bind on first use"
			setModelCondition(model, aiv1alpha2.ConditionModelCached, true, "PVCProvisioning", model.Status.Cache.Message)
		}
	}

	if err := r.Status().Patch(ctx, model, client.MergeFrom(original)); err != nil {
		return false, err
	}
	return model.Status.Cache.Ready, nil
}

// ensureQuantization manages the quantization job lifecycle for a model.
// Returns (ready, error) — ready=true means quantization is complete.
func (r *ModelReconciler) ensureQuantization(ctx context.Context, model *aiv1alpha2.Model, pvcName string, original *aiv1alpha2.Model) (bool, error) {
	log := log.FromContext(ctx)
	spec := model.Spec.Quantize

	builder, err := quantization.GetBuilder(spec.Format)
	if err != nil {
		return false, fmt.Errorf("unsupported quantization format %q: %w", spec.Format, err)
	}
	if err := builder.Validate(spec); err != nil {
		return false, fmt.Errorf("invalid quantization spec: %w", err)
	}

	jobName := model.Name + "-quantize"

	// Determine GPU vendor for the quantization job.
	gpuVendor := "nvidia"
	if model.Spec.GPU != nil {
		switch model.Spec.GetGPUVendor() {
		case aiv1alpha2.GPUVendorAMD:
			gpuVendor = "amd"
		case aiv1alpha2.GPUVendorNVIDIA:
			gpuVendor = "nvidia"
		}
	}

	params := quantization.JobParams{
		Name:         model.Name,
		Namespace:    model.Namespace,
		PVCName:      pvcName,
		ModelPath:    model.Name,
		Spec:         spec,
		GPUVendor:    gpuVendor,
		NodeSelector: model.Spec.NodeSelector,
	}

	job := &batchv1.Job{}
	err = r.Get(ctx, types.NamespacedName{Name: jobName, Namespace: model.Namespace}, job)
	if err != nil && !errors.IsNotFound(err) {
		return false, err
	}

	if errors.IsNotFound(err) {
		newJob, buildErr := builder.BuildJob(params)
		if buildErr != nil {
			return false, fmt.Errorf("building quantization job: %w", buildErr)
		}
		newJob.OwnerReferences = []metav1.OwnerReference{
			*metav1.NewControllerRef(model, aiv1alpha2.GroupVersion.WithKind("Model")),
		}
		if err := r.Create(ctx, newJob); err != nil {
			return false, err
		}
		log.Info("created quantization job", "job", jobName, "format", spec.Format)

		model.Status.Cache.Ready = false
		model.Status.Cache.JobName = jobName
		model.Status.Cache.JobPhase = "Running"
		model.Status.Cache.Message = fmt.Sprintf("quantization job started (format=%s)", spec.Format)
		setModelCondition(model, aiv1alpha2.ConditionModelCached, false, "QuantizationRunning", model.Status.Cache.Message)
		if err := r.Status().Patch(ctx, model, client.MergeFrom(original)); err != nil {
			return false, err
		}
		return false, nil
	}

	// Job exists — check status.
	if job.Status.Succeeded > 0 {
		meta, _ := r.readQuantizationMetadataFromJob(ctx, model.Namespace, jobName)
		if meta != nil {
			model.Status.Cache.Quantization = &aiv1alpha1.QuantizationStatus{
				Format:              string(spec.Format),
				Type:                meta.Type,
				OriginalSizeBytes:   meta.OriginalSizeBytes,
				CompressedSizeBytes: meta.CompressedSizeBytes,
			}
			if meta.OriginalSizeBytes > 0 && meta.CompressedSizeBytes > 0 {
				ratio := float64(meta.OriginalSizeBytes) / float64(meta.CompressedSizeBytes)
				model.Status.Cache.Quantization.CompressionRatio = fmt.Sprintf("%.2f", ratio)
			}
			if meta.QuantizationTimeSeconds > 0 {
				model.Status.Cache.Quantization.QuantizationTime = fmt.Sprintf("%ds", meta.QuantizationTimeSeconds)
			}
		}
		log.Info("quantization job completed", "job", jobName)
		return true, nil
	}

	if job.Status.Failed > 0 {
		model.Status.Cache.Ready = false
		model.Status.Cache.JobName = jobName
		model.Status.Cache.JobPhase = "Failed"
		model.Status.Cache.Message = "quantization job failed"
		setModelCondition(model, aiv1alpha2.ConditionModelCached, false, "QuantizationFailed", model.Status.Cache.Message)
		if err := r.Status().Patch(ctx, model, client.MergeFrom(original)); err != nil {
			return false, err
		}
		return false, nil
	}

	// Still running.
	model.Status.Cache.Ready = false
	model.Status.Cache.JobName = jobName
	model.Status.Cache.JobPhase = "Running"
	model.Status.Cache.Message = "quantization job running"
	setModelCondition(model, aiv1alpha2.ConditionModelCached, false, "QuantizationRunning", model.Status.Cache.Message)
	if err := r.Status().Patch(ctx, model, client.MergeFrom(original)); err != nil {
		return false, err
	}
	return false, nil
}

// quantizationJobMetadataV2 mirrors the termination-log JSON from quantization jobs.
type quantizationJobMetadataV2 struct {
	Type                    string `json:"type,omitempty"`
	OriginalSizeBytes       int64  `json:"originalSizeBytes,omitempty"`
	CompressedSizeBytes     int64  `json:"compressedSizeBytes,omitempty"`
	QuantizationTimeSeconds int64  `json:"quantizationTimeSeconds,omitempty"`
	OutputFile              string `json:"outputFile,omitempty"`
	OutputDir               string `json:"outputDir,omitempty"`
}

// readQuantizationMetadataFromJob reads termination-log metadata from completed quantization job pods.
func (r *ModelReconciler) readQuantizationMetadataFromJob(ctx context.Context, namespace, jobName string) (*quantizationJobMetadataV2, error) {
	podList := &corev1.PodList{}
	if err := r.List(ctx, podList, client.InNamespace(namespace), client.MatchingLabels{"job-name": jobName}); err != nil {
		return nil, err
	}

	for i := range podList.Items {
		pod := &podList.Items[i]
		for _, cs := range pod.Status.ContainerStatuses {
			if cs.Name != "quantizer" {
				continue
			}
			if cs.State.Terminated == nil {
				continue
			}
			msg := strings.TrimSpace(cs.State.Terminated.Message)
			if msg == "" {
				continue
			}
			var meta quantizationJobMetadataV2
			if err := json.Unmarshal([]byte(msg), &meta); err != nil {
				continue
			}
			return &meta, nil
		}
	}
	return nil, nil
}

// handleSharedGPU implements GPU sharing logic for models with gpu.shared set.
// Demand-based swap tuning constants for shared GPU groups.
const (
	// sharedDemandWindow is how recent a LastActiveTime must be to count as
	// active demand from the proxy.
	sharedDemandWindow = 2 * time.Minute

	// sharedSwapCooldown prevents thrashing by blocking demand-based swaps
	// for this duration after the most recent preemption.  Set high enough
	// to cover model loading (large diffusers models need 3-5 min).
	sharedSwapCooldown = 5 * time.Minute
)

func chooseSharedGroupLeader(groupModels []*aiv1alpha2.Model, now time.Time) *aiv1alpha2.Model {
	if len(groupModels) == 0 {
		return nil
	}

	better := func(a, b *aiv1alpha2.Model) *aiv1alpha2.Model {
		if a == nil {
			return b
		}
		if b == nil {
			return a
		}
		pa := a.Spec.GetPriority()
		pb := b.Spec.GetPriority()
		if pa != pb {
			if pa > pb {
				return a
			}
			return b
		}
		if a.Status.LastActiveTime != nil && b.Status.LastActiveTime != nil {
			if !a.Status.LastActiveTime.Equal(b.Status.LastActiveTime) {
				if a.Status.LastActiveTime.After(b.Status.LastActiveTime.Time) {
					return a
				}
				return b
			}
		}
		if a.Name <= b.Name {
			return a
		}
		return b
	}

	// Anti-thrashing: if a swap happened recently, keep the currently
	// active model regardless of demand or priority.
	// Use the maximum SwapCooldown from any model in the group, falling
	// back to the default (5m) if none specify it.
	cooldown := sharedSwapCooldown
	for _, m := range groupModels {
		if m.Spec.GPU != nil && m.Spec.GPU.SwapCooldown != nil {
			if d := m.Spec.GPU.SwapCooldown.Duration; d > cooldown {
				cooldown = d
			}
		}
	}
	recentSwap := false
	for _, m := range groupModels {
		if m.Status.SharedGroup != nil && m.Status.SharedGroup.PreemptedAt != nil {
			if now.Sub(m.Status.SharedGroup.PreemptedAt.Time) < cooldown {
				recentSwap = true
				break
			}
		}
	}
	if recentSwap {
		for _, m := range groupModels {
			if m.Status.SharedGroup != nil && m.Status.SharedGroup.State == "Active" {
				return m
			}
		}
	}

	var readyLeader *aiv1alpha2.Model
	var recentLeader *aiv1alpha2.Model
	var fallbackLeader *aiv1alpha2.Model
	var demandedLeader *aiv1alpha2.Model
	for _, m := range groupModels {
		fallbackLeader = better(fallbackLeader, m)
		if m.Status.Phase == aiv1alpha2.ModelPhaseReady {
			readyLeader = better(readyLeader, m)
			continue
		}
		if m.Status.LastActiveTime == nil {
			continue
		}
		if now.Sub(m.Status.LastActiveTime.Time) < 5*time.Minute {
			recentLeader = better(recentLeader, m)
		}
		if now.Sub(m.Status.LastActiveTime.Time) < sharedDemandWindow {
			demandedLeader = better(demandedLeader, m)
		}
	}

	// Demand-based preemption: if a non-ready model has recent demand
	// (proxy set its LastActiveTime) and the current ready leader is idle,
	// swap to the demanded model.
	if demandedLeader != nil && readyLeader != nil {
		readyIdle := readyLeader.Status.LastActiveTime == nil ||
			now.Sub(readyLeader.Status.LastActiveTime.Time) > sharedDemandWindow
		if readyIdle && demandedLeader.Spec.GetPriority() >= readyLeader.Spec.GetPriority() {
			return demandedLeader
		}
	}

	if readyLeader != nil {
		return readyLeader
	}
	if recentLeader != nil {
		return recentLeader
	}
	return fallbackLeader
}

func queuePositionForSharedModel(modelName string, activeModel *aiv1alpha2.Model, groupModels []*aiv1alpha2.Model) int32 {
	if activeModel != nil && modelName == activeModel.Name {
		return 0
	}

	queued := make([]*aiv1alpha2.Model, 0, len(groupModels))
	for _, m := range groupModels {
		if activeModel != nil && m.Name == activeModel.Name {
			continue
		}
		queued = append(queued, m)
	}
	sort.Slice(queued, func(i, j int) bool {
		pi := queued[i].Spec.GetPriority()
		pj := queued[j].Spec.GetPriority()
		if pi != pj {
			return pi > pj
		}
		return queued[i].Name < queued[j].Name
	})
	for i, m := range queued {
		if m.Name == modelName {
			return int32(i + 1)
		}
	}
	return 0
}

func sharedGroupStatusEqual(a, b *aiv1alpha2.SharedGroupStatus) bool {
	switch {
	case a == nil && b == nil:
		return true
	case a == nil || b == nil:
		return false
	}
	if a.GroupName != b.GroupName || a.State != b.State || a.QueuePosition != b.QueuePosition || a.PreemptedBy != b.PreemptedBy {
		return false
	}
	switch {
	case a.PreemptedAt == nil && b.PreemptedAt == nil:
		return true
	case a.PreemptedAt == nil || b.PreemptedAt == nil:
		return false
	default:
		return a.PreemptedAt.Equal(b.PreemptedAt)
	}
}

func cloneSharedGroupStatus(in *aiv1alpha2.SharedGroupStatus) *aiv1alpha2.SharedGroupStatus {
	if in == nil {
		return nil
	}
	out := *in
	return &out
}

func (r *ModelReconciler) handleSharedGPU(ctx context.Context, model *aiv1alpha2.Model) (ctrl.Result, error) {
	log := log.FromContext(ctx)

	if model.Spec.GPU == nil || model.Spec.GPU.Shared == "" {
		return ctrl.Result{}, nil
	}

	groupName := model.Spec.GPU.Shared

	// Find all models in the same shared group
	modelList := &aiv1alpha2.ModelList{}
	if err := r.List(ctx, modelList, client.InNamespace(model.Namespace)); err != nil {
		return ctrl.Result{}, err
	}

	var groupModels []*aiv1alpha2.Model
	for i := range modelList.Items {
		m := &modelList.Items[i]
		if m.Spec.GPU != nil && m.Spec.GPU.Shared == groupName {
			groupModels = append(groupModels, m)
		}
	}

	activeModel := chooseSharedGroupLeader(groupModels, time.Now())
	if activeModel == nil {
		return ctrl.Result{RequeueAfter: 3 * time.Second}, nil
	}

	// Update this model's shared group status
	origPhase := model.Status.Phase
	origShared := cloneSharedGroupStatus(model.Status.SharedGroup)

	if model.Status.SharedGroup == nil {
		model.Status.SharedGroup = &aiv1alpha2.SharedGroupStatus{}
	}
	model.Status.SharedGroup.GroupName = groupName

	if activeModel.Name == model.Name {
		// This model should be active
		model.Status.SharedGroup.State = "Active"
		model.Status.SharedGroup.QueuePosition = 0
		model.Status.SharedGroup.PreemptedBy = ""
		// Keep PreemptedAt until the model becomes Ready again so swap latency can be observed.
		if origShared == nil || origShared.State != "Active" {
			log.Info("Model is active in shared group", "group", groupName)
		}
	} else {
		// This model should be preempted/queued
		model.Status.SharedGroup.State = "Queued"
		model.Status.SharedGroup.QueuePosition = queuePositionForSharedModel(model.Name, activeModel, groupModels)
		model.Status.SharedGroup.PreemptedBy = activeModel.Name

		if model.Status.Phase == aiv1alpha2.ModelPhaseReady {
			// Preempt this model
			log.Info("Preempting model in favor of higher priority", "preemptedBy", activeModel.Name)
			model.Status.Phase = aiv1alpha2.ModelPhasePreempted
			model.Status.SharedGroup.PreemptedAt = &metav1.Time{Time: time.Now()}
			r.Recorder.Event(model, corev1.EventTypeNormal, "Preempted",
				fmt.Sprintf("Preempted by %s with priority %d", activeModel.Name, activeModel.Spec.GetPriority()))
		}
	}

	// Sync active service labels on Services for the entire group.
	// The active model's Service gets ai.flexinfer/active-services set;
	// all other group members have it removed.  This lets the proxy
	// route group-wide names (serviceLabels) only to the active model.
	r.syncActiveServiceLabels(ctx, activeModel, groupModels)

	if origPhase == model.Status.Phase && sharedGroupStatusEqual(origShared, model.Status.SharedGroup) {
		return ctrl.Result{RequeueAfter: 3 * time.Second}, nil
	}

	if err := r.Status().Update(ctx, model); err != nil {
		return ctrl.Result{}, err
	}

	return ctrl.Result{RequeueAfter: 3 * time.Second}, nil
}

// syncActiveServiceLabels sets the ai.flexinfer/active-services annotation on
// every Service in a shared-GPU group.  The active model gets its serviceLabels
// written into the annotation; all other group members get an empty string.
// An empty annotation (key present, value "") tells the proxy "this service is
// managed but currently inactive — do not fall back to static service-labels".
func (r *ModelReconciler) syncActiveServiceLabels(ctx context.Context, activeModel *aiv1alpha2.Model, groupModels []*aiv1alpha2.Model) {
	log := log.FromContext(ctx)
	const annoKey = "ai.flexinfer/active-services"

	for _, m := range groupModels {
		svc := &corev1.Service{}
		if err := r.Get(ctx, client.ObjectKey{Namespace: m.Namespace, Name: m.Name}, svc); err != nil {
			continue // service may not exist yet
		}
		if svc.Annotations == nil {
			svc.Annotations = make(map[string]string)
		}

		var desired string
		if m.Name == activeModel.Name && len(m.Spec.ServiceLabels) > 0 {
			desired = strings.Join(m.Spec.ServiceLabels, ",")
		}
		// desired is "" for non-active members — annotation is still SET so
		// the proxy knows not to fall back to static service-labels.

		current, exists := svc.Annotations[annoKey]
		if exists && current == desired {
			continue
		}

		svc.Annotations[annoKey] = desired
		if desired != "" {
			log.Info("Setting active service labels", "model", m.Name, "labels", desired)
		} else {
			log.Info("Clearing active service labels (inactive member)", "model", m.Name)
		}
		if err := r.Update(ctx, svc); err != nil {
			log.Error(err, "Failed to update active service labels", "model", m.Name)
		}
	}
}

// updateStatusFromDeployment updates the Model status based on the deployment state.
func (r *ModelReconciler) updateStatusFromDeployment(ctx context.Context, model *aiv1alpha2.Model) error {
	log := log.FromContext(ctx)
	prevPhase := model.Status.Phase
	prevReadyCond := modelCondition(model.Status.Conditions, aiv1alpha2.ConditionModelReady)
	readyStartedAt := time.Time{}
	prevReadyReason := ""
	if prevReadyCond != nil && prevReadyCond.Status == metav1.ConditionFalse {
		readyStartedAt = prevReadyCond.LastTransitionTime.Time
		prevReadyReason = prevReadyCond.Reason
	} else if prevReadyCond != nil {
		prevReadyReason = prevReadyCond.Reason
	}
	now := time.Now()

	deployment := &appsv1.Deployment{}
	if err := r.Get(ctx, types.NamespacedName{Name: model.Name, Namespace: model.Namespace}, deployment); err != nil {
		if errors.IsNotFound(err) {
			return nil
		}
		return err
	}

	// Update endpoint
	port := int32(80)
	if b, ok := backend.Get(model.Spec.Backend); ok {
		port = b.Port()
	} else {
		log.Error(fmt.Errorf("backend %q not found", model.Spec.Backend), "failed to resolve backend port for endpoint", "backend", model.Spec.Backend)
	}
	model.Status.Endpoint = k8surl.ServiceURL(model.Name, model.Namespace, port, false)

	// If the cache is not ready, keep the model in Pending regardless of deployment replicas.
	if model.Status.Cache != nil && !model.Status.Cache.Ready {
		setModelCondition(model, aiv1alpha2.ConditionModelCached, false, aiv1alpha2.ReasonCacheNotReady, "Cache is not ready")
		setModelCondition(model, aiv1alpha2.ConditionModelReady, false, aiv1alpha2.ReasonCacheNotReady, "Waiting for cache to be ready")
		if model.Status.Phase != aiv1alpha2.ModelPhasePreempted {
			model.Status.Phase = aiv1alpha2.ModelPhasePending
		}
		return r.Status().Update(ctx, model)
	}

	// Cache is ready (or not applicable)
	if model.Status.Cache != nil && model.Status.Cache.Ready {
		setModelCondition(model, aiv1alpha2.ConditionModelCached, true, aiv1alpha2.ReasonBackendReady, "Cache is ready")
	}

	// Determine phase from deployment status and set conditions
	if deployment.Status.ReadyReplicas > 0 {
		model.Status.Phase = aiv1alpha2.ModelPhaseReady
		setModelCondition(model, aiv1alpha2.ConditionModelReady, true, aiv1alpha2.ReasonBackendReady, "Backend is ready to serve requests")

		if prevPhase != aiv1alpha2.ModelPhaseReady && !readyStartedAt.IsZero() {
			loadDuration := now.Sub(readyStartedAt).Seconds()
			metrics.ModelColdStartDurationSeconds.WithLabelValues(
				model.Name,
				model.Namespace,
				model.Spec.Backend,
				cacheStrategy(model),
			).Observe(loadDuration)

			// Set last-known load time for the Mission Control dashboard.
			nodeName := ""
			if model.Status.GPU != nil {
				nodeName = model.Status.GPU.Node
			}
			metrics.ModelLoadSeconds.WithLabelValues(model.Name, nodeName).Set(loadDuration)
		}

		if model.Spec.IsShared() && model.Status.SharedGroup != nil && model.Status.SharedGroup.State == "Active" {
			group := model.Status.SharedGroup.GroupName
			swapStart := time.Time{}
			if model.Status.SharedGroup.PreemptedAt != nil {
				swapStart = model.Status.SharedGroup.PreemptedAt.Time
			} else if prevReadyReason == aiv1alpha2.ReasonPreempted && !readyStartedAt.IsZero() {
				swapStart = readyStartedAt
			}
			if !swapStart.IsZero() {
				metrics.ModelSwapDurationSeconds.WithLabelValues(
					model.Name,
					model.Namespace,
					model.Spec.Backend,
					group,
				).Observe(now.Sub(swapStart).Seconds())
				model.Status.SharedGroup.PreemptedAt = nil
			}
		}
	} else if *deployment.Spec.Replicas == 0 {
		if model.Status.Phase != aiv1alpha2.ModelPhasePreempted {
			model.Status.Phase = aiv1alpha2.ModelPhaseIdle
			setModelCondition(model, aiv1alpha2.ConditionModelReady, false, aiv1alpha2.ReasonWaitingForActivation, "Model is idle, waiting for traffic")
		} else {
			setModelCondition(model, aiv1alpha2.ConditionModelReady, false, aiv1alpha2.ReasonPreempted, "Model was preempted by higher priority model")
		}
	} else if deployment.Status.UnavailableReplicas > 0 {
		model.Status.Phase = aiv1alpha2.ModelPhaseLoading
		setModelCondition(model, aiv1alpha2.ConditionModelReady, false, aiv1alpha2.ReasonStartingBackend, "Backend container is starting")
	} else {
		model.Status.Phase = aiv1alpha2.ModelPhasePending
		setModelCondition(model, aiv1alpha2.ConditionModelReady, false, aiv1alpha2.ReasonStartingBackend, "Waiting for deployment to be ready")
	}

	return r.Status().Update(ctx, model)
}

// updatePhase updates just the phase field in status.
func (r *ModelReconciler) updatePhase(ctx context.Context, model *aiv1alpha2.Model, phase aiv1alpha2.ModelPhase) error {
	model.Status.Phase = phase
	return r.Status().Update(ctx, model)
}

// detectGPU detects the GPU vendor and architecture from nodes.
func (r *ModelReconciler) detectGPU(ctx context.Context, model *aiv1alpha2.Model) (backend.GPUVendor, string, error) {
	if model.Spec.GetGPUCount() == 0 || model.Spec.GetGPUVendor() == aiv1alpha2.GPUVendorCPU {
		return backend.GPUVendorCPU, "", nil
	}

	nodeList := &corev1.NodeList{}
	if err := r.List(ctx, nodeList); err != nil {
		return backend.GPUVendorUnknown, "", err
	}

	nodes := nodeList.Items
	if len(model.Spec.NodeSelector) > 0 {
		filtered := make([]corev1.Node, 0, len(nodes))
		for _, node := range nodes {
			matches := true
			for k, v := range model.Spec.NodeSelector {
				if node.Labels == nil || node.Labels[k] != v {
					matches = false
					break
				}
			}
			if matches {
				filtered = append(filtered, node)
			}
		}
		nodes = filtered
	}

	type nodeMatch struct {
		vendor backend.GPUVendor
		arch   string
	}
	isNodeReady := func(node corev1.Node) bool {
		for _, condition := range node.Status.Conditions {
			if condition.Type == corev1.NodeReady {
				return condition.Status == corev1.ConditionTrue
			}
		}
		return false
	}

	findFirst := func(vendor backend.GPUVendor) (nodeMatch, bool) {
		for _, node := range nodes {
			if !isNodeReady(node) {
				continue
			}
			switch vendor {
			case backend.GPUVendorNVIDIA:
				qty, ok := node.Status.Capacity["nvidia.com/gpu"]
				if !ok || qty.Value() < 1 {
					continue
				}
				major := ""
				if node.Labels != nil {
					major = node.Labels["nvidia.com/gpu.compute.major"]
				}
				arch := ""
				if major != "" {
					arch = "sm_" + major
				}
				// Fall back to flexinfer.ai/gpu.arch label (same as AMD detection).
				if arch == "" && node.Labels != nil {
					arch = node.Labels["flexinfer.ai/gpu.arch"]
				}
				return nodeMatch{vendor: backend.GPUVendorNVIDIA, arch: arch}, true
			case backend.GPUVendorAMD:
				qty, ok := node.Status.Capacity["amd.com/gpu"]
				if !ok || qty.Value() < 1 {
					continue
				}
				arch := ""
				if node.Labels != nil {
					arch = node.Labels["gpu.amd.com/gpu-architecture"]
					if arch == "" {
						// FlexInfer agent sets this label via rocminfo detection.
						arch = node.Labels["flexinfer.ai/gpu.arch"]
					}
					if arch == "" {
						// ROCm arch label isn't always present; fall back to common node-level labels.
						// Prefer RDNA3 dGPU (GC 11.0.0) when multiple AMD GPUs exist on the same node.
						if node.Labels["amd.com/gpu.family.GC_11_0_0"] != "" {
							arch = "gfx1100"
						} else if node.Labels["amd.com/gpu.family.GC_10_3_6"] != "" {
							arch = "gfx1036"
						} else if modelName := node.Labels["gpu.amd.com/model"]; strings.Contains(modelName, "7900") {
							arch = "gfx1100"
						}
					}
				}
				return nodeMatch{vendor: backend.GPUVendorAMD, arch: arch}, true
			default:
				continue
			}
		}
		return nodeMatch{}, false
	}

	switch model.Spec.GetGPUVendor() {
	case aiv1alpha2.GPUVendorNVIDIA:
		if match, ok := findFirst(backend.GPUVendorNVIDIA); ok {
			return match.vendor, match.arch, nil
		}
		return backend.GPUVendorUnknown, "", &noMatchingNodesError{reason: fmt.Sprintf("no NVIDIA GPU nodes match selector for model %s", model.Name)}
	case aiv1alpha2.GPUVendorAMD:
		if match, ok := findFirst(backend.GPUVendorAMD); ok {
			return match.vendor, match.arch, nil
		}
		return backend.GPUVendorUnknown, "", &noMatchingNodesError{reason: fmt.Sprintf("no AMD GPU nodes match selector for model %s", model.Name)}
	default: // auto
		nvidiaMatch, nvidiaOK := findFirst(backend.GPUVendorNVIDIA)
		amdMatch, amdOK := findFirst(backend.GPUVendorAMD)

		// Tighten vendor selection: when both vendors match, force the user to pick.
		// This avoids surprising behavior on mixed-vendor clusters where "auto" would
		// otherwise prefer NVIDIA.
		if nvidiaOK && amdOK {
			return backend.GPUVendorUnknown, "", &ambiguousGPUVendorError{
				reason: fmt.Sprintf(
					"gpu.vendor is %q but both NVIDIA and AMD GPU nodes match selector for model %s; set spec.gpu.vendor explicitly",
					aiv1alpha2.GPUVendorAuto,
					model.Name,
				),
			}
		}

		if nvidiaOK {
			return nvidiaMatch.vendor, nvidiaMatch.arch, nil
		}
		if amdOK {
			return amdMatch.vendor, amdMatch.arch, nil
		}
	}

	return backend.GPUVendorUnknown, "", &noMatchingNodesError{reason: fmt.Sprintf("no GPU nodes match selector for model %s", model.Name)}
}

// labelsForModel returns the labels to apply to resources for this model.
func (r *ModelReconciler) labelsForModel(model *aiv1alpha2.Model) map[string]string {
	labels := r.selectorLabelsForModel(model)

	var gpuGroup string
	if model.Spec.GPU != nil && model.Spec.GPU.Shared != "" {
		gpuGroup = model.Spec.GPU.Shared
	}
	if gpuGroup == "" && model.Status.SharedGroup != nil && model.Status.SharedGroup.GroupName != "" {
		gpuGroup = model.Status.SharedGroup.GroupName
	}
	if gpuGroup != "" {
		labels["flexinfer.ai/gpu-group"] = gpuGroup
	}

	return labels
}

func (r *ModelReconciler) selectorLabelsForModel(model *aiv1alpha2.Model) map[string]string {
	return map[string]string{
		"app.kubernetes.io/name":       "model",
		"app.kubernetes.io/instance":   model.Name,
		"app.kubernetes.io/managed-by": "flexinfer",
		"flexinfer.ai/model":           model.Name,
		"flexinfer.ai/backend":         model.Spec.Backend,
	}
}

func (r *ModelReconciler) podAnnotationsForModel(model *aiv1alpha2.Model) map[string]string {
	ann := map[string]string{
		"flexinfer.ai/model":   model.Name,
		"flexinfer.ai/backend": model.Spec.Backend,
	}
	if model.Spec.GPU != nil && model.Spec.GPU.VRAMEstimateMB != nil && *model.Spec.GPU.VRAMEstimateMB > 0 {
		ann["flexinfer.ai/gpu.vram-estimate-mb"] = fmt.Sprintf("%d", *model.Spec.GPU.VRAMEstimateMB)
	}
	return ann
}

// shouldScaleToZero checks if the model should be scaled to zero.
// getIdleTimeout returns the idle timeout for the model.
func getIdleTimeout(model *aiv1alpha2.Model, b backend.Backend) time.Duration {
	if model.Spec.Serverless != nil && model.Spec.Serverless.IdleTimeout != nil {
		return model.Spec.Serverless.IdleTimeout.Duration
	}
	return b.DefaultIdleTimeout()
}

type flashLoaderRuntimeConfig struct {
	Enabled         bool
	Image           string
	Concurrency     int
	TmpfsSizeLimit  *resource.Quantity
	BufferSizeKB    int
	VerifyIntegrity bool
	ExcludePatterns string
}

const (
	defaultFlashLoaderImage       = "registry.harbor.lan/flexinfer/flash-loader:latest"
	defaultFlashLoaderConcurrency = 4
	defaultSHMSizeLimitRaw        = "8Gi"
)

func defaultSHMSizeLimit() resource.Quantity {
	raw := strings.TrimSpace(os.Getenv("DEFAULT_SHM_SIZE_LIMIT"))
	if raw == "" {
		raw = defaultSHMSizeLimitRaw
	}
	if parsed, err := resource.ParseQuantity(raw); err == nil {
		return parsed
	}
	return resource.MustParse(defaultSHMSizeLimitRaw)
}

func envStringOrDefault(name, fallback string) string {
	if v := strings.TrimSpace(os.Getenv(name)); v != "" {
		return v
	}
	return fallback
}

func envIntOrDefault(name string, fallback int) int {
	v := strings.TrimSpace(os.Getenv(name))
	if v == "" {
		return fallback
	}
	n, err := strconv.Atoi(v)
	if err != nil || n <= 0 {
		return fallback
	}
	return n
}

func envBoolOrDefault(name string, fallback bool) bool {
	v := strings.TrimSpace(os.Getenv(name))
	if v == "" {
		return fallback
	}
	switch strings.ToLower(v) {
	case "1", "true", "yes", "on":
		return true
	case "0", "false", "no", "off":
		return false
	default:
		return fallback
	}
}

func parseOptionalQuantity(raw string) (*resource.Quantity, bool) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil, false
	}
	q, err := resource.ParseQuantity(raw)
	if err != nil {
		return nil, false
	}
	return &q, true
}

func modelUsesPersistentVolume(model *aiv1alpha2.Model) bool {
	if _, _, ok := parsePVCSource(model.Spec.Source); ok {
		return true
	}
	s := cacheStrategy(model)
	return s == "SharedPVC" || s == "Local"
}

func (r *ModelReconciler) matchingModelCache(ctx context.Context, model *aiv1alpha2.Model) *aiv1alpha1.ModelCache {
	cacheList := &aiv1alpha1.ModelCacheList{}
	if err := r.List(ctx, cacheList, client.InNamespace(model.Namespace)); err != nil {
		return nil
	}

	for i := range cacheList.Items {
		mc := &cacheList.Items[i]
		if mc.Spec.FlashLoader == nil {
			continue
		}
		// Match by source: if the ModelCache source matches the model source
		if strings.HasPrefix(model.Spec.Source, "HF://") && strings.Contains(mc.Spec.Source, strings.TrimPrefix(model.Spec.Source, "HF://")) {
			return mc
		}
		// Match by name convention: modelcache name = model name
		if mc.Name == model.Name || mc.Name == model.Name+"-cache" {
			return mc
		}
	}
	return nil
}

// resolveFlashLoaderConfig decides if flash-loader should be injected and which runtime settings to use.
// Resolution layers (lowest to highest priority): env vars → v1alpha1 ModelCache → v1alpha2 CacheSpec.FlashLoader.
func (r *ModelReconciler) resolveFlashLoaderConfig(ctx context.Context, model *aiv1alpha2.Model) flashLoaderRuntimeConfig {
	// Layer 1: Environment variable defaults
	cfg := flashLoaderRuntimeConfig{
		Enabled:         envBoolOrDefault("DEFAULT_FLASH_LOADER_ENABLED", false),
		Image:           envStringOrDefault("DEFAULT_FLASH_LOADER_IMAGE", defaultFlashLoaderImage),
		Concurrency:     envIntOrDefault("DEFAULT_FLASH_LOADER_CONCURRENCY", defaultFlashLoaderConcurrency),
		BufferSizeKB:    envIntOrDefault("DEFAULT_FLASH_LOADER_BUFFER_KB", 4096),
		VerifyIntegrity: envBoolOrDefault("DEFAULT_FLASH_LOADER_VERIFY", false),
		ExcludePatterns: envStringOrDefault("DEFAULT_FLASH_LOADER_EXCLUDE", ""),
	}
	if tmpfs, ok := parseOptionalQuantity(os.Getenv("DEFAULT_FLASH_LOADER_TMPFS_SIZE_LIMIT")); ok {
		cfg.TmpfsSizeLimit = tmpfs
	}

	// Layer 2: v1alpha1 ModelCache overrides
	if mc := r.matchingModelCache(ctx, model); mc != nil && mc.Spec.FlashLoader != nil {
		flash := mc.Spec.FlashLoader
		cfg.Enabled = flash.Enabled
		if strings.TrimSpace(flash.Image) != "" {
			cfg.Image = strings.TrimSpace(flash.Image)
		}
		if flash.Concurrency > 0 {
			cfg.Concurrency = flash.Concurrency
		}
		if flash.TmpfsSizeLimit != nil {
			if tmpfs, ok := parseOptionalQuantity(*flash.TmpfsSizeLimit); ok {
				cfg.TmpfsSizeLimit = tmpfs
			}
		}
	}

	// Layer 3: v1alpha2 Model.Spec.Cache.FlashLoader (highest priority)
	if model.Spec.Cache != nil && model.Spec.Cache.FlashLoader != nil {
		fl := model.Spec.Cache.FlashLoader
		if fl.Enabled != nil {
			cfg.Enabled = *fl.Enabled
		}
		if fl.Image != "" {
			cfg.Image = fl.Image
		}
		if fl.Concurrency != nil && *fl.Concurrency > 0 {
			cfg.Concurrency = int(*fl.Concurrency)
		}
		if fl.TmpfsSizeLimit != "" {
			if tmpfs, ok := parseOptionalQuantity(fl.TmpfsSizeLimit); ok {
				cfg.TmpfsSizeLimit = tmpfs
			}
		}
		if fl.BufferSizeKB != nil {
			cfg.BufferSizeKB = int(*fl.BufferSizeKB)
		}
		if fl.VerifyIntegrity != nil {
			cfg.VerifyIntegrity = *fl.VerifyIntegrity
		}
	}

	// Auto-enable for shared GPU models on Local strategy
	if model.Spec.Cache != nil && model.Spec.Cache.FlashLoader == nil &&
		model.Spec.IsShared() && cacheStrategy(model) == "Local" {
		cfg.Enabled = true
	}

	if cfg.Concurrency < 1 {
		cfg.Concurrency = defaultFlashLoaderConcurrency
	}
	if cfg.BufferSizeKB < 32 {
		cfg.BufferSizeKB = 4096
	}
	if !modelUsesPersistentVolume(model) {
		cfg.Enabled = false
	}
	return cfg
}

// reconcileKVCachePressure checks KV-cache utilization from agent annotations
// and reacts according to the model's KVCache pressure policy.
func (r *ModelReconciler) reconcileKVCachePressure(ctx context.Context, model *aiv1alpha2.Model) {
	if model.Spec.KVCache == nil {
		return
	}

	log := log.FromContext(ctx)

	// Read KV-cache utilization from the node where the model is running.
	if model.Status.GPU == nil || model.Status.GPU.Node == "" {
		return
	}

	node := &corev1.Node{}
	if err := r.Get(ctx, types.NamespacedName{Name: model.Status.GPU.Node}, node); err != nil {
		log.V(1).Info("cannot read node for KV-cache check", "node", model.Status.GPU.Node, "error", err)
		return
	}

	utilStr := ""
	if node.Annotations != nil {
		utilStr = node.Annotations["flexinfer.ai/kv-cache-usage"]
	}
	if utilStr == "" {
		return
	}

	util, err := strconv.ParseFloat(strings.TrimSpace(utilStr), 64)
	if err != nil {
		return
	}

	// Update status
	if model.Status.KVCache == nil {
		model.Status.KVCache = &aiv1alpha2.KVCacheStatus{}
	}
	model.Status.KVCache.Utilization = fmt.Sprintf("%.4f", util)

	highWatermark := model.Spec.GetKVCacheHighWatermark()
	lowWatermark := model.Spec.GetKVCacheLowWatermark()

	if util < highWatermark {
		model.Status.KVCache.Pressure = false
		return
	}

	// Pressure detected
	model.Status.KVCache.Pressure = true
	now := metav1.Now()
	model.Status.KVCache.LastPressureTime = &now

	policy := model.Spec.GetKVCachePressurePolicy()

	switch policy {
	case aiv1alpha2.KVCachePressurePolicyObserve:
		model.Status.KVCache.LastAction = "Observed"
		r.Recorder.Event(model, corev1.EventTypeWarning, "KVCachePressure",
			fmt.Sprintf("KV-cache utilization %.2f exceeds high watermark %.2f (policy: Observe)", util, highWatermark))

	case aiv1alpha2.KVCachePressurePolicyReconfigure:
		model.Status.KVCache.LastAction = "Reconfigure"
		r.Recorder.Event(model, corev1.EventTypeWarning, "KVCachePressure",
			fmt.Sprintf("KV-cache utilization %.2f exceeds high watermark %.2f (policy: Reconfigure, target: %.2f)", util, highWatermark, lowWatermark))

	case aiv1alpha2.KVCachePressurePolicyEvict:
		model.Status.KVCache.LastAction = "EvictRequested"
		r.Recorder.Event(model, corev1.EventTypeWarning, "KVCachePressure",
			fmt.Sprintf("KV-cache utilization %.2f exceeds high watermark %.2f (policy: Evict)", util, highWatermark))
		log.Info("KV-cache pressure: eviction requested", "model", model.Name, "utilization", util, "highWatermark", highWatermark)
	}
}

// SetupWithManager sets up the controller with the Manager.
func (r *ModelReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&aiv1alpha2.Model{}).
		Owns(&appsv1.Deployment{}).
		Owns(&corev1.Service{}).
		Owns(&corev1.PersistentVolumeClaim{}).
		Owns(&batchv1.Job{}).
		Complete(r)
}

// checkAliasConflicts detects litellm.aliases and copilotAlias conflicts
// across all Models in the namespace. Sets the ConfigValid condition accordingly.
// litellm.aliases are global (proxy resolves across ALL models), so duplicates
// cause non-deterministic routing. serviceLabels are exempt — they are group-aware.
func (r *ModelReconciler) checkAliasConflicts(ctx context.Context, model *aiv1alpha2.Model) {
	if model.Spec.LiteLLM == nil {
		setModelCondition(model, aiv1alpha2.ConditionConfigValid, true, aiv1alpha2.ReasonConfigValid, "No litellm config to validate")
		return
	}

	var allModels aiv1alpha2.ModelList
	if err := r.List(ctx, &allModels, client.InNamespace(model.Namespace)); err != nil {
		return // skip check on list failure
	}

	// Build alias → owner map from all other models.
	type claim struct {
		alias string
		owner string
	}
	var conflicts []claim

	aliasOwners := make(map[string]string) // alias → model name
	for i := range allModels.Items {
		m := &allModels.Items[i]
		if m.Name == model.Name || m.Spec.LiteLLM == nil {
			continue
		}
		if served := m.Spec.LiteLLM.ServedModelName; served != "" {
			aliasOwners[served] = m.Name
		}
		for _, alias := range m.Spec.LiteLLM.Aliases {
			aliasOwners[alias] = m.Name
		}
		if cop := m.Spec.LiteLLM.CopilotAlias; cop != "" {
			aliasOwners["copilotAlias:"+cop] = m.Name
		}
	}

	// Check this model's aliases against the map.
	if served := model.Spec.LiteLLM.ServedModelName; served != "" {
		if owner, ok := aliasOwners[served]; ok {
			conflicts = append(conflicts, claim{alias: served, owner: owner})
		}
	}
	for _, alias := range model.Spec.LiteLLM.Aliases {
		if owner, ok := aliasOwners[alias]; ok {
			conflicts = append(conflicts, claim{alias: alias, owner: owner})
		}
	}
	if cop := model.Spec.LiteLLM.CopilotAlias; cop != "" {
		if owner, ok := aliasOwners["copilotAlias:"+cop]; ok {
			conflicts = append(conflicts, claim{alias: "copilotAlias:" + cop, owner: owner})
		}
	}

	if len(conflicts) > 0 {
		msgs := make([]string, 0, len(conflicts))
		for _, c := range conflicts {
			msgs = append(msgs, fmt.Sprintf("%q conflicts with %s", c.alias, c.owner))
		}
		msg := "litellm alias conflicts (use serviceLabels for group-wide routing): " + strings.Join(msgs, "; ")
		setModelCondition(model, aiv1alpha2.ConditionConfigValid, false, aiv1alpha2.ReasonAliasConflict, msg)
		r.Recorder.Event(model, corev1.EventTypeWarning, aiv1alpha2.ReasonAliasConflict, msg)
		return
	}

	setModelCondition(model, aiv1alpha2.ConditionConfigValid, true, aiv1alpha2.ReasonConfigValid, "No alias conflicts detected")
}

func setModelCondition(model *aiv1alpha2.Model, conditionType string, status bool, reason, message string) {
	condStatus := metav1.ConditionFalse
	if status {
		condStatus = metav1.ConditionTrue
	}

	now := metav1.Now()
	newCond := metav1.Condition{
		Type:               conditionType,
		Status:             condStatus,
		Reason:             reason,
		Message:            message,
		LastTransitionTime: now,
		ObservedGeneration: model.Generation,
	}

	// Upsert in-place.
	for i := range model.Status.Conditions {
		if model.Status.Conditions[i].Type == conditionType {
			// Only bump transition time when status changes.
			if model.Status.Conditions[i].Status == condStatus &&
				model.Status.Conditions[i].Reason == reason &&
				model.Status.Conditions[i].Message == message &&
				model.Status.Conditions[i].ObservedGeneration == model.Generation {
				return
			}
			if model.Status.Conditions[i].Status == condStatus {
				newCond.LastTransitionTime = model.Status.Conditions[i].LastTransitionTime
			}
			model.Status.Conditions[i] = newCond
			return
		}
	}
	model.Status.Conditions = append(model.Status.Conditions, newCond)
}

func modelCondition(conds []metav1.Condition, condType string) *metav1.Condition {
	for i := range conds {
		if conds[i].Type == condType {
			return &conds[i]
		}
	}
	return nil
}

func isMlcModelSource(source string) bool {
	return strings.HasPrefix(source, "HF://mlc-ai/") || strings.Contains(source, "-MLC")
}

type hfDownloadOptions struct {
	allowPatterns  []string
	ignorePatterns []string
	revision       string
}

func configStringValue(cfg map[string]interface{}, keys ...string) string {
	for _, key := range keys {
		raw, ok := cfg[key]
		if !ok {
			continue
		}
		if s, ok := raw.(string); ok {
			s = strings.TrimSpace(s)
			if s != "" {
				return s
			}
		}
	}
	return ""
}

func configStringListValue(cfg map[string]interface{}, key string) []string {
	raw, ok := cfg[key]
	if !ok || raw == nil {
		return nil
	}

	out := make([]string, 0)
	appendItem := func(s string) {
		s = strings.TrimSpace(s)
		if s != "" {
			out = append(out, s)
		}
	}

	switch v := raw.(type) {
	case string:
		if strings.Contains(v, ",") {
			for _, item := range strings.Split(v, ",") {
				appendItem(item)
			}
		} else {
			appendItem(v)
		}
	case []string:
		for _, item := range v {
			appendItem(item)
		}
	case []interface{}:
		for _, item := range v {
			if s, ok := item.(string); ok {
				appendItem(s)
			}
		}
	}

	return out
}

func sanitizeHFPatterns(patterns []string) []string {
	seen := make(map[string]struct{}, len(patterns))
	out := make([]string, 0, len(patterns))
	for _, p := range patterns {
		p = strings.TrimSpace(p)
		if p == "" {
			continue
		}
		p = strings.TrimLeft(p, "/")
		if p == "" || strings.Contains(p, "..") {
			continue
		}
		if _, ok := seen[p]; ok {
			continue
		}
		seen[p] = struct{}{}
		out = append(out, p)
	}
	return out
}

func resolveHFDownloadOptions(model *aiv1alpha2.Model) hfDownloadOptions {
	cfg := model.Spec.GetConfigMap()
	opts := hfDownloadOptions{
		allowPatterns:  configStringListValue(cfg, "hfAllowPatterns"),
		ignorePatterns: configStringListValue(cfg, "hfIgnorePatterns"),
		revision:       configStringValue(cfg, "hfRevision"),
	}

	backendName := strings.ToLower(strings.TrimSpace(model.Spec.Backend))

	// Backends that load GGUF files: filter downloads to just the specified file
	// to avoid downloading all quantization variants from multi-GGUF repos.
	if backendName == "llamacpp" || backendName == "llama.cpp" || backendName == "vllm" {
		ggufFile := configStringValue(cfg, "ggufFile", "modelFile")
		if ggufFile != "" {
			opts.allowPatterns = append(opts.allowPatterns, ggufFile)
		}

		// mmproj is optional for multimodal models and can live in the same repo.
		mmproj := configStringValue(cfg, "mmproj")
		if mmproj != "" && !strings.HasPrefix(mmproj, "/") {
			opts.allowPatterns = append(opts.allowPatterns, mmproj)
		}
	}

	opts.allowPatterns = sanitizeHFPatterns(opts.allowPatterns)
	opts.ignorePatterns = sanitizeHFPatterns(opts.ignorePatterns)
	return opts
}

func (r *ModelReconciler) jobForPrefetch(model *aiv1alpha2.Model, pvcName, destSubdir string) (*batchv1.Job, error) {
	modelID := extractModelFromSource(model.Spec.Source)
	hfOpts := resolveHFDownloadOptions(model)

	envVars := []corev1.EnvVar{
		{Name: "HF_HUB_ENABLE_HF_TRANSFER", Value: "0"},
		// Keep HuggingFace cache on the mounted volume so backends can reuse downloads.
		{Name: "HF_HOME", Value: "/models/.cache/huggingface"},
		{Name: "HF_HUB_CACHE", Value: "/models/.cache/huggingface/hub"},
		{Name: "HUGGINGFACE_HUB_CACHE", Value: "/models/.cache/huggingface/hub"},
		{Name: "TRANSFORMERS_CACHE", Value: "/models/.cache/huggingface/transformers"},
	}
	if len(hfOpts.allowPatterns) > 0 {
		allowJSON, err := json.Marshal(hfOpts.allowPatterns)
		if err != nil {
			return nil, fmt.Errorf("marshal HF allow patterns: %w", err)
		}
		envVars = append(envVars, corev1.EnvVar{Name: "HF_ALLOW_PATTERNS", Value: string(allowJSON)})
	}
	if len(hfOpts.ignorePatterns) > 0 {
		ignoreJSON, err := json.Marshal(hfOpts.ignorePatterns)
		if err != nil {
			return nil, fmt.Errorf("marshal HF ignore patterns: %w", err)
		}
		envVars = append(envVars, corev1.EnvVar{Name: "HF_IGNORE_PATTERNS", Value: string(ignoreJSON)})
	}
	if hfOpts.revision != "" {
		envVars = append(envVars, corev1.EnvVar{Name: "HF_REVISION", Value: hfOpts.revision})
	}

	// If the model config specifies a VAE repo (e.g. madebyollin/sdxl-vae-fp16-fix),
	// pass it to the download script so the VAE is prefetched alongside the model.
	if vaeRepo := model.Spec.ConfigString("vaeRepo", ""); vaeRepo != "" {
		vaeDest := "/models/.vae/" + filepath.Base(vaeRepo)
		envVars = append(envVars,
			corev1.EnvVar{Name: "VAE_REPO", Value: vaeRepo},
			corev1.EnvVar{Name: "VAE_DEST_DIR", Value: vaeDest},
		)
	}

	destSubdir = strings.Trim(destSubdir, "/")
	destDir := "/models/" + destSubdir

	var nodeSelector map[string]string
	if len(model.Spec.NodeSelector) > 0 {
		nodeSelector = model.Spec.NodeSelector
	}
	var tolerations []corev1.Toleration
	if model.Spec.GetGPUCount() > 0 {
		tolerations = append(tolerations, corev1.Toleration{
			Key:      "dedicated",
			Operator: corev1.TolerationOpEqual,
			Value:    "gpu",
			Effect:   corev1.TaintEffectNoSchedule,
		})
	}

	var image string
	var downloadScript string

	if isMlcModelSource(model.Spec.Source) {
		image = "debian:bookworm-slim"
		downloadScript = fmt.Sprintf(`
set -ex
MODEL_ID="%s"
DEST_DIR="%s"
MARKER="$DEST_DIR/.flexinfer_cached"

if [ -f "$MARKER" ]; then
    echo "Model already cached at $DEST_DIR"
    exit 0
fi

apt-get update && apt-get install -y git git-lfs ca-certificates
git lfs install

mkdir -p "$DEST_DIR"
echo "Cloning $MODEL_ID to $DEST_DIR..."
GIT_LFS_SKIP_SMUDGE=0 git clone "%s/$MODEL_ID" "$DEST_DIR"
touch "$MARKER"
echo "Download complete."
`, modelID, destDir, huggingFaceRepositoryBaseURL)
	} else {
		image = "python:3.10-slim"
		downloadScript = fmt.Sprintf(`
set -ex
MODEL_ID="%s"
DEST_DIR="%s"
MARKER="$DEST_DIR/.flexinfer_cached"

if [ -f "$MARKER" ]; then
    echo "Model already cached at $DEST_DIR"
    exit 0
fi

pip install --no-cache-dir huggingface_hub
mkdir -p "$DEST_DIR"
MODEL_ID="$MODEL_ID" DEST_DIR="$DEST_DIR" python - <<'PY'
import json
import os

from huggingface_hub import snapshot_download

repo_id = os.environ["MODEL_ID"]
local_dir = os.environ["DEST_DIR"]
token = os.environ.get("HF_TOKEN") or os.environ.get("HUGGINGFACE_HUB_TOKEN")
cache_dir = os.environ.get("HF_HOME")
allow_patterns = json.loads(os.environ.get("HF_ALLOW_PATTERNS", "[]") or "[]")
ignore_patterns = json.loads(os.environ.get("HF_IGNORE_PATTERNS", "[]") or "[]")
revision = (os.environ.get("HF_REVISION") or "").strip() or None

download_kwargs = {
    "repo_id": repo_id,
    "local_dir": local_dir,
    "local_dir_use_symlinks": False,
    "cache_dir": cache_dir,
    "token": token,
}
if allow_patterns:
    download_kwargs["allow_patterns"] = allow_patterns
if ignore_patterns:
    download_kwargs["ignore_patterns"] = ignore_patterns
if revision:
    download_kwargs["revision"] = revision

snapshot_download(**download_kwargs)

# Download additional VAE repo if configured (e.g. madebyollin/sdxl-vae-fp16-fix).
vae_repo = os.environ.get("VAE_REPO", "").strip()
if vae_repo:
    vae_dir = os.environ.get("VAE_DEST_DIR", "")
    print(f"Downloading VAE: {vae_repo} -> {vae_dir}")
    snapshot_download(repo_id=vae_repo, local_dir=vae_dir, local_dir_use_symlinks=False, cache_dir=cache_dir, token=token)
PY
touch "$MARKER"
echo "Download complete."
`, modelID, destDir)
	}

	job := &batchv1.Job{
		ObjectMeta: metav1.ObjectMeta{
			Name:      model.Name + "-cache-prefetch",
			Namespace: model.Namespace,
			Labels:    r.labelsForModel(model),
			Annotations: map[string]string{
				"flexinfer.ai/source":     model.Spec.Source,
				"flexinfer.ai/cache-kind": "prefetch",
				"flexinfer.ai/cache-pvc":  pvcName,
				"flexinfer.ai/cache-dest": destSubdir,
			},
		},
		Spec: batchv1.JobSpec{
			BackoffLimit: ptr.To(int32(3)),
			Template: corev1.PodTemplateSpec{
				Spec: corev1.PodSpec{
					RestartPolicy:                corev1.RestartPolicyOnFailure,
					NodeSelector:                 nodeSelector,
					Tolerations:                  tolerations,
					AutomountServiceAccountToken: ptr.To(false),
					Containers: []corev1.Container{{
						Name:    "downloader",
						Image:   image,
						Command: []string{"/bin/sh", "-c"},
						Args:    []string{downloadScript},
						VolumeMounts: []corev1.VolumeMount{{
							Name:      "model-store",
							MountPath: "/models",
						}},
						Env: envVars,
						Resources: corev1.ResourceRequirements{
							Requests: corev1.ResourceList{
								corev1.ResourceCPU:    resource.MustParse("100m"),
								corev1.ResourceMemory: resource.MustParse("1Gi"),
							},
							Limits: corev1.ResourceList{
								corev1.ResourceMemory: resource.MustParse("8Gi"),
							},
						},
					}},
					Volumes: []corev1.Volume{{
						Name: "model-store",
						VolumeSource: corev1.VolumeSource{
							PersistentVolumeClaim: &corev1.PersistentVolumeClaimVolumeSource{
								ClaimName: pvcName,
							},
						},
					}},
				},
			},
		},
	}

	if err := ctrl.SetControllerReference(model, job, r.Scheme); err != nil {
		return nil, err
	}
	return job, nil
}

func (r *ModelReconciler) jobForCacheCheck(model *aiv1alpha2.Model, pvcName, subPath string) (*batchv1.Job, error) {
	subPath = strings.Trim(subPath, "/")
	target := "/models"
	if subPath != "" {
		target = "/models/" + subPath
	}

	var nodeSelector map[string]string
	if len(model.Spec.NodeSelector) > 0 {
		nodeSelector = model.Spec.NodeSelector
	}
	var tolerations []corev1.Toleration
	if model.Spec.GetGPUCount() > 0 {
		tolerations = append(tolerations, corev1.Toleration{
			Key:      "dedicated",
			Operator: corev1.TolerationOpEqual,
			Value:    "gpu",
			Effect:   corev1.TaintEffectNoSchedule,
		})
	}

	script := fmt.Sprintf(`
set -ex
TARGET="%s"
if [ ! -e "$TARGET" ]; then
  echo "Missing path: $TARGET"
  exit 1
fi
if [ -d "$TARGET" ]; then
  if [ -z "$(ls -A "$TARGET" 2>/dev/null)" ]; then
    echo "Directory is empty: $TARGET"
    exit 1
  fi
  echo "Artifact present at directory $TARGET"
  exit 0
fi
if [ ! -s "$TARGET" ]; then
  echo "File is empty: $TARGET"
  exit 1
fi
echo "Artifact present at file $TARGET"
`, target)

	job := &batchv1.Job{
		ObjectMeta: metav1.ObjectMeta{
			Name:      model.Name + "-cache-check",
			Namespace: model.Namespace,
			Labels:    r.labelsForModel(model),
			Annotations: map[string]string{
				"flexinfer.ai/source":     model.Spec.Source,
				"flexinfer.ai/cache-kind": "check",
				"flexinfer.ai/cache-pvc":  pvcName,
				"flexinfer.ai/cache-path": subPath,
			},
		},
		Spec: batchv1.JobSpec{
			BackoffLimit: ptr.To(int32(0)),
			Template: corev1.PodTemplateSpec{
				Spec: corev1.PodSpec{
					RestartPolicy:                corev1.RestartPolicyNever,
					NodeSelector:                 nodeSelector,
					Tolerations:                  tolerations,
					AutomountServiceAccountToken: ptr.To(false),
					Containers: []corev1.Container{{
						Name:    "checker",
						Image:   "alpine:3.19",
						Command: []string{"/bin/sh", "-c"},
						Args:    []string{script},
						VolumeMounts: []corev1.VolumeMount{{
							Name:      "model-store",
							MountPath: "/models",
						}},
						Resources: corev1.ResourceRequirements{
							Requests: corev1.ResourceList{
								corev1.ResourceCPU:    resource.MustParse("10m"),
								corev1.ResourceMemory: resource.MustParse("32Mi"),
							},
							Limits: corev1.ResourceList{
								corev1.ResourceMemory: resource.MustParse("128Mi"),
							},
						},
					}},
					Volumes: []corev1.Volume{{
						Name: "model-store",
						VolumeSource: corev1.VolumeSource{
							PersistentVolumeClaim: &corev1.PersistentVolumeClaimVolumeSource{
								ClaimName: pvcName,
							},
						},
					}},
				},
			},
		},
	}

	if err := ctrl.SetControllerReference(model, job, r.Scheme); err != nil {
		return nil, err
	}
	return job, nil
}

func (r *ModelReconciler) jobForCacheCopy(model *aiv1alpha2.Model, sourcePVCName, cachePVCName, subPath string) (*batchv1.Job, error) {
	subPath = strings.Trim(subPath, "/")
	src := "/src"
	dst := "/models"
	if subPath != "" {
		src = "/src/" + subPath
		dst = "/models/" + subPath
	}

	var nodeSelector map[string]string
	if len(model.Spec.NodeSelector) > 0 {
		nodeSelector = model.Spec.NodeSelector
	}
	var tolerations []corev1.Toleration
	if model.Spec.GetGPUCount() > 0 {
		tolerations = append(tolerations, corev1.Toleration{
			Key:      "dedicated",
			Operator: corev1.TolerationOpEqual,
			Value:    "gpu",
			Effect:   corev1.TaintEffectNoSchedule,
		})
	}

	sum := sha256.Sum256([]byte(model.Spec.Source))
	marker := "/models/.flexinfer_cached_" + hex.EncodeToString(sum[:])

	script := fmt.Sprintf(`
set -ex
SRC="%s"
DST="%s"
MARKER="%s"

if [ -f "$MARKER" ]; then
  echo "Already cached: $MARKER"
  exit 0
fi

if [ ! -e "$SRC" ]; then
  echo "Missing source path: $SRC"
  exit 1
fi

if [ -d "$SRC" ]; then
  mkdir -p "$DST"
  cp -a "$SRC/." "$DST/"
else
  mkdir -p "$(dirname "$DST")"
  cp -a "$SRC" "$DST"
fi

touch "$MARKER"
echo "Copy complete."
`, src, dst, marker)

	job := &batchv1.Job{
		ObjectMeta: metav1.ObjectMeta{
			Name:      model.Name + "-cache-copy",
			Namespace: model.Namespace,
			Labels:    r.labelsForModel(model),
			Annotations: map[string]string{
				"flexinfer.ai/source":        model.Spec.Source,
				"flexinfer.ai/cache-kind":    "copy",
				"flexinfer.ai/cache-src-pvc": sourcePVCName,
				"flexinfer.ai/cache-pvc":     cachePVCName,
				"flexinfer.ai/cache-path":    subPath,
			},
		},
		Spec: batchv1.JobSpec{
			BackoffLimit: ptr.To(int32(1)),
			Template: corev1.PodTemplateSpec{
				Spec: corev1.PodSpec{
					RestartPolicy:                corev1.RestartPolicyOnFailure,
					NodeSelector:                 nodeSelector,
					Tolerations:                  tolerations,
					AutomountServiceAccountToken: ptr.To(false),
					Containers: []corev1.Container{{
						Name:    "copier",
						Image:   "alpine:3.20",
						Command: []string{"/bin/sh", "-c"},
						Args:    []string{script},
						VolumeMounts: []corev1.VolumeMount{
							{
								Name:      "source",
								MountPath: "/src",
								ReadOnly:  true,
							},
							{
								Name:      "model-store",
								MountPath: "/models",
							},
						},
						Resources: corev1.ResourceRequirements{
							Requests: corev1.ResourceList{
								corev1.ResourceCPU:    resource.MustParse("100m"),
								corev1.ResourceMemory: resource.MustParse("256Mi"),
							},
							Limits: corev1.ResourceList{
								corev1.ResourceMemory: resource.MustParse("2Gi"),
							},
						},
					}},
					Volumes: []corev1.Volume{
						{
							Name: "source",
							VolumeSource: corev1.VolumeSource{
								PersistentVolumeClaim: &corev1.PersistentVolumeClaimVolumeSource{
									ClaimName: sourcePVCName,
								},
							},
						},
						{
							Name: "model-store",
							VolumeSource: corev1.VolumeSource{
								PersistentVolumeClaim: &corev1.PersistentVolumeClaimVolumeSource{
									ClaimName: cachePVCName,
								},
							},
						},
					},
				},
			},
		},
	}

	if err := ctrl.SetControllerReference(model, job, r.Scheme); err != nil {
		return nil, err
	}
	return job, nil
}
