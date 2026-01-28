---
title: Next Roadmap
description: Near-term roadmap (next series of features/enhancements).
---

# Next Roadmap

> Last updated: 2026-01-28

This document is the working “next slice” roadmap. It is intentionally biased toward **operational stability** and **shipping improvements incrementally**.

## Principles

- Prefer **small, reversible** iterations.
- Prefer **better status + better errors** over silent behavior.
- Avoid “big rewrites”; keep v1alpha2 stable while iterating.

## Phase 1: Controller & API correctness (1–2 weeks)

Concrete checklist: `docs/planning/phase-1-controller-api-hardening.md`

- Harden reconciliation around **immutable fields** (deployments/services) and drift correction.
- Codify vendor-specific runtime requirements:
  - ensure NVIDIA workloads consistently set `runtimeClassName: nvidia` when requesting GPUs
- Ensure consistent **multi-replica spreading**:
  - anti-affinity and/or topology spread for replicas > 1.
- Improve `Model.status` so operators can quickly answer:
  - why it isn’t scheduled
  - why it isn’t Ready
  - why it was preempted
  - what endpoint is active and what backends are serving

## Phase 2: Serverless/Activator hardening (1–3 weeks)

- Define a strict compatibility target for proxy behavior:
  - OpenAI `/v1/*` request parsing and response semantics
  - Streaming support (SSE) with request coalescing policy
- Make activation behavior explicit:
  - cold start budgets + backoff
  - concurrency caps during activation
  - clear metrics (cold start latency, activation failures)

## Phase 3: Routing & performance (2–6 weeks)

- Add a path toward KV-cache-aware routing:
  - start with simple request hashing / session affinity
  - evolve toward prefix-based routing (opt-in)
- Add better load-balancing semantics for multi-replica models:
  - optionally expose “least loaded” based on per-pod metrics

## Phase 4: Operational polish (ongoing)

- E2E test harness for “real cluster” regression checks (smoke tests for v1alpha2 workflows).
- Documentation consolidation:
  - refresh `docs/INSTALL.md` + user quickstart flows
  - keep runbooks for known GPU/backend quirks
