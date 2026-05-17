#!/usr/bin/env bash
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd "${SCRIPT_DIR}/.." && pwd)"
TMP_ROOT="$(mktemp -d)"
trap 'rm -rf "${TMP_ROOT}"' EXIT

mkdir -p \
  "${TMP_ROOT}/build" \
  "${TMP_ROOT}/deploy/gpuprofiles" \
  "${TMP_ROOT}/deploy/system" \
  "${TMP_ROOT}/scripts"

cp "${REPO_ROOT}/build/runtime.yaml" "${TMP_ROOT}/build/runtime.yaml"
cp "${REPO_ROOT}/deploy/gpuprofiles/gfx1100.yaml" "${TMP_ROOT}/deploy/gpuprofiles/gfx1100.yaml"
cp "${REPO_ROOT}/deploy/gpuprofiles/gfx906.yaml" "${TMP_ROOT}/deploy/gpuprofiles/gfx906.yaml"
cp "${REPO_ROOT}/deploy/system/values-k3s.yaml" "${TMP_ROOT}/deploy/system/values-k3s.yaml"
mkdir -p "${TMP_ROOT}/deploy/models"
cp "${REPO_ROOT}/deploy/models/gemma4-e4b-turboquant.yaml" "${TMP_ROOT}/deploy/models/gemma4-e4b-turboquant.yaml"
cp "${REPO_ROOT}/deploy/models/gemma4-31b-gptq-long.yaml" "${TMP_ROOT}/deploy/models/gemma4-31b-gptq-long.yaml"
cp "${REPO_ROOT}/scripts/promote-runtime-digest.sh" "${TMP_ROOT}/scripts/promote-runtime-digest.sh"
cp "${REPO_ROOT}/scripts/check-runtime-profile-consistency.sh" "${TMP_ROOT}/scripts/check-runtime-profile-consistency.sh"

digest="sha256:0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"
rollback_digest="sha256:fedcba9876543210fedcba9876543210fedcba9876543210fedcba9876543210"
target="registry.harbor.lan/flexinfer/runtime@${digest}"
serving_digest="sha256:3333333333333333333333333333333333333333333333333333333333333333"
serving_rollback_digest="sha256:4444444444444444444444444444444444444444444444444444444444444444"
serving_target="registry.harbor.lan/flexinfer/runtime@${serving_digest}"
runtime_images_before="$(
  yq -r '.runtime.profiles[] | select(.gpuArch == "gfx1100") | .image' \
    "${TMP_ROOT}/deploy/system/values-k3s.yaml" | sort -u
)"

"${TMP_ROOT}/scripts/promote-runtime-digest.sh" gfx1100 \
  --repo-root "${TMP_ROOT}" \
  --digest "${digest}" \
  --validation-row "Required canary: gfx1100 textgen" \
  --rollback-digest "${rollback_digest}" \
  --apply >/tmp/flexinfer-promote-runtime-test.log

profile_image="$(yq -r '.spec.runtime.image' "${TMP_ROOT}/deploy/gpuprofiles/gfx1100.yaml")"
if [[ "${profile_image}" != "${target}" ]]; then
  echo "GPUProfile image mismatch: got ${profile_image}, want ${target}" >&2
  exit 1
fi

runtime_images="$(
  yq -r '.runtime.profiles[] | select(.gpuArch == "gfx1100") | .image' \
    "${TMP_ROOT}/deploy/system/values-k3s.yaml" | sort -u
)"
if [[ "${runtime_images}" != "${runtime_images_before}" ]]; then
  echo "gfx1100 promotion mutated serving runtime profiles: got ${runtime_images}, want ${runtime_images_before}" >&2
  exit 1
fi

dry_before="$(shasum "${TMP_ROOT}/deploy/gpuprofiles/gfx1100.yaml" "${TMP_ROOT}/deploy/system/values-k3s.yaml")"
"${TMP_ROOT}/scripts/promote-runtime-digest.sh" gfx1100 \
  --repo-root "${TMP_ROOT}" \
  --digest "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa" >/tmp/flexinfer-promote-runtime-dry-run.log
dry_after="$(shasum "${TMP_ROOT}/deploy/gpuprofiles/gfx1100.yaml" "${TMP_ROOT}/deploy/system/values-k3s.yaml")"
if [[ "${dry_before}" != "${dry_after}" ]]; then
  echo "dry-run mutated files" >&2
  exit 1
fi
grep -F "Promotion targets:" /tmp/flexinfer-promote-runtime-dry-run.log >/dev/null
grep -F "Validation reminders:" /tmp/flexinfer-promote-runtime-dry-run.log >/dev/null
grep -F "Promotion gate: update .loom/60-validation-matrix.md before --apply." /tmp/flexinfer-promote-runtime-dry-run.log >/dev/null
grep -F "Required matrix fields: artifact, context_length, gpu_class, backend, support_level, runtime_image, oci_ref, validation_evidence, observed_failure_mode, canary_command, rollback_digest, spec_roadmap_link, promotion_decision." /tmp/flexinfer-promote-runtime-dry-run.log >/dev/null
grep -F "Validation matrix row: REQUIRED for --apply via --validation-row." /tmp/flexinfer-promote-runtime-dry-run.log >/dev/null
grep -F "Rollback digest: REQUIRED for --apply via --rollback-digest." /tmp/flexinfer-promote-runtime-dry-run.log >/dev/null
grep -F "Required lanes to keep represented: gfx1100 textgen, gfx1100 imagegen, gfx906 textgen/quantization, gfx906 imagegen/offload." /tmp/flexinfer-promote-runtime-dry-run.log >/dev/null
grep -F "Smoke gfx1100 textgen and imagegen lanes before Flux reconciliation." /tmp/flexinfer-promote-runtime-dry-run.log >/dev/null

serving_before="$(shasum "${TMP_ROOT}/deploy/gpuprofiles/gfx1100.yaml")"
"${TMP_ROOT}/scripts/promote-runtime-digest.sh" gfx1100-serving \
  --repo-root "${TMP_ROOT}" \
  --digest "${serving_digest}" >/tmp/flexinfer-promote-runtime-serving-dry-run.log
grep -F "Helm runtime profiles: deploy/system/values-k3s.yaml" /tmp/flexinfer-promote-runtime-serving-dry-run.log >/dev/null
if grep -F "GPUProfile runtime image:" /tmp/flexinfer-promote-runtime-serving-dry-run.log >/dev/null; then
  echo "serving dry-run unexpectedly targeted GPUProfile" >&2
  exit 1
fi
"${TMP_ROOT}/scripts/promote-runtime-digest.sh" gfx1100-serving \
  --repo-root "${TMP_ROOT}" \
  --digest "${serving_digest}" \
  --validation-row "Required canary: gfx1100 serving" \
  --rollback-digest "${serving_rollback_digest}" \
  --apply >/tmp/flexinfer-promote-runtime-serving-test.log
serving_after="$(shasum "${TMP_ROOT}/deploy/gpuprofiles/gfx1100.yaml")"
if [[ "${serving_before}" != "${serving_after}" ]]; then
  echo "serving promotion mutated GPUProfile" >&2
  exit 1
fi
serving_runtime_images="$(
  yq -r '.runtime.profiles[] | select(.gpuArch == "gfx1100") | .image' \
    "${TMP_ROOT}/deploy/system/values-k3s.yaml" | sort -u
)"
if [[ "${serving_runtime_images}" != "${serving_target}" ]]; then
  echo "serving values runtime image mismatch: got ${serving_runtime_images}, want ${serving_target}" >&2
  exit 1
fi

if "${TMP_ROOT}/scripts/promote-runtime-digest.sh" gfx1100 \
  --repo-root "${TMP_ROOT}" \
  --digest "${digest}" \
  --apply >/tmp/flexinfer-promote-runtime-missing-gate.log 2>&1; then
  echo "apply without validation gate unexpectedly succeeded" >&2
  exit 1
fi
grep -E -- "--apply requires --validation-row" /tmp/flexinfer-promote-runtime-missing-gate.log >/dev/null

canary_digest="sha256:1111111111111111111111111111111111111111111111111111111111111111"
canary_rollback_digest="sha256:2222222222222222222222222222222222222222222222222222222222222222"
canary_target="registry.harbor.lan/flexinfer/runtime@${canary_digest}"
profile_before="$(shasum "${TMP_ROOT}/deploy/gpuprofiles/gfx1100.yaml" "${TMP_ROOT}/deploy/system/values-k3s.yaml")"
"${TMP_ROOT}/scripts/promote-runtime-digest.sh" gfx1100-gemma4-turboquant-experimental \
  --repo-root "${TMP_ROOT}" \
  --digest "${canary_digest}" >/tmp/flexinfer-promote-runtime-canary-dry-run.log
grep -E "Model manifest image: deploy/models/gemma4-e4b-turboquant.yaml|Model manifest image: deploy/models/gemma4-31b-gptq-long.yaml|Dry-run only" /tmp/flexinfer-promote-runtime-canary-dry-run.log >/dev/null
"${TMP_ROOT}/scripts/promote-runtime-digest.sh" gfx1100-gemma4-turboquant-experimental \
  --repo-root "${TMP_ROOT}" \
  --digest "${canary_digest}" \
  --validation-row "gemma4-e4b-turboquant runtime probe" \
  --rollback-digest "${canary_rollback_digest}" \
  --apply >/tmp/flexinfer-promote-runtime-canary-test.log
profile_after="$(shasum "${TMP_ROOT}/deploy/gpuprofiles/gfx1100.yaml" "${TMP_ROOT}/deploy/system/values-k3s.yaml")"
if [[ "${profile_before}" != "${profile_after}" ]]; then
  echo "canary promotion mutated broad runtime consumers" >&2
  exit 1
fi

for model_file in \
  "${TMP_ROOT}/deploy/models/gemma4-e4b-turboquant.yaml" \
  "${TMP_ROOT}/deploy/models/gemma4-31b-gptq-long.yaml"; do
  model_image="$(yq -r '.spec.image' "${model_file}")"
  if [[ "${model_image}" != "${canary_target}" ]]; then
    echo "canary model image mismatch in ${model_file}: got ${model_image}, want ${canary_target}" >&2
    exit 1
  fi
done

"${TMP_ROOT}/scripts/check-runtime-profile-consistency.sh" --repo-root "${TMP_ROOT}"

echo "promote-runtime-digest tests passed"
