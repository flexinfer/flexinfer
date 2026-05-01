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
cp "${REPO_ROOT}/deploy/system/values-k3s.yaml" "${TMP_ROOT}/deploy/system/values-k3s.yaml"
mkdir -p "${TMP_ROOT}/deploy/models"
cp "${REPO_ROOT}/deploy/models/gemma4-e4b-turboquant.yaml" "${TMP_ROOT}/deploy/models/gemma4-e4b-turboquant.yaml"
cp "${REPO_ROOT}/deploy/models/gemma4-31b-gptq-long.yaml" "${TMP_ROOT}/deploy/models/gemma4-31b-gptq-long.yaml"
cp "${REPO_ROOT}/scripts/promote-runtime-digest.sh" "${TMP_ROOT}/scripts/promote-runtime-digest.sh"

digest="sha256:0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"
target="registry.harbor.lan/flexinfer/runtime@${digest}"

"${TMP_ROOT}/scripts/promote-runtime-digest.sh" gfx1100 \
  --repo-root "${TMP_ROOT}" \
  --digest "${digest}" \
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
if [[ "${runtime_images}" != "${target}" ]]; then
  echo "values runtime image mismatch: got ${runtime_images}, want ${target}" >&2
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

canary_digest="sha256:1111111111111111111111111111111111111111111111111111111111111111"
canary_target="registry.harbor.lan/flexinfer/runtime@${canary_digest}"
profile_before="$(shasum "${TMP_ROOT}/deploy/gpuprofiles/gfx1100.yaml" "${TMP_ROOT}/deploy/system/values-k3s.yaml")"
"${TMP_ROOT}/scripts/promote-runtime-digest.sh" gfx1100-gemma4-turboquant-experimental \
  --repo-root "${TMP_ROOT}" \
  --digest "${canary_digest}" \
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

echo "promote-runtime-digest tests passed"
