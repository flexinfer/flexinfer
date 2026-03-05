---
title: Feature Inventory
description: Current feature status (what's shipped, what's partial, what's missing).
---

# Feature Inventory

> Last updated: 2026-03-05

This is a pragmatic inventory of "what works in practice" and "what's next". **Phases 1-4 plus 6 Advanced Features are now complete** — the project is at 95%+ production readiness. See phase planning docs for details.

## Core components (binaries)

| Component | Binary | Primary responsibility | Current status |
|----------|--------|------------------------|----------------|
| Node agent | `flexinfer-agent` | Hardware discovery + node labels | Working |
| Controller | `flexinfer-manager` | Reconcile CRDs into Deployments/Services/Jobs | Working (active iteration) |
| Scheduler | `flexinfer-sched` | kube-scheduler extender scoring/filtering | Working |
| Benchmarker | `flexinfer-bench` | Measure perf for scheduling inputs | Working (backend-dependent) |
| Proxy | `flexinfer-proxy` | Request routing + "activator" for serverless | Working (Phase 2-3 hardened, multipart support) |
| Flash-Loader | `flexinfer-flash-loader` | Parallel model preloading (PVC→tmpfs, P2P) | Working (init container) |

## CRDs / APIs

### v1alpha2 (recommended)

- `Model` (`ai.flexinfer/v1alpha2`)
  - Single-resource “homelab-friendly” API.
  - Backends selected via `spec.backend`.
  - Supports GPU vendor selection + optional shared GPU groups via `spec.gpu.shared`.
  - Supports scale-to-zero via `spec.serverless`.
  - Supports caching via `spec.cache`.
  - Supports LiteLLM discovery via `spec.litellm` + `spec.serviceLabels`.

Docs: `docs/user/models-v1alpha2.md`

### v1alpha2 (new CRDs)

- `LoRAAdapter` (`ai.flexinfer/v1alpha2`) — Declarative hot-swapping of LoRA adapters on running models.
- `ModelCatalog` (`ai.flexinfer/v1alpha2`) — Syncs model metadata from OCI, HuggingFace, and Ollama registries.

### v1alpha1 (legacy / advanced)

- `ModelDeployment`, `ModelCache`, `GPUGroup` exist for the earlier workflow; still referenced in architecture/spec docs.

Docs: `docs/user/legacy-v1alpha1.md`

## Backend support (controller registry)

Backend definitions live in `backend/` (image/args/env/probes per backend).

Common backends used in homelab:

- `mlc-llm` (ROCm + CUDA variants)
- `vllm` (ROCm variants)
- `diffusers` (image generation)
- `ollama` / `llamacpp` (CPU + GPU depending on build)

Docs: `docs/dev/backends.md`

## What's solid today

- Running **multi-replica models** behind a single Kubernetes Service (simple load-balancing via Service endpoints).
- Shared GPU groups for "one active model at a time" workflows (demand-based swapping).
- Caching strategies that work for homelab (notably `pvc://...` sources and SharedPVC patterns).
- LiteLLM discovery metadata (annotations + service labels) for external routing/proxying.
- **KV-Cache tiering** with LRU/LFU/FIFO eviction policies and /dev/shm Memory strategy.
- **Dynamic Multi-LoRA** hot-swapping via `LoRAAdapter` CRD with vLLM backend integration.
- **OCI model registry** support (Harbor, GHCR, ECR) via `ModelCatalog` CRD and `pkg/registry/` adapters.
- **Flash-Loader sidecar** for parallel model preloading from PVC to tmpfs, reducing cold start I/O.
- **Spot-instance resilience** with termination detectors for AWS, Azure, GCP, and Harvester.
- **CNCF compliance artifacts**: GOVERNANCE.md, SECURITY.md, ADOPTERS.md, SBOM generation, license scanning.
- **FLUX.1 image generation** on ROCm gfx1100 with NF4 quantization (Schnell text-to-image + Fill inpainting).
- **Multipart proxy** model extraction for `/v1/images/edits` multipart/form-data requests.
- **Configurable tolerations** via `spec.tolerations` on CRD spec for scheduling on tainted nodes.
- **GPU detection fallback** using K8s `node.status.allocatable` when vendor tools are unavailable.
- **gfx1100 perf tuning**: HipBLASLt, prefill-decode split attention for vLLM v1.

## Recent operational learnings (k3s)

- NVIDIA GPU pods require `runtimeClassName: nvidia` to reliably get `/dev/nvidia*` injected by the runtime.
- AMD ROCm nodes can be detected without `rocm-smi` in the agent container (sysfs VRAM + `rocminfo` for `gfx*` arch when available).
- Mutable image tags + `IfNotPresent` can cause stale node caches; prefer pinning critical images by digest when possible.
- Deployment/Service reconciliation must treat certain fields as immutable (or handle replacements safely) to avoid reconcile loops.

## Known gaps / pain points (prioritized)

### Resolved in Phases 1-4 ✅

1. ~~**Serverless/activator hardening**~~ → Phase 2 complete: OpenAI compatibility, streaming docs, cold start budgets, activation metrics
2. ~~**Production-grade rollout behavior**~~ → Phase 1 complete: immutable field handling, multi-replica spreading, actionable status conditions
3. ~~**L7 routing for cache locality**~~ → Phase 3 complete: session affinity, prefix-based routing, least-loaded routing
4. ~~**Operational guardrails**~~ → Phase 1 complete: status conditions explain "why not scheduled", "why scaled to zero", "why preempted"

### Still Open

5. **Backend build + distribution ergonomics** (ROCm `gfx1100` image builds are resolved; remaining work is making backend build/publish paths more reproducible and documenting digest pinning patterns end-to-end.)
6. **Error handling tech debt** - Reduced from 13+ to a handful of locations (proxy JSON-encode-after-header-sent and scheduler handlers fixed via pre-marshal pattern)
7. **E2E GPU scenarios** - Expand coverage for real GPU scheduling/placement paths (many are currently skipped or too slow for CI)

### Recently Resolved ✅

1. ~~**CLI test coverage**~~ → `cmd/flexinfer/commands` is now 78.6% covered (target was 50%+).
2. ~~**v1alpha1 → v1alpha2 migration guide**~~ → `docs/migration/v1alpha1-to-v1alpha2.md`.

### Recently Shipped (Advanced Features) ✅

- ✅ **KV-Cache tiering** — LRU/LFU/FIFO eviction policies, /dev/shm Memory strategy
- ✅ **Dynamic Multi-LoRA** — `LoRAAdapter` CRD, hot-swap adapters on running vLLM deployments
- ✅ **OCI Model Registry** — `ModelCatalog` CRD, Harbor/GHCR/ECR via `pkg/registry/`
- ✅ **Flash-Loader Sidecar** — `flexinfer-flash-loader` binary, parallel PVC→tmpfs preloading
- ✅ **Spot-Instance Resilience** — Termination detectors for AWS, Azure, GCP, Harvester
- ✅ **CNCF Sandbox Prep** — Governance, security, adopters, SBOM, license scanning

### Delivered Phases (since inventory creation)

- ✅ **Multi-cluster federation** — Cluster CRD, FederatedModel, GlobalProxy with weighted/latency routing. See `docs/design/multi-cluster.md`.
- ✅ **Multi-tenancy** — Tenant baseline policy bundle, onboarding workflow, admission + fair-share follow-ups defined. See `docs/design/multi-tenancy.md`.
- ✅ **Context-aware router** — Canonical prefix keying, safety/fallback controls, E2E validation. See `docs/user/routing.md`.
- ✅ **FLUX.1 image generation** — NF4 on ROCm gfx1100, Schnell + Fill pipelines, diffusers OOM fix.
- ✅ **Controller hardening** — Configurable tolerations, scheduler RBAC, benchmark sidecar termination, GPU detection fallback.

### Future Work

- **User-facing FLUX NF4 docs** — Three-layer dtype strategy, bitsandbytes requirements, memory analysis
- **GPU sharing operational docs** — Priority preemption semantics, demand/swap timing, latency breakdown
- **Backend distribution ergonomics** — Digest pinning, reproducible build/publish paths
