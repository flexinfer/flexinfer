#!/bin/bash
# Build flexinfer-runtime images from build/runtime.yaml config.
#
# Usage:
#   ./build/build-runtime.sh <profile|all> [--push] [--dry-run] [--no-cache]
#
# Examples:
#   ./build/build-runtime.sh gfx1100              # build gfx1100 profile
#   ./build/build-runtime.sh gfx906 --push        # build + push gfx906
#   ./build/build-runtime.sh all --push            # build + push all profiles
#   ./build/build-runtime.sh gfx1100 --dry-run     # print docker command
#   ./build/build-runtime.sh gfx1100 --no-cache    # build without layer cache
#
# Requires: yq (https://github.com/mikefarah/yq)

set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd "${SCRIPT_DIR}/.." && pwd)"
CONFIG="${SCRIPT_DIR}/runtime.yaml"
DOCKERFILE="${SCRIPT_DIR}/Dockerfile.runtime"

# ── Validate prerequisites ───────────────────────────────────────────
if ! command -v yq &>/dev/null; then
    echo "ERROR: yq is required but not found. Install: brew install yq" >&2
    exit 1
fi

if [ ! -f "${CONFIG}" ]; then
    echo "ERROR: Config file not found: ${CONFIG}" >&2
    exit 1
fi

# ── Parse arguments ──────────────────────────────────────────────────
PROFILE="${1:-}"
PUSH=false
DRY_RUN=false
NO_CACHE=""

if [ -z "${PROFILE}" ]; then
    echo "Usage: $0 <profile|all> [--push] [--dry-run] [--no-cache]" >&2
    echo "" >&2
    echo "Available profiles:" >&2
    yq '.profiles | keys | .[]' "${CONFIG}" | sed 's/^/  /' >&2
    exit 1
fi

shift
for arg in "$@"; do
    case "${arg}" in
        --push)     PUSH=true ;;
        --dry-run)  DRY_RUN=true ;;
        --no-cache) NO_CACHE="--no-cache" ;;
        *)          echo "Unknown argument: ${arg}" >&2; exit 1 ;;
    esac
done

# ── Helper: read config value ────────────────────────────────────────
cfg() {
    local path="$1"
    yq -r "${path}" "${CONFIG}"
}

# ── Helper: read profile value with fallback ─────────────────────────
pcfg() {
    local profile="$1" field="$2" default="${3:-}"
    local val
    val=$(yq -r ".profiles[\"${profile}\"] | .${field}" "${CONFIG}" 2>/dev/null || true)
    if [ -z "${val}" ] || [ "${val}" = "null" ]; then
        echo "${default}"
    else
        echo "${val}"
    fi
}

# ── Build one profile ────────────────────────────────────────────────
build_profile() {
    local profile="$1"

    # Validate profile exists
    local exists
    exists=$(yq -r ".profiles | has(\"${profile}\")" "${CONFIG}")
    if [ "${exists}" != "true" ]; then
        echo "ERROR: Unknown profile '${profile}'. Available:" >&2
        yq '.profiles | keys | .[]' "${CONFIG}" | sed 's/^/  /' >&2
        exit 1
    fi

    # Read global config
    local registry go_version llamacpp_version ollama_version ollama_go_version
    registry=$(cfg '.registry')
    go_version=$(cfg '.go_version')
    llamacpp_version=$(cfg '.llamacpp_version')
    ollama_version=$(cfg '.ollama_version')
    ollama_go_version=$(cfg '.ollama_go_version')

    # Read profile config
    local tag base_image gpu_vendor gpu_arch amdgpu_targets build_context
    tag=$(pcfg "${profile}" "tag")
    base_image=$(pcfg "${profile}" "base_image")
    gpu_vendor=$(pcfg "${profile}" "gpu_vendor")
    gpu_arch=$(pcfg "${profile}" "gpu_arch")
    amdgpu_targets=$(pcfg "${profile}" "amdgpu_targets" "${gpu_arch}")
    build_context=$(pcfg "${profile}" "build_context" "default")

    # Backend flags
    local include_vllm include_llamacpp include_ollama include_diffusers include_steam include_quantizer
    local include_turboquant
    include_vllm=$(pcfg "${profile}" "backends.vllm" "false")
    include_llamacpp=$(pcfg "${profile}" "backends.llamacpp" "true")
    include_ollama=$(pcfg "${profile}" "backends.ollama" "true")
    include_diffusers=$(pcfg "${profile}" "backends.diffusers" "false")
    include_steam=$(pcfg "${profile}" "backends.steam" "false")
    include_quantizer=$(pcfg "${profile}" "include_quantizer" "false")
    include_turboquant=$(pcfg "${profile}" "include_turboquant" "false")

    # Python/runtime package config
    local include_bitsandbytes transformers_constraint transformers_install_mode transformers_repo transformers_ref
    local vllm_install_mode vllm_version vllm_extra_index_url vllm_repo vllm_ref vllm_source_patch_script
    local vllm_extra_deps_profile install_qwen35_fastpath
    local turboquant_install_mode turboquant_version turboquant_repo turboquant_ref turboquant_source_patch_script
    include_bitsandbytes=$(pcfg "${profile}" "include_bitsandbytes" "false")
    transformers_constraint=$(pcfg "${profile}" "transformers_constraint" ">=5.0")
    transformers_install_mode=$(pcfg "${profile}" "transformers_install_mode" "constraint")
    transformers_repo=$(pcfg "${profile}" "transformers_repo" "https://github.com/huggingface/transformers.git")
    transformers_ref=$(pcfg "${profile}" "transformers_ref" "main")
    vllm_install_mode=$(pcfg "${profile}" "vllm_install_mode" "wheel")
    vllm_version=$(pcfg "${profile}" "vllm_version" "0.17.0+rocm700")
    vllm_extra_index_url=$(pcfg "${profile}" "vllm_extra_index_url" "https://wheels.vllm.ai/rocm/0.17.0/rocm700")
    vllm_repo=$(pcfg "${profile}" "vllm_repo" "https://github.com/vllm-project/vllm.git")
    vllm_ref=$(pcfg "${profile}" "vllm_ref" "main")
    vllm_source_patch_script=$(pcfg "${profile}" "vllm_source_patch_script" "")
    vllm_extra_deps_profile=$(pcfg "${profile}" "vllm_extra_deps_profile" "full")
    install_qwen35_fastpath=$(pcfg "${profile}" "install_qwen35_fastpath" "true")
    turboquant_install_mode=$(pcfg "${profile}" "turboquant_install_mode" "none")
    turboquant_version=$(pcfg "${profile}" "turboquant_version" "1.4.0")
    turboquant_repo=$(pcfg "${profile}" "turboquant_repo" "https://github.com/Alberto-Codes/turboquant-vllm.git")
    turboquant_ref=$(pcfg "${profile}" "turboquant_ref" "main")
    turboquant_source_patch_script=$(pcfg "${profile}" "turboquant_source_patch_script" "")

    # Determine builder image
    local llamacpp_build_image cuda_architectures
    if [ "${gpu_vendor}" = "amd" ]; then
        llamacpp_build_image=$(pcfg "${profile}" "rocm_dev_image")
    else
        llamacpp_build_image=$(pcfg "${profile}" "cuda_dev_image")
    fi
    cuda_architectures=$(pcfg "${profile}" "cuda_architectures" "52")

    local full_tag="${registry}/${tag}"

    # Build docker context flag
    local context_flag=""
    if [ "${build_context}" != "default" ]; then
        context_flag="--context ${build_context}"
    fi

    # Generate .runtime-env file with profile env vars
    # This file is COPY'd into the image and sourced at runtime by runtime-entrypoint.sh
    local env_file="${SCRIPT_DIR}/.runtime-env"
    : > "${env_file}"
    echo "# Generated by build-runtime.sh for profile: ${profile}" >> "${env_file}"
    echo "# Do not edit — regenerated on each build." >> "${env_file}"
    echo "GPU_VENDOR=${gpu_vendor}" >> "${env_file}"
    echo "GPU_ARCH=${gpu_arch}" >> "${env_file}"
    local env_keys
    env_keys=$(yq -r ".profiles[\"${profile}\"].env | keys | .[]" "${CONFIG}" 2>/dev/null || true)
    for key in ${env_keys}; do
        local val
        val=$(yq -r ".profiles[\"${profile}\"].env.${key}" "${CONFIG}")
        echo "${key}=${val}" >> "${env_file}"
    done
    # Ensure cleanup on exit
    trap "rm -f '${env_file}'" EXIT

    # Assemble docker build command
    local -a cmd=("docker")
    if [ -n "${context_flag}" ]; then
        cmd+=("--context" "${build_context}")
    fi
    cmd+=(
        "build"
        "-f" "${DOCKERFILE}"
        "--build-arg" "BASE_IMAGE=${base_image}"
        "--build-arg" "GO_VERSION=${go_version}"
        "--build-arg" "LLAMACPP_VERSION=${llamacpp_version}"
        "--build-arg" "OLLAMA_VERSION=${ollama_version}"
        "--build-arg" "OLLAMA_GO_VERSION=${ollama_go_version}"
        "--build-arg" "AMDGPU_TARGETS=${amdgpu_targets}"
        "--build-arg" "GPU_VENDOR=${gpu_vendor}"
        "--build-arg" "GPU_ARCH=${gpu_arch}"
        "--build-arg" "INCLUDE_VLLM=${include_vllm}"
        "--build-arg" "INCLUDE_LLAMACPP=${include_llamacpp}"
        "--build-arg" "INCLUDE_OLLAMA=${include_ollama}"
        "--build-arg" "INCLUDE_DIFFUSERS=${include_diffusers}"
        "--build-arg" "INCLUDE_BITSANDBYTES=${include_bitsandbytes}"
        "--build-arg" "INCLUDE_STEAM=${include_steam}"
        "--build-arg" "INCLUDE_QUANTIZER=${include_quantizer}"
        "--build-arg" "TRANSFORMERS_CONSTRAINT=${transformers_constraint}"
        "--build-arg" "TRANSFORMERS_INSTALL_MODE=${transformers_install_mode}"
        "--build-arg" "TRANSFORMERS_REPO=${transformers_repo}"
        "--build-arg" "TRANSFORMERS_REF=${transformers_ref}"
        "--build-arg" "VLLM_INSTALL_MODE=${vllm_install_mode}"
        "--build-arg" "VLLM_VERSION=${vllm_version}"
        "--build-arg" "VLLM_EXTRA_INDEX_URL=${vllm_extra_index_url}"
        "--build-arg" "VLLM_REPO=${vllm_repo}"
        "--build-arg" "VLLM_REF=${vllm_ref}"
        "--build-arg" "VLLM_SOURCE_PATCH_SCRIPT=${vllm_source_patch_script}"
        "--build-arg" "VLLM_EXTRA_DEPS_PROFILE=${vllm_extra_deps_profile}"
        "--build-arg" "INSTALL_QWEN35_FASTPATH=${install_qwen35_fastpath}"
        "--build-arg" "INCLUDE_TURBOQUANT=${include_turboquant}"
        "--build-arg" "TURBOQUANT_INSTALL_MODE=${turboquant_install_mode}"
        "--build-arg" "TURBOQUANT_VERSION=${turboquant_version}"
        "--build-arg" "TURBOQUANT_REPO=${turboquant_repo}"
        "--build-arg" "TURBOQUANT_REF=${turboquant_ref}"
        "--build-arg" "TURBOQUANT_SOURCE_PATCH_SCRIPT=${turboquant_source_patch_script}"
        "--build-arg" "LLAMACPP_BUILD_IMAGE=${llamacpp_build_image}"
        "--build-arg" "CUDA_ARCHITECTURES=${cuda_architectures}"
    )
    if [ -n "${NO_CACHE}" ]; then
        cmd+=("${NO_CACHE}")
    fi
    cmd+=(
        "-t" "${full_tag}"
        "${REPO_ROOT}"
    )

    echo "=== Building profile: ${profile} ==="
    echo "  Tag:    ${full_tag}"
    echo "  Base:   ${base_image}"
    echo "  Vendor: ${gpu_vendor} / ${gpu_arch}"
    echo "  Backends: vllm=${include_vllm} llamacpp=${include_llamacpp} ollama=${include_ollama} diffusers=${include_diffusers} steam=${include_steam} quantizer=${include_quantizer} turboquant=${include_turboquant}"
    echo "  Python: transformers=${transformers_install_mode}:${transformers_ref} vllm=${vllm_install_mode}:$(if [ "${vllm_install_mode}" = "wheel" ]; then echo "${vllm_version}"; else echo "${vllm_ref}"; fi) turboquant=${turboquant_install_mode}:$(if [ "${turboquant_install_mode}" = "none" ]; then echo none; elif [ "${turboquant_install_mode}" = "source" ]; then echo "${turboquant_ref}"; else echo "${turboquant_version}"; fi)"
    echo ""

    if [ "${DRY_RUN}" = "true" ]; then
        printf '[dry-run]'; printf ' %q' "${cmd[@]}"; printf '\n'
        echo ""
        echo "[dry-run] runtime.env contents:"
        sed 's/^/  /' "${env_file}"
        echo ""
        return 0
    fi

    printf '$'; printf ' %q' "${cmd[@]}"; printf '\n'
    "${cmd[@]}"

    if [ "${PUSH}" = "true" ]; then
        local -a push_cmd=("docker")
        if [ -n "${context_flag}" ]; then
            push_cmd+=("--context" "${build_context}")
        fi
        push_cmd+=("push" "${full_tag}")
        echo ""
        printf '$'; printf ' %q' "${push_cmd[@]}"; printf '\n'
        "${push_cmd[@]}"
    fi

    echo ""
    echo "=== Done: ${profile} → ${full_tag} ==="
    echo ""
}

# ── Main ─────────────────────────────────────────────────────────────
if [ "${PROFILE}" = "all" ]; then
    profiles=$(yq '.profiles | keys | .[]' "${CONFIG}")
    for p in ${profiles}; do
        build_profile "${p}"
    done
else
    build_profile "${PROFILE}"
fi
