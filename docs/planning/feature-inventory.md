---
title: Feature Inventory
description: Current feature status (what's shipped, what's partial, what's missing).
---

# Feature Inventory

> Last updated: 2026-02-01

This is a pragmatic inventory of "what works in practice" and "what's next". **Phases 1-4 are now complete** - see phase planning docs for details.

## Core components (binaries)

| Component | Binary | Primary responsibility | Current status |
|----------|--------|------------------------|----------------|
| Node agent | `flexinfer-agent` | Hardware discovery + node labels | Working |
| Controller | `flexinfer-manager` | Reconcile CRDs into Deployments/Services/Jobs | Working (active iteration) |
| Scheduler | `flexinfer-sched` | kube-scheduler extender scoring/filtering | Working |
| Benchmarker | `flexinfer-bench` | Measure perf for scheduling inputs | Working (backend-dependent) |
| Proxy | `flexinfer-proxy` | Request routing + "activator" for serverless | Working (Phase 2-3 hardened) |

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

## What’s solid today

- Running **multi-replica models** behind a single Kubernetes Service (simple load-balancing via Service endpoints).
- Shared GPU groups for “one active model at a time” workflows (demand-based swapping).
- Caching strategies that work for homelab (notably `pvc://...` sources and SharedPVC patterns).
- LiteLLM discovery metadata (annotations + service labels) for external routing/proxying.

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

5. **Backend build + distribution ergonomics** (digest pinning docs done, but ROCm GFX1100 image NOT built - blocker for quality models)
6. **CLI test coverage** - Only 7% coverage for user-facing CLI commands
7. **Error handling tech debt** - 13+ locations with ignored error returns
8. **v1alpha1 migration guide** - Deprecated API needs documented migration path

### Future Phases

- **Multi-cluster federation** - See `docs/design/multi-cluster.md`
- **Dynamic Multi-LoRA** - Hot-swap adapters
- **KV-Cache tiering** - Advanced memory management
