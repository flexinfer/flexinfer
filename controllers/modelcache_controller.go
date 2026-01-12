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
	"path/filepath"
	"strings"
	"time"

	appsv1 "k8s.io/api/apps/v1"
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
//+kubebuilder:rbac:groups=apps,resources=daemonsets,verbs=get;list;watch;create;update;patch;delete

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

	if strategy == aiv1alpha1.StorageStrategyNodeLocal {
		return r.reconcileNodeLocal(ctx, modelCache)
	}

	if strategy == aiv1alpha1.StorageStrategyMemory {
		return r.reconcileMemory(ctx, modelCache)
	}

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

// reconcileNodeLocal handles the NodeLocal storage strategy using DaemonSets
func (r *ModelCacheReconciler) reconcileNodeLocal(ctx context.Context, m *aiv1alpha1.ModelCache) (ctrl.Result, error) {
	log := log.FromContext(ctx)

	// 1. Determine host path
	hostPath := "/var/lib/flexinfer/models"
	if m.Spec.HostPath != nil && *m.Spec.HostPath != "" {
		hostPath = *m.Spec.HostPath
	}
	modelPath := filepath.Join(hostPath, m.Name)

	// 2. Get or create DaemonSet
	dsName := m.Name + "-syncer"
	ds := &appsv1.DaemonSet{}
	err := r.Get(ctx, types.NamespacedName{Name: dsName, Namespace: m.Namespace}, ds)

	if err != nil && errors.IsNotFound(err) {
		// Create DaemonSet
		newDS, err := r.daemonSetForNodeLocal(m, modelPath, hostPath)
		if err != nil {
			return ctrl.Result{}, err
		}
		log.Info("Creating NodeLocal DaemonSet", "DaemonSet", dsName)
		if err := r.Create(ctx, newDS); err != nil {
			return ctrl.Result{}, err
		}
		m.Status.Phase = aiv1alpha1.ModelCachePhaseProvisioning
		return ctrl.Result{Requeue: true}, r.Status().Update(ctx, m)
	} else if err != nil {
		return ctrl.Result{}, err
	}

	// 3. Check DaemonSet status
	readyNodes := ds.Status.NumberReady
	totalNodes := ds.Status.DesiredNumberScheduled

	m.Status.ReadyNodes = readyNodes
	m.Status.TotalNodes = totalNodes
	m.Status.Path = modelPath

	if readyNodes == totalNodes && totalNodes > 0 {
		if m.Status.Phase != aiv1alpha1.ModelCachePhaseReady {
			m.Status.Phase = aiv1alpha1.ModelCachePhaseReady
			log.Info("ModelCache is Ready", "readyNodes", readyNodes, "totalNodes", totalNodes, "path", modelPath)
		}
	} else if readyNodes < totalNodes {
		m.Status.Phase = aiv1alpha1.ModelCachePhaseProvisioning
		log.Info("ModelCache provisioning", "readyNodes", readyNodes, "totalNodes", totalNodes)
	}

	if err := r.Status().Update(ctx, m); err != nil {
		return ctrl.Result{}, err
	}

	// Requeue to monitor DaemonSet status during provisioning
	if m.Status.Phase != aiv1alpha1.ModelCachePhaseReady {
		return ctrl.Result{RequeueAfter: 30 * time.Second}, nil
	}
	return ctrl.Result{}, nil
}

// daemonSetForNodeLocal creates a DaemonSet for syncing models to each node
func (r *ModelCacheReconciler) daemonSetForNodeLocal(m *aiv1alpha1.ModelCache, modelPath, hostPath string) (*appsv1.DaemonSet, error) {
	// Determine download method and script based on source type
	var image, downloadScript string

	if isOCISource(m.Spec.Source) {
		// OCI registry source - use ORAS
		image = "ghcr.io/oras-project/oras:v1.2.2"
		if img, ok := os.LookupEnv("ORAS_DOWNLOADER_IMAGE"); ok && img != "" {
			image = img
		}
		registryRef := parseOCISource(m.Spec.Source)
		downloadScript = fmt.Sprintf(`
set -ex
DEST_DIR="%s"
MODEL_REF="%s"
MARKER="$DEST_DIR/.synced"

if [ -f "$MARKER" ]; then
    echo "Model already synced at $DEST_DIR"
    while true; do sleep 3600; done
fi

mkdir -p "$DEST_DIR"
echo "Pulling OCI artifact $MODEL_REF to $DEST_DIR..."
oras pull "$MODEL_REF" -o "$DEST_DIR"
touch "$MARKER"
echo "Sync complete, entering sleep"
while true; do sleep 3600; done
`, modelPath, registryRef)
	} else if isMlcModel(m.Spec.Source) {
		// MLC-LLM models require git clone with LFS
		image = "debian:bookworm-slim"
		modelID := parseModelSource(m.Spec.Source)
		downloadScript = fmt.Sprintf(`
set -ex
DEST_DIR="%s"
MODEL_ID="%s"
MARKER="$DEST_DIR/.synced"

if [ -f "$MARKER" ]; then
    echo "Model already synced at $DEST_DIR"
    while true; do sleep 3600; done
fi

apt-get update && apt-get install -y git git-lfs ca-certificates
git lfs install
mkdir -p "$DEST_DIR"
echo "Cloning $MODEL_ID to $DEST_DIR..."
GIT_LFS_SKIP_SMUDGE=0 git clone "https://huggingface.co/$MODEL_ID" "$DEST_DIR"
touch "$MARKER"
echo "Sync complete, entering sleep"
while true; do sleep 3600; done
`, modelPath, modelID)
	} else {
		// Standard HuggingFace models
		image = "python:3.10-slim"
		modelID := parseModelSource(m.Spec.Source)
		downloadScript = fmt.Sprintf(`
set -ex
DEST_DIR="%s"
MODEL_ID="%s"
MARKER="$DEST_DIR/.synced"

if [ -f "$MARKER" ]; then
    echo "Model already synced at $DEST_DIR"
    while true; do sleep 3600; done
fi

pip install --no-cache-dir huggingface_hub
echo "Downloading $MODEL_ID to $DEST_DIR..."
mkdir -p "$DEST_DIR"
huggingface-cli download "$MODEL_ID" --local-dir "$DEST_DIR" --local-dir-use-symlinks False
touch "$MARKER"
echo "Sync complete, entering sleep"
while true; do sleep 3600; done
`, modelPath, modelID)
	}

	// Node selector - default to GPU nodes
	nodeSelector := m.Spec.NodeSelector
	if nodeSelector == nil {
		nodeSelector = map[string]string{
			"nvidia.com/gpu.present": "true",
		}
	}

	// Environment variables
	var envVars []corev1.EnvVar
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

	// Build volumes and mounts
	volumes := []corev1.Volume{{
		Name: "model-cache",
		VolumeSource: corev1.VolumeSource{
			HostPath: &corev1.HostPathVolumeSource{
				Path: hostPath,
				Type: hostPathTypePtr(corev1.HostPathDirectoryOrCreate),
			},
		},
	}}

	volumeMounts := []corev1.VolumeMount{{
		Name:      "model-cache",
		MountPath: hostPath,
	}}

	// Mount docker config secret for OCI registry auth
	if isOCISource(m.Spec.Source) && m.Spec.OCIRegistrySecretRef != nil && *m.Spec.OCIRegistrySecretRef != "" {
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

	labels := map[string]string{
		"app.kubernetes.io/name":       "modelcache-syncer",
		"app.kubernetes.io/instance":   m.Name,
		"app.kubernetes.io/managed-by": "flexinfer",
	}

	ds := &appsv1.DaemonSet{
		ObjectMeta: metav1.ObjectMeta{
			Name:      m.Name + "-syncer",
			Namespace: m.Namespace,
			Labels:    labels,
		},
		Spec: appsv1.DaemonSetSpec{
			Selector: &metav1.LabelSelector{
				MatchLabels: map[string]string{
					"app.kubernetes.io/name":     "modelcache-syncer",
					"app.kubernetes.io/instance": m.Name,
				},
			},
			Template: corev1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{
					Labels: labels,
				},
				Spec: corev1.PodSpec{
					NodeSelector: nodeSelector,
					Containers: []corev1.Container{{
						Name:         "syncer",
						Image:        image,
						Command:      []string{"/bin/sh", "-c"},
						Args:         []string{downloadScript},
						VolumeMounts: volumeMounts,
						Env:          envVars,
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
					Volumes: volumes,
				},
			},
		},
	}

	if err := ctrl.SetControllerReference(m, ds, r.Scheme); err != nil {
		return nil, err
	}
	return ds, nil
}

// hostPathTypePtr returns a pointer to a HostPathType
func hostPathTypePtr(t corev1.HostPathType) *corev1.HostPathType {
	return &t
}

// reconcileMemory handles the Memory storage strategy using DaemonSets with /dev/shm
func (r *ModelCacheReconciler) reconcileMemory(ctx context.Context, m *aiv1alpha1.ModelCache) (ctrl.Result, error) {
	log := log.FromContext(ctx)

	// Memory strategy uses /dev/shm (shared memory tmpfs) for RAM-backed caching
	shmBasePath := "/dev/shm/flexinfer"
	modelPath := filepath.Join(shmBasePath, m.Name)

	// Get or create DaemonSet
	dsName := m.Name + "-ram-syncer"
	ds := &appsv1.DaemonSet{}
	err := r.Get(ctx, types.NamespacedName{Name: dsName, Namespace: m.Namespace}, ds)

	if err != nil && errors.IsNotFound(err) {
		// Create DaemonSet
		newDS, err := r.daemonSetForMemory(m, modelPath, shmBasePath)
		if err != nil {
			return ctrl.Result{}, err
		}
		log.Info("Creating Memory DaemonSet", "DaemonSet", dsName)
		if err := r.Create(ctx, newDS); err != nil {
			return ctrl.Result{}, err
		}
		m.Status.Phase = aiv1alpha1.ModelCachePhaseProvisioning
		return ctrl.Result{Requeue: true}, r.Status().Update(ctx, m)
	} else if err != nil {
		return ctrl.Result{}, err
	}

	// Check DaemonSet status
	readyNodes := ds.Status.NumberReady
	totalNodes := ds.Status.DesiredNumberScheduled

	m.Status.ReadyNodes = readyNodes
	m.Status.TotalNodes = totalNodes
	m.Status.Path = modelPath

	if readyNodes == totalNodes && totalNodes > 0 {
		if m.Status.Phase != aiv1alpha1.ModelCachePhaseReady {
			m.Status.Phase = aiv1alpha1.ModelCachePhaseReady
			log.Info("ModelCache (Memory) is Ready", "readyNodes", readyNodes, "totalNodes", totalNodes, "path", modelPath)
		}
	} else if readyNodes < totalNodes {
		m.Status.Phase = aiv1alpha1.ModelCachePhaseProvisioning
		log.Info("ModelCache (Memory) provisioning", "readyNodes", readyNodes, "totalNodes", totalNodes)
	}

	if err := r.Status().Update(ctx, m); err != nil {
		return ctrl.Result{}, err
	}

	// Requeue to monitor DaemonSet status during provisioning
	if m.Status.Phase != aiv1alpha1.ModelCachePhaseReady {
		return ctrl.Result{RequeueAfter: 30 * time.Second}, nil
	}
	return ctrl.Result{}, nil
}

// daemonSetForMemory creates a DaemonSet for syncing models to /dev/shm (RAM)
func (r *ModelCacheReconciler) daemonSetForMemory(m *aiv1alpha1.ModelCache, modelPath, shmBasePath string) (*appsv1.DaemonSet, error) {
	// Determine download method and script based on source type
	var image, downloadScript string
	var sourcePVC string
	copyFromPVC := false

	// If existingClaimName is set, copy from that PVC instead of downloading
	// This enables the pattern: NFS ModelCache -> RAM ModelCache (fast local copy)
	if m.Spec.ExistingClaimName != nil && *m.Spec.ExistingClaimName != "" {
		sourcePVC = *m.Spec.ExistingClaimName
		copyFromPVC = true
		image = "alpine:3.19"

		// Determine source path within the PVC
		// If modelPath is set, use it; otherwise use the cache name as subdirectory
		sourcePath := "/source"
		if m.Spec.ModelPath != nil && *m.Spec.ModelPath != "" {
			sourcePath = fmt.Sprintf("/source/%s", *m.Spec.ModelPath)
		}

		downloadScript = fmt.Sprintf(`
set -ex
SOURCE_DIR="%s"
DEST_DIR="%s"
MARKER="$DEST_DIR/.synced"

# Check if already synced
if [ -f "$MARKER" ]; then
    echo "Model already synced at $DEST_DIR (RAM cache)"
    # Monitor source for changes
    while true; do
        sleep 300
        if [ "$SOURCE_DIR/.synced" -nt "$MARKER" ] 2>/dev/null; then
            echo "Source updated, re-syncing..."
            rm -f "$MARKER"
        fi
    done
fi

# Wait for source to be ready
echo "Waiting for source model at $SOURCE_DIR..."
TIMEOUT=600
WAITED=0
while [ ! -f "$SOURCE_DIR/.synced" ] && [ $WAITED -lt $TIMEOUT ]; do
    sleep 5
    WAITED=$((WAITED + 5))
    echo "Waiting for source... ($WAITED/$TIMEOUT seconds)"
done

if [ ! -f "$SOURCE_DIR/.synced" ]; then
    echo "ERROR: Source model not ready after ${TIMEOUT}s"
    exit 1
fi

# Install rsync for efficient copy
apk add --no-cache rsync

# Copy from NFS to RAM
echo "Copying model from NFS ($SOURCE_DIR) to RAM ($DEST_DIR)..."
mkdir -p "$DEST_DIR"
rsync -av --delete "$SOURCE_DIR/" "$DEST_DIR/"
touch "$MARKER"
echo "RAM cache sync complete (copied from NFS)"

# Monitor for source updates
while true; do
    sleep 300
    if [ "$SOURCE_DIR/.synced" -nt "$MARKER" ] 2>/dev/null; then
        echo "Source updated, re-syncing..."
        rsync -av --delete "$SOURCE_DIR/" "$DEST_DIR/"
        touch "$MARKER"
        echo "Re-sync complete"
    fi
done
`, sourcePath, modelPath)
	} else if isOCISource(m.Spec.Source) {
		// OCI registry source - use ORAS
		image = "ghcr.io/oras-project/oras:v1.2.2"
		if img, ok := os.LookupEnv("ORAS_DOWNLOADER_IMAGE"); ok && img != "" {
			image = img
		}
		registryRef := parseOCISource(m.Spec.Source)
		downloadScript = fmt.Sprintf(`
set -ex
DEST_DIR="%s"
MODEL_REF="%s"
MARKER="$DEST_DIR/.synced"

if [ -f "$MARKER" ]; then
    echo "Model already synced at $DEST_DIR (RAM cache)"
    while true; do sleep 3600; done
fi

mkdir -p "$DEST_DIR"
echo "Pulling OCI artifact $MODEL_REF to $DEST_DIR (RAM cache)..."
oras pull "$MODEL_REF" -o "$DEST_DIR"
touch "$MARKER"
echo "Sync complete to RAM cache, entering sleep"
while true; do sleep 3600; done
`, modelPath, registryRef)
	} else if isMlcModel(m.Spec.Source) {
		// MLC-LLM models require git clone with LFS
		image = "debian:bookworm-slim"
		modelID := parseModelSource(m.Spec.Source)
		downloadScript = fmt.Sprintf(`
set -ex
DEST_DIR="%s"
MODEL_ID="%s"
MARKER="$DEST_DIR/.synced"

if [ -f "$MARKER" ]; then
    echo "Model already synced at $DEST_DIR (RAM cache)"
    while true; do sleep 3600; done
fi

apt-get update && apt-get install -y git git-lfs ca-certificates
git lfs install
mkdir -p "$DEST_DIR"
echo "Cloning $MODEL_ID to $DEST_DIR (RAM cache)..."
GIT_LFS_SKIP_SMUDGE=0 git clone "https://huggingface.co/$MODEL_ID" "$DEST_DIR"
touch "$MARKER"
echo "Sync complete to RAM cache, entering sleep"
while true; do sleep 3600; done
`, modelPath, modelID)
	} else {
		// Standard HuggingFace models
		image = "python:3.10-slim"
		modelID := parseModelSource(m.Spec.Source)
		downloadScript = fmt.Sprintf(`
set -ex
DEST_DIR="%s"
MODEL_ID="%s"
MARKER="$DEST_DIR/.synced"

if [ -f "$MARKER" ]; then
    echo "Model already synced at $DEST_DIR (RAM cache)"
    while true; do sleep 3600; done
fi

pip install --no-cache-dir huggingface_hub
export PATH="$PATH:/root/.local/bin"
echo "Downloading $MODEL_ID to $DEST_DIR (RAM cache)..."
mkdir -p "$DEST_DIR"
python -m huggingface_hub.commands.huggingface_cli download "$MODEL_ID" --local-dir "$DEST_DIR" --local-dir-use-symlinks False
touch "$MARKER"
echo "Sync complete to RAM cache, entering sleep"
while true; do sleep 3600; done
`, modelPath, modelID)
	}

	// Node selector - default to GPU nodes
	nodeSelector := m.Spec.NodeSelector
	if nodeSelector == nil {
		nodeSelector = map[string]string{
			"nvidia.com/gpu.present": "true",
		}
	}

	// Environment variables
	var envVars []corev1.EnvVar
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

	// Build volumes - mount /dev/shm via hostPath
	volumes := []corev1.Volume{{
		Name: "model-ram-cache",
		VolumeSource: corev1.VolumeSource{
			HostPath: &corev1.HostPathVolumeSource{
				Path: shmBasePath,
				Type: hostPathTypePtr(corev1.HostPathDirectoryOrCreate),
			},
		},
	}}

	volumeMounts := []corev1.VolumeMount{{
		Name:      "model-ram-cache",
		MountPath: shmBasePath,
	}}

	// Mount docker config secret for OCI registry auth
	if isOCISource(m.Spec.Source) && m.Spec.OCIRegistrySecretRef != nil && *m.Spec.OCIRegistrySecretRef != "" {
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

	// Mount source PVC when copying from NFS to RAM
	if copyFromPVC && sourcePVC != "" {
		volumes = append(volumes, corev1.Volume{
			Name: "source-pvc",
			VolumeSource: corev1.VolumeSource{
				PersistentVolumeClaim: &corev1.PersistentVolumeClaimVolumeSource{
					ClaimName: sourcePVC,
					ReadOnly:  true,
				},
			},
		})
		volumeMounts = append(volumeMounts, corev1.VolumeMount{
			Name:      "source-pvc",
			MountPath: "/source",
			ReadOnly:  true,
		})
	}

	labels := map[string]string{
		"app.kubernetes.io/name":       "modelcache-ram-syncer",
		"app.kubernetes.io/instance":   m.Name,
		"app.kubernetes.io/managed-by": "flexinfer",
	}

	// GPU node tolerations for RAM cache syncer
	gpuTolerations := []corev1.Toleration{
		{
			Key:      "dedicated",
			Operator: corev1.TolerationOpEqual,
			Value:    "gpu",
			Effect:   corev1.TaintEffectNoSchedule,
		},
		{
			Key:      "nvidia.com/gpu",
			Operator: corev1.TolerationOpExists,
			Effect:   corev1.TaintEffectNoSchedule,
		},
		{
			Key:      "amd.com/gpu",
			Operator: corev1.TolerationOpExists,
			Effect:   corev1.TaintEffectNoSchedule,
		},
	}

	ds := &appsv1.DaemonSet{
		ObjectMeta: metav1.ObjectMeta{
			Name:      m.Name + "-ram-syncer",
			Namespace: m.Namespace,
			Labels:    labels,
		},
		Spec: appsv1.DaemonSetSpec{
			Selector: &metav1.LabelSelector{
				MatchLabels: map[string]string{
					"app.kubernetes.io/name":     "modelcache-ram-syncer",
					"app.kubernetes.io/instance": m.Name,
				},
			},
			Template: corev1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{
					Labels: labels,
				},
				Spec: corev1.PodSpec{
					NodeSelector: nodeSelector,
					Tolerations:  gpuTolerations,
					Containers: []corev1.Container{{
						Name:         "syncer",
						Image:        image,
						Command:      []string{"/bin/sh", "-c"},
						Args:         []string{downloadScript},
						VolumeMounts: volumeMounts,
						Env:          envVars,
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
					Volumes: volumes,
				},
			},
		},
	}

	if err := ctrl.SetControllerReference(m, ds, r.Scheme); err != nil {
		return nil, err
	}
	return ds, nil
}

// SetupWithManager sets up the controller with the Manager.
func (r *ModelCacheReconciler) SetupWithManager(mgr ctrl.Manager) error {
	r.Recorder = mgr.GetEventRecorderFor("modelcache-controller")
	return ctrl.NewControllerManagedBy(mgr).
		For(&aiv1alpha1.ModelCache{}).
		Owns(&corev1.PersistentVolumeClaim{}).
		Owns(&batchv1.Job{}).
		Owns(&appsv1.DaemonSet{}).
		Complete(r)
}
