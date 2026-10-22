# Changelog

All notable changes to this project will be documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/).

## [Unreleased]

### Added

- GPU health sentinel in the node agent (`agents/agent/health.go`): every probe cycle first inspects each discrete AMD card through sysfs — `current_link_speed=Unknown` on an awake card (`pcie-link-down`), `EIO`/`EREMOTEIO` on SMU-backed attributes (`device-io-error`), a card that vanished (`gpu-missing`) — and records vendor tools that hang past the new `--probe-timeout` (default 15 s, `probe-timeout`) or an nvidia-smi "fallen off the bus" (`fallen-off-bus`). The verdict is published as the node label `flexinfer.ai/gpu.health` (`ok`/`degraded`) with `flexinfer.ai/gpu.health-reason` / `-since` annotations and the metrics `flexinfer_gpu_healthy` and `flexinfer_gpu_health_degraded_total`; recovery needs three clean cycles. The scheduler extender's `Filter` rejects degraded nodes, the controller's `detectGPU` passes over them and reports `Schedulable=False/GPUDegraded` (Deployment parked, `GPUDegraded` event) when a Model's only matching node is degraded, a new Node watch re-reconciles GPU Models on each label flip, and the chart gains the critical `FlexInferGPUDegraded` alert (`alerting.thresholds.gpuDegradedFor`). Motivation: the 2026-10 RX 7900 XTX PCIe/SMU drops (six in a fortnight) went unnoticed by the control plane each time — the node stayed Ready, every activation ran its 25-minute cold-start timeout, and the lane was parked by hand with an offline hostname selector. `--gpu-health-sentinel=false` disables the verdict. Runbook: `docs/agents/operations.md#gpu-health-sentinel`.

### Fixed

- The node agent no longer hangs when a vendor tool does: every `rocm-smi` / `rocminfo` / `nvidia-smi` call now runs under a per-command deadline and `exec.Cmd.WaitDelay`, so a tool stuck in uninterruptible sleep on a wedged GPU is abandoned after the probe timeout instead of freezing the agent's label/annotation loop for the rest of the boot (observed 2026-10-21/22: the agent's `rocminfo` in state D, `gpu-free-memory` frozen at its pre-wedge value).

- Parked (scaled-to-zero, Idle) Models now converge their Deployment's managed annotations (`flexinfer.ai/service-labels`, `litellm.flexinfer.ai/aliases`, and the rest of the litellm advertisement) on spec edits without an activation (`controllers/model_controller.go`, `controllers/model_deployment.go`). The steady-idle fast path returned before `ensureDeployment`, so a `spec.serviceLabels` change re-stamped only the Service — litellm's manager, which gathers aliases from Deployment annotations, kept advertising removed labels until a manual `kubectl annotate` (observed 2026-08-30 removing the borrowed `qwen35-9b-ablit-rp` label, MR !1009). The sync is metadata-only: no template render, no replica change, and no write when already converged.

- The proxy now answers 404 `model_not_found` for a model id that cannot be a Kubernetes resource name (e.g. a LiteLLM gateway alias such as `or/kimi-k3` that no Model advertises). client-go rejects such names with a plain error before dialing the API server, which `internal/proxy/proxy.go` previously reported as 500 "Internal error fetching model". Genuine API-server failures (timeouts, 5xx) still return 500. Plain not-found responses (no Model, no ModelDeployment) are now counted under `flexinfer_proxy_requests_total{status="not_found"}` instead of `status="error"`, so unknown-model probes no longer feed the `FlexInferProxyHighErrorRate` alert.

- `config.enableSleepMode` now also sets `VLLM_SERVER_DEV_MODE=1` on the runtime container (`backend/vllm.go`). vLLM registers `/sleep`, `/wake_up` and `/is_sleeping` only inside its dev API router, so the `--enable-sleep-mode` passthrough was previously inert — every sleep/wake call returned 404 (verified against vLLM 0.23.0).

### Changed

- Added a SIGTERM-aware graceful HTTP shutdown contract for `cmd/flexinfer-proxy/main.go`, `internal/proxy/proxy.go`, `internal/proxy/metrics.go`, and `internal/proxy/proxy_test.go` so proxy rollouts drain in-flight requests with bounded observability.
