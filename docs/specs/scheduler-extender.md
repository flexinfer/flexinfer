---
title: Scheduler extender
description: Kube scheduler extender endpoints and scoring knobs.
---

# Scheduler extender

Binary: `flexinfer-sched` (`services/flexinfer/cmd/flexinfer-sched`)

FlexInfer uses the Kubernetes scheduler extender mechanism to:

- filter nodes that can run GPU workloads
- score nodes using benchmark and heuristic signals

## Endpoints

The extender exposes:

- `POST /filter`
- `POST /score`
- `GET /healthz`

The request/response types follow the kube-scheduler extender v1 API:

- request: `k8s.io/kube-scheduler/extender/v1.ExtenderArgs`
- response:
  - filter: `ExtenderFilterResult`
  - score: `[]HostPriority`

## Environment variables

These influence scoring (see `services/flexinfer/scheduler/scheduler.go`):

- `BENCHMARK_RESULTS_CONFIGMAP` (default `flexinfer-benchmark-results`)
- `SCHED_TPS_WEIGHT` (default `0.7`)
- `SCHED_UTIL_WEIGHT` (default `0.2`)
- `SCHED_COST_WEIGHT` (default `0.1`)
- `SCHED_CACHE_WEIGHT` (default `0.3`)

## Inputs used for scoring

- Pod annotations:
  - `flexinfer.ai/model`
  - `flexinfer.ai/backend`
- Benchmark results (ConfigMap data)
- Optional node annotations (if present):
  - `flexinfer.ai/gpu.util`
  - `flexinfer.ai/cost`
  - `flexinfer.ai/kv-cache-usage`

