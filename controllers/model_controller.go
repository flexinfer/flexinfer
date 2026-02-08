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
	stderrors "errors"
	"fmt"
	"sort"
	"strings"
	"time"

	appsv1 "k8s.io/api/apps/v1"
	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
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

	aiv1alpha2 "github.com/flexinfer/flexinfer/api/v1alpha2"
	"github.com/flexinfer/flexinfer/backend"
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

var managedModelAnnotations = []string{
	"litellm.flexinfer.ai/served-model",
	"litellm.flexinfer.ai/aliases",
	"litellm.flexinfer.ai/copilot-model",
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
	log := log.FromContext(ctx)

	// Fetch the Model instance
	model := &aiv1alpha2.Model{}
	err := r.Get(ctx, req.NamespacedName, model)
	if err != nil {
		if errors.IsNotFound(err) {
			log.Info("Model resource not found, ignoring since object must be deleted")
			return ctrl.Result{}, nil
		}
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
		err := fmt.Errorf("unknown backend: %s", model.Spec.Backend)
		log.Error(err, "Backend validation failed")
		r.Recorder.Event(model, corev1.EventTypeWarning, "ValidationFailed", err.Error())
		return ctrl.Result{}, r.updatePhase(ctx, model, aiv1alpha2.ModelPhaseFailed)
	}

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

	return ctrl.Result{RequeueAfter: requeueAfter}, nil
}

// validateBackendGPUCompatibility checks if the backend is compatible with the target GPU arch.
// This is primarily used for Maxwell (sm_5x), where several backends assume newer SMs.
func (r *ModelReconciler) validateBackendGPUCompatibility(model *aiv1alpha2.Model, b backend.Backend, gpuVendor backend.GPUVendor, gpuArch string) error {
	if !isMaxwellGPUArch(gpuArch) {
		return nil
	}
	if gpuVendor != backend.GPUVendorNVIDIA {
		return nil
	}

	switch b.Name() {
	case "vllm", "vllm-omni", "diffusers":
		return fmt.Errorf("%s backend is not supported on Maxwell GPUs (compute capability 5.x). Use ollama, mlc-llm (pre-compiled), or llamacpp instead", b.Name())
	case "mlc-llm":
		// MLC-LLM on Maxwell should use a pre-compiled model library and avoid JIT.
		// Prefer requiring an explicit modelLibPath unless we can infer a conventional
		// on-disk location under /models/<modelName>.
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
			// Backend will default to <modelPath>/maxwell-lib.so.
			return nil
		}

		return fmt.Errorf("mlc-llm on Maxwell GPUs requires config.modelLibPath (pre-compiled library). See docs/user/backends-maxwell.md")
	default:
		return nil
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

	// Get container configuration from backend
	image := b.Image(gpuVendor, gpuArch)
	port := b.Port()
	command := b.Command()
	args := b.Args(spec)
	env := b.Env(spec)
	probe := b.ReadinessProbe()

	// If this is a HuggingFace source and we have a /models volume, store HF caches on it.
	// This makes SharedPVC act as a real cache layer without adding a dedicated downloader job.
	if strings.HasPrefix(model.Spec.Source, "HF://") && b.NeedsVolume() {
		env = mergeEnv(env, hfCacheEnvVars("/models/.cache/huggingface"))
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
	}

	// Add volume mounts if backend needs volume
	var volumes []corev1.Volume
	if b.NeedsVolume() {
		// Add model volume mount
		volumeMount := corev1.VolumeMount{
			Name:      "model",
			MountPath: "/models",
		}
		// For diffusers + HF SharedPVC, prefetch materializes a full diffusers repo under /models/<modelName>.
		// Mount that directory as /models so the server finds model_index.json at the expected root.
		if b.Name() == "diffusers" && strings.HasPrefix(model.Spec.Source, "HF://") && cacheStrategy(model) == "SharedPVC" {
			volumeMount.SubPath = model.Name
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
	container.VolumeMounts = append(container.VolumeMounts, corev1.VolumeMount{
		Name:      "shm",
		MountPath: "/dev/shm",
	})
	volumes = append(volumes, corev1.Volume{
		Name: "shm",
		VolumeSource: corev1.VolumeSource{
			EmptyDir: &corev1.EmptyDirVolumeSource{
				Medium:    corev1.StorageMediumMemory,
				SizeLimit: resource.NewQuantity(8*1024*1024*1024, resource.BinarySI), // 8Gi
			},
		},
	})

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
							// GID 992 is the render group on most ROCm hosts
							SupplementalGroups: []int64{992},
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
					Containers: []corev1.Container{container},
					Volumes:    volumes,
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

	// Update deployment
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
	return r.Update(ctx, deployment)
}

// buildBackendModelSpec converts Model spec to backend.ModelSpec.
func (r *ModelReconciler) buildBackendModelSpec(model *aiv1alpha2.Model, b backend.Backend, gpuVendor backend.GPUVendor) *backend.ModelSpec {
	modelValue := extractModelFromSource(model.Spec.Source)
	spec := &backend.ModelSpec{
		Model:     modelValue,
		ModelPath: "",
		GPUVendor: gpuVendor,
	}

	// If we're using a SharedPVC cache with an HF source, point backends at a local artifact directory
	// populated by the prefetch job. This keeps model startup deterministic and avoids pulling weights
	// during container startup.
	if strings.HasPrefix(model.Spec.Source, "HF://") && cacheStrategy(model) == "SharedPVC" && model.Status.Cache != nil {
		if model.Status.Cache.PVCName != "" {
			spec.ModelPath = "/models/" + model.Name
		}
	}

	// If the source points at a PVC path, construct an absolute path under /models.
	// Example: pvc://my-pvc/subdir -> /models/subdir
	if strings.HasPrefix(model.Spec.Source, "pvc://") {
		if strings.HasPrefix(modelValue, "/") {
			spec.ModelPath = "/models" + modelValue
		} else {
			spec.ModelPath = "/models"
		}
	}

	// If the source is a file path, treat it as an in-container absolute path.
	// Example: file:///models/model.gguf -> /models/model.gguf
	if strings.HasPrefix(model.Spec.Source, "file://") {
		spec.ModelPath = modelValue
	}

	// Parse config into the spec
	if model.Spec.Config != nil {
		spec.Config = model.Spec.GetConfigMap()
	}

	// llama.cpp needs an actual GGUF file path. For HF sources staged into /models/<modelName>,
	// allow selecting the file via spec.config.ggufFile (or legacy modelFile).
	if b != nil && b.Name() == "llamacpp" && strings.HasPrefix(model.Spec.Source, "HF://") && cacheStrategy(model) == "SharedPVC" && model.Status.Cache != nil {
		if model.Status.Cache.PVCName != "" {
			ggufFile := ""
			if spec.Config != nil {
				if v, ok := spec.Config["ggufFile"]; ok {
					if s, ok := v.(string); ok {
						ggufFile = s
					}
				}
				if strings.TrimSpace(ggufFile) == "" {
					if v, ok := spec.Config["modelFile"]; ok {
						if s, ok := v.(string); ok {
							ggufFile = s
						}
					}
				}
			}

			ggufFile = strings.TrimLeft(strings.TrimSpace(ggufFile), "/")
			// Best-effort safety: ignore traversal attempts.
			if ggufFile != "" && !strings.Contains(ggufFile, "..") {
				spec.ModelPath = "/models/" + model.Name + "/" + ggufFile
			}
		}
	}

	return spec
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
						Resources: corev1.ResourceRequirements{
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
				Resources: corev1.ResourceRequirements{
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
			model.Status.Cache.Ready = true
			model.Status.Cache.JobPhase = "Succeeded"
			model.Status.Cache.Message = "artifact prefetched"
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

// handleSharedGPU implements GPU sharing logic for models with gpu.shared set.
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

	// Find the active model (highest priority that's running or has pending requests)
	var activeModel *aiv1alpha2.Model
	highestPriority := int32(-1)

	for _, m := range groupModels {
		priority := m.Spec.GetPriority()

		// Check if this model should be active
		// Priority: Running models > Models with recent activity > Highest priority
		if m.Status.Phase == aiv1alpha2.ModelPhaseReady {
			if activeModel == nil || priority > highestPriority {
				activeModel = m
				highestPriority = priority
			}
		} else if m.Status.LastActiveTime != nil {
			// Model had recent activity (request came in while idle)
			timeSinceActive := time.Since(m.Status.LastActiveTime.Time)
			if timeSinceActive < 5*time.Minute && priority > highestPriority {
				activeModel = m
				highestPriority = priority
			}
		}
	}

	// If no model is active, this model can become active
	if activeModel == nil {
		activeModel = model
	}

	// Update this model's shared group status
	if model.Status.SharedGroup == nil {
		model.Status.SharedGroup = &aiv1alpha2.SharedGroupStatus{}
	}
	model.Status.SharedGroup.GroupName = groupName

	if activeModel.Name == model.Name {
		// This model should be active
		model.Status.SharedGroup.State = "Active"
		model.Status.SharedGroup.QueuePosition = 0
		log.Info("Model is active in shared group", "group", groupName)
	} else {
		// This model should be preempted/queued
		model.Status.SharedGroup.State = "Queued"
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

	if err := r.Status().Update(ctx, model); err != nil {
		return ctrl.Result{}, err
	}

	return ctrl.Result{RequeueAfter: 10 * time.Second}, nil
}

// updateStatusFromDeployment updates the Model status based on the deployment state.
func (r *ModelReconciler) updateStatusFromDeployment(ctx context.Context, model *aiv1alpha2.Model) error {
	deployment := &appsv1.Deployment{}
	if err := r.Get(ctx, types.NamespacedName{Name: model.Name, Namespace: model.Namespace}, deployment); err != nil {
		if errors.IsNotFound(err) {
			return nil
		}
		return err
	}

	// Update endpoint
	b, _ := backend.Get(model.Spec.Backend)
	port := b.Port()
	model.Status.Endpoint = fmt.Sprintf("http://%s.%s.svc:%d", model.Name, model.Namespace, port)

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

	findFirst := func(vendor backend.GPUVendor) (nodeMatch, bool) {
		for _, node := range nodes {
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

func isMlcModelSource(source string) bool {
	return strings.HasPrefix(source, "HF://mlc-ai/") || strings.Contains(source, "-MLC")
}

func (r *ModelReconciler) jobForPrefetch(model *aiv1alpha2.Model, pvcName, destSubdir string) (*batchv1.Job, error) {
	modelID := extractModelFromSource(model.Spec.Source)

	envVars := []corev1.EnvVar{
		{Name: "HF_HUB_ENABLE_HF_TRANSFER", Value: "0"},
		// Keep HuggingFace cache on the mounted volume so backends can reuse downloads.
		{Name: "HF_HOME", Value: "/models/.cache/huggingface"},
		{Name: "HF_HUB_CACHE", Value: "/models/.cache/huggingface/hub"},
		{Name: "HUGGINGFACE_HUB_CACHE", Value: "/models/.cache/huggingface/hub"},
		{Name: "TRANSFORMERS_CACHE", Value: "/models/.cache/huggingface/transformers"},
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
GIT_LFS_SKIP_SMUDGE=0 git clone "https://huggingface.co/$MODEL_ID" "$DEST_DIR"
touch "$MARKER"
echo "Download complete."
`, modelID, destDir)
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
import os

from huggingface_hub import snapshot_download

repo_id = os.environ["MODEL_ID"]
local_dir = os.environ["DEST_DIR"]
token = os.environ.get("HF_TOKEN") or os.environ.get("HUGGINGFACE_HUB_TOKEN")
cache_dir = os.environ.get("HF_HOME")

snapshot_download(
    repo_id=repo_id,
    local_dir=local_dir,
    local_dir_use_symlinks=False,
    cache_dir=cache_dir,
    token=token,
)
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
