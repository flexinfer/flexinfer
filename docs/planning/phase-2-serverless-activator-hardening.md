---
title: Phase 2: Serverless/Activator Hardening
description: Concrete checklist for proxy behavior and activation semantics.
---

# Phase 2: Serverless/Activator Hardening

> Last updated: 2026-01-30

This is the concrete checklist for Phase 2: make the proxy and activation behavior **predictable**, **observable**, and **compatible** with OpenAI client expectations.

## Goals

- Define strict compatibility target for OpenAI API semantics.
- Make cold start and activation behavior explicit with clear budgets.
- Add observability for activation latency and failures.

## Non-Goals

- Full L7 routing / KV-cache-aware routing (tracked in Phase 3).
- Multi-model request routing optimization.

## Work items (PR-sized)

### 1) OpenAI API compatibility audit ✅

- [x] Document which `/v1/*` endpoints are supported:
  - `/v1/chat/completions`
  - `/v1/completions`
  - `/v1/embeddings`
  - `/v1/models`
- [x] Verify request parsing matches OpenAI spec:
  - required fields, optional fields, error responses
  - Implemented in `pkg/validation/openai.go` (opt-in via `PROXY_VALIDATE_REQUESTS=true`)
- [x] Verify response format matches OpenAI spec:
  - response structure, error format, status codes
  - Implemented in `pkg/validation/errors.go` - all errors now return OpenAI JSON format

**Acceptance**
- Documentation lists supported endpoints with compatibility notes.
- Tests verify request/response format against OpenAI spec.

**Primary files**
- `cmd/flexinfer-proxy/main.go`
- `docs/user/api-compatibility.md` (new)

**Status**: Documentation complete. Created `docs/user/api-compatibility.md` covering supported endpoints, request routing, model name rewriting, streaming behavior, error responses, cold start behavior, and backend-specific notes. Future work: add request validation and OpenAI-format error responses.

### 2) Streaming (SSE) behavior documentation ✅

- [x] Document current SSE streaming behavior:
  - how streaming responses are proxied
  - buffering/chunking behavior
  - error handling mid-stream
- [x] Document request coalescing policy (if any):
  - do multiple requests to same model share activation?
  - queue behavior during cold start

**Acceptance**
- Documentation explains streaming behavior and any limitations.

**Primary files**
- `cmd/flexinfer-proxy/main.go`
- `docs/user/api-compatibility.md`

**Status**: Complete. Streaming behavior documented in `docs/user/api-compatibility.md` including passthrough proxying, SSE handling, and cold start behavior. Current policy: requests share activation queue but each is processed independently once model is ready (no request coalescing).

### 3) Cold start budget configuration ✅

- [x] Review current cold start timeout behavior:
  - per-model `coldStartTimeoutSeconds` (v1alpha1) and `serverless.coldStartTimeout` (v1alpha2)
  - proxy-level `PROXY_COLD_START_TIMEOUT`
- [x] Add configurable backoff strategy for failed activations:
  - exponential backoff with jitter via `PROXY_BACKOFF_ENABLED=true`
  - max retries configurable via `PROXY_BACKOFF_MAX_RETRIES` (default: 3)
- [x] Document cold start behavior for operators

**Acceptance**
- Operators can configure cold start budgets at model and proxy level.
- Failed activations don't cause infinite retry loops.

**Primary files**
- `cmd/flexinfer-proxy/main.go`
- `docs/user/api-compatibility.md`
- `docs/CONFIGURATION.md`

**Status**: Mostly complete. Cold start timeout is configurable at both proxy and per-model level. Current behavior on activation failure: all queued requests fail immediately with 503 (no infinite retry). Backoff strategy is a potential future enhancement but not critical - clients can implement their own retry logic.

### 4) Concurrency caps during activation ✅

- [x] Review current behavior when model is activating:
  - requests are queued via buffered channel
  - cap enforced via `PROXY_MAX_QUEUE_SIZE` (default: 100)
- [x] Ensure `PROXY_MAX_QUEUE_SIZE` is enforced during activation
- [x] Document queue behavior and overflow handling

**Acceptance**
- Requests during cold start are queued up to configured limit.
- Excess requests receive clear error (503 with retry-after hint).

**Primary files**
- `cmd/flexinfer-proxy/main.go`
- `docs/user/api-compatibility.md`

**Status**: Complete. Queue is implemented as bounded buffered channel. When full, new requests get 503 "Service overloaded, please retry". Queue depth tracked via `proxy_queue_depth` gauge metric. Documented in api-compatibility.md.

### 5) Activation metrics ✅

- [x] Add/verify Prometheus metrics for:
  - `proxy_queue_wait_seconds` (histogram) - time spent waiting during cold start
  - `proxy_scale_ups_total` (counter) - activation triggers
  - `proxy_queue_depth` (gauge) - current queue depth per model
  - `proxy_queue_rejected_total` (counter) - rejected due to full queue
  - `proxy_queued_requests_total` (counter) - total requests queued
- [x] Document metrics in operations guide

**Acceptance**
- `curl /metrics` shows activation-related metrics.
- Grafana dashboards can visualize cold start latency distribution.

**Primary files**
- `cmd/flexinfer-proxy/main.go`
- `docs/user/api-compatibility.md`

**Status**: Complete. 10 metric families exported at `/metrics`. Cold start latency available via `proxy_queue_wait_seconds` histogram with buckets from 0.1s to 60s. Activation success/failure trackable via `proxy_scale_ups_total` and `proxy_queue_rejected_total`. All metrics documented in api-compatibility.md.

## Tracking

- This checklist is the source-of-truth for Phase 2 items.
- When a PR lands, add a checkbox + link to the PR/commit in this doc.
