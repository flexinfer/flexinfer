# Research Brief: Codebase Indexer Benchmark Operability

## Problem

Recent codebase indexer work added stage-level timing, a benchmark runner, a mixed-language watch fixture, and sprint-report docs, but the harness still needed to prove it could run under the repo's default MCP output mode and produce attributable artifacts.

## Research Questions

- Q1: Is the benchmark surface actually wired end-to-end in the repo?
- Q2: What broke when we tried to use it in the current loom runtime?
- Q3: What does the first successful watch artifact say about the next optimization target?
- Q4: What should the next slice prioritize before anyone claims another performance win?

## Facts Found

### F1: The benchmark surface is in place for full, incremental, watch, and all-scenario runs

- `Makefile` defines `codebase-bench-baseline`, `codebase-bench-full`, `codebase-bench-incremental`, and `codebase-bench-watch`, all routing through `go run ./cmd/codebase-bench ...`. Source: `Makefile:682`, `Makefile:690`, `Makefile:698`, `Makefile:706`.
- The operator-facing doc matches that intent and describes the same four scenarios plus artifact output under `.loom/codebase-bench/`. Source: `docs/codebase-indexer-benchmarking.md:3`.

### F2: The indexer now exposes stage timing primitives suitable for performance attribution

- `IndexStats` and `WatchStats` include nested stage timing structs. Source: `pkg/codebase/schema/types.go:65`.
- The tracked stages include `preflight_lookup`, `delete_before_upsert`, `parse_index`, `qdrant_upsert`, and `total`. Source: `pkg/codebase/schema/types.go:104`, `pkg/codebase/schema/types.go:119`.
- The service merges those stage samples into long-running jobs for both index and watch flows. Source: `pkg/codebase/stage_stats.go:11`, `pkg/codebase/stage_stats.go:53`, `pkg/codebase/stage_stats.go:113`.

### F3: The original benchmark harness was not operable under the repo's default MCP output mode

- `cmd/codebase-bench` drives the service through MCP-style handlers such as `HandleIndexStart`, `HandleIndexPoll`, `HandleWatchStart`, and `HandleWatchPoll`. Source: `cmd/codebase-bench/main.go:223`, `cmd/codebase-bench/main.go:283`, `cmd/codebase-bench/main.go:366`.
- The repo's MCP library serializes structured tool results through `JSONResult(...)`, which defaults to TOON unless `LOOM_MCP_OUTPUT_FORMAT` is overridden. Source: `/Users/cblevins/go/pkg/mod/gitlab.flexinfer.ai/libs/mcp-go@v0.2.1-0.20260303153918-d2e5fba8ab13/server.go:805`, `/Users/cblevins/go/pkg/mod/gitlab.flexinfer.ai/libs/mcp-go@v0.2.1-0.20260303153918-d2e5fba8ab13/output_format.go:11`.
- The initial local attempt to run `make codebase-bench-baseline` failed immediately with `full decode start: invalid character 'e' looking for beginning of value`, which meant the harness was assuming plain JSON text instead of TOON or MCP error envelopes. Source: command `make codebase-bench-baseline` run on 2026-03-11 before the fix.

### F4: The harness is now fixed to handle JSON, TOON, and MCP error envelopes, and watch artifacts now carry a stable repo ID

- `decodeToolJSON(...)` now checks `res.IsError`, extracts the first text payload, tries JSON first, then falls back to `mcp.DecodeTOONToJSON(...)`. Source: `cmd/codebase-bench/main.go:418`.
- The watch scenario now derives `repo_id` through `watchRepoID(cfg)` instead of appending `-watch` to a potentially empty string. Source: `cmd/codebase-bench/main.go:298`, `cmd/codebase-bench/main.go:477`.
- Focused tests now cover TOON decoding, JSON decoding, error-envelope handling, and fixture-name repo ID fallback. Source: `cmd/codebase-bench/main_test.go:10`.

### F5: The first successful watch artifact shows Qdrant write cost dominating the measured watch path

- Command run:
  - `go run ./cmd/codebase-bench -scenario watch -runs 1 -warmup-runs 0 -timeout-seconds 120 -output-dir .loom/codebase-bench`
- Artifact:
  - `.loom/codebase-bench/codebase-bench-20260312-004421.json`
- Key measurements from that artifact:
  - End-to-end watch scenario duration: `3255ms`
  - `main.go` latency: `1251ms`
  - `src/index.ts` latency: `1251ms`
  - `watch_stats.stages.total`: `3.005s`
  - `watch_stats.stages.qdrant_upsert`: `1.419s`
  - `watch_stats.stages.preflight_lookup`: `310ms`
  - `watch_stats.stages.delete_before_upsert`: `10ms`
  - `watch_stats.stages.parse_index`: `2.2ms`
  - `watch_stats.stages.file_read`: `0.45ms`
- The stage split means parser and file-read work are not the current bottleneck on this fixture; the storage path is.

### F6: Full baseline remains too heavy or opaque for a quick planning loop

- `make codebase-bench-baseline` launches the all-scenario runner against the repo root. Source: `Makefile:682`.
- In this session, the all-scenario command stayed active for multiple minutes without producing an artifact or progress output, so it was not suitable as an interactive planning check. Source: commands `make codebase-bench-baseline` and `ps -Ao pid,etime,pcpu,pmem,command | rg 'codebase-bench|go run ./cmd/codebase-bench'` run on 2026-03-11.

## Assumptions

- The watch-only artifact is a smoke measurement, not a publishable benchmark claim, because it uses one measured run instead of the documented median-of-five rule.
- The current Qdrant target may include network or shared-cluster variance, so stage-level timings matter more than raw wall-clock totals for next-step prioritization.

## Recommendation

Treat the next codebase indexer slice as a two-part effort:

1. Make the benchmark runner operational enough to be trusted in everyday engineering loops.
2. Use the now-working watch artifact path to attack the storage-dominated hot path first, especially `qdrant_upsert`, before chasing parser or chunker micro-optimizations.

## Open Questions

- Why does the full/incremental path stay quiet for so long in interactive use: legitimate repo-scale work, missing progress reporting, or an avoidable wait on storage?
- Can watch-mode `qdrant_upsert` be reduced by batching, fewer deletes, or a different debounce/coalescing strategy without hurting correctness?
- Should the sprint report be generated from artifacts automatically instead of being filled by hand?

## Sources

- `Makefile:682`
- `Makefile:690`
- `Makefile:698`
- `Makefile:706`
- `docs/codebase-indexer-benchmarking.md:3`
- `pkg/codebase/schema/types.go:65`
- `pkg/codebase/schema/types.go:104`
- `pkg/codebase/schema/types.go:119`
- `pkg/codebase/stage_stats.go:11`
- `pkg/codebase/stage_stats.go:53`
- `pkg/codebase/stage_stats.go:113`
- `cmd/codebase-bench/main.go:223`
- `cmd/codebase-bench/main.go:283`
- `cmd/codebase-bench/main.go:366`
- `cmd/codebase-bench/main.go:418`
- `cmd/codebase-bench/main_test.go:10`
- `.loom/codebase-bench/codebase-bench-20260312-004421.json`
- Command: `make codebase-bench-baseline` (2026-03-11, pre-fix failure)
- Command: `go run ./cmd/codebase-bench -scenario watch -runs 1 -warmup-runs 0 -timeout-seconds 120 -output-dir .loom/codebase-bench` (2026-03-11)
- Command: `ps -Ao pid,etime,pcpu,pmem,command | rg 'codebase-bench|go run ./cmd/codebase-bench'` (2026-03-11)
