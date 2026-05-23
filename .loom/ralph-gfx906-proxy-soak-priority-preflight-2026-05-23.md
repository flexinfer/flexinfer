# RALPH: gfx906 Proxy Soak Priority Preflight

Date: 2026-05-23

## Review

- Roadmap milestone: keep Lane 1 moving toward a proxy-backed Radeon VII
  llama.cpp soak before any alias or default fallback promotion.
- Latest live verdict: the proxy-backed soak after MR !480 failed because the
  temporary Qwen3 8B target stayed queued behind the Ready
  `qwen3-1p7b-tools-radeonvii` fallback.
- Current source state already has a debug-only
  `qwen3-8b-radeonvii-soak` target with `gpu.forcePromotion: true`.

## Align

- Slice name: proxy-soak priority preflight.
- Scope in:
  - Make the proxy soak traffic job verify that the soak endpoint can serve the
    expected target before entering the 24 hour loop.
  - Persist preflight records and a preflight-failed summary to the evidence PVC.
  - Preserve the canary-only `qwen3-8b-radeonvii-soak` target; do not change
    production priorities or aliases.
- Scope out:
  - No live 24 hour soak rerun in this source-only slice.
  - No fallback/default alias promotion.
  - No changes to the resident tool-router priority.

## Acceptance Criteria

- `deploy/debug/gfx906-llamacpp-proxy-soak.yaml` fails fast with exit `23` if
  the proxy endpoint cannot serve during the bounded preflight window.
- The JSONL evidence includes `preflight_request` records and the summary
  includes `status: preflight_failed` when the preflight fails.
- A successful preflight falls through to the existing warmup/measured soak
  loop unchanged.

## Risk Notes

- The preflight is an operator guardrail, not a scheduler policy change. It
  prevents ambiguous long-running failures, but the actual live proof still
  requires applying `deploy/debug/gfx906-llamacpp-proxy-soak-target.yaml` before
  the traffic job.

## Test Plan

- `kubectl apply --dry-run=client -f deploy/debug/gfx906-llamacpp-proxy-soak-target.yaml`
- `kubectl apply --dry-run=client -f deploy/debug/gfx906-llamacpp-proxy-soak.yaml`
- Extract embedded `proxy-soak.py`, run `python3 -m py_compile`, and verify a
  closed localhost endpoint exits `23` with `status: preflight_failed`.
- `git diff --check`

## Handoff

Next live gate:

1. Apply `deploy/debug/gfx906-llamacpp-proxy-soak-target.yaml`.
2. Run a short preflight by setting `SOAK_DURATION_SECONDS=900`.
3. Confirm `qwen3-8b-radeonvii-soak` becomes Active/Ready and the evidence PVC
   records a passing preflight.
4. Only then run the full 24 hour proxy soak.
