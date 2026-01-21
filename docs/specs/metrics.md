---
title: Metrics
description: Prometheus metrics exposed by FlexInfer components.
---

# Metrics

FlexInfer components expose Prometheus metrics. The exact set depends on which binaries you deploy.

## Shared library metrics (`pkg/metrics`)

Defined in `services/flexinfer/pkg/metrics/exporter.go`:

- `flexinfer_tokens_per_second{model,backend,node}`
- `flexinfer_model_load_seconds{model,node}`
- `flexinfer_gpu_temperature_celsius{gpu,node}`
- `flexinfer_modelcache_resident_seconds{cache,node,strategy}`
- `flexinfer_dev_shm_utilization_percent{node}`
- `flexinfer_modelcache_evictions_total{cache,node,policy}`
- `flexinfer_modelcache_hit_rate{cache,node}`
- `flexinfer_modelcache_size_bytes{cache,node,strategy}`
- `flexinfer_modelcache_access_count{cache,node}`
- `flexinfer_modelcache_phase{cache,namespace,phase}`

## Proxy metrics (`flexinfer-proxy`)

Defined in `services/flexinfer/cmd/flexinfer-proxy/main.go`:

- `proxy_requests_total{model,status}`
- `proxy_scale_ups_total{model}`
- `proxy_request_duration_seconds{model}`
- `proxy_queued_requests_total{model}`
- `proxy_queue_rejected_total{model}`
- `proxy_queue_wait_seconds{model}`
- `proxy_active_connections{model}`
- `proxy_queue_depth{model}`
- `proxy_gpugroup_swap_signals_total{gpugroup,model}`
- `proxy_gpugroup_queued_requests_total{gpugroup,model}`

