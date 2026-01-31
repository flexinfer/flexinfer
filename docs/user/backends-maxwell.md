---
title: Maxwell GPU Backend Guide
description: Running inference on NVIDIA Maxwell GPUs (GTX 980 Ti, etc.)
---

# Maxwell GPU Backend Guide

FlexInfer supports NVIDIA Maxwell architecture GPUs (compute capability 5.x) with MLC-LLM. This guide covers the requirements and configuration for Maxwell GPUs.

## Overview

Maxwell GPUs like the GTX 980 Ti can run LLM inference, but have a critical limitation: **no native FP16 support**. This means you must use FP32 quantized models.

| GPU | Compute Capability | VRAM | FP16 Support |
|-----|-------------------|------|--------------|
| GTX 980 Ti | 5.2 | 6GB | No |
| GTX Titan X | 5.2 | 12GB | No |
| Tesla M40 | 5.2 | 12GB | No |

## Key Requirement: FP32 Models Only

Maxwell GPUs lack FP16 hardware intrinsics. Models quantized with `q4f16_1` (4-bit weights, FP16 activations) **will not work**.

**Supported quantization formats:**

| Format | Description | Maxwell | Memory Usage |
|--------|-------------|---------|--------------|
| `q0f32` | Full FP32 | Yes | High |
| `q4f32_1` | 4-bit weights, FP32 activations | Yes | Medium |
| `q4f16_1` | 4-bit weights, FP16 activations | **No** | Low |

## Compatible Models

Models verified to work on Maxwell GPUs with 6GB VRAM:

| Model | HuggingFace ID | VRAM | Tokens/sec |
|-------|----------------|------|------------|
| Qwen3-0.6B | `mlc-ai/Qwen3-0.6B-q0f32-MLC` | ~5.1GB | ~60 |
| Qwen2.5-0.5B | `mlc-ai/Qwen2.5-0.5B-q0f32-MLC` | ~4.5GB | ~70 |
| TinyLlama-1.1B | `mlc-ai/TinyLlama-1.1B-q4f32_1-MLC` | ~2.5GB | ~45 |

**Finding models:** Search HuggingFace for `q0f32-MLC` or `q4f32-MLC`.

## FlexInfer Configuration

### ModelCache with Pre-compiled Library

Maxwell requires pre-compiled model libraries to avoid JIT compilation failures:

```yaml
apiVersion: ai.flexinfer/v1alpha1
kind: ModelCache
metadata:
  name: qwen3-maxwell
spec:
  source:
    type: huggingface
    repository: mlc-ai/Qwen3-0.6B-q0f32-MLC
  storageStrategy: SharedPVC
  storageClassName: longhorn
  storageSize: 5Gi
  # Pre-compile for Maxwell during cache provisioning
  precompile:
    enabled: true
    targetArch: sm_52
    compileOptions:
      useCutlass: false
      useFlashInfer: false
      useCublasGemm: true
```

### ModelDeployment

```yaml
apiVersion: ai.flexinfer/v1alpha1
kind: ModelDeployment
metadata:
  name: qwen3-maxwell
spec:
  backend: mlc-llm
  model: Qwen3-0.6B-q0f32-MLC
  modelCacheRef: qwen3-maxwell

  # Target Maxwell GPUs specifically
  nodeSelector:
    flexinfer.ai/gpu.arch: sm_52

  mlcllm:
    mode: local
    # Use pre-compiled library (avoids JIT compilation)
    modelLibPath: /models/maxwell-lib.so
    jitPolicy: "OFF"
    gpuMemoryBytes: 5000000000  # ~5GB for GTX 980 Ti
    compileOptions:
      useCutlass: false      # Requires FP16 - disabled
      useFlashInfer: false   # Requires sm_80+ - disabled
      useCublasGemm: true    # cuBLAS fallback
      useCudaGraph: false    # Disabled for stability
    overrides:
      maxTotalSeqLength: 2048  # Reduced for limited VRAM

  resources:
    limits:
      nvidia.com/gpu: 1
      memory: 8Gi
    requests:
      memory: 6Gi
```

## Complete Example

```yaml
# 1. Create ModelCache to download and pre-compile
apiVersion: ai.flexinfer/v1alpha1
kind: ModelCache
metadata:
  name: qwen3-0.6b-maxwell
  namespace: flexinfer-system
spec:
  source:
    type: huggingface
    repository: mlc-ai/Qwen3-0.6B-q0f32-MLC
  storageStrategy: SharedPVC
  storageClassName: longhorn
  storageSize: 5Gi
---
# 2. Create ModelDeployment targeting Maxwell nodes
apiVersion: ai.flexinfer/v1alpha1
kind: ModelDeployment
metadata:
  name: qwen3-maxwell
  namespace: flexinfer-system
spec:
  backend: mlc-llm
  model: Qwen3-0.6B-q0f32-MLC
  modelCacheRef: qwen3-0.6b-maxwell

  nodeSelector:
    flexinfer.ai/gpu.arch: sm_52

  mlcllm:
    mode: local
    jitPolicy: "OFF"
    gpuMemoryBytes: 5000000000
    compileOptions:
      useCutlass: false
      useFlashInfer: false
      useCublasGemm: true
    overrides:
      maxTotalSeqLength: 2048
      prefillChunkSize: 256

  resources:
    limits:
      nvidia.com/gpu: 1
      memory: 8Gi

  minReplicas: 0
  replicas: 1
  idleTimeoutSeconds: 600
  coldStartTimeoutSeconds: 300
```

## Node Detection

The FlexInfer agent automatically detects Maxwell GPUs and applies labels:

```bash
# Labels set by flexinfer-agent
flexinfer.ai/gpu.vendor: NVIDIA
flexinfer.ai/gpu.arch: sm_52
flexinfer.ai/gpu.vram: 6Gi
flexinfer.ai/gpu.int4: false
```

## Performance Expectations

Maxwell GPUs are significantly slower than modern GPUs. Expect:
- 3-5x slower inference compared to RTX 30/40 series
- ~60 tokens/sec for 0.6B models
- ~45 tokens/sec for 1B models

For better performance, consider upgrading to Pascal (10-series) or newer GPUs.

## Troubleshooting

### "identifier __half is undefined"

**Cause:** Using FP16 model on Maxwell GPU.

**Solution:** Use FP32 quantization (`q0f32` or `q4f32_1`).

### Out of memory on 6GB GPU

**Solutions:**
1. Use smaller model (Qwen3-0.6B fits in ~5GB)
2. Reduce `maxTotalSeqLength` in overrides
3. Set `gpuMemoryBytes` to leave headroom

### Compilation takes forever

**Cause:** JIT compilation on Maxwell is slow.

**Solution:** Pre-compile models and set `jitPolicy: "OFF"`.

## References

- [Full Maxwell Setup Guide](/build/README-maxwell.md)
- [MLC-AI Models on HuggingFace](https://huggingface.co/mlc-ai)
- [MLC-LLM Documentation](https://llm.mlc.ai/docs/)
