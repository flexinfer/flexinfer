#!/usr/bin/env bash
# Off-CI builder for the unified multi-arch (gfx906;gfx1100) vLLM serving image.
#
# WHY OFF-CI: builds FROM the multi-arch torch wheel and source-compiles vLLM for
# both arches — a >130GB working set that the in-cluster buildkitd-central VMs
# (~134GB free, 14GB RAM) cannot hold (they evict mid-build). Build on a beefy
# host (the 7900xtx Docker builder: 24c/62G/~496G free); the companion CI job
# `publish_vllm_multiarch` is MANUAL-only break-glass and will fail on buildkit.
#
# Gates the push on a dual-arch check of vLLM's compiled C-extension.
#
# USAGE:
#   build/build-vllm-multiarch.sh                 # build + verify + push
#   PUSH=0 build/build-vllm-multiarch.sh          # build + verify only
set -euo pipefail

CONTEXT="${DOCKER_CONTEXT:-7900xtx}"
REGISTRY="${REGISTRY:-registry.harbor.lan/flexinfer}"
PYTORCH_ROCM_ARCH="${PYTORCH_ROCM_ARCH:-gfx906;gfx1100}"
VLLM_REF="${VLLM_REF:-v0.6.3.post1}"
PUSH="${PUSH:-1}"

cd "$(dirname "$0")/.."   # repo root = docker build context
SHA="$(git rev-parse --short HEAD 2>/dev/null || echo nogit)"
IMAGE="${REGISTRY}/vllm:rocm6.3.4-multiarch"
SHATAG="${IMAGE}-${SHA}"

echo ">> Building ${IMAGE} on docker context '${CONTEXT}' (arch=${PYTORCH_ROCM_ARCH}, vllm=${VLLM_REF})"
docker --context "${CONTEXT}" build \
  -f build/Dockerfile.vllm-rocm-multiarch \
  --build-arg PYTORCH_ROCM_ARCH="${PYTORCH_ROCM_ARCH}" \
  --build-arg VLLM_REF="${VLLM_REF}" \
  -t "${IMAGE}" -t "${SHATAG}" \
  .

echo ">> Verifying both arches in vLLM's compiled C-extension ..."
ARCHES="$(docker --context "${CONTEXT}" run --rm --entrypoint bash "${IMAGE}" -c '
  VC="$(find /opt/conda -name "_C*.so" -path "*vllm*" | head -1)"
  (roc-obj-ls "$VC" 2>/dev/null || llvm-objdump --offloading "$VC" 2>/dev/null) \
    | grep -ioE "gfx[0-9a-f]+" | sort -u | tr "\n" " "')"
echo "   embedded arches: ${ARCHES}"
for need in gfx906 gfx1100; do
  case " ${ARCHES} " in
    *" ${need} "*) ;;
    *) echo "FAIL: ${need} missing from vLLM _C.so — not pushing." >&2; exit 2 ;;
  esac
done
echo ">> OK: both gfx906 and gfx1100 present."

if [ "${PUSH}" = "1" ]; then
  echo ">> Pushing ${IMAGE} and ${SHATAG} ..."
  docker --context "${CONTEXT}" push "${IMAGE}"
  docker --context "${CONTEXT}" push "${SHATAG}"
  echo ">> Done: ${IMAGE}"
else
  echo ">> PUSH=0, skipping push."
fi
