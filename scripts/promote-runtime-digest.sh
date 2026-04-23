#!/usr/bin/env bash
# Promote a built flexinfer-runtime tag to an immutable digest across GitOps consumers.
#
# Default mode is dry-run. Use --apply only after validating the resolved digest.

set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd "${SCRIPT_DIR}/.." && pwd)"
RUNTIME_CONFIG="build/runtime.yaml"
VALUES_FILE="deploy/system/values-k3s.yaml"
APPLY=false
PROFILE=""
DIGEST=""
IMAGE_REF=""
RESOLVE_TOOL="auto"

usage() {
  cat <<'USAGE'
Usage:
  scripts/promote-runtime-digest.sh <profile> [flags]

Flags:
  --digest sha256:<hex>   Use an already-resolved digest instead of querying the registry.
  --image <ref>           Resolve this image ref instead of build/runtime.yaml's profile tag.
  --apply                 Update files in place. Default is dry-run diff only.
  --repo-root <path>      Repository root, for tests or unusual launch paths.
  --resolve-tool <tool>   auto, crane, or docker. Default: auto.
  -h, --help              Show this help.

Examples:
  scripts/promote-runtime-digest.sh gfx1100
  scripts/promote-runtime-digest.sh gfx1100 --digest <sha256> --apply
  scripts/promote-runtime-digest.sh gfx906 --image registry.harbor.lan/flexinfer/runtime:rocm-gfx906

The script updates:
  - deploy/gpuprofiles/<profile-or-arch>.yaml: .spec.runtime.image
  - deploy/system/values-k3s.yaml runtime.profiles entries matching profile name or gpuArch
USAGE
}

fail() {
  echo "ERROR: $*" >&2
  exit 1
}

need_cmd() {
  command -v "$1" >/dev/null 2>&1 || fail "$1 is required"
}

normalize_digest() {
  local raw="$1"
  if [[ "${raw}" =~ ^sha256:[0-9a-fA-F]{64}$ ]]; then
    echo "${raw}"
    return 0
  fi
  if [[ "${raw}" =~ ^[0-9a-fA-F]{64}$ ]]; then
    echo "sha256:${raw}"
    return 0
  fi
  fail "digest must be sha256:<64 hex chars>"
}

image_repo() {
  local ref="$1"
  ref="${ref%@*}"
  local leaf="${ref##*/}"
  if [[ "${leaf}" == *:* ]]; then
    ref="${ref%:*}"
  fi
  echo "${ref}"
}

resolve_digest() {
  local ref="$1"
  case "${RESOLVE_TOOL}" in
    auto)
      if command -v crane >/dev/null 2>&1; then
        crane digest "${ref}"
      elif command -v docker >/dev/null 2>&1; then
        docker buildx imagetools inspect "${ref}" --format '{{.Digest}}'
      else
        fail "cannot resolve digest: install crane or docker, or pass --digest"
      fi
      ;;
    crane)
      need_cmd crane
      crane digest "${ref}"
      ;;
    docker)
      need_cmd docker
      docker buildx imagetools inspect "${ref}" --format '{{.Digest}}'
      ;;
    *)
      fail "--resolve-tool must be auto, crane, or docker"
      ;;
  esac
}

profile_image_ref() {
  local profile="$1"
  local config="${REPO_ROOT}/${RUNTIME_CONFIG}"
  need_cmd yq
  [[ -f "${config}" ]] || fail "runtime config not found: ${config}"
  local exists
  exists="$(yq -r ".profiles | has(\"${profile}\")" "${config}")"
  [[ "${exists}" == "true" ]] || fail "unknown runtime profile '${profile}' in ${RUNTIME_CONFIG}"
  yq -r ".registry + \"/\" + .profiles[\"${profile}\"].tag" "${config}"
}

profile_arch() {
  local profile="$1"
  yq -r ".profiles[\"${profile}\"].gpu_arch" "${REPO_ROOT}/${RUNTIME_CONFIG}"
}

profile_consumer_file() {
  local profile="$1" arch="$2"
  if [[ -f "${REPO_ROOT}/deploy/gpuprofiles/${profile}.yaml" ]]; then
    echo "deploy/gpuprofiles/${profile}.yaml"
    return 0
  fi
  if [[ -f "${REPO_ROOT}/deploy/gpuprofiles/${arch}.yaml" ]]; then
    echo "deploy/gpuprofiles/${arch}.yaml"
    return 0
  fi
  fail "no GPUProfile manifest found for profile '${profile}' or arch '${arch}'"
}

runtime_profile_matches() {
  local values="$1" profile="$2" arch="$3"
  yq -r ".runtime.profiles[]? | select(.name == \"${profile}\" or .gpuArch == \"${arch}\") | .name" "${values}"
}

update_files() {
  local root="$1" profile_file="$2" values_file="$3" profile="$4" arch="$5" target="$6"
  python3 - "$root" "$profile_file" "$values_file" "$profile" "$arch" "$target" <<'PY'
import re
import sys
from pathlib import Path

root = Path(sys.argv[1])
profile_file = sys.argv[2]
values_file = sys.argv[3]
profile = sys.argv[4]
arch = sys.argv[5]
target = sys.argv[6]


def replace_gpuprofile(path: Path) -> int:
    lines = path.read_text(encoding="utf-8").splitlines(keepends=True)
    in_spec = False
    in_runtime = False
    changed = 0
    for i, line in enumerate(lines):
        if re.match(r"^spec:\s*$", line):
            in_spec = True
            in_runtime = False
            continue
        if in_spec and re.match(r"^[^ \n#]", line) and not re.match(r"^spec:\s*$", line):
            in_spec = False
            in_runtime = False
        if in_spec and re.match(r"^  runtime:\s*$", line):
            in_runtime = True
            continue
        if in_runtime and re.match(r"^  [^ \n#].*:\s*", line):
            in_runtime = False
        if in_runtime and re.match(r"^    image:\s*", line):
            newline = "\n" if line.endswith("\n") else ""
            lines[i] = f"    image: {target}{newline}"
            changed += 1
            break
    if changed != 1:
        raise SystemExit(f"expected to update exactly one GPUProfile runtime image in {path}, updated {changed}")
    path.write_text("".join(lines), encoding="utf-8")
    return changed


def replace_values_runtime_profiles(path: Path) -> int:
    lines = path.read_text(encoding="utf-8").splitlines(keepends=True)
    changed = 0
    in_runtime = False
    in_profiles = False
    current = None

    def finish_item(item) -> None:
        nonlocal changed
        if not item:
            return
        name = str(item.get("name") or "")
        gpu_arch = str(item.get("gpuArch") or "")
        image_idx = item.get("image_idx")
        if image_idx is None:
            return
        if name == profile or gpu_arch == arch:
            idx = int(image_idx)
            newline = "\n" if lines[idx].endswith("\n") else ""
            lines[idx] = f"      image: \"{target}\"{newline}"
            changed += 1

    for i, line in enumerate(lines):
        top_level = re.match(r"^[A-Za-z0-9_-]+:\s*", line)
        if top_level:
            if in_runtime and not re.match(r"^runtime:\s*$", line):
                finish_item(current)
                current = None
                in_runtime = False
                in_profiles = False
            if re.match(r"^runtime:\s*$", line):
                in_runtime = True
                in_profiles = False
                current = None
                continue

        if not in_runtime:
            continue
        if re.match(r"^  profiles:\s*$", line):
            in_profiles = True
            continue
        if not in_profiles:
            continue

        m_item = re.match(r"^    - name:\s*\"?([^\"\n]+?)\"?\s*$", line)
        if m_item:
            finish_item(current)
            current = {"name": m_item.group(1).strip()}
            continue
        if current is None:
            continue
        m_image = re.match(r"^      image:\s*", line)
        if m_image:
            current["image_idx"] = i
            continue
        m_arch = re.match(r"^      gpuArch:\s*\"?([^\"\n]+?)\"?\s*$", line)
        if m_arch:
            current["gpuArch"] = m_arch.group(1).strip()
            continue

    if in_runtime:
        finish_item(current)
    if changed < 1:
        raise SystemExit(f"expected to update at least one runtime.profiles image in {path}, updated {changed}")
    path.write_text("".join(lines), encoding="utf-8")
    return changed


replace_gpuprofile(root / profile_file)
replace_values_runtime_profiles(root / values_file)
PY
}

print_current_consumers() {
  local profile_file="$1" values_file="$2" profile="$3" arch="$4"
  echo "Current consumers:"
  echo "  ${profile_file}: $(yq -r '.spec.runtime.image // "<unset>"' "${REPO_ROOT}/${profile_file}")"
  while IFS= read -r name; do
    [[ -n "${name}" ]] || continue
    local image
    image="$(PROFILE_NAME="${name}" yq -r '.runtime.profiles[] | select(.name == strenv(PROFILE_NAME)) | .image' "${REPO_ROOT}/${values_file}")"
    echo "  ${values_file}: runtime.profiles[${name}].image = ${image}"
  done < <(runtime_profile_matches "${REPO_ROOT}/${values_file}" "${profile}" "${arch}")
}

while [[ $# -gt 0 ]]; do
  case "$1" in
    --digest)
      [[ $# -ge 2 ]] || fail "--digest requires a value"
      DIGEST="$2"
      shift 2
      ;;
    --image)
      [[ $# -ge 2 ]] || fail "--image requires a value"
      IMAGE_REF="$2"
      shift 2
      ;;
    --apply)
      APPLY=true
      shift
      ;;
    --repo-root)
      [[ $# -ge 2 ]] || fail "--repo-root requires a value"
      REPO_ROOT="$(cd "$2" && pwd)"
      shift 2
      ;;
    --resolve-tool)
      [[ $# -ge 2 ]] || fail "--resolve-tool requires a value"
      RESOLVE_TOOL="$2"
      shift 2
      ;;
    -h|--help)
      usage
      exit 0
      ;;
    -*)
      fail "unknown flag: $1"
      ;;
    *)
      [[ -z "${PROFILE}" ]] || fail "only one profile may be specified"
      PROFILE="$1"
      shift
      ;;
  esac
done

[[ -n "${PROFILE}" ]] || { usage >&2; exit 1; }
need_cmd yq

cd "${REPO_ROOT}"

if [[ -z "${IMAGE_REF}" ]]; then
  IMAGE_REF="$(profile_image_ref "${PROFILE}")"
fi

ARCH="$(profile_arch "${PROFILE}")"
PROFILE_FILE="$(profile_consumer_file "${PROFILE}" "${ARCH}")"
TARGET_DIGEST="${DIGEST:-$(resolve_digest "${IMAGE_REF}")}"
TARGET_DIGEST="$(normalize_digest "${TARGET_DIGEST}")"
TARGET_IMAGE="$(image_repo "${IMAGE_REF}")@${TARGET_DIGEST}"

[[ -f "${PROFILE_FILE}" ]] || fail "GPUProfile file missing: ${PROFILE_FILE}"
[[ -f "${VALUES_FILE}" ]] || fail "values file missing: ${VALUES_FILE}"

matched_values="$(runtime_profile_matches "${VALUES_FILE}" "${PROFILE}" "${ARCH}" | sed '/^$/d' | wc -l | tr -d ' ')"
[[ "${matched_values}" != "0" ]] || fail "no runtime.profiles entries in ${VALUES_FILE} match profile=${PROFILE} arch=${ARCH}"

echo "Runtime digest promotion"
echo "  profile: ${PROFILE}"
echo "  arch:    ${ARCH}"
echo "  source:  ${IMAGE_REF}"
echo "  target:  ${TARGET_IMAGE}"
echo "  mode:    $([[ "${APPLY}" == "true" ]] && echo apply || echo dry-run)"
echo ""
print_current_consumers "${PROFILE_FILE}" "${VALUES_FILE}" "${PROFILE}" "${ARCH}"
echo ""

if [[ "${APPLY}" == "true" ]]; then
  update_files "${REPO_ROOT}" "${PROFILE_FILE}" "${VALUES_FILE}" "${PROFILE}" "${ARCH}" "${TARGET_IMAGE}"
  echo "Updated files:"
  echo "  ${PROFILE_FILE}"
  echo "  ${VALUES_FILE}"
else
  tmpdir="$(mktemp -d)"
  trap 'rm -rf "${tmpdir}"' EXIT
  mkdir -p "${tmpdir}/$(dirname "${PROFILE_FILE}")" "${tmpdir}/$(dirname "${VALUES_FILE}")"
  cp "${PROFILE_FILE}" "${tmpdir}/${PROFILE_FILE}"
  cp "${VALUES_FILE}" "${tmpdir}/${VALUES_FILE}"
  update_files "${tmpdir}" "${PROFILE_FILE}" "${VALUES_FILE}" "${PROFILE}" "${ARCH}" "${TARGET_IMAGE}"
  echo "Dry-run diff:"
  diff -u "${PROFILE_FILE}" "${tmpdir}/${PROFILE_FILE}" || true
  diff -u "${VALUES_FILE}" "${tmpdir}/${VALUES_FILE}" || true
  echo ""
  echo "No files changed. Re-run with --apply after validating the digest."
fi
