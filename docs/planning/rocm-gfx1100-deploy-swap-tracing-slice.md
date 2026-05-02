# ROCm/gfx1100 Deploy-Swap + Tracing Slice (PR-2)

> Last updated: 2026-05-02
> Status: Proof-gathering after partial implementation

## Goal

Make the ROCm/gfx1100 deploy/swap path operator-ready by proving and documenting
three connected surfaces:

1. Controller-level defaults for `/dev/shm` and flash-loader behavior.
2. Lifecycle latency metrics for cold starts and shared-GPU swaps.
3. Opt-in OpenTelemetry tracing in the manager and proxy request paths.

This slice reinforces FlexInfer's positioning as an on-prem and hybrid inference
control plane for sensitive workloads: runtime changes should be explicit,
observable, and reversible through Helm and GitOps.

## Non-Goals

- Build full distributed traces across every controller, backend, and runtime
  component.
- Add new CRD fields beyond the flash-loader surfaces already present on
  `ModelCache` and v1alpha2 `Model` cache specs.
- Ship tracing dashboards, alert rules, or a collector stack.
- Change backend images, ROCm runtime Dockerfiles, or scheduler scoring.
- Modify production manifests outside the Helm chart defaults and docs.

## Users / Operators

- Homelab and platform operators running AMD RX 7900 XTX / ROCm `gfx1100` model
  pods in k3s.
- FlexInfer maintainers validating deploy/swap behavior before promoting a chart.
- Observability operators who need cold-start, swap, reconcile, and proxy request
  signals without enabling tracing by default.
- Adjacent sensitive-workload lanes, such as healthcare workflows, that depend on
  auditable runtime behavior.

## Current Evidence

- Helm already renders controller runtime defaults from `controller.runtime` into
  manager environment variables, including `DEFAULT_SHM_SIZE_LIMIT` and the
  `DEFAULT_FLASH_LOADER_*` settings
  (`charts/flexinfer/templates/deployment.yaml`,
  `charts/flexinfer/values.yaml`).
- Helm already exposes opt-in tracing values under `observability.tracing` and
  renders `FLEXINFER_OTEL_ENABLED`, `OTEL_EXPORTER_OTLP_ENDPOINT`, and
  `FLEXINFER_OTEL_SERVICE_NAMESPACE` for the manager
  (`charts/flexinfer/templates/deployment.yaml`,
  `charts/flexinfer/values.yaml`).
- `controllers/flash_loader.go` resolves flash-loader settings from controller
  env defaults, then matching v1alpha1 `ModelCache.spec.flashLoader`, then
  v1alpha2 `Model.spec.cache.flashLoader`.
- `controllers/model_helpers.go` records cold-start and shared swap latency when
  models transition to `Ready`.
- `pkg/metrics/exporter.go` defines and registers
  `flexinfer_model_cold_start_duration_seconds` and
  `flexinfer_model_swap_duration_seconds`.
- `pkg/observability/tracing.go` initializes OTLP/HTTP tracing only when
  `FLEXINFER_OTEL_ENABLED=true`, installs trace-context propagation, and provides
  reconcile span helpers.
- `cmd/flexinfer-manager/main.go` and `cmd/flexinfer-proxy/main.go` call
  `observability.InitTracing` during startup.
- Proxy and controller paths already include initial spans in
  `internal/proxy/*` and multiple controllers, but this slice owns only planning
  proof for the requested manager/proxy entry points.

## Requirements

### Functional

- Helm values must keep `/dev/shm` and flash-loader defaults configurable without
  requiring CRD changes.
- Controller flash-loader resolution must preserve this precedence:
  environment defaults, matching `ModelCache.spec.flashLoader`, then v1alpha2
  `Model.spec.cache.flashLoader`.
- Tracing must remain disabled by default and must activate only through
  `FLEXINFER_OTEL_ENABLED=true` / Helm `observability.tracing.enabled=true`.

### Observability / Status

- Manager `/metrics` must expose cold-start and shared-swap latency histograms
  with bounded labels:
  - `flexinfer_model_cold_start_duration_seconds{model,namespace,backend,cache_strategy}`
  - `flexinfer_model_swap_duration_seconds{model,namespace,backend,group}`
- Reconcile spans should include namespace/name/controller attributes and record
  errors through existing metrics.
- Proxy request spans should extract incoming trace context and keep request
  tracing optional.

### Operational

- Helm rendering must make every default inspectable before rollout.
- Rollback must be possible by reverting the chart/code commit or disabling the
  relevant Helm values.
- Generated CRDs are not expected for this slice unless another slice changes API
  types.

## Acceptance Criteria

- `helm template` or the chart unit path shows controller env for
  `DEFAULT_SHM_SIZE_LIMIT`, `DEFAULT_FLASH_LOADER_ENABLED`,
  `DEFAULT_FLASH_LOADER_IMAGE`, `DEFAULT_FLASH_LOADER_CONCURRENCY`, and optional
  `DEFAULT_FLASH_LOADER_TMPFS_SIZE_LIMIT`.
- Tests or review proof show matching `ModelCache.spec.flashLoader` overrides
  global flash-loader defaults for image, concurrency, and tmpfs when present.
- Metrics proof shows both lifecycle histograms are registered and observed by
  controller lifecycle transitions.
- Tracing proof shows manager and proxy initialize OpenTelemetry only when
  enabled and that request/reconcile span entry points exist.
- Documentation names rollout/backout behavior for Helm, Flux, and runtime
  observability toggles.
- `git diff --check` and targeted `rg` checks pass for this planning update.

## Implementation Slices

### PR-2A: Flash-Loader Config Proof

- Status: complete as of 2026-05-02.
- Owner boundary:
  - `controllers/flash_loader.go`
  - `controllers/model_controller_test.go`
  - `charts/flexinfer/templates/deployment.yaml`
  - `charts/flexinfer/values.yaml`
  - related configuration docs only
- Proof summary:
  - Controller tests cover global defaults, matching v1alpha1
    `ModelCache.spec.flashLoader` overrides, v1alpha2
    `Model.spec.cache.flashLoader` overrides, Local/shared auto-enable,
    persistent shared tmpfs, and non-shared ephemeral tmpfs.
  - Helm renders `DEFAULT_SHM_SIZE_LIMIT` and the `DEFAULT_FLASH_LOADER_*`
    defaults from `controller.runtime`.
  - `docs/CONFIGURATION.md` and `docs/user/caching.md` document the precedence
    chain and rollout/backout knobs.
- Do not touch: metrics exporter, tracing package, proxy request handling.
- Validation:
  - `go test ./controllers -run 'Test.*FlashLoader|Test.*ModelCache'`
  - `helm template flexinfer ./charts/flexinfer --set controller.runtime.flashLoader.enabled=true`

### PR-2B: Lifecycle Latency Metrics Proof

- Status: complete as of 2026-05-02.
- Owner boundary:
  - `controllers/model_helpers.go`
  - `pkg/metrics/exporter.go`
  - `pkg/metrics/exporter_test.go`
  - optional metrics docs
- Proof summary:
  - `pkg/metrics/exporter.go` defines and registers
    `flexinfer_model_cold_start_duration_seconds{model,namespace,backend,cache_strategy}`
    and `flexinfer_model_swap_duration_seconds{model,namespace,backend,group}`.
  - `controllers/model_helpers.go` observes cold-start latency when a model
    becomes `Ready` and observes shared-swap latency when a preempted/shared
    model returns to `Ready`.
  - `pkg/metrics/exporter_test.go` covers histogram bucket counts and sample
    observation for both metric families.
- Do not touch: Helm chart runtime config or tracing bootstrap.
- Validation:
  - `go test ./pkg/metrics`
  - `go test ./controllers -run 'Test.*Phase|Test.*Shared|Test.*Ready'`
  - `rg 'flexinfer_model_(cold_start|swap)_duration_seconds' pkg controllers docs`

### PR-2C: Tracing Foundation Proof

- Status: complete as of 2026-05-02.
- Owner boundary:
  - `pkg/observability/tracing.go`
  - `cmd/flexinfer-manager/main.go`
  - `cmd/flexinfer-proxy/main.go`
  - `internal/proxy/proxy.go`
  - `internal/proxy/models.go`
  - `internal/proxy/queue.go`
  - controller files that already call `StartReconcileSpan`
- Proof summary:
  - `pkg/observability/tracing.go` initializes OTLP/HTTP tracing only when
    `FLEXINFER_OTEL_ENABLED=true` and installs W3C trace-context/baggage
    propagation.
  - `pkg/observability/tracing_test.go` proves disabled-by-default startup does
    not fail even when OTLP env is invalid, and covers boolean env parsing.
  - `cmd/flexinfer-manager/main.go` and `cmd/flexinfer-proxy/main.go` call
    `observability.InitTracing`; Helm renders tracing env for the manager and
    activator/proxy templates when `observability.tracing.enabled=true`.
  - Controller reconcilers use `StartReconcileSpan`; proxy request/queue paths
    keep request tracing opt-in through the shared bootstrap.
- Do not touch: flash-loader config or lifecycle metric definitions.
- Validation:
  - `go test ./pkg/observability ./internal/proxy ./controllers`
  - `FLEXINFER_OTEL_ENABLED=false go test ./cmd/...`
  - `rg 'StartReconcileSpan|InitTracing|TraceContext|Extract' pkg cmd controllers internal`

### PR-2D: Helm and Docs Readiness

- Status: this document is the readiness conversion; another agent may own
  chart docs if needed.
- Owner boundary:
  - `docs/planning/rocm-gfx1100-deploy-swap-tracing-slice.md`
  - `docs/planning/README.md`
  - optional `docs/CONFIGURATION.md` if implementation proof finds drift
- Agent-ready task: keep the slice plan source-backed, mark proof gaps
  explicitly, and avoid code changes.
- Do not touch: `.loom`, generated CRDs, controllers, metrics, proxy, or chart
  templates.
- Validation:
  - `git diff --check`
  - `rg 'Readiness|Implementation Slices|Rollout' docs/planning/rocm-gfx1100-deploy-swap-tracing-slice.md`

## Readiness

- Status: PR-2A through PR-2C proof-complete; ready for live rollout validation
  on a selected gfx1100 model/node pair.
- Target files/modules:
  - Flash-loader config: `charts/flexinfer/templates/deployment.yaml`,
    `charts/flexinfer/values.yaml`, `controllers/flash_loader.go`,
    `controllers/model_controller_test.go`
  - Latency metrics: `controllers/model_helpers.go`,
    `pkg/metrics/exporter.go`, `pkg/metrics/exporter_test.go`
  - Tracing: `pkg/observability/tracing.go`,
    `cmd/flexinfer-manager/main.go`, `cmd/flexinfer-proxy/main.go`,
    `internal/proxy/*`, traced controller reconcilers
  - Docs/readiness: `docs/planning/rocm-gfx1100-deploy-swap-tracing-slice.md`,
    `docs/planning/README.md`
- Owner boundary: this Slice 2 planning branch owns only the two planning docs;
  code-owning slices should stay within the module boundaries listed above.
- Validation commands:
  - `git diff --check`
  - `rg 'Readiness|Implementation Slices|Rollout' docs/planning/rocm-gfx1100-deploy-swap-tracing-slice.md`
  - `go test ./pkg/metrics ./pkg/observability ./controllers -run 'ColdStart|Swap|Metric|Tracing|FlashLoader'`
  - `helm template flexinfer ./charts/flexinfer --set controller.runtime.flashLoader.enabled=true --set observability.tracing.enabled=true --set observability.tracing.otlpEndpoint=http://otel-collector.observability:4318`
- Generated artifacts: none for this planning-only conversion; no CRDs,
  dashboards, screenshots, or `.loom` artifacts are expected.
- Rollout/backout: see the dedicated rollout section below; follow-up code
  slices must keep Helm toggles disabled by default unless explicitly promoted.
- Non-blocking questions:
  - Should tracing be rendered for proxy/activator deployments in the same chart
    promotion as manager tracing, or tracked as a separate observability rollout?
  - Should cold-start histogram labels use the CRD backend string only, or should
    runtime profile / GPU architecture be added later through a low-cardinality
    label?
  - Resolved for PR-2A: flash-loader docs now describe v1alpha1 `ModelCache`
    and v1alpha2 `Model.spec.cache.flashLoader` precedence in one shared
    user-facing page.

## Validation Plan

- Planning validation:
  - `git diff --check`
  - `rg 'Readiness|Implementation Slices|Rollout' docs/planning/rocm-gfx1100-deploy-swap-tracing-slice.md`
- Helm validation:
  - Render defaults:
    `helm template flexinfer ./charts/flexinfer`
  - Render enabled flash-loader and tracing:
    `helm template flexinfer ./charts/flexinfer --set controller.runtime.flashLoader.enabled=true --set observability.tracing.enabled=true --set observability.tracing.otlpEndpoint=http://otel-collector.observability:4318`
- Go validation:
  - `go test ./controllers`
  - `go test ./pkg/metrics ./pkg/observability ./internal/proxy`
  - `go test ./cmd/...`
- Runtime smoke validation after rollout:
  - `kubectl -n flexinfer-system get deploy flexinfer-controller -o yaml | rg 'DEFAULT_SHM_SIZE_LIMIT|DEFAULT_FLASH_LOADER|FLEXINFER_OTEL|OTEL_EXPORTER_OTLP_ENDPOINT'`
  - `kubectl -n flexinfer-system port-forward deploy/flexinfer-controller 8080:8080`
  - `curl -s localhost:8080/metrics | rg 'flexinfer_model_(cold_start|swap)_duration_seconds'`

## Rollout / Backout

- Rollout:
  - Merge proof/code slices only after targeted tests and Helm rendering pass.
  - Bump chart metadata only in the chart-owning slice if rendered behavior
    changes.
  - Reconcile through Flux:
    `flux reconcile source git flexinfer -n flux-system` and
    `flux reconcile helmrelease flexinfer -n flexinfer-system`.
  - Keep `observability.tracing.enabled=false` until an OTLP collector endpoint
    is known-good.
  - Enable flash-loader defaults first on one `gfx1100` node/model pair, then
    broaden after cold-start and swap metrics are visible.
- Backout:
  - Disable tracing with `observability.tracing.enabled=false` or unset
    `FLEXINFER_OTEL_ENABLED`.
  - Disable global flash-loader injection with
    `controller.runtime.flashLoader.enabled=false`; remove per-model cache
    overrides if they are the active source.
  - Revert chart/code commits and re-run Flux reconciliation if env rendering or
    controller behavior regresses.
  - If Helm rollouts stall because a homelab node is NotReady, force-delete stuck
    `flexinfer-system` pods on the dead node per the project runbook.

## Open Questions

- Do we need a dedicated Helm unit/golden test for controller runtime env
  rendering, or is `helm template` evidence sufficient for PR-2?
- Which MR should own user-facing documentation for tracing collector setup:
  this gfx1100 operations slice or a later observability slice?
- Should lifecycle latency metrics be added to `docs/specs/metrics.md` before
  this slice is called complete?
- Is v1alpha1 `ModelCache.spec.flashLoader` still the intended compatibility
  layer, or should v1alpha2 cache settings become the only documented path after
  the next CRD promotion?

## Sources

- `charts/flexinfer/templates/deployment.yaml`: controller env rendering for
  shm, flash-loader defaults, and tracing values.
- `charts/flexinfer/values.yaml`: default `controller.runtime` and
  `observability.tracing` values.
- `controllers/flash_loader.go`: flash-loader runtime config resolution,
  override precedence, and tmpfs cleanup behavior.
- `controllers/model_helpers.go`: phase transition handling and cold-start/swap
  metric observations.
- `pkg/metrics/exporter.go`: lifecycle histogram definitions and Prometheus
  registration.
- `pkg/observability/tracing.go`: opt-in OTLP/HTTP tracing initialization,
  propagation, and reconcile span helper.
- `cmd/flexinfer-manager/main.go`: manager startup tracing bootstrap.
- `cmd/flexinfer-proxy/main.go`: proxy startup tracing bootstrap.
