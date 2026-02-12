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
	"reflect"
	"sort"
	"strconv"
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
	"github.com/flexinfer/flexinfer/pkg/metrics"
	"github.com/flexinfer/flexinfer/pkg/quantization"
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

	// Unknown or unsupported strategy - return error instead of silently succeeding
	return ctrl.Result{}, fmt.Errorf("storage strategy %q not implemented", strategy)
}

func (r *ModelCacheReconciler) reconcileSharedPVC(ctx context.Context, modelCache *aiv1alpha1.ModelCache) (ctrl.Result, error) {
	log := log.FromContext(ctx)

	// Determine PVC name - either existing or create new
	var pvcName string
	pvcNamespace := modelCache.Namespace

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
		// Path includes both PVC name and model subdirectory
		modelCache.Status.Path = fmt.Sprintf("%s:%s", pvcName, modelPath)

		// Set OCI-specific status fields
		if isOCISource(modelCache.Spec.Source) {
			now := metav1.Now()
			modelCache.Status.OCIPulledAt = &now
			modelCache.Status.OCIRegistry = extractOCIRegistry(modelCache.Spec.Source)
		}

		// If quantization is requested, handle it before marking Ready
		if modelCache.Spec.Quantization != nil {
			return r.reconcileQuantization(ctx, modelCache, pvcName, modelPath)
		}

		if modelCache.Status.Phase != aiv1alpha1.ModelCachePhaseReady {
			modelCache.Status.Phase = aiv1alpha1.ModelCachePhaseReady
			if err := r.Status().Update(ctx, modelCache); err != nil {
				return ctrl.Result{}, err
			}
			log.Info("ModelCache is Ready", "path", modelCache.Status.Path)
			r.Recorder.Event(modelCache, corev1.EventTypeNormal, "CacheReady",
				fmt.Sprintf("Model cached successfully at %s", modelCache.Status.Path))
		}
	} else if job.Status.Failed > 0 {
		modelCache.Status.Phase = aiv1alpha1.ModelCachePhaseFailed
		if err := r.Status().Update(ctx, modelCache); err != nil {
			return ctrl.Result{}, err
		}
		r.Recorder.Event(modelCache, corev1.EventTypeWarning, "CacheFailed",
			"Model download job failed - check job logs for details")
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

// extractOCIRegistry extracts the registry hostname from an OCI source URL
func extractOCIRegistry(source string) string {
	ref := parseOCISource(source)
	// Split on first slash to get registry hostname
	parts := strings.SplitN(ref, "/", 2)
	if len(parts) > 0 {
		return parts[0]
	}
	return ""
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
GIT_LFS_SKIP_SMUDGE=0 git clone "%s/$MODEL_ID" "$DEST_DIR"

echo "Download complete."
ls -la "$DEST_DIR"
`, modelID, modelPath, huggingFaceRepositoryBaseURL)
	} else {
		// Standard HuggingFace models use huggingface_hub snapshot_download (more stable than huggingface-cli)
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
mkdir -p "$DEST_DIR"
MODEL_ID="$MODEL_ID" DEST_DIR="$DEST_DIR" python - <<'PY'
import os

from huggingface_hub import snapshot_download

repo_id = os.environ["MODEL_ID"]
local_dir = os.environ["DEST_DIR"]
token = os.environ.get("HF_TOKEN") or os.environ.get("HUGGINGFACE_HUB_TOKEN")

snapshot_download(
    repo_id=repo_id,
    local_dir=local_dir,
    local_dir_use_symlinks=False,
    token=token,
)
PY
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

	// Extract registry host for health check
	registryHost := extractOCIRegistry(m.Spec.Source)

	downloadScript := fmt.Sprintf(`
set -e
MODEL_REF="%s"
DEST_DIR="/models/%s"
REGISTRY_HOST="%s"
MAX_RETRIES=3
RETRY_DELAY=10

# Skip if already downloaded
if [ -d "$DEST_DIR" ] && [ "$(ls -A $DEST_DIR 2>/dev/null)" ]; then
    echo "Model already cached at $DEST_DIR"
    exit 0
fi

# Registry health check with retry
echo "Checking registry connectivity to $REGISTRY_HOST..."
for i in $(seq 1 $MAX_RETRIES); do
    if oras repo tags "$MODEL_REF" --last 1 >/dev/null 2>&1; then
        echo "Registry is reachable"
        break
    fi
    if [ $i -eq $MAX_RETRIES ]; then
        echo "ERROR: Cannot reach registry $REGISTRY_HOST after $MAX_RETRIES attempts"
        exit 1
    fi
    echo "Registry check failed, retrying in ${RETRY_DELAY}s... (attempt $i/$MAX_RETRIES)"
    sleep $RETRY_DELAY
    RETRY_DELAY=$((RETRY_DELAY * 2))
done

mkdir -p "$DEST_DIR"

# Pull with retry and exponential backoff
RETRY_DELAY=10
for i in $(seq 1 $MAX_RETRIES); do
    echo "Pulling OCI artifact $MODEL_REF to $DEST_DIR (attempt $i/$MAX_RETRIES)..."
    if oras pull "$MODEL_REF" -o "$DEST_DIR"; then
        echo "Download complete."
        break
    fi
    if [ $i -eq $MAX_RETRIES ]; then
        echo "ERROR: Failed to pull artifact after $MAX_RETRIES attempts"
        exit 1
    fi
    echo "Pull failed, retrying in ${RETRY_DELAY}s..."
    sleep $RETRY_DELAY
    RETRY_DELAY=$((RETRY_DELAY * 2))
done

# Show downloaded contents
ls -la "$DEST_DIR"
echo "Successfully cached model from $MODEL_REF"
`, registryRef, modelPath, registryHost)

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

	// BackoffLimit controls Kubernetes-level job retries (in addition to in-script retries)
	backoffLimit := int32(3)

	job := &batchv1.Job{
		ObjectMeta: metav1.ObjectMeta{
			Name:      m.Name + "-downloader",
			Namespace: m.Namespace,
			Labels: map[string]string{
				"app.kubernetes.io/name":      "modelcache-downloader",
				"app.kubernetes.io/component": "oci-puller",
				"app.kubernetes.io/instance":  m.Name,
			},
		},
		Spec: batchv1.JobSpec{
			BackoffLimit: &backoffLimit,
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

	// Keep the DaemonSet spec in sync with the desired state (no kubectl patches needed).
	desiredDS, err := r.daemonSetForNodeLocal(m, modelPath, hostPath)
	if err != nil {
		return ctrl.Result{}, err
	}

	dsNeedsUpdate := false

	// Ensure controller ownership so we get DaemonSet events and can reconcile drift.
	if !metav1.IsControlledBy(ds, m) {
		if err := ctrl.SetControllerReference(m, ds, r.Scheme); err != nil {
			return ctrl.Result{}, err
		}
		dsNeedsUpdate = true
	}

	// Sync labels (merge-only: keep any extra labels set by users/tools).
	if ds.Labels == nil {
		ds.Labels = make(map[string]string)
	}
	for k, v := range desiredDS.Labels {
		if ds.Labels[k] != v {
			ds.Labels[k] = v
			dsNeedsUpdate = true
		}
	}
	if ds.Spec.Template.Labels == nil {
		ds.Spec.Template.Labels = make(map[string]string)
	}
	for k, v := range desiredDS.Spec.Template.Labels {
		if ds.Spec.Template.Labels[k] != v {
			ds.Spec.Template.Labels[k] = v
			dsNeedsUpdate = true
		}
	}

	// Sync the PodSpec fields we own.
	if !reflect.DeepEqual(ds.Spec.Template.Spec.NodeSelector, desiredDS.Spec.Template.Spec.NodeSelector) {
		ds.Spec.Template.Spec.NodeSelector = desiredDS.Spec.Template.Spec.NodeSelector
		dsNeedsUpdate = true
	}
	if !reflect.DeepEqual(ds.Spec.Template.Spec.Tolerations, desiredDS.Spec.Template.Spec.Tolerations) {
		ds.Spec.Template.Spec.Tolerations = desiredDS.Spec.Template.Spec.Tolerations
		dsNeedsUpdate = true
	}
	if !reflect.DeepEqual(ds.Spec.Template.Spec.Volumes, desiredDS.Spec.Template.Spec.Volumes) {
		ds.Spec.Template.Spec.Volumes = desiredDS.Spec.Template.Spec.Volumes
		dsNeedsUpdate = true
	}

	if len(desiredDS.Spec.Template.Spec.Containers) == 0 {
		return ctrl.Result{}, fmt.Errorf("desired DaemonSet has no containers")
	}
	desiredSyncer := desiredDS.Spec.Template.Spec.Containers[0]

	syncerIndex := -1
	for i := range ds.Spec.Template.Spec.Containers {
		if ds.Spec.Template.Spec.Containers[i].Name == desiredSyncer.Name {
			syncerIndex = i
			break
		}
	}
	if syncerIndex == -1 {
		ds.Spec.Template.Spec.Containers = desiredDS.Spec.Template.Spec.Containers
		dsNeedsUpdate = true
	} else {
		syncer := &ds.Spec.Template.Spec.Containers[syncerIndex]
		if syncer.Image != desiredSyncer.Image {
			syncer.Image = desiredSyncer.Image
			dsNeedsUpdate = true
		}
		if !reflect.DeepEqual(syncer.Command, desiredSyncer.Command) {
			syncer.Command = desiredSyncer.Command
			dsNeedsUpdate = true
		}
		if !reflect.DeepEqual(syncer.Args, desiredSyncer.Args) {
			syncer.Args = desiredSyncer.Args
			dsNeedsUpdate = true
		}
		if !reflect.DeepEqual(syncer.Env, desiredSyncer.Env) {
			syncer.Env = desiredSyncer.Env
			dsNeedsUpdate = true
		}
		if !reflect.DeepEqual(syncer.VolumeMounts, desiredSyncer.VolumeMounts) {
			syncer.VolumeMounts = desiredSyncer.VolumeMounts
			dsNeedsUpdate = true
		}
		if !reflect.DeepEqual(syncer.Resources, desiredSyncer.Resources) {
			syncer.Resources = desiredSyncer.Resources
			dsNeedsUpdate = true
		}
	}

	if dsNeedsUpdate {
		log.Info("Updating NodeLocal DaemonSet", "DaemonSet", dsName)
		if err := r.Update(ctx, ds); err != nil {
			return ctrl.Result{}, err
		}
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

if [ -f "$MARKER" ] || [ -f "$DEST_DIR/mlc-chat-config.json" ]; then
    echo "Model already synced at $DEST_DIR"
    while true; do sleep 3600; done
fi

apt-get update && apt-get install -y git git-lfs ca-certificates
git lfs install
mkdir -p "$DEST_DIR"
echo "Cloning $MODEL_ID to $DEST_DIR..."
GIT_LFS_SKIP_SMUDGE=0 git clone "%s/$MODEL_ID" "$DEST_DIR"
touch "$MARKER"
echo "Sync complete, entering sleep"
while true; do sleep 3600; done
`, modelPath, modelID, huggingFaceRepositoryBaseURL)
	} else {
		// Standard HuggingFace models
		image = "python:3.10-slim"
		modelID := parseModelSource(m.Spec.Source)
		downloadScript = fmt.Sprintf(`
set -ex
DEST_DIR="%s"
MODEL_ID="%s"
MARKER="$DEST_DIR/.synced"

if [ -f "$MARKER" ] || { [ -d "$DEST_DIR" ] && [ "$(ls -A "$DEST_DIR" 2>/dev/null)" ]; }; then
    echo "Model already synced at $DEST_DIR"
    while true; do sleep 3600; done
fi

pip install --no-cache-dir huggingface_hub
echo "Downloading $MODEL_ID to $DEST_DIR..."
mkdir -p "$DEST_DIR"
MODEL_ID="$MODEL_ID" DEST_DIR="$DEST_DIR" python - <<'PY'
import os

from huggingface_hub import snapshot_download

repo_id = os.environ["MODEL_ID"]
local_dir = os.environ["DEST_DIR"]
token = os.environ.get("HF_TOKEN") or os.environ.get("HUGGINGFACE_HUB_TOKEN")

snapshot_download(
    repo_id=repo_id,
    local_dir=local_dir,
    local_dir_use_symlinks=False,
token=token,
)
PY
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

	// GPU node tolerations (allows scheduling on dedicated GPU nodes).
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

	// Keep the DaemonSet spec in sync with the desired state (no kubectl patches needed).
	desiredDS, err := r.daemonSetForMemory(m, modelPath, shmBasePath)
	if err != nil {
		return ctrl.Result{}, err
	}

	dsNeedsUpdate := false

	// Ensure controller ownership so we get DaemonSet events and can reconcile drift.
	if !metav1.IsControlledBy(ds, m) {
		if err := ctrl.SetControllerReference(m, ds, r.Scheme); err != nil {
			return ctrl.Result{}, err
		}
		dsNeedsUpdate = true
	}

	// Sync labels (merge-only: keep any extra labels set by users/tools).
	if ds.Labels == nil {
		ds.Labels = make(map[string]string)
	}
	for k, v := range desiredDS.Labels {
		if ds.Labels[k] != v {
			ds.Labels[k] = v
			dsNeedsUpdate = true
		}
	}
	if ds.Spec.Template.Labels == nil {
		ds.Spec.Template.Labels = make(map[string]string)
	}
	for k, v := range desiredDS.Spec.Template.Labels {
		if ds.Spec.Template.Labels[k] != v {
			ds.Spec.Template.Labels[k] = v
			dsNeedsUpdate = true
		}
	}

	// Sync the PodSpec fields we own.
	if !reflect.DeepEqual(ds.Spec.Template.Spec.NodeSelector, desiredDS.Spec.Template.Spec.NodeSelector) {
		ds.Spec.Template.Spec.NodeSelector = desiredDS.Spec.Template.Spec.NodeSelector
		dsNeedsUpdate = true
	}
	if !reflect.DeepEqual(ds.Spec.Template.Spec.Tolerations, desiredDS.Spec.Template.Spec.Tolerations) {
		ds.Spec.Template.Spec.Tolerations = desiredDS.Spec.Template.Spec.Tolerations
		dsNeedsUpdate = true
	}
	if !reflect.DeepEqual(ds.Spec.Template.Spec.Volumes, desiredDS.Spec.Template.Spec.Volumes) {
		ds.Spec.Template.Spec.Volumes = desiredDS.Spec.Template.Spec.Volumes
		dsNeedsUpdate = true
	}

	if len(desiredDS.Spec.Template.Spec.Containers) == 0 {
		return ctrl.Result{}, fmt.Errorf("desired DaemonSet has no containers")
	}
	desiredSyncer := desiredDS.Spec.Template.Spec.Containers[0]

	syncerIndex := -1
	for i := range ds.Spec.Template.Spec.Containers {
		if ds.Spec.Template.Spec.Containers[i].Name == desiredSyncer.Name {
			syncerIndex = i
			break
		}
	}
	if syncerIndex == -1 {
		ds.Spec.Template.Spec.Containers = desiredDS.Spec.Template.Spec.Containers
		dsNeedsUpdate = true
	} else {
		syncer := &ds.Spec.Template.Spec.Containers[syncerIndex]
		if syncer.Image != desiredSyncer.Image {
			syncer.Image = desiredSyncer.Image
			dsNeedsUpdate = true
		}
		if !reflect.DeepEqual(syncer.Command, desiredSyncer.Command) {
			syncer.Command = desiredSyncer.Command
			dsNeedsUpdate = true
		}
		if !reflect.DeepEqual(syncer.Args, desiredSyncer.Args) {
			syncer.Args = desiredSyncer.Args
			dsNeedsUpdate = true
		}
		if !reflect.DeepEqual(syncer.Env, desiredSyncer.Env) {
			syncer.Env = desiredSyncer.Env
			dsNeedsUpdate = true
		}
		if !reflect.DeepEqual(syncer.VolumeMounts, desiredSyncer.VolumeMounts) {
			syncer.VolumeMounts = desiredSyncer.VolumeMounts
			dsNeedsUpdate = true
		}
		if !reflect.DeepEqual(syncer.Resources, desiredSyncer.Resources) {
			syncer.Resources = desiredSyncer.Resources
			dsNeedsUpdate = true
		}
	}

	if dsNeedsUpdate {
		log.Info("Updating Memory DaemonSet to match desired spec", "DaemonSet", dsName)
		if err := r.Update(ctx, ds); err != nil {
			return ctrl.Result{}, err
		}
	}

	// Check DaemonSet status
	readyNodes := ds.Status.NumberReady
	totalNodes := ds.Status.DesiredNumberScheduled

	m.Status.ReadyNodes = readyNodes
	m.Status.TotalNodes = totalNodes
	m.Status.Path = modelPath

	wasNotReady := m.Status.Phase != aiv1alpha1.ModelCachePhaseReady

	if readyNodes == totalNodes && totalNodes > 0 {
		if wasNotReady {
			m.Status.Phase = aiv1alpha1.ModelCachePhaseReady
			// Mark as resident and update access time when transitioning to Ready
			if err := r.markCacheResident(ctx, m); err != nil {
				log.Error(err, "Failed to mark cache as resident")
				// Continue anyway, non-fatal
			}
			if err := r.updateCacheAccessTime(ctx, m); err != nil {
				log.Error(err, "Failed to update cache access time")
				// Continue anyway, non-fatal
			}
			log.Info("ModelCache (Memory) is Ready", "readyNodes", readyNodes, "totalNodes", totalNodes, "path", modelPath)
		}
	} else if readyNodes < totalNodes {
		m.Status.Phase = aiv1alpha1.ModelCachePhaseProvisioning
		log.Info("ModelCache (Memory) provisioning", "readyNodes", readyNodes, "totalNodes", totalNodes)
	}

	if err := r.Status().Update(ctx, m); err != nil {
		return ctrl.Result{}, err
	}

	// Update Prometheus metrics for this cache
	// Use empty string for nodeName since this is a cluster-wide view
	r.updateCacheMetrics(m, "")

	// Check for eviction pressure when cache becomes ready
	// This ensures we make room for new caches by evicting older ones
	if wasNotReady && m.Status.Phase == aiv1alpha1.ModelCachePhaseReady {
		evicted, err := r.checkAndPerformEviction(ctx, m)
		if err != nil {
			log.Error(err, "Failed to check/perform eviction")
			// Don't fail the reconcile; eviction is best-effort
		}
		if evicted {
			log.Info("Eviction performed to make room for new cache")
		}
	}

	if dsNeedsUpdate {
		return ctrl.Result{RequeueAfter: 5 * time.Second}, nil
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
MARKER="$DEST_DIR/.flexinfer_synced"

wait_for_source() {
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
}

sync_from_source() {
    rm -f "$MARKER"

    # Install rsync for efficient copy
    apk add --no-cache rsync

    # Copy from NFS/PVC to RAM
    echo "Copying model from source ($SOURCE_DIR) to RAM ($DEST_DIR)..."
    mkdir -p "$DEST_DIR"
    # Do not copy the source .synced marker into the destination marker.
    rsync -av --delete --exclude '.synced' "$SOURCE_DIR/" "$DEST_DIR/"
    touch "$MARKER"
    echo "RAM cache sync complete"
}

# Wait for source to be ready
wait_for_source

if [ -f "$MARKER" ]; then
    echo "Model already synced at $DEST_DIR (RAM cache)"
else
    sync_from_source
fi

# Monitor for source updates
while true; do
    sleep 300
    if [ ! -f "$MARKER" ]; then
        echo "Sync marker missing, re-syncing..."
        sync_from_source
        continue
    fi

    # If any source file is newer than the last successful sync marker, re-sync.
    if find "$SOURCE_DIR" -type f -newer "$MARKER" -print -quit 2>/dev/null | grep -q .; then
        echo "Source updated, re-syncing..."
        sync_from_source
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
GIT_LFS_SKIP_SMUDGE=0 git clone "%s/$MODEL_ID" "$DEST_DIR"
touch "$MARKER"
echo "Sync complete to RAM cache, entering sleep"
while true; do sleep 3600; done
`, modelPath, modelID, huggingFaceRepositoryBaseURL)
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

	memoryRequest := resource.MustParse("512Mi")
	memoryLimit := resource.MustParse("2Gi")
	if copyFromPVC {
		memoryRequest = resource.MustParse("1Gi")
		memoryLimit = resource.MustParse("12Gi")
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
								corev1.ResourceMemory: memoryRequest,
							},
							Limits: corev1.ResourceList{
								corev1.ResourceMemory: memoryLimit,
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

// === LRU Eviction Support ===

// checkAndPerformEviction checks if storage pressure requires eviction and performs it if needed.
// Returns true if eviction was performed, false otherwise.
func (r *ModelCacheReconciler) checkAndPerformEviction(ctx context.Context, currentCache *aiv1alpha1.ModelCache) (bool, error) {
	log := log.FromContext(ctx)

	// Only check eviction for Memory strategy caches
	if currentCache.Spec.StorageStrategy != aiv1alpha1.StorageStrategyMemory {
		return false, nil
	}

	// Get eviction threshold (default 85%)
	threshold := int32(85)
	if currentCache.Spec.EvictionThresholdPercent != nil {
		threshold = *currentCache.Spec.EvictionThresholdPercent
	}

	// Get eviction policy (default LRU)
	policy := aiv1alpha1.EvictionPolicyLRU
	if currentCache.Spec.EvictionPolicy != "" {
		policy = currentCache.Spec.EvictionPolicy
	}

	// Skip if eviction is disabled
	if policy == aiv1alpha1.EvictionPolicyNone {
		return false, nil
	}

	// List all Memory strategy ModelCaches in the namespace
	cacheList := &aiv1alpha1.ModelCacheList{}
	if err := r.List(ctx, cacheList, client.InNamespace(currentCache.Namespace)); err != nil {
		return false, fmt.Errorf("failed to list ModelCaches: %w", err)
	}

	// Filter to only Memory strategy caches that are Ready
	var memoryCaches []aiv1alpha1.ModelCache
	var totalCacheSize int64
	for _, cache := range cacheList.Items {
		if cache.Spec.StorageStrategy == aiv1alpha1.StorageStrategyMemory &&
			cache.Status.Phase == aiv1alpha1.ModelCachePhaseReady {
			memoryCaches = append(memoryCaches, cache)
			totalCacheSize += cache.Status.CacheSizeBytes
		}
	}

	// Estimate /dev/shm utilization based on tracked cache sizes
	// This is a heuristic; in production you'd want node-level metrics
	estimatedUsagePercent := int32(0)
	if totalCacheSize > 0 {
		// Assume 16GB /dev/shm as typical size (can be made configurable)
		shmCapacity := int64(16 * 1024 * 1024 * 1024) // 16GB
		if envCapacity, ok := os.LookupEnv("FLEXINFER_SHM_CAPACITY_BYTES"); ok {
			if parsed, err := resource.ParseQuantity(envCapacity); err == nil {
				shmCapacity = parsed.Value()
			}
		}
		estimatedUsagePercent = int32((totalCacheSize * 100) / shmCapacity)
	}

	// Check if we're over threshold
	if estimatedUsagePercent < threshold {
		return false, nil
	}

	log.Info("Storage pressure detected, considering eviction",
		"usagePercent", estimatedUsagePercent,
		"threshold", threshold,
		"policy", policy,
		"memoryCaches", len(memoryCaches))

	// Select eviction candidate
	candidate := r.selectEvictionCandidate(memoryCaches, policy, currentCache.Name)
	if candidate == nil {
		log.Info("No eviction candidate found (only current cache exists)")
		return false, nil
	}

	log.Info("Selected eviction candidate",
		"candidate", candidate.Name,
		"lastAccess", candidate.Status.LastAccessTime,
		"priority", candidate.Spec.RetentionPriority,
		"cacheSize", candidate.Status.CacheSizeBytes)

	// Perform eviction by deleting the DaemonSet
	if err := r.evictCache(ctx, candidate); err != nil {
		return false, fmt.Errorf("failed to evict cache %s: %w", candidate.Name, err)
	}

	r.Recorder.Eventf(currentCache, corev1.EventTypeNormal, "EvictionPerformed",
		"Evicted cache %s to make room (policy: %s, usage: %d%%)", candidate.Name, policy, estimatedUsagePercent)

	return true, nil
}

// selectEvictionCandidate selects the best cache to evict based on policy.
// Never evicts the currentCacheName (the cache being reconciled).
func (r *ModelCacheReconciler) selectEvictionCandidate(caches []aiv1alpha1.ModelCache, policy aiv1alpha1.EvictionPolicy, currentCacheName string) *aiv1alpha1.ModelCache {
	// Filter out the current cache and caches with EvictionPolicyNone
	var candidates []aiv1alpha1.ModelCache
	for _, cache := range caches {
		if cache.Name == currentCacheName {
			continue
		}
		if cache.Spec.EvictionPolicy == aiv1alpha1.EvictionPolicyNone {
			continue
		}
		candidates = append(candidates, cache)
	}

	if len(candidates) == 0 {
		return nil
	}

	// Sort by eviction priority based on policy
	switch policy {
	case aiv1alpha1.EvictionPolicyLRU:
		// Sort by last access time (oldest first), then by retention priority (lowest first)
		sort.Slice(candidates, func(i, j int) bool {
			// Get effective timestamps (use creation time if no access time)
			iTime := candidates[i].CreationTimestamp.Time
			if candidates[i].Status.LastAccessTime != nil {
				iTime = candidates[i].Status.LastAccessTime.Time
			}
			jTime := candidates[j].CreationTimestamp.Time
			if candidates[j].Status.LastAccessTime != nil {
				jTime = candidates[j].Status.LastAccessTime.Time
			}

			// Primary sort: older access time first
			if !iTime.Equal(jTime) {
				return iTime.Before(jTime)
			}

			// Secondary sort: lower retention priority first
			iPriority := int32(50) // default
			if candidates[i].Spec.RetentionPriority != nil {
				iPriority = *candidates[i].Spec.RetentionPriority
			}
			jPriority := int32(50)
			if candidates[j].Spec.RetentionPriority != nil {
				jPriority = *candidates[j].Spec.RetentionPriority
			}
			return iPriority < jPriority
		})

	case aiv1alpha1.EvictionPolicyLFU:
		// Sort by access count (lowest first), then by retention priority
		sort.Slice(candidates, func(i, j int) bool {
			if candidates[i].Status.AccessCount != candidates[j].Status.AccessCount {
				return candidates[i].Status.AccessCount < candidates[j].Status.AccessCount
			}
			iPriority := int32(50)
			if candidates[i].Spec.RetentionPriority != nil {
				iPriority = *candidates[i].Spec.RetentionPriority
			}
			jPriority := int32(50)
			if candidates[j].Spec.RetentionPriority != nil {
				jPriority = *candidates[j].Spec.RetentionPriority
			}
			return iPriority < jPriority
		})

	case aiv1alpha1.EvictionPolicyFIFO:
		// Sort by creation time (oldest first), then by retention priority
		sort.Slice(candidates, func(i, j int) bool {
			if !candidates[i].CreationTimestamp.Time.Equal(candidates[j].CreationTimestamp.Time) {
				return candidates[i].CreationTimestamp.Time.Before(candidates[j].CreationTimestamp.Time)
			}
			iPriority := int32(50)
			if candidates[i].Spec.RetentionPriority != nil {
				iPriority = *candidates[i].Spec.RetentionPriority
			}
			jPriority := int32(50)
			if candidates[j].Spec.RetentionPriority != nil {
				jPriority = *candidates[j].Spec.RetentionPriority
			}
			return iPriority < jPriority
		})
	}

	return &candidates[0]
}

// evictCache removes the cache's DaemonSet and updates its status.
func (r *ModelCacheReconciler) evictCache(ctx context.Context, cache *aiv1alpha1.ModelCache) error {
	log := log.FromContext(ctx)

	// Delete the RAM syncer DaemonSet
	dsName := cache.Name + "-ram-syncer"
	ds := &appsv1.DaemonSet{}
	err := r.Get(ctx, types.NamespacedName{Name: dsName, Namespace: cache.Namespace}, ds)
	if err != nil && !errors.IsNotFound(err) {
		return err
	}

	if err == nil {
		log.Info("Deleting DaemonSet for evicted cache", "daemonSet", dsName)
		if err := r.Delete(ctx, ds); err != nil && !errors.IsNotFound(err) {
			return err
		}
	}

	// Update cache status
	now := metav1.Now()
	cache.Status.Phase = aiv1alpha1.ModelCachePhasePending
	cache.Status.EvictionCount++
	cache.Status.ReadyNodes = 0

	// Calculate residency time if we were tracking it
	if cache.Status.ResidentSince != nil {
		residencyDuration := now.Sub(cache.Status.ResidentSince.Time)
		cache.Status.ResidencySeconds += int64(residencyDuration.Seconds())
		cache.Status.ResidentSince = nil
	}

	if err := r.Status().Update(ctx, cache); err != nil {
		return err
	}

	// Record eviction metric
	r.recordEvictionMetric(cache, "")

	r.Recorder.Eventf(cache, corev1.EventTypeWarning, "Evicted",
		"Cache evicted due to storage pressure (eviction count: %d)", cache.Status.EvictionCount)

	return nil
}

// updateCacheAccessTime updates the last access time and access count for a cache.
// Called when a ModelDeployment references this cache.
func (r *ModelCacheReconciler) updateCacheAccessTime(ctx context.Context, cache *aiv1alpha1.ModelCache) error {
	now := metav1.Now()
	cache.Status.LastAccessTime = &now
	cache.Status.AccessCount++
	return r.Status().Update(ctx, cache)
}

// markCacheResident marks the cache as resident in memory and starts tracking residency time.
func (r *ModelCacheReconciler) markCacheResident(ctx context.Context, cache *aiv1alpha1.ModelCache) error {
	if cache.Status.ResidentSince == nil {
		now := metav1.Now()
		cache.Status.ResidentSince = &now
	}
	return nil // Don't update here; let the caller batch status updates
}

// updateCacheMetrics publishes Prometheus metrics for a cache.
func (r *ModelCacheReconciler) updateCacheMetrics(cache *aiv1alpha1.ModelCache, nodeName string) {
	strategy := string(cache.Spec.StorageStrategy)
	cacheName := cache.Name

	// Update size metric
	if cache.Status.CacheSizeBytes > 0 {
		metrics.ModelCacheSizeBytes.WithLabelValues(cacheName, nodeName, strategy).Set(float64(cache.Status.CacheSizeBytes))
	}

	// Update access count metric
	metrics.ModelCacheAccessCount.WithLabelValues(cacheName, nodeName).Set(float64(cache.Status.AccessCount))

	// Update residency time metric
	if cache.Status.ResidentSince != nil {
		residencySeconds := time.Since(cache.Status.ResidentSince.Time).Seconds()
		metrics.ModelCacheResidentSeconds.WithLabelValues(cacheName, nodeName, strategy).Set(residencySeconds)
	} else {
		// Not currently resident
		metrics.ModelCacheResidentSeconds.WithLabelValues(cacheName, nodeName, strategy).Set(0)
	}

	// Update hit rate metric
	if cache.Status.CacheHitRate != "" {
		if hitRate, err := strconv.ParseFloat(cache.Status.CacheHitRate, 64); err == nil {
			metrics.ModelCacheHitRate.WithLabelValues(cacheName, nodeName).Set(hitRate)
		}
	}

	// Update phase metric (set 1 for current phase, 0 for others)
	phases := []string{"Pending", "Initializing", "Provisioning", "Quantizing", "Ready", "Failed"}
	for _, phase := range phases {
		val := 0.0
		if string(cache.Status.Phase) == phase {
			val = 1.0
		}
		metrics.ModelCachePhase.WithLabelValues(cacheName, cache.Namespace, phase).Set(val)
	}
}

// recordEvictionMetric increments the eviction counter for a cache.
func (r *ModelCacheReconciler) recordEvictionMetric(cache *aiv1alpha1.ModelCache, nodeName string) {
	policy := string(cache.Spec.EvictionPolicy)
	if policy == "" {
		policy = "LRU" // default
	}
	metrics.ModelCacheEvictionsTotal.WithLabelValues(cache.Name, nodeName, policy).Inc()
}

// reconcileQuantization handles the quantization phase of the ModelCache lifecycle.
// It is called after the download job succeeds, when spec.quantization is set.
// Lifecycle: Provisioning (download done) → Quantizing → Ready
func (r *ModelCacheReconciler) reconcileQuantization(ctx context.Context, modelCache *aiv1alpha1.ModelCache, pvcName, modelPath string) (ctrl.Result, error) {
	log := log.FromContext(ctx)

	// If already Ready or already has quantization status, skip
	if modelCache.Status.Phase == aiv1alpha1.ModelCachePhaseReady && modelCache.Status.Quantization != nil {
		return ctrl.Result{}, nil
	}

	quantJobName := modelCache.Name + "-quantize"
	quantJob := &batchv1.Job{}
	err := r.Get(ctx, types.NamespacedName{Name: quantJobName, Namespace: modelCache.Namespace}, quantJob)
	if err != nil && errors.IsNotFound(err) {
		// Build and create the quantization job
		builder, builderErr := quantization.GetBuilder(modelCache.Spec.Quantization.Format)
		if builderErr != nil {
			r.Recorder.Event(modelCache, corev1.EventTypeWarning, "QuantizationFailed",
				fmt.Sprintf("Unsupported quantization format: %s", builderErr))
			modelCache.Status.Phase = aiv1alpha1.ModelCachePhaseFailed
			if statusErr := r.Status().Update(ctx, modelCache); statusErr != nil {
				return ctrl.Result{}, statusErr
			}
			return ctrl.Result{}, nil
		}

		params := quantization.JobParams{
			Name:      modelCache.Name,
			Namespace: modelCache.Namespace,
			PVCName:   pvcName,
			ModelPath: modelPath,
			Spec:      modelCache.Spec.Quantization,
		}

		newJob, buildErr := builder.BuildJob(params)
		if buildErr != nil {
			r.Recorder.Event(modelCache, corev1.EventTypeWarning, "QuantizationFailed",
				fmt.Sprintf("Failed to build quantization job: %s", buildErr))
			modelCache.Status.Phase = aiv1alpha1.ModelCachePhaseFailed
			if statusErr := r.Status().Update(ctx, modelCache); statusErr != nil {
				return ctrl.Result{}, statusErr
			}
			return ctrl.Result{}, nil
		}

		// Set owner reference so job status changes trigger reconcile
		if err := ctrl.SetControllerReference(modelCache, newJob, r.Scheme); err != nil {
			return ctrl.Result{}, err
		}

		log.Info("Creating quantization job", "Job", newJob.Name, "format", modelCache.Spec.Quantization.Format)
		if err := r.Create(ctx, newJob); err != nil {
			return ctrl.Result{}, err
		}

		// Transition to Quantizing phase
		modelCache.Status.Phase = aiv1alpha1.ModelCachePhaseQuantizing
		if err := r.Status().Update(ctx, modelCache); err != nil {
			return ctrl.Result{}, err
		}

		r.Recorder.Event(modelCache, corev1.EventTypeNormal, "QuantizationStarted",
			fmt.Sprintf("Quantization job created: format=%s type=%s",
				modelCache.Spec.Quantization.Format,
				modelCache.Spec.Quantization.GGUFType))

		// Requeue after 30s to check job status
		return ctrl.Result{RequeueAfter: 30 * time.Second}, nil
	} else if err != nil {
		return ctrl.Result{}, err
	}

	// Check quantization job status
	if quantJob.Status.Succeeded > 0 {
		log.Info("Quantization job succeeded", "cache", modelCache.Name)

		// Populate quantization status
		ggufType := modelCache.Spec.Quantization.GGUFType
		if ggufType == "" {
			ggufType = "Q4_K_M"
		}
		modelCache.Status.Quantization = &aiv1alpha1.QuantizationStatus{
			Format: string(modelCache.Spec.Quantization.Format),
			Type:   ggufType,
		}
		modelCache.Status.Phase = aiv1alpha1.ModelCachePhaseReady
		if err := r.Status().Update(ctx, modelCache); err != nil {
			return ctrl.Result{}, err
		}

		r.Recorder.Event(modelCache, corev1.EventTypeNormal, "CacheReady",
			fmt.Sprintf("Model quantized (%s/%s) and cached at %s",
				modelCache.Spec.Quantization.Format, ggufType, modelCache.Status.Path))

		// Update quantization metrics
		metrics.QuantizationJobsTotal.WithLabelValues(modelCache.Name, "succeeded").Inc()

		return ctrl.Result{}, nil
	}

	if quantJob.Status.Failed > 0 {
		log.Info("Quantization job failed", "cache", modelCache.Name)
		modelCache.Status.Phase = aiv1alpha1.ModelCachePhaseFailed
		if err := r.Status().Update(ctx, modelCache); err != nil {
			return ctrl.Result{}, err
		}
		r.Recorder.Event(modelCache, corev1.EventTypeWarning, "QuantizationFailed",
			"Quantization job failed - check job logs for details")
		metrics.QuantizationJobsTotal.WithLabelValues(modelCache.Name, "failed").Inc()
		return ctrl.Result{}, nil
	}

	// Job still running — requeue to check later
	return ctrl.Result{RequeueAfter: 30 * time.Second}, nil
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
