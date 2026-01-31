---
title: ROCm GFX1100 Backend Guide
description: Running inference on AMD RX 7900 series GPUs (RDNA3)
---

# ROCm GFX1100 Backend Guide

FlexInfer provides optimized support for AMD RDNA3 GPUs (gfx1100 architecture) including the RX 7900 XTX, RX 7900 XT, and RX 7900 GRE. This guide covers the requirements and configuration for these consumer GPUs.

## Overview

GFX1100 GPUs offer excellent price-to-performance for LLM inference but require specific configuration due to differences from AMD's data center GPUs (MI300X, etc.).

| GPU | Architecture | VRAM | ROCm GFX | Notes |
|-----|-------------|------|----------|-------|
| RX 7900 XTX | RDNA3 | 24GB | gfx1100 | Best consumer option |
| RX 7900 XT | RDNA3 | 20GB | gfx1100 | Good balance |
| RX 7900 GRE | RDNA3 | 16GB | gfx1100 | Budget option |

## Key Requirements

### 1. ROCm 6.4+ Driver

GFX1100 requires ROCm 6.4 or newer for stable operation:

```bash
# Check ROCm version
rocminfo | grep -i "version"

# Check amdgpu driver
cat /sys/module/amdgpu/version
```

### 2. Environment Variables

FlexInfer automatically sets these for gfx1100 GPUs, but they're critical for correct operation:

```bash
# RDNA3 version override (required)
HSA_OVERRIDE_GFX_VERSION=11.0.0

# Enable stable flash attention on RDNA3
TORCH_ROCM_AOTRITON_ENABLE_EXPERIMENTAL=1

# Target architecture
PYTORCH_ROCM_ARCH=gfx1100
```

### 3. GFX1100-Optimized Images

FlexInfer uses specialized images for gfx1100 that are built with RDNA3-specific optimizations:

| Backend | Default GFX1100 Image |
|---------|----------------------|
| mlc-llm | `registry.harbor.lan/flexinfer/mlc-llm:rocm64-gfx1100` |
| vLLM | `registry.harbor.lan/flexinfer/vllm:rocm-gfx1100` |

## FlexInfer Configuration

### Basic ModelDeployment

```yaml
apiVersion: ai.flexinfer/v1alpha1
kind: ModelDeployment
metadata:
  name: qwen3-8b-gfx1100
  namespace: flexinfer-system
spec:
  backend: mlc-llm
  model: Qwen/Qwen3-8B
  modelCacheRef: qwen3-8b-mlc

  # Target gfx1100 GPUs specifically
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
    requests:
      memory: 24Gi

  minReplicas: 0
  replicas: 1
  idleTimeoutSeconds: 600
```

### ModelCache with Pre-quantized Model

```yaml
apiVersion: ai.flexinfer/v1alpha1
kind: ModelCache
metadata:
  name: qwen3-8b-mlc
  namespace: flexinfer-system
spec:
  source:
    type: huggingface
    repository: mlc-ai/Qwen3-8B-q4f16_1-MLC
  storageStrategy: SharedPVC
  storageClassName: longhorn
  storageSize: 10Gi
```

### vLLM Backend (Alternative)

For vLLM, additional environment variables are required to avoid GPU hangs:

```yaml
apiVersion: ai.flexinfer/v1alpha1
kind: ModelDeployment
metadata:
  name: mistral-7b-vllm-gfx1100
spec:
  backend: vllm
  model: mistralai/Mistral-7B-Instruct-v0.3
  modelCacheRef: mistral-7b-cache

  nodeSelector:
    flexinfer.ai/gpu.vendor: AMD
    flexinfer.ai/gpu.arch: gfx1100

  vllm:
    # GFX1100 requires V0 engine and disabled Triton flash attention
    extraEnv:
      - name: VLLM_USE_V1
        value: "0"
      - name: VLLM_USE_TRITON_FLASH_ATTN
        value: "0"
      - name: VLLM_ROCM_USE_AITER
        value: "0"

  resources:
    limits:
      amd.com/gpu: 1
      memory: 32Gi
```

## Complete Example

```yaml
# 1. Create ModelCache to download the model
apiVersion: ai.flexinfer/v1alpha1
kind: ModelCache
metadata:
  name: llama3-8b-gfx1100
  namespace: flexinfer-system
spec:
  source:
    type: huggingface
    repository: mlc-ai/Llama-3-8B-Instruct-q4f16_1-MLC
  storageStrategy: SharedPVC
  storageClassName: longhorn
  storageSize: 10Gi
---
# 2. Create ModelDeployment targeting gfx1100 nodes
apiVersion: ai.flexinfer/v1alpha1
kind: ModelDeployment
metadata:
  name: llama3-8b-gfx1100
  namespace: flexinfer-system
spec:
  backend: mlc-llm
  model: Llama-3-8B-Instruct-q4f16_1-MLC
  modelCacheRef: llama3-8b-gfx1100

  nodeSelector:
    flexinfer.ai/gpu.vendor: AMD
    flexinfer.ai/gpu.arch: gfx1100

  mlcllm:
    mode: local
    jitPolicy: READONLY
    gpuMemoryBytes: 22000000000  # ~22GB for RX 7900 XTX
    overrides:
      maxTotalSeqLength: 8192
      prefillChunkSize: 512

  resources:
    limits:
      amd.com/gpu: 1
      memory: 32Gi

  minReplicas: 0
  replicas: 1
  idleTimeoutSeconds: 600
  coldStartTimeoutSeconds: 180
```

## Node Detection

The FlexInfer agent automatically detects gfx1100 GPUs and applies labels:

```bash
# Labels set by flexinfer-agent
flexinfer.ai/gpu.vendor: AMD
flexinfer.ai/gpu.arch: gfx1100
flexinfer.ai/gpu.vram: 24Gi
```

Verify labels on your node:

```bash
kubectl get node <node-name> -o yaml | grep -A 10 "flexinfer.ai"
```

## Performance Expectations

GFX1100 GPUs provide competitive performance for LLM inference:

| Model | Quantization | VRAM Usage | Tokens/sec |
|-------|-------------|------------|------------|
| Llama-3-8B | q4f16_1 | ~6GB | ~80-100 |
| Qwen3-8B | q4f16_1 | ~6GB | ~85-105 |
| Mistral-7B | q4f16_1 | ~5GB | ~90-110 |
| Llama-3-70B | q4f16_1 | ~40GB | N/A (exceeds VRAM) |

For larger models (70B+), consider using multiple GPUs or switching to data center GPUs (MI300X).

## Recommended Quantizations

| Quantization | Memory | Speed | Quality | Recommended |
|--------------|--------|-------|---------|-------------|
| q4f16_1 | Low | Fast | Good | Yes |
| q4f32_1 | Medium | Medium | Better | For accuracy |
| q3f16_1 | Lowest | Fastest | Acceptable | For large models |

## Troubleshooting

### SIGSEGV Crashes During Inference

**Cause:** Flash attention compatibility issues with RDNA3.

**Solution:** Ensure environment variables are set:
```yaml
env:
  - name: TORCH_ROCM_AOTRITON_ENABLE_EXPERIMENTAL
    value: "1"
```

For vLLM:
```yaml
env:
  - name: VLLM_USE_TRITON_FLASH_ATTN
    value: "0"
  - name: VLLM_ROCM_USE_AITER
    value: "0"
```

### "HIP error: shared object initialization failed"

**Cause:** ROCm driver/userspace version mismatch.

**Solution:** Ensure host has ROCm 6.4+ driver:
```bash
# Check driver version
cat /sys/module/amdgpu/version

# Should show 6.4.x or higher
```

### GPU Hangs (System Freeze)

**Cause:** Using V1 engine or Triton flash attention with vLLM.

**Solution:** Force V0 engine:
```yaml
env:
  - name: VLLM_USE_V1
    value: "0"
```

### Out of Memory (OOM)

**Symptom:** `RuntimeError: HIP out of memory`

**Solutions:**
1. Reduce `maxTotalSeqLength` in overrides
2. Use smaller quantization (q4f16_1 instead of q4f32_1)
3. Set `prefillChunkSize: 512` to reduce buffer allocation
4. Use `mode: local` instead of `mode: server`

### Slow JIT Compilation

**Symptom:** First inference takes 5+ minutes.

**Solution:** Use pre-compiled models and disable JIT:
```yaml
mlcllm:
  jitPolicy: READONLY
```

Or use pre-built MLC models from HuggingFace that include compiled libraries.

## Building Custom Images

If you need to build a custom gfx1100 image:

```bash
cd services/flexinfer

# Build MLC-LLM for gfx1100
docker build \
  -f build/Dockerfile.mlc-rocm64-gfx1100 \
  -t registry.harbor.lan/flexinfer/mlc-llm:rocm64-gfx1100 \
  build/

# Build vLLM for gfx1100
docker build \
  -f build/Dockerfile.vllm-rocm-gfx1100 \
  -t registry.harbor.lan/flexinfer/vllm:rocm-gfx1100 \
  build/
```

Build time is approximately 30-45 minutes per image.

## Helm Configuration

Override the default gfx1100 image in values.yaml:

```yaml
mlcllm:
  gfx1100Image: "registry.harbor.lan/flexinfer/mlc-llm:rocm64-gfx1100"

vllm:
  gfx1100Image: "registry.harbor.lan/flexinfer/vllm:rocm-gfx1100"
```

Or set via environment variable:

```bash
export DEFAULT_MLC_LLM_IMAGE_GFX1100="my-registry/mlc-llm:custom-gfx1100"
```

## References

- [Full ROCm Build Guide](/build/README-rocm.md)
- [MLC-AI Models on HuggingFace](https://huggingface.co/mlc-ai)
- [MLC-LLM Documentation](https://llm.mlc.ai/docs/)
- [ROCm Installation Guide](https://rocm.docs.amd.com/projects/install-on-linux/en/latest/)
- [vLLM ROCm Support](https://docs.vllm.ai/en/latest/getting_started/amd-installation.html)
