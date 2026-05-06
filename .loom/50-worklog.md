# Worklog

Chronological notes while executing the plan (useful for handoffs and debugging).

## 2026-05-06

### RALPH Slice 1 — gfx1100/gfx906 capability matrix reconciliation

- What changed:
  - Ran the roadmap/spec RALPH loop against the new `gfx1100/gfx906` platform spec and selected Slice 1 as the smallest reversible increment.
  - Added `docs/planning/rocm-gfx1100-gfx906-platform-slice.md` with the iteration plan, support matrix, acceptance criteria, validation plan, and rollback path.
  - Demoted `gfx906` vLLM support in `deploy/gpuprofiles/gfx906.yaml` from `full` to `experimental`.
  - Updated `build/README-gfx906.md` so vLLM and MLC-LLM are canary/experimental, not full default lanes, and corrected the Vega20 env guidance to include `HSA_OVERRIDE_GFX_VERSION=9.0.6`, `HSA_ENABLE_SDMA=0`, `HSA_USE_SVM=0`, and `PYTORCH_ROCM_ARCH=gfx906`.
  - Added a canary warning to `examples/v1alpha2/model-vllm-gfx906.yaml`.
  - Linked the new platform lane from `docs/planning/next-roadmap.md` and marked RG-1 complete.
- Why:
  - `build/runtime.yaml` disables vLLM in the current unified `gfx906` runtime, while the GPUProfile and README called it full support. The first slice removes that contradictory truth before API or runtime-image work.
- Validation:
  - `git diff --check -- deploy/gpuprofiles/gfx906.yaml build/README-gfx906.md examples/v1alpha2/model-vllm-gfx906.yaml docs/planning/rocm-gfx1100-gfx906-platform-slice.md docs/planning/next-roadmap.md .loom/gfx1100-gfx906-platform-enhancements-plan.md .loom/40-decisions.md .loom/50-worklog.md` passed.
  - `rg -n "gfx906|vLLM|runtime:rocm-gfx906|support:|HSA_OVERRIDE_GFX_VERSION|HSA_ENABLE_SDMA|HSA_USE_SVM" ...` confirmed the reconciled support/env statements are present.
  - `yq e '.' deploy/gpuprofiles/gfx906.yaml` and `yq e '.' examples/v1alpha2/model-vllm-gfx906.yaml` passed.
- Blockers:
  - `agent_context__agent_session_start` failed with `Transport closed`; handoff is captured in `.loom` docs for this pass.
- Next:
  - Add a consistency check for `build/runtime.yaml` vs GPUProfiles/Helm runtime profiles.
  - Expand `.loom/60-validation-matrix.md` for runtime digest canary rows.

## 2026-05-02

- Refreshed `.loom/00-workspace-snapshot.md` with the plan-loom-core snapshot script.
- Confirmed Loom resource mode is available through `loom://config`, `loom://servers`, `loom://tools/index`, and `loom://health`.
- Confirmed `codebase_memory` health via `loom tools call codebase_memory__codebase_stats --args '{"repo_id":"flexinfer"}' --json` with `total_chunks: 2831`.
- Added `docs/planning/spec-driven-delivery.md`.
- Updated `ROADMAP.md`, `docs/planning/next-roadmap.md`, and `docs/planning/README.md` to expose the spec-driven delivery lane.
- Updated `.loom/00-index.md`, `.loom/00-mcp-inventory.md`, `.loom/20-product-spec.md`, `.loom/30-implementation-plan.md`, and `.loom/40-decisions.md` with current planning context.

## 2026-04-25

### Planned 31B TurboQuant memory fix

- What changed:
  - Added `.loom/gemma4-31b-turboquant-memory-fix-plan.md`.
  - Updated `.loom/30-implementation-plan.md` with the preferred fix:
    patch TurboQuant to share/lazily materialize immutable codec primitives
    across attention layers.
- Key finding:
  - Pinned upstream `turboquant-vllm@9d19b87c` constructs `TurboQuantMSE` and
    moves rotation/codebook tensors onto the target device for every
    `TQ4AttentionImpl`.
  - Those primitives depend on head size, bit width, and seed, not layer
    identity, so sharing them is the lowest-risk memory fix before trying
    weight re-quantization or hardware changes.
- Sources:
  - `git clone --depth 1 https://github.com/Alberto-Codes/turboquant-vllm.git /tmp/turboquant-vllm-plan`
  - `git fetch --depth 1 origin 9d19b87cef462cf0abd5643f6d052ac5a3bc99b6`
  - `/tmp/turboquant-vllm-plan/src/turboquant_vllm/vllm/tq4_backend.py:347-392`
  - `/tmp/turboquant-vllm-plan/src/turboquant_vllm/quantizer.py:93-110`
  - `build/scripts/patch_turboquant_quantizer_gpu_qr.py`

### MR !192 closeout and durable knowledge capture

- What changed:
  - Refreshed the Loom context templates and regenerated
    `.loom/00-workspace-snapshot.md`.
  - Confirmed current MCP runtime is loom-resource mode rather than the old
    CLI-only fallback path:
    - `functions.list_mcp_resources({})` exposed `loom://config`,
      `loom://servers`, `loom://tools`, `loom://tools/index`, and
      `loom://health`.
    - `functions.read_mcp_resource(server="loom", uri="loom://config")`
      reported profile `full`, `serverCount=47`, `toolCount=504`.
  - Confirmed `codebase_memory` is healthy through the CLI fallback:
    `repo_id=flexinfer`, `total_chunks=2831`.
  - Fetched GitLab remotes and verified MR !192 state with `glab mr view 192`:
    `state: merged`.
  - Added `.loom/gemma4-31b-turboquant-closeout.md` and the decision entry above
    so future agents can recover the 31B TurboQuant ceiling without reading the
    whole MR thread.
- Production state after MR !192:
  - Historical closeout notes referenced `maxModelLen: 2048`, but the current
    manifest now caps `gemma4-31b-gptq` at `maxModelLen: 1920` because the
    loaded `keqv` artifact is semantically corrupt.
  - `gemma4-31b-gptq-long.yaml` is preserved but removed from Flux
    reconciliation.
  - The 31B TurboQuant lane is closed on 24 GiB gfx1100.
  - The next long-context path is the separate
    `gemma4-26b-a4b-gptq-long` canary.
- Sources:
  - `python /Users/cblevins/.codex/skills/plan-loom-core/scripts/init_loom_context.py --root .`
  - `python /Users/cblevins/.codex/skills/plan-loom-core/scripts/workspace_snapshot.py --root .`
  - `functions.list_mcp_resources({})`
  - `functions.list_mcp_resource_templates({})`
  - `functions.read_mcp_resource(server="loom", uri="loom://config")`
  - `loom tools call codebase_memory__codebase_stats --args '{"repo_id":"flexinfer"}' --json`
  - `git fetch --all --prune`
  - `glab mr view 192`
  - `git show b64f0502:docs/dev/gemma4-31b-turboquant-24gb-oom.md | nl -ba`

### Gemma4 26B/31B GPTQ + TurboQuant planning pass

- What changed:
  - Added `.loom/gemma4-26b-31b-gptq-turboquant-plan.md`.
  - Updated `10-research.md`, `20-product-spec.md`, `30-implementation-plan.md`, and `40-decisions.md` with the current direction.
  - Refreshed `00-workspace-snapshot.md` with the plan-loom-core snapshot script.
- Key conclusion:
  - The correct direction is clean GPTQ artifacts first, TurboQuant second. The current 31B `keqv` artifact is semantically corrupt and must be re-quantized before TurboQuant memory work can prove anything useful.
- Remaining issues accounted for:
  - 26B hybrid is coherent but large; 32K requires canary validation.
  - 26B long canary has a selector risk because it currently allows `gpu.count: 1`.
  - 31B `keqv` loads at 1920 but collapses to `<pad>` because late-layer tensors repeat.
  - 31B TurboQuant previously OOMed before KV because plugin state consumed about 3.57 GiB on top of about 20.02 GiB of weights.
  - TurboQuant is KV/vector compression, not GPTQ weight quantization.
- Evidence reviewed:
  - Tavily searches/extracts for Gemma4 official docs, TurboQuant paper/blog, vLLM ROCm docs, vLLM quantized KV docs, ROCm quantization docs, GPTQModel support, and community TurboQuant/Gemma4 reports.
  - Git history for Gemma4/GPTQ/TurboQuant/gfx1100 work, including commits `bde445f0`, `96d091e3`, `3b6f52b9`, `0fb31ecd`, `078914ae`, and `b64f0502`.
  - Current manifests under `deploy/models/` and `deploy/modelcaches/`.
- Tool note:
  - `codebase_memory` stats reported `total_chunks: 2831`, but semantic search failed with Morph HTTP 521 `origin_down`, so the plan relied on `rg`, direct file reads, git history, and Tavily.

### Rapid iteration 1 — 26B long canary dGPU selector

- Isolate:
  - The 26B long-context canary still rendered and lived with `spec.gpu.count: 1`, while the working primary 26B profile requires `count: 2` on `cblevins-7900xtx` to avoid the Raphael iGPU slot.
- Hypothesis:
  - Matching the primary's `count: 2` selector retires the iGPU-placement failure mode without starting the canary, because the canary remains `minReplicas: 0`.
- Patch:
  - Updated `deploy/models/gemma4-26b-a4b-gptq-long.yaml` to set `gpu.count: 2` and document why.
  - Updated `deploy/models/kustomization.yaml` stale comments from "TurboQuant/priority 50" to "32K long-context/priority 200".
- Build/prove:
  - `kubectl kustomize deploy/models` rendered `gemma4-26b-a4b-gptq-long` with `gpu.count: 2`, `config.hipVisibleDevices: "0"`, `minReplicas: 0`, and `shared: 7900xtx-textgen`.
  - `kubectl diff -f deploy/models/gemma4-26b-a4b-gptq-long.yaml` showed a single intended live spec delta: `gpu.count: 1` -> `2`.
- Observe:
  - Live cluster before reconcile: `kubectl -n flexinfer-system get model gemma4-26b-a4b-gptq-long -o jsonpath='{.spec.gpu.count}{"\n"}{.spec.config.hipVisibleDevices}{"\n"}{.spec.serverless.minReplicas}{"\n"}{.status.phase}{"\n"}'` returned `1`, `0`, `0`, `Idle`.
  - `kubectl diff -k deploy/models` also showed unrelated drift on the primary 26B and 31B objects, so no broad apply was performed.
- Next:
  - Land/reconcile this scoped GitOps change when ready.
  - Next technical blocker after selector safety: add repeated-tensor integrity checks so a corrupt 31B GPTQ artifact cannot advance into `k_eq_v`.

### Rapid iteration 2 — 31B repeated qweight integrity guard

- Isolate:
  - The current 31B `gptq-w4-g128-keqv` artifact loads but emits `<pad>` because the source GPTQ artifact has repeated projection tensors on late layers. The existing `k_eq_v` task could make a complete-looking artifact from that corrupt source.
- Hypothesis:
  - Hashing projection `qweight` tensors by module family and failing when the same qweight appears on different layers catches this corruption class before serving promotion.
- Patch:
  - Added a Gemma4 31B repeated-qweight guard to `build/scripts/validate_quantized_artifact.py`.
  - Added unit tests for duplicate-vs-distinct 31B qweights in `build/scripts/test_validate_quantized_artifact.py`.
  - Added the same source-integrity guard to `deploy/tasks/gemma4-31b-keqv/postprocess.py` before any `v_proj` duplication.
  - Re-check source integrity even when `DST_DIR` already contains a complete-looking `keqv` output, so an old bad artifact cannot keep no-oping forever.
  - Bumped the Flux Job names to `gemma4-31b-keqv-postprocess-v4` and `gemma4-31b-keqv-cache-copy-reset-v4`; updated the reset helper to wait for the v4 postprocess Job.
- Build/prove:
  - `python3 -m py_compile build/scripts/validate_quantized_artifact.py build/scripts/test_validate_quantized_artifact.py deploy/tasks/gemma4-31b-keqv/postprocess.py`
  - `python3 build/scripts/test_validate_quantized_artifact.py` -> 11 tests OK.
  - Synthetic duplicate source with an already-complete destination returned `dup_existing_dst_rc=4` and logged `source integrity failed: repeated qweight tensors across layers: self_attn.q_proj layers=[0, 1]`.
  - Synthetic distinct source with an already-complete destination returned `ok_existing_dst_rc=0` and logged `DST already complete... GitOps-safe no-op`.
  - `kubectl kustomize deploy/tasks/gemma4-31b-keqv` renders `gemma4-31b-keqv-postprocess-v4`, `gemma4-31b-keqv-cache-copy-reset-v4`, and the new source-integrity code.
- Observe:
  - Live cluster still has only `gemma4-31b-keqv-postprocess-v3` complete; no Flux reconcile or hot apply was performed in this iteration.
  - `kubectl diff -k deploy/tasks/gemma4-31b-keqv` shows v4 Job/ConfigMap additions, as expected.
- Next:
  - Land the GitOps changes, reconcile the task kustomization, and expect the v4 postprocess Job to fail on the current corrupt source. That failure is useful evidence confirming the guard caught the known bad artifact.
  - After the guard lands, the next technical blocker is a clean 31B re-quant with lower corruption risk and the same guard in front of `k_eq_v`.

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

## 2026-04-07

### Gemma4 GPTQ monitoring, cache cleanup, and next-round planning refresh

- What changed:
  - Refreshed `.loom/00-workspace-snapshot.md` with `plan-loom-core` scripts.
  - Re-validated that MCP resource/template discovery is still empty in this session and fell back to repo-local evidence for planning.
  - Confirmed `gemma4-31b-gptq` is not actually stalled at low displayed progress; it is resuming from `perplexity_validated` and spending time in baseline perplexity validation.
  - Confirmed `gemma4-26b-a4b-gptq` was genuinely wedged on `gfx1100`:
    - full model load completed,
    - progress stopped at `harmful activations 0/128`,
    - Python process was in Linux `D` state.
  - Observed `cblevins-7900xtx` transition `NotReady`, lose SSH reachability, and destabilize cluster API/etcd leadership during reboot/failover.
  - Applied live mitigation:
    - patched `gfx1100` `GPUProfile` to set `ABLITERATION_ACTIVATION_CAPTURE_MODE=hidden_states`,
    - patched `gemma4-26b-a4b-gptq` selectors to `cblevins-5930k`,
    - deleted stale 26B jobs/pods and repeated stale `VolumeAttachment` cleanup.
  - Updated local repo manifests/tests to reflect the safer `gfx1100` activation-capture override.
  - Updated `.loom/10-research.md` and `.loom/30-implementation-plan.md` with the next improvement round focused on runtime stability, placement, storage, recovery, and observability.

- Why:
  - Convert the current live incident into a durable improvement plan while jobs remain in flight, instead of treating each failure as an isolated retry problem.

- Current live state when writing:
  - `gemma4-31b-gptq` still running on `cblevins-radeonvii`.
  - `gemma4-26b-a4b-gptq` rerouted to `cblevins-5930k`, with the warmup pod still pulling the runtime image.
  - `cblevins-7900xtx` still `NotReady`.

- What’s next:
  - Validate that the rerouted `26B` run on `5930k` actually starts with `hidden_states` and progresses past `harmful activations 0/128`.
  - If successful, commit/push the `gfx1100` profile change and follow with placement hardening.
  - If unsuccessful, stop scheduling Gemma4 abliteration onto `gfx1100` and treat `gfx906` as the only safe architecture until the ROCm/amdgpu failure is root-caused.

- Sources:
  - `python /Users/cblevins/.codex/skills/plan-loom-core/scripts/workspace_snapshot.py --root .`
  - `functions.list_mcp_resources({})`
  - `functions.list_mcp_resource_templates({})`
  - `kubectl -n flexinfer-system logs gemma4-26b-a4b-gptq-abliterate-zwvpv --tail=160`
  - `kubectl -n flexinfer-system exec gemma4-26b-a4b-gptq-abliterate-zwvpv -- sh -lc 'ps -o pid,ppid,stat,%cpu,%mem,etime,cmd -C python3'`
  - `kubectl -n flexinfer-system logs gemma4-31b-gptq-abliterate-sxxwv --tail=160`
  - `kubectl get nodes -o wide | rg 'cblevins-7900xtx|cblevins-5930k|cblevins-radeonvii'`
  - `ssh -o BatchMode=yes -o ConnectTimeout=5 cblevins-7900xtx 'hostname && uptime && systemctl is-active k3s && uname -a'`
  - `ssh cblevins-5930k 'kubectl get nodes -o wide | grep -E "cblevins-(5930k|7900xtx|radeonvii)"'`
  - `deploy/gpuprofiles/gfx1100.yaml`
  - `pkg/quantization/abliteration_test.go`

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

## 2026-04-09

### Gemma4 research / plan reset

- What changed:
  - Refreshed `.loom/00-workspace-snapshot.md`.
  - Re-checked MCP/runtime inventory:
    - MCP resource/template discovery is still empty,
    - direct loom MCP bridge calls still return `Transport closed`,
    - `loom` CLI fallback works and reports `502` tools.
  - Converted the Gemma4 incident work into a source-backed failure taxonomy.
  - Confirmed the two durable bug classes from live cluster evidence:
    1. missing-source retry loops after downstream phases removed weights,
    2. partial sharded-cache reuse caused by marker-only validation.
  - Landed and deployed two code fixes during the incident:
    - `af22ecc0`: reset to download when abliteration has lost source weights
    - `fdadc03d`: require complete shard sets before cache reuse / abliteration start
  - Updated `.loom/10-research.md` and `.loom/30-implementation-plan.md` with a phased permanent-fix program centered on integrity gates, recovery semantics, runtime image determinism, and status/monitoring.

- Why:
  - The current problem is no longer “why did the last job fail”; it is “how do we prevent Gemma4 pipelines from re-entering the same integrity and runtime traps next week”.

- What’s next:
  - Finish the `26B` full-redownload validation after `fdadc03d`.
  - Convert duplicated shard-integrity shell logic into one shared helper.
  - Build a Gemma4-capable runtime image so abliteration does not install Transformers from Git during job startup.

- Sources:
  - `python "$CODEX_HOME/skills/plan-loom-core/scripts/workspace_snapshot.py" --root .`
  - `loom tools list --json | sed -n '1,220p'`
  - `loom tools list --json | jq -r '.tools[].name' | awk -F'__' '{print $1}' | sort | uniq -c | sort -nr | sed -n '1,20p'`
  - `kubectl describe modelcache gemma4-26b-a4b-gptq -n flexinfer-system`
  - `kubectl logs -n flexinfer-system gemma4-26b-a4b-gptq-abliterate-hh4wn --tail=80`
  - `kubectl logs -n flexinfer-system gemma4-26b-a4b-gptq-downloader-85mm9 --tail=80`
  - `kubectl logs -n flexinfer-system gemma4-31b-gptq-abliterate-28pj6 --tail=120`
  - `git rev-parse HEAD`

## 2026-04-18

### Incident triage — 26B cold-start stall + observability gap

- What happened:
  - FlexDeck showed `gemma4-26b-a4b-gptq` stuck in `Loading` with a growing proxy queue. Verified against cluster: `kubectl get model … -o jsonpath='{.status.phase}'` → `Loading`. FlexDeck was accurate, not stale.
  - Events showed a preemption at 13:26:48Z by `gemma4-26b-a4b-gptq-long` (priority 200 vs 150) in the `7900xtx-textgen` shared group.
  - After re-activation, a fresh pod (`…-xtqb6`) pulled the runtime image in 749 ms, but vLLM weight load wedged for 8m47s on safetensors shard 31/34 (shards 1–30 loaded at ~2 s/it each).
  - Serving PVC is `pvc-ec945ced-172d-439a-b386-abe6a439dc71` (`gemma4-26b-a4b-gptq-cache`, 50Gi, storage class `longhorn`, 3 replicas across k3s-w-4 / cblevins-radeonvii / cblevins-7900xtx). Longhorn volume state was `attached/healthy` throughout; the stall was a replica-read slowdown, not a volume failure.
  - Pod eventually reached `Ready`; `/health` returns 200 and serves now.
  - Separately spotted vLLM validation error in logs: `VLLMValidationError: ... your prompt contains 79 characters (more than 0 characters, which is the upper bound for 0 input tokens)` — looks like `max_tokens=8192` + `max_model_len=8192` leaves 0 prompt budget. Tracked as a separate small issue below.
- Why it mattered:
  - Operator UX on FlexDeck gave no indication of "transient Longhorn stall" vs "actually wedged". Queue built up. Only `kubectl logs` into the pod could distinguish the two.
- Two decisions landed in `40-decisions.md`:
  - Add `Model.status.loadingSubstage` enum + `status.message` to surface shard progress and distinguish `ImagePulling`/`Initializing`/`LoadingWeights`/`Compiling`/`HealthCheckPending`/`Preempted`.
  - Migrate the 26B cache PVC off default 3-replica Longhorn to `local-path` or `nvme-1r-gpu` (1 replica, GPU-local NVMe), matching the 2026-02-20 Qwen3-30B GGUF precedent.
- What's next:
  - [services/flexinfer#53](https://gitlab.flexinfer.ai/services/flexinfer/-/issues/53) tracks the CRD + controller + proxy + FlexDeck changes and the PVC migration.
  - Separate follow-up for the `max_tokens == max_model_len` validation error if it turns out to be a manifest bug vs a vLLM upstream quirk.
- Sources:
  - `kubectl get model gemma4-26b-a4b-gptq -n flexinfer-system -o jsonpath='{.status.phase}'`
  - `kubectl get events -n flexinfer-system --sort-by=.lastTimestamp | grep gemma4-26b`
  - `kubectl logs gemma4-26b-a4b-gptq-87c45466d-xtqb6 -n flexinfer-system | grep Loading`
  - `kubectl -n longhorn-system get volume pvc-ec945ced-172d-439a-b386-abe6a439dc71 -o yaml`

### Slice A1-lite — gemma4-26b-a4b-gptq validator evidence

- What changed:
  - Ran `build/scripts/validate_quantized_artifact.py` via `kubectl exec` into the live runtime pod `gemma4-26b-a4b-gptq-87c45466d-wpkg6` on `cblevins-7900xtx`.
  - Validated both on-PVC artifacts against `--layout vllm-gptq --family gemma4-26b-a4b`. Both returned `ok: true` with one flat-shape warning each. Raw JSON in `.loom/local/validation/gemma4-26b-a4b-gptq/20260418-085841/`.
  - Confirmed the **active** serving artifact is `/models/gemma4-26b-a4b-gptq/gptq-w4-g128-attnfp16-clean` (extracted from `/proc/1/cmdline`).
  - Populated `.loom/60-validation-matrix.md` with the first two rows (clean + hybrid-v10) and added a findings block noting two validator follow-ups (family auto-detect gap, flat-warning noise).
  - Corrected the 2026-04-18 plan-refresh entry earlier today: the validator is **metadata/layout**, not cosine — see `30-implementation-plan.md` "Slice A1 execution path" for the A1-lite / A1-full split.
- Why:
  - Slice A1 acceptance asked for validator evidence. A1-lite (validate existing artifact) unblocks the matrix at ~10 min of cluster cost instead of committing to a 12–24 h A1-full re-quant on cblevins-7900xtx.
  - The `detected_family: null` finding is itself a tractable fix for the next slice and proves the validator is safe to rely on once families are registered.
- What's next:
  - Add `gemma4_text` / architecture-string markers to `FAMILY_PROFILES` so `--family` auto-detects (small PR, 1 file, adds a test).
  - Run `--run-generation` probe as a lightweight coherence gate (needs `torch` + a tokenizer import — confirm runtime pod has transformers before trying).
  - Move on to Slice B (Qwen3.5-9B port to gfx1100) or Slice C (OmniCoder-9B end-to-end) — user's call on priority.
- Sources:
  - `kubectl exec -n flexinfer-system gemma4-26b-a4b-gptq-87c45466d-wpkg6 -- python3 /tmp/validate_quantized_artifact.py --artifact-path /models/gemma4-26b-a4b-gptq/gptq-w4-g128-attnfp16-clean --layout vllm-gptq --family gemma4-26b-a4b --json`
  - `kubectl exec -n flexinfer-system gemma4-26b-a4b-gptq-87c45466d-wpkg6 -- sh -c 'cat /proc/1/cmdline'`
  - `kubectl get pvc -n flexinfer-system` → `gemma4-26b-a4b-gptq` (96Gi, nvme-1r-gpu), `gemma4-26b-a4b-gptq-cache` (50Gi, longhorn).
  - `build/scripts/validate_quantized_artifact.py:393-554` (validator entry point).
  - `cmd/flexinfer/commands/quantize.go:117-169` (CLI spec).

### gfx1100 quant pipeline multi-family plan refresh

- What changed:
  - Updated `00-index.md` Current Goal to target gfx1100 quant pipeline multi-family rollout (Gemma4 → Qwen3.5 → OmniCoder → Qwen3-14B regression → validation matrix).
  - Appended 2026-04-18 Execution Slice to `30-implementation-plan.md` with six priority targets, five delivery slices (A–E), acceptance gates, and open questions for `/feature-dev` to resolve before branching.
- Why:
  - Recent merge train (2026-04-13..18) landed dense GPTQ validation, artifact recovery, and gfx1100 vLLM env pins. Pipeline is stable enough to stop firefighting and start systematic family coverage with artifact-validator evidence.
- What's next:
  - `/feature-dev` should pick Slice A1 (drive `gemma4-26b-a4b-gptq` through full pipeline under `denseModulePolicy: validate`), answer the three open questions in the slice, then proceed family-by-family.
- Sources:
  - `git log --oneline --since="2026-04-13"`
  - `deploy/gpuprofiles/gfx1100.yaml`
  - `deploy/modelcaches/{gemma4-26b-a4b-gptq,omnicoder-9b-gptq,qwen35-9b-gptq,gemma4-31b-gptq}.yaml`
  - Commits: `551f6763`, `0378749e`, `0e8ec72a`, `f3b6c164`, `3e77d9da`, `b8ab9cf4`, `d5355aec`.
