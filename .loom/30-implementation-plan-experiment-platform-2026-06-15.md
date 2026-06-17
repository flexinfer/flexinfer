# Implementation Plan: Experiment Platform + vLLM Currency (A+B)

- **Date**: 2026-06-15
- **Author**: claude-code
- **Lineage**: chosen from [brainstorm A+B](brainstorm-next-high-leverage-feature-2026-06-15.md); motivated by [DiffusionGemma servability ceiling](research-diffusiongemma-2026-06-15.md)
- **Goal**: A `ModelExperiment` CRD + controller + MCP tool whose first workload is "canary a new vLLM/arch build against a smoke/benchmark gauntlet, auto-verdict, promote if green." Turns multi-day hand-run experiments (F5 window, Track-B self-quant, currency bumps) into repeatable, observable, one-declaration trials.
- **Directive**: operator-requested experiment platform ([[project_experiment_platform]]), seeded with vLLM-currency as the concrete first workload.

## Riskiest assumption + kill-test (this IS Slice 1)

**Load-bearing assumption**: a *meaningfully newer* vLLM (toward the V1 / model-runner-v2 line that current architectures need) can be built for ROCm **gfx1100** (and ideally gfx906) on flexinfer's build lane and actually **boots + serves coherently** on the fleet.

**Why it's risky**: the current lane pins `VLLM_REF=v0.6.3.post1` on `torch:rocm6.3.4-multiarch` + `transformers==4.45.2` (compat-frozen). A large vLLM bump likely forces a newer torch/ROCm base and transformers, and may hit ROCm kernel gaps (the DiffusionGemma dLLM path is NVIDIA-only — proof that newer paths aren't always ROCm-covered).

**Kill test** (live, ≤ half a day of build + 30 min validate): build ONE newer-vLLM multiarch image off-CI (`build/build-vllm-multiarch.sh` with a bumped `VLLM_REF` + whatever base it forces) → deploy a canary GPUProfile/Model → serve an existing model (e.g. a GPTQ Qwen3-14B or gemma4) → assert HTTP 200 + coherent output + `flexinfer-bench` gauntlet passes. **Observable outcome**: green serve+bench on the new image, or a specific build/runtime failure that bounds how far currency can go on ROCm.

**Failure mode if wrong**: building the CRD/controller/MCP for a currency workload that can't actually run on AMD — re-running the "wire format correct, nothing serves" waste class.

**Status**: **PARTIAL PASS — 2026-06-17** (evidence: `deploy/debug/qwen36-currency-canary-model.yaml`, this session). The crux is confirmed: a current vLLM (official AMD prebuilt **rocm/vllm 0.19.1**, mirrored to Harbor — no custom build even needed) **boots and serves on gfx1100**. It loaded the brand-new Qwen3.5-MoE-VL 35B-A3B (text-only GPTQ) hybrid linear-attention + MoE arch, ran the gated-deltanet/FLA + rotary kernels on the 5930k 7900xtx, allocated a hybrid attention/mamba KV cache (42,432 tokens, 9.24x concurrency), reached `Application startup complete`, and returned **HTTP 200 generating tokens** via `/v1/completions` + `/v1/chat/completions`.

Four layered blockers were solved (recipe captured in the canary YAML's startup plugin + hfOverrides): (1) arch registration via a `vllm.general_plugins` entry-point, (2) mRoPE-vs-standard-rotary position-shape (strip mrope), (3) hybrid KV page-size unify (`is_hybrid=True` + grafted `get_mamba_state_*`), (4) routed-expert weight rename `moe.* -> mlp.experts.*` (skip count 0).

**Remaining (OPEN, not a ROCm/currency limit)**: coherent output of *this specific gptqmodel fused-MoE GPTQ artifact* is blocked by a **vLLM-internal bug** — `load_fused_expert_weights` builds GPTQ expert param name `experts.w2_weight.qweight` (the unquantized stacked-mapping name + `.qweight`) instead of `experts.w2_qweight` → `KeyError` (qwen3_5.py:258). Without the rename (blocker 4) it serves but is incoherent (all 256 experts unloaded → repetition loops). So the kill-test's "serves coherently" criterion is **not yet met for the exotic Qwen3.5-MoE GPTQ target**, for a *model-loader* reason, not a ROCm/build/currency reason.

**Recommended next steps**: (a) get the unambiguous green the spec intended by serving a *standard existing* model (GPTQ Qwen3-14B / gemma4) on 0.19.1 — should serve coherently, confirming the image; (b) treat "nail the Qwen3.5-MoE GPTQ serving recipe on vLLM 0.19.1" — patch vLLM's Qwen3.5 GPTQ-MoE expert loader OR re-quantize in a vLLM-native MoE format (compressed-tensors/AWQ) — as the experiment platform's **first automated bring-up workload**.

(GPU contention note: the run preempted the gemma4-5930k twin via forcePromotion; canary deleted after the verdict, twin reclaiming its GPU. Leftover: 30KB `_dbg_serve.log` at llm-models-nfs root — harmless cruft to sweep.)

**Blocking rule**: per spec-riskiest-assumption policy, Slices 3–5 (CRD, controller, MCP) **do not ship until Slice 1's kill-test passes live**. Slice 2 (gauntlet harness) is allowed first because it is the verdict mechanism the kill-test itself needs.

## Sequencing around the running quant

| When | Slice | GPU? |
|---|---|---|
| **Now (quant running)** | Slice 2 — gauntlet/verdict harness | No (code-only) |
| When GPU frees | **Slice 1 — currency-canary kill-test** | Yes (build + serve) |
| After kill-test green | Slice 3 → 4 → 5 (CRD → controller → MCP) | Mixed |
| Later | Slice 6 — currency automation (B+C loop) | No |

## Slices

### Slice 1 — Currency-canary kill-test (the riskiest assumption)
- **Scope**: manually build one newer-vLLM ROCm multiarch image off-CI; deploy canary; serve an existing model; run the gauntlet (Slice 2); record verdict + the exact base/version matrix that worked or failed.
- **Builds on**: `build/build-vllm-multiarch.sh`, `build/Dockerfile.vllm-rocm-multiarch` (VLLM_REF pin), off-CI `docker --context 7900xtx` pattern.
- **Acceptance**: documented runbook + a PASS/FAIL verdict with the working `(vllm, torch, ROCm, transformers)` tuple; canary serves HTTP 200 coherent + bench within threshold OR a bounded failure report.
- **Risk/safety**: GPU contention (gate on quant idle); canary only, never touches primary lanes; keep the 0.6.3 image as the fallback.

### Slice 2 — Gauntlet-as-contract (verdict harness) — **start now, code-only**
- **Scope**: wrap the existing `cmd/flexinfer-bench` + `agents/benchmarker` + `pkg/benchmarkconfig` into a reusable **verdict** primitive: given a model-name + thresholds (min decode tok/s, max TTFT, a coherence smoke prompt/assert), emit a structured PASS/FAIL result and persist it (ConfigMap/Postgres, reuse existing stores).
- **Builds on**: `cmd/flexinfer-bench/main.go:1-95`, `agents/benchmarker/*`, `pkg/benchmarkconfig/config.go`.
- **Acceptance**: `flexinfer-bench --gauntlet --thresholds ...` (or a new `pkg/gauntlet`) returns a typed verdict; unit tests for threshold logic + coherence assert; no GPU needed for unit tests (mock proxy).
- **Risk/safety**: pure additive; existing bench paths unchanged.

### Slice 3 — `ModelExperiment` CRD + types (blocked on Slice 1 green)
- **Scope**: `api/v1alpha2/modelexperiment_types.go` — spec: `{ image/vllmRef, modelRef, gpuArch, partition, quant, gauntlet{thresholds}, promoteOnGreen }`; status: `{ phase, buildRef, gauntletVerdict, benchResults, conditions }`. `make manifests` + `make generate`; Helm CRD copy.
- **Builds on**: `api/v1alpha2/groupversion_info.go:18-34`, Makefile `manifests`/`generate` (lines 34-43), `config/crd/`, `charts/flexinfer/crds/`.
- **Acceptance**: CRD installs; `kubectl apply` a sample ModelExperiment validates; deepcopy generated; no controller yet.

### Slice 4 — ModelExperiment controller (blocked on Slice 1 green)
- **Scope**: reconciler state machine: (optional build hook) → deploy canary → run gauntlet job → collect verdict → promote-if-green / leave-for-manual. Wire into `cmd/flexinfer-manager/main.go` after GPUProfile/Model.
- **Builds on**: `controllers/job_template.go:28-95` (CacheJobParams/buildCacheJob), benchmarker, existing reconciler patterns (`controllers/modelcache_controller.go`).
- **Acceptance**: envtest drives Pending→Building→Serving→Gauntlet→Verdict→(Promoted|Failed); idempotent; canary cleanup on completion.

### Slice 5 — MCP tool (blocked on Slice 1 green)
- **Scope**: `flexinfer_create_experiment` + `flexinfer_get_experiment` + `flexinfer_list_experiments` in `loom-core/cmd/mcp-flexinfer/main.go`; add experiment GVR.
- **Builds on**: `loom-core/cmd/mcp-flexinfer/main.go` (tool pattern lines 113-381, GVR lines 32-36, dynamic client 388-399).
- **Acceptance**: agent can declare an experiment and read its verdict via MCP; loom-core docs guardrail satisfied (README/CHANGELOG delta).

### Slice 6 — Currency automation (B+C loop, optional)
- **Scope**: version-watch (Renovate customManager already tracks toolchain — extend to vLLM ref) auto-creates a ModelExperiment when a new vLLM ref appears; green verdict → promote PR.
- **Acceptance**: a new vLLM ref produces an experiment + verdict without hand-running.

## Open question (carried from brainstorm)
Confirm the "70B daily driver settled" gate is satisfied (F5 3-way 72B served) so Slices 3–5 are authorized once Slice 1 is green. (Operator selected A+B on 2026-06-15, implying yes.)
