# Brainstorm — gfx906 72B prefill wall: `HIP invalid argument` only inside the distributed engine

**Date**: 2026-06-09
**Status**: diverged + converged; kill-test not yet run
**Related**: project memory `project_f5_3way_window`, `gfx906-vllm-fill-segfault.md`, MR chain for unified multi-arch image (2ffda431, b0d2e43d)

## Problem (one sentence)

Qwen2.5-72B-GPTQ 3-way PP (gfx1100 + gfx906 + gfx1100, unified image, vLLM 0.6.3, torch 2.4) dies during the profiling forward on the gfx906 rank inside math-mode SDPA with `HIP invalid argument` — while **every standalone reproduction passes** (same shapes, GQA 64q/8kv, non-contiguous slices, 13 GB VRAM fill), so the fault only manifests in the full engine (Ray actor + RCCL + GPTQ shard).

## Constraints

- Every full 72B relaunch costs the daily driver (gemma4 lanes vacated) — experiments are expensive in wall-clock and disruption.
- vLLM 0.6.3 + torch 2.4 pinned by the unified multi-arch image; the proven gfx906 fork (mobydick, torch 2.11) is a *different* stack.
- gfx906/Vega20 hard limits: no VMM, ~2 GB single-allocation cap, `HSA_OVERRIDE_GFX_VERSION=9.0.6`, deprecated in ROCm.
- 3-way graph-mode coherence with opt-125m is already banked; the 72B is the remaining milestone.
- Restore runbook is durable — windows are repeatable, just costly.

## Riskiest assumption + kill-test

**Load-bearing assumption**: The failure is determined by the *op set and engine path* (custom HIP kernels + Ray/RCCL context), not by 72B *scale* — so a toy Qwen GPTQ model pushed through the same unified-image engine on radeonvii reproduces it.

**Kill test** (≤30 min): Deploy `Qwen2.5-0.5B-Instruct-GPTQ-Int4` single-GPU on radeonvii using the unified image with `AMD_SERIALIZE_KERNEL=3` (and `HIP_LAUNCH_BLOCKING=1`), send one 2048-token prefill. Observable outcome: either the same `HIP invalid argument` fires (and serialization names the exact kernel), or the toy passes cleanly.

**Failure mode if wrong**: We burn debugging effort on kernel-level bisection while the real cause is allocator/fragmentation behavior that only appears at ~13 GB shard scale. Mitigation: a toy-pass verdict is itself evidence — it redirects squarely to the memory/fragmentation framing (F6) for the one next 72B relaunch.

**Status**: ran 2026-06-09 (same day) — **decisive for the toy kill-test, but incomplete for 72B**. Toy refined to Qwen2.5-**1.5B**-GPTQ (head_dim 128; 0.5B rejected — head_dim 64 = opt-125m fingerprint). Op-set/Ray-actor variants PASSED (assumption's op-set half falsified); adding the **memory context** (11GB in-process fill + util 0.92) REPRODUCED the exact failure with no Ray/RCCL/72B. Serialized attribution in the toy path named `cache_engine._allocate_kv_cache → torch.zeros` (profile-derived KV alloc on near-full Vega20) — the window's SDPA traceback was async misattribution (F1 confirmed). Fix matrix: util cut FAIL, rebalance FAIL, **`num_gpu_blocks_override` PASS** for toy. Evidence: `.loom/local/validation/f5-3way-2026-06-09/TOY-KILLTEST-VERDICT.md`.

**2026-06-10 full 72B correction**: the 3-way relaunch with `--num-gpu-blocks-override 256` did **not** serve. It joined all three GPUs, loaded all 11 shards, and put the Radeon VII rank at 13.4144 GB, then still entered `determine_num_available_blocks -> profile_run` and failed on the Radeon VII rank inside `vllm/_custom_ops.py:gptq_gemm` during Qwen2 MLP `gate_up_proj`. So the full 72B blocker is no longer best framed as KV allocation alone; the next proof must target the gfx906 fused GPTQ kernel path under 72B shard pressure. Evidence: `.loom/local/validation/f5-3way-2026-06-10/head.log` and `.loom/32-iteration-plan-f5-72b-3way-relaunch-2026-06-10.md`.

## Phase 1 — Diverge

### F1 — "The error is lying" (async blame-shift)

HIP kernel-launch errors surface asynchronously: an invalid launch configuration from a kernel *before* SDPA can be reported at the next synchronizing call — which math-mode SDPA is full of. On the gfx906 rank, the ops immediately preceding SDPA in a Qwen layer are: RCCL recv (middle rank), RMSNorm **custom HIP kernel**, QKV projection via **GPTQ exllama HIP kernel**, rotary embedding **custom HIP kernel**. SDPA may be the messenger, not the culprit. Diagnose with `AMD_SERIALIZE_KERNEL=3` / `HIP_LAUNCH_BLOCKING=1` on the gfx906 worker so the error fires at the true call site.

- **Bet**: SDPA is innocent; a custom HIP op launched just before it has an invalid launch config (block dims / LDS size) on gfx906.
- **Risk**: Serialization changes timing and perf; if the error genuinely originates in SDPA-with-engine-context-inputs, this run names SDPA again and adds little.

### F2 — "Shrink the failure, keep the physics" (toy same-op-family repro)

The "small model passed" comfort is likely false: opt-125m uses learned positional embeddings (no rotary kernel), `nn.LayerNorm` (no RMSNorm custom kernel), ReLU (no `silu_and_mul` kernel), and fp16 linear (no GPTQ kernels). *(Assumption — verify against vLLM 0.6.3 OPT/Qwen2 model code.)* So the 3-way opt test exercised almost none of Qwen's custom HIP ops on gfx906. A toy Qwen GPTQ (0.5B/1.5B) through the same engine exercises the exact op set at minutes-per-iteration cost: first single-GPU on radeonvii, then 3-way PP at toy scale.

- **Bet**: The failure is op-set/engine-path determined and reproduces at toy scale — converting an unfalsifiable "deep HIP/process-context bug" into a cheap bisect loop.
- **Risk**: The failure is scale-bound (memory pressure, shard size); toy passes and one probe is spent — but that verdict is still decision-grade (points to F6).

### F3 — "Route around profiling" (config-only dodge)

The crash is in the *profiling* forward — the worst-case `max_num_batched_tokens` batch vLLM runs once to size the KV cache. `--num-gpu-blocks-override` skips profiling entirely; enabling chunked prefill with a small chunk (256–512) keeps every *real* prefill far below the profiling shape. Zero build, pure engine args.

- **Bet**: Only the worst-case profiling shape fails; real traffic shapes pass (consistent with all standalone shape probes passing).
- **Risk**: Masks the bug instead of fixing it; a long prompt or batched prefill later hits the same wall in production, at a worse time.

### F4 — "Patch the call site" (gfx906 SDPA shim)

The fix family the window already converged toward: monkey-patch the gfx906 rank's prefill attention — repeat KV heads before SDPA, force `.contiguous()`, or decompose into a chunked manual softmax(QKᵀ)V that keeps every intermediate under the Vega20 2 GB single-alloc cap.

- **Bet**: Fused math SDPA with engine-context inputs (exact strides/batch layout the engine produces) is the failure; decomposition avoids it while preserving coherence.
- **Risk**: Standalone probes already pass these exact shapes/GQA/contiguity — so this likely fixes nothing, and the verdict costs a full 72B relaunch (the most expensive experiment we have).

### F5 — "The unified image is the wrong bet" (per-arch images, matched versions)

Abandon the one-image constraint. The 3-way RCCL kill-test already proved mixed-arch communicators work with *per-arch images* when RCCL versions match. Build a gfx906 worker image from the mobydick fork stack (torch 2.11, proven dtype-clean on gfx906) with vLLM pinned to the same version as the gfx1100 image, and let Ray place per-arch images per node.

- **Bet**: The fork's newer torch/kernel stack has working gfx906 prefill, and Ray PP tolerates heterogeneous images when vLLM versions match (cross-worker traffic is RCCL tensors + pickled control objects).
- **Risk**: Large build effort; version-skew fragility across images; trades a localized known wall for a frontier of unknown ones.

### F6 — "It's still Vega20 memory, just dressed up" (allocation-context framing)

In-engine, the gfx906 HIP heap is shaped by a 13 GB GPTQ shard, RCCL bounce buffers, and torch caching-allocator fragmentation — then profiling asks for large SDPA temporaries. Vega20's no-VMM `invalid argument` on stressed allocations is the *known* failure signature from project memory. The standalone fill probe filled VRAM but not with the *same fragmentation pattern*. Fix: rebalance layers off radeonvii (e.g. 29/14/29 via `VLLM_PP_LAYER_PARTITION`), drop `gpu_memory_utilization` to ~0.85, tune `PYTORCH_HIP_ALLOC_CONF` (`max_split_size_mb`).

- **Bet**: It's the allocator/fragmentation interaction, not any kernel — rebalancing + headroom makes the same code pass.
- **Risk**: The under-fill probe (probe3) already passed; "fragmentation pattern" is hard to falsify cheaply, and the test costs a full relaunch.

### F7 — "Someone has seen this" (community intelligence)

The mobydick fork author and the small vllm-gfx906 community are the only people on earth running this intersection. Search ROCm/pytorch/vLLM issues for `gfx906 SDPA "invalid argument" distributed`, ask the fork author directly, and run the *negative* search ("torch 2.4 math SDPA gfx906 broken") per the riskiest-assumption protocol.

- **Bet**: This exact bug is known upstream with a workaround (env var, kernel flag, version pin).
- **Risk**: The gfx906 × vLLM-PP × GPTQ population is tiny; days of latency for likely-nothing.

### F8 — "Stop paying window costs" (fix the experiment substrate, not the bug)

The real constraint isn't the bug — it's that every experiment displaces the daily driver, which caps iteration count at ~1 per window. Invest in a zero-displacement test loop: toy-scale repro co-resident with bge on radeonvii (co-residency already proven for the fork dtype probe), scheduled night windows via automation, or accept-and-restore tooling so a window costs minutes of operator attention instead of hours.

- **Bet**: With a cheap iteration loop, even a genuinely deep bug yields; without one, even a shallow bug looks like a wall.
- **Risk**: Infrastructure work displaces actual debugging; over-engineering for a bug that one good probe might pin.

## Phase 2 — Cross-Pollinate

### C1 = F2 × F1 × F8 — "Toy repro with serialized kernels, co-resident"

The strongest combination. F2 supplies the *coverage-gap insight* (opt-125m never exercised rotary/RMSNorm/silu/GPTQ custom kernels on gfx906 — the exact untested surface). F1 supplies the *instrument* (serialized launches name the true failing kernel instead of the SDPA messenger). F8 supplies the *economics* (single-GPU toy on radeonvii can run co-resident with bge → zero daily-driver cost, minutes per iteration). Together: a free experiment that either names the failing kernel or cleanly eliminates the entire kernel-launch hypothesis class.

### C2 = F3 + F4 + F6 — "Stacked-mitigation relaunch"

If/when the next 72B relaunch happens anyway, never spend it on a single hypothesis. Stack every cheap mitigation in one window: `num_gpu_blocks_override` + chunked prefill 256 (F3), the gfx906 chunked-SDPA shim (F4), rebalanced layers + util 0.85 (F6), and serialized launches on the gfx906 rank (F1's instrument) so that even a failure produces a named kernel. One window, four mitigations, guaranteed diagnostic yield.

### Tension — diagnose-first (F1/F2) vs. patch-and-relaunch (F3/F4/F6)

The real decision axis is **cost-per-experiment**. A 72B relaunch is hours + daily-driver displacement and tests ~1 hypothesis; a toy repro is minutes, possibly zero displacement, and bisects a whole hypothesis class. Until the toy verdict exists, every 72B relaunch is a coin-flip purchased at the most expensive rate available. The patch-first instinct only wins if a window were already scheduled and the patch is free to add — which is exactly C2.

## Phase 3 — Converge

### Recommended: C1 — toy Qwen GPTQ repro with serialized kernels, then one stacked relaunch

Run the kill-test above: `Qwen2.5-0.5B-Instruct-GPTQ-Int4`, unified image, single-GPU radeonvii, `AMD_SERIALIZE_KERNEL=3`, one 2048-token prefill — co-resident with bge so it costs nothing. It closes the precise coverage gap that makes "all standalone probes pass" misleading (the probes tested torch SDPA, which was never the only suspect; they never tested the unified image's custom HIP ops or GPTQ kernels on gfx906). Escalate to 3-way toy PP only if single-GPU passes. Either branch terminates well: **reproduce** → serialized launch names the kernel → mechanical fix → one final 72B relaunch with a *proven* patch (fold in C2's mitigations); **no repro even at toy PP** → failure is scale-bound → spend the one relaunch on F6's rebalance + allocator config, again with C2 stacking.

### Runner-up: F3 — config-only profiling dodge

If a 72B window gets scheduled before the toy verdict exists, `num_gpu_blocks_override` + chunked prefill is free to add and could get straight to the throughput benchmark. What tips the choice this way: toy repro turning out *not* to be runnable co-resident (so diagnosis costs a window anyway), or the milestone deadline outweighing root-cause confidence. Do not run it *instead of* diagnosis — run it inside C2's stack.

### Open question for the operator

Can the toy Qwen GPTQ test run co-resident with bge on radeonvii under the **unified image** (the fork dtype probe proved co-residency for the *fork* image)? If yes, the recommended path is literally free and there is no reason to schedule another 72B window before the toy verdict.

## Next actions

1. Stage `Qwen2.5-0.5B-Instruct-GPTQ-Int4` (or reuse smallest staged Qwen GPTQ) on llm-models-nfs.
2. Run kill-test single-GPU on radeonvii, unified image, `AMD_SERIALIZE_KERNEL=3`, `HIP_LAUNCH_BLOCKING=1`, one 2048-token prefill. Record verdict here (Status line above).
3. Branch per verdict: kernel named → patch + C2 relaunch; toy passes → 3-way toy PP; toy PP passes → F6 rebalance relaunch with C2 stack.
4. In parallel (zero cost): F7 positive + negative searches; ping mobydick fork author.
