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

	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/tools/record"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log"

	aiv1alpha1 "github.com/flexinfer/flexinfer/api/v1alpha1"
)

// ModelCacheReconciler reconciles a ModelCache object
type ModelCacheReconciler struct {
	client.Client
	Scheme   *runtime.Scheme
	Recorder record.EventRecorder
}

//+kubebuilder:rbac:groups=ai.flexinfer,resources=modelcaches,verbs=get;list;watch;create;update;patch;delete
//+kubebuilder:rbac:groups=ai.flexinfer,resources=modelcaches/status,verbs=get;update;patch
//+kubebuilder:rbac:groups=ai.flexinfer,resources=modelcaches/finalizers,verbs=update
//+kubebuilder:rbac:groups=core,resources=persistentvolumeclaims,verbs=get;list;watch;create;update;patch;delete
//+kubebuilder:rbac:groups=batch,resources=jobs,verbs=get;list;watch;create;update;patch;delete

// Reconcile is part of the main kubernetes reconciliation loop
func (r *ModelCacheReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	log := log.FromContext(ctx)

	// Fetch the ModelCache instance
	modelCache := &aiv1alpha1.ModelCache{}
	err := r.Get(ctx, req.NamespacedName, modelCache)
	if err != nil {
		if errors.IsNotFound(err) {
			return ctrl.Result{}, nil
		}
		return ctrl.Result{}, err
	}

	// Initialize status
	if modelCache.Status.Phase == "" {
		modelCache.Status.Phase = aiv1alpha1.ModelCachePhasePending
		if err := r.Status().Update(ctx, modelCache); err != nil {
			return ctrl.Result{}, err
		}
	}

	// Determine strategy
	strategy := modelCache.Spec.StorageStrategy
	if strategy == aiv1alpha1.StorageStrategyAuto {
		// Default to SharedPVC for this implementation
		strategy = aiv1alpha1.StorageStrategySharedPVC
	}

	if strategy == aiv1alpha1.StorageStrategySharedPVC {
		return r.reconcileSharedPVC(ctx, modelCache)
	}

	// TODO: Implement NodeLocal
	log.Info("Strategy not implemented yet", "strategy", strategy)
	return ctrl.Result{}, nil
}

func (r *ModelCacheReconciler) reconcileSharedPVC(ctx context.Context, modelCache *aiv1alpha1.ModelCache) (ctrl.Result, error) {
	log := log.FromContext(ctx)

	// Determine PVC name - either existing or create new
	var pvcName string
	var pvcNamespace string = modelCache.Namespace

	if modelCache.Spec.ExistingClaimName != nil && *modelCache.Spec.ExistingClaimName != "" {
		// Use existing PVC - may be in a different namespace, parse if needed
		pvcName = *modelCache.Spec.ExistingClaimName
		log.Info("Using existing PVC", "pvcName", pvcName)
	} else {
		// Create new PVC
		pvcName = modelCache.Name
		pvc := &corev1.PersistentVolumeClaim{}
		err := r.Get(ctx, types.NamespacedName{Name: pvcName, Namespace: pvcNamespace}, pvc)
		if err != nil && errors.IsNotFound(err) {
			newPVC, err := r.pvcForModelCache(modelCache)
			if err != nil {
				return ctrl.Result{}, err
			}
			log.Info("Creating PVC for ModelCache", "PVC", newPVC.Name)
			if err := r.Create(ctx, newPVC); err != nil {
				return ctrl.Result{}, err
			}
			return ctrl.Result{Requeue: true}, nil
		} else if err != nil {
			return ctrl.Result{}, err
		}
	}

	// Determine model path within the PVC
	modelPath := modelCache.Name
	if modelCache.Spec.ModelPath != nil && *modelCache.Spec.ModelPath != "" {
		modelPath = *modelCache.Spec.ModelPath
	}

	// 2. Check if data is populated via Downloader Job
	jobName := modelCache.Name + "-downloader"
	job := &batchv1.Job{}
	err := r.Get(ctx, types.NamespacedName{Name: jobName, Namespace: modelCache.Namespace}, job)
	if err != nil && errors.IsNotFound(err) {
		// Create Downloader Job - use OCI job for OCI sources
		var newJob *batchv1.Job
		var jobErr error
		if isOCISource(modelCache.Spec.Source) {
			newJob, jobErr = r.jobForOCIDownload(modelCache, pvcName, modelPath)
		} else {
			newJob, jobErr = r.jobForDownload(modelCache, pvcName, modelPath)
		}
		if jobErr != nil {
			return ctrl.Result{}, jobErr
		}
		log.Info("Creating Downloader Job", "Job", newJob.Name, "modelPath", modelPath, "isOCI", isOCISource(modelCache.Spec.Source))
		if err := r.Create(ctx, newJob); err != nil {
			return ctrl.Result{}, err
		}

		// Update status to Provisioning
		modelCache.Status.Phase = aiv1alpha1.ModelCachePhaseProvisioning
		if err := r.Status().Update(ctx, modelCache); err != nil {
			return ctrl.Result{}, err
		}
		return ctrl.Result{Requeue: true}, nil
	} else if err != nil {
		return ctrl.Result{}, err
	}

	// 3. Check Job Status
	if job.Status.Succeeded > 0 {
		if modelCache.Status.Phase != aiv1alpha1.ModelCachePhaseReady {
			modelCache.Status.Phase = aiv1alpha1.ModelCachePhaseReady
			// Path includes both PVC name and model subdirectory
			modelCache.Status.Path = fmt.Sprintf("%s:%s", pvcName, modelPath)
			if err := r.Status().Update(ctx, modelCache); err != nil {
				return ctrl.Result{}, err
			}
			log.Info("ModelCache is Ready", "path", modelCache.Status.Path)
		}
	} else if job.Status.Failed > 0 {
		modelCache.Status.Phase = aiv1alpha1.ModelCachePhaseFailed
		if err := r.Status().Update(ctx, modelCache); err != nil {
			return ctrl.Result{}, err
		}
	}

	return ctrl.Result{}, nil
}

func (r *ModelCacheReconciler) pvcForModelCache(m *aiv1alpha1.ModelCache) (*corev1.PersistentVolumeClaim, error) {
	// Use ReadWriteMany for shared access across nodes
	modes := []corev1.PersistentVolumeAccessMode{corev1.ReadWriteMany}

	// Use configured storage size or default to 50Gi
	storageSize := "50Gi"
	if m.Spec.StorageSize != nil && *m.Spec.StorageSize != "" {
		storageSize = *m.Spec.StorageSize
	}

	pvc := &corev1.PersistentVolumeClaim{
		ObjectMeta: metav1.ObjectMeta{
			Name:      m.Name,
			Namespace: m.Namespace,
		},
		Spec: corev1.PersistentVolumeClaimSpec{
			AccessModes: modes,
			Resources: corev1.ResourceRequirements{
				Requests: corev1.ResourceList{
					corev1.ResourceStorage: resource.MustParse(storageSize),
				},
			},
			StorageClassName: m.Spec.ClusterStorageClassName,
		},
	}
	if err := ctrl.SetControllerReference(m, pvc, r.Scheme); err != nil {
		return nil, err
	}
	return pvc, nil
}

// isMlcModel returns true if the source is an MLC-LLM compiled model
func isMlcModel(source string) bool {
	return strings.HasPrefix(source, "mlc://") ||
		strings.HasPrefix(source, "HF://mlc-ai/") ||
		strings.Contains(source, "-MLC")
}

// parseModelSource extracts the model ID from various source formats
func parseModelSource(source string) string {
	// Remove common prefixes
	source = strings.TrimPrefix(source, "huggingface://")
	source = strings.TrimPrefix(source, "mlc://")
	source = strings.TrimPrefix(source, "HF://")
	return source
}

// isOCISource returns true if the source is an OCI registry reference
func isOCISource(source string) bool {
	return strings.HasPrefix(source, "oci://") ||
		strings.HasPrefix(source, "oras://")
}

// parseOCISource extracts the registry reference from OCI source formats
func parseOCISource(source string) string {
	source = strings.TrimPrefix(source, "oci://")
	source = strings.TrimPrefix(source, "oras://")
	return source
}

func (r *ModelCacheReconciler) jobForDownload(m *aiv1alpha1.ModelCache, pvcName, modelPath string) (*batchv1.Job, error) {
	// 1. Prepare Environment Variables
	envVars := []corev1.EnvVar{
		{
			Name:  "HF_HUB_ENABLE_HF_TRANSFER",
			Value: "0",
		},
	}

	// Inject HF_TOKEN if SecretRef is provided
	if m.Spec.SecretRef != nil {
		envVars = append(envVars, corev1.EnvVar{
			Name: "HF_TOKEN",
			ValueFrom: &corev1.EnvVarSource{
				SecretKeyRef: &corev1.SecretKeySelector{
					LocalObjectReference: corev1.LocalObjectReference{
						Name: *m.Spec.SecretRef,
					},
					Key: "HF_TOKEN",
				},
			},
		})
	}

	// Determine download strategy based on model source
	modelID := parseModelSource(m.Spec.Source)
	var downloadScript string
	var image string

	if isMlcModel(m.Spec.Source) {
		// MLC-LLM models require git clone with LFS support
		// Use debian:bookworm-slim as stable base with apt-get support
		image = "debian:bookworm-slim"
		downloadScript = fmt.Sprintf(`
set -ex
MODEL_ID="%s"
DEST_DIR="/models/%s"

# Skip if already downloaded
if [ -d "$DEST_DIR" ] && [ -f "$DEST_DIR/mlc-chat-config.json" ]; then
    echo "Model already cached at $DEST_DIR"
    exit 0
fi

# Install git and git-lfs
apt-get update && apt-get install -y git git-lfs ca-certificates
git lfs install

# Create destination directory
mkdir -p "$DEST_DIR"

# Clone from HuggingFace with LFS
echo "Cloning $MODEL_ID to $DEST_DIR..."
GIT_LFS_SKIP_SMUDGE=0 git clone "https://huggingface.co/$MODEL_ID" "$DEST_DIR"

echo "Download complete."
ls -la "$DEST_DIR"
`, modelID, modelPath)
	} else {
		// Standard HuggingFace models use huggingface-cli
		image = "python:3.10-slim"
		downloadScript = fmt.Sprintf(`
set -ex
MODEL_ID="%s"
DEST_DIR="/models/%s"

# Skip if already downloaded
if [ -d "$DEST_DIR" ] && [ "$(ls -A $DEST_DIR 2>/dev/null)" ]; then
    echo "Model already cached at $DEST_DIR"
    exit 0
fi

pip install --no-cache-dir huggingface_hub
echo "Downloading $MODEL_ID to $DEST_DIR..."
huggingface-cli download "$MODEL_ID" --local-dir "$DEST_DIR" --local-dir-use-symlinks False
echo "Download complete."
`, modelID, modelPath)
	}

	job := &batchv1.Job{
		ObjectMeta: metav1.ObjectMeta{
			Name:      m.Name + "-downloader",
			Namespace: m.Namespace,
		},
		Spec: batchv1.JobSpec{
			Template: corev1.PodTemplateSpec{
				Spec: corev1.PodSpec{
					RestartPolicy: corev1.RestartPolicyOnFailure,
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
								corev1.ResourceMemory: resource.MustParse("512Mi"),
							},
							Limits: corev1.ResourceList{
								corev1.ResourceMemory: resource.MustParse("2Gi"),
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
	if err := ctrl.SetControllerReference(m, job, r.Scheme); err != nil {
		return nil, err
	}
	return job, nil
}

func (r *ModelCacheReconciler) jobForOCIDownload(m *aiv1alpha1.ModelCache, pvcName, modelPath string) (*batchv1.Job, error) {
	registryRef := parseOCISource(m.Spec.Source)

	// Get ORAS image from environment or use default
	orasImage := "ghcr.io/oras-project/oras:v1.2.2"
	if img, ok := os.LookupEnv("ORAS_DOWNLOADER_IMAGE"); ok && img != "" {
		orasImage = img
	}

	downloadScript := fmt.Sprintf(`
set -ex
MODEL_REF="%s"
DEST_DIR="/models/%s"

# Skip if already downloaded
if [ -d "$DEST_DIR" ] && [ "$(ls -A $DEST_DIR 2>/dev/null)" ]; then
    echo "Model already cached at $DEST_DIR"
    exit 0
fi

mkdir -p "$DEST_DIR"
echo "Pulling OCI artifact $MODEL_REF to $DEST_DIR..."
oras pull "$MODEL_REF" -o "$DEST_DIR"
echo "Download complete."
ls -la "$DEST_DIR"
`, registryRef, modelPath)

	volumes := []corev1.Volume{{
		Name: "model-store",
		VolumeSource: corev1.VolumeSource{
			PersistentVolumeClaim: &corev1.PersistentVolumeClaimVolumeSource{
				ClaimName: pvcName,
			},
		},
	}}

	volumeMounts := []corev1.VolumeMount{{
		Name:      "model-store",
		MountPath: "/models",
	}}

	// Mount docker config secret for registry auth
	if m.Spec.OCIRegistrySecretRef != nil && *m.Spec.OCIRegistrySecretRef != "" {
		volumes = append(volumes, corev1.Volume{
			Name: "docker-config",
			VolumeSource: corev1.VolumeSource{
				Secret: &corev1.SecretVolumeSource{
					SecretName: *m.Spec.OCIRegistrySecretRef,
					Items: []corev1.KeyToPath{{
						Key:  ".dockerconfigjson",
						Path: "config.json",
					}},
				},
			},
		})
		volumeMounts = append(volumeMounts, corev1.VolumeMount{
			Name:      "docker-config",
			MountPath: "/root/.docker",
			ReadOnly:  true,
		})
	}

	job := &batchv1.Job{
		ObjectMeta: metav1.ObjectMeta{
			Name:      m.Name + "-downloader",
			Namespace: m.Namespace,
		},
		Spec: batchv1.JobSpec{
			Template: corev1.PodTemplateSpec{
				Spec: corev1.PodSpec{
					RestartPolicy: corev1.RestartPolicyOnFailure,
					Containers: []corev1.Container{{
						Name:         "downloader",
						Image:        orasImage,
						Command:      []string{"/bin/sh", "-c"},
						Args:         []string{downloadScript},
						VolumeMounts: volumeMounts,
						Resources: corev1.ResourceRequirements{
							Requests: corev1.ResourceList{
								corev1.ResourceCPU:    resource.MustParse("100m"),
								corev1.ResourceMemory: resource.MustParse("128Mi"),
							},
							Limits: corev1.ResourceList{
								corev1.ResourceMemory: resource.MustParse("512Mi"),
							},
						},
					}},
					Volumes: volumes,
				},
			},
		},
	}

	if err := ctrl.SetControllerReference(m, job, r.Scheme); err != nil {
		return nil, err
	}
	return job, nil
}

// SetupWithManager sets up the controller with the Manager.
func (r *ModelCacheReconciler) SetupWithManager(mgr ctrl.Manager) error {
	r.Recorder = mgr.GetEventRecorderFor("modelcache-controller")
	return ctrl.NewControllerManagedBy(mgr).
		For(&aiv1alpha1.ModelCache{}).
		Owns(&corev1.PersistentVolumeClaim{}).
		Owns(&batchv1.Job{}).
		Complete(r)
}
