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
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/intstr"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log"

	aiv1alpha1 "github.com/flexinfer/flexinfer/api/v1alpha1"
)

// ModelDeploymentReconciler reconciles a ModelDeployment object
type ModelDeploymentReconciler struct {
	client.Client
	Scheme *runtime.Scheme
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
		// If the job is found, we just wait for it to complete. The next reconciliation will handle it.
		log.Info("Benchmark job is still running")
		return ctrl.Result{RequeueAfter: 30 * time.Second}, nil
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
		if err = r.Create(ctx, svc); err != nil {
			log.Error(err, "Failed to create new Service", "Service.Namespace", svc.Namespace, "Service.Name", svc.Name)
			return ctrl.Result{}, err
		}
		// Service created successfully - return and requeue
		return ctrl.Result{Requeue: true}, nil
	} else if err != nil {
		log.Error(err, "Failed to get Service")
		return ctrl.Result{}, err
	}

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
					Containers: []corev1.Container{{
						Image: r.getBackendImage(),
						Name:  "llm-backend",
						Ports: []corev1.ContainerPort{{
							ContainerPort: 11434,
							Name:          "http",
						}},
						Resources: m.Spec.Resources,
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

// jobForBenchmark returns a benchmark Job object
func (r *ModelDeploymentReconciler) jobForBenchmark(m *aiv1alpha1.ModelDeployment) *batchv1.Job {
	job := &batchv1.Job{
		ObjectMeta: metav1.ObjectMeta{
			Name:      r.benchmarkJobName(m),
			Namespace: m.Namespace,
		},
		Spec: batchv1.JobSpec{
			Template: corev1.PodTemplateSpec{
				Spec: corev1.PodSpec{
					Containers: []corev1.Container{{
						Image: "flexinfer-bench:latest", // This will be built locally
						Name:  "flexinfer-bench",
						Args: []string{
							"--model", m.Spec.Model,
							"--configmap", r.benchmarkConfigMapName(m),
						},
					}},
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

// labelsForModelDeployment returns the labels for selecting the resources
// belonging to the given ModelDeployment CR name.
func labelsForModelDeployment(name string) map[string]string {
	return map[string]string{"app": "modeldeployment", "modeldeployment_cr": name}
}

// SetupWithManager sets up the controller with the Manager.
func (r *ModelDeploymentReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&aiv1alpha1.ModelDeployment{}).
		Owns(&appsv1.Deployment{}).
		Owns(&corev1.Service{}).
		Owns(&corev1.PersistentVolumeClaim{}).
		Owns(&batchv1.Job{}).
		Complete(r)
}
