# RALPH iteration — controller event-watch fix

**Loop**: roadmap-spec-ralph-loop
**Date**: 2026-05-19
**Prior slice**: !440 `chore(asr): tear down whisper-kill-test-v2` — merged in `10082bba`
**Driver**: Two reproducible controller bugs documented in v1/v2 kill-test evidence (.loom/asr-diarization-kill-test-*-inconclusive-2026-05-19.md) requiring manual `flexinfer.ai/force-reconcile` annotation workarounds. Operator toil + silent-stuck-Model risk.

## Scope (in)
1. Extend startup-sweep at `controllers/model_controller.go:504-541` so brand-new Models with empty `status.phase` are re-enqueued after controller restart.
2. Add a sibling-fan-out Watch on `Model` so that when a Model in a shared group is **deleted**, every other Model in the same shared group gets a reconcile request. This clears stale `status.sharedGroup.preemptedBy` automatically.
3. Unit tests for both behaviors (extend `controllers/model_runtime_test.go` pattern).

## Scope (out)
- v3 Whisper kill-test Model CR (queued as follow-up slice).
- Create-event fan-out (separate concern; deferred unless tests show convergence still unreliable).
- `flexinfer.ai/pause: "true"` annotation handler (separate ask).
- Refactoring `chooseSharedGroupLeader`.
- Cluster-side validation. CI green is the kill-test for this slice.

## Acceptance criteria
- `Start()` re-enqueues Models whose `Status.Phase` is empty OR in `{Loading, Pending}`.
- New `requestsForSharedGroupSiblings` map func enqueues all siblings of a deleted Model that share `spec.gpu.shared`, excluding the deleted Model itself.
- `SetupWithManager` registers the new watch with delete-only predicates.
- Unit tests cover: (a) empty-Phase Model is swept; (b) sibling fanout returns expected requests on delete of a group member; (c) sibling fanout returns empty when the deleted Model has no `gpu.shared`.
- `go test ./controllers/...` is green locally; `make lint` clean.
- MR open, CI green, merged.

## Risk notes
- `For(&Model{})` and additional `Watches(&Model{}, ...)` on the same type: controller-runtime supports this and dedupes by NamespacedName via the workqueue.
- Sweep `Update()` on annotation may conflict with concurrent reconcile writes. Existing code uses `r.Update(ctx, m)` for the annotation poke; we follow the same pattern. Risk is unchanged.
- Delete-only predicate avoids reconcile amplification on busy clusters.

## Test plan
- Local: `go test ./controllers/...` (full package). New tests:
  - `TestStartupSweep_SweepsEmptyPhase`
  - `TestRequestsForSharedGroupSiblings_FansOutToSiblings`
  - `TestRequestsForSharedGroupSiblings_NoFanoutForNonSharedDelete`
- CI: rely on existing pipeline (Go test + lint stages).

## Dependency/blocker map
- No blockers. Branch worktree already allocated (`claude/dazzling-morse-3d3a0f`).
- Follow-up slice (v3 kill-test) is *unblocked* by this fix but does not require it (manual annotation workaround still works as fallback).

## Riskiest assumption + kill-test
**Load-bearing assumption**: controller-runtime allows `For(&Model{})` and an additional `Watches(&Model{}, ...)` on the same type with predicates, and the workqueue dedupes duplicate `ctrl.Request` enqueues by NamespacedName so the fan-out can't amplify reconciles for non-deleted events.

**Kill test**: New unit test `TestRequestsForSharedGroupSiblings_FansOutToSiblings` exercises the map func directly with a fake client and a synthetic shared-group Model set. If the map func returns the expected sibling set, the public API contract is satisfied. (controller-runtime workqueue dedup is library-guaranteed and out of scope for our test.)

**Failure mode if the assumption is wrong**: duplicate watch panics on controller start (caught by `make build` / `flexinfer-controller-manager` pod CrashLoopBackoff on next deploy) — fast feedback at deploy time, not a silent runtime bug. Recovery: revert MR.

**Status**: not run
