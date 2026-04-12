# Research Brief

## Problem

We need a current, evidence-backed planning baseline for FlexInfer that is safe to use immediately in this workspace, including tool/runtime capabilities and known blockers.

## Questions

- What is the current local repo state and architecture context?
- Which MCP inventory path is actually available in this session?
- Is `codebase_memory` indexing/search ready for planning workflows?

## Constraints

- Work with current branch state (do not discard local changes).
- Treat unsupported/unavailable MCP resource APIs as hard constraints.
- Separate observed facts from assumptions.

## Method

- Ran `plan-loom-core` scripts to refresh `.loom/` scaffolding and workspace snapshot.
- Queried MCP resource discovery (`list_mcp_resources`, `list_mcp_resource_templates`).
- Used `loom` CLI fallback for server/tool inventory and counts.
- Ran `codebase_memory` stats/index start/poll checks for `repo_id=flexinfer`.
- Collected architecture anchors from `AGENTS.md`.

## Findings (Facts)

- FlexInfer architecture context remains six cooperating executables documented in `AGENTS.md`.
- Initial snapshot in this research run showed `master` behind `origin/master`; repository has since been reconciled and `master` is aligned at `a16b2d1`.
- MCP resource/template APIs returned empty collections, so loom-resource mode was not available through MCP resource reads.
- CLI fallback succeeded and reported `42` running servers and `445` tools.
- `codebase_memory` index readiness failed for this repo:
  - baseline stats require `repo_id`,
  - `repo_id=flexinfer` shows `0` chunks,
  - two index attempts failed (vector schema mismatch, then invalid point-id format),
  - subsequent stats calls returned transport closed.

## Update (2026-02-19, later in session)

- After recreating `codebase_memory_v1` with vector size `1536` and rebuilding `mcp-codebase-memory`, indexing succeeded:
  - `job_id=1869e8aca6a0ab14`
  - `chunks_total=1877`
  - `errors=0`
- Semantic lookup now returns expected symbols (for example `ModelReconciler` in `controllers/model_controller.go`).
- The remaining issue is bridge-specific: direct `functions.mcp__loom__*` calls in this chat still return `Transport closed`, while `loom tools call ...` works.

## Assumptions

- `repo_id=flexinfer` is the intended identifier for this workspace until explicitly changed.
- CLI inventory (`loom servers/tools`) is trustworthy enough for planning despite MCP resource discovery gaps.

## Update (2026-02-20)

- Three serverless cold-start bugs found and fixed during Qwen3-30B-A3B deployment:
  1. Controller `desiredReplicas()` reaped Loading models when `LastActiveTime` exceeded `idleTimeout`.
  2. Proxy `triggerScaleUp()` silently swallowed Kubernetes conflict errors, leaving `LastActiveTime` stale.
  3. GPUGroup queue path used global timeout, ignoring per-model `ColdStartTimeoutSeconds`.
- Performance finding: Longhorn mmap overhead for 18.7GB GGUF is 15-20 minutes. Switching to `local-path` (direct NVMe) reduces load to ~3 minutes.
- Qwen3-30B-A3B benchmark on AMD gfx1100: 108 tok/s generation, 72.5 tok/s prompt processing, Q4_K_M quantization.
- All fixes merged via MR !40, CI pipeline #1787 green, deployed to cluster via Flux.

## Recommendation

- Use this `.loom` pack as the planning baseline now.
- Prioritize a short recovery task to restore `codebase_memory` indexing before relying on semantic search-driven workflows.
- Continue using shell-native discovery as primary mechanism until index health is verified.
- For new large GGUF models (>10GB), use `storageClass: local-path` to avoid Longhorn mmap overhead.

## Sources

- [S1] `AGENTS.md:7`
- [S2] `AGENTS.md:12`
- [S3] `AGENTS.md:13`
- [S4] `AGENTS.md:14`
- [S5] `AGENTS.md:15`
- [S6] `AGENTS.md:16`
- [S7] `.loom/00-workspace-snapshot.md:11`
- [S8] `.loom/00-workspace-snapshot.md:12`
- [C1] `python /Users/cblevins/.codex/skills/plan-loom-core/scripts/workspace_snapshot.py --root .`
- [C2] `loom servers --json | jq '.servers | length'` -> `42`
- [C3] `loom tools list --json --limit 500 --page 1 | jq '{server,page,pageSize,totalTools,totalPages,serverCount,cachedAt}'`
- [C4] `functions.list_mcp_resources({})` -> `resources: []`
- [C5] `functions.list_mcp_resource_templates({})` -> `resourceTemplates: []`
- [C6] `functions.mcp__loom__codebase_memory__codebase_index_poll({job_id:\"5380e4246b4b7cf1\"})`
- [C7] `functions.mcp__loom__codebase_memory__codebase_index_poll({job_id:\"237b41f443376c18\"})`
- [C8] `loom tools call codebase_memory__codebase_index_poll --args '{"job_id":"1869e8aca6a0ab14"}' --json`
- [C9] `loom tools call codebase_memory__codebase_get_definition --args '{"repo_id":"flexinfer","symbol":"ModelReconciler","limit":5}' --json`

## Update (2026-04-02): Gemma 4 and TurboQuant

### Question

- Can FlexInfer support the new Gemma 4 family in vLLM inference plus the existing abliteration, quantization, and finetune pipeline?
- Is Google Research's TurboQuant something we can implement on Radeon/ROCm in this codebase now?

### Findings (Facts)

- Upstream Gemma 4 checkpoints are Hugging Face `transformers` models, not GGUF or vLLM-native artifacts:
  - `google/gemma-4-31B` and `google/gemma-4-31B-it` are `Gemma4ForConditionalGeneration` with `gemma4_text` + `gemma4_vision`, 256K context, and ~32.68 GB safetensors payload.
  - `google/gemma-4-26B-A4B-it` is also `Gemma4ForConditionalGeneration`, but its text config enables MoE (`enable_moe_block=true`, `num_experts=128`, `top_k_experts=8`) and has ~26.54 GB safetensors payload.
  - `google/gemma-4-E4B-it` is `Gemma4ForConditionalGeneration` with `gemma4_text`, `gemma4_vision`, and `gemma4_audio`, 128K context, and ~8.00 GB safetensors payload.
- Current upstream Gemma 4 configs declare `transformers_version: 5.5.0.dev0`, so they target a newer Transformers stack than older Gemma/Qwen paths.
- Current stable/released vLLM support was not there in the versions inspected initially:
  - `vllm` `v0.18.0` registry includes Gemma 1/2/3/3n entries but no `Gemma4*`.
  - The model file list in `vllm/model_executor/models` at `v0.18.0` likewise stops at `gemma3*`.
- Current `vllm` `main` has since moved:
  - local clone on 2026-04-02 contains `gemma4.py`, `gemma4_mm.py`, and `gemma4_utils.py`
  - local registry includes both `Gemma4ForCausalLM` and `Gemma4ForConditionalGeneration`
  - local `origin/main` is at commit `08ed2b9` titled `feat(models): implement Google Gemma 4 architecture support (MoE, Multimodal, Reasoning, Tool-Use) (#38826)`
- FlexInfer's current vLLM integration is tuned around generic text serving plus explicit Qwen 3.5 compatibility work:
  - runtime image pins `vllm==0.17.0+rocm700` with extra `transformers>=5.0`;
  - the entrypoint patcher is Qwen 3.5-specific, not a general multimodal/Gemma 4 shim.
- FlexInfer's current post-download pipelines are still fundamentally text-CausalLM oriented:
  - abliteration loads with `AutoModelForCausalLM` and only has a model policy for Qwen 3.5;
  - AWQ quantization is pinned to `transformers==4.51.3`, which is below Gemma 4's declared config requirement;
  - finetuning uses Unsloth or `AutoModelForCausalLM` plus TRL `SFTTrainer` over plain text columns, not multimodal/audio processing.
- FlexInfer already has a good control-plane base for adapter-style finetuning at inference time:
  - vLLM backend advertises LoRA support and load/unload endpoints;
  - `LoRAAdapter` controller hot-loads adapters onto ready replicas.
- TurboQuant is a runtime KV-cache compression method, not a model-cache weight quantizer:
  - Google Research describes it as KV-cache/vector compression with 2.5/3.5-bit effective precision, and the blog explicitly frames the gains around KV-cache bottlenecks and H100 runtime measurements.
  - This is materially different from FlexInfer's existing `ModelCache` quantization pipeline (GGUF/AWQ/GPTQ/EXL2/FP8), which transforms stored model weights before inference.
- I did not find an official Google code release for TurboQuant in the linked Google Research post or the arXiv pages during this spike.
- I found community implementations, but they reinforce that this is still an experimental runtime topic:
  - `OmarHory/turboquant` implements the paper in Python with Triton CUDA kernels and benchmarks on NVIDIA A40/A100, not ROCm;
  - `arozanov/turboquant-mlx` implements a Metal/MLX path for Apple GPUs;
  - neither is an official Google release or a drop-in vLLM ROCm backend.

### Implications

- Gemma 4 support should still be split into two tracks:
  1. **Inference-only support**: now primarily blocked on getting a Gemma4-capable `vllm` source tree to run cleanly against FlexInfer's ROCm/PyTorch base image.
  2. **Pipeline support**: separate work to make abliteration, quantization, and finetune stages understand Gemma 4's multimodal configs.
- For Gemma 4, the safest first target is `google/gemma-4-31B-it` or `google/gemma-4-31B-it` in text-only mode, because:
  - it is dense rather than MoE,
  - it has no audio tower,
  - it avoids the extra routing/quantization complexity of `26B-A4B-it` and the multimodal audio complexity of `E4B-it`.
- `26B-A4B-it` likely needs extra quantization policy work for MoE layers and a careful decision on whether experts stay BF16 while dense projections quantize.
- `E4B-it` is the highest-risk target because it adds audio config and would require multimodal/audio request plumbing in addition to model loading.
- TurboQuant does not belong in `controllers/modelcache_quantization.go` style workflows. If we pursue it, it belongs in the vLLM runtime path as a KV-cache compression capability exposed through model config / backend flags.

### Recommended Next Actions

- Short term:
  - Treat Gemma 4 vLLM support as **not ready** in current FlexInfer runtime.
  - Do not promise AWQ support for Gemma 4 from the current ROCm image; it is pinned too old.
  - Consider GPTQ/abliteration/finetune support exploratory only, and only after building a Gemma 4-capable Transformers/TRL image.
- Best first implementation slice:
  - Use a source-based FlexInfer vLLM runtime path against current `vllm` `main` or a small fork from it.
  - Fix the current ROCm base compatibility mismatch in `vllm.env_override` first.
  - Keep the experimental image off the Qwen3.5 fast-path dependency bundle so `transformers@main` is not downgraded by older stable pins like `compressed-tensors` and `outlines_core`.
  - Smoke test `google/gemma-4-E4B-it` or `google/gemma-4-31B-it` next, depending on whether the goal is fit-on-24GB or simplest text path.
  - Only then extend the model-cache pipeline for Gemma 4 text-only GPTQ and LoRA/QLoRA.
- TurboQuant:
  - Track as an R&D item, not a near-term product feature.
  - Gate implementation on either:
    - an official/open kernel implementation, or
    - a deliberate FlexInfer-specific ROCm implementation plan inside a vLLM fork.
  - If we prototype it ourselves, scope it narrowly to KV-cache compression for long-context text models on Radeon, not to the general quantization pipeline.

### Sources

- Hugging Face model cards and configs:
  - https://huggingface.co/google/gemma-4-31B
  - https://huggingface.co/google/gemma-4-31B-it
  - https://huggingface.co/google/gemma-4-26B-A4B-it
  - https://huggingface.co/google/gemma-4-E4B-it
  - https://huggingface.co/api/models/google/gemma-4-31B
  - https://huggingface.co/api/models/google/gemma-4-26B-A4B-it
  - https://huggingface.co/api/models/google/gemma-4-E4B-it
- Transformers upstream:
  - https://raw.githubusercontent.com/huggingface/transformers/main/src/transformers/models/auto/modeling_auto.py
  - https://raw.githubusercontent.com/huggingface/transformers/main/src/transformers/models/gemma4/configuration_gemma4.py
  - https://raw.githubusercontent.com/huggingface/transformers/main/src/transformers/models/gemma4/modeling_gemma4.py
- vLLM upstream:
  - https://raw.githubusercontent.com/vllm-project/vllm/v0.18.0/vllm/model_executor/models/registry.py
  - https://api.github.com/repos/vllm-project/vllm/contents/vllm/model_executor/models?ref=v0.18.0
  - https://api.github.com/repos/vllm-project/vllm/contents/vllm/model_executor/models?ref=main
- TurboQuant:
  - https://research.google/blog/turboquant-redefining-ai-efficiency-with-extreme-compression/
  - https://arxiv.org/abs/2504.19874
  - https://arxiv.org/abs/2502.02617
- Local implementation anchors:
  - `backend/vllm.go`
  - `build/Dockerfile.runtime`
  - `build/scripts/vllm-patched-entrypoint.sh`
  - `build/scripts/patch_vllm_env_override_torch29.py`
  - `build/Dockerfile.quantizer-awq-rocm`
  - `build/Dockerfile.quantizer-gptq-rocm`
  - `build/scripts/abliterate.py`
  - `build/scripts/finetune.py`
  - `controllers/lora_controller.go`

### Implementation Update (2026-04-02)

- FlexInfer now has an explicit `gfx1100-gemma4-experimental` runtime profile that:
  - installs `vllm` from source,
  - installs `transformers` from source,
  - carries an explicit experimental `turboquant` KV-cache intent flag without claiming backend support,
  - skips the Qwen3.5 fast path so the experimental image can preserve a mainline Transformers install.
- Local clone verification changed the upstream picture:
  - `vllm` `main` already contains Gemma4 model files and registry entries.
  - The active blocker is compatibility between current `vllm` `main` and the AMD Navi base image's PyTorch APIs.
  - The first patch had to be tightened because torch 2.9's `CaptureOutput` exists but does not expose `get_runtime_env`; the safe path is to skip that monkeypatch when the method is absent.
- The experimental packaging path also needed correction:
  - re-installing the stable vLLM-adjacent dependency bundle after `transformers@main` was downgrading `transformers` back below Gemma4's declared requirement,
  - so the experimental profile now uses a minimal extra-dependency mode rather than the full stable runtime bundle.
- TurboQuant remains an experimental runtime-only topic:
  - Google sources reviewed here did not provide an official implementation we can consume directly,
  - a community CUDA/Triton + vLLM implementation exists at `0xSero/turboquant`, but that is reference material rather than ROCm-ready support.
- Detailed operator notes live in `.loom/gemma4-inference-experimental-plan-2026-04-02.md`.

### Validation Update (2026-04-02, later)

- The experimental image `registry.harbor.lan/flexinfer/runtime:rocm-gfx1100-gemma4-experimental` built successfully.
- Image-level validation confirmed:
  - `transformers==5.5.0.dev0`
  - `vllm==0.1.dev1+g7b743ba95.d20260402`
- installed Gemma4 files: `gemma4.py`, `gemma4_mm.py`, `gemma4_utils.py`

## Update (2026-04-09): Gemma4 Abliteration / Quantization Reset

### Question

- Why have Gemma4 `26B-A4B` and `31B` ablit/quant pipelines consumed ~2 weeks with repeated retries and low durable progress?
- What permanent fixes belong in source control instead of more operator intervention?

### Findings (Facts)

- The repeated failures were not one bug; they were a chain of integrity and runtime gaps:
  1. **Missing-source retry loop**:
     - `31B` could enter abliteration with `.download_complete` present but source weights absent, then loop in `abliteration_waiting_for_download`.
     - The controller now detects that specific failure and resets the pipeline back to download instead of retrying abliteration in place.
     - Local fix anchor: `controllers/modelcache_abliteration.go:461-478`, `controllers/modelcache_abliteration.go:641-648`.
  2. **Partial shard reuse**:
     - `26B` cache reuse logic originally treated “marker exists + any weight file exists” as complete.
     - Live cluster evidence showed a cache with only `13` shard files present while `model.safetensors.index.json` described a larger sharded checkpoint; abliteration then failed on a missing shard file.
     - Local fix anchor: `controllers/modelcache_shared_pvc.go:761-872`, `pkg/quantization/abliteration.go:473-575`.
  3. **Runtime self-mutation**:
     - Gemma4 jobs still install `git+https://github.com/huggingface/transformers.git` at runtime when the bundled environment does not recognize `model_type='gemma4'`.
     - Current runtime image still bakes an older pinned Transformers commit for quantizer flows.
     - Local anchors: `build/scripts/abliterate.py:388-405`, `build/Dockerfile.runtime:341-345`.
- External Hugging Face documentation confirms why shard completeness must be explicit:
  - Transformers big-model docs say sharded checkpoints consist of multiple shard files plus `model.safetensors.index.json`, and that the index file’s `weight_map` determines where weights are stored.
  - That means a cache is not valid just because one safetensors file exists; every shard referenced by `weight_map` must be present.
- External Gemma4 documentation confirms the runtime packaging direction:
  - The current Gemma 4 model card says to use Gemma4 with the latest Transformers and shows `pip install -U transformers torch accelerate`.
  - This supports pre-baking a Gemma4-capable Transformers version into the runtime image; it is not evidence that per-job `pip install` should remain in the hot path.
- Current live cluster state at this checkpoint:
  - `31B` moved from false-ready waiting loops into real abliteration progress after clean redownload and recovery; it progressed through model load and activation capture.
  - `26B` is now correctly back in `Provisioning/download` after the shard-integrity fix was deployed, rather than continuing against a corrupted partial cache.

### Implications

- The durable problem statement is:
  - **cache integrity checks were too weak**, and
  - **runtime image support for Gemma4 was too implicit**.
- The work should be split into two permanent tracks:
  1. **Pipeline integrity**:
     - single reusable shard-completeness validator,
     - phase resets on integrity violations,
     - status/events that surface expected vs missing shards.
  2. **Runtime determinism**:
     - no Git HEAD package installation during abliteration/quantization runs,
     - prebuilt runtime image that already recognizes Gemma4,
     - explicit version pin and rollout policy.
- The biggest planning risk is continuing to mix:
  - source-cache recovery logic,
  - runtime package upgrades,
  - and live job retries
  in the same debugging loop. That produces movement, but not durable progress.

### Recommended Next Actions

1. Lock in cache-integrity enforcement as a shared helper, not duplicated shell snippets.
2. Remove runtime `transformers` mutation from the Gemma4 job path by baking a known-good Gemma4-capable build into the runtime image and exposing the version explicitly in profile/image metadata.
3. Add operator-visible status for shard completeness and checkpoint stage so a partial download cannot masquerade as a healthy cache.
4. Run a narrow validation matrix:
   - `31B` dense path on `gfx906`
   - `26B-A4B` MoE path on `gfx1100`
   - each with download -> abliteration -> quantization phase handoff checks
5. Stop accepting “job restarted” as evidence of progress; require one of:
   - completed shard set,
   - model loaded,
   - activation counters increasing,
   - or quantization artifacts appearing.

### Sources

- Local code:
  - `controllers/modelcache_abliteration.go:461-478`
  - `controllers/modelcache_abliteration.go:641-648`
  - `controllers/modelcache_shared_pvc.go:761-872`
  - `pkg/quantization/abliteration.go:473-575`
  - `build/scripts/abliterate.py:388-405`
  - `build/Dockerfile.runtime:341-345`
  - `deploy/modelcaches/gemma4-26b-a4b-gptq.yaml:1-70`
  - `deploy/gpuprofiles/gfx906.yaml:51-64`
- Commands / live evidence:
  - `python "$CODEX_HOME/skills/plan-loom-core/scripts/workspace_snapshot.py" --root .`
  - `kubectl describe modelcache gemma4-26b-a4b-gptq -n flexinfer-system`
  - `kubectl logs -n flexinfer-system gemma4-26b-a4b-gptq-abliterate-hh4wn --tail=80`
  - `kubectl logs -n flexinfer-system gemma4-26b-a4b-gptq-downloader-85mm9 --tail=80`
  - `kubectl logs -n flexinfer-system gemma4-31b-gptq-abliterate-28pj6 --tail=120`
  - `kubectl logs -n flexinfer-system gemma4-31b-gptq-downloader-mmnl6 --tail=120`
  - `git rev-parse HEAD` -> `fdadc03d2d760489e0075064e189310a15dfad74`
- External:
  - https://huggingface.co/docs/transformers/main/big_models
  - https://huggingface.co/google/gemma-4-E4B

## Update (2026-04-07): Gemma4 Abliteration Stability, Cache Placement, and GPU Node Risk

### Question

- Why are the active Gemma4 GPTQ pipelines not visibly progressing?
- Which failures are real runtime stalls versus misleading controller progress?
- What should the next improvement round target while the jobs continue to run?

### Findings (Facts)

- `gemma4-31b-gptq` is not truly stuck at the low displayed percentage.
  - Current live logs show it resuming from `perplexity_validated`, reloading the model, restoring activation and refusal-direction checkpoints, and entering `Running pre-abliteration baseline perplexity check...`.
  - The reported `2-3%` is therefore a coarse/stale controller progress signal, not the true internal stage.
- `gemma4-26b-a4b-gptq` reproduced a real runtime hang on `gfx1100`.
  - It loads fully, reaches `Collecting harmful activations... 0/128`, and then stops making progress.
  - In-container process inspection showed the Python worker in Linux `D` state (`uninterruptible sleep`), which is consistent with a kernel/device or low-level I/O wait rather than Python compute.
- The first occurrence of the `26B` stall coincided with host failure on `cblevins-7900xtx`.
  - Kubernetes marked the node `NotReady` / `NodeStatusUnknown`.
  - SSH to `192.168.50.125` timed out during the incident.
  - The cluster API itself temporarily became unreachable from this workstation, and direct `kubectl` on `cblevins-5930k` returned `etcdserver: leader changed` during the reboot/failover window.
- The current strongest software-side correlation is the activation capture mode on `gfx1100`.
  - `build/scripts/abliterate.py` supports both `hooks` and `hidden_states`.
  - The default path in `pkg/quantization/abliteration.go` is still `hooks`.
  - `gfx1100` had no profile override for activation capture mode before this session.
  - The hang occurs exactly at the first real forward pass after entering activation capture, which is the point where the `hooks` path becomes active.
- Live mitigation has already been applied:
  - `gfx1100` `GPUProfile` now carries `ABLITERATION_ACTIVATION_CAPTURE_MODE=hidden_states`.
  - Local repo manifest `deploy/gpuprofiles/gfx1100.yaml` was updated to match.
  - `gemma4-26b-a4b-gptq` was live-patched to target `cblevins-5930k` instead of landing generically on any `gfx1100` node.
  - The stale `VolumeAttachment` for the 26B PVC had to be manually deleted more than once to break attachment stickiness to `cblevins-7900xtx`.
- Storage and cache placement remain part of the problem surface, but they are no longer the primary blocker for the active Gemma jobs.
  - `gemma4-26b-a4b-gptq` still declares `clusterStorageClassName: bulk-1r-stable`.
  - `gemma4-31b-gptq` already declares `clusterStorageClassName: nvme-1r-gpu`.
  - Earlier cleanup work removed legacy GPU-pinned source caches from active reconciliation and steered rebuildable cache storage toward worker NVMe, but the active Gemma source caches still use a mixed policy.
- MCP/codebase inventory in this session remains degraded:
  - `functions.list_mcp_resources({})` and `functions.list_mcp_resource_templates({})` still returned empty results.
  - `mcp__loom__codebase_memory__codebase_stats(repo_id=...)` failed with `Transport closed`.
  - Planning in this run therefore relied on repo-local manifests, logs, and live cluster commands.

### Implications

- The next improvement round should treat `gfx1100` abliteration stability as a first-class runtime problem, not just an operator retry problem.
- Generic `flexinfer.ai/gpu.arch=gfx1100` selection is too coarse for long-running abliteration jobs when one of the matching hosts is also an unstable control-plane/etcd node.
- Progress reporting needs to distinguish:
  - checkpoint-based internal progress,
  - externally visible controller phase/progress,
  - and actual liveness/hang detection.
- Storage work should continue, but the immediate Gemma reliability bottleneck is the combination of:
  - runtime forward-pass behavior on `gfx1100`,
  - node-health-aware placement,
  - and stale CSI/Longhorn attachment cleanup after host failure.

### Recommended Next Actions

- Runtime stability:
  - Validate `ABLITERATION_ACTIVATION_CAPTURE_MODE=hidden_states` on `gfx1100` by ensuring `26B` progresses past `harmful activations 0/128`.
  - If it still hangs, make `gfx1100` abliteration unsupported or experimental for Gemma4 until ROCm/amdgpu root cause is isolated.
  - Add a profile-level activation-capture policy matrix by GPU architecture instead of relying on a global default.
- Scheduling and node safety:
  - Stop placing long-running abliteration jobs on generic `gfx1100` selectors alone.
  - Introduce explicit node-class or health-gated placement for risky jobs so a flapping GPU control-plane host is not chosen automatically.
  - Consider removing control-plane/etcd responsibility from `cblevins-7900xtx` if it is expected to host unstable experimental GPU workloads.
- Storage and attachment recovery:
  - Continue moving rebuildable source caches off GPU-node NVMe where possible.
  - Split storage policy more explicitly between:
    - rebuildable source caches,
    - finished quantized artifacts,
    - and hot serving caches.
  - Add safer controller/operator handling for stale `VolumeAttachment` / multi-attach residue after node loss.
- Observability:
  - Emit finer-grained abliteration progress and last-checkpoint timestamps into status.
  - Add explicit hang detection for long periods with no checkpoint/log movement plus low CPU.
  - Alert when GPU worker/control-plane nodes transition `NotReady` while owning active `ModelCache` jobs.

### Sources

- [C10] `python /Users/cblevins/.codex/skills/plan-loom-core/scripts/workspace_snapshot.py --root .`
- [C11] `functions.list_mcp_resources({})` -> `resources: []`
- [C12] `functions.list_mcp_resource_templates({})` -> `resourceTemplates: []`
- [C13] `mcp__loom__codebase_memory__codebase_stats({repo_id:"services/flexinfer"})` -> `Transport closed`
- [C14] `kubectl -n flexinfer-system logs gemma4-26b-a4b-gptq-abliterate-zwvpv --tail=160`
- [C15] `kubectl -n flexinfer-system exec gemma4-26b-a4b-gptq-abliterate-zwvpv -- sh -lc 'ps -o pid,ppid,stat,%cpu,%mem,etime,cmd -C python3; cat /cache/gemma4-26b-a4b-gptq/.abliteration-checkpoint.json'`
- [C16] `kubectl -n flexinfer-system logs gemma4-31b-gptq-abliterate-sxxwv --tail=160`
- [C17] `kubectl get nodes -o wide | rg 'cblevins-7900xtx|cblevins-5930k|cblevins-radeonvii'`
- [C18] `ssh -o BatchMode=yes -o ConnectTimeout=5 cblevins-7900xtx 'hostname && uptime && systemctl is-active k3s && uname -a'` -> SSH timeout during incident
- [C19] `ssh cblevins-5930k 'kubectl get nodes -o wide | grep -E "cblevins-(5930k|7900xtx|radeonvii)"'` -> `etcdserver: leader changed` during reboot/failover
- [S20] `build/scripts/abliterate.py`
- [S21] `pkg/quantization/abliteration.go`
- [S22] `deploy/gpuprofiles/gfx1100.yaml`
- [S23] `deploy/modelcaches/gemma4-26b-a4b-gptq.yaml`
- [S24] `deploy/modelcaches/gemma4-31b-gptq.yaml`
- Node-level smoke test on `cblevins-7900xtx` completed successfully after importing the image into the node's `k3s` containerd store.
- Live generation with `google/gemma-4-E4B-it` on one Radeon 7900XTX also completed successfully through the direct `vllm.LLM(...)` path.

Measured/observed from the generate job:

- model weights downloaded in about `126.99s`
- safetensors load took about `15.07s`
- model loading consumed about `16.59 GiB` VRAM
- `torch.compile` plus initial profiling/warmup took about `85.03s`
- engine init took about `107.97s`
- available GPU KV cache memory was about `2.64 GiB`
- reported GPU KV cache size was about `28,848` tokens
- observed estimated output speed was about `15.25 toks/s` for the one-shot prompt

Remaining operational issues:

- the image tag was not available from Harbor immediately, so the successful node test used a direct import into `k3s` containerd rather than a normal registry pull path
- current `vllm` `main` reports `VLLM_USE_TRITON_FLASH_ATTN` as an unknown env var
- this PyTorch build deprecates `PYTORCH_HIP_ALLOC_CONF` in favor of `PYTORCH_ALLOC_CONF`
- unauthenticated HF Hub access worked for this run but should not be relied on for repeated testing

### Validation Update (2026-04-02, HF + context follow-up)

- HF access for the Gemma4 debug jobs is now wired to the cluster secret `flexinfer-system/hf-token`:
  - `HF_TOKEN` and `HUGGINGFACE_HUB_TOKEN` are injected from the secret
  - debug jobs now set `HF_HUB_DISABLE_TELEMETRY=1` and `HF_HUB_DISABLE_XET=1`
- The first implementation leaked the token into logs indirectly by passing `hf_token=...` into `vllm.LLM(...)`, which causes `vllm` to print the value in its non-default args banner.
  - The debug jobs now rely on environment-based HF auth instead of passing `hf_token` explicitly.
  - This removes the secret from vLLM argument logging.
- The Gemma4 smoke/generate/context jobs now:
  - source `/etc/flexinfer/runtime.env`
  - explicitly unset stale `VLLM_USE_TRITON_FLASH_ATTN`
  - replace deprecated `PYTORCH_HIP_ALLOC_CONF` with `PYTORCH_ALLOC_CONF` at runtime
  - mount a cache volume at `/models`
  - set `HF_HOME`, `HF_HUB_CACHE`, `HUGGINGFACE_HUB_CACHE`, and `TRANSFORMERS_CACHE` under `/models/.cache/huggingface`
  - preload `AutoConfig.from_pretrained(...)` before engine init to reduce false negatives from one-off Hub metadata failures

Context optimization findings on `google/gemma-4-E4B-it` text-only mode:

- Shared RWX NFS cache was the wrong tier for this experiment:
  - a probe backed by `llm-models-nfs` spent minutes blocked in uninterruptible I/O reading the 15.99 GB safetensors blob
  - context/load experiments should use a node-local cache tier instead
- The heavy Gemma4 probes now use the node-local PVC `mlc-models-nvme-7900xtx`
  - this removed the NFS read bottleneck and made model loading behave predictably again

Measured results:

- `32k + fp8 KV cache + gpu_memory_utilization=0.95 + text-only multimodal limits`
  - failed with a ROCm GPU memory fault / GPU hang during engine startup
  - this is a runtime stability failure, not an HF auth problem
- `32k + auto KV cache + gpu_memory_utilization=0.95 + text-only multimodal limits`
  - succeeded through weight download/load and compile
  - then failed cleanly during KV cache init with:
    - `Available KV cache memory: -0.91 GiB`
    - `ValueError: No available memory for the cache blocks`
  - interpretation: on this stack, 32k context is not viable on a single 7900 XTX without freeing additional GPU memory
- `32k + auto KV cache + gpu_memory_utilization=0.95 + cpu_offload_gb=4`
  - switched vLLM to `UVAOffloader`
  - reduced GPU weight residency from about `15.52 GiB` to about `11.5 GiB`
  - compile completed successfully and the run got past the non-offloaded memory failure point
  - but engine init then failed with:
    - `AssertionError: Cannot re-initialize the input batch when CPU weight offloading is enabled`
  - interpretation:
    - CPU offload does free enough memory to move the bottleneck
    - current upstream `vllm` V1 engine still has a functional limitation on this path, so offload is not a drop-in fix yet

Practical conclusion so far:

- Near-term safe levers for longer context on Radeon are:
- Near-term safe levers for longer context on Radeon are:
  - node-local HF cache
  - text-only multimodal limits (`image=0,audio=0,video=0`)
  - `mm_processor_cache_gb=0`
  - `enable_prefix_caching=false`
  - `cudagraph_mode=NONE`
- CPU offload is a promising memory lever, but current `vllm` V1 behavior blocks it for this exact path
- `fp8` KV cache on this Gemma4 + ROCm path is not ready to treat as a reliable context-expansion mechanism yet

TurboQuant follow-up:

- There is now a more credible community runtime path than the earlier minimal references:
  - `Alberto-Codes/turboquant-vllm` is an actively released vLLM plugin repo that presents TurboQuant as a drop-in `--attention-backend CUSTOM` plugin
  - it claims 3.76x KV compression, asymmetric K/V bit widths, validation across 8 models, and a vLLM plugin install path
  - its README frames the current path around Triton kernels and consumer GPUs, not an upstream ROCm/vLLM integration
- Current packaging and compatibility facts matter for FlexInfer:
  - the published package is `turboquant-vllm==1.4.0`
  - it declares Python `>=3.12`, which matches the current Gemma4 experimental ROCm base image
  - it also declares `transformers>=4.57,<5` and `vllm>=0.18`
  - that conflicts directly with the Gemma4 runtime path, which currently needs `transformers@main` / `5.5.0.dev0`
- The safe integration pattern for FlexInfer is therefore:
  - keep TurboQuant in a separate experimental image profile
  - install it with `--no-deps` so it cannot silently downgrade the Gemma4 runtime stack
  - verify the `vllm.general_plugins` entry point (`tq4_backend`) exists in-image before any cluster run
- Build-system note:
  - upstream `vllm` ROCm source builds do not reliably constrain HIP targets via `PYTORCH_ROCM_ARCH` alone
  - in practice, the FlexInfer image needed `CMAKE_ARGS=-DGPU_TARGETS=gfx1100` during `pip install /tmp/vllm-src`
  - without that, the build faned out across multiple architectures (`gfx90a`, `gfx942`, `gfx950`, `gfx110*`, `gfx120*`) and wasted substantial compile time
- Plugin implementation details suggest the first validation target is strictly "boot/import/register", not throughput:
  - the backend code imports `vllm.v1.attention.backends.flash_attn` and registers through `vllm.v1.attention.backends.registry`
  - it relies on Triton kernels at import/runtime, with explicit fused-paged and INT8-prefill feature gates
  - it is not enough to check that the wheel installs; we need to confirm registration and at least one manual `register_tq4_backend()` call on the ROCm node
- Validation results on `cblevins-7900xtx`:
  - the dedicated image `registry.harbor.lan/flexinfer/runtime:rocm-gfx1100-gemma4-turboquant-experimental@sha256:cab1eeff6e0f5430d8d1d621400cd5952de38a5dac3251f375cd57341534ee6f` built successfully
  - the cluster smoke job confirmed:
    - `turboquant-vllm=1.4.0`
    - `tq4_backend` present in `vllm.general_plugins`
    - manual `register_tq4_backend()` succeeded on the ROCm node
    - `AutoConfig.from_pretrained("google/gemma-4-E4B-it")` still resolved as `Gemma4Config`
  - a deeper `attention_backend="CUSTOM"` generate probe failed during vLLM engine startup with:
    - `RuntimeError: Calling torch.geqrf on a CPU tensor requires compiling PyTorch with LAPACK`
  - interpretation:
    - the plugin does more than import; it reaches Gemma4 attention layer construction on ROCm
    - the current blocker is a CPU-side linear algebra dependency in `turboquant_vllm.quantizer._generate_rotation_matrix`, not a Triton kernel crash and not Gemma4 config incompatibility
- This still does not change the product recommendation for FlexInfer:
  - treat TurboQuant as a runtime-fork / plugin experiment
  - do not model it as a `ModelCache` quantization backend
  - do not assume ROCm readiness until we validate its Triton kernels on the 7900 XTX stack

TurboQuant allocator follow-up:

- After the GPU-QR fix, the next end-to-end blocker moved into vLLM KV cache setup:
  - `NotImplementedError: The page size of the layer is not divisible by the maximum page size`
- Local code inspection across upstream `vllm` and the TurboQuant plugin showed the intended fix mechanism is `page_size_padded`, not changing a spec's real payload size:
  - `AttentionSpec.page_size_bytes` returns `page_size_padded` when present
  - hybrid KV unification checks `page_size_bytes`
  - `SlidingWindowSpec -> FullAttentionSpec` normalization preserves `page_size_padded`
- The previous Gemma4 padding helper in the FlexInfer TurboQuant shim never activated for the live checkpoint because it keyed on `model_type == "gemma4"`, while `google/gemma-4-E4B-it` text config reports `model_type == "gemma4_text"`.
- The corrected build-time patch now does two things:
  - recognizes both `gemma4` and `gemma4_text`
  - stamps a common Gemma4 allocator page size into emitted decoder-attention specs inside `register_tq4_backend()`
- The corrected shim also keeps `real_page_size_bytes` as the true packed payload size and uses `page_size_padded` separately for allocator-visible alignment, matching upstream `vllm` behavior.
- A new runtime image rebuild and validation cycle for `registry.harbor.lan/flexinfer/runtime:rocm-gfx1100-gemma4-turboquant-experimental` is in progress as of 2026-04-02.

TurboQuant ROCm attention follow-up:

- Local `vllm` inspection plus background research confirmed a cleaner ROCm path than continuing to patch upstream `flash_attn_triton_amd`:
  - generic ROCm `fa_utils.py` imports upstream `flash_attn.flash_attn_varlen_func`
  - `vllm`'s ROCm `aiter` backend instead gathers paged KV into dense K/V for prefill/extend, then calls `rocm_aiter_ops.flash_attn_varlen_func`
  - this better matches the dense K/V fallback already added in the FlexInfer TurboQuant shim
- The build-time TurboQuant patch was updated again so that on ROCm:
  - paged KV is still reconstructed to dense K/V when needed
  - the actual attention call uses `vllm._aiter_ops.rocm_aiter_ops.flash_attn_varlen_func`
  - non-ROCm backends keep the previous generic `flash_attn_varlen_func` compatibility path
- Validation outcome after pushing image `registry.harbor.lan/flexinfer/runtime:rocm-gfx1100-gemma4-turboquant-experimental@sha256:a450aaa158b72d210507720a8bd1516a776262225f7adc5ed97128765b431bf2`:
  - the old ROCm upstream FlashAttention failure disappeared
  - specifically, the previous `flash_attn_triton_amd/fwd_prefill.py` assertion on `n_extra_tokens` (`int32[]` vs `int64[]`) did not recur
  - an initial rerun on `cblevins-5930k` failed earlier on free-VRAM headroom at `gpu_memory_utilization=0.80`, so the probe was repeated at `0.70`
  - at `0.70`, the engine initialized successfully, loaded weights, created KV cache, and entered prompt processing under the TurboQuant `CUSTOM` backend
- The current long pole on this ROCm `aiter` path is runtime JIT compilation:
  - `aiter` starts building `module_aiter_enum` and then `mha_varlen_fwd_bf16_nlogits_nbias_mask_nlse_ndropout_skip_nqscale`
  - it emits a warning that `-mllvm -amdgpu-coerce-illegal-types=1` is unsupported by the image's `hipcc`, but that warning is not immediately fatal because `aiter` filters unsupported HIP flags in `aiter/jit/core.py`
  - as of the latest probe, the job remained running in `aiter` kernel build rather than failing inside TurboQuant attention execution
- Practical interpretation:
  - the ROCm `aiter` dispatch is the right correctness direction for TurboQuant on Gemma4
  - the next optimization target is likely `aiter` JIT ergonomics or caching, not another TurboQuant KV layout patch

TurboQuant ROCm correctness-first fallback:

- Further validation showed the `aiter` path was still not usable for Gemma4 on the 7900 XTX stack:
  - after the ROCm dispatch change, the live run progressed far enough to JIT-build `aiter` kernels
  - but execution still failed inside `aiter` with `RuntimeError: invalid argument for fmha_fwd`
  - the failure remained at the TurboQuant attention call site, even after a 512-head fallback split, which implies the remaining unsupported path included 256-head layers too
- Local upstream evidence helps explain this:
  - `vllm`'s ROCm `aiter` backend only advertises supported head sizes `[64, 128, 256]`
  - Gemma4 mixes 256-dim sliding-attention layers with 512-dim full-attention layers
  - the experimental TurboQuant backend does not use the same full prefill/extend/decode choreography as upstream `vllm` ROCm attention, so matching `aiter` kernel expectations exactly is non-trivial
- The next experimental step therefore removed ROCm custom attention kernels entirely from the TurboQuant path:
  - disabled fused paged decode on ROCm
  - replaced the ROCm attention dispatch with a pure PyTorch `scaled_dot_product_attention` fallback over per-sequence dense K/V
  - preserved grouped-query expansion and bottom-right causal masking needed when `q_len != k_len`
- Validation result with image `registry.harbor.lan/flexinfer/runtime:rocm-gfx1100-gemma4-turboquant-experimental@sha256:51bdab2b8406512dc09b5857fab82bba2f26299c3a99d99a7961b49c02091d59`:
  - the generate probe on `cblevins-5930k` completed successfully
  - no `flash_attn_triton_amd` compile crash
  - no `aiter` `fmha_fwd` crash
  - the model produced output and shut down cleanly
- But the generated text was obviously degraded / nonsensical:
  - prompt: `Write one short sentence about KV cache compression.`
  - sample completion began with `4 ไป Ur_2e_m ...`
- Current interpretation:
  - the experimental runtime now has a crash-free ROCm execution path for Gemma4 + TurboQuant
  - however, output quality is not yet trustworthy, so this is still not a usable inference mode
  - the next work item is quality validation and attention-math debugging, not bootstrapping/runtime integration

TurboQuant ROCm PyTorch-codec follow-up:

- Added a new experimental ROCm escape hatch in the FlexInfer TurboQuant shim:
  - `TQ4_USE_PYTORCH_CODEC=1`
  - on ROCm, this bypasses `tq4_compress` / `tq4_decompress` Triton kernels and uses pure PyTorch on-device math for:
    - norm extraction
    - rotation
    - bucketization
    - nibble pack / unpack
    - centroid reconstruction
- Motivation:
  - isolate whether the remaining gibberish output was caused by the Triton TQ4 codec kernels, especially since upstream plugin validation is strongest for head dims `64/96/128` and only partially documented for Gemma-family `256`, while Gemma4 adds `512`-dim full-attention layers
- Validation details:
  - patch script still compiles and applies cleanly to a fresh upstream `turboquant-vllm` checkout
  - rebuilt and pushed the overlay image:
    - `registry.harbor.lan/flexinfer/runtime:rocm-gfx1100-gemma4-turboquant-experimental@sha256:6577c8a552f5e94ee2a7e5217377ec540109c54f550e369c3a4116ba5420b458`
  - reran the low-memory generate probe on `cblevins-5930k`
  - pod pulled the expected digest and completed end to end
- Result:
  - output quality did **not** recover
  - example completion remained nonsensical:
    - `9 \\land \\cosya0.5\\_circ[]-d_{1-| \\nu \\cdot\\text{`
- Interpretation:
  - this effectively rules out the Triton TQ4 codec kernels as the primary cause of the degraded Gemma4 output on ROCm
  - the remaining problem is more likely structural in Gemma4 handling:
    - grouped-query / KV-head semantics
    - mixed sliding/full-attention behavior
    - or another upstream Gemma4-specific assumption inside `turboquant-vllm`

TurboQuant upstream Gemma4 research follow-up:

- Background research found a stronger public signal than earlier runtime debugging suggested:
  - upstream issue `Alberto-Codes/turboquant-vllm#67` reports Gemma4-specific GQA shape handling problems
  - reported model shape:
    - `num_attention_heads=8`
    - `num_key_value_heads=2`
  - that matches `google/gemma-4-E4B-it`
- Additional upstream limitations now matter more:
  - `turboquant-vllm` README validation matrix covers Gemma2/Gemma3, not Gemma4
  - current implementation contract requires strict rotated-space handling:
    - rotate Q by `Pi^T`
    - keep K/V in rotated space
    - post-rotate output by `Pi`
  - the FlexInfer ROCm SDPA fallback was checked separately on synthetic tensors and matched expected rotated-attention math closely, so the fallback path itself is not the leading suspect anymore
- Current best hypothesis:
  - Gemma4 support in upstream `turboquant-vllm` is not semantically correct yet, and the remaining bug is likely in Gemma4 KV/GQA semantics rather than the ROCm kernel stack

TurboQuant Gemma4 shared-KV patch result:

- Implemented a shared-KV guard in the FlexInfer TurboQuant shim so layers with `kv_sharing_target_layer_name` skip `_compress_and_store()` across decode, prefill, fused decode, and INT8 prefill paths.
- This matches vLLM backend behavior for shared-KV layers and avoids writing raw shared-layer `k` / `v` tensors into cache.
- Validation:
  - patch script still applies cleanly to a fresh upstream `Alberto-Codes/turboquant-vllm` checkout
  - rebuilt and pushed `registry.harbor.lan/flexinfer/runtime:rocm-gfx1100-gemma4-turboquant-experimental@sha256:8c4d2dabc0a5e84686c8e90d3c743e20b8602c66b5c55996cf6f7f6458bdf304`
  - verified the cluster pod on `cblevins-5930k` pulled that exact digest
- Probe details:
  - model: `google/gemma-4-E4B-it`
  - node: `cblevins-5930k`
  - PVC: `llm-models-nfs`
  - `gpu_memory_utilization=0.70`
  - load time: `52.39s`
  - model load VRAM: `15.62 GiB`
  - available KV cache memory: `0.63 GiB`
  - GPU KV cache size: `27,136 tokens`
- Behavior changed materially:
  - before patch: output was math-like garbage
  - after patch: output became readable English, though still degenerate
- Prompt:
  - `Write one short sentence about KV cache compression.`
- Completion after patch:
  - `short sentence about KV cache compression.` repeated three times
- Interpretation:
  - shared-KV handling was a real correctness bug
  - remaining issue is no longer raw corruption; it is residual quality drift / repetition in the TurboQuant attention path

TurboQuant Gemma4 prompt-format quality fix:

- The remaining repetition turned out to be primarily a probe/harness issue, not a new backend corruption signal.
- Both debug generate jobs were driving the instruction-tuned `google/gemma-4-E4B-it` checkpoint through raw `llm.generate(...)`.
- Updated the debug ConfigMap scripts to default to `llm.chat(...)` for `-it` models:
  - [deploy/debug/gemma4-vllm-turboquant-generate.yaml](/Users/cblevins/workspace/services/flexinfer/deploy/debug/gemma4-vllm-turboquant-generate.yaml)
  - [deploy/debug/gemma4-vllm-experimental-generate.yaml](/Users/cblevins/workspace/services/flexinfer/deploy/debug/gemma4-vllm-experimental-generate.yaml)
- Also kept the ROCm soft-cap fallback patch in the TurboQuant shim for correctness parity when the fallback path is used.
- Validation:
  - reran TurboQuant job on `cblevins-5930k`
  - active image digest: `sha256:2b82214e72741c22d99e52620158ab2b5970d5273d69584d1894d01af8210aed`
  - prompt:
    - `Write one short sentence about KV cache compression.`
  - completion:
    - `KV cache compression reduces memory usage by efficiently storing and encoding past key and value states in transformer models.`
- Current interpretation:
  - Gemma4 + TurboQuant on ROCm now has a sane end-to-end text response on the debug path
  - the most visible remaining “quality bug” was caused by using completion-style prompting against an instruction-tuned checkpoint
