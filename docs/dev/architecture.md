---
title: Architecture
description: Components, control loops, and code layout.
---

# Architecture

FlexInfer is a set of cooperating components:

- `flexinfer-agent`: node-level hardware discovery + labeling
- `flexinfer-manager`: Kubernetes controller manager (CRDs → Deployments/Services)
- `flexinfer-sched`: scheduler extender (`/filter`, `/score`) for placement decisions
- `flexinfer-bench`: benchmark runner for tokens/sec measurement (v1alpha1 workflow)
- `flexinfer-proxy`: request router + scale-to-zero activator + GPUGroup demand signaling

## Primary contracts

- CRDs:
  - v1alpha2: `Model`
  - v1alpha1: `ModelDeployment`, `ModelCache`, `GPUGroup`
- Scheduler extender HTTP API (`kube-scheduler` extender v1)
- Proxy HTTP behavior (OpenAI-style model selection)
- Labels / annotations for discovery and routing

See `docs/specs/README.md` for the contract-level docs.

## Code layout

High-signal directories:

- `services/flexinfer/api/`: Go types for CRDs (source of schema)
- `services/flexinfer/controllers/`: reconciliation logic
- `services/flexinfer/backend/`: backend plugin registry (images/args/probes per backend)
- `services/flexinfer/scheduler/`: scheduler extender logic
- `services/flexinfer/agents/`: node agent + benchmarker implementations
- `services/flexinfer/cmd/`: main packages for binaries

## Control-plane flow (simplified)

1. Agent labels GPU nodes (`flexinfer.ai/gpu.*`)
2. Controller reconciles CRDs into:
   - Deployments (backend pods)
   - Services (stable in-cluster endpoint)
3. Scheduler extender biases placement using:
   - benchmark results (tokens/sec)
   - node annotations (util/cost/cache hints, if present)
4. Proxy routes requests and triggers scale-up when needed

