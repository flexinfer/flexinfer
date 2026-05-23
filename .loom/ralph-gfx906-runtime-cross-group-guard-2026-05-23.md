# RALPH: gfx906 Runtime Cross-Group Guard

Date: 2026-05-23

## Review

- Roadmap milestone: Lane 1 from `.loom/roadmap-unblock-plan-2026-05-21.md`,
  unblock `gfx906` production fallback through llama.cpp.
- Immediate blocker: the proxy-backed Qwen3 8B soak on the shimmed persistent
  runtime failed after `gonzalomo-fluxpony-imagegen` loaded over the active
  Qwen runtime session.
- Prior decisions to preserve:
  - `gfx906` production textgen substrate remains llama.cpp, not vLLM.
  - The standalone shim, standalone model-load smoke, and standalone 24h soak
    passed.
  - No `default-chat-fallback` or broad alias promotion until the proxy-backed
    runtime path passes with durable evidence.

## Align

- Slice name: persistent-runtime cross-group load guard.
- Scope in:
  - Query `/api/v1/status` before a runtime-managed Model issues a new load.
  - If another Model is the active runtime peer and is still loading or has
    recent proxy demand, defer an idle candidate instead of unloading that peer.
  - Clear stale endpoints and set `RuntimeBusy` while the candidate waits.
  - Preserve `gpu.forcePromotion=true` as the operator override for explicit
    kill-tests.
- Scope out:
  - No manifest priority reshuffle.
  - No immediate rerun of the 24h proxy soak in this source slice.
  - No fallback/default alias promotion.

## Acceptance Criteria

- A controller regression test proves an idle imagegen candidate does not POST
  `/load` over an actively loading, recently demanded Qwen runtime peer.
- The deferred candidate gets `Ready=False`, reason `RuntimeBusy`, and stale
  endpoints cleared.
- Existing duplicate-load and runtime-loading preservation tests still pass.

## Prove

- `go test ./controllers -run 'Test(ReconcileViaRuntime_(DefersIdleCandidateWhenActivePeerProtected|PreservesLoadingWhenRuntimeAlreadyLoading|SuppressesDuplicateLoadRequestsWhileLoading|RetriesLoadAfterBackoffWindow)|ShouldDeferRuntimeLoadForActivePeer_ForcePromotionBypassesGuard)'`
- `go test ./controllers`
- `go test ./...`

## Handoff

Next gate after merge:

1. Reconcile the controller image carrying this guard.
2. Rerun `deploy/debug/gfx906-llamacpp-proxy-soak.yaml`.
3. Harvest PVC evidence and update `.loom/60-validation-matrix.md`.
4. Promote `default-chat-fallback` only if the proxy soak completes without
   cross-family runtime unloads, request failures, or missing summary evidence.
