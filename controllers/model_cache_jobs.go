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
	ctrl "sigs.k8s.io/controller-runtime"

	aiv1alpha2 "github.com/flexinfer/flexinfer/api/v1alpha2"
)

func (r *ModelReconciler) jobForPrefetch(model *aiv1alpha2.Model, pvcName, destSubdir string) (*batchv1.Job, error) {
	modelID := extractModelFromSource(model.Spec.Source)
	hfOpts := resolveHFDownloadOptions(model)

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

	// If the model config specifies a VAE repo (e.g. madebyollin/sdxl-vae-fp16-fix),
	// pass it to the download script so the VAE is prefetched alongside the model.
	if vaeRepo := model.Spec.ConfigString("vaeRepo", ""); vaeRepo != "" {
		vaeDest := "/models/.vae/" + filepath.Base(vaeRepo)
		envVars = append(envVars,
			corev1.EnvVar{Name: "VAE_REPO", Value: vaeRepo},
			corev1.EnvVar{Name: "VAE_DEST_DIR", Value: vaeDest},
		)
	}

	destSubdir = strings.Trim(destSubdir, "/")
	destDir := "/models/" + destSubdir

	var image string
	var downloadScript string

	if isMlcModelSource(model.Spec.Source) {
		image = ImageDebianSlim
		downloadScript = fmt.Sprintf(`
set -ex
MODEL_ID="%s"
DEST_DIR="%s"
MARKER="$DEST_DIR/.flexinfer_cached"
VAE_REPO="${VAE_REPO:-}"
VAE_DEST_DIR="${VAE_DEST_DIR:-}"

if [ -f "$MARKER" ]; then
    if [ -z "$VAE_REPO" ] || [ -d "$VAE_DEST_DIR" ]; then
        echo "Model already cached at $DEST_DIR"
        exit 0
    fi
    echo "Marker exists but VAE cache is incomplete; downloading VAE assets"
fi

apt-get update && apt-get install -y git git-lfs ca-certificates
git lfs install

mkdir -p "$DEST_DIR"
echo "Cloning $MODEL_ID to $DEST_DIR..."
GIT_LFS_SKIP_SMUDGE=0 git clone "%s/$MODEL_ID" "$DEST_DIR"
touch "$MARKER"
echo "Download complete."
`, modelID, destDir, huggingFaceRepositoryBaseURL)
	} else {
		image = ImagePythonSlim
		downloadScript = fmt.Sprintf(`
set -ex
MODEL_ID="%s"
DEST_DIR="%s"
MARKER="$DEST_DIR/.flexinfer_cached"

if [ -f "$MARKER" ]; then
    echo "Model already cached at $DEST_DIR"
    exit 0
fi

pip install --no-cache-dir huggingface_hub
mkdir -p "$DEST_DIR"
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

# Download additional VAE repo if configured (e.g. madebyollin/sdxl-vae-fp16-fix).
vae_repo = os.environ.get("VAE_REPO", "").strip()
if vae_repo:
    vae_dir = os.environ.get("VAE_DEST_DIR", "")
    print(f"Downloading VAE: {vae_repo} -> {vae_dir}")
    snapshot_download(repo_id=vae_repo, local_dir=vae_dir, local_dir_use_symlinks=False, cache_dir=cache_dir, token=token)
PY
touch "$MARKER"
echo "Download complete."
`, modelID, destDir)
	}

	nodeSelector, tolerations := modelNodeSelectorAndTolerations(model)

	job := buildCacheJob(CacheJobParams{
		Name:      model.Name + "-cache-prefetch",
		Namespace: model.Namespace,
		Labels:    r.labelsForModel(model),
		Annotations: map[string]string{
			AnnotationSource:    model.Spec.Source,
			AnnotationCacheKind: "prefetch",
			AnnotationCachePVC:  pvcName,
			AnnotationCacheDest: destSubdir,
		},
		NodeSelector:  nodeSelector,
		Tolerations:   tolerations,
		BackoffLimit:  DefaultDownloadBackoffLimit,
		RestartPolicy: corev1.RestartPolicyOnFailure,
		ContainerName: "downloader",
		Image:         image,
		Command:       []string{"/bin/sh", "-c"},
		Args:          []string{downloadScript},
		Env:           envVars,
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
				PersistentVolumeClaim: &corev1.PersistentVolumeClaimVolumeSource{
					ClaimName: pvcName,
				},
			},
		}},
	})

	if err := ctrl.SetControllerReference(model, job, r.Scheme); err != nil {
		return nil, err
	}
	return job, nil
}

func (r *ModelReconciler) jobForCacheCheck(model *aiv1alpha2.Model, pvcName, subPath string) (*batchv1.Job, error) {
	subPath = strings.Trim(subPath, "/")
	target := "/models"
	if subPath != "" {
		target = "/models/" + subPath
	}

	script := fmt.Sprintf(`
set -ex
TARGET="%s"
if [ ! -e "$TARGET" ]; then
  echo "Missing path: $TARGET"
  exit 1
fi
if [ -d "$TARGET" ]; then
  if [ -z "$(ls -A "$TARGET" 2>/dev/null)" ]; then
    echo "Directory is empty: $TARGET"
    exit 1
  fi
  echo "Artifact present at directory $TARGET"
  exit 0
fi
if [ ! -s "$TARGET" ]; then
  echo "File is empty: $TARGET"
  exit 1
fi
echo "Artifact present at file $TARGET"
`, target)

	nodeSelector, tolerations := modelNodeSelectorAndTolerations(model)

	job := buildCacheJob(CacheJobParams{
		Name:      model.Name + "-cache-check",
		Namespace: model.Namespace,
		Labels:    r.labelsForModel(model),
		Annotations: map[string]string{
			AnnotationSource:    model.Spec.Source,
			AnnotationCacheKind: "check",
			AnnotationCachePVC:  pvcName,
			AnnotationCachePath: subPath,
		},
		NodeSelector:  nodeSelector,
		Tolerations:   tolerations,
		BackoffLimit:  0,
		RestartPolicy: corev1.RestartPolicyNever,
		ContainerName: "checker",
		Image:         ImageAlpine,
		Command:       []string{"/bin/sh", "-c"},
		Args:          []string{script},
		Resources: corev1.ResourceRequirements{
			Requests: corev1.ResourceList{
				corev1.ResourceCPU:    resource.MustParse("10m"),
				corev1.ResourceMemory: resource.MustParse("32Mi"),
			},
			Limits: corev1.ResourceList{
				corev1.ResourceMemory: resource.MustParse("128Mi"),
			},
		},
		VolumeMounts: []corev1.VolumeMount{{
			Name:      "model-store",
			MountPath: "/models",
		}},
		Volumes: []corev1.Volume{{
			Name: "model-store",
			VolumeSource: corev1.VolumeSource{
				PersistentVolumeClaim: &corev1.PersistentVolumeClaimVolumeSource{
					ClaimName: pvcName,
				},
			},
		}},
	})

	if err := ctrl.SetControllerReference(model, job, r.Scheme); err != nil {
		return nil, err
	}
	return job, nil
}

func (r *ModelReconciler) jobForCacheCopy(model *aiv1alpha2.Model, sourcePVCName, cachePVCName, subPath string) (*batchv1.Job, error) {
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
  echo "Already cached: $MARKER"
  exit 0
fi

if [ ! -e "$SRC" ]; then
  echo "Missing source path: $SRC"
  exit 1
fi

# Copy files individually with sync between each to flush dirty pages
# and avoid OOMKill when copying multi-GB safetensor shards (page
# cache is charged to the container cgroup).
if [ -d "$SRC" ]; then
  mkdir -p "$DST"
  for f in "$SRC"/*; do
    [ -e "$f" ] || continue
    base="$(basename "$f")"
    cp -a "$f" "$DST/$base"
    sync
    echo "Copied: $base"
  done
else
  mkdir -p "$(dirname "$DST")"
  cp -a "$SRC" "$DST"
fi

touch "$MARKER"
echo "Copy complete."
`, src, dst, marker)

	nodeSelector, tolerations := modelNodeSelectorAndTolerations(model)

	job := buildCacheJob(CacheJobParams{
		Name:      model.Name + "-cache-copy",
		Namespace: model.Namespace,
		Labels:    r.labelsForModel(model),
		Annotations: map[string]string{
			AnnotationSource:      model.Spec.Source,
			AnnotationCacheKind:   "copy",
			AnnotationCacheSrcPVC: sourcePVCName,
			AnnotationCachePVC:    cachePVCName,
			AnnotationCachePath:   subPath,
		},
		NodeSelector:  nodeSelector,
		Tolerations:   tolerations,
		BackoffLimit:  1,
		RestartPolicy: corev1.RestartPolicyOnFailure,
		ContainerName: "copier",
		Image:         ImageAlpine,
		Command:       []string{"/bin/sh", "-c"},
		Args:          []string{script},
		Resources: corev1.ResourceRequirements{
			Requests: corev1.ResourceList{
				corev1.ResourceCPU:    resource.MustParse("100m"),
				corev1.ResourceMemory: resource.MustParse("256Mi"),
			},
			Limits: corev1.ResourceList{
				corev1.ResourceMemory: resource.MustParse("16Gi"),
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
					PersistentVolumeClaim: &corev1.PersistentVolumeClaimVolumeSource{
						ClaimName: cachePVCName,
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
