---
title: Phase 1: Controller & API Hardening
description: Concrete checklist for next implementation series (controller correctness + operator clarity).
---

# Phase 1: Controller & API Hardening

> Last updated: 2026-01-28

This is the concrete checklist for the next “Phase 1” implementation series: make v1alpha2 operations **predictable**, **safe to reconcile**, and **easy to debug**.

## Goals

- Eliminate common reconcile loops caused by immutable fields (Services/Deployments).
- Improve multi-replica correctness (spreading + stable selection).
- Make operator-facing status answer “what’s wrong?” without reading controller logs.

## Non-Goals

- Major v1alpha2 API redesign.
- Full L7 routing / KV-cache-aware routing (tracked in later phases).

## Work items (PR-sized)

### 1) Service reconciliation: preserve immutables ✅

- [x] Ensure `Service.spec.clusterIP` / `clusterIPs` are preserved on update.
- [x] Avoid "wipe & replace spec" patterns; instead patch only:
  - ports
  - selector
  - (optionally) type if we explicitly own it
- [x] Add unit tests that exercise "existing Service with allocated ClusterIP" and ensure updates don't error.

**Acceptance**
- Controller does not emit "Service … field is immutable" errors during normal operation.

**Primary files**
- `controllers/model_controller.go` (`ensureService`)

**Status**: Complete. Implementation already preserves `clusterIP`/`clusterIPs` by only updating `Spec.Ports`, `Spec.Selector`, `Labels`, and `Annotations`. Test `TestEnsureServicePreservesClusterIP` verifies this behavior and confirms ports are updated correctly.

### 2) Deployment reconciliation: immutable selector policy ✅

- [x] Keep the "selector is immutable" policy explicit and consistent:
  - Preserve selector on updates.
  - Ensure pod template labels include selector labels (so Service selection doesn't break).
- [x] Add unit tests around selector preservation and label merging.

**Acceptance**
- Controller does not emit "Deployment … spec.selector … immutable" errors during normal operation.
- Multi-replica models retain stable Service membership across upgrades.

**Primary files**
- `controllers/model_controller.go` (`ensureDeployment`)

**Status**: Complete. Implementation preserves existing selector via `DeepCopy()` and merges selector labels into pod template labels. Test `TestEnsureDeploymentPreservesSelectorAndMatchesTemplate` verifies this behavior.

### 3) Multi-replica placement guarantees ✅

- [x] Ensure the replica spreading behavior is deterministic when `spec.serverless.minReplicas > 1`:
  - pod anti-affinity (required) keyed on the stable selector labels
  - topology spread constraints (preferred) keyed on stable selector labels
- [x] Add one focused unit test verifying that a multi-replica model includes spreading constraints.

**Acceptance**
- Two replicas of a model land on distinct nodes when possible (and don't co-locate unless the cluster forces it).

**Primary files**
- `controllers/model_controller.go`

**Status**: Complete. Implementation includes `PodAntiAffinity` (required) and `TopologySpreadConstraints` (preferred) when `desiredReplicas > 1`. Test `TestEnsureDeploymentMultiReplicaIncludesSpreadingConstraints` verifies this behavior.

### 4) NVIDIA runtime requirements: codify + document ✅

- [x] Ensure NVIDIA GPU workloads consistently set `runtimeClassName: nvidia` (already implemented; keep it locked in).
- [x] Add operator-facing documentation:
  - why it's required
  - how to verify (`/dev/nvidia*` presence)
  - what failure looks like (e.g., `torch.cuda.is_available() == false`)

**Acceptance**
- NVIDIA model pods reliably see CUDA devices when requesting `nvidia.com/gpu`.

**Primary files**
- `controllers/model_controller.go`
- `docs/user/operations.md` (or `docs/dev/backends.md`)

**Status**: Complete. Implementation sets `runtimeClassName: nvidia` for NVIDIA GPU workloads (model_controller.go:660-668). Added comprehensive documentation in `docs/user/operations.md` covering requirements, verification steps, and common failure symptoms.

### 5) Status clarity: expose actionable conditions ✅

Add/normalize `Model.status` conditions so operators can answer:

- [x] "No matching nodes" (selector mismatch, GPU vendor ambiguity).
- [x] "Cache not ready" (prefetch/check Job failed).
- [x] "Waiting for activation" vs "Starting backend" vs "Ready".
- [x] "Preempted" (shared GPU group scheduling).

**Acceptance**
- `kubectl get model -o yaml` shows a human-readable reason for non-ready states without digging through logs.

**Primary files**
- `api/v1alpha2/model_types.go`
- `controllers/model_controller.go`

**Status**: Complete. Added condition type `ConditionModelSchedulable` and comprehensive reason constants (`ReasonNoMatchingNodes`, `ReasonAmbiguousGPUVendor`, `ReasonBackendUnsupported`, `ReasonCacheNotReady`, `ReasonWaitingForActivation`, `ReasonStartingBackend`, `ReasonBackendReady`, `ReasonPreempted`). Controller now sets:
- `Schedulable` condition during GPU detection (true/false with appropriate reason)
- `Cached` condition based on cache status
- `Ready` condition based on deployment state and phase
Test `TestSetModelCondition` verifies the condition-setting behavior.

### 6) Image pinning guidance (docs + optional defaults) ✅

- [x] Document best practices:
  - [x] pin critical runtime images by digest when mutable tags are used with `IfNotPresent`
  - [x] where to configure default backend images (controller env)

**Acceptance**
- Docs include a short "avoid stale node caches" section with recommended patterns.

**Primary files**
- `docs/CONFIGURATION.md`
- `docs/dev/release.md`

**Status**: Complete. Added comprehensive "Image Pinning Best Practices" section to `docs/CONFIGURATION.md` covering:
- Why image pinning matters (stale node caches with IfNotPresent)
- Recommended patterns (digest pinning for production, versioned tags with Always for dev)
- How to force re-pull on all nodes
- Configuring default images via controller env vars
- Per-model image override in spec

## Tracking

- This checklist is the source-of-truth for Phase 1 items.
- When a PR lands, add a checkbox + link to the PR/commit in this doc.

