# Changelog

All notable changes to this project will be documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/).

## [Unreleased]

### Fixed

- `config.enableSleepMode` now also sets `VLLM_SERVER_DEV_MODE=1` on the runtime container (`backend/vllm.go`). vLLM registers `/sleep`, `/wake_up` and `/is_sleeping` only inside its dev API router, so the `--enable-sleep-mode` passthrough was previously inert — every sleep/wake call returned 404 (verified against vLLM 0.23.0).

### Changed

- Added a SIGTERM-aware graceful HTTP shutdown contract for `cmd/flexinfer-proxy/main.go`, `internal/proxy/proxy.go`, `internal/proxy/metrics.go`, and `internal/proxy/proxy_test.go` so proxy rollouts drain in-flight requests with bounded observability.
