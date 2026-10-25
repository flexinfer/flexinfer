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
	"path"
	"strings"

	corev1 "k8s.io/api/core/v1"

	aiv1alpha2 "github.com/flexinfer/flexinfer/api/v1alpha2"
)

// transformerSideArtifactDir is where an HF cache stage puts a diffusers
// Model's single-file transformer, beside the snapshot:
// <model cache dir>/.transformer/<transformerFile>. server-diffusers.py looks
// for it at the same place under LOCAL_MODEL_PATH.
const transformerSideArtifactDir = ".transformer"

// transformerSideArtifact is a single file fetched from a second HF repo, e.g.
// a GGUF transformer for a diffusers pipeline whose other components come from
// spec.source (config transformerRepo, transformerFile, transformerRevision).
type transformerSideArtifact struct {
	repo     string
	file     string
	revision string
}

// resolveTransformerSideArtifact returns the transformer a Model stages beside
// its HF snapshot. A file path that leaves the destination directory names no
// artifact. The cache marker vouches for the snapshot only, so stages check the
// side file by presence.
func resolveTransformerSideArtifact(model *aiv1alpha2.Model) (transformerSideArtifact, bool) {
	cfg := model.Spec.GetConfigMap()
	artifact := transformerSideArtifact{
		repo:     configStringValue(cfg, "transformerRepo"),
		file:     strings.TrimLeft(configStringValue(cfg, "transformerFile"), "/"),
		revision: configStringValue(cfg, "transformerRevision"),
	}
	if artifact.repo == "" || artifact.file == "" {
		return transformerSideArtifact{}, false
	}
	for _, part := range strings.Split(artifact.file, "/") {
		if part == ".." {
			return transformerSideArtifact{}, false
		}
	}
	return artifact, true
}

// transformerSideArtifactEnvVars passes the side artifact to a cache stage
// script whose model directory is modelDir. It returns nil without one.
func transformerSideArtifactEnvVars(model *aiv1alpha2.Model, modelDir string) []corev1.EnvVar {
	artifact, ok := resolveTransformerSideArtifact(model)
	if !ok {
		return nil
	}
	return []corev1.EnvVar{
		{Name: "TRANSFORMER_REPO", Value: artifact.repo},
		{Name: "TRANSFORMER_FILE", Value: artifact.file},
		{Name: "TRANSFORMER_REVISION", Value: artifact.revision},
		{Name: "TRANSFORMER_DEST_DIR", Value: path.Join(modelDir, transformerSideArtifactDir)},
	}
}

// stageTransformerSideArtifactFunc defines the shell helpers the HF cache
// scripts use for the side artifact. stage_transformer_side_artifact fetches
// TRANSFORMER_FILE from TRANSFORMER_REPO at TRANSFORMER_REVISION into
// TRANSFORMER_DEST_DIR unless it is already there and non-empty. It first
// removes whatever else that directory holds, so a changed transformerFile (an
// A/B quant) replaces the old file instead of piling up beside it, and it
// fails the Job when the file is still missing afterwards. Both helpers are
// no-ops without TRANSFORMER_FILE. huggingface_hub must already be installed.
const stageTransformerSideArtifactFunc = `
transformer_side_artifact_missing() {
  [ -n "${TRANSFORMER_FILE:-}" ] && [ ! -s "$TRANSFORMER_DEST_DIR/$TRANSFORMER_FILE" ]
}

stage_transformer_side_artifact() {
  transformer_side_artifact_missing || return 0
  echo "Downloading transformer side artifact: $TRANSFORMER_REPO@${TRANSFORMER_REVISION:-main} $TRANSFORMER_FILE -> $TRANSFORMER_DEST_DIR"
  mkdir -p "$TRANSFORMER_DEST_DIR"
  _tsa_keep="${TRANSFORMER_FILE%%/*}"
  find "$TRANSFORMER_DEST_DIR" -mindepth 1 -maxdepth 1 ! -name "$_tsa_keep" ! -name ".cache" -exec rm -rf {} +
  python - <<'PY'
import os

from huggingface_hub import hf_hub_download

hf_hub_download(
    repo_id=os.environ["TRANSFORMER_REPO"],
    filename=os.environ["TRANSFORMER_FILE"],
    revision=(os.environ.get("TRANSFORMER_REVISION") or "").strip() or None,
    local_dir=os.environ["TRANSFORMER_DEST_DIR"],
    cache_dir=os.environ.get("HF_HOME"),
    token=os.environ.get("HF_TOKEN") or os.environ.get("HUGGINGFACE_HUB_TOKEN"),
)
PY
  if transformer_side_artifact_missing; then
    echo "FATAL: transformer side artifact '$TRANSFORMER_FILE' is missing or empty under '$TRANSFORMER_DEST_DIR'." >&2
    exit 1
  fi
}
`
