# Changelog

All notable changes to this project will be documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/).

## [Unreleased]

### Changed

- Added a SIGTERM-aware graceful HTTP shutdown contract for `cmd/flexinfer-proxy/main.go`, `internal/proxy/proxy.go`, `internal/proxy/metrics.go`, and `internal/proxy/proxy_test.go` so proxy rollouts drain in-flight requests with bounded observability.
