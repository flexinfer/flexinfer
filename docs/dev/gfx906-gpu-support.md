# AMD Radeon VII (gfx906) GPU Support in FlexInfer

> Last updated: 2026-03-20. Verified against ROCm 6.4.1, PyTorch 2.6, transformers 5.x.

## Hardware: Radeon VII / Vega20 / gfx906

- **Node**: `cblevins-radeonvii` (128 GB system RAM, 16 GB HBM2)
- **ISA**: gfx906 (reports as gfx900 at runtime)
- **Wave size**: 64
- **Key limitation**: No VMM (Virtual Memory Management) support

## ROCm Support Timeline

| Milestone | Date | ROCm Version | Detail |
|-----------|------|-------------|--------|
| Full support | Through Q3 2023 | ROCm 5.7 | Last version with full gfx906 support |
| Maintenance mode | Q3 2023 | ROCm 5.7+ | No new features or optimizations |
| Bug fixes end | Q2 2024 | ROCm 6.0.x | End of maintenance |
| Effective removal | 2025+ | ROCm 6.4+ | Not in default build targets; Tensile libraries not shipped |

gfx906 is **deprecated but not removed** from ROCm. Code paths still exist, but AMD
does not test, optimize, or build pre-compiled libraries (rocBLAS Tensile, etc.) for it.

**Sources**:
- [ROCm/ROCm Discussion #3893](https://github.com/ROCm/ROCm/discussions/3893)
- [ROCm/ROCm Issue #2849](https://github.com/ROCm/ROCm/issues/2849)
- [ROCm Compatibility Matrix](https://rocm.docs.amd.com/en/latest/compatibility/compatibility-matrix.html)

## Known Hardware Limitations

### hipMemGetInfo / VMM Not Supported (UNFIXABLE)

`hipMemGetInfo()` returns `hipErrorInvalidValue` on gfx906 because the underlying VMM
API is not available on Vega20 hardware. This is a **hardware limitation**, not a
software bug. It cannot be fixed in any ROCm version.

```
Device 0: AMD Radeon Graphics, gfx906:sramecc+:xnack-, VMM: no
```

**Impact**: Any code that calls `torch.cuda.mem_get_info()`, including:
- `transformers` `caching_allocator_warmup` (added in v5.x)
- `accelerate` device map auto-detection
- vLLM memory profiling
- GPTQModel GPU memory estimation

**Workarounds by application**:

| Application | Workaround | Reference |
|-------------|-----------|-----------|
| llama.cpp | `patch-hipmemgetinfo.sh` — reads `/sys/class/drm/card0/device/mem_info_vram_total` | `build/patch-hipmemgetinfo.sh` |
| ONNX Runtime | Uses `rocm_smi` library (`rsmi_dev_memory_total_get`) | [PR #21190](https://github.com/microsoft/onnxruntime/pull/21190) |
| PyTorch/transformers | Monkey-patch `torch.cuda.mem_get_info` to return hardcoded values | `pkg/quantization/abliteration.go` wrapper script |
| FlexInfer abliteration | `device_map=cpu` (avoids GPU entirely) | Current approach for gfx906 |

**Sources**:
- [ROCm/ROCm Issue #1909](https://github.com/RadeonOpenCompute/ROCm/issues/1909)
- [PyTorch Issue #126015](https://github.com/pytorch/pytorch/issues/126015)
- [vLLM Issue #5994](https://github.com/vllm-project/vllm/issues/5994)

### torch.empty() on GPU Also Fails (ROCm 6.4)

On ROCm 6.4.1 with `HSA_OVERRIDE_GFX_VERSION=9.0.6`, even basic GPU memory allocation
via `torch.empty(..., device="cuda")` fails with `RuntimeError: HIP error: invalid argument`.
This means **GPU compute is completely non-functional** on gfx906 with ROCm 6.4.

This was confirmed on 2026-03-20 during abliteration testing. The `caching_allocator_warmup`
function in transformers 5.x calls `torch.empty()` on GPU after `mem_get_info`, and both fail.

### Required Environment Variables

These are set in the `gfx906` GPUProfile (`deploy/gpuprofiles/gfx906.yaml`):

```bash
HSA_OVERRIDE_GFX_VERSION=9.0.6   # Hardware reports gfx900, override to gfx906
HSA_ENABLE_SDMA=0                 # DMA stability on Vega20
HSA_USE_SVM=0                     # hipMemGetInfo workaround (ROCm/ROCm#2433)
PYTORCH_ROCM_ARCH=gfx906         # Build target
TORCH_BLAS_PREFER_HIPBLASLT=0    # Not available on GCN5
MIOPEN_FIND_MODE=2               # Reduce MIOpen overhead
```

## PyTorch + Transformers Compatibility Matrix

### The Core Constraint

transformers >= 5.0 **hard-requires PyTorch 2.4+**. This was a deliberate decision
([transformers #43508](https://github.com/huggingface/transformers/issues/43508)).
There is no workaround or compatibility shim.

### Available Combinations

| ROCm | PyTorch | transformers | gfx906 GPU works? | Source |
|------|---------|-------------|-------------------|--------|
| 5.7 | 2.0-2.1 | < 5.0 | Yes (full support) | Official |
| 6.1 | 2.4.1 | >= 5.0 | **Likely yes** (untested) | Official wheel: `torch==2.4.1+rocm6.1` |
| 6.2.3 | 2.3.0 | < 5.0 only | Yes (with HSA override) | `Dockerfile.quantizer-gptq-rocm-gfx906` |
| 6.4.1 | 2.6.0 | >= 5.0 | **No** (GPU alloc broken) | `Dockerfile.quantizer-gptq-rocm64-gfx906` |
| 6.3-7.2 | 2.7-2.8 | >= 5.0 | **Likely yes** (community) | mixa3607/ML-gfx906 |

### Current FlexInfer Images for gfx906

| Image | Base | PyTorch | transformers | GPU? | Use case |
|-------|------|---------|-------------|------|----------|
| `quantizer:gptq-rocm-gfx906` | ROCm 6.2.3 | 2.3 | < 5 | Yes | GPTQ quant (non-Qwen3.5 models) |
| `quantizer:gptq-rocm64-gfx906` | ROCm 6.4.1 | 2.6 | >= 5 | **CPU only** | Abliteration/quant for Qwen3.5+ |
| `runtime:rocm-gfx906` | ROCm 6.2.3 | 2.3 | N/A | Yes | vLLM inference |

## Upgrade Paths to Restore GPU on gfx906

### Option A: PyTorch 2.4.1 + ROCm 6.1 (Recommended)

```dockerfile
FROM rocm/pytorch:rocm6.1_ubuntu22.04_py3.10_pytorch_release_2.4.1
# OR install via pip:
# pip install torch==2.4.1 --index-url https://download.pytorch.org/whl/rocm6.1
```

- **Pro**: Official AMD wheel, gfx906 likely still in default build targets
- **Pro**: PyTorch 2.4 satisfies transformers>=5.0 requirement
- **Con**: ROCm 6.1 is older; fewer bug fixes
- **Con**: hipMemGetInfo still broken (need monkey-patch for `torch.cuda.mem_get_info`)
- **Status**: Untested. Validate `torch.empty(device="cuda")` works before adopting.

### Option B: mixa3607/ML-gfx906 Community Images

[github.com/mixa3607/ML-gfx906](https://github.com/mixa3607/ML-gfx906) — active
community project providing Docker images with PyTorch 2.7-2.11 on ROCm 6.3-7.2,
all built with gfx906 targets.

- **Pro**: Latest PyTorch + ROCm, actively maintained
- **Pro**: Includes vLLM 0.11-0.12, llama.cpp, ComfyUI images
- **Con**: Community builds, not AMD-supported
- **Con**: May need additional testing for production use
- **Status**: Not evaluated. Worth testing for GPU-accelerated abliteration/quantization.

### Option C: nlzy/vllm-gfx906 (Archived)

[github.com/nlzy/vllm-gfx906](https://github.com/nlzy/vllm-gfx906) — modified vLLM
for gfx906 with custom triton-gfx906. Reports 43-92 tok/s on MI50 (same gfx906 arch).
**Archived Feb 2026** — use as reference only.

## Current Workaround: CPU-Only on radeonvii

For workloads requiring transformers>=5.0 (e.g., Qwen3.5 abliteration), we run
CPU-only on radeonvii's 128GB RAM:

- `device_map=cpu` via `FLEXINFER_ABLITERATION_DEVICE_MAP=cpu`
- `useGPU: false` in ModelCache abliteration spec
- ~10s/sample for 27B forward passes (vs ~27s/sample GPU on gfx1100)
- 128 harmful + 128 harmless samples = ~45 min activation collection
- Total abliteration including save: ~55 min

This is acceptable for abliteration (one-time operation per model) but would be
too slow for inference or iterative quantization calibration.

## Inference on gfx906

For inference, gfx906 **works** with:
- **vLLM** on ROCm 6.2.3 (PyTorch 2.3) — the runtime image uses this
- **llama.cpp** with `patch-hipmemgetinfo.sh` for GGUF models
- **Ollama** with patched llama.cpp backend
- **Diffusers** (SDXL inpainting) with bitsandbytes built from source for gfx906

The GPU is functional for inference workloads that don't require transformers>=5.0
or `torch.cuda.mem_get_info()` during loading.

## Decision Log

| Date | Decision | Rationale |
|------|----------|-----------|
| 2026-03-20 | CPU-only abliteration on gfx906 | GPU alloc broken on ROCm 6.4; 128GB RAM makes CPU viable |
| 2026-03-20 | Use `gptq-rocm64-gfx906` image | Only image with transformers>=5.0 for Qwen3.5 support |
| 2026-03-08 | Keep `gptq-rocm-gfx906` (ROCm 6.2.3) | GPU works for non-Qwen3.5 GPTQ quantization |
| TBD | Evaluate ROCm 6.1 + PyTorch 2.4.1 | Could restore GPU for transformers>=5.0 workloads |
| TBD | Evaluate mixa3607/ML-gfx906 images | Community builds with latest PyTorch for gfx906 |
