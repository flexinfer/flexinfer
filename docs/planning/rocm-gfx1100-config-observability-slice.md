# ROCm/gfx1100 Config + Observability Slice

> Last updated: 2026-02-18
> Status: Complete (PR-1 merged on 2026-02-18)

## Goal

Improve operator ergonomics and runtime confidence for AMD RDNA3 (`gfx1100`) deployments by:

1. Reducing config drift between Helm values, controller env vars, and backend selection.
2. Improving deploy/model-swap speed controls.
3. Expanding runtime visibility (metrics now, traces next).

## Why This Slice

This directly supports current FlexInfer positioning as an on-prem/hybrid control plane for sensitive workloads with strong operational observability, and aligns with adjacent platform lanes:

- FlexInfer: private runtime + inference control plane with explicit configuration boundaries.
- fi-fhir: sensitive workflow traceability and OpenTelemetry-first operations.
- Loom Core: audit-oriented context controls with opt-in tracing patterns.

## Scope In

1. Wire `gfx1100Image` Helm values to controller env vars used by backend image resolvers.
2. Add `gfx1100`-aware image resolution in legacy v1alpha1 controller path.
3. Normalize AMD GPU architecture detection from common label keys.
4. Add/refresh targeted tests for image resolution and env precedence.
5. Document resolved env/value wiring in operator docs.

## Scope Out

1. Full context-aware router implementation.
2. Quantization auto-selection and quality gate implementation.
3. Full distributed tracing rollout in every component (tracked as a follow-up slice).

## Acceptance Criteria

1. Setting `mlcllm.gfx1100Image` and `vllm.gfx1100Image` in Helm changes selected backend image for `gfx1100` targets.
2. Legacy v1alpha1 and current backend abstractions both support explicit `gfx1100` image override env vars.
3. AMD architecture detection works for at least:
   - `amd.com/gpu.arch`
   - `gpu.amd.com/gpu-architecture`
   - `flexinfer.ai/gpu.arch`
4. Targeted tests pass for new selection logic and no regression in existing backend image tests.
5. Docs state how to configure defaults vs `gfx1100` overrides.

## Risks

1. Legacy v1alpha1 behavior drift if defaults are changed too aggressively.
2. Image naming inconsistencies across registries (`library/*` vs `flexinfer/*`) can cause confusion.
3. Expanded config surface can increase operator error without clear docs/tests.

## Dependencies

1. Existing backend image env var contract in controller + Helm chart.
2. Existing ROCm/gfx1100 backend images in registry.
3. Existing controller/backend test suite.

## Validation Plan

```bash
go test ./controllers -run 'GetBackendImage|GetGPUArchitecture' -count=1
go test ./backend/... -count=1
helm template charts/flexinfer >/tmp/flexinfer-render.yaml
rg "DEFAULT_MLC_LLM_IMAGE_GFX1100|DEFAULT_VLLM_IMAGE_GFX1100" /tmp/flexinfer-render.yaml
```

## Follow-Up Slice Candidates

1. Deploy/swap speed knobs: make flash-loader image/concurrency and `/dev/shm` sizing operator-configurable in v1alpha2 path. (active: `docs/planning/rocm-gfx1100-deploy-swap-tracing-slice.md`)
2. Detailed metrics: add first-class cold-start and swap latency histograms with consistent model/backend labels.
3. Tracing foundation: add optional OTel tracing in proxy/controller, reusing Loom Core opt-in model.
