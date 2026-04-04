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
| `gemma4-e4b-long` | `32768` | `128` | `0.80` | `minReplicas: 0` |

## Latest baseline

Source:

- `scripts/bench-gemma4-profiles.sh`
- run id: `gemma4-20260404T091120-f0bf7e`

Environment:

- LiteLLM via `kubectl -n ai port-forward svc/litellm 18000:8000`
- `WARMUP=1`
- `SHORT_REPEAT=2000`
- `LONG_REPEAT=6000`
- `MAX_TOKENS=64`

Results:

| Model ID | Prompt tokens | Elapsed | Prompt tok/s | Completion tok/s | Notes |
|----------|---------------|---------|--------------|------------------|-------|
| `gemma4-e4b` | `73` | `0.987s` | `73.93` | `64.81` | Default alias health check |
| `gemma4-e4b-fast` | `10035` | `5.389s` | `1862.28` | `11.88` | Stable fast path |
| `gemma4-e4b-long` | `10035` | `29.481s` | `340.38` | `2.17` | Warmed short-context TurboQuant |
| `gemma4-e4b-long` | `30035` | `134.645s` | `223.07` | `0.48` | Warmed long-context TurboQuant |

Interpretation:

- The fast profile is in a good place for interactive use on the 7900 XTX.
- The long profile is functional at ~30k prompt tokens, but its prefill path is
  still the main performance bottleneck.
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

## Next tuning queue

1. Raise `gemma4-e4b-long` `maxNumBatchedTokens` conservatively and remeasure.
2. Recheck whether the fast lane benefits from `maxNumBatchedTokens > 512`.
3. Separate cold-start and warm-path benchmarks in automation and preserve
   JSON artifacts per run.
4. Inspect TurboQuant prefill behavior around paged decompress fallback rather
   than continuing blind manifest tuning.
