# Worklog

Chronological notes while executing the plan (useful for handoffs and debugging).

## 2026-02-19

- What changed:
  - Ran loom-context initialization and snapshot scripts.
  - Refreshed `.loom` docs (`00`, `10`, `20`, `30`, `40`) to current session evidence.
  - Captured MCP inventory through `loom` CLI fallback.
  - Attempted `codebase_memory` re-index twice; both failed with compatibility errors.
  - Diagnosed `codebase_memory_v1` vector schema mismatch (`size=1`) and recreated collection with `size=1536`.
  - Rebuilt `/Users/cblevins/workspace/services/loom-core/bin/mcp-codebase-memory` from source and restarted loom daemon.
  - Re-ran index successfully (`job_id=1869e8aca6a0ab14`, `chunks_total=1877`, `errors=0`).
- Why:
  - Establish a trustworthy planning baseline before further implementation work.
- What’s next:
  - Resolve direct MCP bridge `Transport closed` instability for this chat; continue using `loom tools call` fallback meanwhile.
- Sources:
  - [S1] `python /Users/cblevins/.codex/skills/plan-loom-core/scripts/init_loom_context.py --root .`
  - [S2] `python /Users/cblevins/.codex/skills/plan-loom-core/scripts/workspace_snapshot.py --root .`
  - [S3] `loom servers --json | jq '.servers | length'`
  - [S4] `loom tools list --json --limit 500 --page 1 | jq '{server,page,pageSize,totalTools,totalPages,serverCount,cachedAt}'`
  - [S5] `functions.mcp__loom__codebase_memory__codebase_index_poll({job_id:\"5380e4246b4b7cf1\"})`
  - [S6] `functions.mcp__loom__codebase_memory__codebase_index_poll({job_id:\"237b41f443376c18\"})`
  - [S7] `loom tools call qdrant__qdrant_create_collection --args '{"collection":"codebase_memory_v1","vector_size":1536,"distance":"Cosine"}' --json`
  - [S8] `go build -o /Users/cblevins/workspace/services/loom-core/bin/mcp-codebase-memory /Users/cblevins/workspace/services/loom-core/cmd/mcp-codebase-memory`
  - [S9] `loom tools call codebase_memory__codebase_index_poll --args '{"job_id":"1869e8aca6a0ab14"}' --json`

## 2026-02-20

### Qwen3-30B-A3B abliterated — cold start bug hunt and resolution

- What changed:
  - Found and fixed **three bugs** blocking serverless cold start for large models:
    1. **Controller idle timeout kills Loading models** (`controllers/model_controller.go`):
       `desiredReplicas()` checked only `time.Since(LastActiveTime) > idleTimeout` without considering Loading phase. Added `if model.Status.Phase == ModelPhaseLoading { return 1 }` guard. Commit `4fecee3`.
    2. **Proxy triggerScaleUp swallows conflict silently** (`internal/proxy/queue.go`):
       `triggerScaleUp()` returned `nil` on `errors.IsConflict`, making `processQueue` think scale-up succeeded while `LastActiveTime` remained stale. Added 3-retry loop with re-fetch. Commit `d9fc215`.
    3. **GPUGroup cold start timeout not per-model** (`internal/proxy/gpugroup.go`, prior session):
       GPUGroup queue path used only the global `queueTimeout`, ignoring per-model `ColdStartTimeoutSeconds`. Added `max(queueTimeout, getColdStartTimeout)` pattern. Commit `bc60e05`.
  - Switched model cache from **Longhorn** (`nvme-cache-1r`) to **local NVMe** (`local-path` storageClass):
    - 18.7GB GGUF loads in ~3min from local NVMe vs 15-20min through Longhorn's block layer (mmap page fault overhead).
    - Updated `examples/v1alpha2/qwen3-30b-a3b-abliterated-llamacpp-amd.yaml`.
  - Increased proxy timeouts to **25m** in `platform/gitops/k3s/ai/flexinfer/values.yaml` and pushed to gitops main.
  - Set model `idleTimeout: 20m` and `coldStartTimeout: 15m` in the example manifest.
  - All three commits pushed to `codex/issue-9-prometheus-deps-batch1` branch (later renamed `codex/issue-8-car5-fallback-proof`).

- Results:
  - Qwen3-30B-A3B abliterated responded successfully: **HTTP 200**, 108 tok/s generation, 72.5 tok/s prompt processing.
  - Total cold start time: ~598s (includes container image pull + 18.7GB GGUF mmap from local NVMe).
  - Model served via llama.cpp ROCm on AMD gfx1100 (RX 7900 XTX), Q4_K_M quantization, flash attention enabled.

- Why:
  - Qwen3-30B-A3B is the first large MoE model tested on the serverless cold start path. The 18.7GB GGUF and 10+ minute load time exposed race conditions between proxy and controller that smaller models never triggered.

- What's next:
  - Rebuild proxy and controller images to deploy the fixes to the cluster (currently running old code; manual patches were used for testing).
  - Create PR for the three fix commits.
  - Test cold start end-to-end without manual patches to validate the fixes work in production.

- Sources:
  - `controllers/model_controller.go:195-199` — Loading phase guard
  - `controllers/model_controller_test.go` — TestDesiredReplicasServerless loading case
  - `internal/proxy/queue.go:296-323` — triggerScaleUp conflict retry
  - `internal/proxy/gpugroup.go` — per-model cold start timeout
  - `examples/v1alpha2/qwen3-30b-a3b-abliterated-llamacpp-amd.yaml` — local-path cache, timeout config
  - `platform/gitops/k3s/ai/flexinfer/values.yaml` — proxy timeouts 25m
  - Cluster test: `kubectl exec curl-30b-local -- curl -s proxy:80/v1/chat/completions` → HTTP 200, 108 tok/s

### Reconciliation + Backlog Tracking Refresh

- What changed:
  - Fast-forwarded `master` to `origin/master`, confirming `fix/cold-start-reliability` was already merged (`fad43a7`).
  - Merged `origin/codex/issue-9-prometheus-deps-batch1` into `master` with clean merge commit `a16b2d1`.
  - Verified merge delta with local tests + lint before commit, then pushed `master`.
  - Updated roadmap planning docs to reflect:
    - dependency rollout progress (`prometheus`, `golang-x` complete),
    - recent feature/tech-debt closure state.
- Why:
  - Ensure local and branch-level deltas are reconciled on default branch and backlog tracking artifacts match repository state.
- Sources:
  - `git log --oneline -n 8`
  - `git merge --no-ff --no-commit origin/codex/issue-9-prometheus-deps-batch1`
  - `go test ./...`
  - `golangci-lint run -c .golangci.v2.yml`
