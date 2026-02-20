# Decisions

Record decisions as they are made, with date, rationale, and sources.

### 2026-02-19: Use CLI fallback for MCP inventory

- Decision:
  - Use `loom servers` and `loom tools list` CLI commands as primary inventory source for this run.
- Rationale:
  - MCP resource discovery APIs returned empty collections, so loom-resource mode was not available.
- Alternatives considered:
  - Continue trying `loom://` resource reads without fallback.
- Consequences:
  - Inventory is still complete enough for planning (`42` servers, `445` tools), but provenance is command-output based.
- Sources:
  - [S1] `functions.list_mcp_resources({})` -> `resources: []`
  - [S2] `functions.list_mcp_resource_templates({})` -> `resourceTemplates: []`
  - [S3] `loom tools list --json --limit 500 --page 1 | jq '{server,page,pageSize,totalTools,totalPages,serverCount,cachedAt}'`

### 2026-02-19: Defer semantic-index reliance until `codebase_memory` is repaired

- Decision:
  - Treat `codebase_memory` semantic indexing/search as degraded and do not make it a prerequisite for planning execution.
- Rationale:
  - Index jobs failed with schema/id compatibility errors; follow-up stats calls returned transport errors.
- Alternatives considered:
  - Continue retrying index jobs in this thread without first resolving Qdrant compatibility.
- Consequences:
  - Use shell-native code discovery (`rg`, direct file reads) until index health checks pass.
- Sources:
  - [S1] `functions.mcp__loom__codebase_memory__codebase_index_poll({job_id:\"5380e4246b4b7cf1\"})`
  - [S2] `functions.mcp__loom__codebase_memory__codebase_index_poll({job_id:\"237b41f443376c18\"})`
  - [S3] `functions.mcp__loom__codebase_memory__codebase_stats({repo_id:\"flexinfer\"})` -> `total_chunks: 0`

### 2026-02-19: Recover indexing via collection recreate + binary rebuild

- Decision:
  - Recreate `codebase_memory_v1` at vector size `1536`, rebuild `mcp-codebase-memory`, restart daemon, and validate indexing via `loom tools call`.
- Rationale:
  - Collection schema mismatch and point-ID validation errors persisted; source code already contained UUID point-ID conversion, indicating stale binary.
- Alternatives considered:
  - Keep retrying index without changing collection/binary.
  - Switch to a new collection name via environment override.
- Consequences:
  - Indexing for `repo_id=flexinfer` is now healthy (`chunks_total: 1877`), but this chat's direct MCP bridge remains unstable.
- Sources:
  - [S1] `loom tools call qdrant__qdrant_get_collection --args '{"collection":"codebase_memory_v1"}' --json`
  - [S2] `go build -o /Users/cblevins/workspace/services/loom-core/bin/mcp-codebase-memory /Users/cblevins/workspace/services/loom-core/cmd/mcp-codebase-memory`
  - [S3] `loom tools call codebase_memory__codebase_index_poll --args '{"job_id":"1869e8aca6a0ab14"}' --json`

### 2026-02-20: Guard Loading phase against idle timeout reaping

- Decision:
  - Add `if model.Status.Phase == ModelPhaseLoading { return 1 }` to `desiredReplicas()` before the idle timeout check.
- Rationale:
  - The proxy sets `LastActiveTime` once at cold start request arrival. For large models (18GB+ GGUF), loading exceeds `idleTimeout`, causing the controller to scale the pod to zero mid-load. The Loading phase is authoritative evidence that the model is actively working.
- Alternatives considered:
  - Periodically refresh `LastActiveTime` from the proxy during queue wait. Rejected: adds proxy-controller coupling and still requires the proxy to know load progress.
  - Always set `idleTimeout > coldStartTimeout`. Rejected: fragile configuration dependency; Loading guard is unconditional.
- Consequences:
  - Models in Loading phase are never reaped regardless of `LastActiveTime` staleness.
  - If a model gets stuck in Loading indefinitely, it will not be automatically cleaned up by idle timeout. Separate health/liveness checks cover this case.
- Sources:
  - `controllers/model_controller.go:195-199`
  - `controllers/model_controller_test.go` — TestDesiredReplicasServerless loading case
  - Commit `4fecee3`

### 2026-02-20: Retry conflict in triggerScaleUp instead of silent nil return

- Decision:
  - Replace `return nil` on `errors.IsConflict` in `triggerScaleUp()` v1alpha2 path with a 3-retry loop that re-fetches the Model before each retry.
- Rationale:
  - The controller concurrently updates Model status (phase transitions, metrics). A single conflict causes the proxy to silently believe scale-up succeeded while `LastActiveTime` remains stale, resulting in the model never scaling up.
- Alternatives considered:
  - Using `client.Patch` with MergePatch instead of full status Update. Rejected: the Kubernetes status subresource still requires resourceVersion matching; conflicts are inherent.
  - Single retry. Rejected: triple-write scenarios (proxy + controller + scheduler) can produce back-to-back conflicts.
- Consequences:
  - Cold start scale-up is more reliable under concurrent controller activity.
  - Adds at most 2 extra API calls on conflict (re-fetch + update per retry), negligible overhead.
- Sources:
  - `internal/proxy/queue.go:296-323`
  - Commit `d9fc215`

### 2026-02-20: Use local-path storageClass for large GGUF model caches

- Decision:
  - Use `storageClass: local-path` (K3s local provisioner, direct NVMe) instead of `nvme-cache-1r` (Longhorn 1-replica) for models with GGUF files >10GB.
- Rationale:
  - llama.cpp loads GGUF via `mmap(2)`. Longhorn's block storage layer adds per-page-fault overhead, turning an 18.7GB load into 15-20 minutes. Local NVMe with `local-path` bypasses this, loading in ~3 minutes.
- Alternatives considered:
  - Longhorn with `readWriteOnce` and `numberOfReplicas: 1`. Already tested (`nvme-cache-1r`); mmap overhead persists regardless of replica count.
  - Pre-loading into tmpfs/hugepages. Rejected: requires 2x memory (weights + KV cache).
- Consequences:
  - Model cache is not replicated. If the NVMe disk fails, the cache must be re-downloaded. Acceptable for hot-spare inference models.
  - Cache PVC is tied to a specific node via `nodeSelector`; this is already the case for GPU-bound models.
- Sources:
  - `examples/v1alpha2/qwen3-30b-a3b-abliterated-llamacpp-amd.yaml:22-27`
  - Cluster test: 598s total cold start (including image pull) vs previous 15-20min load-only through Longhorn

### 2026-02-20: Set proxy timeouts to 25m for worst-case cold start

- Decision:
  - Set `proxy.timeouts.queue` and `proxy.timeouts.coldStart` to `25m` in Helm values.
- Rationale:
  - Even with local NVMe, a first-time cold start includes container image pull (~5 min for ROCm images) + model load (~3-10 min depending on GGUF size) + health check stabilization. 25 minutes provides headroom for worst-case scenarios without requiring per-model proxy configuration.
- Alternatives considered:
  - Per-model proxy timeout via CRD annotation. Deferred: per-model `ColdStartTimeoutSeconds` on the ModelDeployment CRD already provides this for the readiness wait; the proxy queue timeout is a global safety net.
- Consequences:
  - Clients making cold start requests will hold connections for up to 25 minutes. Load balancers and ingress controllers must be configured with matching timeouts.
- Sources:
  - `platform/gitops/k3s/ai/flexinfer/values.yaml:72-74`
  - Gitops commit `80c934c9`

### 2026-02-20: Reconcile branch deltas into master before backlog status updates

- Decision:
  - Reconcile pending branch deltas into `master` first, then update planning docs + backlog trackers.
- Rationale:
  - Tracking docs and issue status must reflect repository truth on default branch, not intermediate branch-only state.
- Alternatives considered:
  - Update issues first and merge later. Rejected: risk of stale or incorrect backlog status.
- Consequences:
  - `master` now contains merged updates for cold-start reliability + dependency refresh before issue notes were posted.
- Sources:
  - `git log --oneline -n 8` (includes `fad43a7`, `a16b2d1`)
  - GitLab notes:
    - `https://gitlab.flexinfer.ai/services/flexinfer/-/issues/9#note_748`
    - `https://gitlab.flexinfer.ai/services/flexinfer/-/issues/1#note_749`
