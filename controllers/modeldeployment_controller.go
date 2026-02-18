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
	"os"
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
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/intstr"
	"k8s.io/client-go/tools/record"
	"k8s.io/client-go/util/retry"
	"k8s.io/utils/ptr"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	aiv1alpha1 "github.com/flexinfer/flexinfer/api/v1alpha1"
	"github.com/flexinfer/flexinfer/pkg/benchmarkconfig"
	"github.com/flexinfer/flexinfer/pkg/k8surl"
)

func canonicalBackend(backend string) string {
	trimmed := strings.TrimSpace(backend)
	if trimmed == "" {
		return "ollama"
	}

	switch strings.ToLower(trimmed) {
	case "llama.cpp":
		return "llamacpp"
	case "mlc":
		return "mlc-llm"
	// Image generation backends
	case "comfy", "comfyui":
		return "comfyui"
	case "vllm-omni", "vllm-diffusion":
		return "vllm-omni"
	case "diffusers", "diffusers-api":
		return "diffusers"
	default:
		return backend
	}
}

// isImageGenBackend returns true if the backend is for image generation
func isImageGenBackend(backend string) bool {
	switch canonicalBackend(backend) {
	case "comfyui", "vllm-omni", "diffusers":
		return true
	default:
		return false
	}
}

func benchmarkServiceAccountName() string {
	return strings.TrimSpace(os.Getenv("BENCHMARK_SERVICE_ACCOUNT"))
}

// LiteLLM annotation constants
const (
	liteLLMAnnotationServedModel = "litellm.flexinfer.ai/served-model"
	liteLLMAnnotationAliases     = "litellm.flexinfer.ai/aliases"
	liteLLMAnnotationCopilot     = "litellm.flexinfer.ai/copilot-model"
)

// getLiteLLMAnnotations builds the LiteLLM discovery annotations for a ModelDeployment.
// Returns nil if LiteLLM integration is disabled.
func getLiteLLMAnnotations(m *aiv1alpha1.ModelDeployment) map[string]string {
	// LiteLLM integration is opt-in: only apply when spec.litellm is provided.
	// (The CRD default only applies once the object exists.)
	if m.Spec.LiteLLM == nil {
		return nil
	}
	if m.Spec.LiteLLM.Enabled != nil && !*m.Spec.LiteLLM.Enabled {
		return nil
	}

	annotations := make(map[string]string)

	// Determine the served model name
	servedModel := m.Name
	if m.Spec.LiteLLM != nil && m.Spec.LiteLLM.ServedModelName != "" {
		servedModel = m.Spec.LiteLLM.ServedModelName
	}
	annotations[liteLLMAnnotationServedModel] = servedModel

	// Add aliases if specified
	if m.Spec.LiteLLM != nil && len(m.Spec.LiteLLM.Aliases) > 0 {
		annotations[liteLLMAnnotationAliases] = strings.Join(m.Spec.LiteLLM.Aliases, ",")
	}

	// Add copilot alias if specified
	if m.Spec.LiteLLM != nil && m.Spec.LiteLLM.CopilotAlias != "" {
		annotations[liteLLMAnnotationCopilot] = m.Spec.LiteLLM.CopilotAlias
	}

	return annotations
}

// ModelDeploymentReconciler reconciles a ModelDeployment object
type ModelDeploymentReconciler struct {
	client.Client
	Scheme    *runtime.Scheme
	Recorder  record.EventRecorder
	APIReader client.Reader // Uncached reader for critical sections
}

//+kubebuilder:rbac:groups=ai.flexinfer,resources=modeldeployments,verbs=get;list;watch;create;update;patch;delete
//+kubebuilder:rbac:groups=ai.flexinfer,resources=modeldeployments/status,verbs=get;update;patch
//+kubebuilder:rbac:groups=ai.flexinfer,resources=modeldeployments/finalizers,verbs=update
//+kubebuilder:rbac:groups=apps,resources=deployments,verbs=get;list;watch;create;update;patch;delete
//+kubebuilder:rbac:groups=core,resources=persistentvolumeclaims,verbs=get;list;watch;create;update;patch;delete
//+kubebuilder:rbac:groups=core,resources=services,verbs=get;list;watch;create;update;patch;delete
//+kubebuilder:rbac:groups=batch,resources=jobs,verbs=get;list;watch;create;update;patch;delete
//+kubebuilder:rbac:groups=core,resources=configmaps,verbs=get;list;watch;create;update;patch;delete
//+kubebuilder:rbac:groups="",resources=nodes,verbs=get;list;watch

// Reconcile is part of the main kubernetes reconciliation loop which aims to
// move the current state of the cluster closer to the desired state.
func (r *ModelDeploymentReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	log := log.FromContext(ctx)

	// Fetch the ModelDeployment instance
	modelDeployment := &aiv1alpha1.ModelDeployment{}
	err := r.Get(ctx, req.NamespacedName, modelDeployment)
	if err != nil {
		if errors.IsNotFound(err) {
			log.Info("ModelDeployment resource not found. Ignoring since object must be deleted")
			return ctrl.Result{}, nil
		}
		log.Error(err, "Failed to get ModelDeployment")
		return ctrl.Result{}, err
	}

	// Handle finalizer logic for cleanup
	if modelDeployment.DeletionTimestamp.IsZero() {
		// Object is not being deleted, ensure finalizer is present
		if !containsString(modelDeployment.GetFinalizers(), aiv1alpha1.ModelDeploymentFinalizer) {
			modelDeployment.SetFinalizers(append(modelDeployment.GetFinalizers(), aiv1alpha1.ModelDeploymentFinalizer))
			if err := r.Update(ctx, modelDeployment); err != nil {
				log.Error(err, "Failed to add finalizer")
				return ctrl.Result{}, err
			}
			return ctrl.Result{Requeue: true}, nil
		}
	} else {
		// Object is being deleted
		if containsString(modelDeployment.GetFinalizers(), aiv1alpha1.ModelDeploymentFinalizer) {
			// Perform cleanup operations
			if err := r.cleanupModelDeployment(ctx, modelDeployment); err != nil {
				log.Error(err, "Failed to cleanup ModelDeployment resources")
				return ctrl.Result{}, err
			}

			// Remove finalizer to allow deletion
			modelDeployment.SetFinalizers(removeString(modelDeployment.GetFinalizers(), aiv1alpha1.ModelDeploymentFinalizer))
			if err := r.Update(ctx, modelDeployment); err != nil {
				log.Error(err, "Failed to remove finalizer")
				return ctrl.Result{}, err
			}
		}
		return ctrl.Result{}, nil
	}

	// Initialize status only on first reconcile (when phase is empty)
	// This prevents resetting phase to Pending on every reconciliation
	if modelDeployment.Status.Phase == "" {
		if err := r.updateModelDeploymentStatus(ctx, modelDeployment, aiv1alpha1.ModelDeploymentPhasePending, "Initializing ModelDeployment"); err != nil {
			log.Error(err, "Failed to update initial status")
			return ctrl.Result{}, err
		}
	}

	// Record event for reconciliation start
	r.Recorder.Event(modelDeployment, corev1.EventTypeNormal, aiv1alpha1.ReasonReconciling, "Starting ModelDeployment reconciliation")

	// Validate GPU resource requirements
	if err := r.validateGPUResources(modelDeployment); err != nil {
		log.Error(err, "GPU resource validation failed")
		r.Recorder.Event(modelDeployment, corev1.EventTypeWarning, aiv1alpha1.ReasonValidationFailed, fmt.Sprintf("GPU validation failed: %v", err))
		if err := r.updateCondition(ctx, modelDeployment, aiv1alpha1.ConditionTypeReady, metav1.ConditionFalse, aiv1alpha1.ReasonValidationFailed, err.Error()); err != nil {
			log.Error(err, "Failed to update condition")
		}
		return ctrl.Result{}, err
	}

	// Validate backend + GPU compatibility (e.g., vLLM on Maxwell is not supported)
	if err := r.validateBackendGPUCompatibility(modelDeployment); err != nil {
		log.Error(err, "Backend-GPU compatibility validation failed")
		r.Recorder.Event(modelDeployment, corev1.EventTypeWarning, aiv1alpha1.ReasonValidationFailed, err.Error())
		if err := r.updateCondition(ctx, modelDeployment, aiv1alpha1.ConditionTypeReady, metav1.ConditionFalse, aiv1alpha1.ReasonValidationFailed, err.Error()); err != nil {
			log.Error(err, "Failed to update condition")
		}
		// Set phase to Failed so it's visible in dashboard
		if err := r.updateModelDeploymentStatus(ctx, modelDeployment, aiv1alpha1.ModelDeploymentPhaseFailed, err.Error()); err != nil {
			log.Error(err, "Failed to update status to Failed")
		}
		return ctrl.Result{}, nil // Don't requeue - user needs to fix the spec
	}

	// Validate vLLM-specific configuration compatibility (when backend is vllm)
	if canonicalBackend(modelDeployment.Spec.Backend) == "vllm" {
		if err := r.validateVLLMSpecCompatibility(modelDeployment); err != nil {
			log.Error(err, "vLLM configuration validation failed")
			r.Recorder.Event(modelDeployment, corev1.EventTypeWarning, aiv1alpha1.ReasonValidationFailed,
				fmt.Sprintf("vLLM configuration validation failed: %v", err))
			if err := r.updateCondition(ctx, modelDeployment, aiv1alpha1.ConditionTypeReady, metav1.ConditionFalse, aiv1alpha1.ReasonValidationFailed, err.Error()); err != nil {
				log.Error(err, "Failed to update condition")
			}
			if err := r.updateModelDeploymentStatus(ctx, modelDeployment, aiv1alpha1.ModelDeploymentPhaseFailed, err.Error()); err != nil {
				log.Error(err, "Failed to update status to Failed")
			}
			return ctrl.Result{}, nil // Don't requeue - user needs to fix the spec
		}
	}

	// Update condition for GPU validation success
	if err := r.updateCondition(ctx, modelDeployment, aiv1alpha1.ConditionTypeGPUAllocated, metav1.ConditionTrue, aiv1alpha1.ReasonGPUAllocated, "GPU resources validated and will be allocated"); err != nil {
		log.Error(err, "Failed to update GPU condition")
		return ctrl.Result{}, err
	}

	// Determine Volume Name early - needed for both benchmark and deployment
	volumeName := modelDeployment.Name
	volumeReadOnly := false
	volumePath := "" // For passing to benchmark job (format: "pvcName:subPath")

	// Backends that download models on-the-fly don't need storage volumes
	backendForVolume := canonicalBackend(modelDeployment.Spec.Backend)
	if backendForVolume == "diffusers" || backendForVolume == "comfyui" || backendForVolume == "tei" {
		volumeName = "" // No volume needed - models downloaded from HuggingFace/web
	}

	if modelDeployment.Spec.ModelCacheRef != nil {
		// Using ModelCache - check it early before benchmark
		cacheName := *modelDeployment.Spec.ModelCacheRef
		modelCache := &aiv1alpha1.ModelCache{}
		if err := r.Get(ctx, types.NamespacedName{Name: cacheName, Namespace: modelDeployment.Namespace}, modelCache); err != nil {
			log.Error(err, "Failed to get ModelCache", "ModelCache", cacheName)
			return ctrl.Result{}, err
		}

		if modelCache.Status.Phase != aiv1alpha1.ModelCachePhaseReady {
			log.Info("ModelCache not ready", "ModelCache", cacheName, "Phase", modelCache.Status.Phase)
			return ctrl.Result{RequeueAfter: 10 * time.Second}, nil
		}

		volumeName = modelCache.Status.Path
		volumePath = modelCache.Status.Path // Pass to benchmark job
		volumeReadOnly = true
		// vLLM with HuggingFace caching needs writable volume for model download
		// When HF_HOME points to the cache, vLLM must be able to write downloaded models
		if canonicalBackend(modelDeployment.Spec.Backend) == "vllm" {
			volumeReadOnly = false
		}
	}

	// Check if a benchmark has been run
	benchmarkCM := &corev1.ConfigMap{}
	err = r.Get(ctx, types.NamespacedName{Name: r.benchmarkConfigMapName(modelDeployment), Namespace: modelDeployment.Namespace}, benchmarkCM)
	if err != nil && errors.IsNotFound(err) {
		// If the ConfigMap is not found, it means we need to run a benchmark.
		// Check if a benchmark job is already running
		benchmarkJob := &batchv1.Job{}
		err = r.Get(ctx, types.NamespacedName{Name: r.benchmarkJobName(modelDeployment), Namespace: modelDeployment.Namespace}, benchmarkJob)
		if err != nil && errors.IsNotFound(err) {
			// If the Job is not found, create it (pass volumePath for cached models)
			job, buildErr := r.jobForBenchmark(modelDeployment, volumePath)
			if buildErr != nil {
				log.Error(buildErr, "Failed to build Benchmark Job")
				return ctrl.Result{}, buildErr
			}
			log.Info("Creating a new Benchmark Job", "Job.Namespace", job.Namespace, "Job.Name", job.Name)
			if err = r.Create(ctx, job); err != nil {
				log.Error(err, "Failed to create new Benchmark Job", "Job.Namespace", job.Namespace, "Job.Name", job.Name)
				return ctrl.Result{}, err
			}
			return ctrl.Result{Requeue: true}, nil
		} else if err != nil {
			log.Error(err, "Failed to get Benchmark Job")
			return ctrl.Result{}, err
		}

		// If the job is found, check its status
		if benchmarkJob.Status.Succeeded > 0 {
			log.Info("Benchmark job completed successfully, requeuing to read results")
			// Requeue immediately to read the ConfigMap that the job should have created
			return ctrl.Result{Requeue: true}, nil
		} else {
			// If the job is still running, requeue the request.
			log.Info("Benchmark job is still running")
			return ctrl.Result{RequeueAfter: 30 * time.Second}, nil
		}
	} else if err != nil {
		log.Error(err, "Failed to get Benchmark ConfigMap")
		return ctrl.Result{}, err
	} else {
		// Benchmark ConfigMap exists - extract TPS and update status
		if tps, ok := benchmarkCM.Data["tokensPerSecond"]; ok && tps != "" {
			if modelDeployment.Status.TokensPerSecond != tps {
				modelDeployment.Status.TokensPerSecond = tps
				log.Info("Updated TokensPerSecond from benchmark", "tps", tps)
				r.Recorder.Event(modelDeployment, corev1.EventTypeNormal, "BenchmarkComplete", fmt.Sprintf("Benchmark completed: %.1f tokens/sec", parseTPSFloat(tps)))
			}
		}
	}

	didMutate := false

	// Handle legacy/private PVC if not using ModelCache
	// Skip PVC creation for backends that download models on-the-fly (diffusers, comfyui, tei)
	backendType := canonicalBackend(modelDeployment.Spec.Backend)
	needsPVC := modelDeployment.Spec.ModelCacheRef == nil &&
		backendType != "diffusers" &&
		backendType != "comfyui" &&
		backendType != "tei"

	if needsPVC {
		// Legacy/Private PVC Logic
		// Check if the pvc already exists, if not create a new one
		pvc := &corev1.PersistentVolumeClaim{}
		err = r.Get(ctx, types.NamespacedName{Name: modelDeployment.Name, Namespace: modelDeployment.Namespace}, pvc)
		if err != nil && errors.IsNotFound(err) {
			// Define a new pvc
			newPVC, buildErr := r.pvcForModelDeployment(modelDeployment)
			if buildErr != nil {
				log.Error(buildErr, "Failed to build Pvc")
				return ctrl.Result{}, buildErr
			}
			log.Info("Creating a new Pvc", "Pvc.Namespace", newPVC.Namespace, "Pvc.Name", newPVC.Name)
			if err = r.Create(ctx, newPVC); err != nil {
				log.Error(err, "Failed to create new Pvc", "Pvc.Namespace", newPVC.Namespace, "Pvc.Name", newPVC.Name)
				return ctrl.Result{}, err
			}
			didMutate = true
		} else if err != nil {
			log.Error(err, "Failed to get Pvc")
			return ctrl.Result{}, err
		}
	}

	// Check if the deployment already exists, if not create a new one
	found := &appsv1.Deployment{}
	err = r.Get(ctx, types.NamespacedName{Name: modelDeployment.Name, Namespace: modelDeployment.Namespace}, found)
	if err != nil && errors.IsNotFound(err) {
		// Define a new deployment
		dep, buildErr := r.deploymentForModelDeployment(modelDeployment, volumeName, volumeReadOnly)
		if buildErr != nil {
			log.Error(buildErr, "Failed to build Deployment")
			return ctrl.Result{}, buildErr
		}
		log.Info("Creating a new Deployment", "Deployment.Namespace", dep.Namespace, "Deployment.Name", dep.Name)
		if err = r.Create(ctx, dep); err != nil {
			log.Error(err, "Failed to create new Deployment", "Deployment.Namespace", dep.Namespace, "Deployment.Name", dep.Name)
			return ctrl.Result{}, err
		}
		found = dep
		didMutate = true
	} else if err != nil {
		log.Error(err, "Failed to get Deployment")
		return ctrl.Result{}, err
	}

	// Check for idle scale-down BEFORE syncing deployment replicas.
	// If the model is idle, we update the ModelDeployment spec first, then the sync
	// logic below will correctly set the deployment to the new replica count.
	if scaledDown, err := r.checkIdleScaleDown(ctx, modelDeployment, found); err != nil {
		log.Error(err, "Failed to check idle scale down")
		// Continue anyway - don't block normal reconciliation
	} else if scaledDown {
		// If we scaled down, re-fetch to get updated spec.replicas before syncing
		if err := r.Get(ctx, client.ObjectKeyFromObject(modelDeployment), modelDeployment); err != nil {
			log.Error(err, "Failed to re-fetch ModelDeployment after scale-down")
			return ctrl.Result{}, err
		}
	}

	// Ensure the deployment size is the same as the spec (default=1)
	desiredReplicaCount := int32(1)
	if modelDeployment.Spec.Replicas != nil {
		desiredReplicaCount = *modelDeployment.Spec.Replicas
	}

	needsUpdate := false
	if found.Spec.Replicas == nil || *found.Spec.Replicas != desiredReplicaCount {
		found.Spec.Replicas = ptr.To(desiredReplicaCount)
		needsUpdate = true
	}

	// Ensure LiteLLM annotations are correct on the Deployment.
	// LiteLLM discovery is service-based, but we also mirror annotations on the Deployment
	// for consistency/diagnostics.
	desiredAnnotations := getLiteLLMAnnotations(modelDeployment)
	if found.Annotations == nil {
		found.Annotations = make(map[string]string)
	}
	// Remove any stale LiteLLM annotations if integration is disabled.
	if desiredAnnotations == nil {
		for _, k := range []string{liteLLMAnnotationServedModel, liteLLMAnnotationAliases, liteLLMAnnotationCopilot} {
			if _, ok := found.Annotations[k]; ok {
				delete(found.Annotations, k)
				needsUpdate = true
			}
		}
	} else {
		for k, v := range desiredAnnotations {
			if found.Annotations[k] != v {
				found.Annotations[k] = v
				needsUpdate = true
			}
		}
	}

	desiredDep, buildErr := r.deploymentForModelDeployment(modelDeployment, volumeName, volumeReadOnly)
	if buildErr != nil {
		log.Error(buildErr, "Failed to build desired Deployment for sync")
		return ctrl.Result{}, buildErr
	}

	// Keep existing selector; only sync mutable fields.
	if !apiequality.Semantic.DeepEqual(found.Spec.Strategy, desiredDep.Spec.Strategy) {
		found.Spec.Strategy = desiredDep.Spec.Strategy
		needsUpdate = true
	}

	desiredPodSpec := desiredDep.Spec.Template.Spec
	if found.Spec.Template.Spec.SchedulerName != desiredPodSpec.SchedulerName {
		found.Spec.Template.Spec.SchedulerName = desiredPodSpec.SchedulerName
		needsUpdate = true
	}
	if !apiequality.Semantic.DeepEqual(found.Spec.Template.Spec.NodeSelector, desiredPodSpec.NodeSelector) {
		found.Spec.Template.Spec.NodeSelector = desiredPodSpec.NodeSelector
		needsUpdate = true
	}
	if !apiequality.Semantic.DeepEqual(found.Spec.Template.Spec.RuntimeClassName, desiredPodSpec.RuntimeClassName) {
		found.Spec.Template.Spec.RuntimeClassName = desiredPodSpec.RuntimeClassName
		needsUpdate = true
	}
	if !apiequality.Semantic.DeepEqual(found.Spec.Template.Spec.Tolerations, desiredPodSpec.Tolerations) {
		found.Spec.Template.Spec.Tolerations = desiredPodSpec.Tolerations
		needsUpdate = true
	}
	if !apiequality.Semantic.DeepEqual(found.Spec.Template.Spec.SecurityContext, desiredPodSpec.SecurityContext) {
		found.Spec.Template.Spec.SecurityContext = desiredPodSpec.SecurityContext
		needsUpdate = true
	}
	if !apiequality.Semantic.DeepEqual(found.Spec.Template.Spec.InitContainers, desiredPodSpec.InitContainers) {
		found.Spec.Template.Spec.InitContainers = desiredPodSpec.InitContainers
		needsUpdate = true
	}
	if !apiequality.Semantic.DeepEqual(found.Spec.Template.Spec.Containers, desiredPodSpec.Containers) {
		found.Spec.Template.Spec.Containers = desiredPodSpec.Containers
		needsUpdate = true
	}
	if !apiequality.Semantic.DeepEqual(found.Spec.Template.Spec.Volumes, desiredPodSpec.Volumes) {
		found.Spec.Template.Spec.Volumes = desiredPodSpec.Volumes
		needsUpdate = true
	}
	if !apiequality.Semantic.DeepEqual(found.Spec.Template.Spec.TerminationGracePeriodSeconds, desiredPodSpec.TerminationGracePeriodSeconds) {
		found.Spec.Template.Spec.TerminationGracePeriodSeconds = desiredPodSpec.TerminationGracePeriodSeconds
		needsUpdate = true
	}

	if !apiequality.Semantic.DeepEqual(found.Spec.Template.Annotations, desiredDep.Spec.Template.Annotations) {
		found.Spec.Template.Annotations = desiredDep.Spec.Template.Annotations
		needsUpdate = true
	}

	if needsUpdate {
		if err = r.Update(ctx, found); err != nil {
			log.Error(err, "Failed to update Deployment", "Deployment.Namespace", found.Namespace, "Deployment.Name", found.Name)
			return ctrl.Result{}, err
		}
		didMutate = true
	}

	// Check if the service already exists, if not create a new one
	service := &corev1.Service{}
	err = r.Get(ctx, types.NamespacedName{Name: modelDeployment.Name, Namespace: modelDeployment.Namespace}, service)
	if err != nil && errors.IsNotFound(err) {
		// Define a new service
		svc, buildErr := r.serviceForModelDeployment(modelDeployment)
		if buildErr != nil {
			log.Error(buildErr, "Failed to build Service")
			return ctrl.Result{}, buildErr
		}
		log.Info("Creating a new Service", "Service.Namespace", svc.Namespace, "Service.Name", svc.Name)
		r.Recorder.Event(modelDeployment, corev1.EventTypeNormal, "ServiceCreating", "Creating service for ModelDeployment")
		if err = r.Create(ctx, svc); err != nil {
			log.Error(err, "Failed to create new Service", "Service.Namespace", svc.Namespace, "Service.Name", svc.Name)
			r.Recorder.Event(modelDeployment, corev1.EventTypeWarning, "ServiceCreateFailed", fmt.Sprintf("Failed to create service: %v", err))
			return ctrl.Result{}, err
		}
		r.Recorder.Event(modelDeployment, corev1.EventTypeNormal, "ServiceCreated", "Service created successfully")
		service = svc
		didMutate = true
	} else if err != nil {
		log.Error(err, "Failed to get Service")
		r.Recorder.Event(modelDeployment, corev1.EventTypeWarning, "ServiceGetFailed", fmt.Sprintf("Failed to get service: %v", err))
		return ctrl.Result{}, err
	}

	// Ensure 'app' label is present on the Service metadata for LiteLLM discovery
	// NOTE: We update metadata labels but NOT the selector, as changing the selector
	// to 'app=<name>' would break routing to existing Pods that have 'app=modeldeployment'.
	// New Services created after this change will have the correct selector from the start.
	svcNeedsUpdate := false
	if service.Labels == nil {
		service.Labels = make(map[string]string)
	}
	if service.Labels["app"] != modelDeployment.Name {
		service.Labels["app"] = modelDeployment.Name
		svcNeedsUpdate = true
	}

	// Ensure LiteLLM annotations (and service label routing metadata) are present on the Service.
	// LiteLLM discovers models via Service annotations.
	if service.Annotations == nil {
		service.Annotations = make(map[string]string)
	}
	desiredSvcAnnotations := make(map[string]string)
	if llm := getLiteLLMAnnotations(modelDeployment); llm != nil {
		for k, v := range llm {
			desiredSvcAnnotations[k] = v
		}
	}
	if len(modelDeployment.Spec.ServiceLabels) > 0 {
		desiredSvcAnnotations["flexinfer.ai/service-labels"] = strings.Join(modelDeployment.Spec.ServiceLabels, ",")
	}

	// Remove stale keys we own when disabled.
	for _, k := range []string{
		liteLLMAnnotationServedModel,
		liteLLMAnnotationAliases,
		liteLLMAnnotationCopilot,
		"flexinfer.ai/service-labels",
	} {
		if _, want := desiredSvcAnnotations[k]; !want {
			if _, ok := service.Annotations[k]; ok {
				delete(service.Annotations, k)
				svcNeedsUpdate = true
			}
		}
	}
	for k, v := range desiredSvcAnnotations {
		if service.Annotations[k] != v {
			service.Annotations[k] = v
			svcNeedsUpdate = true
		}
	}

	if svcNeedsUpdate {
		if err = r.Update(ctx, service); err != nil {
			log.Error(err, "Failed to update Service", "Service.Namespace", service.Namespace, "Service.Name", service.Name)
			return ctrl.Result{}, err
		}
		didMutate = true
	}

	// Update endpoint status now that service exists
	if err := r.updateEndpointStatus(ctx, modelDeployment, service); err != nil {
		log.Error(err, "Failed to update endpoint status")
		return ctrl.Result{}, err
	}

	// All resources are ready - update final status
	if err := r.updateModelDeploymentStatus(ctx, modelDeployment, aiv1alpha1.ModelDeploymentPhaseRunning, "ModelDeployment is running successfully"); err != nil {
		log.Error(err, "Failed to update final status")
		return ctrl.Result{}, err
	}

	// Update Ready condition based on actual deployment readiness.
	// For serverless (minReplicas=0), we consider "Ready" to mean:
	// - If spec.replicas=0: Ready=False (scaled down, waiting for traffic)
	// - If spec.replicas>0: Ready=True only when deployment has available replicas
	desiredReplicas := int32(1)
	if modelDeployment.Spec.Replicas != nil {
		desiredReplicas = *modelDeployment.Spec.Replicas
	}

	if desiredReplicas == 0 {
		// Scaled down - Ready should be False (idle)
		if err := r.updateCondition(ctx, modelDeployment, aiv1alpha1.ConditionTypeReady, metav1.ConditionFalse, "Idle", "Model is scaled to zero, waiting for traffic"); err != nil {
			log.Error(err, "Failed to update Ready condition for idle model")
			return ctrl.Result{}, err
		}
	} else if found.Status.ReadyReplicas > 0 {
		// Running and has ready pods - Ready=True
		if err := r.updateCondition(ctx, modelDeployment, aiv1alpha1.ConditionTypeReady, metav1.ConditionTrue, aiv1alpha1.ReasonDeploymentReady, "All resources are ready and healthy"); err != nil {
			log.Error(err, "Failed to update Ready condition")
			return ctrl.Result{}, err
		}
	} else {
		// Running but no ready pods yet - Ready=False (starting up)
		if err := r.updateCondition(ctx, modelDeployment, aiv1alpha1.ConditionTypeReady, metav1.ConditionFalse, "Pending", fmt.Sprintf("Waiting for pods to become ready (0/%d ready)", desiredReplicas)); err != nil {
			log.Error(err, "Failed to update Ready condition")
			return ctrl.Result{}, err
		}
		// Requeue to check again soon
		return ctrl.Result{RequeueAfter: 5 * time.Second}, nil
	}

	if didMutate {
		return ctrl.Result{Requeue: true}, nil
	}

	// Note: Scale-to-Zero check is now done earlier in reconciliation (before deployment sync)
	// This ensures the deployment gets the correct replica count immediately.

	r.Recorder.Event(modelDeployment, corev1.EventTypeNormal, "ReconcileComplete", "ModelDeployment reconciliation completed successfully")

	// Schedule periodic idle check if serverless is enabled and model is running
	// This ensures responsive scale-down even without external triggers
	if r.shouldScheduleIdleCheck(modelDeployment, found) {
		checkInterval := r.getIdleCheckInterval(modelDeployment)
		log.V(1).Info("Scheduling idle check", "interval", checkInterval)
		return ctrl.Result{RequeueAfter: checkInterval}, nil
	}

	return ctrl.Result{}, nil
}

// checkIdleScaleDown checks if the deployment should be scaled to zero.
// Returns (true, nil) if scaled down, (false, nil) if not scaled, (false, err) on error.
func (r *ModelDeploymentReconciler) checkIdleScaleDown(ctx context.Context, m *aiv1alpha1.ModelDeployment, deployment *appsv1.Deployment) (bool, error) {
	log := log.FromContext(ctx)

	// If this ModelDeployment belongs to a GPUGroup, the GPUGroup controller handles scaling
	if m.Spec.GPUGroupRef != nil {
		log.V(1).Info("Skipping idle scale-down check: managed by GPUGroup", "gpuGroup", *m.Spec.GPUGroupRef)
		return false, nil
	}

	// If MinReplicas is not set or > 0, we don't scale to zero
	if m.Spec.MinReplicas == nil || *m.Spec.MinReplicas > 0 {
		return false, nil
	}

	// If already at MinReplicas, nothing to do
	if *deployment.Spec.Replicas <= *m.Spec.MinReplicas {
		return false, nil
	}

	// detailed logic:
	// 1. Get LastAccessTime from status
	// 2. If nil, assume active (or set to now if we want to start timer)
	// 3. If time.Since(LastAccessTime) > IdleTimeout, scale down

	lastAccess := m.Status.LastAccessTime
	if lastAccess == nil {
		// If no last access time is recorded, we assume it's active or just deployed.
		// However, to start the timer, the Proxy MUST set this.
		// For safety, if it's missing, we do nothing.
		return false, nil
	}

	idleTimeout := int32(300) // Default 5 minutes
	if m.Spec.IdleTimeoutSeconds != nil {
		idleTimeout = *m.Spec.IdleTimeoutSeconds
	}

	if time.Since(lastAccess.Time) > time.Duration(idleTimeout)*time.Second {
		// Scale down to minReplicas due to inactivity.
		// We update the ModelDeployment's spec.replicas (not just the deployment)
		// because the controller's reconciliation loop syncs deployment replicas
		// to match the ModelDeployment spec. If we only update the deployment,
		// the next reconciliation will reset it back.

		newReplicas := *m.Spec.MinReplicas

		// Re-fetch to get latest version before updating spec
		// IMPORTANT: Use APIReader (uncached) to avoid race conditions with proxy updates
		fresh := &aiv1alpha1.ModelDeployment{}
		if err := r.APIReader.Get(ctx, client.ObjectKeyFromObject(m), fresh); err != nil {
			return false, fmt.Errorf("failed to get fresh ModelDeployment: %w", err)
		}

		// Log the comparison values for debugging
		log.Info("Idle scale-down check",
			"staleLastAccess", lastAccess.Time,
			"freshLastAccess", fresh.Status.LastAccessTime,
			"staleReplicas", m.Spec.Replicas,
			"freshReplicas", fresh.Spec.Replicas,
			"resourceVersion.stale", m.ResourceVersion,
			"resourceVersion.fresh", fresh.ResourceVersion)

		// Re-check idle condition with fresh data - abort if lastAccessTime was updated
		// This prevents a race where the proxy updates lastAccessTime during our scale-down decision
		if fresh.Status.LastAccessTime != nil && fresh.Status.LastAccessTime.After(lastAccess.Time) {
			log.Info("Aborting scale-down: lastAccessTime was updated during reconciliation",
				"stale", lastAccess.Time, "fresh", fresh.Status.LastAccessTime.Time)
			return false, nil
		}

		// Also abort if spec.replicas was changed DURING this reconciliation by the proxy
		// Compare with the original m.Spec.Replicas, not with minReplicas
		originalReplicas := int32(0)
		if m.Spec.Replicas != nil {
			originalReplicas = *m.Spec.Replicas
		}
		freshReplicas := int32(0)
		if fresh.Spec.Replicas != nil {
			freshReplicas = *fresh.Spec.Replicas
		}
		if freshReplicas > originalReplicas {
			log.Info("Aborting scale-down: spec.replicas was updated during reconciliation",
				"original", originalReplicas, "fresh", freshReplicas)
			return false, nil
		}

		fresh.Spec.Replicas = &newReplicas
		if err := r.Update(ctx, fresh); err != nil {
			return false, fmt.Errorf("failed to update ModelDeployment replicas: %w", err)
		}

		r.Recorder.Event(m, corev1.EventTypeNormal, "ScaledDown", fmt.Sprintf("Scaled down to %d replicas due to inactivity", newReplicas))

		// Update status to reflect we triggered this
		if err := r.updateCondition(ctx, fresh, aiv1alpha1.ConditionTypeReady, metav1.ConditionFalse, "Idle", "Deployment scaled down to zero due to inactivity"); err != nil {
			return true, err // Scaled down but condition update failed
		}
		return true, nil
	}

	return false, nil
}

// shouldScheduleIdleCheck returns true if periodic idle checks should be scheduled.
// This is needed when serverless is enabled and the model is currently running.
func (r *ModelDeploymentReconciler) shouldScheduleIdleCheck(m *aiv1alpha1.ModelDeployment, deployment *appsv1.Deployment) bool {
	// Serverless is enabled when MinReplicas is set to 0
	if m.Spec.MinReplicas == nil || *m.Spec.MinReplicas > 0 {
		return false
	}

	// Only schedule if model is currently running (replicas > minReplicas)
	if deployment.Spec.Replicas == nil || *deployment.Spec.Replicas <= *m.Spec.MinReplicas {
		return false
	}

	return true
}

// getIdleCheckInterval returns the interval at which to check for idle scale-down.
// Uses half the idle timeout for responsive scale-down.
func (r *ModelDeploymentReconciler) getIdleCheckInterval(m *aiv1alpha1.ModelDeployment) time.Duration {
	idleTimeout := 300 * time.Second // Default 5 minutes
	if m.Spec.IdleTimeoutSeconds != nil {
		idleTimeout = time.Duration(*m.Spec.IdleTimeoutSeconds) * time.Second
	}

	// Check at half the timeout to ensure responsive scale-down
	// Minimum interval of 30 seconds to avoid excessive reconciliation
	checkInterval := idleTimeout / 2
	if checkInterval < 30*time.Second {
		checkInterval = 30 * time.Second
	}

	return checkInterval
}

// getTerminationGracePeriod returns the termination grace period for a model deployment.
// For serverless deployments, uses a longer grace period (60s) to allow in-flight requests
// to complete during scale-down. For regular deployments, uses the default (30s).
func (r *ModelDeploymentReconciler) getTerminationGracePeriod(m *aiv1alpha1.ModelDeployment) int64 {
	// If serverless is enabled (minReplicas=0), use a longer grace period
	// to ensure in-flight requests complete during scale-down
	if m.Spec.MinReplicas != nil && *m.Spec.MinReplicas == 0 {
		// Use cold start timeout if specified, as it represents expected request duration
		if m.Spec.ColdStartTimeoutSeconds != nil {
			return int64(*m.Spec.ColdStartTimeoutSeconds)
		}
		// Default to 60 seconds for serverless models
		return 60
	}
	// Default Kubernetes termination grace period
	return 30
}

// deploymentForModelDeployment returns a ModelDeployment Deployment object
func (r *ModelDeploymentReconciler) deploymentForModelDeployment(m *aiv1alpha1.ModelDeployment, volumeName string, readOnly bool) (*appsv1.Deployment, error) {
	ls := labelsForModelDeployment(m.Name)
	// Add 'app' label for LiteLLM service discovery
	ls["app"] = m.Name
	replicas := m.Spec.Replicas

	// Build LiteLLM annotations for the Deployment
	depAnnotations := getLiteLLMAnnotations(m)

	backendImage := r.getBackendImage(m)

	// Determine volume type based on path format:
	// - Empty string means no volume needed (e.g., diffusers downloads from HuggingFace)
	// - Absolute paths (starting with /) are NodeLocal hostPath volumes
	// - Format "pvcName:subPath" are SharedPVC volumes
	var volumes []corev1.Volume
	var volumeMounts []corev1.VolumeMount
	var initContainers []corev1.Container

	if volumeName != "" {
		volumeMount := corev1.VolumeMount{
			Name:      "model-cache",
			MountPath: "/models",
			ReadOnly:  readOnly,
		}

		var volume corev1.Volume
		if strings.HasPrefix(volumeName, "/") {
			// NodeLocal strategy: volumeName is an absolute hostPath
			volume = corev1.Volume{
				Name: "model-cache",
				VolumeSource: corev1.VolumeSource{
					HostPath: &corev1.HostPathVolumeSource{
						Path: volumeName,
						Type: hostPathTypePtr(corev1.HostPathDirectory),
					},
				},
			}
		} else {
			// SharedPVC strategy: parse "pvcName:subPath" format
			pvcName := volumeName
			subPath := ""
			if parts := strings.SplitN(volumeName, ":", 2); len(parts) == 2 {
				pvcName = parts[0]
				subPath = parts[1]
			}
			if subPath != "" {
				volumeMount.SubPath = subPath
			}
			volume = corev1.Volume{
				Name: "model-cache",
				VolumeSource: corev1.VolumeSource{
					PersistentVolumeClaim: &corev1.PersistentVolumeClaimVolumeSource{
						ClaimName: pvcName,
						ReadOnly:  readOnly,
					},
				},
			}
		}
		volumes = append(volumes, volume)
		volumeMounts = append(volumeMounts, volumeMount)
	}

	// Note: ROCm library mounts removed - mlc-llm:rocm64-v4+ images now bundle
	// all required libraries (hipblas, rocblas, libstdc++) using Ubuntu 24.04
	// as the base image, which has matching glibc version for ROCm 6.4.

	backend := canonicalBackend(m.Spec.Backend)
	if backend == "comfyui" && volumeName != "" {
		volumes = append(volumes, corev1.Volume{
			Name: "comfyui-checkpoints",
			VolumeSource: corev1.VolumeSource{
				EmptyDir: &corev1.EmptyDirVolumeSource{},
			},
		})

		volumeMounts = append(volumeMounts, corev1.VolumeMount{
			Name:      "comfyui-checkpoints",
			MountPath: "/app/ComfyUI/models/checkpoints",
		})

		initContainers = append(initContainers, corev1.Container{
			Name:            "comfyui-checkpoints-linker",
			Image:           backendImage,
			ImagePullPolicy: corev1.PullIfNotPresent,
			Command:         []string{"/bin/sh", "-lc"},
			Args: []string{`
set -euo pipefail
mkdir -p /checkpoints
rm -f /checkpoints/* 2>/dev/null || true

if [ -d /models/checkpoints ]; then
  src="/models/checkpoints"
else
  src="/models"
fi

linked=0
for f in "$src"/*.safetensors "$src"/*.ckpt; do
  if [ -e "$f" ]; then
    ln -sf "$f" "/checkpoints/$(basename "$f")"
    linked=1
  fi
done

if [ "$linked" -eq 0 ]; then
  echo "No checkpoint files found in $src"
fi
ls -la /checkpoints || true
`},
			Resources: corev1.ResourceRequirements{
				Requests: corev1.ResourceList{
					corev1.ResourceCPU:    resource.MustParse("10m"),
					corev1.ResourceMemory: resource.MustParse("64Mi"),
				},
				Limits: corev1.ResourceList{
					corev1.ResourceMemory: resource.MustParse("256Mi"),
				},
			},
			VolumeMounts: []corev1.VolumeMount{
				{
					Name:      "model-cache",
					MountPath: "/models",
					ReadOnly:  true,
				},
				{
					Name:      "comfyui-checkpoints",
					MountPath: "/checkpoints",
				},
			},
		})
	}

	deploymentStrategy := appsv1.DeploymentStrategy{}
	if r.detectGPUResourceFromSpec(m) != "" {
		deploymentStrategy = appsv1.DeploymentStrategy{
			Type: appsv1.RollingUpdateDeploymentStrategyType,
			RollingUpdate: &appsv1.RollingUpdateDeployment{
				MaxSurge:       ptr.To(intstr.FromInt32(0)),
				MaxUnavailable: ptr.To(intstr.FromInt32(1)),
			},
		}
	}

	dep := &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{
			Name:        m.Name,
			Namespace:   m.Namespace,
			Annotations: depAnnotations,
		},
		Spec: appsv1.DeploymentSpec{
			Replicas: replicas,
			Strategy: deploymentStrategy,
			Selector: &metav1.LabelSelector{
				MatchLabels: ls,
			},
			Template: corev1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{
					Labels: ls,
					Annotations: map[string]string{
						"flexinfer.ai/model":   m.Spec.Model,
						"flexinfer.ai/backend": canonicalBackend(m.Spec.Backend),
					},
				},
				Spec: corev1.PodSpec{
					// Use our custom scheduler
					SchedulerName: "flexinfer-scheduler",
					NodeSelector:  r.getNodeSelector(m),
					// Use NVIDIA runtime for CUDA workloads (provides libcuda.so driver access)
					RuntimeClassName: r.getRuntimeClassName(m),
					// Tolerate GPU node taints so model pods can run on dedicated GPU nodes
					Tolerations: []corev1.Toleration{
						{
							Key:      "dedicated",
							Operator: corev1.TolerationOpEqual,
							Value:    "gpu",
							Effect:   corev1.TaintEffectNoSchedule,
						},
					},
					// ROCm devices (/dev/kfd, /dev/dri/renderD*) are typically 0660 root:render.
					// Add the render group GID (992 on most systems) to supplementalGroups so
					// non-root users can access GPU devices without running as root.
					SecurityContext: func() *corev1.PodSecurityContext {
						if r.detectGPUResourceFromSpec(m) != "amd.com/gpu" {
							return nil
						}
						return &corev1.PodSecurityContext{
							// GID 992 is the render group on most ROCm hosts
							SupplementalGroups: []int64{992},
						}
					}(),
					InitContainers: initContainers,
					Containers: []corev1.Container{{
						Image:           backendImage,
						ImagePullPolicy: corev1.PullAlways,
						Name:            "llm-backend",
						Ports: []corev1.ContainerPort{{
							ContainerPort: r.getBackendPort(m),
							Name:          "http",
						}},
						Command:      r.getBackendCommand(m),
						Args:         r.getBackendArgs(m),
						Env:          r.getBackendEnv(m),
						Resources:    r.getResourceRequirements(m),
						VolumeMounts: volumeMounts,
						// Readiness probe ensures pod is only marked Ready when inference endpoint is serving.
						// This is critical for serverless scale-to-zero to work correctly.
						ReadinessProbe: r.getReadinessProbe(m),
					}},
					Volumes: volumes,
					// Graceful shutdown period for draining in-flight requests
					// LLM inference requests can take 10-30+ seconds for long context windows
					TerminationGracePeriodSeconds: ptr.To(r.getTerminationGracePeriod(m)),
				},
			},
		},
	}
	// Set ModelDeployment instance as the owner and controller
	if err := ctrl.SetControllerReference(m, dep, r.Scheme); err != nil {
		return nil, err
	}
	return dep, nil
}

// serviceForModelDeployment returns a ModelDeployment Service object
func (r *ModelDeploymentReconciler) serviceForModelDeployment(m *aiv1alpha1.ModelDeployment) (*corev1.Service, error) {
	ls := labelsForModelDeployment(m.Name)
	// Add 'app' label for LiteLLM service discovery (must match pod labels)
	ls["app"] = m.Name

	annotations := make(map[string]string)
	if llm := getLiteLLMAnnotations(m); llm != nil {
		for k, v := range llm {
			annotations[k] = v
		}
	}
	if len(m.Spec.ServiceLabels) > 0 {
		annotations["flexinfer.ai/service-labels"] = strings.Join(m.Spec.ServiceLabels, ",")
	}
	if len(annotations) == 0 {
		annotations = nil
	}

	svc := &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{
			Name:        m.Name,
			Namespace:   m.Namespace,
			Labels:      ls,
			Annotations: annotations,
		},
		Spec: corev1.ServiceSpec{
			Selector: ls,
			Ports: []corev1.ServicePort{{
				Port:       r.getBackendPort(m),
				TargetPort: intstr.FromString("http"),
				Name:       "http",
			}},
		},
	}
	// Set ModelDeployment instance as the owner and controller
	if err := ctrl.SetControllerReference(m, svc, r.Scheme); err != nil {
		return nil, err
	}
	return svc, nil
}

// pvcForModelDeployment returns a ModelDeployment Pvc object
func (r *ModelDeploymentReconciler) pvcForModelDeployment(m *aiv1alpha1.ModelDeployment) (*corev1.PersistentVolumeClaim, error) {
	pvc := &corev1.PersistentVolumeClaim{
		ObjectMeta: metav1.ObjectMeta{
			Name:      m.Name,
			Namespace: m.Namespace,
		},
		Spec: corev1.PersistentVolumeClaimSpec{
			AccessModes: []corev1.PersistentVolumeAccessMode{
				corev1.ReadWriteOnce,
			},
			Resources: corev1.ResourceRequirements{
				Requests: corev1.ResourceList{
					corev1.ResourceStorage: m.Spec.Resources.Requests[corev1.ResourceStorage],
				},
			},
		},
	}
	// Set ModelDeployment instance as the owner and controller
	if err := ctrl.SetControllerReference(m, pvc, r.Scheme); err != nil {
		return nil, err
	}
	return pvc, nil
}

// getBenchmarkerImage returns the benchmarker image from the environment variable or a default.
func (r *ModelDeploymentReconciler) getBenchmarkerImage() string {
	if image, ok := os.LookupEnv("BENCHMARKER_IMAGE"); ok {
		return image
	}
	return "flexinfer-bench:latest"
}

// jobForBenchmark returns a benchmark Job object
// volumePath is optional - if provided (format "pvcName:subPath"), mounts the cached model
func (r *ModelDeploymentReconciler) jobForBenchmark(m *aiv1alpha1.ModelDeployment, volumePath string) (*batchv1.Job, error) {
	// Benchmark jobs are now pure clients that call through the proxy.
	// The proxy signals demand to GPUGroup which scales up the model.
	// This ensures benchmarks respect the GPUGroup scheduling and test real production paths.
	backendType := canonicalBackend(m.Spec.Backend)
	benchmarkerImage := r.getBenchmarkerImage()

	warmupIterations := int32(2)
	minDuration := 30 * time.Second
	batchSize := int32(128)
	iterations := int32(5)
	if m.Spec.Benchmark != nil {
		if m.Spec.Benchmark.WarmupIterations != nil {
			warmupIterations = *m.Spec.Benchmark.WarmupIterations
		}
		if m.Spec.Benchmark.MinDuration != nil {
			minDuration = m.Spec.Benchmark.MinDuration.Duration
		}
		if m.Spec.Benchmark.BatchSize != nil {
			batchSize = *m.Spec.Benchmark.BatchSize
		}
		if m.Spec.Benchmark.Iterations != nil {
			iterations = *m.Spec.Benchmark.Iterations
		}
	}

	// Build benchmarker args - now includes model name for proxy routing
	benchArgs := []string{
		"--model", m.Spec.Model,
		"--model-name", m.Name, // Used for proxy routing
		"--configmap", r.benchmarkConfigMapName(m),
		"--backend", backendType,
		"--warmup-iterations", fmt.Sprintf("%d", warmupIterations),
		"--min-duration", minDuration.String(),
		"--iterations", fmt.Sprintf("%d", iterations),
		"--batch-size", fmt.Sprintf("%d", batchSize),
	}

	// Proxy URL - benchmark calls through proxy which signals GPUGroup for scale-up
	// Default targets the in-cluster proxy service; can be overridden by env.
	proxyURL := benchmarkconfig.ProxyURL()

	job := &batchv1.Job{
		ObjectMeta: metav1.ObjectMeta{
			Name:      r.benchmarkJobName(m),
			Namespace: m.Namespace,
		},
		Spec: batchv1.JobSpec{
			Template: corev1.PodTemplateSpec{
				Spec: corev1.PodSpec{
					ServiceAccountName: benchmarkServiceAccountName(),
					// No GPU-specific node selector needed - benchmark is just a client
					// Can run on any node that can reach the proxy
					RestartPolicy: corev1.RestartPolicyNever,
					Containers: []corev1.Container{
						{
							Name:  "flexinfer-bench",
							Image: benchmarkerImage,
							Env: []corev1.EnvVar{
								{
									Name:  "PROXY_URL",
									Value: proxyURL,
								},
								{
									Name:  "MODEL_NAME",
									Value: m.Name,
								},
								{
									Name: "POD_NAMESPACE",
									ValueFrom: &corev1.EnvVarSource{
										FieldRef: &corev1.ObjectFieldSelector{
											FieldPath: "metadata.namespace",
										},
									},
								},
							},
							// Simple execution - just run the benchmark
							// Proxy handles model scale-up via GPUGroup
							Command: []string{"/flexinfer-bench"},
							Args:    benchArgs,
							Resources: corev1.ResourceRequirements{
								Requests: corev1.ResourceList{
									corev1.ResourceCPU:    resource.MustParse("100m"),
									corev1.ResourceMemory: resource.MustParse("128Mi"),
								},
							},
						},
					},
					TerminationGracePeriodSeconds: ptr.To(int64(10)),
				},
			},
			// Longer deadline for benchmark jobs (30 minutes)
			// Cold start can take several minutes for large models
			ActiveDeadlineSeconds: ptr.To(int64(1800)),
		},
	}

	if err := ctrl.SetControllerReference(m, job, r.Scheme); err != nil {
		return nil, err
	}
	return job, nil
}

func (r *ModelDeploymentReconciler) benchmarkJobName(m *aiv1alpha1.ModelDeployment) string {
	return fmt.Sprintf("%s-benchmark", m.Name)
}

func (r *ModelDeploymentReconciler) benchmarkConfigMapName(m *aiv1alpha1.ModelDeployment) string {
	return benchmarkconfig.DeploymentResultsConfigMapName(m.Name)
}

// getBackendImage returns the backend image based on spec and GPU vendor
func (r *ModelDeploymentReconciler) getBackendImage(m *aiv1alpha1.ModelDeployment) string {
	gpuArch := strings.ToLower(r.getGPUArchitecture(m))
	isGFX1100 := strings.HasPrefix(gpuArch, "gfx110")

	switch canonicalBackend(m.Spec.Backend) {
	case "vllm":
		// vLLM supports both NVIDIA (CUDA) and AMD (ROCm) GPUs
		gpuResource := r.detectGPUResourceFromSpec(m)
		switch gpuResource {
		case GPUResourceAMD:
			if isGFX1100 {
				if image, ok := os.LookupEnv("DEFAULT_VLLM_IMAGE_GFX1100"); ok {
					return image
				}
			}
			if image, ok := os.LookupEnv("DEFAULT_VLLM_IMAGE_AMD"); ok {
				return image
			}
			// ROCm-enabled vLLM image for gfx1100 (RX 7900 XTX)
			if isGFX1100 {
				return "registry.harbor.lan/library/vllm-api:rocm-gfx1100"
			}
			return "registry.harbor.lan/library/vllm-api:rocm-navi"
		default:
			if image, ok := os.LookupEnv("DEFAULT_VLLM_IMAGE"); ok {
				return image
			}
			// CUDA-enabled vLLM image (OpenAI-compatible API)
			return "vllm/vllm-openai:latest"
		}
	case "llamacpp":
		// llama.cpp supports NVIDIA (CUDA), AMD (ROCm/hipBLAS), and CPU
		gpuResource := r.detectGPUResourceFromSpec(m)
		switch gpuResource {
		case GPUResourceAMD:
			if image, ok := os.LookupEnv("DEFAULT_LLAMA_CPP_IMAGE_AMD"); ok {
				return image
			}
			// ROCm-enabled llama.cpp server
			return "ghcr.io/ggerganov/llama.cpp:server-rocm"
		default:
			if image, ok := os.LookupEnv("DEFAULT_LLAMA_CPP_IMAGE"); ok {
				return image
			}
			// CUDA-enabled llama.cpp server
			return "ghcr.io/ggerganov/llama.cpp:server-cuda"
		}
	case "mlc-llm":
		// MLC-LLM supports multiple GPU backends - select based on GPU vendor
		gpuResource := r.detectGPUResourceFromSpec(m)
		switch gpuResource {
		case GPUResourceAMD:
			if isGFX1100 {
				if image, ok := os.LookupEnv("DEFAULT_MLC_LLM_IMAGE_GFX1100"); ok {
					return image
				}
			}
			if image, ok := os.LookupEnv("DEFAULT_MLC_LLM_IMAGE_AMD"); ok {
				return image
			}
			// ROCm-enabled MLC-LLM image
			if isGFX1100 {
				return "registry.harbor.lan/flexinfer/mlc-llm:rocm64-gfx1100"
			}
			return "ghcr.io/mlc-ai/mlc-llm:rocm"
		case GPUResourceNVIDIA:
			// Check for Maxwell architecture first (requires custom build with CUDA 11.8)
			if r.isMaxwellGPU(m) {
				if image, ok := os.LookupEnv("DEFAULT_MLC_LLM_IMAGE_MAXWELL"); ok {
					return image
				}
				// Maxwell-specific image built with CUDA 11.8 and compute capability 52
				return "registry.harbor.lan/flexinfer/mlc-llm:cuda-maxwell-v7"
			}
			if image, ok := os.LookupEnv("DEFAULT_MLC_LLM_IMAGE"); ok {
				return image
			}
			// CUDA-enabled MLC-LLM image (requires CUDA 12.4+)
			return "ghcr.io/mlc-ai/mlc-llm:cuda"
		default:
			if image, ok := os.LookupEnv("DEFAULT_MLC_LLM_IMAGE"); ok {
				return image
			}
			// CUDA-enabled MLC-LLM image
			return "ghcr.io/mlc-ai/mlc-llm:cuda"
		}
	case "comfyui":
		// ComfyUI for image generation - supports ROCm and CUDA
		gpuResource := r.detectGPUResourceFromSpec(m)
		switch gpuResource {
		case GPUResourceAMD:
			if image, ok := os.LookupEnv("DEFAULT_COMFYUI_IMAGE_AMD"); ok {
				return image
			}
			// ROCm-enabled ComfyUI image
			return "registry.harbor.lan/library/comfyui:rocm6.2.3-v8"
		default:
			if image, ok := os.LookupEnv("DEFAULT_COMFYUI_IMAGE"); ok {
				return image
			}
			// CUDA-enabled ComfyUI image
			return "comfyanonymous/comfyui:latest"
		}
	case "vllm-omni":
		// vLLM-Omni for diffusion models with OpenAI-compatible API
		gpuResource := r.detectGPUResourceFromSpec(m)
		switch gpuResource {
		case GPUResourceAMD:
			if isGFX1100 {
				if image, ok := os.LookupEnv("DEFAULT_VLLM_IMAGE_GFX1100"); ok {
					return image
				}
			}
			if image, ok := os.LookupEnv("DEFAULT_VLLM_OMNI_IMAGE_AMD"); ok {
				return image
			}
			// ROCm-enabled vLLM-Omni image for gfx1100 (RX 7900 XTX)
			if isGFX1100 {
				return "registry.harbor.lan/library/vllm-api:rocm-gfx1100"
			}
			return "registry.harbor.lan/library/vllm-api:rocm-navi"
		default:
			if image, ok := os.LookupEnv("DEFAULT_VLLM_OMNI_IMAGE"); ok {
				return image
			}
			// CUDA-enabled vLLM-Omni image
			return "vllm/vllm-openai:latest"
		}
	case "diffusers":
		// Diffusers API server with OpenAI-compatible image generation
		gpuResource := r.detectGPUResourceFromSpec(m)
		switch gpuResource {
		case GPUResourceAMD:
			if image, ok := os.LookupEnv("DEFAULT_DIFFUSERS_IMAGE_AMD"); ok {
				return image
			}
			// ROCm-enabled Diffusers API server
			return "registry.harbor.lan/library/diffusers-api:rocm6.2.3-fast1"
		default:
			if image, ok := os.LookupEnv("DEFAULT_DIFFUSERS_IMAGE"); ok {
				return image
			}
			// CUDA-enabled Diffusers API server
			return "registry.harbor.lan/library/diffusers-api:cuda"
		}
	case "tei":
		// Text Embeddings Inference - supports CPU, CUDA, and Intel
		gpuResource := r.detectGPUResourceFromSpec(m)
		switch gpuResource {
		case GPUResourceNVIDIA:
			if image, ok := os.LookupEnv("DEFAULT_TEI_IMAGE_CUDA"); ok {
				return image
			}
			// CUDA-enabled TEI image
			return "ghcr.io/huggingface/text-embeddings-inference:1.8"
		default:
			// CPU fallback (also works for unsupported GPUs)
			if image, ok := os.LookupEnv("DEFAULT_TEI_IMAGE_CPU"); ok {
				return image
			}
			return "ghcr.io/huggingface/text-embeddings-inference:cpu-1.8"
		}
	default:
	}

	// For ollama backend, select image based on GPU vendor from spec.resources
	gpuResource := r.detectGPUResourceFromSpec(m)
	switch gpuResource {
	case GPUResourceAMD:
		// AMD GPUs require ROCm-enabled image
		if image, ok := os.LookupEnv("DEFAULT_BACKEND_IMAGE_AMD"); ok {
			return image
		}
		return "ollama/ollama:rocm"
	case GPUResourceIntel:
		// Intel GPUs - use default for now (no dedicated Intel image yet)
		if image, ok := os.LookupEnv("DEFAULT_BACKEND_IMAGE_INTEL"); ok {
			return image
		}
		return "ollama/ollama:latest"
	default:
		// NVIDIA or unknown - use CUDA image
		if image, ok := os.LookupEnv("DEFAULT_BACKEND_IMAGE_NVIDIA"); ok {
			return image
		}
		// Fall back to legacy env var for backwards compatibility
		if image, ok := os.LookupEnv("DEFAULT_BACKEND_IMAGE"); ok {
			return image
		}
		return "ollama/ollama:latest"
	}
}

// getBackendPort returns the port based on backend
func (r *ModelDeploymentReconciler) getBackendPort(m *aiv1alpha1.ModelDeployment) int32 {
	switch canonicalBackend(m.Spec.Backend) {
	case "vllm":
		return 8000
	case "mlc-llm":
		return 8000
	case "llamacpp":
		return 8080
	case "comfyui":
		// ComfyUI WebSocket/HTTP server
		return 8188
	case "vllm-omni":
		// vLLM-Omni OpenAI-compatible API
		return 8000
	case "diffusers":
		// Diffusers API server
		return 8000
	case "tei":
		// Text Embeddings Inference
		return 80
	default:
	}
	return 11434
}

// getReadinessProbe returns the readiness probe configuration for the model container.
// This ensures the pod is only marked Ready when the inference endpoint is actually serving,
// which is critical for serverless scale-to-zero to work correctly.
func (r *ModelDeploymentReconciler) getReadinessProbe(m *aiv1alpha1.ModelDeployment) *corev1.Probe {
	port := r.getBackendPort(m)
	backend := canonicalBackend(m.Spec.Backend)

	// ComfyUI uses WebSocket - use TCP socket probe instead of HTTP
	if backend == "comfyui" {
		return &corev1.Probe{
			ProbeHandler: corev1.ProbeHandler{
				TCPSocket: &corev1.TCPSocketAction{
					Port: intstr.FromInt32(port),
				},
			},
			// Image gen models can take longer to load (SDXL, Flux models)
			InitialDelaySeconds: 10,
			PeriodSeconds:       10,
			TimeoutSeconds:      5,
			// Be very patient - large diffusion models take time
			FailureThreshold: 90, // 15 minutes of attempts (90 * 10s)
			SuccessThreshold: 1,
		}
	}

	// HTTP-based health checks for other backends
	path := "/v1/models"

	switch backend {
	case "", "ollama":
		// Ollama uses a different health endpoint
		path = "/api/tags"
	case "vllm-omni":
		// vLLM-Omni uses standard OpenAI endpoint
		path = "/v1/models"
	case "diffusers":
		// Diffusers API server uses /health
		path = "/health"
	case "tei":
		// Text Embeddings Inference uses /health
		path = "/health"
	}

	// Default probe timings
	initialDelay := int32(5)
	period := int32(5)
	failureThreshold := int32(60) // 5 minutes

	// Image generation backends need more time for model loading
	if isImageGenBackend(backend) {
		initialDelay = 10
		period = 10
		failureThreshold = 90 // 15 minutes
	}

	return &corev1.Probe{
		ProbeHandler: corev1.ProbeHandler{
			HTTPGet: &corev1.HTTPGetAction{
				Path: path,
				Port: intstr.FromInt32(port),
			},
		},
		InitialDelaySeconds: initialDelay,
		PeriodSeconds:       period,
		TimeoutSeconds:      5,
		FailureThreshold:    failureThreshold,
		SuccessThreshold:    1,
	}
}

// getBackendCommand returns the command (entrypoint override) for backends that need it.
// Returns nil for backends that use their container's default entrypoint.
func (r *ModelDeploymentReconciler) getBackendCommand(m *aiv1alpha1.ModelDeployment) []string {
	switch canonicalBackend(m.Spec.Backend) {
	case "mlc-llm":
		// MLC-LLM is invoked as a Python module: python -m mlc_llm
		// This works regardless of whether mlc_llm is in PATH
		return []string{"python", "-m", "mlc_llm"}
	case "comfyui":
		// ComfyUI: python main.py with --listen and other args
		return []string{"python", "main.py"}
	default:
		return nil
	}
}

// getBackendArgs returns the arguments based on backend
func (r *ModelDeploymentReconciler) getBackendArgs(m *aiv1alpha1.ModelDeployment) []string {
	// Determine model path - use /models when ModelCacheRef is set (cached model mounted at /models)
	modelPath := m.Spec.Model
	if m.Spec.ModelCacheRef != nil {
		// When using ModelCacheRef with SubPath, model files are mounted directly at /models
		modelPath = "/models"
	}

	switch canonicalBackend(m.Spec.Backend) {
	case "vllm":
		// vLLM args: --model is required, other options are optional
		if args, ok := os.LookupEnv("DEFAULT_VLLM_ARGS"); ok && args != "" {
			return strings.Fields(args)
		}
		// For vLLM, always use the model ID (HuggingFace repo format)
		// When ModelCacheRef is set, HF_HOME env var points to the cache mount
		// This allows vLLM to find cached models via standard HuggingFace resolution
		return r.buildVLLMArgs(m, m.Spec.Model)
	case "mlc-llm":
		// MLC-LLM serve command format: mlc_llm serve MODEL --host 0.0.0.0 --mode local
		// Complete args override for backwards compatibility
		if args, ok := os.LookupEnv("DEFAULT_MLC_LLM_ARGS"); ok && args != "" {
			return strings.Fields(args)
		}

		args := []string{"serve", modelPath, "--host", "0.0.0.0"}

		// Mode: CRD > env > default ("local" for lower memory usage)
		mode := r.getMLCMode(m)
		args = append(args, "--mode", mode)

		// Model library path (for pre-compiled libraries, required for Maxwell)
		if libPath := r.getMLCModelLib(m); libPath != "" {
			args = append(args, "--model-lib", libPath)
		}

		// Overrides for memory optimization
		overrides := r.buildMLCOverrides(m)
		if overrides != "" {
			args = append(args, "--overrides", overrides)
		}

		return args
	case "llamacpp":
		// llama.cpp server args
		if args, ok := os.LookupEnv("DEFAULT_LLAMA_CPP_ARGS"); ok && args != "" {
			return strings.Fields(args)
		}
		return r.buildLlamaCppArgs(m, modelPath)
	case "comfyui":
		// ComfyUI args
		if args, ok := os.LookupEnv("DEFAULT_COMFYUI_ARGS"); ok && args != "" {
			return strings.Fields(args)
		}
		return r.buildComfyUIArgs(m)
	case "vllm-omni":
		// vLLM-Omni args for diffusion models
		if args, ok := os.LookupEnv("DEFAULT_VLLM_OMNI_ARGS"); ok && args != "" {
			return strings.Fields(args)
		}
		return r.buildVLLMOmniArgs(m, modelPath)
	case "diffusers":
		// Diffusers API server - no command-line args needed, uses env vars
		return nil
	default:
	}
	return nil
}

// getBackendEnv returns environment variables for the backend container
func (r *ModelDeploymentReconciler) getBackendEnv(m *aiv1alpha1.ModelDeployment) []corev1.EnvVar {
	switch canonicalBackend(m.Spec.Backend) {
	case "mlc-llm":
		var env []corev1.EnvVar

		// GPU memory: CRD > env > Maxwell auto-detect > default 23GB
		gpuMem := r.getMLCGPUMemory(m)
		env = append(env, corev1.EnvVar{
			Name:  "MLC_GPU_SIZE_BYTES",
			Value: gpuMem,
		})

		// JIT Policy: CRD > env (only set if specified)
		if jitPolicy := r.getMLCJITPolicy(m); jitPolicy != "" {
			env = append(env, corev1.EnvVar{
				Name:  "MLC_JIT_POLICY",
				Value: jitPolicy,
			})
		}

		// ROCm environment variables for AMD GPUs
		if r.detectGPUResourceFromSpec(m) == GPUResourceAMD {
			env = append(env, r.rocmEnvVars()...)
		}

		return env
	case "ollama":
		return []corev1.EnvVar{
			{
				Name:  "OLLAMA_HOST",
				Value: "0.0.0.0",
			},
		}
	case "comfyui":
		// ComfyUI environment configuration
		var env []corev1.EnvVar
		// Ensure ComfyUI outputs are accessible
		env = append(env, corev1.EnvVar{
			Name:  "COMFYUI_OUTPUT_DIRECTORY",
			Value: "/output",
		})
		// GPU device ordering for multi-GPU setups
		env = append(env, corev1.EnvVar{
			Name:  "CUDA_DEVICE_ORDER",
			Value: "PCI_BUS_ID",
		})
		// ROCm-specific environment for AMD GPUs
		if r.detectGPUResourceFromSpec(m) == GPUResourceAMD {
			// Override GFX version for RDNA3 (RX 7900 series)
			env = append(env, corev1.EnvVar{
				Name:  "HSA_OVERRIDE_GFX_VERSION",
				Value: "11.0.0",
			})
			// Note: Don't set HIP_VISIBLE_DEVICES - the device plugin allocates
			// specific GPUs to the container, and within the container, the
			// allocated GPU is always at index 0.
		}
		return env
	case "vllm":
		return r.buildVLLMEnv(m, false)
	case "vllm-omni":
		// vLLM-Omni inherits vLLM env behavior and pins CUDA ordering.
		return r.buildVLLMEnv(m, true)
	case "diffusers":
		// Diffusers API server environment
		var env []corev1.EnvVar
		// Model ID for the diffusion model
		env = append(env, corev1.EnvVar{
			Name:  "MODEL_ID",
			Value: m.Spec.Model,
		})
		// Also set MODEL for compatibility
		env = append(env, corev1.EnvVar{
			Name:  "MODEL",
			Value: m.Spec.Model,
		})
		// Port configuration
		env = append(env, corev1.EnvVar{
			Name:  "PORT",
			Value: "8000",
		})

		// When using ModelCacheRef with broader mount (e.g., huggingface level),
		// compute paths to model and VAE subdirectories
		if m.Spec.ModelCacheRef != nil {
			// Model path: convert HF repo format (org/model) to filesystem path
			// e.g., "stabilityai/sdxl-turbo" -> "/models/stabilityai/sdxl-turbo"
			modelPath := "/models/" + m.Spec.Model
			env = append(env, corev1.EnvVar{
				Name:  "LOCAL_MODEL_PATH",
				Value: modelPath,
			})

			// VAE path for SDXL models: use madebyollin/sdxl-vae-fp16-fix
			// This fixes fp16 NaN issues that crash ROCm gfx1100 GPUs
			// See: https://huggingface.co/madebyollin/sdxl-vae-fp16-fix
			if strings.Contains(strings.ToLower(m.Spec.Model), "sdxl") {
				env = append(env, corev1.EnvVar{
					Name:  "VAE_PATH",
					Value: "/models/madebyollin/sdxl-vae-fp16-fix",
				})
			}
		}

		// ROCm-specific environment for AMD GPUs (gfx1100 / RX 7900 XTX)
		if r.detectGPUResourceFromSpec(m) == GPUResourceAMD {
			env = append(env, corev1.EnvVar{
				Name:  "HSA_OVERRIDE_GFX_VERSION",
				Value: "11.0.0",
			})
			// Force ROCm/HIP to only use the first visible GPU device.
			// Even with device plugin isolation, KFD sees all system GPUs.
			// Set both ROCR and HIP environment variables for complete isolation.
			env = append(env, corev1.EnvVar{
				Name:  "ROCR_VISIBLE_DEVICES",
				Value: "0",
			})
			env = append(env, corev1.EnvVar{
				Name:  "HIP_VISIBLE_DEVICES",
				Value: "0",
			})
			env = append(env, corev1.EnvVar{
				Name:  "GPU_DEVICE_ORDINAL",
				Value: "0",
			})
			// Use fp16 for SDXL with fixed VAE - reduces memory and is now stable
			env = append(env, corev1.EnvVar{
				Name:  "USE_FP16",
				Value: "1",
			})
			// Allow async GPU operations for performance (sync mode is significantly slower)
			env = append(env, corev1.EnvVar{
				Name:  "HIP_LAUNCH_BLOCKING",
				Value: "0",
			})
			// Enable AOTriton flash attention for gfx1100 performance/stability
			env = append(env, corev1.EnvVar{
				Name:  "TORCH_ROCM_AOTRITON_ENABLE_EXPERIMENTAL",
				Value: "1",
			})
			// Disable CPU offload - gfx1100 is fast enough without it and
			// CPU offload causes ~10x slowdown on modern RDNA3 GPUs
			env = append(env, corev1.EnvVar{
				Name:  "USE_CPU_OFFLOAD",
				Value: "0",
			})

			// SDXL Turbo is designed to run in very few steps. When clients omit
			// diffusion params (OpenAI-compatible requests), keep defaults fast.
			if strings.Contains(strings.ToLower(m.Spec.Model), "sdxl-turbo") {
				env = append(env,
					corev1.EnvVar{Name: "DEFAULT_NUM_INFERENCE_STEPS", Value: "4"},
					corev1.EnvVar{Name: "DEFAULT_GUIDANCE_SCALE", Value: "0.0"},
				)
			}
		}
		return env
	case "tei":
		// Text Embeddings Inference environment
		optionalTrue := true
		var env []corev1.EnvVar
		// Model ID for the embedding model (HuggingFace repo or local path)
		env = append(env, corev1.EnvVar{
			Name:  "MODEL_ID",
			Value: m.Spec.Model,
		})
		// HuggingFace token for private/gated models
		env = append(env, corev1.EnvVar{
			Name: "HUGGINGFACE_HUB_TOKEN",
			ValueFrom: &corev1.EnvVarSource{
				SecretKeyRef: &corev1.SecretKeySelector{
					LocalObjectReference: corev1.LocalObjectReference{
						Name: "hf-token",
					},
					Key:      "HF_TOKEN",
					Optional: &optionalTrue,
				},
			},
		})
		return env
	default:
		return nil
	}
}

func (r *ModelDeploymentReconciler) buildVLLMEnv(m *aiv1alpha1.ModelDeployment, includeCUDADeviceOrder bool) []corev1.EnvVar {
	optionalTrue := true
	var env []corev1.EnvVar

	if includeCUDADeviceOrder {
		env = append(env, corev1.EnvVar{
			Name:  "CUDA_DEVICE_ORDER",
			Value: "PCI_BUS_ID",
		})
	}

	// When using ModelCacheRef, set HF_HOME so vLLM uses the cached models.
	// This allows passing the model ID to --model while using local cache.
	if m.Spec.ModelCacheRef != nil {
		env = append(env, corev1.EnvVar{
			Name:  "HF_HOME",
			Value: "/models",
		})
		env = append(env, corev1.EnvVar{
			Name:  "TRANSFORMERS_CACHE",
			Value: "/models",
		})
	}

	// HuggingFace token for private/gated models (optional).
	env = append(env, corev1.EnvVar{
		Name: "HUGGINGFACE_HUB_TOKEN",
		ValueFrom: &corev1.EnvVarSource{
			SecretKeyRef: &corev1.SecretKeySelector{
				LocalObjectReference: corev1.LocalObjectReference{
					Name: "hf-token",
				},
				Key:      "HF_TOKEN",
				Optional: &optionalTrue,
			},
		},
	})

	// ROCm-specific environment for AMD GPUs.
	if r.detectGPUResourceFromSpec(m) == GPUResourceAMD {
		env = append(env, r.rocmEnvVars()...)

		// Keep HIP and ROCR visibility in sync for reliable ROCm device isolation.
		// This helps on hosts where KFD can still enumerate multiple GPUs.
		if m.Spec.VLLM != nil && m.Spec.VLLM.HIPVisibleDevices != "" {
			env = append(env, corev1.EnvVar{
				Name:  "ROCR_VISIBLE_DEVICES",
				Value: m.Spec.VLLM.HIPVisibleDevices,
			})
			env = append(env, corev1.EnvVar{
				Name:  "HIP_VISIBLE_DEVICES",
				Value: m.Spec.VLLM.HIPVisibleDevices,
			})
		}
	}

	return env
}

// getNodeSelector returns the node selector for the deployment.
// For GPU workloads, adds flexinfer.ai/gpu-present: true.
// For CPU-only workloads, does not restrict to GPU nodes.
func (r *ModelDeploymentReconciler) getNodeSelector(m *aiv1alpha1.ModelDeployment) map[string]string {
	selector := map[string]string{}

	// Only add GPU node selector if this deployment requires GPU resources
	gpuResource := r.detectGPUResourceFromSpec(m)
	if gpuResource != "" {
		selector["flexinfer.ai/gpu-present"] = "true"
	}

	// Merge user-specified nodeSelector from spec (user values override defaults)
	for k, v := range m.Spec.NodeSelector {
		selector[k] = v
	}
	return selector
}

// getResourceRequirements returns the resource requirements including GPU resources
func (r *ModelDeploymentReconciler) getResourceRequirements(m *aiv1alpha1.ModelDeployment) corev1.ResourceRequirements {
	requirements := corev1.ResourceRequirements{
		Requests: corev1.ResourceList{},
		Limits:   corev1.ResourceList{},
	}

	// Add CPU and Memory resources from spec if present
	if m.Spec.Resources.Requests != nil {
		if cpu := m.Spec.Resources.Requests[corev1.ResourceCPU]; !cpu.IsZero() {
			requirements.Requests[corev1.ResourceCPU] = cpu
		}
		if memory := m.Spec.Resources.Requests[corev1.ResourceMemory]; !memory.IsZero() {
			requirements.Requests[corev1.ResourceMemory] = memory
		}
	}

	if m.Spec.Resources.Limits != nil {
		if cpu := m.Spec.Resources.Limits[corev1.ResourceCPU]; !cpu.IsZero() {
			requirements.Limits[corev1.ResourceCPU] = cpu
		}
		if memory := m.Spec.Resources.Limits[corev1.ResourceMemory]; !memory.IsZero() {
			requirements.Limits[corev1.ResourceMemory] = memory
		}
	}

	// Add GPU resource requests - support multiple vendors
	// Check if spec already defines GPU resources (AMD, NVIDIA, or Intel)
	// Only add GPU resources if this is not a CPU-only deployment
	gpuResourceName := r.detectGPUResourceFromSpec(m)
	if gpuResourceName != "" {
		// Use GPU count from spec, defaulting to 1 if not specified
		gpuCount := int64(1)
		if m.Spec.Resources.Limits != nil {
			if qty, exists := m.Spec.Resources.Limits[gpuResourceName]; exists && !qty.IsZero() {
				gpuCount = qty.Value()
			}
		}
		requirements.Requests[gpuResourceName] = *resource.NewQuantity(gpuCount, resource.DecimalSI)
		requirements.Limits[gpuResourceName] = *resource.NewQuantity(gpuCount, resource.DecimalSI)
	}

	return requirements
}

// GPU resource name constants for different vendors
const (
	GPUResourceNVIDIA = corev1.ResourceName("nvidia.com/gpu")
	GPUResourceAMD    = corev1.ResourceName("amd.com/gpu")
	GPUResourceIntel  = corev1.ResourceName("intel.com/gpu")
)

// detectGPUResourceFromSpec checks if the ModelDeployment spec already defines
// a GPU resource type (AMD, NVIDIA, or Intel) and returns it.
// Returns empty string if no GPU resource is requested (CPU-only deployment).
func (r *ModelDeploymentReconciler) detectGPUResourceFromSpec(m *aiv1alpha1.ModelDeployment) corev1.ResourceName {
	gpuResources := []corev1.ResourceName{GPUResourceAMD, GPUResourceNVIDIA, GPUResourceIntel}

	// Check requests first
	if m.Spec.Resources.Requests != nil {
		for _, gpuRes := range gpuResources {
			if qty, exists := m.Spec.Resources.Requests[gpuRes]; exists && !qty.IsZero() {
				return gpuRes
			}
		}
	}

	// Check limits
	if m.Spec.Resources.Limits != nil {
		for _, gpuRes := range gpuResources {
			if qty, exists := m.Spec.Resources.Limits[gpuRes]; exists && !qty.IsZero() {
				return gpuRes
			}
		}
	}

	// CPU-first by default unless explicitly requested.
	if canonicalBackend(m.Spec.Backend) == "tei" {
		return ""
	}

	// Infer from node selector hints when possible.
	if m.Spec.NodeSelector != nil {
		if vendor, ok := m.Spec.NodeSelector["flexinfer.ai/gpu.vendor"]; ok && vendor != "" {
			switch strings.ToUpper(vendor) {
			case "AMD":
				return GPUResourceAMD
			case "INTEL":
				return GPUResourceIntel
			case "NVIDIA":
				return GPUResourceNVIDIA
			}
		}
		if v, ok := m.Spec.NodeSelector["amd.com/gpu.present"]; ok && v == "true" {
			return GPUResourceAMD
		}
		if _, ok := m.Spec.NodeSelector["amd.com/gpu.arch"]; ok {
			return GPUResourceAMD
		}
		if v, ok := m.Spec.NodeSelector["nvidia.com/gpu.present"]; ok && v == "true" {
			return GPUResourceNVIDIA
		}
		if _, ok := m.Spec.NodeSelector["nvidia.com/gpu.product"]; ok {
			return GPUResourceNVIDIA
		}
	}

	// If the deployment targets a specific node, attempt to resolve the vendor from the Node labels.
	if r.Client != nil && m.Spec.NodeSelector != nil {
		if nodeName, ok := m.Spec.NodeSelector["kubernetes.io/hostname"]; ok && nodeName != "" {
			node := &corev1.Node{}
			if err := r.Get(context.Background(), types.NamespacedName{Name: nodeName}, node); err == nil {
				if vendor, ok := node.Labels["flexinfer.ai/gpu.vendor"]; ok && vendor != "" {
					switch strings.ToUpper(vendor) {
					case "AMD":
						return GPUResourceAMD
					case "INTEL":
						return GPUResourceIntel
					case "NVIDIA":
						return GPUResourceNVIDIA
					}
				}
				if v, ok := node.Labels["amd.com/gpu.present"]; ok && v == "true" {
					return GPUResourceAMD
				}
				if v, ok := node.Labels["nvidia.com/gpu.present"]; ok && v == "true" {
					return GPUResourceNVIDIA
				}
			}
		}
	}

	// Default: assume NVIDIA GPU if unspecified.
	return GPUResourceNVIDIA
}

// rocmEnvVars returns common environment variables for AMD ROCm GPUs.
// These help with GPU detection and stability on RDNA3 architecture.
// Note: LD_LIBRARY_PATH and LD_PRELOAD are no longer needed as mlc-llm:rocm64-v4+
// images bundle all required libraries with matching glibc version.
func (r *ModelDeploymentReconciler) rocmEnvVars() []corev1.EnvVar {
	return []corev1.EnvVar{
		{
			Name:  "HSA_OVERRIDE_GFX_VERSION",
			Value: "11.0.0", // RDNA3 (RX 7900 series)
		},
		{
			// Critical for gfx1100 stability - enables experimental AOTriton
			// flash attention which prevents SIGSEGV crashes on RDNA3.
			Name:  "TORCH_ROCM_AOTRITON_ENABLE_EXPERIMENTAL",
			Value: "1",
		},
		{
			Name:  "PYTORCH_ROCM_ARCH",
			Value: "gfx1100",
		},
		{
			Name: "VLLM_USE_TRITON_FLASH_ATTN",
			// Prefer CK flash attention on consumer RDNA3 (gfx1100). This avoids
			// Triton FP8 dtype issues during prefill and improves stability for
			// some architectures (e.g., SWA support).
			Value: "0",
		},
		{
			// Disable AITER (AI Tensor Engine for ROCm) on consumer RDNA3 GPUs.
			// AITER is optimized for MI300X/CDNA3 architecture, not gfx1100.
			// Enabling AITER on gfx1100 causes GPU hangs during attention ops.
			Name:  "VLLM_ROCM_USE_AITER",
			Value: "0",
		},
		{
			// Force vLLM V0 engine on RDNA3 GPUs.
			// V1 engine only supports Triton-based attention which causes GPU hangs on gfx1100.
			// V0 engine supports CK flash attention via VLLM_USE_TRITON_FLASH_ATTN=0.
			Name:  "VLLM_USE_V1",
			Value: "0",
		},
	}
}

// getRuntimeClassName returns the appropriate RuntimeClassName for the GPU type.
// NVIDIA GPUs require "nvidia" runtime for driver access (libcuda.so).
// AMD GPUs don't need a special runtime (uses default containerd).
func (r *ModelDeploymentReconciler) getRuntimeClassName(m *aiv1alpha1.ModelDeployment) *string {
	gpuResource := r.detectGPUResourceFromSpec(m)
	switch gpuResource {
	case GPUResourceNVIDIA:
		runtime := "nvidia"
		return &runtime
	default:
		// AMD and Intel use default runtime
		return nil
	}
}

// isMaxwellGPU checks if the ModelDeployment targets a Maxwell GPU node
// via node selector labels. Maxwell GPUs (compute capability 5.x) require
// special handling because MLC-LLM only provides CUDA 12.4+ wheels.
func (r *ModelDeploymentReconciler) isMaxwellGPU(m *aiv1alpha1.ModelDeployment) bool {
	if m.Spec.NodeSelector == nil {
		return false
	}
	// Check for explicit Maxwell label from NVIDIA device plugin
	if arch, ok := m.Spec.NodeSelector["nvidia.com/gpu.arch"]; ok {
		return strings.ToLower(arch) == "maxwell"
	}
	// Check for compute capability 5.x (Maxwell is 5.0, 5.2, 5.3)
	if cc, ok := m.Spec.NodeSelector["nvidia.com/gpu.compute.major"]; ok {
		return cc == "5"
	}
	return false
}

// validateBackendGPUCompatibility checks if the backend is compatible with the target GPU.
// Returns an error if the combination is invalid (e.g., vLLM on Maxwell).
func (r *ModelDeploymentReconciler) validateBackendGPUCompatibility(m *aiv1alpha1.ModelDeployment) error {
	backend := canonicalBackend(m.Spec.Backend)
	isMaxwell := r.isMaxwellGPU(m)

	// vLLM requires sm_70+ (Volta or newer), Maxwell (sm_52) is not supported
	if backend == "vllm" && isMaxwell {
		return fmt.Errorf("vLLM backend is not supported on Maxwell GPUs (compute capability 5.x). Use ollama, mlc-llm with pre-compiled library, or llamacpp instead")
	}

	// MLC-LLM on Maxwell requires pre-compiled model library (no JIT support)
	if (backend == "mlc-llm" || backend == "mlc") && isMaxwell {
		if m.Spec.MLCLLM == nil || m.Spec.MLCLLM.ModelLibPath == "" {
			return fmt.Errorf("MLC-LLM on Maxwell GPUs requires a pre-compiled modelLibPath (JIT compilation not supported on compute capability 5.x)")
		}
	}

	return nil
}

// validateVLLMSpecCompatibility checks for incompatible vLLM configuration combinations.
// Returns an error if validation fails.
func (r *ModelDeploymentReconciler) validateVLLMSpecCompatibility(m *aiv1alpha1.ModelDeployment) error {
	if m.Spec.VLLM == nil {
		return nil
	}

	v := m.Spec.VLLM
	isAMD := r.detectGPUResourceFromSpec(m) == GPUResourceAMD
	log := ctrl.Log.WithName("validateVLLMSpec")

	kvCacheDtype := strings.TrimSpace(v.KVCacheDtype)
	if kvCacheDtype != "" {
		if strings.EqualFold(kvCacheDtype, "int8") {
			log.Info("vllm.kvCacheDtype=int8 is deprecated; treating as fp8",
				"deployment", m.Name)
		}

		// Note: FP8 dtype support varies across GPU architectures and vLLM builds.
		// Don't hard-fail here; let the backend validate at runtime.
		if isAMD && kvCacheDtype == "fp8_e5m2" {
			log.Info("vllm.kvCacheDtype=fp8_e5m2 requested on AMD; support depends on ROCm/vLLM build and GPU arch",
				"deployment", m.Name)
		}
	}

	// Validate attention backend for AMD GPUs - warn about potentially unstable backends
	if isAMD && v.AttentionBackend != "" {
		backend := strings.ToUpper(v.AttentionBackend)
		if backend == "FLASH_ATTN" || backend == "FLASHINFER" {
			// Log warning but don't fail - user may know what they're doing
			log.Info("Warning: flash attention backends may be unstable on AMD ROCm GPUs",
				"backend", v.AttentionBackend,
				"recommendation", "TORCH_SDPA",
				"deployment", m.Name)
		}
	}

	// Validate RoPE scaling - factor required when type is specified (and vice versa)
	if v.RopeScaling != nil {
		if v.RopeScaling.Type != "" && v.RopeScaling.Factor == "" {
			return fmt.Errorf("vllm.ropeScaling.factor is required when vllm.ropeScaling.type is specified")
		}
		if v.RopeScaling.Factor != "" && v.RopeScaling.Type == "" {
			return fmt.Errorf("vllm.ropeScaling.type is required when vllm.ropeScaling.factor is specified")
		}
	}

	// Validate CPU offload with tensor parallelism - not compatible
	if v.CPUOffloadGB != nil && *v.CPUOffloadGB > 0 &&
		v.TensorParallelSize != nil && *v.TensorParallelSize > 1 {
		return fmt.Errorf("vllm.cpuOffloadGB is not compatible with tensor parallelism (vllm.tensorParallelSize > 1)")
	}

	// Log info when using prefix caching with chunked prefill (supported in modern vLLM)
	if v.EnableChunkedPrefill != nil && *v.EnableChunkedPrefill &&
		v.EnablePrefixCaching != nil && *v.EnablePrefixCaching {
		log.Info("Note: chunked prefill with prefix caching enabled (requires vLLM 0.6+)",
			"deployment", m.Name)
	}

	return nil
}

// getGPUArchitecture returns the GPU architecture string from node selector.
// Returns empty string if not determinable from spec.
func (r *ModelDeploymentReconciler) getGPUArchitecture(m *aiv1alpha1.ModelDeployment) string {
	if m.Spec.NodeSelector == nil {
		return ""
	}
	// FlexInfer node-agent label (preferred for mixed-vendor/mixed-arch clusters).
	if arch, ok := m.Spec.NodeSelector["flexinfer.ai/gpu.arch"]; ok && arch != "" {
		return arch
	}
	// Check for explicit architecture label
	if arch, ok := m.Spec.NodeSelector["nvidia.com/gpu.arch"]; ok {
		return arch
	}
	// Check for AMD ROCm architecture
	if arch, ok := m.Spec.NodeSelector["amd.com/gpu.arch"]; ok {
		return arch
	}
	// AMD device plugin commonly uses this key.
	if arch, ok := m.Spec.NodeSelector["gpu.amd.com/gpu-architecture"]; ok {
		return arch
	}
	// Check for compute capability and convert to sm_XX format
	if major, ok := m.Spec.NodeSelector["nvidia.com/gpu.compute.major"]; ok {
		minor := m.Spec.NodeSelector["nvidia.com/gpu.compute.minor"]
		if minor == "" {
			minor = "0"
		}
		return fmt.Sprintf("sm_%s%s", major, minor)
	}
	return ""
}

// getGPUVendor returns the GPU vendor from the spec.
// Detected from resource requests or node selectors.
func (r *ModelDeploymentReconciler) getGPUVendor(m *aiv1alpha1.ModelDeployment) string {
	gpuResource := r.detectGPUResourceFromSpec(m)
	switch gpuResource {
	case GPUResourceAMD:
		return "AMD"
	case GPUResourceIntel:
		return "Intel"
	default:
		return "NVIDIA"
	}
}

// getMLCMode returns the MLC-LLM serve mode.
// Precedence: CRD > env > default ("local")
func (r *ModelDeploymentReconciler) getMLCMode(m *aiv1alpha1.ModelDeployment) string {
	// CRD takes precedence
	if m.Spec.MLCLLM != nil && m.Spec.MLCLLM.Mode != "" {
		return m.Spec.MLCLLM.Mode
	}
	// Environment variable
	if mode, ok := os.LookupEnv("DEFAULT_MLC_LLM_MODE"); ok && mode != "" {
		return mode
	}
	// Default to "local" for lower memory usage
	return "local"
}

// getMLCModelLib returns the pre-compiled model library path.
// Precedence: CRD > env
func (r *ModelDeploymentReconciler) getMLCModelLib(m *aiv1alpha1.ModelDeployment) string {
	// CRD takes precedence
	if m.Spec.MLCLLM != nil && m.Spec.MLCLLM.ModelLibPath != "" {
		return m.Spec.MLCLLM.ModelLibPath
	}
	// Environment variable
	if libPath, ok := os.LookupEnv("DEFAULT_MLC_LLM_MODEL_LIB"); ok {
		return libPath
	}
	return ""
}

// getMLCGPUMemory returns the GPU memory limit in bytes.
// Precedence: CRD > env > Maxwell auto-detect (5GB) > default (23GB)
func (r *ModelDeploymentReconciler) getMLCGPUMemory(m *aiv1alpha1.ModelDeployment) string {
	// CRD takes precedence
	if m.Spec.MLCLLM != nil && m.Spec.MLCLLM.GPUMemoryBytes != nil {
		return fmt.Sprintf("%d", *m.Spec.MLCLLM.GPUMemoryBytes)
	}
	// Environment variable
	if gpuMem, ok := os.LookupEnv("DEFAULT_MLC_GPU_SIZE_BYTES"); ok && gpuMem != "" {
		return gpuMem
	}
	// Auto-detect Maxwell GPUs (limited to ~5GB usable)
	if r.isMaxwellGPU(m) {
		return "5000000000"
	}
	// Default: 23GB (leaves headroom on 24GB cards)
	return "23068672000"
}

// getMLCJITPolicy returns the JIT compilation policy.
// Precedence: CRD > env
func (r *ModelDeploymentReconciler) getMLCJITPolicy(m *aiv1alpha1.ModelDeployment) string {
	// CRD takes precedence
	if m.Spec.MLCLLM != nil && m.Spec.MLCLLM.JITPolicy != "" {
		return m.Spec.MLCLLM.JITPolicy
	}
	// Environment variable
	if jitPolicy, ok := os.LookupEnv("DEFAULT_MLC_JIT_POLICY"); ok {
		return jitPolicy
	}
	return ""
}

// buildMLCOverrides constructs the --overrides string for MLC-LLM.
// Format: key1=value1;key2=value2
func (r *ModelDeploymentReconciler) buildMLCOverrides(m *aiv1alpha1.ModelDeployment) string {
	var parts []string

	// Default values for memory optimization
	prefillChunkSize := int32(512)
	maxTotalSeqLength := int32(16384)

	// Override from CRD if specified
	if m.Spec.MLCLLM != nil && m.Spec.MLCLLM.Overrides != nil {
		overrides := m.Spec.MLCLLM.Overrides
		if overrides.PrefillChunkSize != nil {
			prefillChunkSize = *overrides.PrefillChunkSize
		}
		if overrides.MaxTotalSeqLength != nil {
			maxTotalSeqLength = *overrides.MaxTotalSeqLength
		}
		if overrides.ContextWindowSize != nil {
			parts = append(parts, fmt.Sprintf("context_window_size=%d", *overrides.ContextWindowSize))
		}
		// MaxNumSequence controls concurrent request batch size in server mode
		if overrides.MaxNumSequence != nil {
			parts = append(parts, fmt.Sprintf("max_num_sequence=%d", *overrides.MaxNumSequence))
		}
		// GPUMemoryUtilization sets fraction of GPU memory to use (0.0-1.0)
		if overrides.GPUMemoryUtilization != "" {
			parts = append(parts, fmt.Sprintf("gpu_memory_utilization=%s", overrides.GPUMemoryUtilization))
		}
	}

	// Always include memory optimization settings
	parts = append(parts, fmt.Sprintf("prefill_chunk_size=%d", prefillChunkSize))
	parts = append(parts, fmt.Sprintf("max_total_seq_length=%d", maxTotalSeqLength))

	// Append raw overrides if specified
	if m.Spec.MLCLLM != nil && m.Spec.MLCLLM.Overrides != nil && m.Spec.MLCLLM.Overrides.Raw != "" {
		parts = append(parts, m.Spec.MLCLLM.Overrides.Raw)
	}

	return strings.Join(parts, ";")
}

// buildVLLMArgs constructs command-line arguments for vLLM.
func (r *ModelDeploymentReconciler) buildVLLMArgs(m *aiv1alpha1.ModelDeployment, modelPath string) []string {
	args := []string{"--model", modelPath, "--host", "0.0.0.0"}

	if m.Spec.VLLM == nil {
		// No AMD-specific defaults needed when VLLM spec is nil
		// Note: --attention-backend removed - not all vLLM ROCm builds support it
		return args
	}

	v := m.Spec.VLLM

	// Tensor parallel size (multi-GPU)
	if v.TensorParallelSize != nil && *v.TensorParallelSize > 1 {
		args = append(args, "--tensor-parallel-size", fmt.Sprintf("%d", *v.TensorParallelSize))
	}

	// Data type
	if v.Dtype != "" {
		args = append(args, "--dtype", v.Dtype)
	}

	// Quantization
	if v.Quantization != "" && v.Quantization != "None" {
		args = append(args, "--quantization", v.Quantization)
	}

	// Max model length
	if v.MaxModelLen != nil {
		args = append(args, "--max-model-len", fmt.Sprintf("%d", *v.MaxModelLen))
	}

	// GPU memory utilization
	if v.GPUMemoryUtilization != nil && *v.GPUMemoryUtilization != "" {
		args = append(args, "--gpu-memory-utilization", *v.GPUMemoryUtilization)
	}

	// Enforce eager mode (disable CUDA graphs)
	if v.EnforceEager != nil && *v.EnforceEager {
		args = append(args, "--enforce-eager")
	}

	// Max number of sequences
	if v.MaxNumSeqs != nil {
		args = append(args, "--max-num-seqs", fmt.Sprintf("%d", *v.MaxNumSeqs))
	}

	// Swap space
	if v.SwapSpace != nil {
		args = append(args, "--swap-space", fmt.Sprintf("%d", *v.SwapSpace))
	}

	// Trust remote code
	if v.TrustRemoteCode != nil && *v.TrustRemoteCode {
		args = append(args, "--trust-remote-code")
	}

	// === AMD GFX1100 (RDNA3) Optimizations ===

	// Prefix caching for improved performance on repeated prompt prefixes
	if v.EnablePrefixCaching != nil && *v.EnablePrefixCaching {
		args = append(args, "--enable-prefix-caching")
	}

	// KV cache data type
	kvCacheDtype := strings.TrimSpace(v.KVCacheDtype)
	if kvCacheDtype != "" {
		// Backwards-compatible alias: "int8" is not a valid vLLM CLI choice on
		// modern builds; map it to fp8 (fp8_e4m3 on ROCm).
		if strings.EqualFold(kvCacheDtype, "int8") {
			kvCacheDtype = "fp8"
		}

		args = append(args, "--kv-cache-dtype", kvCacheDtype)

		// When using FP8 KV cache on ROCm, enabling dynamic KV scales is commonly
		// required unless the checkpoint embeds calibrated KV-scale tensors.
		// Default to enabled for AMD when KV cache dtype is fp8.
		if r.detectGPUResourceFromSpec(m) == GPUResourceAMD &&
			(kvCacheDtype == "fp8" || kvCacheDtype == "fp8_e4m3" || kvCacheDtype == "fp8_e5m2") {
			if v.CalculateKVScales == nil || *v.CalculateKVScales {
				args = append(args, "--calculate-kv-scales")
			} else {
				args = append(args, "--no-calculate-kv-scales")
			}
		}
	}

	// Attention backend selection
	// Only add if explicitly specified - not all vLLM ROCm builds support --attention-backend
	// Users can set attentionBackend: "TORCH_SDPA" or "ROCM_FLASH" explicitly if needed
	if v.AttentionBackend != "" {
		args = append(args, "--attention-backend", strings.ToUpper(v.AttentionBackend))
	}

	// CPU offload for extending effective cache on memory-constrained GPUs
	if v.CPUOffloadGB != nil && *v.CPUOffloadGB > 0 {
		args = append(args, "--cpu-offload-gb", fmt.Sprintf("%d", *v.CPUOffloadGB))
	}

	// Chunked prefill for better memory efficiency during prompt processing
	if v.EnableChunkedPrefill != nil && *v.EnableChunkedPrefill {
		args = append(args, "--enable-chunked-prefill")
	}

	// Block size for KV cache management
	if v.BlockSize != nil {
		args = append(args, "--block-size", fmt.Sprintf("%d", *v.BlockSize))
	}

	// RoPE scaling for extended context length
	if v.RopeScaling != nil {
		if v.RopeScaling.Type != "" {
			args = append(args, "--rope-scaling-type", v.RopeScaling.Type)
		}
		if v.RopeScaling.Factor != "" {
			args = append(args, "--rope-scaling-factor", v.RopeScaling.Factor)
		}
	}

	return args
}

// buildLlamaCppArgs constructs command-line arguments for llama.cpp server.
func (r *ModelDeploymentReconciler) buildLlamaCppArgs(m *aiv1alpha1.ModelDeployment, modelPath string) []string {
	args := []string{"--model", modelPath, "--host", "0.0.0.0"}

	if m.Spec.LlamaCpp == nil {
		return args
	}

	l := m.Spec.LlamaCpp

	// Context size
	if l.ContextSize != nil {
		args = append(args, "--ctx-size", fmt.Sprintf("%d", *l.ContextSize))
	}

	// GPU layers
	if l.NGPULayers != nil {
		args = append(args, "--n-gpu-layers", fmt.Sprintf("%d", *l.NGPULayers))
	}

	// Batch size
	if l.BatchSize != nil {
		args = append(args, "--batch-size", fmt.Sprintf("%d", *l.BatchSize))
	}

	// Threads
	if l.Threads != nil {
		args = append(args, "--threads", fmt.Sprintf("%d", *l.Threads))
	}

	// Flash attention
	if l.FlashAttention != nil && *l.FlashAttention {
		args = append(args, "--flash-attn")
	}

	// Main GPU
	if l.MainGPU != nil {
		args = append(args, "--main-gpu", fmt.Sprintf("%d", *l.MainGPU))
	}

	// RoPE frequency base
	if l.RopeFreqBase != "" {
		args = append(args, "--rope-freq-base", l.RopeFreqBase)
	}

	// RoPE frequency scale
	if l.RopeFreqScale != "" {
		args = append(args, "--rope-freq-scale", l.RopeFreqScale)
	}

	return args
}

// buildComfyUIArgs constructs command-line arguments for ComfyUI server.
func (r *ModelDeploymentReconciler) buildComfyUIArgs(m *aiv1alpha1.ModelDeployment) []string {
	args := []string{"--listen", "0.0.0.0", "--port", "8188"}

	if m.Spec.ComfyUI == nil {
		return args
	}

	c := m.Spec.ComfyUI

	// Models path: ComfyUI doesn't have --models-dir; use --extra-model-paths-config
	// or mount models directly. If modelsPath is set, create a config file path.
	// For now, we pass it via ExtraArgs if needed (e.g., --extra-model-paths-config /path/to/config.yaml)

	// Custom workflows directory
	if c.WorkflowsPath != "" {
		args = append(args, "--input-directory", c.WorkflowsPath)
	}

	// Enable CORS for browser access
	if c.EnableCORS != nil && *c.EnableCORS {
		args = append(args, "--enable-cors-header")
	}

	if !containsArg(args, "--use-split-cross-attention") && !containsArg(c.ExtraArgs, "--use-split-cross-attention") {
		args = append(args, "--use-split-cross-attention")
	}

	// Extra arguments (escape hatch for additional options)
	if len(c.ExtraArgs) > 0 {
		args = append(args, c.ExtraArgs...)
	}

	return args
}

func containsArg(args []string, target string) bool {
	for _, a := range args {
		if a == target {
			return true
		}
	}
	return false
}

// buildVLLMOmniArgs constructs command-line arguments for vLLM-Omni (diffusion server).
func (r *ModelDeploymentReconciler) buildVLLMOmniArgs(m *aiv1alpha1.ModelDeployment, modelPath string) []string {
	port := r.getBackendPort(m)
	args := []string{"--model", modelPath, "--host", "0.0.0.0", "--port", fmt.Sprintf("%d", port)}

	if m.Spec.VLLMOmni == nil {
		return args
	}

	v := m.Spec.VLLMOmni

	// Diffusion model (for multi-modal setups, e.g., FLUX with LLM controller)
	if v.DiffusionModel != "" {
		args = append(args, "--diffusion-model", v.DiffusionModel)
	}

	// Cache acceleration method (teacache, cachedit, etc.)
	if v.CacheAcceleration != "" {
		args = append(args, "--cache-acceleration", v.CacheAcceleration)
	}

	// Default image size
	if v.DefaultSize != "" {
		args = append(args, "--default-size", v.DefaultSize)
	}

	// GPU memory utilization
	if v.GPUMemoryUtilization != nil {
		args = append(args, "--gpu-memory-utilization", *v.GPUMemoryUtilization)
	}

	// Max sequences for batch processing
	if v.MaxNumSeqs != nil {
		args = append(args, "--max-num-seqs", fmt.Sprintf("%d", *v.MaxNumSeqs))
	}

	return args
}

// validateGPUResources validates that GPU resources are properly configured
func (r *ModelDeploymentReconciler) validateGPUResources(m *aiv1alpha1.ModelDeployment) error {
	// For now, we ensure that GPU resources will be added by our controller
	// In future iterations, we could add more sophisticated validation:
	// - Check if cluster has GPU nodes available
	// - Validate specific GPU types/models
	// - Check resource quotas

	// Basic validation: ensure backend supports GPU workloads
	supportedBackends := map[string]bool{
		"ollama":    true,
		"vllm":      true,
		"tgi":       true, // Text Generation Inference
		"tei":       true, // Text Embeddings Inference
		"llamacpp":  true,
		"mlc-llm":   true,
		"comfyui":   true, // Image generation
		"vllm-omni": true, // Diffusion models
		"diffusers": true, // Diffusers API
	}

	backend := canonicalBackend(m.Spec.Backend)
	if backend == "" {
		backend = "ollama"
	}

	if !supportedBackends[backend] {
		return fmt.Errorf("backend %s is not supported for GPU workloads", m.Spec.Backend)
	}

	return nil
}

// cleanupModelDeployment handles cleanup of all resources when ModelDeployment is deleted
func (r *ModelDeploymentReconciler) cleanupModelDeployment(ctx context.Context, m *aiv1alpha1.ModelDeployment) error {
	log := log.FromContext(ctx)

	// Clean up Deployment
	deployment := &appsv1.Deployment{}
	err := r.Get(ctx, types.NamespacedName{Name: m.Name, Namespace: m.Namespace}, deployment)
	if err == nil {
		log.Info("Cleaning up Deployment", "Deployment.Name", deployment.Name)
		if err := r.Delete(ctx, deployment); err != nil {
			return fmt.Errorf("failed to delete deployment: %w", err)
		}
	} else if !errors.IsNotFound(err) {
		return fmt.Errorf("failed to get deployment for cleanup: %w", err)
	}

	// Clean up Service
	service := &corev1.Service{}
	err = r.Get(ctx, types.NamespacedName{Name: m.Name, Namespace: m.Namespace}, service)
	if err == nil {
		log.Info("Cleaning up Service", "Service.Name", service.Name)
		if err := r.Delete(ctx, service); err != nil {
			return fmt.Errorf("failed to delete service: %w", err)
		}
	} else if !errors.IsNotFound(err) {
		return fmt.Errorf("failed to get service for cleanup: %w", err)
	}

	// Clean up PVC
	pvc := &corev1.PersistentVolumeClaim{}
	err = r.Get(ctx, types.NamespacedName{Name: m.Name, Namespace: m.Namespace}, pvc)
	if err == nil {
		log.Info("Cleaning up PVC", "PVC.Name", pvc.Name)
		if err := r.Delete(ctx, pvc); err != nil {
			return fmt.Errorf("failed to delete pvc: %w", err)
		}
	} else if !errors.IsNotFound(err) {
		return fmt.Errorf("failed to get pvc for cleanup: %w", err)
	}

	// Clean up benchmark Job
	job := &batchv1.Job{}
	jobName := r.benchmarkJobName(m)
	err = r.Get(ctx, types.NamespacedName{Name: jobName, Namespace: m.Namespace}, job)
	if err == nil {
		log.Info("Cleaning up benchmark Job", "Job.Name", job.Name)
		if err := r.Delete(ctx, job); err != nil {
			return fmt.Errorf("failed to delete benchmark job: %w", err)
		}
	} else if !errors.IsNotFound(err) {
		return fmt.Errorf("failed to get benchmark job for cleanup: %w", err)
	}

	// Clean up benchmark ConfigMap
	configMap := &corev1.ConfigMap{}
	configMapName := r.benchmarkConfigMapName(m)
	err = r.Get(ctx, types.NamespacedName{Name: configMapName, Namespace: m.Namespace}, configMap)
	if err == nil {
		log.Info("Cleaning up benchmark ConfigMap", "ConfigMap.Name", configMap.Name)
		if err := r.Delete(ctx, configMap); err != nil {
			return fmt.Errorf("failed to delete benchmark configmap: %w", err)
		}
	} else if !errors.IsNotFound(err) {
		return fmt.Errorf("failed to get benchmark configmap for cleanup: %w", err)
	}

	log.Info("ModelDeployment cleanup completed successfully")
	return nil
}

// containsString checks if a slice contains a string
func containsString(slice []string, s string) bool {
	for _, item := range slice {
		if item == s {
			return true
		}
	}
	return false
}

// removeString removes a string from a slice
func removeString(slice []string, s string) []string {
	var result []string
	for _, item := range slice {
		if item != s {
			result = append(result, item)
		}
	}
	return result
}

// parseTPSFloat converts a TPS string to float64 for display formatting.
// Returns 0.0 if parsing fails.
func parseTPSFloat(tps string) float64 {
	var result float64
	n, err := fmt.Sscanf(tps, "%f", &result)
	if err != nil || n != 1 {
		return 0.0
	}
	return result
}

// updateModelDeploymentStatus updates the overall status of the ModelDeployment
func (r *ModelDeploymentReconciler) updateModelDeploymentStatus(ctx context.Context, m *aiv1alpha1.ModelDeployment, phase aiv1alpha1.ModelDeploymentPhase, message string) error {
	key := client.ObjectKeyFromObject(m)
	return retry.RetryOnConflict(retry.DefaultRetry, func() error {
		// Re-fetch fresh to avoid overwriting proxy's lastAccessTime
		fresh := &aiv1alpha1.ModelDeployment{}
		if err := r.APIReader.Get(ctx, key, fresh); err != nil {
			return fmt.Errorf("failed to get fresh ModelDeployment for phase update: %w", err)
		}

		// Update phase on fresh copy
		fresh.Status.Phase = phase

		// Also update the progressing condition inline (avoid double re-fetch)
		now := metav1.NewTime(time.Now())
		conditionType := aiv1alpha1.ConditionTypeProgressing
		found := false
		for i := range fresh.Status.Conditions {
			if fresh.Status.Conditions[i].Type == conditionType {
				fresh.Status.Conditions[i].Status = metav1.ConditionTrue
				fresh.Status.Conditions[i].Reason = aiv1alpha1.ReasonReconciling
				fresh.Status.Conditions[i].Message = message
				fresh.Status.Conditions[i].LastTransitionTime = now
				found = true
				break
			}
		}
		if !found {
			fresh.Status.Conditions = append(fresh.Status.Conditions, metav1.Condition{
				Type:               conditionType,
				Status:             metav1.ConditionTrue,
				LastTransitionTime: now,
				Reason:             aiv1alpha1.ReasonReconciling,
				Message:            message,
			})
		}

		return r.Status().Update(ctx, fresh)
	})
}

// updateCondition updates a specific condition in the ModelDeployment status
func (r *ModelDeploymentReconciler) updateCondition(ctx context.Context, m *aiv1alpha1.ModelDeployment, conditionType string, status metav1.ConditionStatus, reason, message string) error {
	key := client.ObjectKeyFromObject(m)
	return retry.RetryOnConflict(retry.DefaultRetry, func() error {
		// Re-fetch the latest status to avoid overwriting proxy's lastAccessTime updates
		// This is critical for serverless scale-to-zero: the proxy updates lastAccessTime
		// and if we use a stale copy, we'll overwrite it and cause immediate scale-down.
		fresh := &aiv1alpha1.ModelDeployment{}
		if err := r.APIReader.Get(ctx, key, fresh); err != nil {
			return fmt.Errorf("failed to get fresh ModelDeployment for status update: %w", err)
		}

		// Find existing condition on the FRESH copy
		var existingCondition *metav1.Condition
		for i := range fresh.Status.Conditions {
			if fresh.Status.Conditions[i].Type == conditionType {
				existingCondition = &fresh.Status.Conditions[i]
				break
			}
		}

		now := metav1.NewTime(time.Now())

		if existingCondition != nil {
			// Update existing condition
			if existingCondition.Status != status || existingCondition.Reason != reason || existingCondition.Message != message {
				existingCondition.Status = status
				existingCondition.Reason = reason
				existingCondition.Message = message
				existingCondition.LastTransitionTime = now
			}
		} else {
			// Add new condition
			newCondition := metav1.Condition{
				Type:               conditionType,
				Status:             status,
				LastTransitionTime: now,
				Reason:             reason,
				Message:            message,
			}
			fresh.Status.Conditions = append(fresh.Status.Conditions, newCondition)
		}

		// Update the FRESH object's status (preserves lastAccessTime from server)
		return r.Status().Update(ctx, fresh)
	})
}

// updateEndpointStatus updates the endpoint information in the status
func (r *ModelDeploymentReconciler) updateEndpointStatus(ctx context.Context, m *aiv1alpha1.ModelDeployment, service *corev1.Service) error {
	// Re-fetch fresh to avoid overwriting proxy's lastAccessTime
	fresh := &aiv1alpha1.ModelDeployment{}
	if err := r.APIReader.Get(ctx, client.ObjectKeyFromObject(m), fresh); err != nil {
		return fmt.Errorf("failed to get fresh ModelDeployment for endpoint update: %w", err)
	}

	if fresh.Status.Endpoints == nil {
		fresh.Status.Endpoints = &aiv1alpha1.ModelEndpoints{}
	}

	// Set internal endpoint on fresh copy
	fresh.Status.Endpoints.Internal = k8surl.ServiceAddress(service.Name, service.Namespace, service.Spec.Ports[0].Port, true)

	// Update endpoint ready condition (updateCondition will re-fetch again, but that's ok)
	if err := r.updateCondition(ctx, fresh, aiv1alpha1.ConditionTypeEndpointReady, metav1.ConditionTrue, aiv1alpha1.ReasonServiceReady, "Service endpoint is ready"); err != nil {
		return err
	}

	// Note: updateCondition already saved fresh status, so we don't need another Update
	// But if we need to save endpoints, we should re-fetch and update
	// For now, re-fetch and save with endpoints included
	fresh2 := &aiv1alpha1.ModelDeployment{}
	if err := r.APIReader.Get(ctx, client.ObjectKeyFromObject(m), fresh2); err != nil {
		return fmt.Errorf("failed to get fresh ModelDeployment for endpoint save: %w", err)
	}
	if fresh2.Status.Endpoints == nil {
		fresh2.Status.Endpoints = &aiv1alpha1.ModelEndpoints{}
	}
	fresh2.Status.Endpoints.Internal = fresh.Status.Endpoints.Internal
	return r.Status().Update(ctx, fresh2)
}

// labelsForModelDeployment returns the labels for selecting the resources
// belonging to the given ModelDeployment CR name.
func labelsForModelDeployment(name string) map[string]string {
	return map[string]string{"app": "modeldeployment", "modeldeployment_cr": name}
}

// SetupWithManager sets up the controller with the Manager.
func (r *ModelDeploymentReconciler) SetupWithManager(mgr ctrl.Manager) error {
	// Initialize the event recorder
	r.Recorder = mgr.GetEventRecorderFor("modeldeployment-controller")
	// Initialize the uncached API reader for critical sections
	r.APIReader = mgr.GetAPIReader()

	return ctrl.NewControllerManagedBy(mgr).
		For(&aiv1alpha1.ModelDeployment{}).
		Owns(&appsv1.Deployment{}).
		Owns(&corev1.Service{}).
		Owns(&corev1.PersistentVolumeClaim{}).
		Owns(&batchv1.Job{}).
		// Benchmark results ConfigMaps are created by the benchmark job, not owned by the ModelDeployment.
		// Watch them so we promptly continue reconciliation when results appear.
		Watches(&corev1.ConfigMap{}, handler.EnqueueRequestsFromMapFunc(func(_ context.Context, obj client.Object) []reconcile.Request {
			cm, ok := obj.(*corev1.ConfigMap)
			if !ok {
				return nil
			}
			const suffix = benchmarkconfig.BenchmarkResultsSuffix
			if !strings.HasSuffix(cm.Name, suffix) {
				return nil
			}
			name := strings.TrimSuffix(cm.Name, suffix)
			if name == "" {
				return nil
			}
			return []reconcile.Request{{NamespacedName: types.NamespacedName{Name: name, Namespace: cm.Namespace}}}
		})).
		Complete(r)
}
