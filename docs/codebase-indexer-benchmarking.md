# Codebase Indexer Benchmarking

## Scenarios

- `make codebase-bench-full`
  Runs full-refresh indexing against the current repo with JSON artifacts in `.loom/codebase-bench/`.
- `make codebase-bench-incremental`
  Primes the repo once, then measures unchanged reruns so skip-path performance is visible.
- `make codebase-bench-watch`
  Uses `pkg/codebase/testdata/mixedrepo` in a temp copy and measures watch-mode update latency for one Go file and one TypeScript file.
- `make codebase-bench-baseline`
  Runs all three scenarios and emits one timestamped JSON artifact.

## Defaults

- Embeddings are disabled by default for deterministic end-to-end baselines.
- Git metadata is disabled by default for the same reason.
- The runner records wall-clock time, stage timings from `IndexStats`/`WatchStats`, and memory deltas.

## Meaningful Difference Thresholds

- End-to-end wall clock: at least 10%
- Stage wall clock: at least 15%
- Allocation or heap reduction: at least 20%
- Watch-mode latency: at least 20%

Use the median of 5 measured runs after 1 warm-up run before claiming a win.

## Notes

- Qdrant can be local or remote. The runner uses the normal codebase service
  environment variables such as `CODEBASE_QDRANT_URL` or `QDRANT_URL`.
- Cluster-backed runs are valid, but they mix parser/chunker cost with network
  and shared-cluster variance. Use the stage timings in the benchmark artifact
  to separate Qdrant time from local CPU work.
- The runner writes raw JSON artifacts so comparisons can be scripted later without scraping console output.
