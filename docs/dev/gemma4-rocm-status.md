---
title: Gemma4 ROCm Status
description: "Living status for Gemma 4 E4B on unified gfx1100 runtimes."
---

# Gemma4 ROCm Status

This document tracks the current managed state of `google/gemma-4-E4B-it` on
the unified `gfx1100` runtime path, including the active profiles, measured
performance, and the feature gaps still being chased.

Update this document whenever a tuning change lands or a new blocker is found.

## Current profiles

| Model ID | Model CR | Node | Attention / KV path | Intent |
|----------|----------|------|---------------------|--------|
| `gemma4-e4b` | `gemma4-e4b-turboquant` | `cblevins-7900xtx` | `TRITON_ATTN` + float16 KV | Default alias |
| `gemma4-e4b-fast` | `gemma4-e4b-turboquant` | `cblevins-7900xtx` | `TRITON_ATTN` + float16 KV | Lower-latency textgen |
| `gemma4-e4b-long` | `gemma4-e4b-turboquant-canary` | `cblevins-5930k` | `CUSTOM` + `kvCacheCodec=turboquant` | Long-context profile |

## Current profile knobs

| Model ID | `maxModelLen` | `maxNumBatchedTokens` | `gpuMemoryUtilization` | Serverless |
|----------|---------------|-----------------------|------------------------|------------|
| `gemma4-e4b` / `gemma4-e4b-fast` | `16384` | `512` | `0.92` | `minReplicas: 1` |
| `gemma4-e4b-long` | `32768` | `160` | `0.80` | `minReplicas: 0` |

## Latest baseline

Source:

- `scripts/bench-gemma4-profiles.sh`
- `scripts/bench-gemma4-suite.sh` for broader warm/cold and phase-matrix runs
- run id: `gemma4-20260404T091910-e32744`

Environment:

- LiteLLM via `kubectl -n ai port-forward svc/litellm 18000:8000`
- `WARMUP=1`
- `SHORT_REPEAT=2000`
- `LONG_REPEAT=6000`
- `MAX_TOKENS=64`

Results:

| Model ID | Prompt tokens | Elapsed | Prompt tok/s | Completion tok/s | Notes |
|----------|---------------|---------|--------------|------------------|-------|
| `gemma4-e4b` | `73` | `0.880s` | `82.96` | `57.96` | Default alias health check |
| `gemma4-e4b-fast` | `10035` | `5.317s` | `1887.31` | `12.04` | Stable fast path |
| `gemma4-e4b-long` | `10035` | `25.802s` | `388.92` | `2.48` | Warmed short-context TurboQuant |
| `gemma4-e4b-long` | `30035` | `112.088s` | `267.96` | `0.57` | Warmed long-context TurboQuant |

Interpretation:

- The fast profile is in a good place for interactive use on the 7900 XTX.
- The long profile is functional at ~30k prompt tokens, but its prefill path is
  still the main performance bottleneck.
- Raising `gemma4-e4b-long` from `maxNumBatchedTokens=128` to `160` improved the
  warmed long-context leg from roughly `223` to `268` prompt tok/s and the
  warmed 10k leg from roughly `340` to `389` prompt tok/s.
- Cold-start noise and prefix-cache artifacts must be excluded from measurements
  or the fast profile numbers will be misleading.

## Features working

| Feature | Status | Notes |
|---------|--------|-------|
| Unified `gfx1100` runtime path | Working | No separate debug runtime required |
| Managed Gemma 4 CRD deployment | Working | Both fast and long lanes reconcile through Flux |
| LiteLLM aliases | Working | Public IDs are now trimmed to `gemma4-e4b`, `gemma4-e4b-fast`, `gemma4-e4b-long` |
| Tool calling | Working | Gemma parser path is enabled on both profiles |
| Warm compile-cache reuse | Working | Managed warm restart path loads compiled graphs from cache |
| TurboQuant managed canary | Working | `kvCacheCodec: turboquant` resolves on the long lane |
| ~30k prompt-token requests | Working | Served successfully on `gemma4-e4b-long` |

## Features still being chased

| Feature | Status | Current read |
|---------|--------|--------------|
| TurboQuant long-context prefill speed | In progress | Functional, but still substantially slower than the fast lane |
| Better long-lane batching | In progress | `maxNumBatchedTokens=128` is stable; higher values need measured retest |
| Fast-lane batching ceiling | In progress | `512` is safe; larger values have not been revalidated on the stable lane yet |
| AITER on ROCm | Blocked / deferred | `TRITON_ATTN` remains the stable path on RDNA3 |
| Production-grade TurboQuant as default | Deferred | Keep on separate long profile until perf and correctness are more mature |
| Speculative decoding | Not started | No Gemma4 speculator path wired yet |
| FP8-centric KV path | Not applicable on current lane | Current managed profiles use float16 KV |

## Gemma4 GPTQ Pipeline Models

### 26B-A4B MoE (GPTQ INT4)

| Field | Value |
|-------|-------|
| ModelCache | `gemma4-26b-a4b-gptq` |
| Model CR | `gemma4-26b-a4b-gptq` |
| Source | `google/gemma-4-26B-A4B-it` |
| Node | `cblevins-7900xtx` (gfx1100) |
| Pipeline | Download BF16 (~27 GB) → Abliterate → GPTQ INT4 (~7-13 GB) |
| PVC | 96 Gi (nvme-1r-gpu) |
| Shared Group | `7900xtx-textgen` (priority 200, always-on) |
| Aliases | `gemma4-26b`, `gemma4-26b-a4b`, `gemma4-moe` |

**MoE Architecture**: 25.2B total / 3.8B active, 128 experts top-8, 30 layers (25 GDN + 5 full-attention). Full MoE GPTQ quantization produces compact INT4 output that fits 24 GB VRAM with room for 32K context.

**Abliteration safety**: Only `o_proj` (shared attention output). Expert FFN weights auto-skipped. `ablitateLmHead: false` (save corruption bug).

**Quantization config**: `sym=true`, `descAct=false`, `maxSamples=512` (MoE expert coverage), `timeoutSeconds=43200` (12h for 640 expert modules).

### 31B Dense (GPTQ INT4)

| Field | Value |
|-------|-------|
| ModelCache | `gemma4-31b-gptq` |
| Source | `google/gemma-4-31B-it` |
| Node | `cblevins-radeonvii` (gfx906, 128 GB RAM) |
| Pipeline | Download BF16 (~61 GB) → Abliterate → GPTQ INT4 (~16 GB) |
| PVC | 120 Gi (nvme-1r-gpu) |
| Status | In progress (abliteration complete, quantization pending) |

**Dense Architecture**: 30.7B params, 60 layers (50 GDN + 10 full-attention). Requires 128 GB RAM node for abliteration + save overhead.

**Abliteration**: Both `o_proj` and `down_proj` (safe for dense models, no MoE experts). `maxMemoryGB=96`.

**Quantization config**: `maxMemoryGB=96`, `maxSamples=256` (no MoE), `timeoutSeconds=28800` (8h).

### GPTQ Performance on ROCm

| Model | Decode tok/s | Prompt tok/s | VRAM | Context |
|-------|-------------|-------------|------|---------|
| 26B-A4B MoE INT4 | ~72 | ~1800 | ~13 GB | 32K |
| 31B Dense INT4 | TBD | TBD | ~16 GB | 4K-8K |

ExLlama v2 kernels (HIP-compiled) with `sym=true` achieve 7x faster decode than AWQ on gfx1100.

## Deployment Reliability (2026-04-13)

| Feature | Status | Notes |
|---------|--------|-------|
| GPUProfile watch | Working | Controller watches GPUProfile CRs; image changes trigger reconciliation |
| Image drift detection | Working | Stale running jobs auto-deleted on GPUProfile image update |
| Script version marker | Working | `FLEXINFER_SCRIPT_VERSION=v7` checked at job startup |
| Deploy automation | Working | `make deploy-quantizer QUANTIZER_ARCH=gfx1100` |
| Spec hash with image | Working | `quantSpecHashWithImage()` includes resolved image in hash |

## Next tuning queue

1. Raise `gemma4-e4b-long` `maxNumBatchedTokens` conservatively and remeasure.
2. Recheck whether the fast lane benefits from `maxNumBatchedTokens > 512`.
3. Separate cold-start and warm-path benchmarks in automation and preserve
   JSON artifacts per run.
4. Inspect TurboQuant prefill behavior around paged decompress fallback rather
   than continuing blind manifest tuning.
5. Benchmark 26B-A4B MoE GPTQ INT4 against E4B GGUF for latency/throughput comparison.
6. Benchmark 31B Dense GPTQ INT4 on radeonvii once quantization completes.
