# RALPH Iteration Plan

## Review

- Roadmap milestone: gfx1100/gfx906 platform enhancements, Track A GPUProfile contract hardening and Track C gfx906 vLLM revive path.
- Spec section(s): `.loom/gfx1100-gfx906-next-round-plan.md` Track A/Track C; GPUProfile vLLM capability defaults in `deploy/gpuprofiles/gfx1100.yaml` and `deploy/gpuprofiles/gfx906.yaml`.
- Prior decisions to preserve: GPUProfile stays the source of truth for arch defaults; explicit Model config overrides profile defaults; gfx906 vLLM remains experimental until live canary evidence supports promotion.

## Align

- Slice name: GPUProfile vLLM defaults consumer.
- Scope in: apply `GPUProfile.spec.backends.<backend>.vllm.defaults` to missing vLLM config keys in controller-managed Deployments and runtime-managed load payloads; add regression tests and update profile comments.
- Scope out: no live canary promotion, no support-level flip, no new vLLM CLI flags, no changes to qwen3-1p7b tool-router evidence.
- Acceptance criteria: profile defaults fill `enforceEager` and `kvCacheDtype` when omitted; explicit Model config remains authoritative; deployment and runtime paths behave consistently; focused Go tests pass.
- Dependencies/blockers: none for code; live validation remains a follow-up.

## Land

- Planned file areas: `backend/gpu_compat.go`, `controllers/model_backend.go`, `controllers/model_deployment.go`, `pkg/runtime/payload.go`, focused tests, and profile comments.
- Implementation steps:
  1. Add a shared profile-default helper that only applies to vLLM-family backends.
  2. Call it from controller Deployment ModelSpec construction and runtime payload construction.
  3. Add tests for defaulting and explicit override behavior.

## Prove

- Tests to run: `go test ./backend ./pkg/runtime ./controllers`.
- Lint/static checks: `gofmt`, `git diff --check`.
- CI checks: GitLab branch pipeline after push and MR creation.

## Handoff/Harvest

- Docs to update: this iteration plan, brainstorm doc, and GPUProfile comments.
- Agent-context entries to add: profile defaults are now consumed in controller/runtime payloads.
- Next-slice candidates: run the `qwen3-1.7b-vllm-radeonvii` smoke; then decide whether to keep gfx906 vLLM experimental or promote support level.
