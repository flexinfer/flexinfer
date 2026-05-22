# RALPH: Context-Curve Runner MVP

Date: 2026-05-22

## Review

- Prior slice: MR !471 added `docs/planning/context-curve-benchmark.md` and
  marked CC-1 complete.
- Current roadmap item: CC-2, a reporting-only context-curve runner that emits
  stable JSON without changing scheduler decisions.
- Live gate preserved: the `gfx906` llama.cpp soak was still running during
  selection, at age `6h34m`; traffic attempts `352` through `391` returned
  HTTP 200 at about `13.7 ms/token`. No Radeon VII alias or runtime promotion
  belongs in this slice.

## Align

- Slice name: context-curve runner MVP.
- Scope in:
  - Add `scripts/bench-context-curve.sh`.
  - Add developer docs for dry-run, proxy, direct-backend, and optional VRAM
    sampling usage.
  - Mark CC-2 complete in `docs/planning/next-roadmap.md`.
- Scope out:
  - No scheduler scoring.
  - No controller or CRD changes.
  - No benchmark ConfigMap writer changes.
  - No live cluster context-curve run; CC-3 owns first live evidence.
- Acceptance criteria:
  - Dry-run creates JSON with `schema_version:
    flexinfer.context_curve.v1`.
  - Report contains `context_curve.points[]`.
  - At least two dry-run points are represented as `skip` with
    `reason: dry_run`.
  - `git diff --check` passes.

## Land

- Planned file areas:
  - `scripts/bench-context-curve.sh`
  - `docs/dev/context-curve-benchmarking.md`
  - `docs/planning/context-curve-benchmark.md`
  - `docs/planning/next-roadmap.md`
  - `docs/planning/README.md`
- Implementation steps:
  1. Add an additive script that runs point-by-point requests and preserves
     partial evidence.
  2. Document operator usage and report shape.
  3. Validate dry-run and JSON shape locally.

## Prove

- Tests to run:
  - `REPORT_DIR="$(mktemp -d)" ./scripts/bench-context-curve.sh --dry-run --points 2k,8k`
  - `python3 -m json.tool "$REPORT_DIR"/bench-context-curve-*.json >/dev/null`
  - `python3 - <<'PY' ... assert schema_version and two skipped dry-run points ... PY`
  - `git diff --check`
- CI checks:
  - MR pipeline; this slice is script/docs only.

## Handoff/Harvest

- Docs to update after CC-3:
  - `.loom/60-validation-matrix.md` with the first live report link and
    observed pass/fail/skipped points.
- Next-slice candidates:
  - CC-3: run the script against one existing warm Gemma4 or Qwen lane with
    points `2048,8192`, then link the raw report.
  - If the `gfx906` soak completes first: harvest the soak logs and update the
    validation matrix before starting live context-curve evidence.
