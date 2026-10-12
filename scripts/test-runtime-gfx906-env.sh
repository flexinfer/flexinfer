#!/usr/bin/env bash
set -euo pipefail
root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
for override in "" "9.0.6"; do
    GPU_ARCH=gfx906 HSA_OVERRIDE_GFX_VERSION="$override" \
        bash "$root/build/runtime-entrypoint.sh" bash -c \
        'test -z "${HSA_OVERRIDE_GFX_VERSION+x}"'
done
GPU_ARCH=gfx1100 HSA_OVERRIDE_GFX_VERSION=11.0.0 \
    bash "$root/build/runtime-entrypoint.sh" bash -c \
    'test "$HSA_OVERRIDE_GFX_VERSION" = 11.0.0'
echo "gfx906 environment regression checks passed"
