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
- **Unified runtime (`flexinfer-runtime`)**: exposes `/metrics` on the runtime API port (default `:8080`).

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
- `flexinfer_kvcache_pressure_evictions_total{model,namespace}` (counts Evict-policy scale-downs under KV-cache pressure)
- `flexinfer_quantization_duration_seconds{model,format,type}` (histogram)
- `flexinfer_quantization_compression_ratio{model,format}`
- `flexinfer_quantization_jobs_total{model,status}`
- `flexinfer_quantization_cache_size_bytes{model,format}`
- `flexinfer_model_cold_start_duration_seconds{model,namespace,backend,cache_strategy}` (histogram)
- `flexinfer_model_swap_duration_seconds{model,namespace,backend,group}` (histogram)
- `flexinfer_gpu_lease_active{group,namespace,owner}` (1 while a training/quant GPU lease holds a shared-GPU group and the serving incumbent is parked, 0 once released)
- `flexinfer_gpu_lease_acquired_total{group,namespace,owner}` (counter — total GPU lease acquisitions by training workloads)
- `flexinfer_model_backfill_starts_total{backfill,namespace,model}` (counter — admitted background Job attempts)
- `flexinfer_model_backfill_completions_total{backfill,namespace,model,result}` (counter — terminal attempts by result)
- `flexinfer_model_backfill_preemptions_total{backfill,namespace,model,reason}` (counter — foreground/gaming cancellations)
- `flexinfer_model_backfill_useful_running_seconds_total{backfill,namespace,model}` (counter — cumulative running time, excluding idle waits)

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
- `flexinfer_proxy_label_group_route_decisions_total{label,strategy,outcome}`
- `flexinfer_proxy_label_group_route_target_hits_total{label,strategy,model}`
- `flexinfer_proxy_queue_depth{model}`
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
- `flexinfer_proxy_request_prompt_tokens{model}` (histogram) — upstream-reported
  `prompt_tokens` per request, by resolved model. Observes **all non-streaming
  completions, plus streaming completions that carry a terminal `usage` chunk**
  (i.e. the client set `stream_options.include_usage`). Streaming requests that
  omit that opt-in are still unobserved.
- `flexinfer_proxy_request_completion_tokens{model}` (histogram) — upstream-reported
  `completion_tokens` per request, by resolved model. Same coverage as the prompt
  histogram above.
- `flexinfer_proxy_completions_total{model,stream}` (counter) — successful completion
  responses by resolved model and `stream` flag. **Coverage denominator** for the two
  histograms above: the `stream="true"` share bounds the remaining blind spot
  (streaming requests without a usage chunk). Read it before trusting a
  histogram-based workload verdict — e.g.
  `sum by (stream) (rate(flexinfer_proxy_completions_total{model="gemma4-26b-a4b-gptq"}[1d]))`.
  A high streaming share whose usage chunks are absent means the shape data is
  biased and the verdict must be read with caution. Streaming usage-chunk capture
  shipped in `internal/proxy/usage_log.go` (`usageSniffingBody`).

> **Why these exist (traffic-shape observability).** The per-request usage *log*
> line (`event=request_usage`, `internal/proxy/usage_log.go`) is the richer
> source but is **unreachable in the log aggregator**: the proxy is pinned to a
> control-plane node whose pod logs are not scraped into Loki, and its stream is
> drowned by controller-runtime `v1 Endpoints is deprecated` spam. These two
> histograms are the durable, scrape-reliable view of the per-lane traffic mix.
> Read them to ground workload-conditional decisions — e.g. blanket n-gram
> speculative decoding is a win on short Q/A but a tax on long-form generation
> (`ngram-sd-workload-conditional`), so the completion-token distribution and the
> prompt:completion ratio per lane decide whether SD should stay on. Example:
> `histogram_quantile(0.5, sum by (le) (rate(flexinfer_proxy_request_completion_tokens_bucket{model="gemma4-26b-a4b-gptq"}[1d])))`.

## Runtime Metrics (`flexinfer-runtime`)

Defined in `services/flexinfer/internal/runtime/metrics.go`:

- `flexinfer_runtime_info{node,gpu_vendor,gpu_arch,runtime_profile,runtime_digest}` (gauge; always `1`; metadata labels identify the DaemonSet profile and immutable image digest, or `unresolved` for mutable tags)
- `flexinfer_runtime_uptime_seconds`
- `flexinfer_runtime_model_loads_total{backend,result}`
- `flexinfer_runtime_model_unloads_total{backend,reason}`
- `flexinfer_runtime_model_load_duration_seconds{model,backend}` (histogram)
- `flexinfer_runtime_model_state{model,backend,state}`
- `flexinfer_runtime_backend_crashes_total{model,backend}`
- `flexinfer_runtime_health_check_failures_total{model,backend}`
- `flexinfer_runtime_gpu_vram_total_bytes{gpu_vendor,gpu_arch}`
- `flexinfer_runtime_gpu_vram_used_bytes{gpu_vendor,gpu_arch}`
- `flexinfer_runtime_gpu_vram_free_bytes{gpu_vendor,gpu_arch}`
- `flexinfer_runtime_gpu_temperature_celsius{gpu_vendor,gpu_arch}`

## GPUGroup Controller Metrics (v1alpha1) — never implemented, superseded

The `flexinfer_gpugroup_*` family (and the proxy-side
`flexinfer_proxy_gpugroup_*` pair) was specified for the v1alpha1 GPUGroup
workflow but never implemented: no `controllers/gpugroup_controller.go`
exists, and no code emits these series (verified 2026-06-10 via
`git grep 'flexinfer_gpugroup' -- '*.go'`). The v1alpha1
ModelDeployment + GPUGroup workflow was replaced by the v1alpha2 shared-GPU
group scheduler (`controllers/model_shared_gpu.go`).

Use the shared-group scheduling metrics instead (see "Recently Shipped
Metrics" below):

| Specified but never emitted | Live equivalent |
|---|---|
| `flexinfer_gpugroup_active_model` | `flexinfer_sharedgroup_state{state="Active"}` |
| `flexinfer_gpugroup_swaps_total` | `flexinfer_sharedgroup_preemptions_total` |
| `flexinfer_gpugroup_model_queue_depth` | `sum by (group) (flexinfer_sharedgroup_state{state="Queued"})` |
| run-duration / cooldown / swap-blocked series | none (anti-thrashing internals are not exported) |

## Recently Shipped Metrics

The following metrics were added as part of the Sprint 1 observability initiative:

- **Model lifecycle** (emitted by `flexinfer-manager`): ✅
  - `flexinfer_model_phase{model,namespace,phase}` (gauge; `1` for current phase)
  - `flexinfer_model_transitions_total{model,namespace,from,to,reason}` (counter)
  - `flexinfer_model_ready_latency_seconds{model,namespace,backend}` (histogram; reconcile to Ready)
- **Serverless activations** (emitted by proxy): ✅
  - `flexinfer_proxy_activation_duration_seconds{model,backend,result}` (histogram; request seen to Ready)
  - `flexinfer_proxy_activation_failures_total{model,reason}` (counter; scale_up, ready_timeout, context_cancelled)
- **Shared-group scheduling (v1alpha2)** (emitted by `flexinfer-manager`): ✅
  - `flexinfer_sharedgroup_state{group,model,namespace,state}` (gauge; Active/Queued/Preempted)
  - `flexinfer_sharedgroup_preemptions_total{group,namespace,from,to}` (counter)
- **Cache/prefetch** (emitted by `flexinfer-manager`): ✅
  - `flexinfer_model_cache_job_duration_seconds{model,namespace,job_type,result}` (histogram; download/abliterate/quantize)
  - `flexinfer_model_cache_failures_total{model,namespace,reason}` (counter)
  - `flexinfer_model_cache_job_create_conflicts_total{job_type}` (counter; AlreadyExists races tolerated by the idempotent-create guard during controller rolling updates — `job_type` is the stage: download/abliterate/quantize/publish/publish_validate/stage_publish/finetune/image_warmup/cache-stage/cache-check/cache-prefetch)
  - `flexinfer_owned_jobs_stale_generation{owner_kind,namespace,owner}` (gauge; Jobs owned by a Model/ModelCache whose `flexinfer.ai/owner-generation` stamp is older than the owner's current `metadata.generation` — i.e. created under a superseded spec. Observability only, never auto-deleted: `metadata.generation` bumps on any spec change, so an older stamp does not imply the Job is unwanted. Series present ⟺ stale Jobs exist for that owner)
- **Benchmarks** (emitted by benchmarker via `pkg/metrics`): ✅
  - `flexinfer_benchmark_tokens_per_second{model,backend,gpu_vendor,gpu_arch}` (gauge)
  - `flexinfer_benchmark_vram_used_bytes{model,backend,gpu_vendor,gpu_arch}` (gauge; defined, not yet populated)
