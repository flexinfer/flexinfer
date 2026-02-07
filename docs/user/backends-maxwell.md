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

FlexInfer supports Maxwell via `ai.flexinfer/v1alpha2` (recommended). `v1alpha1` resources still work, but the docs below focus on v1alpha2 because it matches how the controller enforces compatibility.

### v1alpha2 `Model` (recommended)

```yaml
apiVersion: ai.flexinfer/v1alpha2
kind: Model
metadata:
  name: qwen3-0-6b-maxwell
  namespace: flexinfer-system
spec:
  backend: mlc-llm
  source: HF://mlc-ai/Qwen3-0.6B-q0f32-MLC
  gpu:
    vendor: nvidia
    vramEstimateMB: 5200
  cache:
    strategy: SharedPVC
    storageClass: longhorn
    size: 6Gi
  # Maxwell requires a pre-compiled model library (no FP16).
  # If you compile to /models/<modelName>/maxwell-lib.so, you can omit modelLibPath.
  config:
    jitPolicy: READONLY
    # modelLibPath: /models/qwen3-0-6b-maxwell/maxwell-lib.so
```

#### Compile `maxwell-lib.so` into the cache PVC

FlexInfer will prefetch the HuggingFace repo into the cache PVC under `/models/<modelName>/`.
For the example above, that directory is `/models/qwen3-0-6b-maxwell/`.

Run a one-time compile Job on the Maxwell node (GTX 980 Ti, `sm_52`) to generate `maxwell-lib.so`:

```yaml
apiVersion: batch/v1
kind: Job
metadata:
  name: qwen3-0-6b-maxwell-compile
  namespace: flexinfer-system
spec:
  backoffLimit: 1
  template:
    spec:
      restartPolicy: Never
      runtimeClassName: nvidia
      nodeSelector:
        flexinfer.ai/gpu.arch: sm_52
      containers:
        - name: compile
          image: registry.harbor.lan/flexinfer/mlc-llm:cuda-maxwell-v7
          command: ["bash", "-lc"]
          args:
            - |
              set -euo pipefail
              model_dir="/models/qwen3-0-6b-maxwell"
              out="${model_dir}/maxwell-lib.so"
              python3 -m mlc_llm compile \
                "${model_dir}" \
                --device cuda:0 \
                --opt "cutlass=0;cublas_gemm=1;cudagraph=0;flashinfer=0" \
                --output "${out}"
              ls -lh "${out}"
          volumeMounts:
            - name: models
              mountPath: /models
      volumes:
        - name: models
          persistentVolumeClaim:
            claimName: qwen3-0-6b-maxwell-cache
```

If you used a different `metadata.name`, update `model_dir` and the PVC name (`<modelName>-cache` by default).

After `maxwell-lib.so` exists, the controller will start the deployment (MLC defaults to `jitPolicy=READONLY` on Maxwell).

### v1alpha1 (legacy) `ModelDeployment`

```yaml
apiVersion: ai.flexinfer/v1alpha1
kind: ModelDeployment
metadata:
  name: qwen3-maxwell
spec:
  backend: mlc-llm
  model: Qwen3-0.6B-q0f32-MLC
  modelCacheRef: qwen3-0.6b-maxwell

  # Target Maxwell GPUs specifically
  nodeSelector:
    flexinfer.ai/gpu.arch: sm_52

  mlcllm:
    mode: local
    # Use pre-compiled library (avoids JIT compilation)
    modelLibPath: /models/maxwell-lib.so
    jitPolicy: "READONLY"
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

### Model shows `ValidationFailed` / "requires config.modelLibPath"

**Cause:** For v1alpha2, FlexInfer blocks Maxwell deployments unless it can find a pre-compiled library.

**Fix options:**
1. Compile `maxwell-lib.so` into `/models/<modelName>/maxwell-lib.so` (recommended), or
2. Set `spec.config.modelLibPath` to the full path of your pre-compiled library.

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
