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

**Status**: not run — **GPU-gated by the in-flight qwen MoE quant** (single-GPU contention on the build/serve nodes). Run when the quant lane frees.

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
