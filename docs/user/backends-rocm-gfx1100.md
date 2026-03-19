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
export DEFAULT_VLLM_IMAGE_GFX1100="my-registry/vllm:custom-gfx1100"
```

## Image Generation with FLUX on ROCm

FlexInfer runs FLUX.1 models on ROCm GPUs using the `diffusers` backend with NF4 quantization via bitsandbytes. This section covers deployment on gfx1100 (24GB) and gfx906 (16GB).

### FLUX Model Variants

| Model | Pipeline | License | Steps | Use Case |
|-------|----------|---------|-------|----------|
| FLUX.1 Schnell | `FluxPipeline` | Apache 2.0 | 4 | Text-to-image (fast) |
| FLUX.1 Fill Dev | `FluxFillPipeline` | Non-commercial | 20 | Inpainting |

Key differences between pipelines:
- **FluxPipeline** (Schnell): text-to-image only, no `negative_prompt`, no `strength` parameter
- **FluxFillPipeline** (Fill): inpainting only, no `negative_prompt`, no `strength`, requires explicit `height`/`width` and mask image
- FLUX has **no FP16 variant files** — do not pass `variant="fp16"` to pipeline loaders

For the unified runtime manifests, set `modelFamily` explicitly instead of
trusting the repo name:

- `flux` for FLUX.1 and FluxFill
- `sdxl` for SDXL and SDXL-derived models like Gonzalomo/FluxPony or RealVisXL
- `sd3` for Stable Diffusion 3 / 3.5 families
- `sd15` for Stable Diffusion 1.5-derived pipelines like InstructPix2Pix

Use `warmupResolutions` to precompile the resolutions you actually serve.
Keep gfx906 warmups conservative (`512x512`) and enable `1024x1024` warmups on
gfx1100 when the model and VRAM budget can absorb it.

### NF4 Quantization and Memory

FLUX.1 models are 12B parameters. At FP16, the transformer alone uses ~24GB — too large for a single 24GB GPU when combined with the T5-XXL text encoder (~9GB).

NF4 quantization via bitsandbytes solves this:

| Component | FP16 | NF4/BF16 |
|-----------|------|----------|
| Transformer | ~24 GB | ~6 GB |
| T5-XXL text encoder | ~9.4 GB | ~9.4 GB (not quantized) |
| VAE + overhead | ~1 GB | ~1 GB |
| **Total** | **~34 GB** | **~16.4 GB** |

NF4 fits comfortably in 24GB VRAM (gfx1100) and in 16GB VRAM (gfx906) with CPU offload.

**Requirements:**
- `bitsandbytes >= 0.49.2` — earlier versions have incorrect blocksize (128 vs 64) on ROCm and an indexing overflow bug (PR #1796)
- bitsandbytes must be installed with `--no-deps` to prevent pip from replacing the ROCm PyTorch with a CUDA build

Enable NF4 in the Model CR:

```yaml
config:
  quantization: "nf4"
```

### Compute Dtype Strategy

The `computeDtype` config controls how bitsandbytes dequantizes NF4 weights during inference. The dtype of the pipeline (`torch_dtype`) must match `bnb_4bit_compute_dtype` — a mismatch causes `"Input type and bias type should be the same"`.

| Dtype | TFLOPS (gfx1100) | Speed | Stability |
|-------|-----------------|-------|-----------|
| `bfloat16` (recommended) | 123 | ~2x faster | Preferred |
| `float32` (fallback) | 61 | Baseline | Safe fallback |

Set via Model CR config:

```yaml
config:
  quantization: "nf4"
  computeDtype: "bfloat16"   # recommended, 2x faster on gfx1100
```

The controller passes this as `BNB_COMPUTE_DTYPE` to the container. The diffusers server loads the entire pipeline with `torch_dtype=torch.bfloat16`, ensuring all non-quantized layers (norms, projections, embeddings) match the compute dtype.

### CPU Offload

CPU offload moves pipeline components to GPU one at a time instead of loading everything at once. This reduces peak VRAM usage at the cost of ~20-30% slower inference.

| GPU | VRAM | CPU Offload | Rationale |
|-----|------|-------------|-----------|
| gfx1100 (24GB) | 24 GB | `false` | NF4 total ~16.4GB fits without offload |
| gfx906 (16GB) | 16 GB | `true` | Only transformer + VAE on GPU (~6GB), T5 stays on CPU |

```yaml
config:
  cpuOffload: "true"   # required for gfx906 (16GB VRAM)
```

### ROCm Environment Variables

The controller automatically sets these for AMD GPUs. Understanding them helps with troubleshooting.

| Variable | Value (gfx1100) | Purpose |
|----------|----------------|---------|
| `MIOPEN_FIND_MODE` | `2` (FAST) | Workaround for ROCm#4729 — VAE decode crash at >1024px. ~10-15% slower but stable. |
| `PYTORCH_HIP_ALLOC_CONF` | `garbage_collection_threshold:0.9,max_split_size_mb:512,expandable_segments:True` | Prevents memory fragmentation on consecutive generations |
| `TORCH_BLAS_PREFER_HIPBLASLT` | `0` | Disabled for diffusers stability (enabled by default for vLLM GEMM) |
| `WARMUP_RESOLUTIONS` | `512x512,1024x1024` | Pre-compiles MIOpen kernels at both resolutions, avoiding 14-16s penalty on first 1024px request |

Override `MIOPEN_FIND_MODE` if needed:

```yaml
config:
  miopenFindMode: "1"   # default=2 (FAST), 1=NORMAL (slower but more kernel options)
```

### GFX906 (Radeon VII) Differences

Running FLUX on gfx906 requires additional configuration:

- **bitsandbytes must be built from source** — the pip wheel only ships HIP kernels for gfx90a/gfx942/gfx1100, not gfx906
- `HSA_OVERRIDE_GFX_VERSION=9.0.6` is required (hardware reports as gfx900)
- `HSA_ENABLE_SDMA=0` and `HSA_USE_SVM=0` for Vega20 stability
- Memory allocation is tighter: `garbage_collection_threshold:0.8,max_split_size_mb:256`
- Attention slicing is auto-enabled (`ENABLE_ATTENTION_SLICING=1`)
- Warmup is limited to `512x512` only (1024x1024 risks OOM on 16GB)

The controller handles all of these automatically when the node has `flexinfer.ai/gpu.arch: gfx906`.

### Example Model CRs

**Text-to-image (Schnell on gfx1100, 24GB):**

```yaml
apiVersion: ai.flexinfer/v1alpha2
kind: Model
metadata:
  name: flux-schnell-imagegen
  namespace: flexinfer-system
spec:
  backend: diffusers
  source: HF://black-forest-labs/FLUX.1-schnell
  gpu:
    vendor: amd
    shared: gpu-imagegen        # share GPU with other image models
    priority: 200
    count: 1
    vramEstimateMB: 16000       # NF4 uses ~16GB
  serverless:
    enabled: true
    minReplicas: 1
    idleTimeout: 10m
    coldStartTimeout: 15m
  cache:
    strategy: Local
    hostPath: /mnt/nvme/flexinfer/models
  config:
    cpuOffload: "false"         # 24GB VRAM — no offload needed
    quantization: "nf4"
    computeDtype: "bfloat16"
    guidanceScale: "0.0"        # Schnell is distilled — no CFG needed
    numInferenceSteps: "4"      # 4-step distilled model
  nodeSelector:
    kubernetes.io/hostname: my-gfx1100-node
  resources:
    requests:
      cpu: "2"
      memory: 16Gi
    limits:
      cpu: "4"
      memory: 40Gi
  serviceLabels:
    - image-gen
    - text-to-image
```

**Inpainting (Fill on gfx906, 16GB):**

```yaml
apiVersion: ai.flexinfer/v1alpha2
kind: Model
metadata:
  name: flux-fill-inpainting
  namespace: flexinfer-system
spec:
  backend: diffusers
  source: HF://black-forest-labs/FLUX.1-Fill-dev
  gpu:
    vendor: amd
    shared: radeonvii-models
    priority: 200
    count: 1
    vramEstimateMB: 8000        # with cpuOffload, only ~6GB on GPU
  serverless:
    enabled: true
    minReplicas: 0
    idleTimeout: 30m
    coldStartTimeout: 15m
  cache:
    size: 30Gi
    storageClass: nvme-1r
    strategy: SharedPVC
  config:
    pipelineMode: "inpainting"  # uses FluxFillPipeline
    cpuOffload: "true"          # required for 16GB VRAM
    quantization: "nf4"
    computeDtype: "bfloat16"
    guidanceScale: "3.5"        # Fill uses flow-matching, needs low CFG
    numInferenceSteps: "20"     # Dev model, not distilled
  nodeSelector:
    kubernetes.io/hostname: my-gfx906-node
  resources:
    requests:
      cpu: "2"
      memory: 16Gi
    limits:
      cpu: "4"
      memory: 40Gi
  serviceLabels:
    - image-edit
    - flux-inpainting
```

### FLUX Troubleshooting

**"Input type and bias type should be the same"**

`torch_dtype` does not match `bnb_4bit_compute_dtype`. Set `computeDtype` in the Model CR config to match. Use `bfloat16` for best performance.

**VAE decode crash (SIGSEGV)**

ROCm issue #4729 causes crashes during VAE decode on RDNA3 at resolutions above 1024px. The controller auto-sets `MIOPEN_FIND_MODE=2` as a workaround. If you still see crashes, try `miopenFindMode: "1"` in the config.

**Memory fragmentation on consecutive generations**

Symptoms: OOM after several successful generations despite sufficient VRAM. The controller auto-sets `expandable_segments:True` in `PYTORCH_HIP_ALLOC_CONF` to prevent this. If running outside FlexInfer, set this env var manually.

**FLUX has no FP16 variant files**

Unlike Stable Diffusion, FLUX model repos do not contain `fp16/` subdirectories. Do not pass `variant="fp16"` to the pipeline — this causes a `FileNotFoundError`.

**Startup takes 8-10 minutes**

NF4 models with CPU offload need time for bitsandbytes quantization hooks and accelerate device placement. The controller sets a startup probe timeout of 900s (15 min). The `coldStartTimeout` in the Model CR should be at least 15m.

**bitsandbytes installs CUDA PyTorch**

Always install bitsandbytes with `--no-deps` on ROCm images. Without this flag, pip resolves bitsandbytes' torch dependency and replaces the ROCm build with a CUDA build, causing `"HIP error"` at runtime.

## References

- [Full ROCm Build Guide](/build/README-rocm.md)
- [MLC-AI Models on HuggingFace](https://huggingface.co/mlc-ai)
- [MLC-LLM Documentation](https://llm.mlc.ai/docs/)
- [ROCm Installation Guide](https://rocm.docs.amd.com/projects/install-on-linux/en/latest/)
- [vLLM ROCm Support](https://docs.vllm.ai/en/latest/getting_started/amd-installation.html)
