---
title: Metrics
description: Prometheus metrics exposed by FlexInfer components.
---

# Metrics

FlexInfer components expose Prometheus metrics. The exact set depends on which binaries you deploy.

## Where Metrics Live

- **Controller manager (`flexinfer-manager`)**: exposes `/metrics` on `--metrics-bind-address` (default `:8080`) via controller-runtime.
- **Proxy (`flexinfer-proxy`)**: exposes `/metrics` on the proxy HTTP port (default `:8080`).
- **Node agent (`flexinfer-agent`)**: exposes `/metrics` on `--metrics-port` (default `:9100`).

Note: backend containers (e.g. diffusers-api, llama.cpp servers) are not required to expose Prometheus metrics. In the homelab, `diffusers-api` returns `404` for `/metrics` (expected).

## Shared Metrics Library (`pkg/metrics`)

Defined in `services/flexinfer/pkg/metrics/exporter.go`:

- `flexinfer_tokens_per_second{model,backend,node}`
- `flexinfer_model_load_seconds{model,node}`
- `flexinfer_gpu_temperature_celsius{gpu,node}`
- `flexinfer_gpu_vram_free_bytes{gpu,node,vendor}`
- `flexinfer_gpu_vram_total_bytes{gpu,node,vendor}`
- `flexinfer_gpu_vram_used_bytes{gpu,node,vendor}`
- `flexinfer_gpu_vram_utilization_percent{gpu,node,vendor}`
- `flexinfer_modelcache_resident_seconds{cache,node,strategy}`
- `flexinfer_dev_shm_utilization_percent{node}`
- `flexinfer_modelcache_evictions_total{cache,node,policy}`
- `flexinfer_modelcache_hit_rate{cache,node}`
- `flexinfer_modelcache_size_bytes{cache,node,strategy}`
- `flexinfer_modelcache_access_count{cache,node}`
- `flexinfer_modelcache_phase{cache,namespace,phase}`
- `flexinfer_quantization_duration_seconds{model,format,type}` (histogram)
- `flexinfer_quantization_compression_ratio{model,format}`
- `flexinfer_quantization_jobs_total{model,status}`
- `flexinfer_quantization_cache_size_bytes{model,format}`
- `flexinfer_model_cold_start_duration_seconds{model,namespace,backend,cache_strategy}` (histogram)
- `flexinfer_model_swap_duration_seconds{model,namespace,backend,group}` (histogram)

### Registry Note (Important)

These metrics are registered to **controller-runtime's Prometheus registry** so they show up on:

- `flexinfer-manager` `/metrics` (controller-runtime server)
- any component that serves `sigs.k8s.io/controller-runtime/pkg/metrics.Registry` (the agent does)

## Proxy Metrics (`flexinfer-proxy`)

Defined in `services/flexinfer/internal/proxy/metrics.go`:

- `flexinfer_proxy_requests_total{model,status}`
- `flexinfer_proxy_scale_ups_total{model}`
- `flexinfer_proxy_request_duration_seconds{model}` (histogram)
- `flexinfer_proxy_queued_requests_total{model}`
- `flexinfer_proxy_queue_rejected_total{model}`
- `flexinfer_proxy_queue_wait_duration_seconds{model}` (histogram)
- `flexinfer_proxy_active_connections{model}`
- `flexinfer_proxy_queue_depth{model}`
- `flexinfer_proxy_gpugroup_swap_signals_total{gpugroup,model}`
- `flexinfer_proxy_gpugroup_queued_requests_total{gpugroup,model}`
- `flexinfer_proxy_endpoint_changes_total{model,change_type}`
- `flexinfer_proxy_endpoint_count{model}`
- `flexinfer_proxy_endpoint_refresh_duration_seconds` (histogram)
- `flexinfer_proxy_routing_decisions_total{model,strategy,key_source,outcome}`
- `flexinfer_proxy_routing_target_hits_total{model,strategy,target}`
- `flexinfer_proxy_routing_key_cardinality{model,strategy,key_source}`
- `flexinfer_proxy_routing_key_cardinality_overflow_total{model,strategy,key_source}`
- `flexinfer_proxy_rate_limited_total{model,scope}`
- `flexinfer_proxy_activation_retries_total{model}`
- `flexinfer_proxy_activation_retry_wait_duration_seconds{model}` (histogram)

## GPUGroup Controller Metrics (v1alpha1)

Defined in `services/flexinfer/controllers/gpugroup_controller.go`:

- `flexinfer_gpugroup_active_model{gpugroup,model,namespace}` (gauge; `1=active`)
- `flexinfer_gpugroup_model_run_duration_seconds{gpugroup,model,namespace}` (gauge)
- `flexinfer_gpugroup_swap_cooldown_seconds{gpugroup,namespace}` (gauge)
- `flexinfer_gpugroup_swaps_total{gpugroup,from_model,to_model,namespace}` (counter)
- `flexinfer_gpugroup_swap_blocked_antithrashing_total{gpugroup,namespace}` (counter)
- `flexinfer_gpugroup_swap_blocked_cooldown_total{gpugroup,namespace}` (counter)
- `flexinfer_gpugroup_model_queue_depth{gpugroup,model,namespace}` (gauge)

These only appear once the controller has observed at least one GPUGroup and called `WithLabelValues(...)` for the series.

## Planned Metrics Extensions

What we have today is enough for basic proxy load + queueing visibility, but it is missing first-class lifecycle metrics for v1alpha2 `Model`.

Recommended additions (all emitted by `flexinfer-manager` unless noted):

- **Model lifecycle**:
  - `flexinfer_model_phase{model,namespace,phase}` (gauge; `1` for current phase)
  - `flexinfer_model_transitions_total{model,namespace,from,to,reason}` (counter)
  - `flexinfer_model_ready_latency_seconds{model,namespace,backend}` (histogram; reconcile to Ready)
- **Serverless activations** (proxy):
  - `flexinfer_proxy_activation_duration_seconds{model,backend,result}` (histogram; request seen to Ready)
  - `flexinfer_proxy_activation_failures_total{model,reason}` (counter; timeout, validation, backend error)
- **Shared-group scheduling (v1alpha2)**:
  - `flexinfer_sharedgroup_state{group,model,namespace,state}` (gauge; Active/Queued/Preempted)
  - `flexinfer_sharedgroup_preemptions_total{group,namespace,from,to}` (counter)
- **Cache/prefetch**:
  - `flexinfer_model_cache_job_duration_seconds{model,namespace,job_type,result}` (histogram; prefetch/check)
  - `flexinfer_model_cache_failures_total{model,namespace,reason}` (counter)
- **Benchmarks**:
  - publish benchmark results into metrics alongside ConfigMaps:
    - `flexinfer_benchmark_tokens_per_second{model,backend,gpu_vendor,gpu_arch}` (gauge)
    - `flexinfer_benchmark_vram_used_bytes{model,backend,gpu_vendor,gpu_arch}` (gauge)
