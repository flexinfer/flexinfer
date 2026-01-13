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
	"strings"
	"time"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
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
	if model.ObjectMeta.DeletionTimestamp.IsZero() {
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

	// Initialize status
	if model.Status.Phase == "" {
		if err := r.updatePhase(ctx, model, aiv1alpha2.ModelPhasePending); err != nil {
			log.Error(err, "Failed to update initial status")
			return ctrl.Result{}, err
		}
	}

	// Validate backend
	b, ok := backend.Get(model.Spec.Backend)
	if !ok {
		err := fmt.Errorf("unknown backend: %s", model.Spec.Backend)
		log.Error(err, "Backend validation failed")
		r.Recorder.Event(model, corev1.EventTypeWarning, "ValidationFailed", err.Error())
		return ctrl.Result{}, r.updatePhase(ctx, model, aiv1alpha2.ModelPhaseFailed)
	}

	// Check if model should be scaled to zero (serverless idle)
	if model.Spec.IsServerless() && model.Status.Phase == aiv1alpha2.ModelPhaseReady {
		if shouldScaleToZero(model) {
			log.Info("Scaling model to zero due to idle timeout")
			if err := r.scaleToZero(ctx, model); err != nil {
				log.Error(err, "Failed to scale to zero")
				return ctrl.Result{}, err
			}
			return ctrl.Result{}, nil
		}
	}

	// Handle shared GPU scheduling
	if model.Spec.IsShared() {
		result, err := r.handleSharedGPU(ctx, model)
		if err != nil {
			log.Error(err, "Failed to handle shared GPU scheduling")
			return result, err
		}
		// If we're preempted or queued, don't proceed with deployment
		if model.Status.Phase == aiv1alpha2.ModelPhasePreempted ||
			model.Status.SharedGroup != nil && model.Status.SharedGroup.State == "Queued" {
			return result, nil
		}
	}

	// Detect GPU info from node
	gpuVendor, gpuArch, err := r.detectGPU(ctx, model)
	if err != nil {
		log.Error(err, "Failed to detect GPU")
		r.Recorder.Event(model, corev1.EventTypeWarning, "GPUDetectionFailed", err.Error())
		// Continue with defaults
		gpuVendor = backend.GPUVendorNVIDIA
		gpuArch = ""
	}

	// Ensure Service exists
	if err := r.ensureService(ctx, model, b); err != nil {
		log.Error(err, "Failed to ensure Service")
		return ctrl.Result{}, err
	}

	// Ensure Deployment exists with correct spec
	if err := r.ensureDeployment(ctx, model, b, gpuVendor, gpuArch); err != nil {
		log.Error(err, "Failed to ensure Deployment")
		return ctrl.Result{}, err
	}

	// Update status based on deployment state
	if err := r.updateStatusFromDeployment(ctx, model); err != nil {
		log.Error(err, "Failed to update status")
		return ctrl.Result{}, err
	}

	// Requeue to check idle timeout for serverless
	if model.Spec.IsServerless() && model.Status.Phase == aiv1alpha2.ModelPhaseReady {
		idleTimeout := getIdleTimeout(model, b)
		return ctrl.Result{RequeueAfter: idleTimeout / 2}, nil
	}

	return ctrl.Result{RequeueAfter: 30 * time.Second}, nil
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
	if model.Spec.LiteLLM != nil && (model.Spec.LiteLLM.Enabled == nil || *model.Spec.LiteLLM.Enabled) {
		servedModel := model.Name
		if model.Spec.LiteLLM.ServedModelName != "" {
			servedModel = model.Spec.LiteLLM.ServedModelName
		}
		annotations["litellm.flexinfer.ai/served-model"] = servedModel
		if len(model.Spec.LiteLLM.Aliases) > 0 {
			annotations["litellm.flexinfer.ai/aliases"] = strings.Join(model.Spec.LiteLLM.Aliases, ",")
		}
		if model.Spec.LiteLLM.CopilotAlias != "" {
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
			Selector: r.labelsForModel(model),
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

	// Update if needed
	service.Spec = desiredService.Spec
	service.Annotations = annotations
	return r.Update(ctx, service)
}

// ensureDeployment creates or updates the Deployment for the model.
func (r *ModelReconciler) ensureDeployment(ctx context.Context, model *aiv1alpha2.Model, b backend.Backend, gpuVendor backend.GPUVendor, gpuArch string) error {
	log := log.FromContext(ctx)

	deployment := &appsv1.Deployment{}
	err := r.Get(ctx, types.NamespacedName{Name: model.Name, Namespace: model.Namespace}, deployment)
	if err != nil && !errors.IsNotFound(err) {
		return err
	}

	// Build ModelSpec for backend
	spec := r.buildBackendModelSpec(model, gpuVendor)

	// Get container configuration from backend
	image := b.Image(gpuVendor, gpuArch)
	port := b.Port()
	command := b.Command()
	args := b.Args(spec)
	env := b.Env(spec)
	probe := b.ReadinessProbe()

	// Determine replicas (0 for serverless idle, 1 otherwise)
	replicas := int32(1)
	if model.Status.Phase == aiv1alpha2.ModelPhaseIdle {
		replicas = 0
	}

	// Build resource requirements
	resources := model.Spec.Resources
	if resources.Limits == nil {
		resources.Limits = corev1.ResourceList{}
	}
	gpuCount := model.Spec.GetGPUCount()
	resources.Limits[gpuVendor.ResourceName()] = *resource.NewQuantity(int64(gpuCount), resource.DecimalSI)

	// Build node selector
	nodeSelector := model.Spec.NodeSelector
	if nodeSelector == nil {
		nodeSelector = make(map[string]string)
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
		container.VolumeMounts = append(container.VolumeMounts, corev1.VolumeMount{
			Name:      "model",
			MountPath: "/models",
		})

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
			OwnerReferences: []metav1.OwnerReference{
				*metav1.NewControllerRef(model, aiv1alpha2.GroupVersion.WithKind("Model")),
			},
		},
		Spec: appsv1.DeploymentSpec{
			Replicas: ptr.To(replicas),
			Selector: &metav1.LabelSelector{
				MatchLabels: r.labelsForModel(model),
			},
			Template: corev1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{
					Labels: r.labelsForModel(model),
				},
				Spec: corev1.PodSpec{
					NodeSelector:       nodeSelector,
					Containers:         []corev1.Container{container},
					Volumes:            volumes,
					RestartPolicy:      corev1.RestartPolicyAlways,
					ServiceAccountName: "default",
				},
			},
		},
	}

	if errors.IsNotFound(err) {
		log.Info("Creating Deployment", "name", model.Name, "replicas", replicas)
		return r.Create(ctx, desiredDeployment)
	}

	// Update deployment
	deployment.Spec = desiredDeployment.Spec
	return r.Update(ctx, deployment)
}

// buildBackendModelSpec converts Model spec to backend.ModelSpec.
func (r *ModelReconciler) buildBackendModelSpec(model *aiv1alpha2.Model, gpuVendor backend.GPUVendor) *backend.ModelSpec {
	spec := &backend.ModelSpec{
		Model:     extractModelFromSource(model.Spec.Source),
		ModelPath: "/models",
		GPUVendor: gpuVendor,
	}

	// Parse config into the spec
	if model.Spec.Config != nil {
		spec.Config = model.Spec.GetConfigMap()
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

			// Scale to zero
			if err := r.scaleToZero(ctx, model); err != nil {
				return ctrl.Result{}, err
			}
		}
	}

	if err := r.Status().Update(ctx, model); err != nil {
		return ctrl.Result{}, err
	}

	return ctrl.Result{RequeueAfter: 10 * time.Second}, nil
}

// scaleToZero scales the deployment to zero replicas.
func (r *ModelReconciler) scaleToZero(ctx context.Context, model *aiv1alpha2.Model) error {
	log := log.FromContext(ctx)

	deployment := &appsv1.Deployment{}
	if err := r.Get(ctx, types.NamespacedName{Name: model.Name, Namespace: model.Namespace}, deployment); err != nil {
		if errors.IsNotFound(err) {
			return nil
		}
		return err
	}

	if *deployment.Spec.Replicas > 0 {
		log.Info("Scaling deployment to zero", "name", model.Name)
		deployment.Spec.Replicas = ptr.To(int32(0))
		if err := r.Update(ctx, deployment); err != nil {
			return err
		}
	}

	model.Status.Phase = aiv1alpha2.ModelPhaseIdle
	return r.Status().Update(ctx, model)
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

	// Determine phase from deployment status
	if deployment.Status.ReadyReplicas > 0 {
		model.Status.Phase = aiv1alpha2.ModelPhaseReady
	} else if *deployment.Spec.Replicas == 0 {
		if model.Status.Phase != aiv1alpha2.ModelPhasePreempted {
			model.Status.Phase = aiv1alpha2.ModelPhaseIdle
		}
	} else if deployment.Status.UnavailableReplicas > 0 {
		model.Status.Phase = aiv1alpha2.ModelPhaseLoading
	} else {
		model.Status.Phase = aiv1alpha2.ModelPhasePending
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
	// If node selector is specified, check those nodes
	nodeList := &corev1.NodeList{}
	if err := r.List(ctx, nodeList); err != nil {
		return backend.GPUVendorNVIDIA, "", err
	}

	for _, node := range nodeList.Items {
		// Check for NVIDIA GPUs
		if _, ok := node.Status.Capacity["nvidia.com/gpu"]; ok {
			arch := node.Labels["nvidia.com/gpu.compute.major"]
			return backend.GPUVendorNVIDIA, "sm_" + arch, nil
		}
		// Check for AMD GPUs
		if _, ok := node.Status.Capacity["amd.com/gpu"]; ok {
			arch := node.Labels["gpu.amd.com/gpu-architecture"]
			return backend.GPUVendorAMD, arch, nil
		}
	}

	// Default to NVIDIA if no GPU detected
	return backend.GPUVendorNVIDIA, "", nil
}

// labelsForModel returns the labels to apply to resources for this model.
func (r *ModelReconciler) labelsForModel(model *aiv1alpha2.Model) map[string]string {
	labels := map[string]string{
		"app.kubernetes.io/name":       "model",
		"app.kubernetes.io/instance":   model.Name,
		"app.kubernetes.io/managed-by": "flexinfer",
		"flexinfer.ai/model":           model.Name,
		"flexinfer.ai/backend":         model.Spec.Backend,
	}

	if model.Spec.GPU != nil && model.Spec.GPU.Shared != "" {
		labels["flexinfer.ai/gpu-group"] = model.Spec.GPU.Shared
	}

	return labels
}

// shouldScaleToZero checks if the model should be scaled to zero.
func shouldScaleToZero(model *aiv1alpha2.Model) bool {
	if model.Status.LastActiveTime == nil {
		return false
	}

	b, ok := backend.Get(model.Spec.Backend)
	if !ok {
		return false
	}

	idleTimeout := getIdleTimeout(model, b)
	return time.Since(model.Status.LastActiveTime.Time) > idleTimeout
}

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
		Complete(r)
}
