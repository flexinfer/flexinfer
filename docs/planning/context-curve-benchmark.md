---
title: Context-Curve Benchmark
description: Spec capsule for reporting-only long-context benchmark curves.
---

# Context-Curve Benchmark

Tracking:
- Issue: none yet
- Roadmap item: `docs/planning/next-roadmap.md`
- Owner: RALPH loop
- Status: Ready

## Goal

Operators can compare model/runtime lanes by how throughput and memory pressure
change as context grows, instead of relying on one short-context tokens/sec
number.

## Riskiest assumption + kill-test

**Load-bearing assumption**: a small fixed context ladder can expose useful
placement and promotion risk without making benchmark runs so expensive that
operators skip them.

**Kill test**: run a prototype against one existing warm model and produce one
machine-readable report with at least two context points, per-point prefill
throughput, decode throughput, elapsed time, free VRAM before/after when
available, and an explicit failure point or "not reached" marker.

**Failure mode if the assumption is wrong**: downstream controller or scheduler
work would be based on data operators do not collect, so the feature should stay
as ad hoc benchmarking docs rather than becoming a stored result contract.

**Status**: not run. The first implementation slice must be reporting-only and
must not change scheduling decisions.

## Non-Goals

- Do not change scheduler scoring, placement filters, or admission decisions.
- Do not add required CRD fields.
- Do not require 32k or 128k runs on every model; unsupported or impractical
  points should be recorded as skipped or failed with a reason.
- Do not replace the existing TPS benchmark path.

## Users / Operators

- Operators choosing between Gemma/Qwen lanes for long-context agent workloads.
- Maintainers promoting runtime images who need to see whether longer prompts
  change memory or latency risk.
- Agents preparing future scheduler work that should be backed by measured
  context behavior.

## Current Evidence

- The current benchmarker stores a single aggregate record with
  `TokensPerSecond`, completion tokens, duration, samples, batch size,
  iterations, warmup iterations, and timestamp
  (`agents/benchmarker/store.go:8`).
- The benchmarker already accepts warmup, min-duration, iterations, batch size,
  prompt, request timeout, model name, and cold-start timeout options
  (`agents/benchmarker/benchmarker.go:30`).
- `scripts/bench-model.sh` already measures prompt processing speed,
  generation speed, TTFT, and multi-turn behavior through the proxy
  (`scripts/bench-model.sh:3`).
- The Gemma4 suite already wraps `bench-model.sh` into a JSON-producing matrix
  for profiles, warm/cold state, and prompt lengths
  (`docs/dev/gemma4-benchmarking.md:8`).
- The validation matrix already treats `context_length` and runtime smoke
  timing as promotion evidence, but does not yet define a context-curve result
  schema (`.loom/60-validation-matrix.md:21`, `.loom/60-validation-matrix.md:39`).
- Lane 1's gfx906 soak remains in flight; this benchmark spec must not promote
  Radeon VII aliases or runtime images while that gate is pending
  (`.loom/ralph-gfx906-llamacpp-soak-2026-05-21.md:31`).

## Requirements

- Functional:
  - Define a context ladder with default points `2k`, `8k`, `32k`, and `128k`,
    while allowing a run to skip points that exceed the model, backend, or
    timeout budget.
  - Capture prefill throughput and decode throughput separately when the
    backend/API response exposes enough timing detail.
  - Capture free VRAM before and after each point when node telemetry is
    available.
  - Record each point as `pass`, `fail`, or `skip` with a reason.
- Operational:
  - MVP output is a report artifact and optional ConfigMap data only.
  - The benchmark can run against an existing proxy route or a direct
    port-forwarded backend.
  - A failed large-context point must not fail earlier successful points.
- Observability/status:
  - Reports should preserve per-point elapsed time, prompt tokens, completion
    tokens, request error, and free-memory samples.
  - Reports should be linkable from `.loom/60-validation-matrix.md` after live
    validation.
- Compatibility:
  - Existing benchmark ConfigMaps and scheduler consumers must keep working.
  - Scheduler scoring remains unchanged until at least two model families have
    comparable curve evidence.
- Security/RBAC:
  - No new broad RBAC in the spec slice.
  - Any later in-cluster runner must use the existing benchmarker service
    account or justify a narrower account.

## Acceptance Criteria

- [ ] The MVP command produces JSON with a stable `context_curve.points[]`
      shape.
- [ ] At least two context points are attempted against one existing model.
- [ ] Failed or skipped context points preserve the reason and do not erase
      earlier successful measurements.
- [ ] Existing one-number benchmark results continue to be emitted unchanged.
- [ ] A validation-matrix row or raw-evidence note links the first live report.
- [ ] Rollout/backout path is clear and does not require runtime promotion.

## Implementation Slices

| Slice | Target files/modules | Owner boundary | Validation | Rollback/backout |
|-------|----------------------|----------------|------------|------------------|
| 4A spec capsule | `docs/planning/context-curve-benchmark.md`, `docs/planning/next-roadmap.md`, `.loom/ralph-context-curve-benchmark-spec-2026-05-21.md` | Planning only | `git diff --check`; `rg "context-curve|Context-Curve" docs .loom` | Revert docs-only MR |
| 4B report MVP | `scripts/bench-context-curve.sh` or `cmd/flexinfer/commands`, optional benchmark docs | Reporting only; no scheduler/controller mutation | Shell dry run, JSON shape check, one live model run | Remove MVP command/script; leave existing benchmarker unchanged |
| 4C stored evidence | `agents/benchmarker`, `configmap_store`, docs, tests | Additive result schema only | Go tests for backward compatibility plus fixture report tests | Keep legacy keys; disable new writer flag |
| Later scheduler use | scheduler/controller planning docs first | Blocked until two model families have evidence | New spec and kill-test required | n/a |

## Agent Delegation Notes

| Workstream | Safe-to-edit files/modules | Do not touch | Local verification | Expected output/signals |
|------------|----------------------------|--------------|--------------------|-------------------------|
| Script MVP | `scripts/bench-context-curve.sh`, `docs/dev/*benchmark*` | `scheduler/`, `controllers/`, CRDs, runtime Dockerfiles | Dry-run; JSON fixture validation; one direct endpoint smoke when available | Report contains per-context points and preserves partial failure |
| Benchmarker storage | `agents/benchmarker/*`, `cmd/flexinfer-bench`, tests | scheduler scoring, model CRD schema | `go test ./agents/benchmarker ./cmd/flexinfer-bench` | Existing ConfigMap fields remain; curve data is additive |
| Matrix/docs evidence | `.loom/60-validation-matrix.md`, `docs/planning/context-curve-benchmark.md` | runtime promotion rows unrelated to the run | `git diff --check`; `rg "context_curve|Context-Curve"` | First live report is linked without marking scheduler changes complete |

Coordination notes:

- Shared contract: the first JSON shape should be stable enough for later
  ConfigMap storage, but the implementation should treat it as additive data.
- Merge order: spec capsule, then report MVP, then optional stored evidence.
- Conflict risks: benchmarker storage changes may overlap with dependency
  refreshes; keep the MVP script slice independent.

## Readiness

Status: Ready for Slice 4B

- Target files/modules: benchmark scripts or CLI benchmark command, benchmarker
  storage only if the MVP proves the report shape.
- Owner boundary: reporting and evidence capture, not placement.
- Validation commands: named in the slices above.
- Generated artifacts: none for the spec slice; later CLI/docs slices may add
  JSON fixtures.
- Rollout/backout: additive command/script can be reverted without changing
  live controllers, schedulers, or CRDs.
- Non-blocking open questions: which warm model should anchor the first run:
  Gemma4 26B, Qwen3 8B/14B, or the current fast-chat alias.

## Validation Plan

Run before opening the spec MR:

```bash
git diff --check
rg "context-curve|Context-Curve" docs .loom
```

First live MVP check, after Slice 4B exists:

```bash
MODEL=<existing-model> ./scripts/bench-context-curve.sh --points 2048,8192
```

## Rollout / Backout

- Rollout: merge the spec, then implement the MVP behind an explicit command or
  flag. Link the first live report from `.loom/60-validation-matrix.md`.
- Backout: revert the docs or remove the additive MVP command/script. Existing
  benchmark results and scheduler scoring remain untouched.
- Risk controls: keep the first implementation reporting-only, cap runtime by
  point, and treat unavailable VRAM telemetry as a missing field rather than a
  failed benchmark.

## Open Questions

- [ ] Should the first run target a model with a currently warm production lane
      or a direct canary where cold-start noise is easier to isolate?
- [ ] Should context-curve reports live in the global benchmark ConfigMap, a
      per-model ConfigMap, or only in raw validation artifacts for the MVP?

## Sources

- `agents/benchmarker/store.go:8`
- `agents/benchmarker/benchmarker.go:30`
- `scripts/bench-model.sh:3`
- `docs/dev/gemma4-benchmarking.md:8`
- `.loom/60-validation-matrix.md:21`
- `.loom/60-validation-matrix.md:39`
- `.loom/ralph-gfx906-llamacpp-soak-2026-05-21.md:31`
