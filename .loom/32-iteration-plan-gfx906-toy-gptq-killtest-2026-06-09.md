# Iteration plan — gfx906 toy GPTQ kill-test (F5 72B wall bisection)

**Date**: 2026-06-09 · **Session**: c751dc27ead786a2 (flexinfer/ralph-loop)
**Parent**: [.loom/brainstorm-gfx906-sdpa-distributed-prefill-2026-06-09.md](brainstorm-gfx906-sdpa-distributed-prefill-2026-06-09.md) (C1 convergence)
**Milestone**: F5 heterogeneous 72B lane — root-cause the gfx906 prefill `HIP invalid argument` that only manifests in the distributed engine.

## Scope

**In**:
- Zero-displacement probe pod on cblevins-radeonvii (hostPath /dev/kfd+/dev/dri bypass, NO amd.com/gpu claim, co-resident with bge lanes).
- Stage `Qwen/Qwen2.5-1.5B-Instruct-GPTQ-Int4` to llm-models-nfs (head_dim **128** + GQA + rotary + RMSNorm + exllama GPTQ = exact 72B op-family; 0.5B rejected — head_dim 64 matches already-passing opt-125m).
- Unified image `registry.harbor.lan/flexinfer/vllm:rocm6.3.4-multiarch`, env parity with the failing 72B run (VLLM_USE_V1=0, VLLM_USE_TRITON_FLASH_ATTN=0, AITER=0, same HIP alloc conf).
- Variant A: plain GPU executor (no Ray). Variant B: `distributed_executor_backend=ray` with world_size=1 (worker = Ray actor, no RCCL) — isolates the "SDPA-in-Ray-actor" delta.
- On failure: serialized rerun (`AMD_SERIALIZE_KERNEL=3` + `HIP_LAUNCH_BLOCKING=1`) to name the true failing kernel (async-error blame-shift check).
- Verify/close daily-driver restore gaps found in review (canary crashloop residual).

**Out**: any 72B relaunch; cross-host RCCL toy PP (needs a window — only if A and B both pass); patching vLLM; image rebuilds.

## Acceptance criteria

1. Probe produces an unambiguous verdict marker per variant: `A_PASS/A_FAIL`, `B_PASS/B_FAIL` (+ serialized-attribution output on FAIL).
2. Daily driver untouched: gemma4 primary+twin stay Ready; bge lanes stay Ready; no Model CR scaled.
3. Verdict + next-slice decision recorded in brainstorm doc (kill-test Status), agent context, and memory.
4. Canary crashloop residual resolved (deployment back to 0 replicas, per ccd64a42 intent).

## Decision table (from brainstorm C1)

| A (plain) | B (ray actor) | Verdict | Next slice |
|-----------|---------------|---------|-----------|
| FAIL | — | Engine-path bug on gfx906, Ray innocent | Serialized attribution names kernel → mechanical patch → stacked 72B relaunch |
| PASS | FAIL | Ray-actor context delta confirmed | Bisect actor env/ctx (minutes/iteration, co-resident) |
| PASS | PASS | Scale/RCCL/multi-rank-shard bound | Next 72B window = F6 rebalance + C2 stacked mitigations |

## Risk notes

- Probe is privileged + hostPath GPU access co-resident with bge: VRAM headroom 15.4GB free / util capped 0.45 (~7.2GB) — bge wake spike safe.
- HF staging from pod assumes cluster egress (downloader-job precedent); fallback = stage via hf-cache-nfs copy.
- AMD_SERIALIZE_KERNEL may mask async-timing bugs → reproduce unserialized FIRST, serialize only for attribution.

## Test plan

Probe IS the test. Daily-driver regression check = gemma4/bge phase+pod status before/after.

## Outcome (2026-06-09 — slice complete, all acceptance criteria met)

- **A1_PASS + B1_PASS** → op-set and Ray-actor hypotheses eliminated.
- Escalation variant **C (11GB fill + util 0.92) REPRODUCED** `HIP invalid argument` — no Ray, no
  RCCL, no 72B. Serialized attribution (C2): true site = `vllm/worker/cache_engine.py:82
  _allocate_kv_cache → torch.zeros` — the window's SDPA traceback was async misattribution.
- **D-matrix**: D1 (util 0.85) FAIL · D3 (fill 9) FAIL · D4 (fill 9 + util 0.85) FAIL ·
  **D5 (`num_gpu_blocks_override=256`) PASS** with coherent gen at 12.9GB resident.
- **72B relaunch config**: keep `27,26,27` + util 0.92, add `--num-gpu-blocks-override 256–512`.
- Restore gap fixed: de-registered canary `qwen3-1p7b-vllm-radeonvii` scaled back to 0 (master
  ccd64a42 intent); whisper/bge/gemma4 verified restored.
- Evidence: `.loom/local/validation/f5-3way-2026-06-09/TOY-KILLTEST-VERDICT.md` (+3 full logs).
- Zero window-hours spent; daily driver Ready throughout.
