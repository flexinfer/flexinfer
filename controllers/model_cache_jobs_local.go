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
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"

	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	"k8s.io/utils/ptr"
	ctrl "sigs.k8s.io/controller-runtime"

	aiv1alpha2 "github.com/flexinfer/flexinfer/api/v1alpha2"
)

// jobForLocalCacheCheck creates a Job that verifies the Local cache hostPath
// directory on the target node contains model files. This prevents the controller
// from marking cache ready when the directory is empty (e.g. after moving a model
// to a new node that has not been pre-populated).
func (r *ModelReconciler) jobForLocalCacheCheck(model *aiv1alpha2.Model) (*batchv1.Job, error) {
	cachePath := resolveLocalCachePath(model)

	script := fmt.Sprintf(`
set -ex
DIR="%s"
if [ ! -d "$DIR" ]; then
  echo "Cache directory does not exist: $DIR"
  exit 1
fi
# Check for at least one .safetensors, .bin, .gguf, or .json file
COUNT=$(find "$DIR" -maxdepth 3 -type f \( -name "*.safetensors" -o -name "*.bin" -o -name "*.gguf" -o -name "*.json" \) 2>/dev/null | head -5 | wc -l)
if [ "$COUNT" -eq 0 ]; then
  echo "Cache directory is empty or contains no model files: $DIR"
  ls -la "$DIR" 2>/dev/null || true
  exit 1
fi
echo "Local cache verified: $DIR ($COUNT+ model files found)"
`, cachePath)

	nodeSelector, tolerations := modelNodeSelectorAndTolerations(model)

	job := buildCacheJob(CacheJobParams{
		Name:      model.Name + "-cache-check",
		Namespace: model.Namespace,
		Labels:    r.labelsForModel(model),
		Annotations: map[string]string{
			AnnotationSource:    model.Spec.Source,
			AnnotationCacheKind: "local-check",
			AnnotationCachePath: cachePath,
		},
		NodeSelector:            nodeSelector,
		Tolerations:             tolerations,
		BackoffLimit:            0,
		RestartPolicy:           corev1.RestartPolicyNever,
		TTLSecondsAfterFinished: ptr.To(int32(300)),
		ContainerName:           "checker",
		Image:                   ImageAlpine,
		Command:                 []string{"/bin/sh", "-c"},
		Args:                    []string{script},
		Resources: corev1.ResourceRequirements{
			Requests: corev1.ResourceList{
				corev1.ResourceCPU:    resource.MustParse("10m"),
				corev1.ResourceMemory: resource.MustParse("32Mi"),
			},
			Limits: corev1.ResourceList{
				corev1.ResourceMemory: resource.MustParse("64Mi"),
			},
		},
		VolumeMounts: []corev1.VolumeMount{{
			Name:      "cache-dir",
			MountPath: cachePath,
		}},
		Volumes: []corev1.Volume{{
			Name: "cache-dir",
			VolumeSource: corev1.VolumeSource{
				HostPath: &corev1.HostPathVolumeSource{
					Path: cachePath,
					Type: hostPathTypePtr(corev1.HostPathDirectoryOrCreate),
				},
			},
		}},
	})

	if err := ctrl.SetControllerReference(model, job, r.Scheme); err != nil {
		return nil, err
	}
	return job, nil
}

func (r *ModelReconciler) jobForLocalCacheStage(model *aiv1alpha2.Model, sourcePVCName, subPath string) (*batchv1.Job, error) {
	subPath = strings.Trim(subPath, "/")
	src := "/src"
	dst := "/models"
	if subPath != "" {
		src = "/src/" + subPath
		dst = "/models/" + subPath
	}

	sum := sha256.Sum256([]byte(model.Spec.Source))
	marker := "/models/.flexinfer_cached_" + hex.EncodeToString(sum[:])

	script := fmt.Sprintf(`
set -ex
SRC="%s"
DST="%s"
MARKER="%s"

if [ -f "$MARKER" ]; then
  echo "Already staged: $MARKER"
  exit 0
fi

if [ ! -e "$SRC" ]; then
  echo "Missing source path: $SRC"
  exit 1
fi

mkdir -p /models
find /models -mindepth 1 -maxdepth 1 ! -name "$(basename "$MARKER")" -exec rm -rf {} +

if [ -d "$SRC" ]; then
  mkdir -p "$DST"
  cp -a "$SRC/." "$DST/"
else
  mkdir -p "$(dirname "$DST")"
  cp -a "$SRC" "$DST"
fi

touch "$MARKER"
echo "Local staging complete."
`, src, dst, marker)

	nodeSelector, tolerations := modelNodeSelectorAndTolerations(model)

	job := buildCacheJob(CacheJobParams{
		Name:      model.Name + "-cache-stage",
		Namespace: model.Namespace,
		Labels:    r.labelsForModel(model),
		Annotations: map[string]string{
			AnnotationSource:      model.Spec.Source,
			AnnotationCacheKind:   "local-stage",
			AnnotationCacheSrcPVC: sourcePVCName,
			AnnotationCachePath:   subPath,
		},
		NodeSelector:  nodeSelector,
		Tolerations:   tolerations,
		BackoffLimit:  1,
		RestartPolicy: corev1.RestartPolicyOnFailure,
		ContainerName: "stager",
		Image:         ImageAlpine,
		Command:       []string{"/bin/sh", "-c"},
		Args:          []string{script},
		Resources: corev1.ResourceRequirements{
			Requests: corev1.ResourceList{
				corev1.ResourceCPU:    resource.MustParse("100m"),
				corev1.ResourceMemory: resource.MustParse("256Mi"),
			},
			Limits: corev1.ResourceList{
				corev1.ResourceMemory: resource.MustParse("2Gi"),
			},
		},
		VolumeMounts: []corev1.VolumeMount{
			{
				Name:      "source",
				MountPath: "/src",
				ReadOnly:  true,
			},
			{
				Name:      "model-store",
				MountPath: "/models",
			},
		},
		Volumes: []corev1.Volume{
			{
				Name: "source",
				VolumeSource: corev1.VolumeSource{
					PersistentVolumeClaim: &corev1.PersistentVolumeClaimVolumeSource{
						ClaimName: sourcePVCName,
					},
				},
			},
			{
				Name: "model-store",
				VolumeSource: corev1.VolumeSource{
					HostPath: &corev1.HostPathVolumeSource{
						Path: resolveLocalCachePath(model),
						Type: hostPathTypePtr(corev1.HostPathDirectoryOrCreate),
					},
				},
			},
		},
	})

	if err := ctrl.SetControllerReference(model, job, r.Scheme); err != nil {
		return nil, err
	}
	return job, nil
}

func (r *ModelReconciler) jobForLocalHFPrefetch(model *aiv1alpha2.Model) (*batchv1.Job, error) {
	modelID := extractModelFromSource(model.Spec.Source)
	hfOpts := resolveHFDownloadOptions(model)
	cachePath := resolveLocalCachePath(model)
	markerSum := sha256.Sum256([]byte(model.Spec.Source))
	markerName := ".flexinfer_cached_" + hex.EncodeToString(markerSum[:])

	envVars := append(
		[]corev1.EnvVar{{Name: "HF_HUB_ENABLE_HF_TRANSFER", Value: "0"}},
		hfCacheEnvVars("/models/.cache/huggingface")...,
	)
	envVars = append(envVars, optionalHFTokenEnvVars()...)
	if len(hfOpts.allowPatterns) > 0 {
		allowJSON, err := json.Marshal(hfOpts.allowPatterns)
		if err != nil {
			return nil, fmt.Errorf("marshal HF allow patterns: %w", err)
		}
		envVars = append(envVars, corev1.EnvVar{Name: "HF_ALLOW_PATTERNS", Value: string(allowJSON)})
	}
	if len(hfOpts.ignorePatterns) > 0 {
		ignoreJSON, err := json.Marshal(hfOpts.ignorePatterns)
		if err != nil {
			return nil, fmt.Errorf("marshal HF ignore patterns: %w", err)
		}
		envVars = append(envVars, corev1.EnvVar{Name: "HF_IGNORE_PATTERNS", Value: string(ignoreJSON)})
	}
	if hfOpts.revision != "" {
		envVars = append(envVars, corev1.EnvVar{Name: "HF_REVISION", Value: hfOpts.revision})
	}

	if vaeRepo := model.Spec.ConfigString("vaeRepo", ""); vaeRepo != "" {
		vaeDest := "/models/.vae/" + filepath.Base(vaeRepo)
		envVars = append(envVars,
			corev1.EnvVar{Name: "VAE_REPO", Value: vaeRepo},
			corev1.EnvVar{Name: "VAE_DEST_DIR", Value: vaeDest},
		)
	}

	script := fmt.Sprintf(`
set -ex
MODEL_ID="%s"
DEST_DIR="/models"
MARKER="/models/%s"
VAE_REPO="${VAE_REPO:-}"
VAE_DEST_DIR="${VAE_DEST_DIR:-}"

if [ -f "$MARKER" ]; then
  if [ -z "$VAE_REPO" ] || [ -d "$VAE_DEST_DIR" ]; then
    echo "Model already cached at $DEST_DIR"
    exit 0
  fi
  echo "Marker exists but VAE cache is incomplete; downloading VAE assets"
fi

mkdir -p "$DEST_DIR" /models/.cache/huggingface
find "$DEST_DIR" -mindepth 1 -maxdepth 1 ! -name ".cache" -exec rm -rf {} +

pip install --no-cache-dir huggingface_hub hf_transfer
MODEL_ID="$MODEL_ID" DEST_DIR="$DEST_DIR" python - <<'PY'
import json
import os

from huggingface_hub import snapshot_download

repo_id = os.environ["MODEL_ID"]
local_dir = os.environ["DEST_DIR"]
token = os.environ.get("HF_TOKEN") or os.environ.get("HUGGINGFACE_HUB_TOKEN")
cache_dir = os.environ.get("HF_HOME")
allow_patterns = json.loads(os.environ.get("HF_ALLOW_PATTERNS", "[]") or "[]")
ignore_patterns = json.loads(os.environ.get("HF_IGNORE_PATTERNS", "[]") or "[]")
revision = (os.environ.get("HF_REVISION") or "").strip() or None

download_kwargs = {
    "repo_id": repo_id,
    "local_dir": local_dir,
    "local_dir_use_symlinks": False,
    "cache_dir": cache_dir,
    "token": token,
}
if allow_patterns:
    download_kwargs["allow_patterns"] = allow_patterns
if ignore_patterns:
    download_kwargs["ignore_patterns"] = ignore_patterns
if revision:
    download_kwargs["revision"] = revision

snapshot_download(**download_kwargs)

vae_repo = os.environ.get("VAE_REPO", "").strip()
if vae_repo:
    vae_dir = os.environ.get("VAE_DEST_DIR", "")
    print(f"Downloading VAE: {vae_repo} -> {vae_dir}")
    snapshot_download(repo_id=vae_repo, local_dir=vae_dir, local_dir_use_symlinks=False, cache_dir=cache_dir, token=token)
PY
touch "$MARKER"
echo "Local HF staging complete."
`, modelID, markerName)

	nodeSelector, tolerations := modelNodeSelectorAndTolerations(model)

	job := buildCacheJob(CacheJobParams{
		Name:      model.Name + "-cache-stage",
		Namespace: model.Namespace,
		Labels:    r.labelsForModel(model),
		Annotations: map[string]string{
			AnnotationSource:    model.Spec.Source,
			AnnotationCacheKind: "local-prefetch",
			AnnotationCachePath: cachePath,
		},
		NodeSelector:            nodeSelector,
		Tolerations:             tolerations,
		BackoffLimit:            DefaultDownloadBackoffLimit,
		RestartPolicy:           corev1.RestartPolicyOnFailure,
		TTLSecondsAfterFinished: ptr.To(int32(300)),
		ContainerName:           "downloader",
		Image:                   ImagePythonSlim,
		Command:                 []string{"/bin/sh", "-c"},
		Args:                    []string{script},
		Env:                     envVars,
		Resources: corev1.ResourceRequirements{
			Requests: corev1.ResourceList{
				corev1.ResourceCPU:    resource.MustParse("100m"),
				corev1.ResourceMemory: resource.MustParse("1Gi"),
			},
			Limits: corev1.ResourceList{
				corev1.ResourceMemory: resource.MustParse("8Gi"),
			},
		},
		VolumeMounts: []corev1.VolumeMount{{
			Name:      "model-store",
			MountPath: "/models",
		}},
		Volumes: []corev1.Volume{{
			Name: "model-store",
			VolumeSource: corev1.VolumeSource{
				HostPath: &corev1.HostPathVolumeSource{
					Path: cachePath,
					Type: hostPathTypePtr(corev1.HostPathDirectoryOrCreate),
				},
			},
		}},
	})

	if err := ctrl.SetControllerReference(model, job, r.Scheme); err != nil {
		return nil, err
	}
	return job, nil
}

func optionalHFTokenEnvVars() []corev1.EnvVar {
	return []corev1.EnvVar{
		{
			Name: "HF_TOKEN",
			ValueFrom: &corev1.EnvVarSource{
				SecretKeyRef: &corev1.SecretKeySelector{
					LocalObjectReference: corev1.LocalObjectReference{Name: "hf-token"},
					Key:                  "HF_TOKEN",
					Optional:             ptr.To(true),
				},
			},
		},
		{
			Name: "HUGGINGFACE_HUB_TOKEN",
			ValueFrom: &corev1.EnvVarSource{
				SecretKeyRef: &corev1.SecretKeySelector{
					LocalObjectReference: corev1.LocalObjectReference{Name: "hf-token"},
					Key:                  "HF_TOKEN",
					Optional:             ptr.To(true),
				},
			},
		},
	}
}
