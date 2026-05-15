# Brainstorm: vLLM feature parity on AMD (gfx1100 + gfx906) vs Nvidia

**Date**: 2026-05-15
**Triggered by**: User scope-narrowing after the fleet-allocation brainstorm — "focus on enabling powerful vLLM features, not pivoting to llama.cpp. I want gfx1100 and gfx906 to run as close to nvidia counterparts with vllm features."
**Prior context**:
- `.loom/brainstorm-fleet-hardware-optimization-2026-05-15.md` (superseded by this scoping)
- `.loom/brainstorm-26b-5930k-decode-perf-round2-2026-05-14.md` (per-forward optimization rounds)
- `.loom/r5-ngram-spec-decode-falsified-2026-05-14.md` (spec-decode reverted)

## Current vLLM Reality (audited from repo, not memory)

| Knob | gfx1100 today | gfx906 today | Note |
|---|---|---|---|
| vLLM version (production) | `0.14.0rc0` in base, `0.17.0+rocm700` default, pinned commit `50cd5674` for experimental gemma4 | `0.17.0+rocm700` (image exists, currently disabled in prod) | Three versions in flight = drift surface |
| Engine | V0 (`VLLM_USE_V1=0`) | V0 (`VLLM_USE_V1=0`) | V1 is mandatory in 0.18+ (env var removed) |
| FlashAttention | CK FA (default in `rocm/vllm-dev` base) + AOTriton experimental ON | Off (no FA kernel for Vega20) | gfx1100 is already on FA, just not Triton FA |
| AITER | OFF — *correctly* (MI300X-only, not RDNA3) | OFF (not applicable to Vega20) | Not a lever; we'd been wrong about this |
| Piecewise CUDA graphs | Off (`enforce_eager: true` load-bearing for gemma4-MoE) | Off | V1 engine prerequisite |
| Native gemma4-MoE | Our custom Python patch (host-dispatch-bound) | n/a | The actual 5930k decode-perf bottleneck |
| INT4 kernel | `turboquant-vllm` Tq4 backend (ExllamaV2) → 72-73 tok/s | n/a | Custom plugin, our own |
| FP8 KV cache | Patch script wires `kv_cache_dtype="fp8"` but unused | n/a | Emulation path exists, unused |
| Marlin AWQ/GPTQ | No (CUDA-only inline PTX) | No | Newer vLLM has ROCm marlin variants |
| Runtime DaemonSet | Active | **Paused** (`flexinfer.ai/runtime-paused: "true"`) | Track B unblocks |

**Corrected gap analysis**: the missing pieces aren't "forgot to enable AITER." They are: **V0→V1 engine migration, piecewise graph capture, native FusedMoE Triton replacing our patch, FP8 KV emulation, version-sprawl consolidation, gfx906 vLLM in production at all.**

## Phase 1 — Diverge

### V1 — Migrate gfx1100 to vLLM V1 engine + piecewise graph capture

V1 is mandatory in vLLM 0.18+ (env var removed). Piecewise graph capture is the precise fix for the gemma4-MoE crash (`hipErrorStreamCaptureUnsupported`): it captures sub-graphs around uncapturable ops, so attention + dense FFN run captured while the MoE patch runs eager. This unblocks the optimization that round-2 R2 (CUDA graphs) couldn't reach. Likely 20-40% latency on top of vectorize + cache-nan.

- **Bet**: piecewise graphs unblock the largest single feature still on the table for gfx1100.
- **Risk**: V1 on ROCm gfx1100 had stability concerns 6+ months ago (README comment). Need validation against current 0.18 builds.

### V2 — Replace the gemma4-MoE Python patch with vLLM-native FusedMoE Triton

vLLM has `vllm.model_executor.layers.fused_moe` (Triton MoE kernel, ROCm-compatible). We wrote the patch *because* gemma4-MoE wasn't supported. That's likely no longer true on mainline. If recent vLLM registers gemma4 through `FusedMoE`, our patch becomes dead code and per-step Python dispatch goes to zero — the actual 5930k host bottleneck.

- **Bet**: upstream has caught up; retire the patch and inherit a tuned Triton kernel.
- **Risk**: gemma4-MoE may still not be in vLLM's MoE registry, or the Triton kernel may be CUDA-tuned and slow on RDNA3.

### V3 — FP8 KV cache via vLLM's emulation path

`patch_vllm_env_override_torch29.py` already wires `kv_cache_dtype="fp8"`. Even without FP8 hardware on RDNA3/Vega20, vLLM stores KV in INT8 with FP8-scale metadata, halving KV memory at minor quality cost. On 27B GPTQ currently capping ~76K KV tokens, unlocks ~150K — matches what Nvidia gets from native FP8. Feature-parity, not perf-parity.

- **Bet**: emulated FP8 KV closes a visible Nvidia gap.
- **Risk**: emulation has quality cost vs native FP8 and compute overhead; needs calibration.

### V4 — vLLM mainline upgrade cycle (consolidate to a single recent version)

Repo has three vLLM versions in flight (0.14rc0 / 0.17 / pinned 50cd5674). Each major version since 0.14 added native ROCm improvements: V1 engine, piecewise graphs, AITER for MI series, better RDNA3 paths, FusedMoE updates. Pin one canonical version on the `gfx1100` profile, build, run canary matrix, promote. Subsumes V1, V2, V3, and part of V5 if the version is recent enough.

- **Bet**: most of the feature gap is just being a few versions behind. Upgrades come bundled.
- **Risk**: each upgrade breaks our custom patches — `patch_vllm_env_override_torch29.py` has a comment "vLLM main drifted past expected hunks" already. Recurring re-patch cost is real.

### V5 — Marlin-equivalent INT4 kernels on ROCm (in turboquant-vllm or via vLLM upstream)

Current GPTQ on gfx1100 = ExllamaV2 via `turboquant-vllm` Tq4 backend = 72-73 tok/s. Marlin on CUDA = 100-130 tok/s. Active work in vLLM for `marlin_rocm`/Conch kernels. Our `turboquant-vllm` plugin is the natural home for a tuned HIP INT4 kernel. ~20-30% further perf on GPTQ if it works.

- **Bet**: closing the INT4 quantization gap is achievable in our plugin's scope.
- **Risk**: kernel writing on RDNA3 is hard (the LDS-limit comment in `runtime.yaml` documents a real constraint we already hit). Multi-week effort.

### V6 — Revive gfx906 vLLM with a deliberately-narrow feature set

Track C Path A from the existing plan. `Dockerfile.vllm-rocm-gfx906` exists, not in prod. Build on PyTorch 2.4+ ROCm 6.4 community wheels, V0 engine (V1 unproven on Vega20), FA off (no kernel for gfx906), paged attention on, prefix caching on, continuous batching on, model size cap at 14 GB (VMM limit). Feature-parity at the *engine level* — gfx906 becomes a real vLLM node, not a non-vLLM hold-out.

- **Bet**: a deliberately-narrow vLLM profile is achievable; the subset that works gets you most of the value.
- **Risk**: each feature toggle needs Vega20-specific validation. Track B (disk-pressure) gates this.

### V7 — Capability matrix in GPUProfile + auto feature gating

Today's feature flags are hand-coded per Dockerfile env vars (`VLLM_USE_V1`, `VLLM_USE_TRITON_FLASH_ATTN`, `VLLM_ROCM_USE_AITER`). Move them into GPUProfile as a typed matrix: `vllm.v1_engine: supported|experimental|unsupported`, `vllm.piecewise_graphs: ...`, `vllm.fused_moe_triton: ...`, `vllm.fp8_kv_emulation: ...`. Controller refuses to schedule a Model with a feature the node doesn't claim, auto-fills safe defaults. Track A extension.

- **Bet**: declarative capability gating is the operational unlock; without it every feature toggle is a manual-test ordeal.
- **Risk**: schema churn; must be maintained as ROCm/vLLM evolves.

### V8 — Upstream gemma4-MoE HIP kernel contribution

Deepest parity move: write the missing `fused_moe` HIP kernel for gemma4-MoE's shapes, contribute upstream. Eliminates our patch, makes gemma4 a first-class AMD citizen. Long-tail bet.

- **Bet**: only true parity is upstream parity; patch-around-it has a maintenance ceiling.
- **Risk**: months of work. Probably ~10% bandwidth, persistent.

## Phase 2 — Cross-Pollinate

### Combinations

- **V4 subsumes V1, V2, V3, V5**: a clean upgrade to a recent vLLM delivers V1 (V1 engine), V2 (FusedMoE registry if gemma4 is in), V3 (FP8 emulation), and a slice of V5 (newer ROCm marlin) — bundled. One disciplined upgrade cycle vs four parallel slices.
- **V4 + V7**: pair the upgrade with the capability matrix so new features have a declarative home. Without V7, new features become a new pile of env vars and the next upgrade is just as painful.
- **V6 + V7**: gfx906 revival *requires* the matrix because half of vLLM's features don't apply. Profile entries (`v1_engine: unsupported`, `flash_attention: unsupported`) give the controller scheduling info.
- **V8 ‖ V4**: kernel work is the same regardless of vLLM version. Start V8 in parallel with V4 as the long-tail track.

### Tensions

- **V4 (upgrade-driven) vs V1+V2+V3+V5 (feature-by-feature)**: build-vs-buy on the engineering. V4 = trust upstream, follow the river, lower per-feature cost, recurring re-patch tax. V1+ = higher control, higher per-feature cost. The drifted patch script is evidence the re-patch tax is non-trivial.
- **V6 (gfx906 vLLM) vs Track B (gfx906 disk-pressure paused)**: V6 is gated on Track B. Until disk-pressure is fixed, V6 is wasted work.
- **V8 (upstream) vs everything else**: different bet — long-tail, persistent, deep. Doesn't compete for the same week.

## Phase 3 — Converge

### Recommended: **V4 + V7** — consolidate to one recent vLLM version + GPUProfile capability matrix

This is the parity-on-features answer. V4 unlocks V1 + V2 + V3 + part of V5 in one shot. The current pain isn't "we don't know which feature to enable"; it's that three different vLLM versions are in flight and feature toggles are scattered across Dockerfile env vars. Consolidating fixes the operational drift, and a recent enough version brings native features (piecewise graphs, FusedMoE, FP8 KV emulation, newer marlin paths) for free.

V7 is the second leg because the upgrade-only path repeats the pain on every future upgrade. Encoding feature support per arch in GPUProfile turns env-var sprawl into a typed schema — the only durable way to track AMD-vs-Nvidia parity over time. Also sets up V6 (gfx906 revival) to be a profile entry change rather than another Dockerfile.

**Sequence**:
1. **V7 schema-only** first (no behavior change): define the `vllm.*` capability fields in GPUProfile v1alpha2, backfill the current state (V0, no piecewise, no FusedMoE, etc.) so the matrix is honest at t=0. Reversible.
2. **V4 sandbox**: build one canonical `gfx1100` runtime image on the chosen vLLM target version (likely 0.18.x or the latest stable), run the coherence + decode benchmark canary against all production models on a non-prod node.
3. **Flip V7 entries** as features pass canary. Each flip is a documented promotion with rollback path. V1 → flip `v1_engine: supported`. V2 → flip `fused_moe_triton: supported` and retire patch. V3 → flip `fp8_kv_emulation: supported`.
4. **V6** in parallel once Track B (disk-pressure) lands.
5. **V5 / V8** as long-tail tracks; reassess after V4+V7 settles.

### Runner-up: **V2 directly** — drop the gemma4-MoE Python patch, switch to vLLM-native FusedMoE Triton

Tipping trigger: V4 reveals the version pin is locked by something other than the gemma4 patch (e.g., CRD shape, transformers compatibility), making a clean upgrade hard. In that case the highest-EV single-feature move is V2: confirm vLLM's `FusedMoE` registry covers gemma4-MoE on a sandbox build, swap the model-executor registration, retire the patch. Cleaner than chasing the patch's residual cost forever.

### Open questions (decision-gating)

1. **Does upstream vLLM register `gemma4-MoE` through `FusedMoE` today?** Determines whether V2 is "config change" or "upstream contribution required." Answer: `git log --oneline vllm/model_executor/models/ | grep -i gemma4` against `github.com/vllm-project/vllm`.
2. **Is V1 engine stable on gfx1100 in 0.18+?** README's "V0 is more stable" comment is 6+ months stale. Decides whether V1 flip is one of the first promotions or held back. Answer: 30-min smoke test on a sandbox node — boot V1 with gemma4-26b, run the coherence gauntlet.
3. **What blocks gfx906 from V1?** If V1 works on Vega20 at V0-equivalent stability, V6 inherits a major feature uplift for free. Worth a short investigation in V6's design.
4. **What's the maintenance state of `turboquant-vllm`?** If we want V5 to ride on our plugin, who is the owner? Internal? Forked? Determines whether V5 is in our scope or a third-party dependency.

## Handoff

- **V4 + V7 chosen** → `plan-loom-core` to draft `.loom/NNN-product-spec-vllm-feature-parity-2026-05-15.md` covering: (a) GPUProfile schema extension for `vllm.*` capability block, (b) target vLLM version selection + version-consolidation plan, (c) regression canary matrix per arch, (d) flag-flip sequencing. Then `feature-dev` for V7 schema-only first (reversible, no behavior change), then V4 upgrade per arch.
- **V2 directly** → `research` to confirm upstream gemma4 + FusedMoE status in current vLLM mainline (one `git log` against vllm-project). If positive, `feature-dev` to swap model-executor registration and retire `patch_vllm_env_override_torch29.py`.
- **V6 chosen** → bundle with Track B (gfx906 disk-pressure) as gating prerequisite, then `feature-dev` to build & promote a `runtime:rocm-gfx906-vllm` profile with the feature set declared in V7.
- **V8 chosen** → long-tail track, allocate ~10% engineering bandwidth, kernel design doc + upstream issue/PR thread.
- Linked spec/plan doc (fill once it exists): `<.loom/NNN-...md>`

---

## Post-Research Findings (2026-05-15, evidence-driven plan adjustments)

Two parallel research sub-agents + one local grep answered the three decision-gating questions. The recommendation is confirmed (V4 + V7) but the sequencing and risk-shape change materially.

### Q1 — gemma4 + FusedMoE in upstream vLLM: **FOUND**

- `vllm/model_executor/models/gemma4.py` natively uses `FusedMoE` with `custom_routing_function`. Merged in **vLLM 0.19.0** via [PR #38826](https://github.com/vllm-project/vllm/pull/38826). Requires `transformers >= 5.5.0`.
- ROCm path: `is_cuda_alike()` returns true on HIP, so the Triton routing kernel is taken on AMD. BF16 path is known to work on gfx1100 (mixtral/qwen3-moe use the same FusedMoE path today).
- **Gap**: GPTQ-INT4 weight-loading through FusedMoE is the unverified piece for our actual prod workload. Related open bugs: [#38912](https://github.com/vllm-project/vllm/issues/38912) (NVFP4), [#39000](https://github.com/vllm-project/vllm/issues/39000) (MXFP4 weight-loader shape).
- **Implication**: V2 is real for BF16 gemma4 (retire patch). For GPTQ-INT4 gemma4 (our actual prod path) V2 is *contingent* — needs a sandbox smoke test on 0.19.x before committing.

### Q2 — V1 engine on gfx1100: **EXPERIMENTAL** (perf-positive features blocked by open bugs)

- [#39010](https://github.com/vllm-project/vllm/issues/39010): V1 hangs during CUDA graph capture on ROCm — `--enforce-eager` is the only workaround. This **kills V1's primary perf win** until it closes.
- [#41622](https://github.com/vllm-project/vllm/issues/41622): `hipErrorCapturedEvent` crash, V1-specific, piecewise capture path.
- [#38587](https://github.com/vllm-project/vllm/issues/38587): RCCL TP=2 init failure on gfx1100. Blocks any V1 + tensor-parallel path on RDNA3.
- V0 is removed in vLLM ≥ 0.18 — there is no "stay on V0" option past 0.17 except pinning the image at 0.17.x.
- AMD's [V1 perf tuning doc](https://rocm.docs.amd.com/en/latest/how-to/rocm-for-ai/inference-optimization/vllm-optimization.html) covers Instinct (MI300/350) only; RDNA3 is not in the matrix.
- **Implication**: V4 upgrade is *feature*-positive (V2a BF16, V3 FP8 KV emulation, native gemma4) but **not yet *perf*-positive on gfx1100** (piecewise graphs hang). Plan must default `cudagraph_mode: NONE` (eager) on gfx1100 until #39010 closes. The perf win from V1 is deferred, not delivered.

### Q3 — turboquant-vllm ownership: **External** (third-party plugin)

- Source: `https://github.com/Alberto-Codes/turboquant-vllm.git` (per `build/Dockerfile.runtime:45` and `build/Dockerfile.runtime-gfx906:69`).
- Provides *both* the Tq4 GPTQ backend (`vllm.general_plugins` entry point `tq4_backend`) *and* a KV cache codec (`FLEXINFER_EXPERIMENTAL_KV_CACHE_CODEC=turboquant`, see `build/runtime-entrypoint.sh:270-271`).
- Already deployed: `deploy/models/gemma4-e4b-turboquant.yaml` (active rollout), with a documented 24GB OOM on `gemma4-31b-turboquant` (`docs/dev/gemma4-31b-turboquant-24gb-oom.md`).
- **Implication**: V5 (Marlin-equivalent INT4 kernel work) is not in our scope to write — kernel improvements live upstream (vLLM mainline or the turboquant-vllm repo). **Drop V5 from this cycle's plan.**

### Adjusted Recommendation

Still **V4 + V7**, with corrected target and sequencing:

1. **V7 schema-only first** — add `vllm.*` capability fields to GPUProfile v1alpha2, backfill current state (V0 engine, no piecewise graphs, native FusedMoE: unknown, FP8 KV: off). No behavior change. Reversible. ~2 days.
2. **V4 target = vLLM 0.19.x** (not "0.18+"). 0.19 is the minimum that brings native gemma4 FusedMoE. Confirm transformers >= 5.5.0 compatibility.
3. **V4 sandbox build** on `gfx1100` profile only. Validate against three canaries:
   - **BF16 gemma4-26b** via native FusedMoE — proves V2a (retire patch for BF16).
   - **GPTQ-INT4 gemma4-26b** via native FusedMoE — proves or falsifies V2b (retire patch for INT4). If falsifies, keep patch but re-rebase against 0.19.x.
   - **Qwen3-14B-GPTQ** via turboquant Tq4 — proves no regression on the production-fast path.
4. **V1 engine flip** stays gated. Default `cudagraph_mode: NONE` on gfx1100. Track #39010, #41622 for closure. The capability matrix entry is `vllm.piecewise_graphs: experimental, default-off`.
5. **V3 (FP8 KV emulation)** flip is independent of V1. Validate emulation overhead against current `kv_cache_dtype: auto` perf on a non-critical model first.
6. **V5 dropped** for this cycle. Track upstream (vLLM + turboquant-vllm) instead.
7. **V6 (gfx906 vLLM)** — V0 is dead, so gfx906 must adopt V1 + eager. Bundle with Track B (disk-pressure) as gate. Capability matrix entries: `vllm.v1_engine: supported`, `vllm.piecewise_graphs: unsupported`, `vllm.flash_attention: unsupported`, `vllm.fp8_kv_emulation: unsupported`. Models capped at 14 GB (VMM).
8. **V8** stays long-tail (kernel contribution), not in this cycle.

**Net effect**: same strategic direction, but the perf payoff is split into two waves. **Wave 1** (V4 + V7 first cycle) delivers feature-parity-on-paper (V1 engine, native FusedMoE for BF16, FP8 KV emulation available, declarative capability matrix). **Wave 2** (when #39010 closes) delivers perf-parity (piecewise graphs flip to `supported`).

### Updated Open Questions

1. **Does transformers ≥ 5.5.0 work with our current GPTQModel + abliteration pipeline?** Memory says current runtime is on `transformers >= 5.0` and our quant pipeline has known transformers-version pitfalls. Worth a compatibility check before locking the V4 target.
2. **Does V1 + eager actually regress vs V0 on gfx1100?** V1 has overhead even with graphs disabled (scheduler differences, KV manager differences). Need a side-by-side benchmark, otherwise V4 ships a slower runtime in exchange for paper features.
3. **Is the `gemma4-e4b-turboquant` rollout production-active or canary-disabled?** The promotion annotation says `turboquant-canary-disabled-pending-shared-primitives-boot` on 31b-long but e4b looks live. Affects whether V4 needs to preserve the turboquant KV codec path or not.
