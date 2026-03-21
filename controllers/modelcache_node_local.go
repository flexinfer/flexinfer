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

	// Keep the DaemonSet spec in sync with the desired state.
	desiredDS, err := r.daemonSetForNodeLocal(m, modelPath, hostPath)
	if err != nil {
		return ctrl.Result{}, err
	}

	_, readyNodes, totalNodes, err := r.syncDaemonSet(ctx, ds, desiredDS, m)
	if err != nil {
		return ctrl.Result{}, err
	}

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
		return ctrl.Result{RequeueAfter: requeueLong}, nil
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

if [ -f "$MARKER" ]; then
    echo "Model already synced at $DEST_DIR"
    while true; do sleep 3600; done
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
