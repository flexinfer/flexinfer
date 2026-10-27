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

// legacyPrefetchMarkerName is the completion marker the SharedPVC prefetch Job
// wrote in /models/<model>/ before it carried the source and download options.
// The MLC git clone still writes it.
const legacyPrefetchMarkerName = ".flexinfer_cached"

// prefetchMarkerName names the completion marker the SharedPVC prefetch Job
// writes in /models/<model>/. An HF snapshot download keys it like the Local
// stage, so a ggufFile rename, a pattern change, an hfRevision bump or a source
// change names a marker the PVC does not hold yet. The MLC git clone fetches
// the whole repo whatever the options say, so it keeps the bare marker.
func prefetchMarkerName(source string, opts hfDownloadOptions) (string, error) {
	if isMlcModelSource(source) {
		return legacyPrefetchMarkerName, nil
	}
	return hfCacheMarkerName(source, opts)
}

// prefetchJobMarkerName returns the marker an existing prefetch Job was built
// to write. Prefetch Jobs have no TTL, so ones created before
// AnnotationCacheMarker existed are still around; their marker is rebuilt from
// the source annotation and the download options in their env.
func prefetchJobMarkerName(job *batchv1.Job) (string, error) {
	if name, ok := job.Annotations[AnnotationCacheMarker]; ok {
		return name, nil
	}
	var opts hfDownloadOptions
	for _, container := range job.Spec.Template.Spec.Containers {
		for _, env := range container.Env {
			var err error
			switch env.Name {
			case "HF_ALLOW_PATTERNS":
				err = json.Unmarshal([]byte(env.Value), &opts.allowPatterns)
			case "HF_IGNORE_PATTERNS":
				err = json.Unmarshal([]byte(env.Value), &opts.ignorePatterns)
			case "HF_REVISION":
				opts.revision = env.Value
			}
			if err != nil {
				// A pattern list that does not parse vouches for no file set.
				return "", nil
			}
		}
	}
	return prefetchMarkerName(job.Annotations[AnnotationSource], opts)
}

// prefetchJobStale reports whether an existing prefetch Job was built for a
// different source or different download options than the model's spec
// resolves to now. The Job never expires, so without the marker comparison its
// success would keep standing for whatever file set it fetched. Jobs without
// annotations were not built by this controller and are read as before.
func prefetchJobStale(model *aiv1alpha2.Model, job *batchv1.Job) (bool, error) {
	if job.Annotations == nil {
		return false, nil
	}
	if job.Annotations[AnnotationSource] != model.Spec.Source {
		return true, nil
	}
	got, err := prefetchJobMarkerName(job)
	if err != nil {
		return false, err
	}
	want, err := prefetchMarkerName(model.Spec.Source, resolveHFDownloadOptions(model))
	if err != nil {
		return false, err
	}
	return got != want, nil
}

// jobForPrefetch builds the SharedPVC prefetch Job. adoptLegacyMarker lets the
// Job adopt a bare legacy marker it finds, after checking the files on disk;
// the caller turns it off when the Job replaces one built for another source,
// because the bare marker does not say which repo it was written for.
func (r *ModelReconciler) jobForPrefetch(model *aiv1alpha2.Model, pvcName, destSubdir string, adoptLegacyMarker bool) (*batchv1.Job, error) {
	modelID := extractModelFromSource(model.Spec.Source)
	hfOpts := resolveHFDownloadOptions(model)
	markerName, err := prefetchMarkerName(model.Spec.Source, hfOpts)
	if err != nil {
		return nil, err
	}

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

	// EXPECTED_FILES names the files the spec requires (ggufFile, a relative
	// mmproj). The legacy-marker check requires them on disk, and the download
	// refuses to write its marker without them, the same contract as the Local
	// stage.
	if expected := expectedHFCacheFiles(model); len(expected) > 0 {
		envVars = append(envVars, corev1.EnvVar{
			Name:  "EXPECTED_FILES",
			Value: strings.Join(expected, "\n"),
		})
	}

	destSubdir = strings.Trim(destSubdir, "/")
	destDir := "/models/" + destSubdir
	envVars = append(envVars, transformerSideArtifactEnvVars(model, destDir)...)

	var image string
	var downloadScript string

	if isMlcModelSource(model.Spec.Source) {
		image = ImageDebianSlim
		downloadScript = cacheSizeReportFunc + fmt.Sprintf(`
set -ex
MODEL_ID="%s"
DEST_DIR="%s"
MARKER="$DEST_DIR/.flexinfer_cached"
VAE_REPO="${VAE_REPO:-}"
VAE_DEST_DIR="${VAE_DEST_DIR:-}"

if [ -f "$MARKER" ]; then
    if [ -z "$VAE_REPO" ] || [ -d "$VAE_DEST_DIR" ]; then
        echo "Model already cached at $DEST_DIR"
        report_cache_size "$DEST_DIR"
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
report_cache_size "$DEST_DIR"
echo "Download complete."
`, modelID, destDir, huggingFaceRepositoryBaseURL)
	} else {
		image = ImagePythonSlim
		downloadScript = cacheSizeReportFunc + stageTransformerSideArtifactFunc + fmt.Sprintf(`
set -ex
MODEL_ID="%s"
DEST_DIR="%s"
MARKER="$DEST_DIR/%s"
LEGACY_MARKER="$DEST_DIR/%s"
ADOPT_LEGACY_MARKER="%t"
EXPECTED_FILES="${EXPECTED_FILES:-}"

# A cache fetched before the marker carried the source and download options
# holds only the bare LEGACY_MARKER. Adopt it without downloading while the
# files on disk still satisfy the current options. The first Job to find it
# consumes it either way; a cache not adopted falls through to
# snapshot_download, which skips every file whose local download metadata or
# hash already matches the repo.
if [ ! -f "$MARKER" ] && [ -f "$LEGACY_MARKER" ]; then
  if [ "$ADOPT_LEGACY_MARKER" != "true" ]; then
    echo "Legacy cache marker not adopted, re-fetching: it was written before a source change"
  elif DEST_DIR="$DEST_DIR" python - <<'PY'
import fnmatch
import json
import os
import sys

root = os.environ["DEST_DIR"]
expected = os.environ.get("EXPECTED_FILES", "").split()
allow_patterns = json.loads(os.environ.get("HF_ALLOW_PATTERNS", "[]") or "[]")
revision = (os.environ.get("HF_REVISION") or "").strip()
# huggingface_hub records the commit of every file it writes into a local_dir
# as the first line of .cache/huggingface/download/<file>.metadata. A quantizer
# may since have deleted the downloaded weights and written its own output
# beside them, so the records, not the directory listing, say what was fetched.
metadata_dir = os.path.join(root, ".cache", "huggingface", "download")


def refuse(reason):
    print(f"Legacy cache marker not adopted, re-fetching: {reason}", flush=True)
    sys.exit(1)


on_disk = []
for dirpath, dirnames, filenames in os.walk(root):
    if dirpath == root:
        dirnames[:] = [d for d in dirnames if d not in (".cache", ".transformer")]
    for name in filenames:
        rel = os.path.relpath(os.path.join(dirpath, name), root)
        if not rel.startswith(".flexinfer_cached"):
            on_disk.append(rel)
if not on_disk:
    refuse("the cache directory holds no files")

recorded = {}
for dirpath, _, filenames in os.walk(metadata_dir):
    for name in filenames:
        if name.endswith(".metadata"):
            path = os.path.join(dirpath, name)
            with open(path) as fh:
                recorded[os.path.relpath(path, metadata_dir)[: -len(".metadata")]] = fh.readline().strip()

for rel in expected:
    path = os.path.join(root, rel)
    if not os.path.isfile(path) or os.path.getsize(path) == 0:
        refuse(f"required file {rel} is missing or empty")

# Mirrors huggingface_hub's filter_repo_objects: fnmatchcase on the repo path,
# with a trailing slash meaning everything under that directory.
fetched = set(on_disk) | set(recorded)
for pattern in allow_patterns:
    glob = pattern + "*" if pattern.endswith("/") else pattern
    if not any(fnmatch.fnmatchcase(rel, glob) for rel in fetched):
        refuse(f"allow pattern {pattern} matches no fetched file")

if revision:
    if not recorded:
        refuse(f"no download metadata records a revision, spec pins {revision}")
    for rel, commit in sorted(recorded.items()):
        if commit != revision:
            refuse(f"{rel} was fetched at revision {commit or 'unknown'}, spec pins {revision}")
PY
  then
    mv "$LEGACY_MARKER" "$MARKER"
    echo "Adopted legacy cache marker for the current download options"
  fi
  rm -f "$LEGACY_MARKER"
fi

if [ -f "$MARKER" ]; then
    # The marker vouches for the snapshot only; fetch a missing transformer
    # side artifact alone.
    if transformer_side_artifact_missing; then
        echo "Marker exists but the transformer side artifact is missing; downloading it alone"
        pip install --no-cache-dir huggingface_hub
        stage_transformer_side_artifact
    fi
    echo "Model already cached at $DEST_DIR"
    report_cache_size "$DEST_DIR"
    exit 0
fi

# The directory is not wiped: snapshot_download fetches only what differs.
# It may still overwrite files another option set's marker vouches for, so
# those markers go before it starts.
mkdir -p "$DEST_DIR"
rm -f "$DEST_DIR"/.flexinfer_cached_*
pip install --no-cache-dir huggingface_hub
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

# snapshot_download returns successfully when allow_patterns matched nothing
# (a ggufFile typo, a gated repo without HF_TOKEN); refuse the marker then.
for rel in $EXPECTED_FILES; do
    if [ ! -s "$DEST_DIR/$rel" ]; then
        echo "FATAL: refusing to write marker: required file '$rel' is missing or empty under $DEST_DIR" >&2
        exit 1
    fi
done

stage_transformer_side_artifact
touch "$MARKER"
report_cache_size "$DEST_DIR"
echo "Download complete."
`, modelID, destDir, markerName, legacyPrefetchMarkerName, adoptLegacyMarker)
	}

	nodeSelector, tolerations := modelNodeSelectorAndTolerations(model)

	job := buildCacheJob(CacheJobParams{
		Name:      model.Name + "-cache-prefetch",
		Namespace: model.Namespace,
		Labels:    r.labelsForModel(model),
		Annotations: map[string]string{
			AnnotationSource:      model.Spec.Source,
			AnnotationCacheKind:   "prefetch",
			AnnotationCachePVC:    pvcName,
			AnnotationCacheDest:   destSubdir,
			AnnotationCacheMarker: markerName,
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

	script := cacheSizeReportFunc + fmt.Sprintf(`
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
  report_cache_size "$TARGET"
  exit 0
fi
if [ ! -s "$TARGET" ]; then
  echo "File is empty: $TARGET"
  exit 1
fi
echo "Artifact present at file $TARGET"
report_cache_size "$TARGET"
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

func (r *ModelReconciler) jobForCacheCopy(model *aiv1alpha2.Model, sourcePVCName, cachePVCName, cachePvcUID, subPath string, sourceReady *cacheSourceReadyStatus) (*batchv1.Job, error) {
	subPath = strings.Trim(subPath, "/")
	src := "/src"
	dst := "/models"
	if subPath != "" {
		src = "/src/" + subPath
		dst = "/models/" + subPath
	}

	sum := sha256.Sum256([]byte(model.Spec.Source))
	marker := "/models/.flexinfer_cached_" + hex.EncodeToString(sum[:])

	script := cacheSizeReportFunc + fmt.Sprintf(`
set -ex
SRC="%s"
DST="%s"
MARKER="%s"

if [ -f "$MARKER" ]; then
  echo "Already cached: $MARKER"
  report_cache_size "$DST"
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
report_cache_size "$DST"
echo "Copy complete."
`, src, dst, marker)

	nodeSelector, tolerations := modelNodeSelectorAndTolerations(model)

	annotations := map[string]string{
		AnnotationSource:      model.Spec.Source,
		AnnotationCacheKind:   "copy",
		AnnotationCacheSrcPVC: sourcePVCName,
		AnnotationCachePVC:    cachePVCName,
		AnnotationCachePvcUID: cachePvcUID,
		AnnotationCachePath:   subPath,
	}
	if sourceReady != nil && sourceReady.Ready {
		annotations[AnnotationCacheSourceReadyJob] = sourceReady.JobName
		annotations[AnnotationCacheSourceReadyUID] = sourceReady.UID
		annotations[AnnotationCacheSourceReadyCompletedAt] = sourceReady.CompletedAt
	}

	job := buildCacheJob(CacheJobParams{
		Name:          model.Name + "-cache-copy",
		Namespace:     model.Namespace,
		Labels:        r.labelsForModel(model),
		Annotations:   annotations,
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
