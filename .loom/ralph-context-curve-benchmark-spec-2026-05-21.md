# RALPH: Context-Curve Benchmark Spec

Date: 2026-05-21

## Review

- Roadmap milestone: Lane 4A from the 2026-05-21 roadmap-unblock plan:
  context-curve benchmark spec capsule.
- Active blocker preserved: the `gfx906` llama.cpp 24-hour soak is still
  running and must finish before any Radeon VII alias, runtime-profile, or
  proxy-backed promotion work resumes.
- Live check during this iteration:
  - Job `gfx906-llamacpp-soak-traffic` was `Running` at age `4h53m`.
  - Pod `gfx906-llamacpp-soak-traffic-brpcf` was `2/2 Running` with `0`
    restarts.
  - Traffic log tail showed attempts `272` through `291` returning HTTP 200 at
    about `13.6-13.9 ms/token`.
- Prior decisions to preserve:
  - Long-context behavior should be measured before scheduler scoring changes.
  - Existing TPS benchmark and runtime-promotion matrix contracts stay backward
    compatible.

## Align

- Slice name: context-curve benchmark spec capsule.
- Scope in:
  - Add a public planning capsule for reporting-only context-curve benchmarks.
  - Update the public next-roadmap with CC-1 through CC-4.
  - Link the spec from the planning README and `.loom` index.
- Scope out:
  - No benchmarker code changes.
  - No scheduler/controller/CRD changes.
  - No ConfigMap schema changes yet.
  - No mutation of the in-flight `gfx906` soak.
- Acceptance criteria:
  - Spec names goal, non-goals, users, evidence, requirements, acceptance
    criteria, implementation slices, delegation boundaries, validation, and
    rollout/backout.
  - Spec includes a riskiest assumption and kill-test.
  - Roadmap has a small CC lane whose first item is complete and later items
    remain blocked behind live MVP evidence.
  - Validation commands pass.
- Dependencies/blockers:
  - First implementation slice needs one existing warm model or direct backend
    target.
  - Scheduler use remains blocked until at least two model families have
    comparable curve evidence.

## Land

- Planned file areas:
  - `docs/planning/context-curve-benchmark.md`
  - `docs/planning/next-roadmap.md`
  - `docs/planning/README.md`
  - `.loom/00-index.md`
  - `.loom/ralph-context-curve-benchmark-spec-2026-05-21.md`
- Implementation steps:
  1. Create the context-curve spec capsule.
  2. Add roadmap and planning-index links.
  3. Run docs/static validation.

## Prove

- Tests to run:
  - `git diff --check`
  - `rg "context-curve|Context-Curve" docs .loom`
- Lint/static checks:
  - `git diff --check`
- CI checks:
  - Docs-only slice; rely on MR pipeline for repository-wide checks.

## Handoff/Harvest

- Docs to update after the next implementation slice:
  - `.loom/60-validation-matrix.md` with the first live report link.
  - `docs/dev/gemma4-benchmarking.md` if the MVP reuses or extends the Gemma
    benchmark suite.
- Agent-context entries to add:
  - decision: context-curve benchmark starts as reporting-only data.
  - finding: `gfx906` soak was healthy but not complete during CC-1.
- Next-slice candidates:
  - CC-2: build `scripts/bench-context-curve.sh` or a CLI equivalent that emits
    stable JSON for `2k`/`8k` first.
  - If the `gfx906` soak finishes first: harvest soak evidence and decide
    whether to build a persistent `gfx906` runtime image with the shim.
