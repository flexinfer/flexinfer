# R5 — ngram speculative decoding: falsified on 5930k

**Date**: 2026-05-14 (post-MR !365 ship)
**Linked from**: `.loom/brainstorm-26b-5930k-decode-perf-round2-2026-05-14.md` (R5 candidate)
**Outcome**: REVERTED. ngram speculative decoding is **−15% throughput** on free-form and structured workloads on this model/host. Not viable as a general optimization.

## What was tried

`speculativeConfig: '{"method": "ngram", "num_speculative_tokens": 5, "prompt_lookup_max": 4, "prompt_lookup_min": 2}'` added to the 5930k Model CR config. vLLM's backend already supports opaque-JSON passthrough to `--speculative-config` (`backend/vllm.go:187`), so no CRD or controller change was needed.

vLLM engine started cleanly on the post-MR !365 image (`runtime:rocm-gfx1100-gemma4-moe-cache-nan-v3`). Engine logged spec config registered correctly: `SpeculativeConfig(method='ngram', model=None, num_spec_tokens=5)`.

Two warnings logged at startup:

- "Async scheduling not supported with ngram-based speculative decoding and will be disabled." — disables async scheduling, an optimization that batches concurrent requests; not relevant for `max_num_seqs=1` workloads.
- "max_num_scheduled_tokens is set to 160 based on the speculative decoding settings. This may lead to suboptimal performance. Consider increasing max_num_batched_tokens to accommodate the additional draft token slots, or decrease num_speculative_tokens or max_num_seqs."

## Measurements

| Workload | mean / completion_tokens | tok/s | Δ vs cache-nan baseline (4.57 tok/s) |
|---|---|---|---|
| Free-form essay (post-MR !365 baseline) | 43.76s / 200 | **4.57** | — |
| Free-form essay + ngram(5,4,2) | 50.25s / 200 | **3.98** | **−13%** |
| Structured JSON + ngram(5,4,2) | 62.0s / 221 | **3.56** | **−22%** |

Coherence: 6/6 exact-match vs 7900xtx goldens (as expected — temperature=0 spec-decode is verified-equivalent).

## Why it didn't work

The brainstorm round-2 R5 framing noted this risk explicitly: "low acceptance can be worse than no spec-decode." That's what happened. The mechanism specifically:

1. **Each step now runs the target forward on `1 + num_speculative_tokens = 6` positions** instead of 1. The target's per-step Python+kernel-launch cost scales roughly linearly with this width (it's still eager mode — no graph capture to amortize).
2. **For gemma4-MoE on the 5930k**, that scaling is brutal: the MoE patch path is the dominant per-step cost (we just spent two slices chipping at it), and now we're running it on 6 positions instead of 1 every step.
3. **The acceptance rate would need to be very high (~4+ tokens accepted per step)** to net out positive. ngram's prompt-pattern matching is fundamentally limited:
   - Free-form prose: low repetition, ~1 token accepted per step on average. Net cost: ~6x per-step compute, ~1.5x output rate at best, often a loss.
   - JSON output: more repetition, but each JSON value (name, email) is still semantically new content; only field-name prefixes and braces match n-grams. ~1-2 tokens accepted per step. Still a loss when the per-forward cost is high.

This is unlike a fast target model where the spec-decode overhead is small relative to per-forward cost. Our target is gpu-dispatch-bound; widening each forward makes that bottleneck worse.

## What this rules in / out for future rounds

- **R5 via ngram** is falsified for this model/host as a general optimization. Do not revisit unless either (a) the MoE patch path becomes much cheaper per-forward (i.e., a Triton fused kernel), or (b) the workload is specifically structured/repetitive at scale (e.g., emit-many-similar-records batch jobs) where ngram acceptance can clear 3+ tokens/step.
- **R5 via draft model (Gemma3-1B)** remains untested but at the same per-forward-width cost. The acceptance rate from a small same-family draft would likely be higher (50-70%) but the per-forward overhead is the same. Could still net negative on this host until graph capture works or per-forward cost is cut further.
- **R5 via suffix decoding** also untested. Similar mechanism, tree-structured prediction; same per-forward-width concern.

## Cleanup performed

- `kubectl patch model gemma4-26b-a4b-gptq-5930k --type=json -p '[{"op":"remove","path":"/spec/config/speculativeConfig"}]'` — removed the config.
- Pod rolled cleanly back to the cache-nan-v3 baseline.
- Flux resumed. Cluster matches gitops master (gitops yaml never carried the spec config — kubectl patch only).

## Recommendation for next R-loop

The cache-nan slice (MR !365) is the last clean win on this surface without changing the per-forward cost structure. Remaining options from round 2:

- **R3 (hot-expert cache with real memory budget)**: requires lowering `gpuMemoryUtilization` to make headroom. Could unlock B3a-style batched dequant. ~1-2 days of careful work to size the budget without breaking KV cache.
- **R7 (stop)**: defensible — cumulative −33% is meaningful, and the remaining gap is hardware-bound (X99 PCIe 3.0 + DDR4 vs whatever the 7900xtx host has).

R5 (any flavor) is the right move only AFTER per-forward cost drops or graph capture lands.
