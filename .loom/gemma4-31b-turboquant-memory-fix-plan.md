# Gemma4 31B TurboQuant Memory Fix Plan

Date: 2026-04-25

## Goal

Make `gemma4-31b-gptq-long` boot on a single 24 GiB gfx1100 card by removing
avoidable TurboQuant plugin residency during model construction.

## Non-Goals

- Do not promote 31B long-context serving until boot, memory, and quality gates
  pass.
- Do not rely on `gpuMemoryUtilization`; it cannot cap raw plugin tensor
  allocations.
- Do not rely on vLLM V1 CPU offload; current code/tests treat it as removed.
- Do not change the production `gemma4-31b-gptq` primary while planning or
  proving this fix.

## Working Theory

The current failure is not KV-cache size. The canary OOMs during attention
module construction, before KV allocation. The pinned upstream
`turboquant-vllm` code constructs immutable compression primitives for every
`TQ4AttentionImpl` instance:

- each layer creates `TurboQuantMSE(head_size, bits, seed=TQ4_SEED)`;
- each instance stores `self._tq4_rotation` on the target device;
- each instance also stores split `rotation.T` halves plus K/V centroids and
  boundaries on the target device.

For Gemma4 31B, that repeats across 60 layers even though the primitives are
dimension/bit/seed dependent, not layer dependent. Sharing or lazily
materializing these tensors should turn the observed ~3.57 GiB plugin overhead
into a small per-head-size cache.

## Preferred Fix

Patch `build/scripts/patch_turboquant_quantizer_gpu_qr.py` so the generated
`turboquant_vllm/vllm/tq4_backend.py` contains a module-level primitive cache.

### Patch Shape

1. Add a helper in the patched backend:
   - key: `(device.type, device.index, head_size, k_bits, v_bits, seed,
     use_pytorch_codec)`;
   - value: immutable tensors for rotation/codebooks;
   - use a lock only if vLLM constructs attention modules concurrently.
2. Materialize at most one full fp32 rotation per `(device, head_size, seed)`.
3. On ROCm with `TQ4_USE_PYTORCH_CODEC=1`, do not allocate
   `_tq4_rot_T_even` / `_tq4_rot_T_odd`; those are only needed by the fused
   Triton compress path.
4. For non-PyTorch-codec paths, create split rotation halves lazily on first
   use, then cache/share them.
5. Assign references from the shared cache to each `TQ4AttentionImpl`; do not
   clone or mutate them.
6. Add explicit logging:
   - primitive cache hit/miss;
   - head size and bit widths;
   - approximate MiB for newly materialized tensors;
   - cumulative cache residency.
7. Gate with env var `TQ4_SHARE_PRIMITIVES=1`, default enabled for the
   `gfx1100-gemma4-turboquant-experimental` profile and easy to disable for
   rollback.

## Validation Slices

### Slice 0: Patch Hygiene

- Apply the patch script to a clean checkout of
  `Alberto-Codes/turboquant-vllm@9d19b87c`.
- Confirm a second patch run is idempotent.
- Run a small Python import check that `tq4_backend.py` imports and that the
  new cache helper exists.

### Slice 1: E4B Regression

- Rebuild `runtime:rocm-gfx1100-gemma4-turboquant-experimental`.
- Run the existing E4B TurboQuant debug probe.
- Acceptance:
  - plugin still registers as `tq4_backend`;
  - prompt-format chat probe remains coherent;
  - cache logs show shared primitive reuse.

### Slice 2: 31B Boot-Only Memory Probe

- Use `gemma4-31b-gptq-long` as a strict canary/debug job, not Flux primary.
- Start with `maxModelLen: 2048`, `maxNumSeqs: 1`, `kvCacheCodec:
  turboquant`, `TQ4_SHARE_PRIMITIVES=1`, and `TQ4_USE_PYTORCH_CODEC=1`.
- Acceptance:
  - engine gets past weight construction;
  - plugin primitive residency is under 512 MiB;
  - vLLM reaches KV-cache sizing instead of `Gemma4MLP.__init__` OOM.

### Slice 3: Context Ladder

Increase context only after Slice 2 succeeds:

1. 4096 tokens
2. 8192 tokens
3. 16384 tokens

At each step record:

- model-load memory;
- available KV cache memory;
- GPU KV cache token capacity;
- cold-start time;
- first-token and decode rate if boot succeeds.

### Slice 4: Quality Gate

Only after 16K boots:

- Paris/fibonacci smoke prompt.
- 30 sequential short chat requests.
- One long prompt near target context.
- Perplexity or held-out prompt comparison against the 2048 primary within an
  agreed drift budget.

## Rollout

1. Land the patch script only.
2. Build/push an experimental runtime image.
3. Run debug jobs and update this plan with exact digests/results.
4. If boot and quality pass, re-enable `gemma4-31b-gptq-long.yaml` in a later
   MR as `minReplicas: 0`, priority 50, and promotion-state `canary-booting`.
5. Promote only after the validation gauntlet passes; keep the primary at 2048
   until then.

## Backout

- Set `TQ4_SHARE_PRIMITIVES=0` or revert the runtime image digest.
- Keep `gemma4-31b-gptq-long.yaml` out of Flux reconciliation if any boot OOM,
  quality regression, or cache mutation bug appears.
- Production `gemma4-31b-gptq` is unaffected because this work stays on the
  experimental TurboQuant image/profile.

## Risks

- Shared tensor references must remain immutable; any in-place write corrupts
  all layers.
- Lazy split-half allocation may still OOM if a non-PyTorch codec path is
  accidentally enabled on 31B.
- Reducing primitive residency may expose the next bottleneck: temporary
  attention buffers, KV cache, or quality drift from Gemma4/TurboQuant
  semantics.
- Build cycle cost is high because the runtime profile installs vLLM and
  TurboQuant from source.

## Sources

- `.loom/gemma4-31b-turboquant-closeout.md`
- `docs/dev/gemma4-31b-turboquant-24gb-oom.md`
- `build/runtime.yaml` profile `gfx1100-gemma4-turboquant-experimental`
- `build/Dockerfile.runtime` TurboQuant source install block
- `build/scripts/patch_turboquant_quantizer_gpu_qr.py`
- Upstream pinned source inspected in `/tmp/turboquant-vllm-plan`:
  - `src/turboquant_vllm/vllm/tq4_backend.py:347-392`
  - `src/turboquant_vllm/quantizer.py:93-110`
