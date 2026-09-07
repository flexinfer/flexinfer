# Changelog

All notable changes to this project will be documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/).

## [Unreleased]

### Fixed

- Parked (scaled-to-zero, Idle) Models now converge their Deployment's managed annotations (`flexinfer.ai/service-labels`, `litellm.flexinfer.ai/aliases`, and the rest of the litellm advertisement) on spec edits without an activation (`controllers/model_controller.go`, `controllers/model_deployment.go`). The steady-idle fast path returned before `ensureDeployment`, so a `spec.serviceLabels` change re-stamped only the Service — litellm's manager, which gathers aliases from Deployment annotations, kept advertising removed labels until a manual `kubectl annotate` (observed 2026-08-30 removing the borrowed `qwen35-9b-ablit-rp` label, MR !1009). The sync is metadata-only: no template render, no replica change, and no write when already converged.

- The proxy now answers 404 `model_not_found` for a model id that cannot be a Kubernetes resource name (e.g. a LiteLLM gateway alias such as `or/kimi-k3` that no Model advertises). client-go rejects such names with a plain error before dialing the API server, which `internal/proxy/proxy.go` previously reported as 500 "Internal error fetching model". Genuine API-server failures (timeouts, 5xx) still return 500. Plain not-found responses (no Model, no ModelDeployment) are now counted under `flexinfer_proxy_requests_total{status="not_found"}` instead of `status="error"`, so unknown-model probes no longer feed the `FlexInferProxyHighErrorRate` alert.

- `config.enableSleepMode` now also sets `VLLM_SERVER_DEV_MODE=1` on the runtime container (`backend/vllm.go`). vLLM registers `/sleep`, `/wake_up` and `/is_sleeping` only inside its dev API router, so the `--enable-sleep-mode` passthrough was previously inert — every sleep/wake call returned 404 (verified against vLLM 0.23.0).

### Changed

- Added a SIGTERM-aware graceful HTTP shutdown contract for `cmd/flexinfer-proxy/main.go`, `internal/proxy/proxy.go`, `internal/proxy/metrics.go`, and `internal/proxy/proxy_test.go` so proxy rollouts drain in-flight requests with bounded observability.
