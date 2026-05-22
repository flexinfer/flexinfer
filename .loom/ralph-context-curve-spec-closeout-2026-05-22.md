# RALPH: Context-Curve Spec Closeout

Date: 2026-05-22

## Review

- Roadmap milestone: Context-Curve Benchmarking from
  `docs/planning/next-roadmap.md`.
- Prior slices:
  - CC-1 spec capsule:
    `.loom/ralph-context-curve-benchmark-spec-2026-05-21.md`.
  - CC-2 runner MVP: `.loom/ralph-context-curve-runner-2026-05-22.md`.
  - CC-3 live capture:
    `.loom/ralph-context-curve-live-capture-2026-05-22.md`.
  - CC-4 ConfigMap storage:
    `.loom/ralph-context-curve-configmap-storage-2026-05-22.md`.

## Align

- Slice name: context-curve spec closeout.
- Scope in:
  - Mark the context-curve acceptance checklist complete now that CC-1 through
    CC-4 have merged.
  - Record the first-run model-selection answer: use a warm production lane.
  - Keep scheduler/controller behavior explicitly out of scope.
- Scope out:
  - No benchmark runner changes.
  - No ConfigMap schema changes.
  - No scheduler scoring or CRD work.

Acceptance criteria:

- `docs/planning/context-curve-benchmark.md` no longer carries stale unchecked
  criteria for the completed MVP.
- The first-run open question points to `gemma4-26b-a4b-gptq` and explains why
  a warm production lane was selected.
- Validation remains documentation-only: `git diff --check` and text search.

## Land

Updated `docs/planning/context-curve-benchmark.md` to tie each acceptance item
to the merged runner, live evidence, or ConfigMap storage slice.

## Prove

Validation commands:

```bash
git diff --check
rg "context-curve|Context-Curve" docs/planning/context-curve-benchmark.md .loom/ralph-context-curve-spec-closeout-2026-05-22.md
```

## Handoff

The context-curve MVP is now closed through storage. Any scheduler or
controller use remains a future spec that requires comparable live curves from
at least two model families.

Separate live follow-up: the `gfx906` llama.cpp 24 hour soak is still running
as of 2026-05-22T13:43Z and should be harvested after completion.
