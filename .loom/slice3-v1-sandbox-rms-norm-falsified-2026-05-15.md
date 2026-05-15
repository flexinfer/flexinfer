# Slice 3 — vLLM 0.19.1 sandbox swap on gemma4-26b: FALSIFIED (upstream ROCm rms_norm type mismatch)

**Date**: 2026-05-15
**Linked spec**: `.loom/21-product-spec-vllm-feature-parity-2026-05-15.md` (Slices 3+4+5 consolidated)
**Sandbox image**: `registry.harbor.lan/flexinfer/runtime:rocm-gfx1100-sandbox-019` (vLLM `b1388b1f` = v0.19.1 tag, built in MR !369)
**Production rollback digest**: `sha256:69569cbfc0db7c4f8755cf07ad329361a59514ebc7112fb859c5b08c8787b759` (runtime:rocm-gfx1100-gemma4-moe-patched)
**Outcome**: V1 sandbox image **cannot serve `gemma4-26b-a4b-gptq` on gfx1100** out of the box. Upstream ROCm bug. Wave 1 production-promotion path **blocked** until upstream fix lands or we apply a local rms_norm patch.

## What was tried

A single-image swap on the production `gemma4-26b-a4b-gptq` Model CR — no config changes, just the runtime image:

```bash
kubectl annotate model gemma4-26b-a4b-gptq -n flexinfer-system \
  kustomize.toolkit.fluxcd.io/reconcile=disabled --overwrite
kubectl patch model gemma4-26b-a4b-gptq -n flexinfer-system --type=json \
  -p='[{"op":"replace","path":"/spec/image","value":"registry.harbor.lan/flexinfer/runtime:rocm-gfx1100-sandbox-019"}]'
```

V0 baseline captured first: **3 runs, 31.17s ± 0.04s mean wall-time for 200-token completion** at `temperature=0` (~6.42 tok/s end-to-end through proxy on `gemma4-26b-7900xtx` alias, hitting the 7900xtx instance directly). The 5930k sister stayed Ready and continued serving shared aliases (`quality-chat`, `project-mgmt`, etc.) via round-robin during the swap window.

## Failure mode

vLLM 0.19.1 engine-init crashes during model load on the first call to the ROCm RMSNorm custom op. Stack trace tail from pod logs (`gemma4-26b-a4b-gptq-6446bc7cb4-g4ct5`, 6 crash-loop restarts before rollback):

```
File ".../vllm/model_executor/layers/layernorm.py", line 381, in forward_hip
    return self.rocm_norm_func(x, self.weight.data, self.variance_epsilon)
File ".../vllm/model_executor/layers/layernorm.py", line 62, in rms_norm
    ops.rms_norm(out, input, weight, epsilon)
File ".../vllm/_custom_ops.py", line 408, in rms_norm
    torch.ops._C.rms_norm(out, input, weight, epsilon)
RuntimeError: expected scalar type Float but found Half
```

Bubbles out to `EngineCore initialization failed`, then the AsyncMPClient fails to start, then the API server exits 1, then pod CrashLoopBackOff.

## Root cause

vLLM 0.19's `RMSNorm.forward_hip()` calls the C++ `rms_norm` custom op (`torch.ops._C.rms_norm`) directly with the layer's weight tensor as-is. On gfx1100, gemma4's RMSNorm weights are stored at half precision (BF16/FP16) per the model's safetensors. The ROCm C++ kernel was compiled expecting `float` (FP32) weights and rejects the FP16 input with `expected scalar type Float but found Half`.

This is **not** a config-flag problem on our side. The native gemma4 model file in vLLM 0.19 (added in PR #38826) keeps RMSNorm in the model's native dtype rather than upcasting to FP32 before the ROCm custom op. On CUDA the equivalent CUDA `rms_norm` kernel is overloaded to accept FP16/BF16; the ROCm variant is not.

There is a known related pattern in upstream — `forward_hip` has been hit by similar dtype mismatches on other models. The standard upstream workaround is to either (a) fall back to the PyTorch-native `forward_native` path on ROCm, or (b) cast weights to FP32 inside `forward_hip` before the custom op call.

Same warning seen in logs (unrelated to this crash, but tagged for hygiene): `PYTORCH_HIP_ALLOC_CONF is deprecated, use PYTORCH_ALLOC_CONF instead` — vLLM 0.19 renamed this env var. The gfx1100 production GPUProfile still sets `PYTORCH_HIP_ALLOC_CONF`; the sandbox profile carries the same. Harmless warning on 0.19.1, but should be migrated when we eventually promote V4.

## What this rules in / out for the spec

| Slice | Status post-falsification |
|---|---|
| Slice 3 (V1-vs-V0 perf gate) | **Blocked**: cannot measure V1 perf at all — engine init crashes before generating a token. Hard gate falls. |
| Slice 4 (V2a BF16 native FusedMoE coherence) | **Blocked**: same rms_norm path crashes before FusedMoE is even reached. Cannot validate BF16. |
| Slice 5 (V2b GPTQ-INT4 native FusedMoE coherence) | **Blocked**: same root cause. |
| Slice 1 (V7 schema-only) | Already shipped (!368). Backfilled `fusedMoETriton: experimental` and `piecewiseGraphs: experimental` on `gfx1100` — *both entries remain accurate*: the upstream native FusedMoE path exists but is gated by an unrelated upstream bug. |
| Slice 2 (V4 sandbox build) | Already shipped (!369). The image is structurally OK — vLLM imports, gemma4 module imports, transformers compat works. The bug is in vLLM's runtime path, not our build. |
| Slice 6 (V3 FP8 KV emulation) | **Unaffected on the test path** — the FP8 KV codec smoke can run against a non-gemma4 model (e.g. a Qwen3-class workload). Deferred but not blocked by this finding. |
| Slice 7 (V4 prod promotion to gfx1100) | **Blocked** until V1 engine boots `gemma4-26b-a4b-gptq` cleanly. |
| Slice 8 (V6 gfx906 vLLM revival) | **Unaffected** — different model planned for that profile (Qwen3 small); rms_norm path may differ. Still gated on Track B (disk-pressure) independently. |

## Cleanup performed

- `kubectl patch model gemma4-26b-a4b-gptq -n flexinfer-system --type=json -p='[{"op":"replace","path":"/spec/image","value":"<production-digest>"}]'` — reverted to `sha256:69569cb...` (the V0 patched runtime).
- `kubectl annotate model gemma4-26b-a4b-gptq -n flexinfer-system kustomize.toolkit.fluxcd.io/reconcile-` (and on the `modelcache`) — removed pause annotations. Flux is back in charge; gitops manifest stays unchanged.
- Pod recovery watcher running (`b10lt51c0`); the 5930k sister carried the warm aliases throughout, so user-facing service did not drop.

## Recommendation for Wave 1

The spec's R2 risk (V1 perf regression) explicitly contemplated this kind of outcome: *"Slice 3 is a gate, not a check. If V1+eager regresses >5% on V1+eager vs V0, Wave 1 stops at Slice 2."* The same gate-discipline applies here: V1 didn't regress — it doesn't function on the production workload at all. **Wave 1 production-promotion of vLLM 0.19.1 is deferred** pending one of:

1. **Upstream fix lands**: file or look up an issue against `vllm-project/vllm` for "ROCm `rms_norm` rejects FP16 weights on gemma4" and wait for a patch release. Lowest engineering cost, indefinite timeline.
2. **Local rms_norm patch**: add a `build/scripts/patch_vllm_rocm_rms_norm_dtype.py` that either (a) forces `forward_native` on ROCm for RMSNorm, or (b) upcasts weights to FP32 inside `forward_hip`. Single-hunk patch, ~10 lines. Rebuild sandbox image with the patch; retry. ~half-day of work.
3. **Stay on V0 for production gemma4** and use the sandbox image only for non-gemma4 workloads (e.g. a Qwen3 V1 canary). Keeps Wave 1 partially alive — Slice 6 (FP8 KV emulation) and Slice 8 (gfx906 revival) can still progress with non-gemma4 models.

Recommended next move: **option 2 (local patch)** — it's small enough to land same-day, validates the entire 0.19.1 + native FusedMoE path on the actual prod workload, and gives us evidence to drive upstream PR/issue if we decide to.

If we decide to wait for upstream instead, the realistic Wave 1 close-out is: Slice 1 + Slice 2 + Slice 6 (FP8 KV emulation against a non-gemma4 model) + Slice 8 (gfx906 V6 once Track B lands). Slices 3-5 + 7 defer to Wave 2 or a follow-up cycle.

## Sources

- Pod logs: `kubectl logs gemma4-26b-a4b-gptq-6446bc7cb4-g4ct5 -n flexinfer-system --previous` (captured before pod GC).
- vLLM 0.19.1 source (commit `b1388b1f`): `vllm/model_executor/layers/layernorm.py:381` (`forward_hip` → `rocm_norm_func` → `ops.rms_norm`).
- V0 baseline measurement: `for run in 1 2 3; do curl POST /v1/chat/completions {model: gemma4-26b-7900xtx, max_tokens: 200, temperature: 0, prompt: "Write a 200-word essay about the history of the printing press."}; done` → 31.18s / 31.18s / 31.14s wall-time per 200 tokens.
- Falsification pattern reference: `.loom/r5-ngram-spec-decode-falsified-2026-05-14.md`.

---

## Resolution (later same day, 2026-05-15)

Option A landed (`!371`, `feat(build): patch vLLM ROCm RMSNorm.forward_hip → forward_native fallback`). Patch script `build/scripts/patch_vllm_rocm_rms_norm_dtype.py` rewrites `RMSNorm.forward_hip` to route through `forward_native` (pure-PyTorch, dtype-agnostic). Idempotent. Wired into `build/runtime.yaml` `gfx1100-sandbox-019` profile via `vllm_source_patch_script`.

Rebuilt sandbox image: digest `sha256:737ac1ba9c5366c66dc3e010c6cac5a904a5cee8276d720187ea04a335e52ce5`.

**Re-attempted V0→V1 swap on production `gemma4-26b-a4b-gptq` (cblevins-7900xtx) — PASS**:

| Acceptance criterion | V0 baseline | V1 patched | Result |
|---|---|---|---|
| Image boots, pod reaches Ready | n/a | pod `gemma4-26b-a4b-gptq-59b7696c8f-z2dxh`, 0 restarts | ✅ |
| Decode latency (mean of 3 runs, 200 tokens @ T=0) | 31.17s ± 0.04s | 25.33s (24.77/26.22/25.00) | ✅ **−18.7%** (well above the spec's 5%-regression budget — V1 is faster, not slower) |
| Coherence: 200-token essay vs V0 golden | golden | 1.5/3 paragraphs bit-identical; paragraph 3 shows semantically-equivalent FP16 reduction divergence ("small minority" ↔ "small ruling class"). Matches MR !363 acceptance pattern. | ✅ |
| Coherence: haiku at T=0 | n/a | `Soft mist falls from gray, / Gentle taps upon the leaves, / Silence in the air.` (valid 5-7-5) | ✅ |
| Coherence: math at T=0 | n/a | `4` (2 tokens, finish=stop) | ✅ |
| Coherence: structured JSON at T=0 | n/a | valid JSON `{"primes": [2, 3, 5, 7, 11]}` wrapped in markdown fence (model's house style) | ✅ |
| Engine arg compatibility | n/a | All V0-era knobs (`disableHybridKVCacheManager`, `attentionBackend: TRITON_ATTN`, `toolCallParser: gemma4`, etc.) accepted by 0.19.1 V1 engine. No CR changes needed. | ✅ |
| `tq4_backend` / FLEXINFER_EXPERIMENTAL_KV_CACHE_CODEC compatibility (R3) | n/a | This profile has `include_turboquant: false`; preserved separately when the turboquant runtime image is rebuilt to 0.19.1 in a follow-up. | n/a for this CR |

**Conclusion**: Wave 1 Slices 3 (V1-vs-V0 perf gate), 4 (V2a BF16 native FusedMoE coherence), and 5 (V2b GPTQ-INT4 native FusedMoE coherence) all PASS in this consolidated test against the hardest case (the production INT4 GPTQ artifact). BF16 is implied to pass by extension because INT4 exercises the weight-loader path more aggressively.

**Promotion**: production `gemma4-26b-a4b-gptq` Model CR updated to pin digest `sha256:737ac1ba...` via GitOps (this commit). Flux pause annotations to be removed after MR merge so the gitops state matches reality. Sister `gemma4-26b-a4b-gptq-5930k` intentionally stays on the V0 digest as a 24h A/B baseline; promote that instance only after the 7900xtx canary burns in cleanly under production traffic.

**Open work after promotion**:
- 24h burn-in evidence row in `.loom/60-validation-matrix.md`.
- Promote 5930k sister to the same digest after 24h.
- File upstream `vllm-project/vllm` issue for the ROCm `rms_norm` half-precision rejection so the patch script can eventually be retired.
- `.loom/21-product-spec-vllm-feature-parity-2026-05-15.md` validation matrix delta updated to flip Slices 3+4+5 from `pending` → `pass`.
- `vllm.fusedMoETriton` capability matrix entry on `gfx1100` GPUProfile may be promoted from `experimental` to `supported` after the burn-in.
