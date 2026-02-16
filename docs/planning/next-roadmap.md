---
title: Next Roadmap
description: Near-term roadmap (next series of features/enhancements).
---

# Next Roadmap

> Last updated: 2026-02-16

This document tracks the implementation phases for FlexInfer. **Phases 1-4 plus Advanced Features are complete.** The project is now at 95%+ production readiness.

## Principles

- Prefer **small, reversible** iterations.
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

## M1: Multi-Tenancy Foundation 🚧 IN PROGRESS

Design: `docs/design/multi-tenancy.md`  
Tracking issue: [#2](https://gitlab.flexinfer.ai/services/flexinfer/-/issues/2)

- [x] Multi-tenancy design draft (guarantees/non-guarantees)
- [x] Helm tenant baseline policy bundle scaffolding (`tenancy.*` values + templates)
- [x] Tenant onboarding workflow documented in deployment runbook
- [ ] Validate baseline bundle in staging namespace and capture verification output

## Phase 5: Multi-Cluster (Future)

Design: `docs/design/multi-cluster.md`
Checklist: `docs/planning/phase-5-multi-cluster.md`

- [ ] Cluster Registry (MVP)
- [ ] Cross-Cluster Model Sync
- [ ] Global Routing
- [ ] Advanced Features

Progress note:
- Cluster CRD API scaffold has landed (`api/v1alpha2/cluster_types.go`, `config/crd/ai.flexinfer_clusters.yaml`).
- Cluster health probing + status transitions + probe metrics are implemented (`controllers/cluster_controller.go`).
- Cluster status now aggregates remote model inventory into `Cluster.status.models` (best-effort).
- FederatedModel CRD scaffold has landed (`api/v1alpha2/federatedmodel_types.go`, `config/crd/ai.flexinfer_federatedmodels.yaml`).
- Remaining Cluster Registry work is controller hardening (timeouts/retries/backoff and richer conditions).

## Tech Debt (Ongoing)

See `ROADMAP.md` for full tech debt tracking:

- **TD-1**: Error handling for ignored returns — ✅ Reduced (proxy + scheduler fixed via pre-marshal pattern)
- ~~**TD-2**: ROCm GFX1100 image builds~~ ✅ RESOLVED (supplementalGroups fix)
- ~~**TD-3**: CLI test coverage~~ ✅ RESOLVED (now 78.6% for `cmd/flexinfer/commands`)
- ~~**TD-5**: v1alpha1 → v1alpha2 migration guide~~ ✅ RESOLVED (`docs/migration/v1alpha1-to-v1alpha2.md`)
- ~~**TD-11**: E2E test names violate RFC 1123~~ ✅ RESOLVED (lowercase + "/" replacement)
- ~~**TD-12**: GPUGroup v1alpha1 not registered in e2e scheme~~ ✅ RESOLVED (added to scheme)
