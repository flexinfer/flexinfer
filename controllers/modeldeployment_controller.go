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
	"time"

	appsv1 "k8s.io/api/apps/v1"
	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/intstr"
	"k8s.io/client-go/tools/record"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log"

	aiv1alpha1 "github.com/flexinfer/flexinfer/api/v1alpha1"
)

// ModelDeploymentReconciler reconciles a ModelDeployment object
type ModelDeploymentReconciler struct {
	client.Client
	Scheme   *runtime.Scheme
	Recorder record.EventRecorder
}

//+kubebuilder:rbac:groups=ai.flexinfer,resources=modeldeployments,verbs=get;list;watch;create;update;patch;delete
//+kubebuilder:rbac:groups=ai.flexinfer,resources=modeldeployments/status,verbs=get;update;patch
//+kubebuilder:rbac:groups=ai.flexinfer,resources=modeldeployments/finalizers,verbs=update
//+kubebuilder:rbac:groups=apps,resources=deployments,verbs=get;list;watch;create;update;patch;delete
//+kubebuilder:rbac:groups=core,resources=persistentvolumeclaims,verbs=get;list;watch;create;update;patch;delete
//+kubebuilder:rbac:groups=core,resources=services,verbs=get;list;watch;create;update;patch;delete
//+kubebuilder:rbac:groups=batch,resources=jobs,verbs=get;list;watch;create;update;patch;delete
//+kubebuilder:rbac:groups=core,resources=configmaps,verbs=get;list;watch;create;update;patch;delete

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
	if modelDeployment.ObjectMeta.DeletionTimestamp.IsZero() {
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

	// Initialize status if needed
	if err := r.updateModelDeploymentStatus(ctx, modelDeployment, aiv1alpha1.ModelDeploymentPhasePending, "Initializing ModelDeployment"); err != nil {
		log.Error(err, "Failed to update initial status")
		return ctrl.Result{}, err
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

	// Update condition for GPU validation success
	if err := r.updateCondition(ctx, modelDeployment, aiv1alpha1.ConditionTypeGPUAllocated, metav1.ConditionTrue, aiv1alpha1.ReasonGPUAllocated, "GPU resources validated and will be allocated"); err != nil {
		log.Error(err, "Failed to update GPU condition")
		return ctrl.Result{}, err
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
			// If the Job is not found, create it
			job := r.jobForBenchmark(modelDeployment)
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
			log.Info("Benchmark job completed successfully")
		} else {
			// If the job is still running, requeue the request.
			log.Info("Benchmark job is still running")
			return ctrl.Result{RequeueAfter: 30 * time.Second}, nil
		}
	} else if err != nil {
		log.Error(err, "Failed to get Benchmark ConfigMap")
		return ctrl.Result{}, err
	}

	// Check if the pvc already exists, if not create a new one
	pvc := &corev1.PersistentVolumeClaim{}
	err = r.Get(ctx, types.NamespacedName{Name: modelDeployment.Name, Namespace: modelDeployment.Namespace}, pvc)
	if err != nil && errors.IsNotFound(err) {
		// Define a new pvc
		pvc := r.pvcForModelDeployment(modelDeployment)
		log.Info("Creating a new Pvc", "Pvc.Namespace", pvc.Namespace, "Pvc.Name", pvc.Name)
		if err = r.Create(ctx, pvc); err != nil {
			log.Error(err, "Failed to create new Pvc", "Pvc.Namespace", pvc.Namespace, "Pvc.Name", pvc.Name)
			return ctrl.Result{}, err
		}
		// Pvc created successfully - return and requeue
		return ctrl.Result{Requeue: true}, nil
	} else if err != nil {
		log.Error(err, "Failed to get Pvc")
		return ctrl.Result{}, err
	}

	// Check if the deployment already exists, if not create a new one
	found := &appsv1.Deployment{}
	err = r.Get(ctx, types.NamespacedName{Name: modelDeployment.Name, Namespace: modelDeployment.Namespace}, found)
	if err != nil && errors.IsNotFound(err) {
		// Define a new deployment
		dep := r.deploymentForModelDeployment(modelDeployment)
		log.Info("Creating a new Deployment", "Deployment.Namespace", dep.Namespace, "Deployment.Name", dep.Name)
		if err = r.Create(ctx, dep); err != nil {
			log.Error(err, "Failed to create new Deployment", "Deployment.Namespace", dep.Namespace, "Deployment.Name", dep.Name)
			return ctrl.Result{}, err
		}
		// Deployment created successfully - return and requeue
		return ctrl.Result{Requeue: true}, nil
	} else if err != nil {
		log.Error(err, "Failed to get Deployment")
		return ctrl.Result{}, err
	}

	// Ensure the deployment size is the same as the spec
	size := modelDeployment.Spec.Replicas
	if *found.Spec.Replicas != *size {
		found.Spec.Replicas = size
		if err = r.Update(ctx, found); err != nil {
			log.Error(err, "Failed to update Deployment", "Deployment.Namespace", found.Namespace, "Deployment.Name", found.Name)
			return ctrl.Result{}, err
		}
		// Spec updated - return and requeue
		return ctrl.Result{Requeue: true}, nil
	}

	// Check if the service already exists, if not create a new one
	service := &corev1.Service{}
	err = r.Get(ctx, types.NamespacedName{Name: modelDeployment.Name, Namespace: modelDeployment.Namespace}, service)
	if err != nil && errors.IsNotFound(err) {
		// Define a new service
		svc := r.serviceForModelDeployment(modelDeployment)
		log.Info("Creating a new Service", "Service.Namespace", svc.Namespace, "Service.Name", svc.Name)
		r.Recorder.Event(modelDeployment, corev1.EventTypeNormal, "ServiceCreating", "Creating service for ModelDeployment")
		if err = r.Create(ctx, svc); err != nil {
			log.Error(err, "Failed to create new Service", "Service.Namespace", svc.Namespace, "Service.Name", svc.Name)
			r.Recorder.Event(modelDeployment, corev1.EventTypeWarning, "ServiceCreateFailed", fmt.Sprintf("Failed to create service: %v", err))
			return ctrl.Result{}, err
		}
		r.Recorder.Event(modelDeployment, corev1.EventTypeNormal, "ServiceCreated", "Service created successfully")
		// Service created successfully - return and requeue
		return ctrl.Result{Requeue: true}, nil
	} else if err != nil {
		log.Error(err, "Failed to get Service")
		r.Recorder.Event(modelDeployment, corev1.EventTypeWarning, "ServiceGetFailed", fmt.Sprintf("Failed to get service: %v", err))
		return ctrl.Result{}, err
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

	// Update Ready condition
	if err := r.updateCondition(ctx, modelDeployment, aiv1alpha1.ConditionTypeReady, metav1.ConditionTrue, aiv1alpha1.ReasonDeploymentReady, "All resources are ready and healthy"); err != nil {
		log.Error(err, "Failed to update Ready condition")
		return ctrl.Result{}, err
	}

	r.Recorder.Event(modelDeployment, corev1.EventTypeNormal, "ReconcileComplete", "ModelDeployment reconciliation completed successfully")
	return ctrl.Result{}, nil
}

// deploymentForModelDeployment returns a ModelDeployment Deployment object
func (r *ModelDeploymentReconciler) deploymentForModelDeployment(m *aiv1alpha1.ModelDeployment) *appsv1.Deployment {
	ls := labelsForModelDeployment(m.Name)
	replicas := m.Spec.Replicas

	dep := &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{
			Name:      m.Name,
			Namespace: m.Namespace,
		},
		Spec: appsv1.DeploymentSpec{
			Replicas: replicas,
			Selector: &metav1.LabelSelector{
				MatchLabels: ls,
			},
			Template: corev1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{
					Labels: ls,
				},
				Spec: corev1.PodSpec{
					// Use our custom scheduler
					SchedulerName: "flexinfer-scheduler",
					NodeSelector:  r.getNodeSelector(m),
					Containers: []corev1.Container{{
						Image: r.getBackendImage(),
						Name:  "llm-backend",
						Ports: []corev1.ContainerPort{{
							ContainerPort: 11434,
							Name:          "http",
						}},
						Resources: r.getResourceRequirements(m),
						VolumeMounts: []corev1.VolumeMount{{
							Name:      "model-cache",
							MountPath: "/models",
						}},
					}},
					Volumes: []corev1.Volume{{
						Name: "model-cache",
						VolumeSource: corev1.VolumeSource{
							PersistentVolumeClaim: &corev1.PersistentVolumeClaimVolumeSource{
								ClaimName: m.Name,
							},
						},
					}},
				},
			},
		},
	}
	// Set ModelDeployment instance as the owner and controller
	ctrl.SetControllerReference(m, dep, r.Scheme)
	return dep
}

// serviceForModelDeployment returns a ModelDeployment Service object
func (r *ModelDeploymentReconciler) serviceForModelDeployment(m *aiv1alpha1.ModelDeployment) *corev1.Service {
	ls := labelsForModelDeployment(m.Name)

	svc := &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{
			Name:      m.Name,
			Namespace: m.Namespace,
		},
		Spec: corev1.ServiceSpec{
			Selector: ls,
			Ports: []corev1.ServicePort{{
				Port:       11434,
				TargetPort: intstr.FromString("http"),
				Name:       "http",
			}},
		},
	}
	// Set ModelDeployment instance as the owner and controller
	ctrl.SetControllerReference(m, svc, r.Scheme)
	return svc
}

// pvcForModelDeployment returns a ModelDeployment Pvc object
func (r *ModelDeploymentReconciler) pvcForModelDeployment(m *aiv1alpha1.ModelDeployment) *corev1.PersistentVolumeClaim {
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
	ctrl.SetControllerReference(m, pvc, r.Scheme)
	return pvc
}

// getBenchmarkerImage returns the benchmarker image from the environment variable or a default.
func (r *ModelDeploymentReconciler) getBenchmarkerImage() string {
	if image, ok := os.LookupEnv("BENCHMARKER_IMAGE"); ok {
		return image
	}
	return "flexinfer-bench:latest"
}

// jobForBenchmark returns a benchmark Job object
func (r *ModelDeploymentReconciler) jobForBenchmark(m *aiv1alpha1.ModelDeployment) *batchv1.Job {
	// Standardize sidecar configuration
	backendImage := r.getBackendImage()
	benchmarkerImage := r.getBenchmarkerImage()
	backendPort := int32(11434)

	job := &batchv1.Job{
		ObjectMeta: metav1.ObjectMeta{
			Name:      r.benchmarkJobName(m),
			Namespace: m.Namespace,
		},
		Spec: batchv1.JobSpec{
			Template: corev1.PodTemplateSpec{
				Spec: corev1.PodSpec{
					// Ensure the job pod requests a GPU so it lands on a GPU node for accurate benchmarking
					// Benchmark jobs bypass the custom scheduler to run on any suitable node initially
					// or we can use the custom scheduler but ensure they don't get filtered out.
					// For now, let default scheduler handle benchmark jobs to avoid circular dependencies.
					NodeSelector: r.getNodeSelector(m),
					Containers: []corev1.Container{
						{
							Name:  "flexinfer-bench",
							Image: benchmarkerImage,
							Env: []corev1.EnvVar{
								{
									Name:  "BACKEND_URL",
									Value: fmt.Sprintf("http://localhost:%d", backendPort),
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
							Args: []string{
								"--model", m.Spec.Model,
								"--configmap", r.benchmarkConfigMapName(m),
							},
							Resources: corev1.ResourceRequirements{
								Requests: corev1.ResourceList{
									corev1.ResourceCPU:    resource.MustParse("100m"),
									corev1.ResourceMemory: resource.MustParse("128Mi"),
								},
							},
						},
						{
							Name:  "llm-backend",
							Image: backendImage,
							Ports: []corev1.ContainerPort{{
								ContainerPort: backendPort,
								Name:          "http",
							}},
							// IMPORTANT: The backend in the benchmark job MUST request the GPU
							// to actually be able to run and measure performance.
							Resources: r.getResourceRequirements(m),
							Env: []corev1.EnvVar{
								{
									Name:  "OLLAMA_HOST",
									Value: "0.0.0.0",
								},
							},
						},
					},
					RestartPolicy: corev1.RestartPolicyNever,
				},
			},
		},
	}
	ctrl.SetControllerReference(m, job, r.Scheme)
	return job
}

func (r *ModelDeploymentReconciler) benchmarkJobName(m *aiv1alpha1.ModelDeployment) string {
	return fmt.Sprintf("%s-benchmark", m.Name)
}

func (r *ModelDeploymentReconciler) benchmarkConfigMapName(m *aiv1alpha1.ModelDeployment) string {
	return fmt.Sprintf("%s-benchmark-results", m.Name)
}

// getBackendImage returns the backend image from the environment variable or a default.
func (r *ModelDeploymentReconciler) getBackendImage() string {
	if image, ok := os.LookupEnv("DEFAULT_BACKEND_IMAGE"); ok {
		return image
	}
	return "ghcr.io/flexinfer/ollama:latest"
}

// getNodeSelector returns the node selector for GPU nodes
func (r *ModelDeploymentReconciler) getNodeSelector(m *aiv1alpha1.ModelDeployment) map[string]string {
	// Ensure GPU workloads are scheduled only on nodes with GPUs
	return map[string]string{
		"flexinfer.ai/gpu-present": "true",
	}
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

	// CRITICAL FIX: Add GPU resource requests
	// Ensure all ModelDeployment pods request at least 1 GPU
	// This prevents pods from being scheduled on non-GPU nodes
	requirements.Requests["nvidia.com/gpu"] = *resource.NewQuantity(1, resource.DecimalSI)
	requirements.Limits["nvidia.com/gpu"] = *resource.NewQuantity(1, resource.DecimalSI)

	return requirements
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
		"ollama": true,
		"vllm":   true,
		"tgi":    true, // Text Generation Inference
	}

	if !supportedBackends[m.Spec.Backend] {
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

// updateModelDeploymentStatus updates the overall status of the ModelDeployment
func (r *ModelDeploymentReconciler) updateModelDeploymentStatus(ctx context.Context, m *aiv1alpha1.ModelDeployment, phase aiv1alpha1.ModelDeploymentPhase, message string) error {
	// Update the original object so tests can verify changes
	m.Status.Phase = phase

	// Update the progressing condition
	return r.updateCondition(ctx, m, aiv1alpha1.ConditionTypeProgressing, metav1.ConditionTrue, aiv1alpha1.ReasonReconciling, message)
}

// updateCondition updates a specific condition in the ModelDeployment status
func (r *ModelDeploymentReconciler) updateCondition(ctx context.Context, m *aiv1alpha1.ModelDeployment, conditionType string, status metav1.ConditionStatus, reason, message string) error {
	// Find existing condition
	var existingCondition *metav1.Condition
	for i := range m.Status.Conditions {
		if m.Status.Conditions[i].Type == conditionType {
			existingCondition = &m.Status.Conditions[i]
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
		m.Status.Conditions = append(m.Status.Conditions, newCondition)
	}

	// Update the status
	return r.Status().Update(ctx, m)
}

// updateEndpointStatus updates the endpoint information in the status
func (r *ModelDeploymentReconciler) updateEndpointStatus(ctx context.Context, m *aiv1alpha1.ModelDeployment, service *corev1.Service) error {
	// Update the original object so tests can verify changes
	if m.Status.Endpoints == nil {
		m.Status.Endpoints = &aiv1alpha1.ModelEndpoints{}
	}

	// Set internal endpoint
	m.Status.Endpoints.Internal = fmt.Sprintf("%s.%s.svc.cluster.local:%d",
		service.Name, service.Namespace, service.Spec.Ports[0].Port)

	// Update endpoint ready condition
	if err := r.updateCondition(ctx, m, aiv1alpha1.ConditionTypeEndpointReady, metav1.ConditionTrue, aiv1alpha1.ReasonServiceReady, "Service endpoint is ready"); err != nil {
		return err
	}

	return r.Status().Update(ctx, m)
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

	return ctrl.NewControllerManagedBy(mgr).
		For(&aiv1alpha1.ModelDeployment{}).
		Owns(&appsv1.Deployment{}).
		Owns(&corev1.Service{}).
		Owns(&corev1.PersistentVolumeClaim{}).
		Owns(&batchv1.Job{}).
		Complete(r)
}
