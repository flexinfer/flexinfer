---
title: Phase 3: Routing & Performance
description: Concrete checklist for KV-cache-aware routing and load balancing.
---

# Phase 3: Routing & Performance

> Last updated: 2026-01-30

This is the concrete checklist for Phase 3: improve request routing for **cache locality** and **load distribution** across multi-replica models.

## Goals

- Enable session affinity for better KV-cache hit rates.
- Provide opt-in prefix-based routing for shared prompt scenarios.
- Support load-balanced routing across multi-replica models.

## Non-Goals

- Full distributed KV-cache (requires backend changes).
- Automatic cache migration between pods.

## Background

### Why KV-cache-aware routing matters

LLM inference backends maintain a KV-cache for each conversation. When requests from the same conversation hit different pods:
- KV-cache must be recomputed from scratch
- Latency increases significantly (especially for long contexts)
- GPU memory is wasted on duplicate caches

### Current state

The proxy currently uses Kubernetes Service load balancing (round-robin by default). This provides no session affinity or cache awareness.

## Work items (PR-sized)

### 1) Session affinity via consistent hashing ✅

- [x] Add session ID extraction from requests:
  - `X-Session-ID` header (explicit)
  - `X-Conversation-ID` header (explicit)
  - `session_id` field in body
  - Hash of `messages` content (implicit, for chat)
- [x] Implement consistent hash ring for pod selection
- [x] Maintain pod membership via endpoint watch
  - Only models with `flexinfer.ai/routing` annotation get direct pod routing
  - Others use Kubernetes Service DNS for load balancing
- [x] Handle pod additions/removals gracefully (minimal rehashing)

**Acceptance**
- Requests with same session ID consistently route to same pod.
- Pod failures cause minimal disruption to other sessions.

**Primary files**
- `internal/routing/hashring.go` (new)
- `internal/routing/router.go` (new)

**Status**: Core implementation complete. Created `internal/routing` package with consistent hash ring and session ID extraction. Tests pass. Integration with proxy endpoint watching is the next step.

### 2) Prefix-based routing (opt-in) ✅

- [x] Add support for system prompt hashing:
  - Extract `messages[0]` if role is "system"
  - Hash prefix to determine target pod
- [x] Make prefix routing opt-in via model annotation:
  - `flexinfer.ai/routing: prefix`
- [x] Document when prefix routing is beneficial:
  - Many requests share same system prompt
  - System prompt is long (saves significant recomputation)

**Acceptance**
- Models with `routing: prefix` annotation route based on system prompt.
- Default behavior remains unchanged (backward compatible).

**Primary files**
- `internal/routing/router.go`
- `docs/user/routing.md` (new)

**Status**: Core implementation complete. `ExtractPrefix` function extracts system prompts and hashes them. Documented in `docs/user/routing.md`.

### 3) Endpoint discovery for multi-replica models ✅

- [x] Watch Endpoints/EndpointSlices for model Services
- [x] Maintain in-memory pod list per model
- [x] Update hash ring when endpoints change
- [x] Add metrics for endpoint churn:
  - `proxy_endpoint_changes_total{model, change_type}` (counter)
  - `proxy_endpoint_count{model}` (gauge)
  - `proxy_endpoint_refresh_seconds` (histogram)

**Acceptance**
- Proxy discovers all ready pods for multi-replica models.
- Endpoint changes are reflected within seconds.

**Primary files**
- `cmd/flexinfer-proxy/main.go`

**Status**: Complete. Added `watchEndpoints` goroutine that refreshes every 10 seconds. The `refreshEndpoints` function lists Services with `flexinfer.ai/model` selector, fetches their Endpoints, and updates the router's hash ring with ready pod addresses.

### 4) Least-loaded routing (opt-in) ✅

- [x] Define "load" metric source:
  - Option B selected: Maintain local connection count per pod
- [x] Implement weighted selection based on load
- [x] Make least-loaded routing opt-in via annotation:
  - `flexinfer.ai/routing: least-loaded`
- [x] Handle metric staleness gracefully (falls back to first available node)

**Acceptance**
- Models with `routing: least-loaded` distribute requests by current load.
- Fallback to round-robin if metrics unavailable.

**Primary files**
- `cmd/flexinfer-proxy/main.go`
- `internal/routing/router.go`

**Status**: Complete. Added `selectLeastLoaded` function to router, per-pod connection tracking to proxy via `podConnectionCount` map, and `RouteWithLoad` function that accepts a load function. Tests verify correct selection of least-loaded pod.

### 5) Routing documentation ✅

- [x] Create `docs/user/routing.md` covering:
  - Default behavior (Kubernetes Service round-robin)
  - Session affinity configuration
  - Prefix-based routing use cases
  - Least-loaded routing configuration (marked as planned)
  - Metrics for monitoring routing effectiveness

**Acceptance**
- Operators can choose appropriate routing strategy for their workload.

**Primary files**
- `docs/user/routing.md` (new)

**Status**: Complete. Created comprehensive routing documentation explaining all strategies, when to use each, and how consistent hashing works.

## Implementation notes

### Consistent hashing library

Consider using `github.com/buraksezer/consistent` or similar for the hash ring implementation. Key requirements:
- Bounded load (prevents hot spots)
- Smooth rebalancing on membership changes
- Configurable replication factor

### Endpoint watching

Use `controller-runtime` informer or direct `client-go` watch on EndpointSlices:
```go
informer.AddEventHandler(cache.ResourceEventHandlerFuncs{
    AddFunc:    p.onEndpointAdd,
    UpdateFunc: p.onEndpointUpdate,
    DeleteFunc: p.onEndpointDelete,
})
```

### Graceful degradation

All routing enhancements should degrade gracefully:
- If hash ring is empty → fall back to Service DNS
- If metrics unavailable → fall back to round-robin
- If session ID missing → use random selection

## Tracking

- This checklist is the source-of-truth for Phase 3 items.
- When a PR lands, add a checkbox + link to the PR/commit in this doc.
