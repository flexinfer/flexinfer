# Quantization Pipelines Execution Plan

> Last updated: 2026-02-17  
> Tracking issue: [#7 Quantization pipelines execution](https://gitlab.flexinfer.ai/services/flexinfer/-/issues/7)

This checklist converts `docs/design/quantization-pipelines.md` into a bounded execution plan with clear validation gates.

## Current Implementation Snapshot

Implemented today:
- [x] `ModelCache.spec.quantization` API and status fields (`api/v1alpha1/modelcache_types.go`)
- [x] Quantization builders for `GGUF`, `AWQ`, and `GPTQ` (`pkg/quantization/`)
- [x] Quantization builder for `EXL2` (`pkg/quantization/exl2.go`)
- [x] Quantization builder for `FP8` (`pkg/quantization/fp8.go`)
- [x] ModelCache quantization lifecycle integration (download -> quantize job -> status/metrics) (`controllers/modelcache_controller.go`)
- [x] Quantization metrics (`pkg/metrics/exporter.go`)
- [x] CLI flows:
  - `flexinfer quantize <cache>`
  - `flexinfer quantize formats`
  - `flexinfer quantize status <cache>` (`cmd/flexinfer/commands/quantize.go`)
- [x] Controller/envtest and unit coverage for quantization paths (`controllers/quantization_test.go`, `controllers/quantization_metadata_test.go`, `pkg/quantization/quantization_test.go`)
- [x] User/operator docs for quantization usage (`docs/user/quantization.md`, `docs/CONFIGURATION.md`)

Planned (not implemented yet):
- [x] Auto-selection of quantization format based on model + GPU constraints
- [ ] Quantization quality validation loop (perplexity/acceptance benchmark baseline)

## Milestones

### QP-1: Baseline Pipeline Validation (GGUF/AWQ/GPTQ)
- [x] Verify declarative flow with `ModelCache.spec.quantization` creates quantization Job.
- [x] Verify status transitions `Provisioning -> Quantizing -> Ready/Failed`.
- [x] Verify metadata ingestion updates `status.quantization` and `status.path`.
- [x] Verify quantization metrics emission (`duration`, `ratio`, `jobs_total`, `cache_size`).

Acceptance:
- Existing unit/envtest suites pass without regressions.

### QP-2: Operator Runbook + CI-Friendly Validation
- [x] Document deterministic validation commands for local/CI execution.
- [x] Record expected output signals and failure triage commands.

Acceptance:
- A contributor can validate quantization behavior end-to-end from docs alone.

### QP-3: Format Expansion (EXL2/FP8)
- [x] Add `EXL2` job builder + validation constraints.
- [x] Add `FP8` job builder + validation constraints.
- [x] Extend CLI/help text with runtime-ready status for new formats.
- [x] Add tests for new builders and controller integration points.

Acceptance:
- [x] `quantization.GetBuilder(EXL2)` returns a concrete builder.
- [x] `quantization.GetBuilder(FP8)` returns a concrete builder.
- [x] `flexinfer quantize formats` shows implemented for both.

### QP-4: Auto-Selection + Recommendations
- [x] Add recommendation logic from model footprint + cluster GPU capabilities.
- [x] Surface recommendation in CLI and/or controller events.
- [x] Guard with explicit opt-in to preserve existing behavior.

Acceptance:
- Recommendation output is deterministic and covered by tests.
- No behavior change for users who do not opt in.

## Validation Gate (Per Slice)

Targeted checks:

```bash
go test ./pkg/quantization/... -count=1
go test ./controllers -run Quantization -count=1
go test ./cmd/flexinfer/commands -run Quantize -count=1
```

Operational checks:

```bash
kubectl get modelcache -n flexinfer-system
kubectl get jobs -n flexinfer-system | grep quantize
kubectl logs -n flexinfer-system job/<cache-name>-quantize
flexinfer quantize status <cache-name> -n flexinfer-system
```

## Done Criteria for Issue #7

Issue #7 can be closed when:
1. `QP-1` and `QP-2` remain green against current `master`.
2. Either `QP-3` and `QP-4` are completed, or are split into explicit follow-up issues with clear ownership and acceptance criteria.
3. Roadmap docs point to this execution plan as the source of truth for quantization progress.
