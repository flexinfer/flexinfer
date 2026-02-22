#!/bin/bash
# Patch ggml-cuda.cu to handle hipMemGetInfo failure on gfx906.
# gfx906 (Radeon VII) returns hipErrorInvalidValue when VMM is not supported.
# This patch makes the failure non-fatal by falling back to sysfs or a hardcoded 16 GB.

set -euo pipefail

FILE="ggml/src/ggml-cuda/ggml-cuda.cu"

if [ ! -f "$FILE" ]; then
    echo "ERROR: $FILE not found"
    exit 1
fi

# Replace the CUDA_CHECK wrapped cudaMemGetInfo call with a fallback
# The exact pattern is: CUDA_CHECK(cudaMemGetInfo(free, total));
if grep -q 'CUDA_CHECK(cudaMemGetInfo(free, total))' "$FILE"; then
    sed -i 's|CUDA_CHECK(cudaMemGetInfo(free, total));|{ \
        auto _err = cudaMemGetInfo(free, total); \
        if (_err != cudaSuccess) { \
            *total = 0; *free = 0; \
            FILE * _f = fopen("/sys/class/drm/card0/device/mem_info_vram_total", "r"); \
            if (_f) { fscanf(_f, "%zu", total); fclose(_f); *free = *total; } \
            if (*total == 0) { *total = (size_t)16 * 1024 * 1024 * 1024; *free = *total; } \
            GGML_LOG_WARN("%s: cudaMemGetInfo failed (err=%d), sysfs fallback: %.2f GB\\n", \
                          __func__, (int)_err, (double)*total / 1e9); \
        } \
    }|' "$FILE"
    echo "Patched $FILE: hipMemGetInfo fallback applied"
else
    echo "WARNING: Could not find 'CUDA_CHECK(cudaMemGetInfo(free, total))' in $FILE"
    echo "Searching for alternative patterns..."
    grep -n 'cudaMemGetInfo\|hipMemGetInfo' "$FILE" || true
    # Try alternative: the function may use hipMemGetInfo directly in HIP builds
    if grep -q 'CUDA_CHECK(hipMemGetInfo(free, total))' "$FILE"; then
        sed -i 's|CUDA_CHECK(hipMemGetInfo(free, total));|{ \
            auto _err = hipMemGetInfo(free, total); \
            if (_err != hipSuccess) { \
                *total = 0; *free = 0; \
                FILE * _f = fopen("/sys/class/drm/card0/device/mem_info_vram_total", "r"); \
                if (_f) { fscanf(_f, "%zu", total); fclose(_f); *free = *total; } \
                if (*total == 0) { *total = (size_t)16 * 1024 * 1024 * 1024; *free = *total; } \
                GGML_LOG_WARN("%s: hipMemGetInfo failed (err=%d), sysfs fallback: %.2f GB\\n", \
                              __func__, (int)_err, (double)*total / 1e9); \
            } \
        }|' "$FILE"
        echo "Patched $FILE: hipMemGetInfo fallback applied (HIP variant)"
    else
        echo "ERROR: No patchable pattern found. Build will likely fail on gfx906."
        grep -n -B2 -A2 'MemGetInfo' "$FILE" || true
        exit 1
    fi
fi
