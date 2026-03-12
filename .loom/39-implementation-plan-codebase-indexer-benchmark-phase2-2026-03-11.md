# Implementation Plan: Codebase Indexer Benchmark Phase 2

## Goal

Turn the new codebase indexer benchmark harness into a credible engineering tool and use it to drive the next latency reduction on the measured watch path.

## Non-Goals

- Claiming full-repo benchmark wins before full and incremental artifacts are collected under the documented measurement rules.
- Rewriting the whole codebase indexing service.
- Optimizing parser or chunker code paths without evidence that they dominate the measured watch workload.

## Acceptance Criteria

- `go test ./cmd/codebase-bench` stays green.
- A bounded local smoke run produces a valid artifact without environment-specific output-format tweaks.
- The sprint report can be populated from at least one source-backed artifact instead of placeholders.
- The next optimization slice targets a measured bottleneck and defines a concrete improvement threshold.

## Current Baseline

- Benchmark targets are available through `Makefile`. Source: `Makefile:682`.
- Stage timings are available for both index and watch flows. Source: `pkg/codebase/schema/types.go:65`, `pkg/codebase/stage_stats.go:11`.
- The current watch smoke artifact is `.loom/codebase-bench/codebase-bench-20260312-004421.json`.
- That artifact shows:
  - total stage time: `3.005s`
  - `qdrant_upsert`: `1.419s`
  - `preflight_lookup`: `310ms`
  - `parse_index`: `2.2ms`
  - file latencies: `1251ms` for both `main.go` and `src/index.ts`

## Workstreams

### 1. Benchmark Operability

Objective:
- Make the harness practical for local iteration, not just one-off manual runs.

Tasks:
- Add progress output or per-scenario milestone logging so long-running all-scenario runs are observable.
- Add or document a smoke path (`watch`, single run, no warmup) as the default quick validation loop.
- Consider writing per-scenario artifacts before the whole `all` run completes, so partial data is not lost.

Exit criteria:
- Engineers can tell whether a long run is making forward progress.
- A quick benchmark check fits inside an interactive session without guesswork.

### 2. Reporting Automation

Objective:
- Remove the current manual gap between raw JSON artifacts and the sprint report.

Tasks:
- Add a small report generator or updater that summarizes the latest artifact into `docs/codebase-indexer-sprint-report.md`.
- Include stage percentages and wall-clock summaries so the bottleneck is obvious.
- Preserve the existing threshold language from `docs/codebase-indexer-benchmarking.md`.

Exit criteria:
- `docs/codebase-indexer-sprint-report.md` can be regenerated from artifacts instead of hand-edited placeholders.

### 3. Watch-Path Latency Reduction

Objective:
- Reduce the measured dominant cost center before chasing smaller optimizations.

Tasks:
- Inspect the watch-mode Qdrant write path and batch behavior around `qdrant_upsert`.
- Measure whether `delete_before_upsert` or preflight lookups can be reduced further without correctness regressions.
- Keep the mixed-language watch fixture as the first regression target because it exercises both Go and TypeScript edits.

Target:
- Reduce watch-mode `qdrant_upsert` by at least 20% from the current `1.419s` smoke baseline on the same fixture and environment.

Exit criteria:
- Updated artifact shows a meaningful `qdrant_upsert` reduction under the repo's documented thresholds.
- Sprint report captures before/after artifacts and stage deltas.

## Validation Plan

- `go test ./cmd/codebase-bench`
- `go run ./cmd/codebase-bench -scenario watch -runs 1 -warmup-runs 0 -timeout-seconds 120 -output-dir .loom/codebase-bench`
- After operability improvements land:
  - `make codebase-bench-baseline`
  - compare generated artifacts using median-of-five measured runs

## Risks

- Shared-cluster or remote Qdrant variance can hide smaller code wins.
- Improving the all-scenario runtime without progress output may still feel hung to operators.
- Chasing parser/chunker work first risks optimizing the wrong stage, because the current watch artifact shows storage dominating.

## Sources

- `Makefile:682`
- `docs/codebase-indexer-benchmarking.md:3`
- `docs/codebase-indexer-sprint-report.md:10`
- `pkg/codebase/schema/types.go:65`
- `pkg/codebase/stage_stats.go:11`
- `cmd/codebase-bench/main.go:223`
- `cmd/codebase-bench/main.go:283`
- `cmd/codebase-bench/main.go:418`
- `cmd/codebase-bench/main_test.go:10`
- `.loom/codebase-bench/codebase-bench-20260312-004421.json`
