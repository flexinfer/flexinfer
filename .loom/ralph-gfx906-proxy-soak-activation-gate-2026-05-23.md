# RALPH: gfx906 Proxy Soak Activation Gate

Date: 2026-05-23

## Review

- Previous live verdict: `.loom/ralph-gfx906-proxy-soak-live-verdict-2026-05-23.md`.
- MR !480 fixed the cross-group idle-candidate unload path, but the live
  proxy rerun exposed a different blocker: same-group arbitration kept the
  warm `qwen3-1p7b-tools-radeonvii` fallback active over the lower-priority
  Qwen3 8B soak target.
- Existing controller semantics already provide the right operator override:
  `gpu.forcePromotion: true` bypasses Ready-first and cooldown guards for
  explicit kill-tests.

## Align

Slice name: gfx906 proxy-soak activation gate.

Scope in:

- Add a debug-only `Model/qwen3-8b-radeonvii-soak` that copies the Qwen3 8B
  llama.cpp Radeon VII config but sets `gpu.forcePromotion: true` and
  `serverless.minReplicas: 1`.
- Keep the soak target out of public LiteLLM aliases/default fallback routing.
- Retarget `deploy/debug/gfx906-llamacpp-proxy-soak.yaml` to the soak-only
  model name.
- Document the preflight/restore expectation before the 24 hour gate.

Scope out:

- No alias promotion.
- No default-chat fallback promotion.
- No permanent priority change to `qwen3-1p7b-tools-radeonvii`.
- No controller behavior change.

## Result

Source change prepared:

- `deploy/debug/gfx906-llamacpp-proxy-soak-target.yaml` creates
  `qwen3-8b-radeonvii-soak` with `gpu.forcePromotion: true`,
  `minReplicas: 1`, and only soak/debug service labels.
- `deploy/debug/gfx906-llamacpp-proxy-soak.yaml` now defaults to
  `/model/qwen3-8b-radeonvii-soak/v1/chat/completions`.

Follow-up after the first live preflight attempt:

- `gpu.forcePromotion: true` worked: `qwen3-1p7b-tools-radeonvii` moved to
  `Preempted` immediately.
- The first target version failed before proving runtime health because the
  soak-only model name produced a new cache path:
  `/models/qwen3-8b-radeonvii-soak/Qwen3-8B-Q4_K_M.gguf`.
- Runtime logs showed the shim active and the Radeon VII detected, then
  llama.cpp failed with `No such file or directory` for that GGUF path.
- The target now points at the already-proven node-local path:
  `file:///models/flexinfer-system/qwen3-8b-radeonvii/Qwen3-8B-Q4_K_M.gguf`
  with `cache.strategy: None`.
- The immediate retry exposed a runtime payload bug: `file://` sources were
  parsed into `payload.Model`, but `BuildLoadPayloadForModel` did not preserve
  that value as `payload.ModelPath`, so the runtime fell back to
  `/models/<model-name>`. The payload builder now passes file paths through
  explicitly, covered by `TestBuildLoadPayloadForModelPreservesFileSourcePath`.
- The proxy traffic script now records `warmup_failures` separately from
  measured request failures. Cold-start warmup failures remain visible in the
  evidence JSONL/summary but do not make the measured soak verdict fail.

## Riskiest Assumption

`gpu.forcePromotion: true` can hold the 8B soak target active long enough to
validate the proxy/runtime lane, and cleanup returns the warm tool-router
fallback to Ready without manual runtime repair.

## Kill-Test

Before any 24 hour run:

1. Apply `deploy/debug/gfx906-llamacpp-proxy-soak-target.yaml`.
2. Run the proxy soak traffic job for a short preflight window
   (`SOAK_DURATION_SECONDS=900`).
3. Verify:
   - `qwen3-8b-radeonvii-soak` reaches `Active`/`Ready`.
   - `qwen3-1p7b-tools-radeonvii` is intentionally preempted by the soak target.
   - No unrelated gfx906 cross-family model unloads the active soak target.
4. Delete the soak target and preflight Job/ConfigMap/PVC.
5. Verify `qwen3-1p7b-tools-radeonvii` returns to `Ready` and answers a proxy
   smoke request.

Only then rerun the full 24 hour proxy-backed soak and harvest durable PVC
evidence.

## Decision

This slice makes the next live test runnable without conflating validation with
promotion. Alias/default fallback promotion remains blocked until the 24 hour
proxy-backed soak completes cleanly with durable summary evidence.
