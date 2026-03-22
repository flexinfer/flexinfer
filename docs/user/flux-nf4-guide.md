---
title: FLUX.1 NF4 Quantization Guide
description: Deploying FLUX.1 image generation models on AMD ROCm GPUs with NF4 quantization
---

# FLUX.1 NF4 Quantization Guide

## Overview

FLUX.1 models from Black Forest Labs are 12-billion-parameter image generation models. At FP16 precision, the transformer alone consumes ~24 GB of VRAM, and the T5-XXL text encoder adds another ~9 GB. That 33+ GB total exceeds the 24 GB available on an RX 7900 XTX.

NF4 (4-bit NormalFloat) quantization via bitsandbytes compresses the transformer from ~24 GB to ~6 GB while preserving image quality. Combined with the unquantized T5-XXL encoder, the full pipeline fits comfortably in 24 GB VRAM on gfx1100 -- or in 16 GB VRAM on gfx906 with CPU offload enabled.

FlexInfer handles NF4 loading, dtype configuration, and ROCm environment setup automatically. You enable it with a single config key: `quantization: "nf4"`.

## Prerequisites

### Hardware

| GPU | Architecture | VRAM | NF4 Support | CPU Offload Required |
|-----|-------------|------|-------------|---------------------|
| RX 7900 XTX | gfx1100 | 24 GB | Yes | No |
| RX 7900 XT | gfx1100 | 20 GB | Yes | No |
| RX 7900 GRE | gfx1100 | 16 GB | Yes | Recommended |
| Radeon VII | gfx906 | 16 GB | Yes (source build) | Yes |

### Software

- **ROCm 6.4+** host driver on the GPU node
- **FlexInfer controller** with diffusers backend support
- **Diffusers runtime image** (`rocm-gfx1100` or `rocm-gfx906` tag)
- **bitsandbytes >= 0.49.2** (bundled in the FlexInfer diffusers image)

The diffusers runtime image ships with the correct bitsandbytes version pre-installed. You do not need to install anything manually.

### bitsandbytes Version Requirements

bitsandbytes `0.49.2` fixes two critical bugs on ROCm:

1. **Incorrect blocksize default** -- earlier versions use blocksize 128 instead of 64, producing incorrect dequantization results.
2. **Indexing overflow** (PR #1796) -- causes silent data corruption on large tensors.

The FlexInfer diffusers image pins `bitsandbytes >= 0.49.2`. If you build a custom image, install it with `--no-deps` to prevent pip from replacing the ROCm PyTorch with a CUDA build:

```bash
pip install 'bitsandbytes>=0.49.2' --no-deps
```

## Quick Start

Deploy FLUX.1 Schnell (text-to-image) on a gfx1100 GPU with NF4 quantization:

```yaml
apiVersion: ai.flexinfer/v1alpha2
kind: Model
metadata:
  name: flux-schnell-nf4
  namespace: flexinfer-system
spec:
  backend: diffusers
  source: HF://black-forest-labs/FLUX.1-schnell

  gpu:
    vendor: amd
    count: 1
    vramEstimateMB: 16000

  serverless:
    enabled: true
    minReplicas: 0
    idleTimeout: 10m
    coldStartTimeout: 15m

  config:
    modelFamily: "flux"
    quantization: "nf4"
    computeDtype: "bfloat16"
    guidanceScale: "0.0"
    numInferenceSteps: "4"
    warmupResolutions: "512x512,1024x1024"
    compilationCache: "true"

  nodeSelector:
    flexinfer.ai/gpu.arch: gfx1100

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

Apply the manifest:

```bash
kubectl apply -f flux-schnell-nf4.yaml
```

Verify the model reaches Ready status:

```bash
kubectl get model flux-schnell-nf4 -n flexinfer-system
```

Expected output:

```
NAME               BACKEND     PHASE   TPS   AGE
flux-schnell-nf4   diffusers   Ready         2m
```

## Configuration Reference

### Config Keys

These keys go in `spec.config` on the Model CR. The controller translates them to container environment variables.

| Config Key | Env Var | Values | Default | Description |
|-----------|---------|--------|---------|-------------|
| `quantization` | `QUANTIZATION` | `nf4` | (none) | Enable NF4 quantization via bitsandbytes |
| `computeDtype` | `BNB_COMPUTE_DTYPE` | `bfloat16`, `float32` | `bfloat16` | Compute dtype for NF4 dequantization |
| `cpuOffload` | `USE_CPU_OFFLOAD` | `true`, `false` | `false` | Move pipeline stages to GPU one at a time |
| `modelFamily` | `MODEL_FAMILY` | `flux`, `sdxl`, `sd3`, `sd15` | (auto-detected) | Pipeline family selection |
| `pipelineMode` | `PIPELINE_MODE` | `inpainting`, `instruct` | (none) | Override default pipeline type |
| `guidanceScale` | `DEFAULT_GUIDANCE_SCALE` | float | `7.5` | Classifier-free guidance strength |
| `numInferenceSteps` | `DEFAULT_NUM_INFERENCE_STEPS` | int | `25` | Number of denoising steps |
| `warmupResolutions` | `WARMUP_RESOLUTIONS` | comma-separated | (none) | Pre-compile MIOpen kernels at startup |
| `compilationCache` | (compilation cache volume) | `true`, `false` | `false` | Persist MIOpen/Triton kernel cache across restarts |
| `hipVisibleDevices` | `HIP_VISIBLE_DEVICES` | device indices | (all) | Restrict visible GPU devices |

### GPU Spec Fields

| Field | Type | Description |
|-------|------|-------------|
| `gpu.vendor` | `amd` | Target AMD GPUs |
| `gpu.count` | int | Number of GPUs (typically 1 for FLUX) |
| `gpu.vramEstimateMB` | int | VRAM estimate for scheduling; use `16000` for NF4 without offload, `8000` with offload |
| `gpu.shared` | string | GPU sharing group name (optional) |
| `gpu.priority` | int | Priority within shared group (higher = more important) |

### Serverless Fields

| Field | Description | Recommended |
|-------|-------------|-------------|
| `serverless.idleTimeout` | Time before scale-to-zero | `10m` (NF4 reload takes ~2-5 min) |
| `serverless.coldStartTimeout` | Max wait for activation | `15m` (NF4 loading with warmup) |
| `serverless.minReplicas` | Minimum running replicas | `0` for serverless, `1` for always-on |

## FLUX.1 Variants

FLUX.1 ships two variants with different capabilities and licenses.

### FLUX.1 Schnell (Text-to-Image)

| Property | Value |
|----------|-------|
| Source | `HF://black-forest-labs/FLUX.1-schnell` |
| Pipeline | `FluxPipeline` |
| License | Apache 2.0 |
| Steps | 4 (distilled) |
| Guidance scale | `0.0` (no CFG needed) |
| Use case | Fast text-to-image generation |

Schnell is a 4-step distilled model optimized for speed. It uses `FluxPipeline` and does not support `negative_prompt` or `strength` parameters.

```yaml
config:
  modelFamily: "flux"
  quantization: "nf4"
  computeDtype: "bfloat16"
  guidanceScale: "0.0"
  numInferenceSteps: "4"
```

### FLUX.1 Fill Dev (Inpainting)

| Property | Value |
|----------|-------|
| Source | `HF://black-forest-labs/FLUX.1-Fill-dev` |
| Pipeline | `FluxFillPipeline` |
| License | Non-commercial |
| Steps | 20 |
| Guidance scale | `3.5` |
| Use case | Image inpainting and editing |

Fill Dev uses `FluxFillPipeline` with flow-matching. It requires an input image and mask. It does not support `negative_prompt` or `strength`. You must pass `height` and `width` explicitly.

There is no Schnell variant of Fill -- only the Dev version exists.

```yaml
config:
  modelFamily: "flux"
  pipelineMode: "inpainting"
  quantization: "nf4"
  computeDtype: "bfloat16"
  guidanceScale: "3.5"
  numInferenceSteps: "20"
```

### Pipeline Comparison

| Feature | FluxPipeline (Schnell) | FluxFillPipeline (Fill Dev) |
|---------|----------------------|---------------------------|
| `negative_prompt` | Not supported | Not supported |
| `strength` | Not supported | Not supported |
| `height`/`width` | Optional (defaults to 1024x1024) | Required |
| Input image | Not accepted | Required |
| Mask image | Not accepted | Required |
| `variant="fp16"` | Not supported (no FP16 variant files) | Not supported |

### Unified Mode (Fill as Both Text2Image and Inpainting)

Fill Dev can serve both text-to-image and inpainting through a single deployment. When a `/v1/images/generations` request arrives without an input image, the server auto-generates a blank image and full mask to produce text-to-image output via the fill path.

```yaml
apiVersion: ai.flexinfer/v1alpha2
kind: Model
metadata:
  name: flux-fill-unified
spec:
  backend: diffusers
  source: HF://black-forest-labs/FLUX.1-Fill-dev
  gpu:
    vendor: amd
    arch: gfx1100
  config:
    pipelineMode: inpainting
    quantization: nf4
    warmupResolutions: "512x512,1024x1024"
  serviceLabels:
    - imagegen
    - image-edit
    - text-to-image
  serverless:
    idleTimeout: 10m
```

Reference: [`examples/v1alpha2/flux-fill-unified.yaml`](/examples/v1alpha2/flux-fill-unified.yaml)

## Memory Footprint

### FP16 vs NF4 Component Breakdown

| Component | FP16 | NF4 + BF16 | Notes |
|-----------|------|-----------|-------|
| FLUX transformer | ~24 GB | ~6 GB | Quantized to 4-bit NormalFloat |
| T5-XXL text encoder | ~9.4 GB | ~9.4 GB | Not quantized (stays BF16) |
| CLIP text encoder | ~0.5 GB | ~0.5 GB | Not quantized |
| VAE | ~0.1 GB | ~0.1 GB | Not quantized |
| Runtime overhead | ~0.5 GB | ~0.5 GB | PyTorch allocator, kernels |
| **Total** | **~34.5 GB** | **~16.5 GB** | |

### GPU VRAM Budget by Hardware

| GPU | VRAM | NF4 Total | Headroom | CPU Offload |
|-----|------|-----------|----------|-------------|
| RX 7900 XTX | 24 GB | ~16.5 GB | ~7.5 GB | Not needed |
| RX 7900 XT | 20 GB | ~16.5 GB | ~3.5 GB | Not needed |
| RX 7900 GRE | 16 GB | ~16.5 GB | Tight | Recommended |
| Radeon VII | 16 GB | ~16.5 GB | Tight | Required |

With CPU offload enabled, only the active pipeline stage resides on GPU at any time. Peak GPU usage drops to ~6 GB (transformer) + ~0.1 GB (VAE) = ~6.1 GB, with the T5-XXL encoder staying on CPU.

## Dtype Strategy

The compute dtype controls how bitsandbytes dequantizes NF4 weights during inference. The `torch_dtype` of the pipeline **must** match `bnb_4bit_compute_dtype`. A mismatch produces the error:

```
RuntimeError: Input type and bias type should be the same
```

### bfloat16 (Default, Recommended)

The preferred path for gfx1100. The entire pipeline loads as `torch_dtype=bfloat16`. All non-quantized layers (layer norms, projections, embeddings, biases) use bfloat16, matching the BNB compute dtype. No post-load fixups needed.

```yaml
config:
  quantization: "nf4"
  computeDtype: "bfloat16"
```

Performance: ~123 TFLOPS on gfx1100 (roughly 2x faster than float32).

### float32 (Fallback)

Use this if bfloat16 produces visual artifacts on your hardware. The pipeline loads as `torch_dtype=float32`. The server downcasts text encoders to bfloat16 post-load and wraps `encode_prompt()` to cast embeddings. This path is slower but maximally safe.

```yaml
config:
  quantization: "nf4"
  computeDtype: "float32"
```

Performance: ~61 TFLOPS on gfx1100.

### Decision Guide

```
Is bfloat16 producing visual artifacts?
  No  --> Use bfloat16 (default, 2x faster)
  Yes --> Use float32 (safe fallback)
```

If `computeDtype` is omitted, the diffusers server defaults to `bfloat16`.

## Radeon VII (gfx906) Notes

Running FLUX NF4 on Radeon VII (gfx906, 16 GB HBM2) requires additional configuration beyond gfx1100.

### Source-Built bitsandbytes

The pip wheel for bitsandbytes only ships HIP kernels for gfx90a, gfx942, and gfx1100. Radeon VII (gfx906) kernels are **not** included. The FlexInfer gfx906 diffusers image builds bitsandbytes from source with `COMPUTE_BACKEND=hip` to include gfx906 kernels.

If you build a custom gfx906 image, you must build bitsandbytes from source:

```bash
git clone https://github.com/bitsandbytes-foundation/bitsandbytes.git
cd bitsandbytes
COMPUTE_BACKEND=hip pip install . --no-deps
```

### HSA Override

Radeon VII hardware reports as gfx900 instead of gfx906. The runtime override is required:

```bash
HSA_OVERRIDE_GFX_VERSION=9.0.6
```

The controller sets this automatically for nodes with `flexinfer.ai/gpu.arch: gfx906`.

### CPU Offload Required

With only 16 GB of VRAM, the full NF4 pipeline (~16.5 GB) does not fit without CPU offload. Enable it to keep only the active stage on GPU:

```yaml
config:
  cpuOffload: "true"
```

Peak GPU usage with offload: ~6.1 GB (transformer + VAE). The T5-XXL encoder stays on CPU, adding ~20-30% inference overhead per step.

### Additional Environment Variables

The controller auto-sets these for gfx906:

| Variable | Value | Purpose |
|----------|-------|---------|
| `HSA_OVERRIDE_GFX_VERSION` | `9.0.6` | Override gfx900 hardware ID |
| `HSA_ENABLE_SDMA` | `0` | Vega20 DMA engine stability |
| `HSA_USE_SVM` | `0` | Disable shared virtual memory |
| `PYTORCH_HIP_ALLOC_CONF` | `garbage_collection_threshold:0.8,max_split_size_mb:256` | Tighter memory management for 16 GB |

### Example: FLUX Fill on Radeon VII

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
    count: 1
    vramEstimateMB: 8000
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
    modelFamily: "flux"
    pipelineMode: "inpainting"
    cpuOffload: "true"
    quantization: "nf4"
    computeDtype: "bfloat16"
    guidanceScale: "3.5"
    numInferenceSteps: "20"
    compilationCache: "true"
    hipVisibleDevices: "0"
  nodeSelector:
    kubernetes.io/hostname: cblevins-radeonvii
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

Reference: [`deploy/models/flux-fill-inpainting.yaml`](/deploy/models/flux-fill-inpainting.yaml)

## Troubleshooting

### "Input type and bias type should be the same"

**Cause:** `torch_dtype` does not match `bnb_4bit_compute_dtype`. This happens when `computeDtype` is set to one value but the pipeline loads with a different dtype.

**Fix:** Set `computeDtype` explicitly in your Model CR config:

```yaml
config:
  quantization: "nf4"
  computeDtype: "bfloat16"
```

### FileNotFoundError for FP16 Variant

**Cause:** Passing `variant="fp16"` to the pipeline loader. FLUX model repositories do not contain FP16 variant subdirectories.

**Fix:** Do not set `useFp16: "1"` for FLUX models. NF4 quantization replaces the need for FP16 variants. The controller excludes the `variant` kwarg for FLUX automatically.

### Out of Memory (OOM) on 16 GB GPUs

**Cause:** The full NF4 pipeline (~16.5 GB) exceeds 16 GB VRAM.

**Fix:** Enable CPU offload:

```yaml
config:
  cpuOffload: "true"
```

This reduces peak VRAM to ~6.1 GB by loading pipeline stages sequentially.

### OOM After Multiple Consecutive Generations

**Cause:** PyTorch HIP memory allocator fragmentation. Allocated memory grows with each generation even though tensors are freed.

**Fix:** The controller auto-sets `PYTORCH_HIP_ALLOC_CONF` with `expandable_segments:True` and `garbage_collection_threshold:0.9`. If running outside FlexInfer, set this env var manually:

```bash
export PYTORCH_HIP_ALLOC_CONF="garbage_collection_threshold:0.9,max_split_size_mb:512,expandable_segments:True"
```

### Startup Takes 5-10 Minutes

**Cause:** NF4 quantization hooks and accelerate device placement are slow on first load. MIOpen kernel compilation adds time at configured warmup resolutions.

**Fix:** This is expected behavior. Set `coldStartTimeout: 15m` to prevent premature timeout. Enable `compilationCache: "true"` to persist MIOpen kernels across restarts -- subsequent startups will be faster.

### VAE Decode Crash (SIGSEGV) on RDNA3

**Cause:** ROCm issue #4729 -- MIOpen crashes during VAE decode at resolutions above 1024px.

**Fix:** The controller auto-sets `MIOPEN_FIND_MODE=2` (FAST mode). If crashes persist, try NORMAL mode:

```yaml
config:
  miopenFindMode: "1"
```

### bitsandbytes Installs CUDA PyTorch

**Cause:** Running `pip install bitsandbytes` without `--no-deps` resolves bitsandbytes' PyTorch dependency and replaces the ROCm build with CUDA.

**Fix:** Always use `--no-deps` when installing bitsandbytes on ROCm images:

```bash
pip install 'bitsandbytes>=0.49.2' --no-deps
```

The FlexInfer diffusers image handles this correctly. This issue only arises in custom image builds.

### "HIP error: invalid argument" on gfx906

**Cause:** ROCm 6.4 broke GPU memory allocation on gfx906 (Radeon VII). VMM is not supported on Vega20 hardware.

**Fix:** Use the FlexInfer gfx906 image, which is based on ROCm 6.2.3 (the last version with working gfx906 support). Do not use ROCm 6.4 images on Radeon VII.

## References

- [ROCm GFX1100 Backend Guide](backends-rocm-gfx1100.md) -- comprehensive ROCm backend reference
- [Quantization Guide](quantization.md) -- GPTQ, AWQ, and other quantization methods
- [GPU Sharing Guide](gpu-sharing.md) -- shared GPU group configuration
- [`examples/v1alpha2/flux-fill-unified.yaml`](/examples/v1alpha2/flux-fill-unified.yaml) -- unified text2image + inpainting example
- [`deploy/models/flux-fill-inpainting.yaml`](/deploy/models/flux-fill-inpainting.yaml) -- production Fill deployment
- [bitsandbytes ROCm Support](https://github.com/bitsandbytes-foundation/bitsandbytes) -- upstream documentation
- [FLUX.1 on HuggingFace](https://huggingface.co/black-forest-labs) -- model cards and usage
