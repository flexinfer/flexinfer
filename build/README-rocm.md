# MLC-LLM on ROCm (AMD GPUs)

This document covers building and running MLC-LLM on AMD GPUs with ROCm, specifically optimized for gfx1100 (RX 7900 XTX, RDNA3).

## Overview

| GPU | Architecture | ROCm GFX | VRAM | Notes |
|-----|-------------|----------|------|-------|
| RX 7900 XTX | RDNA3 | gfx1100 | 24GB | Primary target |
| RX 7900 XT | RDNA3 | gfx1100 | 20GB | Supported |
| RX 7800 XT | RDNA3 | gfx1101 | 16GB | Should work |
| MI300X | CDNA3 | gfx942 | 192GB | Data center |

## ROCm 6.4 Build

The `Dockerfile.mlc-rocm64-full` builds MLC-LLM from source with ROCm 6.4 support on Ubuntu 24.04.

### Why Build from Source?

1. **ROCm 6.4 requires Ubuntu 24.04** - The glibc 2.39 with GLIBCXX_3.4.32 is needed
2. **LLVM 19 compatibility** - ROCm 6.4 bitcode requires LLVM 19
3. **MLC-AI's TVM fork (relax)** - Better integration than upstream TVM

### Build the Image

```bash
cd services/flexinfer

# Build the full ROCm 6.4 image (takes ~30-45 minutes)
docker build \
  -f build/Dockerfile.mlc-rocm64-full \
  -t registry.harbor.lan/library/mlc-llm:rocm64-src \
  build/
```

### Key Build Options

The Dockerfile configures TVM with:
- `USE_ROCM ON` - Enable ROCm backend
- `USE_LLVM /usr/bin/llvm-config-19` - LLVM 19 for ROCm 6.4 compatibility
- `USE_ROCBLAS OFF` - Disabled due to API incompatibilities in relax fork
- `USE_MIOPEN OFF` - Disabled for stability

## Environment Variables for gfx1100

FlexInfer automatically sets these for AMD GPUs (see `backend/interface.go:ROCmEnvVars()`):

```bash
# Core ROCm settings
HSA_OVERRIDE_GFX_VERSION=11.0.0          # RDNA3 version override
TORCH_ROCM_AOTRITON_ENABLE_EXPERIMENTAL=1 # Enables stable flash attention
PYTORCH_ROCM_ARCH=gfx1100                # Target architecture
```

For vLLM specifically, additional settings are applied (see `backend/vllm.go`):

```bash
VLLM_USE_V1=0              # V0 engine is more stable on gfx1100
VLLM_USE_TRITON_FLASH_ATTN=0  # Use CK flash attention instead
VLLM_ROCM_USE_AITER=0      # Disable AITER which can cause crashes
```

## Running MLC-LLM

### Direct Docker Run

```bash
docker run --rm -it \
  --device=/dev/kfd \
  --device=/dev/dri \
  --group-add video \
  --security-opt seccomp=unconfined \
  -v /path/to/models:/models \
  -p 8000:8000 \
  -e HSA_OVERRIDE_GFX_VERSION=11.0.0 \
  -e TORCH_ROCM_AOTRITON_ENABLE_EXPERIMENTAL=1 \
  registry.harbor.lan/library/mlc-llm:rocm64-src \
  serve /models/Qwen3-8B-q4f16_1-MLC \
    --host 0.0.0.0 \
    --mode local
```

### Kubernetes Deployment

```yaml
apiVersion: ai.flexinfer/v1alpha1
kind: ModelDeployment
metadata:
  name: qwen3-8b-rocm
spec:
  backend: mlc-llm
  model: Qwen/Qwen3-8B
  modelCacheRef: qwen3-8b-mlc
  nodeSelector:
    flexinfer.ai/gpu.vendor: AMD
    flexinfer.ai/gpu.arch: gfx1100
  mlcllm:
    mode: local
    overrides:
      maxTotalSeqLength: 8192
      prefillChunkSize: 512
  resources:
    limits:
      amd.com/gpu: 1
      memory: 32Gi
```

## Troubleshooting

### SIGSEGV Crashes

**Symptom**: Segmentation fault during inference
**Cause**: Flash attention compatibility issues with gfx1100

**Solution**: Ensure environment variables are set:
```bash
TORCH_ROCM_AOTRITON_ENABLE_EXPERIMENTAL=1
```

For vLLM:
```bash
VLLM_USE_TRITON_FLASH_ATTN=0
VLLM_ROCM_USE_AITER=0
VLLM_USE_V1=0
```

### "HIP error: shared object initialization failed"

**Cause**: ROCm driver/userspace version mismatch

**Solution**: Ensure host has ROCm 6.4 driver installed:
```bash
# Check driver version
cat /sys/module/amdgpu/version

# Should show 6.4.x
```

### Out of Memory (OOM)

**Symptom**: `RuntimeError: HIP out of memory`

**Solutions**:
1. Reduce `maxTotalSeqLength` in overrides
2. Use smaller quantization (q4f16_1 vs q4f32_1)
3. Set `prefillChunkSize: 512` to reduce buffer allocation
4. Use `mode: local` instead of `mode: server`

### Slow Compilation

**Symptom**: JIT compilation takes 5+ minutes

**Solution**: Pre-compile models or use `jitPolicy: READONLY`:
```yaml
mlcllm:
  jitPolicy: READONLY
  modelLibPath: /models/compiled-lib.so
```

## Model Compatibility

### Recommended Quantizations

| Quantization | Memory | Speed | Quality |
|--------------|--------|-------|---------|
| q4f16_1 | Low | Fast | Good |
| q4f32_1 | Medium | Medium | Better |
| q3f16_1 | Lowest | Fastest | Acceptable |

### Pre-built Models

MLC-AI provides pre-quantized models on HuggingFace:
- [mlc-ai organization](https://huggingface.co/mlc-ai)

Look for models with `-MLC` suffix that include ROCm-compatible quantizations.

## Alternative Dockerfiles

| Dockerfile | Description |
|------------|-------------|
| `Dockerfile.mlc-rocm` | Older ROCm version, less stable |
| `Dockerfile.mlc-rocm64-build` | Build stage only |
| `Dockerfile.mlc-rocm64-hipblas` | HIPBlas backend variant |
| `Dockerfile.mlc-rocm64-full` | Recommended, generic ROCm source build |
| `Dockerfile.mlc-rocm64-gfx1100` | GFX1100 (RX 7900 series) optimized build |
| `Dockerfile.vllm-rocm-gfx1100` | vLLM for GFX1100 with flash attention disabled |

### Building GFX1100 Images

For RX 7900 XTX/XT/GRE (gfx1100 architecture):

```bash
# MLC-LLM for gfx1100
docker build \
  -f build/Dockerfile.mlc-rocm64-gfx1100 \
  -t registry.harbor.lan/flexinfer/mlc-llm:rocm64-gfx1100 \
  build/

# vLLM for gfx1100
docker build \
  -f build/Dockerfile.vllm-rocm-gfx1100 \
  -t registry.harbor.lan/flexinfer/vllm:rocm-gfx1100 \
  build/
```

The GFX1100-specific images include:
- `PYTORCH_ROCM_ARCH=gfx1100` for targeted kernel compilation
- `HSA_OVERRIDE_GFX_VERSION=11.0.0` for RDNA3 compatibility
- `TORCH_ROCM_AOTRITON_ENABLE_EXPERIMENTAL=1` for stable flash attention
- Flash attention disabled in vLLM (`BUILD_FA=0`) to prevent GPU hangs

## References

- [ROCm Installation Guide](https://rocm.docs.amd.com/projects/install-on-linux/en/latest/)
- [MLC-LLM Documentation](https://llm.mlc.ai/docs/)
- [MLC-AI TVM Fork (relax)](https://github.com/mlc-ai/relax)
