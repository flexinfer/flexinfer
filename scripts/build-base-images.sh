#!/usr/bin/env bash
# Build and push pre-built base images to Harbor for devbox sandboxes.
# These images include language runtimes + common tools, so project-specific
# Dockerfiles only need a thin deps layer on top.
#
# Usage: ./scripts/build-base-images.sh [--push]
set -euo pipefail

REGISTRY="${DEVBOX_REGISTRY:-registry.harbor.lan}"
PREFIX="mcp/devbox-base"
DOCKERFILE_DIR="$(cd "$(dirname "$0")/../internal/devbox/baseimage/dockerfiles" && pwd)"

PUSH=false
if [[ "${1:-}" == "--push" ]]; then
    PUSH=true
fi

images=(
    "go:1.24:go-1.24.Dockerfile"
    "go:1.25:go-1.25.Dockerfile"
    "python:3.13:python-3.13.Dockerfile"
    "node:22:node-22.Dockerfile"
)

for entry in "${images[@]}"; do
    IFS=: read -r lang ver dockerfile <<< "$entry"
    tag="${REGISTRY}/${PREFIX}/${lang}:${ver}"
    echo "=== Building ${tag} ==="
    docker build -t "$tag" -f "${DOCKERFILE_DIR}/${dockerfile}" "${DOCKERFILE_DIR}"

    if $PUSH; then
        echo "=== Pushing ${tag} ==="
        docker push "$tag"
    fi
done

echo "Done. Built ${#images[@]} base images."
