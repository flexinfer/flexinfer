---
title: Next Roadmap
description: Near-term roadmap (next series of features/enhancements).
---

# Next Roadmap

> Last updated: 2026-05-02

This document tracks the implementation phases for FlexInfer. **Phases 1-4 plus Advanced Features are complete.** The project is now at 95%+ production readiness.

## Principles

- Prefer **small, reversible** iterations.
- Prefer **spec capsules before implementation** for any multi-file feature or operational workflow change.
- Prefer **better status + better errors** over silent behavior.
- Avoid "big rewrites"; keep v1alpha2 stable while iterating.

## Phase 1: Controller & API Hardening ✅ COMPLETE

Checklist: `docs/planning/phase-1-controller-api-hardening.md`

- ✅ Hardened reconciliation around **immutable fields** (deployments/services)
- ✅ Codified vendor-specific runtime requirements (NVIDIA `runtimeClassName`)
- ✅ Consistent **multi-replica spreading** with anti-affinity and topology spread
- ✅ Improved `Model.status` with actionable conditions

## Phase 2: Serverless/Activator Hardening ✅ COMPLETE

Checklist: `docs/planning/phase-2-serverless-activator-hardening.md`

- ✅ OpenAI API compatibility documented
- ✅ Streaming (SSE) behavior documented
- ✅ Cold start budget configuration
- ✅ Concurrency caps during activation
- ✅ Activation metrics (10 metric families)

## Phase 3: Routing & Performance ✅ COMPLETE

Checklist: `docs/planning/phase-3-routing-performance.md`

- ✅ Session affinity via consistent hashing
- ✅ Prefix-based routing (opt-in)
- ✅ Endpoint discovery for multi-replica models
- ✅ Least-loaded routing (opt-in)

## Phase 4: Operational Polish ✅ COMPLETE

Checklist: `docs/planning/phase-4-operational-polish.md`

- ✅ E2E test harness (`make test-e2e`)
- ✅ INSTALL.md refresh
- ✅ User quickstart guide
- ✅ GPU/backend quirks runbook

## Phase: Advanced Features ✅ COMPLETE

Shipped in pipeline #498 across 3 commits.

- ✅ KV-Cache tiering — LRU/LFU/FIFO eviction policies, /dev/shm Memory strategy, eviction metrics
- ✅ Dynamic Multi-LoRA — `LoRAAdapter` CRD, hot-swap adapters on running vLLM deployments
- ✅ OCI Model Registry — `ModelCatalog` CRD, Harbor/GHCR/ECR support via `pkg/registry/`
- ✅ Flash-Loader sidecar — `flexinfer-flash-loader` binary, parallel PVC→tmpfs preloading, P2P transfer
- ✅ Spot-Instance Resilience — Termination detectors for AWS, Azure, GCP, Harvester; proactive draining
- ✅ CNCF Sandbox Prep — GOVERNANCE.md, SECURITY.md, ADOPTERS.md, SBOM generation, license scanning

## M1: Multi-Tenancy Foundation ✅ COMPLETE

Design: `docs/design/multi-tenancy.md`  
Tracking issue: [#2](https://gitlab.flexinfer.ai/services/flexinfer/-/issues/2)

- [x] Multi-tenancy design draft (guarantees/non-guarantees)
- [x] Helm tenant baseline policy bundle scaffolding (`tenancy.*` values + templates)
- [x] Tenant onboarding workflow documented in deployment runbook
- [x] Validate baseline bundle in staging namespace and capture verification output
- [x] Define admission-policy follow-up slice (`docs/planning/multi-tenancy-followups.md`, MT-2)
- [x] Define tenant-aware fair-share follow-up slice (`docs/planning/multi-tenancy-followups.md`, MT-3)

## Innovation: Context-Aware Router ✅ COMPLETE

Tracking issue: [#8](https://gitlab.flexinfer.ai/services/flexinfer/-/issues/8)  
Execution plan: `docs/planning/context-aware-router-execution.md`

- [x] Baseline routing primitives shipped (session affinity, prefix, least-loaded)
- [x] Execution plan and closure criteria documented
- [x] Explicit cache-key contract and precedence implemented (`X-Flexinfer-Cache-Key`, `cache_key`, `cacheKey`, `prefix`, canonical, session fallback)
- [x] Safety/fallback controls implemented (key normalization, max length, malformed-key fallback)
- [x] Canonical prefix keying expanded (normalized multi-system + optional document context)
- [x] Observability signals expanded (key-source + routing-outcome metrics and proxy log signals)
- [x] E2E Chat-with-Doc validation and runbook guidance (`e2e/routing_test.go`, `docs/user/routing.md`)

## Quantization: Quality Validation Gate ✅ COMPLETE

Tracking issue: [#10](https://gitlab.flexinfer.ai/services/flexinfer/-/issues/10)  
Execution plan: `docs/planning/quantization-pipelines-execution.md`

- [x] Per-format perplexity/acceptance policy implemented
- [x] `flexinfer quantize validate` deterministic gate command implemented
- [x] User docs include quality-gate workflow and failure triage guidance

## Phase 5: Multi-Cluster ✅ COMPLETE

Design: `docs/design/multi-cluster.md`
Checklist: `docs/planning/phase-5-multi-cluster.md`

- [x] Cluster Registry (MVP)
- [x] Cross-Cluster Model Sync
- [x] Global Routing
- [x] Advanced Features

Progress note:
- Cluster CRD API scaffold has landed (`api/v1alpha2/cluster_types.go`, `config/crd/ai.flexinfer_clusters.yaml`).
- Cluster health probing + status transitions + probe metrics are implemented (`controllers/cluster_controller.go`).
- Cluster status now aggregates remote model inventory into `Cluster.status.models` (best-effort).
- FederatedModel CRD scaffold has landed (`api/v1alpha2/federatedmodel_types.go`, `config/crd/ai.flexinfer_federatedmodels.yaml`).
- FederatedModel controller scaffold now resolves placement and updates aggregated cluster readiness status (`controllers/federatedmodel_controller.go`).
- GlobalProxy CRD + global proxy binary + round-robin/failover/latency/weighted strategies are implemented (`api/v1alpha2/globalproxy_types.go`, `cmd/flexinfer-global-proxy/main.go`).
- Dynamic weight adjustment and GPU-aware routing are complete in the delivered advanced-features slice.

## FLUX.1 Diffusers + Image Generation Hardening ✅ COMPLETE

Shipped across commits `db4cfde`..`053a2d6`.

- [x] FLUX.1 Schnell (text-to-image) and FLUX.1 Fill (inpainting) pipeline support
- [x] NF4 quantization via bitsandbytes for 24GB VRAM cards (three-layer dtype strategy)
- [x] Diffusers `gc.collect()` fix for consecutive image generation OOM
- [x] Auto-detect NF4 and force `cpu_offload` mode
- [x] Startup timeout bumped to 900s for NF4+cpuOffload loading
- [x] SDXL InpaintPipeline direct usage (bypass auto-pipeline detection)
- [x] PyTorch 2.3 polyfills (RMSNorm, SDPA enable_gqa) for ROCm base images
- [x] Apex removal (fused_rms_norm_affine incompatible with FP16)

## ROCm gfx1100 Performance Tuning ✅ COMPLETE

Shipped in commit `45d311c`.

- [x] `TORCH_BLAS_PREFER_HIPBLASLT=1` and `HIPBLAS_OPERATION_TUNING=1` perf env vars for RDNA3
- [x] Prefill-decode split attention (`VLLM_V1_USE_PREFILL_DECODE_ATTENTION=1`) config support

## Controller & API Improvements (Feb-Mar 2026) ✅ COMPLETE

Shipped across commits `9334ba3`..`017d46f`.

- [x] Configurable tolerations in CRD spec — `spec.tolerations` on both v1alpha1 and v1alpha2 ([#24](https://gitlab.flexinfer.ai/services/flexinfer/-/issues/24))
- [x] Dedicated `flexinfer-kube-scheduler` ClusterRole replacing overly-broad `system:kube-scheduler` ([#25](https://gitlab.flexinfer.ai/services/flexinfer/-/issues/25))
- [x] Benchmark job sidecar termination for Istio/Linkerd compatibility ([#23](https://gitlab.flexinfer.ai/services/flexinfer/-/issues/23))
- [x] K8s allocatable GPU detection fallback when vendor tools are unavailable ([#22](https://gitlab.flexinfer.ai/services/flexinfer/-/issues/22))
- [x] Preserve quantization status across cache rebuilds
- [x] Use `CompletedAt` for quantized model path redirect
- [x] Idempotent quantize job completed status

## Proxy: Multipart Image Editing ✅ COMPLETE

Shipped in commit `7892613`.

- [x] Multipart/form-data model extraction for `/v1/images/edits` endpoint
- [x] JSON model rewriting guard (skips rewrite for non-JSON content types)

## Maintenance: Dependency Refresh 🚧 IN PROGRESS

Tracking issue: [#9](https://gitlab.flexinfer.ai/services/flexinfer/-/issues/9)

- [x] Merge first minor/patch dependency batches (`prometheus`, `golang-x`) into `master` (`a16b2d1`)
- [x] Merge `helm` Renovate batch: oras v1.3.0, busybox 1.37, kube-scheduler pinned to v1.33.4 (cluster-matched) (`32c4c74`)
- [x] Merge safe `docker` minor/patch subset: alpine 3.23, golang 1.26.0, rocm 6.4.4, cuda 12.2.2 (`7a3f95a`)
- [ ] Stage major docker updates in a separate rollout: python 3.14, pytorch 2.3, cuda 12.9, rocm 6.4 (mlc) ([#21](https://gitlab.flexinfer.ai/services/flexinfer/-/issues/21))
- [x] Keep roadmap tracking issue `#1` synchronized with dependency rollout status (updated 2026-02-26)

## Delivery Acceleration: Spec-Driven Development ✅ COMPLETE

Objective: make future feature delivery faster by requiring small, source-backed
specs and explicit validation gates before multi-file implementation begins.

- [x] SD-1: Add reusable spec capsule template and contributor checklist ([#55](https://gitlab.flexinfer.ai/services/flexinfer/-/issues/55)).
- [x] SD-2: Define a slice-readiness gate with target files/modules, validation commands, and rollback notes ([#56](https://gitlab.flexinfer.ai/services/flexinfer/-/issues/56)).
- [x] SD-3: Expand `.loom/60-validation-matrix.md` as the canonical canary and runtime-promotion evidence table, with the contract tracked in `docs/planning/spec-driven-delivery.md` ([#57](https://gitlab.flexinfer.ai/services/flexinfer/-/issues/57)).
- [x] SD-4: Add agent-ready delegation notes to feature plans so parallel slices have clear ownership boundaries, safe-to-edit modules, local verification commands, and expected output signals ([#58](https://gitlab.flexinfer.ai/services/flexinfer/-/issues/58)).
- [x] SD-5: Run roadmap reconciliation after planning changes and keep tracking issues linked through `docs/planning/roadmap-reconciliation.md` ([#59](https://gitlab.flexinfer.ai/services/flexinfer/-/issues/59)).

Acceptance criteria:

- Every future roadmap slice either links to a spec capsule or declares why a full spec is unnecessary.
- Every multi-file feature plan includes acceptance criteria, validation commands, and rollback/backout notes.
- Parallel implementation plans include agent delegation notes with disjoint ownership boundaries, files/modules to avoid, local verification, and expected output signals.
- Runtime canary promotions can be traced from spec to `.loom/60-validation-matrix.md` evidence without relying on chat history.
- Planning changes are reconciled across `ROADMAP.md`, this file, `.loom/00-index.md`, and tracking issues before closing a slice.

## ROCm gfx1100/gfx906 Platform Enhancements 🚧 PLANNED

Planning docs: `docs/planning/rocm-gfx1100-gfx906-platform-slice.md`,
`.loom/gfx1100-gfx906-platform-enhancements-spec.md`,
`.loom/gfx1100-gfx906-platform-enhancements-plan.md`

Objective: make AMD `gfx1100` and `gfx906` first-class platform lanes with
consistent backend support truth, digest-pinned runtime promotion, and canary
evidence that survives beyond chat history.

- [x] RG-1: Reconcile the initial capability matrix and demote unvalidated `gfx906` vLLM from full default support.
- [x] RG-2: Add runtime promotion consistency checks for `build/runtime.yaml`, GPUProfiles, and Helm runtime profiles.
- [ ] RG-3: Expand `.loom/60-validation-matrix.md` for runtime digest and canary evidence across both arches.
- [ ] RG-4: Run `gfx906` conservative-lane canaries for llama.cpp/Ollama, GPTQ/abliteration, and 512px diffusers offload.
- [ ] RG-5: Run `gfx1100` textgen/imagegen canaries for Navi vLLM, FLUX/Fill, and any Gemma4/TurboQuant runtime candidates.

## Context-Curve Benchmarking 🚧 PLANNED

Planning doc: `docs/planning/context-curve-benchmark.md`

Objective: make long-context runtime behavior measurable before making it
schedulable. The first pass is reporting-only: record prefill throughput, decode
throughput, free-VRAM slope when available, and failure/skipped points across a
small context ladder.

- [x] CC-1: Add spec capsule and readiness gate for the context-curve benchmark MVP.
- [x] CC-2: Add a reporting-only context-curve runner that emits a stable JSON report without changing scheduler decisions.
- [x] CC-3: Capture one live curve on an existing Gemma4 or Qwen lane and link it from `.loom/60-validation-matrix.md`.
- [x] CC-4: Add opt-in ConfigMap storage for context-curve reports without changing scheduler consumers.
- [x] CC-4b: Capture a second-family live curve (`qwen3-8b-radeonvii-soak`) so scheduler use can be specified.
- [ ] CC-5: Spec the scheduler/proxy use of stored curves, including a backtest kill-test, before any runtime code lands. See `docs/planning/context-curve-scheduler-spec.md`.
- [ ] CC-6: Run the backtest kill-test offline and record the verdict in the validation matrix.
- [ ] CC-7: Proxy curve-aware routing implementation (opt-in annotation, default off). **Blocked** until CC-6 passes.

## Tech Debt (Ongoing)

See `ROADMAP.md` for full tech debt tracking:

- **TD-1**: Error handling for ignored returns — ✅ Reduced (proxy + scheduler fixed via pre-marshal pattern)
- ~~**TD-2**: ROCm GFX1100 image builds~~ ✅ RESOLVED (supplementalGroups fix)
- ~~**TD-3**: CLI test coverage~~ ✅ RESOLVED (now 78.6% for `cmd/flexinfer/commands`)
- ~~**TD-5**: v1alpha1 → v1alpha2 migration guide~~ ✅ RESOLVED (`docs/migration/v1alpha1-to-v1alpha2.md`)
- ~~**TD-11**: E2E test names violate RFC 1123~~ ✅ RESOLVED (lowercase + "/" replacement)
- ~~**TD-12**: GPUGroup v1alpha1 not registered in e2e scheme~~ ✅ RESOLVED (added to scheme)
