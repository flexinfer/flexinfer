# ROCm/gfx1100 Deploy-Swap + Tracing Slice (PR-2)

> Last updated: 2026-02-18
> Status: In Progress

## Goal

Ship the next ROCm/gfx1100 operations slice focused on:

1. Faster and safer deploy/swap behavior through controller-level config abstraction.
2. First-class cold-start and shared-swap latency visibility.
3. Opt-in tracing foundation for manager/proxy request and reconcile paths.

## Product Positioning Alignment

This slice reinforces FlexInfer positioning used across `services/flexinfer-site`:

- Private runtime and inference control plane for Kubernetes.
- On-prem and hybrid inference operations for sensitive workloads.
- Explicit configuration boundaries, rollout controls, and operational observability.

Cross-lane consistency:

- `libs/fi-fhir`: healthcare-aligned sensitive-data workflows depend on traceable runtime behavior.
- `services/loom-core`: opt-in, audit-oriented context controls align with optional tracing enablement.

## Scope In

1. Controller runtime knobs for v1alpha2 model pods:
   - `DEFAULT_SHM_SIZE_LIMIT`
   - `DEFAULT_FLASH_LOADER_ENABLED`
   - `DEFAULT_FLASH_LOADER_IMAGE`
   - `DEFAULT_FLASH_LOADER_CONCURRENCY`
   - `DEFAULT_FLASH_LOADER_TMPFS_SIZE_LIMIT`
2. Flash-loader configuration resolution:
   - use matching `ModelCache.spec.flashLoader` overrides when present
   - otherwise use controller defaults
3. Lifecycle latency metrics:
   - `flexinfer_model_cold_start_duration_seconds{model,namespace,backend,cache_strategy}`
   - `flexinfer_model_swap_duration_seconds{model,namespace,backend,group}`
4. Tracing foundation:
   - opt-in OTel bootstrap in manager + proxy
   - initial reconcile/request spans with context propagation support
5. Helm + docs updates for all new controls.

## Scope Out

1. Full distributed tracing across every controller/backend component.
2. New CRD API surface for per-model v1alpha2 flash-loader fields (deferred).
3. End-to-end tracing dashboards/alerting policy pack.

## Acceptance Criteria

1. Helm values can set shm and flash-loader defaults, and rendered controller env reflects them.
2. Matching `ModelCache.spec.flashLoader` can override global defaults for image/concurrency/tmpfs.
3. Cold-start and swap latency histograms are emitted on manager `/metrics`.
4. Tracing can be enabled with `FLEXINFER_OTEL_ENABLED=true` and OTLP endpoint config.
5. Controller + proxy tests pass with no regressions in existing behavior.
