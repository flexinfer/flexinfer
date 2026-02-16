---
title: Multi-Tenancy Follow-Ups
description: Concrete implementation slices after M1 tenant baseline.
---

# Multi-Tenancy Follow-Ups

> Last updated: 2026-02-16

This document captures the next executable slices for multi-tenancy after the M1 baseline.

## MT-2: Admission Policy Guardrails

Goal: prevent tenant workloads from bypassing quota/sizing guardrails by omitting resource requests/limits.

### Scope

- Add admission policy rules for FlexInfer workload CRDs:
  - `Model` (`ai.flexinfer/v1alpha2`)
  - `ModelDeployment` (`ai.flexinfer/v1alpha1`)
- Enforce minimum requirements:
  - CPU and memory `requests`/`limits` must be set
  - GPU workloads must declare vendor/count consistently
- Add namespace-level tenant guardrails:
  - deny tenant workloads outside tenant namespaces when `tenancy.enabled=true`
  - optional allowlist for platform namespaces (`flexinfer-system`, `kube-system`)

### Deliverables

- `charts/flexinfer/templates/tenancy-admission-policies.yaml` (new)
- `charts/flexinfer/values.yaml` additions under `tenancy.admission.*`
- Runbook updates in `docs/DEPLOYMENT_RUNBOOK.md`
- Policy tests/examples in `docs/examples/tenancy/`

### Acceptance Criteria

- Invalid workloads are rejected with actionable validation messages.
- Valid workloads in tenant namespaces continue to apply.
- Policies are opt-in by chart value and safe by default for existing clusters.

## MT-3: Tenant-Aware Fair-Share Scheduling

Goal: reduce starvation by balancing GPU usage across tenants under contention.

### Scope

- Add tenant identity signal to routing/scheduling path:
  - tenant namespace as primary key
  - optional tenant label override
- Introduce fair-share score term in scheduler extender:
  - penalize tenants above configured share/budget
  - boost tenants below configured share
- Expose tenant scheduling metrics:
  - `flexinfer_scheduler_tenant_usage_ratio`
  - `flexinfer_scheduler_tenant_score_adjustment`

### Deliverables

- Scheduler config updates (`charts/flexinfer/values.yaml` -> `scheduler.tenantFairShare.*`)
- Scheduler scoring updates in `scheduler/` package
- Metrics wiring in `pkg/metrics/`
- Test coverage in `scheduler/*_test.go`

### Acceptance Criteria

- Under synthetic contention, no tenant permanently starves.
- Score adjustments are visible in metrics and logs.
- Feature can be disabled to preserve current scheduling behavior.

## Rollout Order

1. MT-2 admission guardrails (safety first)
2. MT-3 fair-share scheduling (contention optimization)

## Tracking

- Primary issue: [#2](https://gitlab.flexinfer.ai/services/flexinfer/-/issues/2)
- Roadmap index: [#1](https://gitlab.flexinfer.ai/services/flexinfer/-/issues/1)
