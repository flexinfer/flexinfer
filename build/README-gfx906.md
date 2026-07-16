# FlexInfer on AMD gfx906 (Radeon VII / MI50)

This document covers deploying inference backends on AMD gfx906 (Vega20) GPUs with FlexInfer.

## Hardware Overview

| GPU | Architecture | ROCm GFX | VRAM | Compute Units | Notes |
|-----|-------------|----------|------|---------------|-------|
| Radeon VII | Vega20 | gfx906 | 16GB HBM2 | 60 | Consumer |
| MI50 | Vega20 | gfx906 | 16GB HBM2 | 60 | Data center |
| MI60 | Vega20 | gfx906 | 32GB HBM2 | 64 | Data center |

## Supported Backends

| Backend | Support Level | Image | Notes |
|---------|-------------|-------|-------|
| llama.cpp | Full | `registry.harbor.lan/flexinfer/llamacpp:rocm-gfx906` | GGUF format, built with GGML_HIPBLAS |
| vLLM | Experimental | `registry.harbor.lan/flexinfer/vllm:rocm-gfx906` | Dedicated image path only; not included in the current unified `runtime:rocm-gfx906` profile |
| MLC-LLM | Experimental | `registry.harbor.lan/flexinfer/mlc-llm:rocm64-gfx906` | Pre-compiled libs recommended; canary before promotion |
| Ollama | Full | `ollama/ollama:rocm` (generic) | Works with runtime env overrides |
| Diffusers | Experimental | `registry.harbor.lan/flexinfer/runtime:rocm-gfx906` or dedicated ROCm image | Requires CPU offload and conservative 512px warmups |
| ComfyUI | Experimental | Dedicated ROCm image | Requires gfx906-specific env and canary validation |

## Key Environment Variables

FlexInfer automatically injects these via `backend/interface.go:ROCmEnvVars()`:

```bash
# Critical: Disable SDMA engine on Vega20 (prevents memory access faults)
HSA_ENABLE_SDMA=0

# Disable SVM to avoid Vega20 VMM/hipMemGetInfo failures.
HSA_USE_SVM=0

# Target architecture
PYTORCH_ROCM_ARCH=gfx906
```

### What NOT to Set

Unlike gfx1100, do **not** set these for gfx906:
- `HSA_OVERRIDE_GFX_VERSION` — serving runtimes detect the real gfx906 target; overriding it changes the certified environment
- `TORCH_ROCM_AOTRITON_ENABLE_EXPERIMENTAL` — AOTriton is gfx1100-specific

## Quantization Guidance

With 16GB HBM2 VRAM:

| Model Size | Recommended Quant | VRAM Usage | Notes |
|------------|-------------------|------------|-------|
| 7B | Q4_K_M (GGUF) | ~5GB | Comfortable fit |
| 7B | AWQ/GPTQ (vLLM) | ~5GB | Experimental until the vLLM gfx906 image is canary-promoted |
| 13B | Q4_K_M (GGUF) | ~8GB | Good fit |
| 30B MoE | Q4_K_M (GGUF) | ~14GB | Tight fit, reduce context |
| 70B | Q2_K (GGUF) | ~15GB | Minimal context only |

Avoid FP16/BF16 models — they exceed VRAM for all but the smallest models.

## Build Instructions

### llama.cpp

```bash
docker build \
  -f build/Dockerfile.llamacpp-rocm-gfx906 \
  -t registry.harbor.lan/flexinfer/llamacpp:rocm-gfx906 \
  .
```

### vLLM

This image is a FlexInfer-owned source build. It uses the official
`rocm/vllm-dev:base` image, pinned by digest, only as the build environment so
HIP, CMake, and PyTorch are from a coherent ROCm stack. It then clones the
pinned vLLM tag and builds the wheel with `BUILD_FA=0` for gfx906.

The vLLM gfx906 image is a canary path, not part of the default unified runtime.
Validate it on hardware and update the GPUProfile support level before using it
as an operator-default backend.

```bash
docker build \
  -f build/Dockerfile.vllm-rocm-gfx906 \
  -t registry.harbor.lan/flexinfer/vllm:rocm-gfx906 \
  .
```

## Kubernetes Deployment

### llama.cpp (GGUF)

```yaml
apiVersion: ai.flexinfer/v1alpha2
kind: Model
metadata:
  name: mistral-7b-gfx906
spec:
  backend: llamacpp
  source: "HF://TheBloke/Mistral-7B-Instruct-v0.2-GGUF"
  modelFileName: "mistral-7b-instruct-v0.2.Q4_K_M.gguf"
  nodeSelector:
    flexinfer.ai/gpu.arch: gfx906
  resources:
    limits:
      amd.com/gpu: 1
      memory: 20Gi
  config:
    contextSize: 8192
    nGPULayers: 999
```

### vLLM

Use this manifest only for canary validation. The default `gfx906` GPUProfile
keeps vLLM experimental until a runtime/image digest has passed smoke tests.

```yaml
apiVersion: ai.flexinfer/v1alpha2
kind: Model
metadata:
  name: vllm-mistral-gfx906
spec:
  backend: vllm
  source: "HF://TheBloke/Mistral-7B-Instruct-v0.2-AWQ"
  nodeSelector:
    flexinfer.ai/gpu.arch: gfx906
  resources:
    limits:
      amd.com/gpu: 1
      memory: 20Gi
  config:
    maxModelLen: 8192
    dtype: auto
```

### Ollama

```yaml
apiVersion: ai.flexinfer/v1alpha2
kind: Model
metadata:
  name: ollama-mistral-gfx906
spec:
  backend: ollama
  source: "ollama://mistral:7b"
  nodeSelector:
    flexinfer.ai/gpu.arch: gfx906
  resources:
    limits:
      amd.com/gpu: 1
      memory: 20Gi
```

## Troubleshooting

### "HSA_STATUS_ERROR_MEMORY_APERTURE_VIOLATION"

**Cause**: SDMA engine issue on Vega20.

**Solution**: Verify `HSA_ENABLE_SDMA=0` is set. FlexInfer injects this automatically,
but verify with:
```bash
kubectl exec <pod> -- env | grep HSA_ENABLE_SDMA
```

### "HIP error: invalid device function"

**Cause**: Binary was compiled for a different GPU architecture.

**Solution**: Ensure the image targets gfx906. Check with:
```bash
kubectl exec <pod> -- env | grep PYTORCH_ROCM_ARCH
# Should show: PYTORCH_ROCM_ARCH=gfx906
```

### vLLM CMake failure on ROCm 6.4.4

**Cause**: gfx906 support is fragile in newer ROCm releases, and the old vLLM
source-build path mixed ROCm 6.4.4 headers with a PyTorch ROCm wheel from a
different ROCm generation.

**Solution**: Use `build/Dockerfile.vllm-rocm-gfx906`, which keeps the custom
FlexInfer runtime image but builds from the official ROCm vLLM development base
instead of mixing a generic ROCm base with a PyTorch wheel from another ROCm
generation.

### vLLM 0.7.3 WARP_SIZE compile failure on ROCm 7.x

**Cause**: ROCm 7.x exposes HIP `warpSize` as a device-only object. vLLM 0.7.3
uses `WARP_SIZE` inside host/device dispatch code, which fails while compiling
MoE alignment kernels for gfx906.

**Solution**: `build/Dockerfile.vllm-rocm-gfx906` patches vLLM's HIP-side
`#define WARP_SIZE warpSize` macros to `#define WARP_SIZE 64`, matching the
gfx906 wavefront size.

### Out of Memory

16GB VRAM limits model size. Solutions:
1. Use smaller quantizations (Q4_K_M, Q3_K_M for GGUF; AWQ/GPTQ 4-bit for vLLM)
2. Reduce context length (`contextSize`, `maxModelLen`)
3. For llama.cpp, reduce `nGPULayers` to offload some layers to CPU

### Diffusers/ComfyUI Issues

These backends use generic ROCm images baked with gfx1100 environment variables.
FlexInfer overrides these at runtime via `ROCmEnvVars(gfx906)`, but this is
experimental. If issues occur, check that the runtime env vars took effect:
```bash
kubectl exec <pod> -- env | grep -E 'HSA|PYTORCH'
```
