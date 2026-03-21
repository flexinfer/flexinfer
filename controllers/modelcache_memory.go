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

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/log"

	aiv1alpha1 "github.com/flexinfer/flexinfer/api/v1alpha1"
)

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

	// Keep the DaemonSet spec in sync with the desired state.
	desiredDS, err := r.daemonSetForMemory(m, modelPath, shmBasePath)
	if err != nil {
		return ctrl.Result{}, err
	}

	dsNeedsUpdate, readyNodes, totalNodes, err := r.syncDaemonSet(ctx, ds, desiredDS, m)
	if err != nil {
		return ctrl.Result{}, err
	}

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
		return ctrl.Result{RequeueAfter: requeueShort}, nil
	}

	// Requeue to monitor DaemonSet status during provisioning
	if m.Status.Phase != aiv1alpha1.ModelCachePhaseReady {
		return ctrl.Result{RequeueAfter: requeueLong}, nil
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
		image = ImageAlpine

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
		image = ImageORAS
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
		image = ImageDebianSlim
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
		image = ImagePythonSlim
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
