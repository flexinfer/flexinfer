# Research: 5930k decode-rate profile against vectorized image (post-MR !363)

**Date**: 2026-05-14
**Linked from**: `.loom/brainstorm-26b-5930k-decode-perf-round2-2026-05-14.md` (R1 — profile-first)
**Pod**: `gemma4-26b-a4b-gptq-5930k-5f87dc647-gjv4b` on cblevins-5930k
**Image**: `registry.harbor.lan/flexinfer/runtime@sha256:c2c89b330c3f414e23b75f468d94b1d80b512a8d539951645c6971446adf77a1` (vectorized, MR !363)
**Method**: `py-spy record --pid 458 --duration 50 --rate 100 -t -i --format raw` against the engine core process during a single 200-token decode under matched workload. **34,986 samples captured.**

## Architecture note — why the first profile was empty

`vLLM v1` is multi-process. The container's PID 1-tier process (`PID 7`, `python3 -m vllm.entrypoints.openai.api_server …`) is the **async API frontend**, not the engine. It runs uvloop + asyncio with thread-pool workers idling on `select`/`queue.get`. The actual model forward pass runs in a separate spawned worker process (`pstree` shows `VLLM::EngineCor(458)` with ~30 child threads). The first profile (against PID 7) was 100% idle frames. All subsequent results below are against the engine core PID 458.

## Top Python leaves on the engine `MainThread` (where the forward pass runs)

MainThread = 4,998 samples (≈50s of work). Excluding shared bootstrap frames:

| Samples | % MainThread | Function | File:Line | What it does |
|---|---|---|---|---|
| 416 | 8.3% | `_unpack_u4_last_dim` | moe_wna16.py:425 | `out[..., 1::2] = (packed_i16 >> 4) & 0xF` |
| 341 | 6.8% | `_unpack_u4_last_dim` | moe_wna16.py:424 | `out[..., 0::2] = packed_i16 & 0xF` |
| 235 | 4.7% | `_get_w2` | moe_wna16.py:487 | per-expert w2 dequant chain |
| 212 | 4.2% | `_get_w13` | moe_wna16.py:470 | per-expert w13 dequant chain |
| 167 | 3.3% | `_unpack_u4_last_dim` | moe_wna16.py:418 | `out = torch.empty(...)` for unpacked tensor |
| 116 | 2.3% | `apply` | moe_wna16.py:534 | per-token loop `_w13_batch = torch.stack([_get_w13(eid)...])` |
| 81 | 1.6% | `apply` | moe_wna16.py:581 | `_expert_out_batch = _expert_out_batch * _tok_router_w.view(...)` |
| 66 | 1.3% | `apply` | moe_wna16.py:547 | `_expert_in_batch = ...` gather |
| 57 | 1.1% | `apply` | moe_wna16.py:582 | `_tok_out = _expert_out_batch.sum(dim=0)` |
| 51 | 1.0% | `apply` | moe_wna16.py:394 | `_x_has_inf = x.isinf().any().item()` ← GPU sync |
| 39 | 0.8% | `apply` | moe_wna16.py:393 | `_x_has_nan = x.isnan().any().item()` ← GPU sync |
| 39 | 0.8% | `apply` | moe_wna16.py:520 | `_topk_ids_cpu = topk_ids.tolist()` ← GPU sync |

Roll-up by function:

| Function | Total samples | % MainThread |
|---|---:|---:|
| `_unpack_u4_last_dim` (5 lines) | 1,022 | **20.4%** |
| `_get_w2` (5 lines) | 573 | **11.5%** |
| `_get_w13` (5 lines) | 528 | **10.6%** |
| `apply` inner loop (lines 510-595) | 490 | **9.8%** |
| Torch `__call__`/dispatch | 411 | 8.2% |
| `__floordiv__` (torch/_tensor.py:1119) | 158 | 3.2% |

**The MoE-wna16 reference path accounts for >55% of engine MainThread time.** This is the path the vectorize patch lives on. The vectorize replaced 16 small matmuls with 2 `bmm` per token, but it did **not** touch the dequant chain, the per-expert iteration, or the NaN/Inf debug checks — and those are now where the time has gone.

## Three specific bottlenecks identified

### B1 — Persistent expert cache cap is broken for gemma4

**Code** (`moe_wna16.py:439-457`):
```python
_cache = getattr(layer, '_gemma4_rocm_ref_cache', None)
if _cache is None:
    _cache = {}
    layer._gemma4_rocm_ref_cache = _cache
    layer._gemma4_rocm_ref_cache_order = []

def _cache_put(key, value):
    if key in _cache:
        return _cache[key]
    _cache[key] = value
    _cache_order.append(key)
    while len(_cache_order) > 16:           # ← cap
        _evict = _cache_order.pop(0)        # ← FIFO, not LRU
        _cache.pop(_evict, None)
    return value
```

**Model config** (`/models/gemma4-26b-a4b-gptq/config.json`):
- `num_experts = 128`
- `top_k_experts = 8`
- `num_hidden_layers = 30` (all MoE per `layer_types` enumeration)
- `moe_intermediate_size = 704`, `hidden_size = 2816`
- Per-expert FP16 dequantized weight footprint: 11.34 MiB (w13: 7.74 MiB + w2: 3.87 MiB)

**Why the cap fails**: every `apply()` call requests `top_k=8` experts × 2 entries (`('w13', eid)` and `('w2', eid)`) = **exactly 16 entries**. The cache cap is 16. So one call fills the cache; the next call with any different experts evicts everything inserted by the previous call. **Inter-call cache reuse is essentially zero**, and intra-call there's no reuse either (each expert is unique). The cache was sized assuming top_k=2 (the typical MoE config). gemma4 is unusual with top_k=8.

**Profile evidence**: if the cache were hot, `_unpack_u4_last_dim` would not appear — `_get_w13`/`_get_w2` would return early at `if key in _cache: return _cache[key]`. The 1,022 leaf samples in `_unpack_u4_last_dim` confirm the cache is missing on every call.

**Fix sizing**: full per-layer cache = 128 experts × 11.34 MiB × 30 layers = 42.5 GiB → won't fit in 24 GB VRAM. Bounded cache feasible: ~3-4 GB headroom available after model (13 GB INT4) + KV cache, giving ~270 cached experts total = ~9 experts/layer = ~7% of 128. Hit rate depends on routing skew — needs measurement on calibration set. **If routing is uniform, the cache buys little. If skewed (typical MoE behavior), 50-80% hit rate is plausible.**

### B2 — Per-call GPU→CPU syncs from debug + dispatch

**Code** (`moe_wna16.py:393-394` and `:583-584`):
```python
_x_has_nan = x.isnan().any().item()       # GPU→CPU sync
_x_has_inf = x.isinf().any().item()       # GPU→CPU sync
...
_r_has_nan = _result.isnan().any().item() # GPU→CPU sync
_r_has_inf = _result.isinf().any().item() # GPU→CPU sync
```

These are NaN/Inf debug guards added during abliteration/quantization debugging. Each `.item()` forces the host to wait for the GPU to flush. Four per `apply()` call × 30 MoE layers × ~140 tokens = **~16,800 GPU→CPU syncs per request**. The 5930k's X99 PCIe 3.0 latency is meaningfully higher than the 7900xtx host's — this is exactly the kind of bottleneck that disproportionately affects the older host.

**Profile evidence**: lines 393 and 394 contribute 90 leaf samples (~1.8% of MainThread). This understates the true cost because the GPU work is stalling — the CPU is waiting, so it samples briefly in `.item()` before the next call. Realistic cost is higher than the raw sample count suggests.

**Fix**: gate behind an env var (e.g. `FLEXINFER_MOE_DEBUG_NANCHECK=0` default) or remove entirely. Pure debug, no behavioral need in steady state.

### B3 — Per-expert Python iteration with 4-6 kernel launches per unpack

**Code** (`moe_wna16.py:417-425`):
```python
def _unpack_u4_last_dim(packed: torch.Tensor) -> torch.Tensor:
    packed_i16 = packed.to(torch.int16)           # 1 launch
    out = torch.empty(...)                         # alloc
    out[..., 0::2] = packed_i16 & 0xF             # 2 launches (mask + assign)
    out[..., 1::2] = (packed_i16 >> 4) & 0xF      # 3 launches (shift + mask + assign)
    return out
```

Plus the surrounding `_get_w13`/`_get_w2` chain:
- `_q.to(torch.float16)` — 1 launch
- `_g = torch.arange(...) // group_size` — 2 launches (arange + floordiv)
- `_w - _zp[:, _g].to(torch.float16)` if zeropoints — 3 launches (gather + cast + sub)
- `_w * scales[:, _g].to(torch.float16)` — 3 launches (gather + cast + mul)
- `_w.transpose(0, 1).contiguous()` — 1 launch

So **~10-12 kernel launches per expert** through `_get_w13`. The per-token loop calls this 8 times (top_k=8) for w13 and 8 times for w2 = **160-192 launches per MoE layer per token just for dequant**. With 30 MoE layers × 140 tokens = **~700K dequant launches per request**.

**Fix options**:
- **B3a (batched unpack)**: gather all top_k=8 expert weights with `layer.w13_qweight[_safe_eids]` first, then call a single batched `_unpack_u4_last_dim` over the [8, ...] tensor. Reduces per-call launches by ~8×.
- **B3b (Triton fused dequant + bmm kernel)**: single kernel does unpack + dequant + matmul. Reduces per-call launches by ~50×. This is R2 from the brainstorm; 3-7 days work.

## What's NOT the bottleneck

- `bmm` itself — the two batched matmuls per layer (the vectorize win) barely show in the profile. The matmul is GPU-bound and fast.
- Attention / KV-cache — not visible in the top leaves; the wna16 path dominates.
- Speculative-decoding-shaped problem — top-leaf evidence is "per-token Python work doing dequant", not "model forward is expensive once and we should do fewer forwards." R5 from the brainstorm would help, but it's attacking the wrong axis given this data.
- Memory bandwidth — no evidence either way from py-spy alone; rocprof would be needed to confirm. But the Python-side overhead is large enough that fixing it should yield wins regardless of any underlying GPU-side bottleneck.

## Recommendation update vs brainstorm round 2

The brainstorm's runner-up was **R3 (hot-expert cache)** and recommended **R5 (speculative decoding)**. This profile flips the priority:

1. **First slice (smallest, lowest-risk, biggest expected win for the effort)**: B1 + B2 combined.
   - B1: lift the cache cap (16 → at least `num_experts × 2 = 256` per layer, or memory-budgeted) and switch FIFO to LRU. The cache *exists and works*, it's just sized for the wrong model. If routing is even mildly skewed, this immediately starts paying off.
   - B2: drop the four NaN/Inf `.item()` syncs (or gate behind env var). Pure overhead removal.
   - Both fit in a single ~30-line patch + image rebuild + canary, identical workflow to MR !363.

2. **Second slice if (1) underperforms**: B3a — batched unpack across top_k experts. Reduces per-expert launches without writing a kernel. Bigger refactor than (1); same in-repo file.

3. **Third slice if structural change still warranted**: B3b — Triton fused kernel. Same as R2 in the brainstorm.

**R5 (speculative decoding) deprioritized** by this data: per-forward cost is not what dominates; per-forward CPU dispatch on the dequant path is. Spec-decode would help but is attacking a smaller lever.

## Suggested next action

Implement B1 + B2 as a single MR (call it `perf/moe-cache-cap-and-nan-sync`). Expected change: ~20 lines in `build/scripts/gemma4_moe_patch.py` (or wherever the patch is applied). Validate with the same 6-prompt coherence gauntlet used for MR !363, then re-benchmark. If the gain is ≥10%, ship; if <5%, the cache thesis (skewed routing assumption) is falsified and B3a becomes the right next move.

## Open instrumentation

If we want a calibration measurement before committing to the cache fix:
- Add a one-shot env-gated counter that records `expert_id` distribution per layer over ~500 tokens of representative workload
- Dump the histograms; if top-20 experts cover ≥70% of routing, the cache is high-value; if uniform, B3a is the better first slice
