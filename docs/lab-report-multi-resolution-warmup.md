# Lab Report: Eliminating First-Request Latency on AMD GPUs with Multi-Resolution Kernel Warmup

**Date:** 2026-02-27
**Git SHA:** `1c83f01`
**Hardware:** AMD Radeon RX 7900 XTX (gfx1100, 24GB VRAM)
**Software:** ROCm 6.2, PyTorch 2.3.0, HIP 6.2.41134
**Model:** RealVisXL V5.0 (SDXL, fp16, DPM++ SDE Karras, 40 steps)

---

## The Problem: A Hidden 14-Second Tax on Every Cold Resolution

When you run Stable Diffusion on an NVIDIA GPU, the first inference at any resolution is slightly slower than subsequent ones. On AMD GPUs with ROCm, it's dramatically worse.

AMD's GPU compute stack uses MIOpen (the ROCm equivalent of cuDNN) for convolution and attention operations. Unlike cuDNN, which selects kernels from a pre-compiled library, MIOpen compiles optimized kernels *at runtime* for the specific tensor shapes it encounters. These shapes are determined by the image resolution — a 512x512 image produces different intermediate tensor dimensions than a 1024x1024 image.

The practical consequence: the first time your diffusion pipeline runs at a new resolution, MIOpen triggers a full kernel search-and-compile cycle. On a 7900 XTX running SDXL, this adds **14-16 seconds** of pure compilation overhead on top of the actual inference time.

Our FlexInfer inference platform already performed a warmup pass at startup — running two dummy inference steps at 512x512 to pre-compile the kernel set. But since production requests typically arrive at 1024x1024 (the SDXL native resolution), the first real request would still hit the compilation wall:

```
Container startup timeline (before):

  0s   ─── Model load (CPU → GPU transfer) ───────── ~5s
  5s   ─── Warmup 512x512 (2 steps, compiles kernels) ─ ~2s
  7s   ─── Ready, serving requests ─────────────────────
         ↓
  First request arrives: "generate at 1024x1024"
         ↓
  7s   ─── MIOpen recompiles kernels for 1024x1024 ── ~14s
  21s  ─── Actual inference (40 steps) ──────────────── ~18s
  39s  ─── Response returned ───────────────────────────

  Total first-request wall clock: ~32s
  Subsequent 1024x1024 requests: ~18s
```

Users sending the first 1024x1024 request after a container restart experienced nearly double the latency of steady-state. In a shared GPU environment where containers restart after model swaps, this happened frequently.

## The Investigation

### Why MIOpen Recompiles Per Resolution

MIOpen's kernel selection is fundamentally different from cuDNN's approach. Where NVIDIA ships pre-compiled kernels for common shapes, MIOpen runs a benchmark-driven search:

1. **Shape discovery** — When a convolution encounters new tensor dimensions, MIOpen identifies it as an uncached shape.
2. **Kernel search** — MIOpen tries multiple algorithm variants (direct, Winograd, FFT, implicit GEMM) and benchmarks each.
3. **Compilation** — The winning algorithm is compiled to GPU ISA (GCN/RDNA machine code).
4. **Caching** — The compiled kernel is stored in `~/.config/miopen/` (or a custom path via `MIOPEN_CUSTOM_CACHE_DIR`).

We set `MIOPEN_FIND_MODE=2` (which limits the search to a fast heuristic rather than exhaustive benchmarking — necessary to avoid driver timeouts on gfx1100), but even the fast path takes significant time for the ~50 unique convolution shapes in an SDXL UNet.

The Triton compiler (used by PyTorch's `scaled_dot_product_attention`) has a similar pattern: it JIT-compiles attention kernels for each unique (batch, heads, seq_len) combination, and seq_len changes with resolution.

### Measuring the Penalty

We measured the first-request penalty by deploying the container and sending sequential requests:

| Request | Resolution | Latency | Notes |
|---------|-----------|---------|-------|
| Warmup (startup) | 512x512 | 2s | 2 inference steps |
| 1st real request | 512x512 | ~8s | Kernels already compiled |
| 1st real request | 1024x1024 | ~32s | **14s recompile + 18s inference** |
| 2nd real request | 1024x1024 | ~18s | Kernels cached |

The 14-second penalty is entirely MIOpen/Triton compilation — no model loading, no memory allocation, just kernel codegen.

## The Solution: Multi-Resolution Warmup

The fix is conceptually simple: if the kernels are resolution-specific, warm up at every resolution you expect to serve.

### Design Decisions

**1. New `WARMUP_RESOLUTIONS` environment variable**

Rather than hardcoding resolutions, we added a comma-separated env var that accepts arbitrary `WxH` pairs:

```
WARMUP_RESOLUTIONS=512x512,1024x1024
```

This keeps the container image generic — the same image works for any resolution set, controlled at deployment time.

**2. Three-tier priority for configuration**

```
Priority 1: Explicit warmupResolutions in the Model CRD config
Priority 2: Legacy warmupWidth/warmupHeight (backward compatible)
Priority 3: GPU-arch auto-default (no config required)
```

The auto-default is where it gets interesting. Our Go controller inspects the GPU architecture at deployment time:

```go
case strings.HasPrefix(spec.GPUArch, "gfx110"):
    // gfx1100 (24GB VRAM): warm up both 512 and 1024
    env = append(env, corev1.EnvVar{
        Name:  "WARMUP_RESOLUTIONS",
        Value: "512x512,1024x1024",
    })
case strings.HasPrefix(spec.GPUArch, "gfx906"):
    // gfx906 (16GB VRAM): 1024x1024 risks OOM, stick with 512
    env = append(env, corev1.EnvVar{
        Name:  "WARMUP_RESOLUTIONS",
        Value: "512x512",
    })
```

A Radeon VII (gfx906, 16GB) can't safely warm up at 1024x1024 — SDXL at that resolution pushes close to the VRAM limit, and running warmup plus inference overhead could trigger OOM before the first real request arrives. The auto-default respects this constraint.

NVIDIA GPUs get no auto-default because cuDNN doesn't have this problem.

**3. VRAM cleanup between resolutions**

Each warmup pass at a new resolution allocates different intermediate buffers. Without cleanup, the second warmup could fail if the first resolution's buffers haven't been freed:

```python
for w, h in resolutions:
    # ... run 2-step inference ...
    if torch.cuda.is_available():
        torch.cuda.empty_cache()  # Free VRAM between resolutions
```

`torch.cuda.empty_cache()` returns unused cached memory to the ROCm allocator so the next resolution starts with a clean slate.

### Implementation

Three files changed:

**`backend/diffusers.go`** (Go controller) — The env var injection logic with the three-tier priority and GPU-arch auto-defaults.

**`build/Dockerfile.diffusers-rocm`** (Python server) — New `_parse_warmup_resolutions()` function that parses the env var, and a rewritten `warmup_inference()` that loops over resolutions with per-resolution timing and VRAM cleanup.

**`backend/diffusers_test.go`** — Six test cases covering explicit config, legacy fallback, gfx1100 auto-default, gfx906 auto-default, NVIDIA no-op, and precedence rules.

## Results

### Startup Warmup Output

After deploying the updated controller and container image, the startup logs show the multi-resolution warmup in action:

```
Running warmup inference at 2 resolution(s): 512x512, 1024x1024
  Warmup 512x512 complete in 13.6s
  Warmup 1024x1024 complete in 5.5s
All warmup passes complete
```

Two things to note:
- The 512x512 warmup takes 13.6s (longer than expected for 2 steps because this is the *first* inference after model load — MIOpen is compiling the base kernel set).
- The 1024x1024 warmup takes only 5.5s. Most MIOpen kernels from the 512x512 pass are reusable; only the resolution-specific shapes need recompilation.

Total startup warmup cost: **19.1 seconds** (vs 2s before). This is a one-time cost at container startup.

### First-Request Latency

After warmup, we sent three consecutive 1024x1024 requests:

| Request | Latency | Delta from min |
|---------|---------|----------------|
| 1st 1024x1024 | 18.18s | +0.58s |
| 2nd 1024x1024 | 17.78s | +0.18s |
| 3rd 1024x1024 | 17.61s | baseline |

**The first-request penalty is gone.** All three requests are within 0.6s of each other — well within normal inference variance (scheduler randomness, GPU clock fluctuation).

### Before/After Comparison

| Metric | Before | After | Change |
|--------|--------|-------|--------|
| Startup warmup duration | ~2s | ~19s | +17s (one-time) |
| First 1024x1024 request | ~32s | **18.2s** | **-14s (-44%)** |
| Second 1024x1024 request | ~18s | 17.8s | Same |
| First-request penalty | ~14s | **<1s** | **Eliminated** |
| Subsequent restarts (with cache) | ~2s warmup | ~2s warmup | Same (cached) |

The 17-second warmup increase is paid once per container start. With a persistent compilation cache (MIOpen cache mounted on a PVC), subsequent restarts skip recompilation entirely — both warmup passes complete in ~2s total because the compiled kernels are loaded from disk.

### 512x512 Warm Inference (Baseline)

The standard bench script confirmed 512x512 performance is unaffected:

```
Warm inference: min=7.70s avg=8.97s max=11.43s
```

This is the steady-state for RealVisXL V5.0 with 40 steps of DPM++ SDE Karras at 512x512 on a 7900 XTX — roughly half the 1024x1024 time, as expected.

## What Makes This Interesting

### 1. It's an AMD-specific problem with an AMD-specific solution

This entire class of issue doesn't exist on NVIDIA hardware. cuDNN ships pre-compiled kernels for common shapes, and its runtime search (when `CUDNN_BENCHMARK=True`) is an optional optimization, not a mandatory compilation step. If you're only targeting NVIDIA, you'd never encounter this.

But AMD GPUs are increasingly attractive for inference: the 7900 XTX offers 24GB of VRAM at a fraction of the cost of an A100 or even a 4090. Making them production-viable means solving problems like this that the CUDA ecosystem has already papered over.

### 2. The controller infers the right behavior from hardware

The Go controller inspects GPU architecture labels at deployment time and auto-configures warmup resolutions without any user intervention. A Model CR with no warmup config at all will get the right behavior:

- **gfx1100** (7900 XTX, 24GB): auto-warms at 512 + 1024
- **gfx906** (Radeon VII, 16GB): auto-warms at 512 only (OOM-safe)
- **NVIDIA**: no change (not needed)

This is a pattern we use throughout FlexInfer — the system adapts to the hardware rather than requiring users to know the quirks of each GPU architecture.

### 3. The cost is amortized to near-zero with persistent caching

FlexInfer mounts a compilation cache PVC (`MIOPEN_CUSTOM_CACHE_DIR`, `TRITON_CACHE_DIR`) on each diffusers pod. The first container start pays the ~19s warmup. Every subsequent restart loads the compiled kernels from the PVC and the warmup passes complete in ~2s (just running the inference with pre-loaded kernels, no compilation).

The cache PVC survives pod restarts, node reboots, and model swaps. The only time you pay the full compilation cost is on first deployment or after a PyTorch/ROCm version upgrade that invalidates the cache.

### 4. Per-resolution timing enables observability

The warmup function logs timing per resolution:

```
Warmup 512x512 complete in 13.6s
Warmup 1024x1024 complete in 5.5s
```

This makes it possible to detect compilation cache misses (high warmup time = cold cache), monitor the impact of ROCm upgrades on kernel compilation, and compare performance across GPU architectures — all from container logs.

## Configuration Reference

### Environment Variables (Container)

| Variable | Default | Description |
|----------|---------|-------------|
| `WARMUP_RESOLUTIONS` | — | Comma-separated `WxH` list (e.g., `512x512,1024x1024`) |
| `WARMUP_WIDTH` | `512` | Legacy: single warmup width |
| `WARMUP_HEIGHT` | `512` | Legacy: single warmup height |
| `SKIP_WARMUP` | `0` | Set to `1` to skip warmup entirely |

### Model CRD Config

```yaml
apiVersion: flexinfer.ai/v1alpha2
kind: Model
metadata:
  name: sdxl-turbo-imagegen
spec:
  backend: diffusers
  source: "HF://SG161222/RealVisXL_V5.0"
  config:
    warmupResolutions: "512x512,1024x1024"
    # Or legacy (single resolution):
    # warmupWidth: "512"
    # warmupHeight: "512"
```

When neither `warmupResolutions` nor `warmupWidth` is set, the controller auto-selects based on GPU architecture.

## Reproduction

```bash
# Run tests
go test ./backend/... -run TestDiffusersWarmupResolutionsEnv -v

# Build and push the diffusers image
docker --context 7900xtx build -f build/Dockerfile.diffusers-rocm-gfx1100 \
  -t registry.harbor.lan/flexinfer/diffusers:rocm-gfx1100 .
docker --context 7900xtx push registry.harbor.lan/flexinfer/diffusers:rocm-gfx1100

# Build and push the controller
docker --context 7900xtx build -f build/Dockerfile.manager \
  -t registry.harbor.lan/flexinfer/flexinfer-controller:latest .
docker --context 7900xtx push registry.harbor.lan/flexinfer/flexinfer-controller:latest

# Benchmark first-request latency
./scripts/bench-image-swap.sh warm
```
