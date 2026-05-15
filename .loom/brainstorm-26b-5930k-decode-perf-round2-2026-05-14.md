# Brainstorm round 2: next 5930k decode-rate optimization after vectorize ship

**Date**: 2026-05-14 (later same day as round 1)
**Triggered by**: MR !363 (gemma4-moe-patch vectorize) shipped −24.9% cumulative on 5930k (48.89 s → 36.70 s mean), closing the 7900xtx gap from 2.13x → 1.60x. User push: pick up from here and find the next most impactful fix.
**Prior brainstorm**: `.loom/brainstorm-26b-5930k-decode-perf-2026-05-14.md` (round 1: F1 governor +4% kept, F2 cuda graphs crashed with `hipErrorStreamCaptureUnsupported`, F7 profile-first led to vectorize).
**Carry-forward constraints**:
- **CUDA graphs are blocked at the framework level** on gemma4-MoE+ROCm+current vLLM (`v0.1.dev1+g467d3247c.d20260410`). Anything depending on graph capture is dead until an upstream fix lands.
- Governor already at `performance` on 5930k (kept live, +4%).
- Per-token Python pruning skipped — F1's marginal result implied diffuse cost.
- `enforce_eager: true` still load-bearing (manifest comment is correct 8 months later).
- Both nodes run 7900 XTX (gfx1100). Host difference is X99/Haswell-E + DDR4 (5930k) vs newer/unknown platform (7900xtx).

## Phase 1 — Framings

### R1 — Re-profile against the new state

Previous profile drove a 25% win. The bottleneck has almost certainly *moved*. 36.70 s / 141 tokens = **260 ms/token on 5930k** vs **163 ms/token on 7900xtx**, a 97 ms/token gap. Vectorize cut Python-dispatched ops per layer from ~16 to ~2; with ~40 layers that's 80 launches/token still. Re-run `py-spy` + `rocprof` against the new image to see whether the remaining gap is (a) residual Python dispatch, (b) dequant-bound compute, (c) attention/KV-cache, or (d) something host↔device we haven't characterized.

- **Bet**: bottleneck has shifted; every R2-R6 below is a guess until we look.
- **Risk**: 1-2 hours, may reveal diffuse cost that loops back to "accept the gap."

### R2 — Triton fused INT4-dequant + matmul kernel for the patch path

Vectorize replaced 16 small matmuls with 2 batched `bmm` calls, but those bmms still operate on **FP16-dequantized expert weights computed per-token**. The INT4→FP16 dequant is a separate kernel (and a transient ~256MB allocation per layer per token at top_k=2). A ~100-line Triton kernel fusing dequant + matmul reduces 2 launches/layer to 1 *and* eliminates the FP16 intermediate. Triton works on ROCm/gfx1100 today (vLLM uses it for its native AWQ path).

- **Bet**: fused kernel removes per-token dequant cost and alloc/dealloc churn — 15-25% on top of vectorize.
- **Risk**: writing/debugging a custom Triton INT4-asym GPTQ kernel for gfx1100 is non-trivial. 3-7 days. Correctness validation needs the full coherence gauntlet again.

### R3 — Hot-expert FP16 cache (skewed routing exploit)

MoE expert routing is empirically skewed: some experts get 5-10x more tokens than others. Pre-dequantize the top-N most-used experts per layer (measured from a calibration pass), keep them resident in FP16, fall back to on-the-fly dequant only for cold experts. With top_k=2 and 8 experts/layer, caching 2-3 experts/layer could hit 60-80% of routing decisions. 27B INT4 = ~13 GB; 24 GB GPU has ~5-7 GB headroom for cache + KV.

- **Bet**: dequant is a meaningful slice of per-token cost and gemma4 routing is skewed enough for a high cache hit rate.
- **Risk**: gemma4-MoE routing may be more uniform than typical (model-specific; need to instrument). Cache-miss path still has today's cost. Adds VRAM pressure on a node where the model already fills 13/24 GB.

### R4 — Re-quantize to a format with a native HIP MoE kernel

The patch exists *because* gemma4 MoE has no compiled HIP MoE kernel in vLLM. Escape it: find any format that does have one — vLLM's `fused_moe` Triton kernel works for several formats (FP8, native AWQ-MoE in newer versions, BF16). Re-quantize gemma4 to that format and drop the patch entirely. Existing quantization pipeline ~75 min for a 27B model.

- **Bet**: leaving the Python reference path is the only path with order-of-magnitude potential without writing kernels.
- **Risk**: no such format may exist for gemma4-MoE on ROCm today (didn't when we built the patch). Quality regression from re-quantization is real (we already validated the GPTQ version). 2-4 days of hunting/quantizing for a possible dead end.

### R5 — Speculative decoding with a small same-family draft

vLLM supports spec-decode. A small draft (Gemma3-1B or Gemma-2-2B, same tokenizer family for high acceptance) generates K tokens; the 27B verifies them in one forward pass. **Crucially: spec-decode does not require CUDA graph capture** — target model still runs eager, but each big-model forward emits K accepted tokens. K_accepted=2.5 on average → 2.5x cut to *per-output-token* wall time even if per-forward cost is unchanged.

- **Bet**: target-model Python dispatch is amortized across accepted draft tokens; this attacks the residual gap where it actually lives (cost per forward × number of forwards).
- **Risk**: vLLM spec-decode on ROCm + gemma4-MoE is unvalidated by us; the patch path that broke graphs *might* break spec-decode similarly. Draft needs ~2-4 GB VRAM — fits but tight. Acceptance rate is workload-dependent; low acceptance can be worse than baseline.

### R6 — Cross-token batching inside the MoE patch

Patch processes tokens within a step sequentially (outer loop iterates tokens). With max_num_seqs=1 the batch is small, but prefill can be tens of tokens at once, and concurrent decode requests would benefit. Refactor patch to accept a [tokens × hidden] tensor and process all tokens in one pass per layer, gathering selected experts via `torch.scatter_add` or grouped GEMM. Current vectorize batched *experts* within a token; this batches *tokens* across the layer.

- **Bet**: there are still hidden serial paths in the patch; batching them helps prefill significantly and helps decode under concurrency.
- **Risk**: more invasive refactor than vectorize. Won't help the C=1 benchmark (single token in flight at decode). May regress correctness in edge cases (single-token early steps, padding).

### R7 — Ship the win, stop pushing on this surface

Pin 7900xtx primary to the vectorized image, declare 5930k operationally good-enough at 1.60x, reallocate engineering bandwidth elsewhere. Vectorize was a clean −25%; the next 25% costs 5-10x the engineering. Diminishing returns are real; backlog has other items competing for this time.

- **Bet**: marginal value of further 5930k optimization < marginal value of doing something else.
- **Risk**: if R5 or R3 is a 20% win for a week of work, walking away leaves it on the table indefinitely.

## Phase 2 — Cross-Pollinate

### Combinations

- **R1 → R2/R3/R4/R5**: R1 is a gate. Previous loop validated this playbook (profile-first → vectorize). The bottleneck has demonstrably shifted; without measuring, we're picking between hypotheses by gut feel.
- **R2 + R3**: Triton fused dequant kernel that *also* reads from a hot-cache pool when available. The fused kernel naturally has a branch for "is this expert in the resident FP16 pool?" — skip dequant, pure GEMM. Higher complexity but the two mechanisms compose cleanly.
- **R5 + (any per-forward speedup)**: speculative decoding multiplies any per-forward win. R5+R2 or R5+R3 stack rather than overlap.

### Tensions

- **R2 (build a kernel) vs R4 (find an existing kernel)**: both leave the Python patch path. R2 is "do the engineering ourselves, certain payoff if it works"; R4 is "go shopping, uncertain payoff, no kernel writing." Real axis: build-vs-buy on inference kernels.
- **R5 (multi-token-per-forward) vs R2/R3 (per-forward tightening)**: opposing theories of where the gap lives. R5 says "each forward costs what it costs; cut the number of forwards." R2/R3 say "cut what each forward costs." Profile decides which is the bigger lever — they are not the same money.
- **R7 (stop) vs everything else**: real axis is *marginal engineering value*. After a clean −25% win, is the next slice 5x harder for the same gain? Probably yes — the easy wins are gone.

## Phase 3 — Converge

### Recommended: **R1 (re-profile) → R5 (speculative decoding)** as the most likely follow-up

The previous loop demonstrates the value of profiling against the *current* state — round 1 dismissed per-token Python cost as "diffuse" and recommended documenting, then a profile revealed a single kernel-launch-bound surface and the fix was −25%. The bottleneck has moved again. 1-2 hours of `py-spy` + `rocprof` on the vectorized image is the highest-EV next action.

If profiling confirms what the math suggests — residual gap dominated by Python dispatch overhead per forward — **R5 (speculative decoding) is the right follow-up**, not R2/R3. Reason: per-forward cost has already been compressed hard by the vectorize, and CUDA graphs (the natural next per-forward optimization) are blocked at the framework level. Spec-decode is the *only* high-ceiling lever that attacks per-output-token cost without requiring graph capture: it reduces the *number* of expensive forwards per accepted token rather than the cost of each forward. Same-family draft (Gemma3-1B) gives high acceptance rates; the draft model fits in VRAM headroom; integration is config-level rather than kernel-level.

### Runner-up: **R3 (hot-expert FP16 cache)** if profile shows dequant cost dominant

Tipping trigger: `rocprof` shows the INT4→FP16 dequant kernel (or the transient FP16 alloc/free) as the top time consumer. In that case spec-decode helps less (each forward is dequant-bound rather than dispatch-bound), and the right move is a targeted attack on the dequant cost. R3 has bounded implementation risk (no kernel writing, no new framework features, just instrumentation + caching policy + memory budget) and a clear measurement path (cache hit rate × per-miss dequant cost). Lower ceiling than R5 if it works (~10-15%) but much higher floor.

### Open question

**What does `py-spy --pid $(pgrep vllm)` show as the top-3 functions during steady-state decode on the vectorized 5930k pod, and what does `rocprof --stats` show as the top-3 HIP kernels by total time?** Without these two numbers, R5 vs R3 is a coin flip. With them, the choice is decided by data. If profiling reveals the gap is now dominated by attention or KV-cache rather than the MoE patch at all, the whole framing changes and we loop back into a third brainstorm round.

A second question worth flagging: **is 5930k traffic projected to grow, or is it a fixed warm-spare?** If warm-spare, R7 (stop) becomes more defensible — capacity is the value, latency parity isn't worth chasing. If 5930k is expected to absorb meaningful traffic, the case for R5 strengthens.

## Handoff

- **R1 chosen** → `research` skill to capture `py-spy` flamegraph + `rocprof --stats` against the live 5930k pod under matched workload, write findings into a follow-up note, then re-enter this brainstorm (or successor) with evidence.
- **R5 chosen directly (skipping profile)** → `feature-dev` to add draft model spec on 5930k Model CR and enable spec-decode in engine args; coherence gauntlet against 7900xtx goldens; benchmark accepted-tokens-per-step and end-to-end latency.
- **R3 chosen directly** → `feature-dev` to instrument expert-selection histograms on the live pod, add an FP16 hot-cache pool to the MoE patch with eviction policy, validate cache-hit rate against the histogram, benchmark.
- **R7 chosen** → `feature-dev` to pin 7900xtx primary to the vectorized image (one-line digest swap in `platform/gitops/k3s/ai/flexinfer/models/`) and update `.loom/00-index.md` to mark the 5930k decode-rate optimization slice closed.
- Linked spec/plan doc (fill in once it exists): `<.loom/NNN-...md>`
