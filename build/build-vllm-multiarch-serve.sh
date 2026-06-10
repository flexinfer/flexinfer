#!/usr/bin/env bash
# Builder for the serving-lane derivative of the unified multi-arch vLLM image
# (request-path fixes baked in — see Dockerfile.vllm-rocm-multiarch-serve).
#
# Unlike build-vllm-multiarch.sh this is a thin patch layer on the already
# pushed base image: minutes, no source compile, no big working set. Still run
# it on the 7900xtx docker context so the base layers are cache-local.
#
# USAGE:
#   build/build-vllm-multiarch-serve.sh          # build + verify + push
#   PUSH=0 build/build-vllm-multiarch-serve.sh   # build + verify only
set -euo pipefail

CONTEXT="${DOCKER_CONTEXT:-7900xtx}"
REGISTRY="${REGISTRY:-registry.harbor.lan/flexinfer}"
BASE_IMAGE="${BASE_IMAGE:-${REGISTRY}/vllm:rocm6.3.4-multiarch}"
PUSH="${PUSH:-1}"

cd "$(dirname "$0")/.."   # repo root = docker build context
SHA="$(git rev-parse --short HEAD 2>/dev/null || echo nogit)"
IMAGE="${REGISTRY}/vllm:rocm6.3.4-multiarch-serve"
SHATAG="${IMAGE}-${SHA}"

echo ">> Building ${IMAGE} on docker context '${CONTEXT}' (base=${BASE_IMAGE})"
docker --context "${CONTEXT}" build \
  -f build/Dockerfile.vllm-rocm-multiarch-serve \
  --build-arg BASE_IMAGE="${BASE_IMAGE}" \
  -t "${IMAGE}" -t "${SHATAG}" \
  .

echo ">> Verifying the guided-decoding short-circuit survived into the image ..."
N="$(docker --context "${CONTEXT}" run --rm --entrypoint bash "${IMAGE}" -c '
  SP="$(python3 -c "import site;print([p for p in site.getsitepackages() if p.endswith(\"site-packages\")][0])")"
  grep -c "flexinfer guided decoding disabled" "$SP/vllm/model_executor/guided_decoding/__init__.py"')"
if [ "${N}" != "2" ]; then
  echo "FAIL: expected 2 short-circuited entry points, found ${N} — not pushing." >&2
  exit 2
fi
echo ">> OK: 2 guided-decoding entry points short-circuited."

if [ "${PUSH}" = "1" ]; then
  echo ">> Pushing ${IMAGE} and ${SHATAG} ..."
  docker --context "${CONTEXT}" push "${IMAGE}"
  docker --context "${CONTEXT}" push "${SHATAG}"
  echo ">> Pushed digest (pin this in deploy/gpuprofiles/gfx906.yaml):"
  docker --context "${CONTEXT}" image inspect "${IMAGE}" --format '{{range .RepoDigests}}{{.}}{{"\n"}}{{end}}' | head -2
else
  echo ">> PUSH=0, skipping push."
fi
