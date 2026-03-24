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
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/utils/ptr"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/log"

	aiv1alpha1 "github.com/flexinfer/flexinfer/api/v1alpha1"
	"github.com/flexinfer/flexinfer/pkg/metrics"
)

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

		// Gate on PVC readiness — don't create jobs against a PVC that is
		// still being provisioned or is being deleted (Terminating).
		if pvc.DeletionTimestamp != nil {
			log.Info("PVC is terminating, waiting for cleanup", "pvc", pvcName)
			return ctrl.Result{RequeueAfter: requeueMedium}, nil
		}
		if pvc.Status.Phase != corev1.ClaimBound {
			log.Info("PVC not yet bound, waiting", "pvc", pvcName, "phase", pvc.Status.Phase)
			return ctrl.Result{RequeueAfter: requeueMedium}, nil
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
		// If download already completed, the job was GC'd by TTL — continue to next phase.
		if downloadGCdShouldProceed(&modelCache.Status) {
			log.Info("Download job GC'd but download already complete, skipping re-creation",
				"cache", modelCache.Name, "phase", modelCache.Status.Phase)
			modelCache.Status.Path = fmt.Sprintf("%s:%s", pvcName, modelPath)
			if modelCache.Spec.Abliteration != nil {
				return r.reconcileAbliteration(ctx, modelCache, pvcName, modelPath)
			}
			if modelCache.Spec.Finetune != nil {
				return r.reconcileFinetune(ctx, modelCache, pvcName, modelPath)
			}
			if modelCache.Spec.Quantization != nil {
				return r.reconcileQuantization(ctx, modelCache, pvcName, modelPath)
			}
			if modelCache.Spec.Publish != nil {
				return r.reconcilePublish(ctx, modelCache, pvcName, modelPath)
			}
			if modelCache.Status.Phase != aiv1alpha1.ModelCachePhaseReady {
				modelCache.Status.Phase = aiv1alpha1.ModelCachePhaseReady
				if err := r.Status().Update(ctx, modelCache); err != nil {
					return ctrl.Result{}, err
				}
			}
			return ctrl.Result{}, nil
		}

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
		// Record download job duration metric
		if job.Status.StartTime != nil && job.Status.CompletionTime != nil {
			dur := job.Status.CompletionTime.Sub(job.Status.StartTime.Time).Seconds()
			metrics.ModelCacheJobDurationSeconds.WithLabelValues(modelCache.Name, modelCache.Namespace, "download", "succeeded").Observe(dur)
		}

		// Path includes both PVC name and model subdirectory
		modelCache.Status.Path = fmt.Sprintf("%s:%s", pvcName, modelPath)

		// Set OCI-specific status fields
		if isOCISource(modelCache.Spec.Source) {
			now := metav1.Now()
			modelCache.Status.OCIPulledAt = &now
			modelCache.Status.OCIRegistry = extractOCIRegistry(modelCache.Spec.Source)
		}

		// If abliteration is requested, handle it before finetune/quantization
		if modelCache.Spec.Abliteration != nil {
			return r.reconcileAbliteration(ctx, modelCache, pvcName, modelPath)
		}

		// If finetuning is requested, handle it before quantization
		if modelCache.Spec.Finetune != nil {
			return r.reconcileFinetune(ctx, modelCache, pvcName, modelPath)
		}

		// If quantization is requested, handle it before marking Ready
		if modelCache.Spec.Quantization != nil {
			return r.reconcileQuantization(ctx, modelCache, pvcName, modelPath)
		}

		// If publishing is requested, handle it before marking Ready
		if modelCache.Spec.Publish != nil {
			return r.reconcilePublish(ctx, modelCache, pvcName, modelPath)
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
		metrics.ModelCacheJobFailuresTotal.WithLabelValues(modelCache.Name, modelCache.Namespace, "download_failed").Inc()
		modelCache.Status.Phase = aiv1alpha1.ModelCachePhaseFailed
		if err := r.Status().Update(ctx, modelCache); err != nil {
			return ctrl.Result{}, err
		}
		r.Recorder.Event(modelCache, corev1.EventTypeWarning, "CacheFailed",
			"Model download job failed - check job logs for details")
	}

	// Emit metrics for SharedPVC caches as well (the Memory strategy already does this).
	r.updateCacheMetrics(modelCache, "")

	return ctrl.Result{}, nil
}

func (r *ModelCacheReconciler) pvcForModelCache(m *aiv1alpha1.ModelCache) (*corev1.PersistentVolumeClaim, error) {
	// Use ReadWriteOnce when all workloads are pinned to a single node via
	// nodeSelector, or for node-local storage classes. RWO avoids the Longhorn
	// NFS share manager layer, giving direct block device I/O. RWX is only
	// needed when pods may be scheduled on different nodes.
	modes := []corev1.PersistentVolumeAccessMode{corev1.ReadWriteMany}
	if len(m.Spec.NodeSelector) > 0 {
		modes = []corev1.PersistentVolumeAccessMode{corev1.ReadWriteOnce}
	}
	if m.Spec.ClusterStorageClassName != nil && *m.Spec.ClusterStorageClassName == "local-path" {
		modes = []corev1.PersistentVolumeAccessMode{corev1.ReadWriteOnce}
	}

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
			Resources: corev1.VolumeResourceRequirements{
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

func isLocalSource(source string) bool {
	return strings.HasPrefix(source, "local://")
}

func parseLocalSource(source string) string {
	// local:// paths are relative to the mounted model store root ("/models").
	source = strings.TrimPrefix(source, "local://")
	source = strings.TrimPrefix(source, "/")
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

// downloadGCdShouldProceed returns true when a GC'd download job can be safely
// skipped (i.e. download already completed). Status.Path is the definitive signal
// that download completed in a prior reconcile cycle. Phase alone is unreliable
// during controller rollouts — the old pod may advance the phase before the new
// pod starts, creating a race where the download job is GC'd but no downstream
// evidence (Path) was recorded.
func downloadGCdShouldProceed(status *aiv1alpha1.ModelCacheStatus) bool {
	return status.Path != ""
}

func (r *ModelCacheReconciler) jobForDownload(m *aiv1alpha1.ModelCache, pvcName, modelPath string) (*batchv1.Job, error) {
	// Resolve download spec with defaults
	memoryGB := int32(DefaultDownloadMemoryGB)
	backoffLimit := DefaultDownloadBackoffLimit
	if m.Spec.Download != nil {
		if m.Spec.Download.MaxMemoryGB != nil {
			memoryGB = *m.Spec.Download.MaxMemoryGB
		}
		if m.Spec.Download.BackoffLimit != nil {
			backoffLimit = *m.Spec.Download.BackoffLimit
		}
	}

	// Determine hf_transfer: explicit override > auto (on when memory >= threshold)
	hfTransferEnabled := memoryGB >= HFTransferAutoThresholdGB
	if m.Spec.Download != nil && m.Spec.Download.HFTransfer != nil {
		hfTransferEnabled = *m.Spec.Download.HFTransfer
	}
	hfTransferValue := "0"
	if hfTransferEnabled {
		hfTransferValue = "1"
	}

	// 1. Prepare Environment Variables
	// HF_HUB_ENABLE_HF_TRANSFER: deprecated in huggingface_hub >=1.7 but still respected by older versions.
	// HF_HUB_DISABLE_XET: disables the xet protocol (replaced hf_transfer in huggingface_hub >=1.7).
	//   The xet client opens 48-64 concurrent connections which can overwhelm constrained WANs.
	envVars := []corev1.EnvVar{
		{
			Name:  "HF_HUB_ENABLE_HF_TRANSFER",
			Value: hfTransferValue,
		},
		{
			Name:  "HF_HUB_DISABLE_XET",
			Value: "1",
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

	if isLocalSource(m.Spec.Source) {
		// local:// sources are paths that should already exist in the mounted model store.
		// If the source is already under the destination dir, just verify it exists (avoid copying onto itself).
		image = ImageAlpine
		srcRel := parseLocalSource(m.Spec.Source)
		downloadScript = fmt.Sprintf(`
set -ex
SRC_REL="%s"
MODEL_PATH="%s"
DEST_DIR="/models/%s"
SRC_PATH="/models/%s"

case "$SRC_REL" in
  "$MODEL_PATH"|"$MODEL_PATH"/*)
    if [ ! -e "$SRC_PATH" ]; then
      echo "Local source missing: $SRC_PATH"
      exit 1
    fi
    if [ -d "$SRC_PATH" ]; then
      test "$(ls -A "$SRC_PATH" 2>/dev/null)" || (echo "Local source directory is empty: $SRC_PATH" && exit 1)
    else
      test -s "$SRC_PATH" || (echo "Local source file is empty: $SRC_PATH" && exit 1)
    fi
    echo "Local source already present under $DEST_DIR"
    exit 0
    ;;
esac

if [ ! -e "$SRC_PATH" ]; then
  echo "Local source missing: $SRC_PATH"
  exit 1
fi

mkdir -p "$DEST_DIR"
if [ -d "$SRC_PATH" ]; then
  cp -a "$SRC_PATH/." "$DEST_DIR/"
else
  cp -a "$SRC_PATH" "$DEST_DIR/"
fi

echo "Local sync complete."
ls -la "$DEST_DIR"
`, srcRel, modelPath, modelPath, srcRel)
	} else if isMlcModel(m.Spec.Source) {
		// MLC-LLM models require git clone with LFS support
		// Use debian:bookworm-slim as stable base with apt-get support
		image = ImageDebianSlim
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
		image = ImagePythonSlim
		downloadScript = fmt.Sprintf(`
set -ex
MODEL_ID="%s"
DEST_DIR="/models/%s"
MARKER="$DEST_DIR/.download_complete"

# Skip if marker exists AND weight files are present.
# A previous quantization retry may have cleaned up source weights,
# leaving the marker but no actual model files.
WEIGHT_COUNT=0
if [ -d "$DEST_DIR" ]; then
    WEIGHT_COUNT=$(find "$DEST_DIR" -maxdepth 1 \( -name '*.safetensors' -o -name '*.bin' -o -name '*.pt' -o -name '*.gguf' \) 2>/dev/null | wc -l)
fi
if [ -f "$MARKER" ] && [ "$WEIGHT_COUNT" -gt 0 ]; then
    echo "Model already cached at $DEST_DIR ($WEIGHT_COUNT weight files)"
    exit 0
elif [ -f "$MARKER" ] && [ "$WEIGHT_COUNT" -eq 0 ]; then
    echo "WARNING: Marker exists but no weight files found — re-downloading"
    rm -f "$MARKER"
fi

pip install --no-cache-dir huggingface_hub hf_transfer
# HF_HUB_ENABLE_HF_TRANSFER controlled via env var.
# Auto-enabled when download container has >= 16Gi memory.
# hf_transfer uses ~4-8Gi for parallel connections on large models.
echo "Downloading $MODEL_ID to $DEST_DIR (hf_transfer=$HF_HUB_ENABLE_HF_TRANSFER)..."
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

# Verify weight files were actually downloaded before marking complete.
WEIGHT_COUNT=$(find "$DEST_DIR" -maxdepth 1 \( -name '*.safetensors' -o -name '*.bin' -o -name '*.pt' -o -name '*.gguf' \) 2>/dev/null | wc -l)
if [ "$WEIGHT_COUNT" -eq 0 ]; then
    echo "ERROR: Download completed but no weight files found in $DEST_DIR"
    exit 1
fi
touch "$MARKER"
echo "Download complete ($WEIGHT_COUNT weight files)."
`, modelID, modelPath)
	}

	// Download jobs don't need GPU — let K8s schedule them wherever the
	// Longhorn volume replica lives for local I/O. Only add GPU node
	// toleration so the pod CAN land on a GPU node if that's where the
	// volume is. Propagate nodeSelector from spec for node-local PVCs.
	tolerations := []corev1.Toleration{{
		Key:      "dedicated",
		Operator: corev1.TolerationOpEqual,
		Value:    "gpu",
		Effect:   corev1.TaintEffectNoSchedule,
	}}

	memoryLimit := resource.MustParse(fmt.Sprintf("%dGi", memoryGB))

	// Optional job deadline
	var activeDeadlineSeconds *int64
	if m.Spec.Download != nil && m.Spec.Download.TimeoutSeconds != nil {
		activeDeadlineSeconds = m.Spec.Download.TimeoutSeconds
	}

	job := &batchv1.Job{
		ObjectMeta: metav1.ObjectMeta{
			Name:      m.Name + "-downloader",
			Namespace: m.Namespace,
			Labels: map[string]string{
				"app.kubernetes.io/name":      "flexinfer",
				"app.kubernetes.io/component": "downloader",
				"app.kubernetes.io/instance":  m.Name,
			},
		},
		Spec: batchv1.JobSpec{
			BackoffLimit:          &backoffLimit,
			ActiveDeadlineSeconds: activeDeadlineSeconds,
			Template: corev1.PodTemplateSpec{
				Spec: corev1.PodSpec{
					RestartPolicy: corev1.RestartPolicyOnFailure,
					Tolerations:   tolerations,
					NodeSelector:  m.Spec.NodeSelector,
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
								corev1.ResourceMemory: memoryLimit,
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
	orasImage := ImageORAS
	if img, ok := os.LookupEnv("ORAS_DOWNLOADER_IMAGE"); ok && img != "" {
		orasImage = img
	}

	// Extract registry host for health check
	registryHost := extractOCIRegistry(m.Spec.Source)

	// Use --insecure for .lan registries (self-signed TLS)
	insecureFlag := ""
	if strings.HasSuffix(registryHost, ".lan") {
		insecureFlag = "--insecure"
	}

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

# Login to OCI registry if credentials are provided
if [ -n "${OCI_USERNAME:-}" ] && [ -n "${OCI_PASSWORD:-}" ]; then
    echo "Logging into OCI registry $REGISTRY_HOST..."
    oras login %s "$REGISTRY_HOST" -u "$OCI_USERNAME" -p "$OCI_PASSWORD"
fi

# Registry health check with retry
echo "Checking registry connectivity to $REGISTRY_HOST..."
for i in $(seq 1 $MAX_RETRIES); do
    if oras repo tags "$MODEL_REF" --last 1 %s >/dev/null 2>&1; then
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
    if oras pull "$MODEL_REF" -o "$DEST_DIR" %s; then
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
`, registryRef, modelPath, registryHost, insecureFlag, insecureFlag, insecureFlag)

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

	// Support OCI auth via Opaque secret (OCI_USERNAME/OCI_PASSWORD keys)
	var envVars []corev1.EnvVar
	if m.Spec.SecretRef != nil && *m.Spec.SecretRef != "" &&
		(m.Spec.OCIRegistrySecretRef == nil || *m.Spec.OCIRegistrySecretRef == "") {
		envVars = append(envVars,
			corev1.EnvVar{
				Name: "OCI_USERNAME",
				ValueFrom: &corev1.EnvVarSource{
					SecretKeyRef: &corev1.SecretKeySelector{
						LocalObjectReference: corev1.LocalObjectReference{Name: *m.Spec.SecretRef},
						Key:                  "OCI_USERNAME",
						Optional:             ptr.To(true),
					},
				},
			},
			corev1.EnvVar{
				Name: "OCI_PASSWORD",
				ValueFrom: &corev1.EnvVarSource{
					SecretKeyRef: &corev1.SecretKeySelector{
						LocalObjectReference: corev1.LocalObjectReference{Name: *m.Spec.SecretRef},
						Key:                  "OCI_PASSWORD",
						Optional:             ptr.To(true),
					},
				},
			},
		)
	}

	// BackoffLimit controls Kubernetes-level job retries (in addition to in-script retries)
	backoffLimit := DefaultDownloadBackoffLimit

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
						Env:          envVars,
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
