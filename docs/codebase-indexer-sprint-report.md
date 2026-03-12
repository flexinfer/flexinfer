# Codebase Indexer Sprint Report

## Scope

- Stage-level observability added to index and watch jobs
- Per-file Qdrant preflight collapsed into one lookup
- Chunker window construction reduced to content slicing instead of repeated joins
- First-class benchmark runner and fixture corpus added

## Baseline Artifact

- `.loom/codebase-bench/codebase-bench-20260312-004421.json` (`go run ./cmd/codebase-bench -scenario watch -runs 1 -warmup-runs 0 -timeout-seconds 120 -output-dir .loom/codebase-bench`)

### Watch Baseline (smoke run)

- Repo ID: `mixedrepo-watch`
- End-to-end duration: `3255ms`
- Per-file watch latency:
  - `main.go`: `1251ms`
  - `src/index.ts`: `1251ms`
- Stage timings from `watch_stats.stages`:
  - `qdrant_upsert`: `1.419s`
  - `preflight_lookup`: `310ms`
  - `delete_before_upsert`: `10ms`
  - `parse_index`: `2.2ms`
  - `file_read`: `0.45ms`
  - `total`: `3.005s`
- Initial read:
  - Qdrant upsert is the dominant cost center in the current watch path.
  - Preflight lookup is visible but clearly secondary.
  - Parser and file-read costs are negligible on the mixed-language fixture.

## Comparison Artifact

- Pending local run

## Measurement Rules

- Median of 5 measured runs after 1 warm-up run
- Meaningful wins:
  - End-to-end wall clock: 10%+
  - Stage wall clock: 15%+
  - Allocation or heap reduction: 20%+
  - Watch latency: 20%+

## Notes

- Populate this report from `.loom/codebase-bench/*.json` after running the
  benchmark targets against the active Qdrant environment.
- `make codebase-bench-baseline` was started during this planning session but did
  not finish inside a quick interactive window, so only the watch scenario is
  source-backed here for now.
- If the target is a shared cluster instead of local Qdrant, compare the stage
  timings as well as total wall clock so storage/network noise does not hide
  parser and chunker improvements.
