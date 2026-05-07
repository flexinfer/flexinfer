# Decisions

Record decisions as they are made, with date, rationale, and sources.

### 2026-05-06: Keep runtime profile sync as consistency-test-only

- Decision:
  - Do not generate `GPUProfile` manifests from `build/runtime.yaml`. Continue relying on `scripts/check-runtime-profile-consistency.sh` (RG-2) until a third managed profile lands or a drift incident proves the test insufficient.
- Rationale:
  - Only two managed profiles exist (`gfx1100`, `gfx906`), and operational `GPUProfile` fields (memory budgets, backend support, quantization images, env) are tuned from cluster evidence, not build inputs.
- Sources:
  - `docs/planning/runtime-profile-generation-decision.md`.

### 2026-05-06: Demote gfx906 vLLM to experimental until canary promotion

- Decision:
  - Treat `gfx906` vLLM as experimental/canary-only, not full default support, until a dedicated image digest is validated on Radeon VII and promoted.
- Rationale:
  - The current unified `gfx906` runtime profile disables vLLM because its PyTorch 2.3 base is too old for that lane.
  - The deployed GPUProfile and gfx906 README previously described vLLM as full support, creating conflicting operator guidance.
- Consequences:
  - `deploy/gpuprofiles/gfx906.yaml` now marks vLLM support as `experimental`.
  - `build/README-gfx906.md` and the vLLM example now frame that path as canary-only.
  - The next slice can add automated consistency checks between `build/runtime.yaml`, GPUProfiles, Helm values, and docs.
- Sources:
  - `build/runtime.yaml`.
  - `deploy/gpuprofiles/gfx906.yaml`.
  - `build/README-gfx906.md`.
  - `docs/planning/rocm-gfx1100-gfx906-platform-slice.md`.

### 2026-05-02: Make spec capsules the default for multi-file feature delivery

- Decision:
  - Add a spec-driven delivery lane to the roadmap and make spec capsules the default planning unit for future multi-file features and operational workflow changes.
- Rationale:
  - FlexInfer's major product phases are already marked complete, and the current bottleneck is keeping feature intent, implementation slices, runtime canaries, and roadmap tracking synchronized.
  - A small sourced spec before implementation should reduce rediscovery and make parallel agent work easier to split safely.
- Consequences:
  - `docs/planning/spec-driven-delivery.md` becomes the public pattern for future roadmap slices.
  - `ROADMAP.md` and `docs/planning/next-roadmap.md` now track delivery acceleration as planned work.
  - Future reconciliation should create or update tracking issues for SD-1 through SD-5.
- Sources:
  - `docs/planning/spec-driven-delivery.md`.
  - `ROADMAP.md`.
  - `docs/planning/next-roadmap.md`.
  - `.loom/30-implementation-plan.md`.

### 2026-04-26: Block 26B fp16-KV long-context promotion above 8K

- Decision:
  - Do not promote `gemma4-26b-a4b-gptq-long` to 16K or 32K on the current hybrid artifact with fp16 KV.
  - Keep the canary manifest as an opt-in probe, but treat it as failed until a memory lever changes the KV budget.
- Rationale:
  - The live 32K canary loaded weights successfully, but vLLM had only `1.87 GiB` available for KV while `32768` tokens required `6.88 GiB`.
  - vLLM estimated the maximum model length at `8896`, which is below the 16K rung as well as the 32K target.
- Consequences:
  - The current production-safe 26B path remains the 8K hybrid lane.
  - Long-context work should move to a smaller dense-validated artifact, FP8 KV, or a TurboQuant/layer-selective KV canary.
- Sources:
  - Live pod log from `gemma4-26b-a4b-gptq-long` on 2026-04-26.
  - `.loom/60-validation-matrix.md`.
  - `deploy/models/gemma4-26b-a4b-gptq-long.yaml`.

### 2026-04-26: Treat the dense-validated 26B rebuild as timeout-blocked, not cosine-failed

- Decision:
  - Extend the dense 26B abliteration and quantization deadlines to 24h before retrying the managed rebuild.
  - Do not record any dense cosine result until a job actually reaches the quantize-time dense-module gate.
- Rationale:
  - The latest live dense retry reached harmful prompt `80/128` before the 4h abliteration deadline and left the checkpoint in `stage: harmful_activations`.
  - The current abliteration script resumes completed payload files, not partial prompt indices, so retries repeat the partial harmful pass instead of continuing at prompt 80.
- Consequences:
  - The next durable Flux-managed run has enough wall time to reach harmless activations and cosine validation.
  - A later improvement can add partial activation resume, but the timeout change is the smallest unblocker.
- Sources:
  - PVC inspection of `gemma4-26b-a4b-gptq-dense` on 2026-04-26.
  - `deploy/modelcaches/gemma4-26b-a4b-gptq-dense.yaml`.
  - `build/scripts/abliterate.py`.

### 2026-04-26: Gate TurboQuant primitive sharing behind `TQ4_SHARE_PRIMITIVES`

- Decision:
  - Share immutable TurboQuant rotation/codec primitives by device/head geometry/bits/seed/codec when `TQ4_SHARE_PRIMITIVES=1`.
  - Keep the behavior opt-in through runtime profile environment, not unconditional patch behavior.
- Rationale:
  - The previous 31B TurboQuant lane OOMed before KV sizing because per-layer plugin state consumed about `3.57 GiB` on a 24 GiB card.
  - Sharing primitives should reduce fixed per-layer residency and makes the next canary distinguish plugin construction memory from true KV-cache limits.
- Consequences:
  - TurboQuant canaries still require a rebuilt runtime image and a boot-only gate before any context ladder.
  - The patcher remains idempotent against the pinned upstream TurboQuant source.
- Sources:
  - `build/scripts/patch_turboquant_quantizer_gpu_qr.py`.
  - `build/runtime.yaml`.
  - `.loom/gemma4-31b-turboquant-memory-fix-plan.md`.

### 2026-04-25: Sequence clean GPTQ artifacts before TurboQuant promotion

- Decision:
  - Treat GPTQ artifact correctness as the first gate and TurboQuant as a later runtime optimization gate.
  - Keep the current 26B hybrid GPTQ lane as the known-good 8K fallback while dense and long-context canaries run.
  - Treat the current 31B `gptq-w4-g128-keqv` artifact as corrupt and non-promotable; re-quantize 31B before any further 31B TurboQuant promotion attempt.
  - Keep `k_eq_v` as a post-process after clean 31B quantization, not as a repair mechanism for repeated late-layer tensors.
- Rationale:
  - Current manifests record that 31B can load and allocate KV at `maxModelLen: 1920`, but output collapses to pure `<pad>` because layers 40-59 contain repeated tensors across attention and MLP families.
  - Earlier 31B TurboQuant OOM analysis remains valid, but it is a second-stage problem. The current artifact must be fixed before plugin memory optimization can prove serving correctness.
  - The 26B lane already has a coherent hybrid artifact, so it should remain the fallback while smaller dense or long-context variants are validated.
- Consequences:
  - The next engineering slice should add artifact integrity guards and fix the 26B long-canary dGPU selector before spending more long GPU cycles.
  - TurboQuant primitive sharing remains useful, but it should be verified against clean artifacts and canaries rather than primary manifests.
- Sources:
  - `.loom/gemma4-26b-31b-gptq-turboquant-plan.md`.
  - `deploy/models/gemma4-26b-a4b-gptq.yaml`.
  - `deploy/models/gemma4-26b-a4b-gptq-long.yaml`.
  - `deploy/models/gemma4-31b-gptq.yaml`.
  - `.loom/gemma4-31b-turboquant-closeout.md`.
  - `.loom/gemma4-31b-turboquant-memory-fix-plan.md`.

### 2026-04-25: Close the 31B TurboQuant long-context lane on 24 GiB gfx1100

- Decision:
  - Historical note: this decision was made when the validated ceiling was
    documented as `maxModelLen: 2048`. Current manifests now cap
    `gemma4-31b-gptq` at `maxModelLen: 1920` because the loaded `keqv` artifact
    is semantically corrupt.
  - Keep `gemma4-31b-gptq-long.yaml` on disk for reference, but leave it out of
    Flux reconciliation.
  - Treat new 31B long-context attempts on this hardware as blocked until there
    is a new memory lever: smaller weights, deferred/lower plugin allocations,
    a different KV compression path, or larger VRAM.
- Rationale:
  - The TurboQuant canary OOM'd during weight construction, before KV cache
    allocation. The merged analysis records 20.02 GiB of 31B INT4 weights plus
    about 3.57 GiB of TurboQuant plugin state, leaving about 0.4 GiB on a
    23.98 GiB card before activations, graph payload, or KV.
  - `gpuMemoryUtilization` cannot bound the plugin's raw `torch.empty()`
    allocations, and vLLM V1 CPU offload is not available as a current runtime
    lever.
- Consequences:
  - Future agents should read the primary CR and current manifest annotations
    for the live ceiling. The newer planning decision above supersedes the
    historical `validated-2048-gfx1100-ceiling` wording.
  - The superseded canary CR carries
    `turboquant-canary-superseded-gfx1100-24gb-insufficient-vram` and evidence
    pointing to the same doc.
  - Long-context work pivots to `gemma4-26b-a4b-gptq-long` in a later MR.
- Sources:
  - `glab mr view 192` -> `state: merged`.
  - `git show b64f0502:docs/dev/gemma4-31b-turboquant-24gb-oom.md | nl -ba`
    lines 13-34, 40-55, 57-90, 120-137.
  - `git show b64f0502:deploy/models/gemma4-31b-gptq.yaml | nl -ba`
    lines 45-53, 125-141.
  - `git show b64f0502:deploy/models/gemma4-31b-gptq-long.yaml | nl -ba`
    lines 65-74.
  - `git show b64f0502:deploy/models/kustomization.yaml | nl -ba`
    lines 14-21.

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

### 2026-04-18: Add granular loading substages to `Model.status`

- Decision:
  - Extend the `ai.flexinfer/v1alpha2` Model CRD with `status.loadingSubstage` (enum) and `status.message` (human string). Populate from pod/container state, a scraped vLLM `/metrics` + log sidecar, and the readiness probe. Surface in FlexDeck + proxy.
- Proposed enum values:
  - `ImagePulling` — kubelet still pulling the runtime image (common for 18 GB ROCm images on cold cache).
  - `Initializing` — container up, process starting.
  - `LoadingWeights` — model read in progress; message carries `"<N>/<total> shards, last progress <age>"`.
  - `Compiling` — torch.compile/kernel warmup.
  - `HealthCheckPending` — load done, `/health` not yet 200.
  - `Preempted` — shared-group peer took the slot; message carries preempting model name + timestamp.
- Rationale:
  - Observed 2026-04-18 ~13:26–13:45Z on `gemma4-26b-a4b-gptq`: shard 31/34 load stalled 8m47s on Longhorn (cache PVC `pvc-ec945ced`, `longhorn` storage class, 3 replicas on k3s-w-4, cblevins-radeonvii, cblevins-7900xtx). Load did eventually complete. During the stall, FlexDeck showed `Phase: Loading` with no progress info, the proxy queue built up (6+ requests), and an operator could not distinguish a transient Longhorn replica stall from a true hang without `kubectl logs` into the pod.
  - The problem is **observability, not controller correctness** — `phase=Loading` was accurate; it just collapses all of "still fine, wait longer" and "actually wedged" into the same signal.
- Alternatives considered:
  - Single free-form `message` field (no enum): rejected — forces every consumer (proxy, FlexDeck, future operators) to string-parse to decide whether to back off.
  - Add new top-level phases (`ImagePulling`, `LoadingWeights`, etc.): rejected — would break every consumer that switches on `.status.phase` today. A substage field keeps the phase enum stable and adds detail.
  - Rely only on pod/container state from the replicaset: rejected — that covers `ImagePulling` and `Initializing` but can't see inside vLLM (weight shards, compile, health).
- Consequences:
  - CRD schema change (minor version bump on the `v1alpha2` status subresource; backward compatible because the new fields are optional).
  - Controller needs a small vLLM log tailer or `/metrics` scraper to capture shard progress — vLLM v0.8+ exposes `vllm:engine_status` and `vllm:num_requests_waiting`; older builds expose progress only via stdout. The log tailer is the safer path near-term.
  - Proxy can read `loadingSubstage` + last-progress age to decide between "keep waiting" and "fail-fast with 503 + retry-after" — prevents the queue-build-up feedback loop.
  - FlexDeck gets a single place to surface richer status without inventing its own heuristics.
- Related follow-up (separate decision below):
  - Move `gemma4-26b-a4b-gptq-cache` off the default `longhorn` storage class to `local-path` or `nvme-1r-gpu` (1 replica on the GPU node), matching the 2026-02-20 Qwen3-30B GGUF precedent. Eliminates cross-node replica reads that caused this stall in the first place.
- Sources:
  - `.loom/60-validation-matrix.md` — gemma4-26b-a4b-gptq row.
  - `kubectl -n longhorn-system get volume pvc-ec945ced-172d-439a-b386-abe6a439dc71` — `state=attached, robustness=healthy, replicas on k3s-w-4 + cblevins-radeonvii + cblevins-7900xtx`.
  - `kubectl logs gemma4-26b-a4b-gptq-87c45466d-xtqb6 -n flexinfer-system` — shards 1-30 @ ~2 s/it, shard 31 @ 8m47s.
  - `kubectl get events -n flexinfer-system | grep gemma4-26b-a4b-gptq` — `Preempted by gemma4-26b-a4b-gptq-long with priority 200` at 13:26:48Z.
  - Precedent: `MEMORY.md` note "Use local-path storageClass for large GGUF model caches" (2026-02-20).
  - Tracker: https://gitlab.flexinfer.ai/services/flexinfer/-/issues/53

### 2026-04-18: Migrate `gemma4-26b-a4b-gptq-cache` PVC off default Longhorn to local/1-replica storage

- Decision:
  - Change `gemma4-26b-a4b-gptq-cache` PVC (and any other serving-path cache PVC used for vLLM weight loads on gfx1100) from `storageClassName: longhorn` (3 replicas by default, spread across nodes) to either `local-path` (K3s local provisioner, no replication) or `nvme-1r-gpu` (Longhorn 1-replica on GPU-local NVMe).
- Rationale:
  - The 2026-04-18 shard-31 stall is consistent with a Longhorn replica-read stall: engine on cblevins-7900xtx, but two of the three replicas live on k3s-w-4 and cblevins-radeonvii. Cross-node reads add network latency and fail-over handling that mmap-driven vLLM weight loads cannot tolerate well.
  - 2026-02-20 precedent: Qwen3-30B GGUF mmap loads went from 15–20 min on Longhorn to ~3 min on `local-path` (`MEMORY.md`, "Use local-path storageClass for large GGUF model caches").
- Alternatives considered:
  - Keep Longhorn but pin `numberOfReplicas=1` via PVC annotation: matches `nvme-1r-gpu` intent; acceptable if we keep the storage class explicit and self-documenting.
  - Keep Longhorn 3r and rely on Longhorn's "local-replica-first" read heuristic: rejected — the whole point of the 3 replicas is durability, not read speed; read-time behavior is still racy.
- Consequences:
  - Manifest change to `deploy/modelcaches/gemma4-26b-a4b-gptq.yaml` + any associated cache-PVC spec (if the cache PVC is controller-managed separately from the main SharedPVC).
  - Cold rebuild cost: if we switch the cache PVC class, the 16 GB artifact must be re-staged into the new PVC. One-time. Acceptable given serving latency win.
  - Durability trade-off: single replica means a node-disk failure re-downloads + re-stages the artifact. Already the operating model for serverless warm caches.
- Sources:
  - See phase-granularity decision above (same evidence base).
  - `MEMORY.md` 2026-02-20 Longhorn/local-path precedent.
  - Tracker: https://gitlab.flexinfer.ai/services/flexinfer/-/issues/53 (same issue).

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
