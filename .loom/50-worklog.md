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

## 2026-02-24

### Cold-Start Swap Optimization Round 3 — implementation and benchmarks

- What changed:
  - **Resolution-aware warmup** (`build/Dockerfile.diffusers-rocm:618-620`):
    Changed warmup from hardcoded 64x64 to `WARMUP_WIDTH`/`WARMUP_HEIGHT` env vars (default 512x512).
    512x512 compiles MIOpen kernels for 64x64 latent space, better kernel coverage for 1024x1024 inference (128x128 latent) than the previous 8x8 latent from 64x64 warmup.
  - **Warmup config passthrough** (`backend/diffusers.go:131-138`):
    Added `warmupWidth`/`warmupHeight` config keys injecting `WARMUP_WIDTH`/`WARMUP_HEIGHT` env vars into the diffusers container.
  - **Persistent flash-tmpfs for shared models** (`controllers/model_controller.go:880-905`):
    Shared GPU models (`IsShared() == true`) now get a `hostPath` volume at `/dev/shm/flexinfer/{namespace}/{name}` instead of ephemeral `emptyDir`.
    Flash-loader's `shouldCopy()` skips files when sizes match, so second swap copies 0 bytes.
    Non-shared models retain the original `emptyDir` tmpfs behavior.
  - **Tests** (`controllers/model_controller_test.go`):
    `TestPersistentFlashTmpfsForSharedModel` — shared model gets hostPath at correct path with `DirectoryOrCreate`.
    `TestEphemeralFlashTmpfsForNonSharedModel` — non-shared model gets emptyDir with Memory medium and size limit.
  - **Build and deploy**:
    Built `diffusers-api:rocm-0cbf9b7` and `flexinfer-controller:0cbf9b7` on 7900xtx.
    Pushed both to Harbor, tagged controller as `master`.
    Updated `platform/gitops/k3s/ai/flexinfer/values.yaml` (`rocmImage: rocm-0cbf9b7`), committed and pushed to gitops main.
    Flux reconciliation deployed HelmRelease v342.

- Results (benchmarks):

  | Metric | Baseline (Round 2) | 1st Swap (cold tmpfs) | 2nd Swap (warm tmpfs) |
  |--------|--------------------|-----------------------|-----------------------|
  | Swap detection | 2.18s | 2.21s | 2.17s |
  | Model loading | 41.61s | 30.29s | 25.58s |
  | Inference | 16.16s | 16.61s | 14.02s |
  | **Total** | **59.95s** | **49.11s** | **41.77s** |

  - Flash-loader on second swap: `copied=0 skipped=21 (6621.3 MB) elapsed=17ms` — persistent `/dev/shm` hostPath confirmed working.
  - Total improvement: -10.8s first swap, -18.2s second swap (30% faster than baseline).

- Why:
  - Round 3 of cold-start optimization (Rounds 1-2 brought 220-370s down to ~60s).
  - Two remaining bottlenecks: wrong warmup kernels (64x64 vs 1024x1024) and re-copying 6.6 GB every swap.

- What's next:
  - Inference still 14-16s on first swap (kernel shapes differ between 512 warmup latents and 1024 inference latents). Consider 1024x1024 warmup or explicit kernel pre-compilation.
  - Automated `/dev/shm` cleanup on model deletion (deferred; manual: `rm -rf /dev/shm/flexinfer/{ns}/{model}`).
  - Commit flexinfer source changes to master.

- Sources:
  - `build/Dockerfile.diffusers-rocm:618-626` — warmup resolution env vars
  - `backend/diffusers.go:131-138` — warmup config passthrough
  - `controllers/model_controller.go:880-905` — persistent flash-tmpfs logic
  - `controllers/model_controller_test.go:2449-2585` — TestPersistentFlashTmpfsForSharedModel, TestEphemeralFlashTmpfsForNonSharedModel
  - `platform/gitops/k3s/ai/flexinfer/values.yaml:151` — `rocmImage: rocm-0cbf9b7`
  - Benchmark: `scripts/bench-image-swap.sh cold` — run IDs `swap-20260224T195610-e83b9e` (49.11s), `swap-20260224T200757-732c68` (41.77s)
  - Flash-loader log: `kubectl logs sdxl-inpainting-xxx -c flash-loader` — `copied=0 skipped=21 elapsed=17ms`

## 2026-02-26

### Loom Context Pack Refresh

- What changed:
  - Regenerated `.loom/00-workspace-snapshot.md` (script: `workspace_snapshot.py`).
  - Updated `00-index.md` with current state: HEAD at `3f175af`, 15 new commits since last snapshot.
  - Updated `00-mcp-inventory.md`: MCP degraded this session (resource discovery empty, codebase_stats aborted).
  - Documented commit themes since 2026-02-24: GPU sharing improvements, alias safety, flash-tmpfs cleanup automation, qwen3-14b-claude-distill onboarding, dependency refresh.
- Sources:
  - `git log --oneline -15` -> HEAD `3f175af`, 15 commits since `0cbf9b7`
  - `ListMcpResourcesTool({})` -> empty

## 2026-02-27

### Priority Inversion Fix — shared GPU group leader election

- What changed:
  - Fixed `chooseSharedGroupLeader` in `controllers/model_controller.go:2082`: added priority gate to demand-based preemption.
  - One-line change: `if readyIdle && demandedLeader.Spec.GetPriority() >= readyLeader.Spec.GetPriority()`.
  - Updated 2 existing test assertions and added 4 new test cases in `TestChooseSharedGroupLeader_DemandPriorityGate`.
  - Committed as `da62a60`, pushed to `origin/master`. CI pipeline #2368 fully green (all 29 jobs).
  - Controller pod restarted with new image. Verified qwen3-14b-claude-distill (priority 160) correctly holds Active position in 5930k-models group.
- Why:
  - Low-priority models (qwen3-30b, priority 110) could preempt high-priority idle models (claude-distill, priority 160) simply by receiving a request. The v1alpha1 GPUGroup controller handled this correctly; v1alpha2 did not.
- Sources:
  - `controllers/model_controller.go:2082` — priority gate condition
  - `controllers/model_controller_test.go:313-381` — TestChooseSharedGroupLeader_DemandPriorityGate
  - CI pipeline #2368: `https://gitlab.flexinfer.ai/services/flexinfer/-/pipelines/2368`

### codebase_memory Re-index

- What changed:
  - Fixed `local-only` routing: added `local-only` category to codebase_memory in `platform/gitops/mcp/context/registry.yaml`. Without this, `--hub-prefer` routed calls to remote hub which lacks local filesystem access.
  - Restarted loom daemon after config change.
  - Indexed 9 directories as separate repo shards (full-repo single-index hangs at `files_done: 0` for 170 files — known bug in mcp-codebase-memory batch processing):
    - controllers: 26 files / 523 chunks
    - api: 17 files / 338 chunks
    - cmd: 29 files / 201 chunks
    - internal: 23 files / 269 chunks
    - pkg: 23 files / 220 chunks
    - scheduler: 2 files / 28 chunks
    - agents: 22 files / 188 chunks
    - backend: 22 files / 227 chunks
    - e2e: 5 files / 78 chunks
    - **Total: 169 files / 2,072 chunks**
  - Text search verified: `chooseSharedGroupLeader` (5 matches), `ModelSpec` (29 matches).
- Sources:
  - `loom tools call codebase_memory__codebase_index_poll` — all 9 jobs `status: done`
  - `loom tools call codebase_memory__codebase_stats --args '{"repo_id":"flexinfer-controllers"}'` -> `total_chunks: 523`

### Renovate Dependency Batches Triggered

- What changed:
  - Updated GitLab issue #9 (Dependency Dashboard) via `gitlab__update_issue` MCP tool.
  - Checked 3 safe batches: kubernetes (k8s.io/* Go deps), all-minor-patch (buildkit v0.27.1), helm (kube-scheduler v1.35.2).
  - Left unchecked: Docker minor (CUDA/ROCm images), Docker major — too high-risk without staged validation.
  - Renovate schedule: `before 6am on Monday` (America/Denver). MRs will appear Monday morning.
- Sources:
  - `renovate.json:11` — schedule config
  - `gitlab__update_issue` -> `updated_at: 2026-02-27T22:50:27.596Z`

### Loom Context Pack Refresh (2026-02-27)

- What changed:
  - Updated `00-index.md`: HEAD at `da62a60`, current state reflects all deliverables.
  - Updated `00-mcp-inventory.md`: codebase_memory fully indexed (2,072 chunks), MCP CLI works, resource API still empty.
  - Updated `30-implementation-plan.md`: added 2026-02-27 status update with all three backlog items.
  - Updated `50-worklog.md`: added entries for priority inversion fix, codebase_memory re-index, Renovate trigger.
- Sources:
  - `git log --oneline -10` -> HEAD `da62a60`
  - `loom tools list` -> 474 tools
  - `ListMcpResourcesTool({})` -> empty

## 2026-03-05

### Architecture Docs Expansion

- What changed:
  - Expanded `docs/dev/architecture.md` from 48 to 488 lines with 6 Mermaid diagrams: system overview, controller reconciliation, proxy routing, GPU sharing swap, scheduler filter+score, scale-to-zero activation.
  - Added swap timing section to `docs/user/gpu-sharing.md` (+65 lines, 1 Mermaid diagram) with timing constants table and priority semantics.
  - Added routing flow diagram and multipart note to `docs/specs/proxy-api.md` (+36 lines, 1 Mermaid flowchart).
  - Added architecture link to `README.md` (+1 line).
  - Committed: `a9ed3af`, pushed to `master`. All pre-push checks passed (go vet, unit tests, golangci-lint, helm lint).
- Why:
  - Only 1 Mermaid diagram existed (README high-level graph). `docs/dev/architecture.md` was 48 lines of prose with no interaction diagrams. Contributors and agents lacked visual guides for core workflows.
- Sources:
  - `git show --stat a9ed3af` -> 4 files changed, 565 insertions(+), 28 deletions(-)
  - `grep -c 'mermaid' docs/dev/architecture.md` -> 6
  - `grep -c 'mermaid' docs/user/gpu-sharing.md` -> 1
  - `grep -c 'mermaid' docs/specs/proxy-api.md` -> 1

### Loom Context Pack Refresh + Next Round Planning

- What changed:
  - Regenerated `00-workspace-snapshot.md` (script: `workspace_snapshot.py`).
  - Updated `00-index.md`: HEAD at `a9ed3af`, 18 commits since last snapshot, 15 open GitLab issues.
  - Updated `00-mcp-inventory.md`: MCP still degraded (resource API empty, CLI works).
  - Researched open GitLab issues (15 open) + code stubs + tech debt for feature planning.
  - Key findings: FLUX.1 user guide missing (#36 critical), image-gen benchmarking is stub-only, Postgres benchmark store unvalidated (#34), GPUGroup metrics missing (#28), vLLM gfx906 build blocked (#31).
- Sources:
  - `git log --oneline -20` -> HEAD `a9ed3af`
  - `ListMcpResourcesTool({})` -> empty
  - GitLab issue scan: 15 open issues
  - Code scan: `benchmarker.go:895,924` (image-gen stub), `modelcache_controller.go:104` (unimplemented strategies)
