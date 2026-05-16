#!/usr/bin/env bash
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd "${SCRIPT_DIR}/.." && pwd)"

usage() {
  cat <<'USAGE'
Usage:
  scripts/check-runtime-profile-consistency.sh [--repo-root <path>]

Checks the stable runtime profile contract:
  - Managed GPUProfile manifests have matching build/runtime.yaml arch/vendor entries.
  - Helm runtime profiles point at known GPUProfile architectures.
  - Runtime consumer images are digest-pinned.
  - Every Helm runtime architecture has a GPUProfile.

This intentionally does not require GPUProfile and Helm runtime image digests to
match yet; live clusters may promote node-specific digests independently. Use
scripts/promote-runtime-digest.sh when intentionally syncing a digest.
USAGE
}

fail() {
  echo "ERROR: $*" >&2
  exit 1
}

need_cmd() {
  command -v "$1" >/dev/null 2>&1 || fail "$1 is required"
}

is_digest_ref() {
  [[ "$1" =~ @sha256:[0-9a-fA-F]{64}$ ]]
}

while [[ $# -gt 0 ]]; do
  case "$1" in
    --repo-root)
      [[ $# -ge 2 ]] || fail "--repo-root requires a path"
      REPO_ROOT="$2"
      shift 2
      ;;
    -h|--help)
      usage
      exit 0
      ;;
    *)
      fail "unknown argument: $1"
      ;;
  esac
done

need_cmd yq

runtime_config="${REPO_ROOT}/build/runtime.yaml"
values_file="${REPO_ROOT}/deploy/system/values-k3s.yaml"
profile_dir="${REPO_ROOT}/deploy/gpuprofiles"

[[ -f "${runtime_config}" ]] || fail "missing ${runtime_config}"
[[ -f "${values_file}" ]] || fail "missing ${values_file}"
[[ -d "${profile_dir}" ]] || fail "missing ${profile_dir}"

declare -A profile_arches=()
declare -A profile_vendors=()
declare -A managed_arches=()

while IFS=$'\t' read -r name arch vendor image; do
  [[ -n "${name}" ]] || continue
  [[ -n "${arch}" && "${arch}" != "null" ]] || fail "values runtime profile ${name}: missing gpuArch"
  managed_arches["${arch}"]=1
done < <(yq -r '.runtime.profiles[]? | [.name, .gpuArch, .gpuVendor, .image] | @tsv' "${values_file}")

while IFS= read -r profile_file; do
  kind="$(yq -r '.kind // ""' "${profile_file}")"
  [[ "${kind}" == "GPUProfile" ]] || continue

  name="$(basename "${profile_file}" .yaml)"
  arch="$(yq -r '.spec.architecture // ""' "${profile_file}")"
  vendor="$(yq -r '.spec.vendor // ""' "${profile_file}")"
  image="$(yq -r '.spec.runtime.image // ""' "${profile_file}")"

  [[ -n "${arch}" ]] || fail "${profile_file}: missing spec.architecture"
  [[ -n "${managed_arches[${arch}]:-}" ]] || continue
  [[ -n "${vendor}" ]] || fail "${profile_file}: missing spec.vendor"
  [[ -n "${image}" ]] || fail "${profile_file}: missing spec.runtime.image"
  is_digest_ref "${image}" || fail "${profile_file}: spec.runtime.image must be digest-pinned, got ${image}"

  if [[ "$(yq -r ".profiles | has(\"${name}\")" "${runtime_config}")" != "true" ]]; then
    fail "${profile_file}: no matching build/runtime.yaml profile named ${name}"
  fi

  build_arch="$(yq -r ".profiles[\"${name}\"].gpu_arch // \"\"" "${runtime_config}")"
  build_vendor="$(yq -r ".profiles[\"${name}\"].gpu_vendor // \"\"" "${runtime_config}")"
  [[ "${build_arch}" == "${arch}" ]] || fail "${name}: GPUProfile arch ${arch} != build/runtime.yaml arch ${build_arch}"
  [[ "${build_vendor}" == "${vendor}" ]] || fail "${name}: GPUProfile vendor ${vendor} != build/runtime.yaml vendor ${build_vendor}"

  profile_arches["${arch}"]=1
  profile_vendors["${arch}"]="${vendor}"
done < <(find "${profile_dir}" -maxdepth 1 -type f -name '*.yaml' | sort)

while IFS=$'\t' read -r name arch vendor image; do
  [[ -n "${name}" ]] || continue
  [[ -n "${arch}" && "${arch}" != "null" ]] || fail "values runtime profile ${name}: missing gpuArch"
  [[ -n "${vendor}" && "${vendor}" != "null" ]] || fail "values runtime profile ${name}: missing gpuVendor"
  [[ -n "${image}" && "${image}" != "null" ]] || fail "values runtime profile ${name}: missing image"
  is_digest_ref "${image}" || fail "values runtime profile ${name}: image must be digest-pinned, got ${image}"

  [[ -n "${profile_arches[${arch}]:-}" ]] || fail "values runtime profile ${name}: gpuArch ${arch} has no GPUProfile"
  [[ "${profile_vendors[${arch}]}" == "${vendor}" ]] || fail "values runtime profile ${name}: vendor ${vendor} != GPUProfile vendor ${profile_vendors[${arch}]}"

  if [[ "$(yq -r ".profiles | has(\"${name}\")" "${runtime_config}")" == "true" ]]; then
    build_arch="$(yq -r ".profiles[\"${name}\"].gpu_arch // \"\"" "${runtime_config}")"
    build_vendor="$(yq -r ".profiles[\"${name}\"].gpu_vendor // \"\"" "${runtime_config}")"
  else
    build_arch="$(yq -r ".profiles[\"${arch}\"].gpu_arch // \"\"" "${runtime_config}")"
    build_vendor="$(yq -r ".profiles[\"${arch}\"].gpu_vendor // \"\"" "${runtime_config}")"
  fi
  [[ "${build_arch}" == "${arch}" ]] || fail "values runtime profile ${name}: build arch ${build_arch} != values arch ${arch}"
  [[ "${build_vendor}" == "${vendor}" ]] || fail "values runtime profile ${name}: build vendor ${build_vendor} != values vendor ${vendor}"

done < <(yq -r '.runtime.profiles[]? | [.name, .gpuArch, .gpuVendor, .image] | @tsv' "${values_file}")

for arch in "${!managed_arches[@]}"; do
  [[ -n "${profile_arches[${arch}]:-}" ]] || fail "deploy/system runtime arch ${arch} has no GPUProfile"
done

echo "runtime profile consistency checks passed"
