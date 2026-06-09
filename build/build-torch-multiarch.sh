#!/usr/bin/env bash
# Off-CI builder for the multi-arch (gfx906;gfx1100) PyTorch 2.4.0 / ROCm 6.3.4
# wheel image.
#
# WHY OFF-CI
#   This wheel is a from-source 2-arch torch + aotriton build whose peak working
#   set (>130 GB) exceeds the in-cluster buildkitd-central VMs (~134 GB free,
#   14 GB RAM each). Those pods get evicted mid-build, wiping the emptyDir cache
#   and canceling the build (observed 3x: snapshot "no such file" + "context
#   canceled"). The build itself is sound — clean configure, both arches compile.
#   So we build it ONCE on a beefy host (the 7900xtx Docker builder: 24 cores,
#   62 GB RAM, ~496 GB free) and push the result to Harbor. Downstream runtime
#   images then `COPY --from=$IMAGE /wheels/torch-*.whl` (the Dockerfile already
#   stashes the wheel at /wheels/) instead of rebuilding torch in CI.
#
#   The companion CI job `publish_torch_multiarch` is kept MANUAL-only and is NOT
#   the canonical path — it will not fit buildkitd-central. Use this script.
#
# USAGE
#   build/build-torch-multiarch.sh                 # build+verify+push (default)
#   DOCKER_CONTEXT=7900xtx build/build-torch-multiarch.sh
#   PUSH=0 build/build-torch-multiarch.sh          # build+verify only, no push
set -euo pipefail

CONTEXT="${DOCKER_CONTEXT:-7900xtx}"
REGISTRY="${REGISTRY:-registry.harbor.lan/flexinfer}"
BASE_IMAGE="${BASE_IMAGE:-rocm/pytorch:rocm6.3.4_ubuntu22.04_py3.10_pytorch_release_2.4.0}"
PYTORCH_ROCM_ARCH="${PYTORCH_ROCM_ARCH:-gfx906;gfx1100}"
PYTORCH_REF="${PYTORCH_REF:-v2.4.0}"
PUSH="${PUSH:-1}"

cd "$(dirname "$0")/.."   # repo root = docker build context
SHA="$(git rev-parse --short HEAD 2>/dev/null || echo nogit)"
IMAGE="${REGISTRY}/torch:rocm6.3.4-multiarch"
SHATAG="${IMAGE}-${SHA}"

echo ">> Building ${IMAGE} on docker context '${CONTEXT}' (arch=${PYTORCH_ROCM_ARCH}, ref=${PYTORCH_REF})"
docker --context "${CONTEXT}" build \
  -f build/Dockerfile.runtime-torch-multiarch \
  --build-arg BASE_IMAGE="${BASE_IMAGE}" \
  --build-arg PYTORCH_ROCM_ARCH="${PYTORCH_ROCM_ARCH}" \
  --build-arg PYTORCH_REF="${PYTORCH_REF}" \
  -t "${IMAGE}" -t "${SHATAG}" \
  .

# --- Kill-test gate: the wheel MUST carry device code for BOTH arches ---------
# Inspect the embedded code-object arches of libtorch_hip.so (no GPU needed).
echo ">> Verifying embedded arches in libtorch_hip.so ..."
ARCHES="$(docker --context "${CONTEXT}" run --rm --entrypoint bash "${IMAGE}" -c '
  TLIB="$(python3 -c "import torch,os;print(os.path.dirname(torch.__file__))")/lib/libtorch_hip.so"
  (roc-obj-ls "$TLIB" 2>/dev/null || llvm-objdump --offloading "$TLIB" 2>/dev/null) \
    | grep -ioE "gfx[0-9a-f]+" | sort -u | tr "\n" " "')"
echo "   embedded arches: ${ARCHES}"
for need in gfx906 gfx1100; do
  case " ${ARCHES} " in
    *" ${need} "*) ;;
    *) echo "FAIL: ${need} missing from libtorch_hip.so — not pushing." >&2; exit 2 ;;
  esac
done
echo ">> OK: both gfx906 and gfx1100 present."

if [ "${PUSH}" = "1" ]; then
  echo ">> Pushing ${IMAGE} and ${SHATAG} ..."
  docker --context "${CONTEXT}" push "${IMAGE}"
  docker --context "${CONTEXT}" push "${SHATAG}"
  echo ">> Done: ${IMAGE} (carries /wheels/torch-*.whl for downstream COPY --from)."
else
  echo ">> PUSH=0, skipping push. Image built locally on context '${CONTEXT}'."
fi
