#!/usr/bin/env bash
# deploy-quantizer.sh — Automate the quantizer build+deploy pipeline.
#
# Builds and pushes the quantizer image, extracts the digest, updates the
# GPUProfile YAML, and applies it to the cluster. Optionally rebuilds the
# controller and/or restarts a named quantization job.
#
# Usage:
#   deploy-quantizer.sh ARCH [FORMAT] [--controller] [--restart-job NAME]
#
# Examples:
#   deploy-quantizer.sh gfx1100                # Build GPTQ quantizer for gfx1100, update+apply GPUProfile
#   deploy-quantizer.sh gfx1100 --controller   # Above + rebuild controller
#   deploy-quantizer.sh gfx906 --restart-job gemma4-26b-a4b-gptq-quantize
#
# Environment:
#   DOCKER_CONTEXT_GPU   Docker context (default: 7900xtx)
#   HARBOR_REGISTRY      Registry hostname (default: registry.harbor.lan)
#   KUBECONFIG           Kubernetes config (defaults to k3s.yaml in repo)
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd "${SCRIPT_DIR}/.." && pwd)"

# Defaults
DOCKER_CONTEXT="${DOCKER_CONTEXT_GPU:-7900xtx}"
REGISTRY="${HARBOR_REGISTRY:-registry.harbor.lan}"
FORMAT="gptq"
REBUILD_CONTROLLER=false
RESTART_JOB=""

# Parse arguments
ARCH=""
while [[ $# -gt 0 ]]; do
    case "$1" in
        --controller)
            REBUILD_CONTROLLER=true
            shift
            ;;
        --restart-job)
            RESTART_JOB="$2"
            shift 2
            ;;
        --format)
            FORMAT="$2"
            shift 2
            ;;
        -h|--help)
            sed -n '2,/^set -/{ /^#/s/^# \?//p }' "$0"
            exit 0
            ;;
        *)
            if [[ -z "${ARCH}" ]]; then
                ARCH="$1"
            else
                echo "ERROR: unexpected argument: $1" >&2
                exit 1
            fi
            shift
            ;;
    esac
done

if [[ -z "${ARCH}" ]]; then
    echo "ERROR: ARCH argument required (e.g., gfx1100, gfx906)" >&2
    exit 1
fi

GPUPROFILE_YAML="${REPO_ROOT}/deploy/gpuprofiles/${ARCH}.yaml"
if [[ ! -f "${GPUPROFILE_YAML}" ]]; then
    echo "ERROR: GPUProfile YAML not found: ${GPUPROFILE_YAML}" >&2
    exit 1
fi

# Resolve Dockerfile
DOCKERFILE=""
case "${FORMAT}" in
    gptq)
        if [[ "${ARCH}" == "gfx906" ]]; then
            DOCKERFILE="build/Dockerfile.quantizer-gptq-rocm-gfx906"
        else
            DOCKERFILE="build/Dockerfile.quantizer-gptq-rocm"
        fi
        ;;
    awq)
        DOCKERFILE="build/Dockerfile.quantizer-awq-rocm"
        ;;
    *)
        echo "ERROR: unsupported format: ${FORMAT}" >&2
        exit 1
        ;;
esac

if [[ ! -f "${REPO_ROOT}/${DOCKERFILE}" ]]; then
    echo "ERROR: Dockerfile not found: ${DOCKERFILE}" >&2
    exit 1
fi

IMAGE_NAME="${REGISTRY}/flexinfer/quantizer:${FORMAT}-rocm-${ARCH}"

echo "=== Step 1/5: Build quantizer image ==="
echo "  Dockerfile: ${DOCKERFILE}"
echo "  Image:      ${IMAGE_NAME}"
echo "  Context:    ${DOCKER_CONTEXT}"
docker --context "${DOCKER_CONTEXT}" build \
    --no-cache \
    -f "${REPO_ROOT}/${DOCKERFILE}" \
    -t "${IMAGE_NAME}" \
    "${REPO_ROOT}"

# Post-build sanity check: confirm the script embedded in the image matches
# the local source. We hit a BuildKit regression where `docker build --no-cache`
# silently shipped stale script content despite new local edits; catching that
# at build time is much cheaper than discovering it via a stuck quantize job.
SCRIPT_PATH_LOCAL=""
SCRIPT_PATH_IMAGE=""
case "${FORMAT}" in
    gptq)
        SCRIPT_PATH_LOCAL="${REPO_ROOT}/build/scripts/quantize_gptq.py"
        SCRIPT_PATH_IMAGE="/opt/flexinfer/scripts/quantize_gptq.py"
        ;;
    awq)
        SCRIPT_PATH_LOCAL="${REPO_ROOT}/build/scripts/quantize_awq.py"
        SCRIPT_PATH_IMAGE="/opt/flexinfer/scripts/quantize_awq.py"
        ;;
esac
if [[ -n "${SCRIPT_PATH_LOCAL}" && -f "${SCRIPT_PATH_LOCAL}" ]]; then
    LOCAL_MD5=$(md5sum "${SCRIPT_PATH_LOCAL}" | awk '{print $1}')
    IMAGE_MD5=$(docker --context "${DOCKER_CONTEXT}" run --rm --entrypoint "" \
        "${IMAGE_NAME}" md5sum "${SCRIPT_PATH_IMAGE}" 2>/dev/null | awk '{print $1}')
    if [[ "${LOCAL_MD5}" != "${IMAGE_MD5}" ]]; then
        echo "ERROR: Image script content mismatch — BuildKit likely shipped stale content." >&2
        echo "  Local ${SCRIPT_PATH_LOCAL}: md5=${LOCAL_MD5}" >&2
        echo "  Image ${SCRIPT_PATH_IMAGE}: md5=${IMAGE_MD5}" >&2
        echo "  Try a fresh docker context (docker system prune on the remote) or rebuild manually." >&2
        exit 2
    fi
    echo "  Script parity verified: md5=${LOCAL_MD5}"
fi

echo ""
echo "=== Step 2/5: Push to registry ==="
docker --context "${DOCKER_CONTEXT}" push "${IMAGE_NAME}"

echo ""
echo "=== Step 3/5: Extract digest ==="
DIGEST=$(docker --context "${DOCKER_CONTEXT}" inspect --format='{{index .RepoDigests 0}}' "${IMAGE_NAME}" 2>/dev/null || true)
if [[ -z "${DIGEST}" ]]; then
    # Fallback: pull manifest to get digest
    DIGEST=$(docker --context "${DOCKER_CONTEXT}" manifest inspect "${IMAGE_NAME}" 2>/dev/null | python3 -c "import sys,json; print('${REGISTRY}/flexinfer/quantizer@' + json.load(sys.stdin).get('digest',''))" 2>/dev/null || true)
fi
if [[ -z "${DIGEST}" || "${DIGEST}" == *"@" ]]; then
    echo "WARNING: Could not extract digest. Using tag reference instead."
    DIGEST="${IMAGE_NAME}"
fi
echo "  Digest: ${DIGEST}"

echo ""
echo "=== Step 4/5: Update GPUProfile YAML ==="
# Replace the image line for the format under quantization.images
# Match pattern: "      <format>: <old-image>"
if grep -q "^      ${FORMAT}:" "${GPUPROFILE_YAML}"; then
    sed -i.bak "s|^      ${FORMAT}:.*|      ${FORMAT}: ${DIGEST}|" "${GPUPROFILE_YAML}"
    rm -f "${GPUPROFILE_YAML}.bak"
    echo "  Updated ${FORMAT} image in ${GPUPROFILE_YAML}"
else
    echo "WARNING: No '${FORMAT}:' entry found in ${GPUPROFILE_YAML}"
    echo "  Add manually:  ${FORMAT}: ${DIGEST}"
fi

echo ""
echo "=== Step 5/5: Apply GPUProfile to cluster ==="
kubectl apply -f "${GPUPROFILE_YAML}"
echo "  Applied ${GPUPROFILE_YAML}"

# Optional: rebuild controller
if [[ "${REBUILD_CONTROLLER}" == "true" ]]; then
    echo ""
    echo "=== Bonus: Rebuild + deploy controller ==="
    CONTROLLER_IMAGE="${REGISTRY}/flexinfer/flexinfer-controller:master"
    CONTROLLER_DOCKERFILE="${REPO_ROOT}/build/Dockerfile.manager"

    echo "  Building controller..."
    docker --context "${DOCKER_CONTEXT}" build \
        --no-cache \
        -f "${CONTROLLER_DOCKERFILE}" \
        -t "${CONTROLLER_IMAGE}" \
        "${REPO_ROOT}"

    echo "  Pushing controller..."
    docker --context "${DOCKER_CONTEXT}" push "${CONTROLLER_IMAGE}"

    echo "  Rolling out..."
    kubectl rollout restart deployment/flexinfer-controller -n flexinfer-system
    kubectl rollout status deployment/flexinfer-controller -n flexinfer-system --timeout=120s
    echo "  Controller rollout complete."
fi

# Optional: restart a quantization job
if [[ -n "${RESTART_JOB}" ]]; then
    echo ""
    echo "=== Bonus: Restart job ${RESTART_JOB} ==="
    kubectl delete job "${RESTART_JOB}" -n flexinfer-system --ignore-not-found
    echo "  Deleted job ${RESTART_JOB} (controller will recreate with new image)."
fi

echo ""
echo "=== Summary ==="
echo "  Arch:       ${ARCH}"
echo "  Format:     ${FORMAT}"
echo "  Image:      ${DIGEST}"
echo "  GPUProfile: ${GPUPROFILE_YAML}"
echo "  Controller: ${REBUILD_CONTROLLER}"
echo "  Job:        ${RESTART_JOB:-<none>}"
echo ""
echo "Done. The controller will pick up the new image on next reconcile."
