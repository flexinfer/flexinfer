#!/usr/bin/env bash
#
# verify-images.sh - Verify FlexInfer backend images exist in Harbor registry
#
# This script checks that all required backend images are available before
# deployment. Useful for catching missing builds early.
#
# Usage:
#   ./scripts/verify-images.sh           # Check all images
#   ./scripts/verify-images.sh --quick   # Only check critical images
#
set -euo pipefail

# Color output
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
NC='\033[0m' # No Color

HARBOR_REGISTRY="${HARBOR_REGISTRY:-registry.harbor.lan}"

# Backend images to verify
declare -A IMAGES=(
    # MLC-LLM backends
    ["mlc-llm:rocm64-src"]="library/mlc-llm:rocm64-src"
    ["mlc-llm:rocm64-gfx906"]="flexinfer/mlc-llm:rocm64-gfx906"
    ["mlc-llm:cuda-maxwell-v7"]="flexinfer/mlc-llm:cuda-maxwell-v7"
    ["mlc-llm:latest"]="library/mlc-llm:latest"

    # vLLM backends
    ["vllm-api:rocm-gfx906"]="library/vllm-api:rocm-gfx906"
    ["vllm-api:rocm-gfx1100"]="library/vllm-api:rocm-gfx1100"
    ["vllm-api:rocm-navi"]="library/vllm-api:rocm-navi"

    # Diffusers backends
    ["diffusers-api:rocm-latest"]="library/diffusers-api:rocm-latest"
)

# Critical images that must exist for basic operation
CRITICAL_IMAGES=(
    "mlc-llm:rocm64-src"
    "mlc-llm:latest"
)

check_image() {
    local name="$1"
    local path="$2"
    local full_url="${HARBOR_REGISTRY}/${path}"

    # Use docker manifest inspect to check if image exists
    # This works with Harbor and doesn't require pulling the image
    if docker manifest inspect "${full_url}" >/dev/null 2>&1; then
        echo -e "  ${GREEN}✓${NC} ${name}"
        return 0
    else
        echo -e "  ${RED}✗${NC} ${name} (${full_url})"
        return 1
    fi
}

check_image_curl() {
    local name="$1"
    local path="$2"

    # Parse repository and tag
    local repo="${path%:*}"
    local tag="${path##*:}"

    # Harbor v2 API to check manifest
    local url="https://${HARBOR_REGISTRY}/v2/${repo}/manifests/${tag}"

    if curl -sf -o /dev/null -w "%{http_code}" \
        -H "Accept: application/vnd.oci.image.manifest.v1+json" \
        -H "Accept: application/vnd.docker.distribution.manifest.v2+json" \
        "${url}" 2>/dev/null | grep -q "200"; then
        echo -e "  ${GREEN}✓${NC} ${name}"
        return 0
    else
        echo -e "  ${RED}✗${NC} ${name} (${HARBOR_REGISTRY}/${path})"
        return 1
    fi
}

main() {
    local quick_mode=false
    local failed=0
    local total=0

    # Parse args
    while [[ $# -gt 0 ]]; do
        case $1 in
            --quick)
                quick_mode=true
                shift
                ;;
            -h|--help)
                echo "Usage: $0 [--quick]"
                echo ""
                echo "Options:"
                echo "  --quick    Only check critical images"
                echo ""
                echo "Environment:"
                echo "  HARBOR_REGISTRY    Harbor registry URL (default: registry.harbor.lan)"
                exit 0
                ;;
            *)
                echo "Unknown option: $1"
                exit 1
                ;;
        esac
    done

    echo "Checking backend images in ${HARBOR_REGISTRY}..."
    echo ""

    if $quick_mode; then
        echo "Critical images:"
        for name in "${CRITICAL_IMAGES[@]}"; do
            local path="${IMAGES[$name]}"
            ((total++))
            if ! check_image "${name}" "${path}"; then
                ((failed++))
            fi
        done
    else
        echo "All backend images:"
        for name in "${!IMAGES[@]}"; do
            local path="${IMAGES[$name]}"
            ((total++))
            if ! check_image "${name}" "${path}"; then
                ((failed++))
            fi
        done
    fi

    echo ""
    if [[ $failed -eq 0 ]]; then
        echo -e "${GREEN}All ${total} images verified successfully.${NC}"
        exit 0
    else
        echo -e "${YELLOW}${failed}/${total} images missing.${NC}"
        echo ""
        echo "Build missing images with:"
        echo "  make build-mlc-rocm64 push-mlc-rocm64   # ROCm 6.4 gfx1100"
        echo "  make build-mlc-maxwell push-mlc-maxwell # CUDA Maxwell"
        echo ""
        echo "Or trigger CI builds manually in GitLab."
        exit 1
    fi
}

main "$@"
