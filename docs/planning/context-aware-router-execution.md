# Context-Aware Router Execution Plan

> Last updated: 2026-02-19  
> Tracking issue: [#8 Context-aware router (prefix caching workloads)](https://gitlab.flexinfer.ai/services/flexinfer/-/issues/8)

This plan turns the roadmap item into PR-sized, testable slices for moving from
"prefix routing exists" to a full context-aware L7 routing path for Chat-with-Doc
workloads.

## Current Implementation Snapshot

Implemented today:
- [x] Session affinity routing via consistent hashing (`internal/routing/`)
- [x] Prefix-based routing opt-in (`flexinfer.ai/routing: prefix`)
- [x] Least-loaded routing opt-in (`flexinfer.ai/routing: least-loaded`)
- [x] Endpoint discovery and routing docs (`docs/user/routing.md`)

Gaps to full context-aware behavior:
- [x] Canonical cache-key policy expanded to model-scoped normalized multi-system + optional document context segments
- [x] Explicit request-level cache-key override contract
- [x] Routing safety bounds for noisy/unstable prefixes (length/charset validation + deterministic fallback)
- [x] End-to-end Chat-with-Doc validation (benchmark signals defined; E2E assertions + runbook guidance landed)

## Milestones

### CAR-1: Canonical Prefix Keying
- [x] Define canonical key extraction policy for chat payloads:
  - model id
  - normalized system prompt
  - optional document-context segment (bounded)
- [x] Implement deterministic key canonicalization helpers.
- [x] Add unit tests for canonicalization edge cases (empty/whitespace/order/noise).

Acceptance:
- Same semantic prompt/doc context yields identical cache keys.
- Small irrelevant payload changes do not fragment routing unnecessarily.

### CAR-2: Explicit Cache-Key Contract
- [x] Add opt-in request field/header for explicit cache key (for advanced clients).
- [x] Define precedence between explicit key, canonical key, and session affinity.
- [x] Document compatibility and safe defaults.

Acceptance:
- Explicit keys are respected only when routing mode allows it.
- Backward compatibility remains unchanged for existing clients.

### CAR-3: Safety + Fallback Controls
- [x] Add safeguards for over-fragmentation (max key length, normalization rules).
- [x] Add fallback behavior for malformed/oversized keys.
- [x] Expose operator knobs for keying strictness.

Acceptance:
- Invalid keys never break request routing.
- Proxy always falls back to deterministic safe routing mode.

### CAR-4: Observability + Benchmark Signals
- [x] Add metrics for key cardinality and route-hit distribution.
- [x] Add proxy logs for key source (session/prefix/explicit/fallback).
- [x] Define Chat-with-Doc benchmark scenario and expected signals.

Benchmark definition:
- `docs/user/routing.md#chat-with-doc-benchmark-scenario`
- Includes: three-phase workload (`explicit-stable`, `canonical-context`, `malformed-key`),
  PromQL signal queries, and expected route-stability/cardinality outcomes.

Acceptance:
- Operators can detect key explosion/hotspots from metrics.
- Benchmark report shows route stability and latency deltas.

### CAR-5: E2E Validation
- [x] Add E2E tests for context-aware routing behavior under multi-replica models (`e2e/routing_test.go::TestRoutingPrefixCanonicalDeterminism`).
- [x] Validate fallback behavior on endpoint churn and pod restart (`e2e/routing_test.go::TestRoutingFallbackDuringPodRestart`).
- [x] Capture runbook troubleshooting guidance (`docs/user/routing.md#troubleshooting-runbook-prefix-routing`).

Acceptance:
- E2E suite proves deterministic behavior for representative chat/doc traffic.
- Docs include concrete triage commands for routing mismatches.

## Validation Gate (Per Slice)

Targeted checks:

```bash
go test ./internal/routing/... -count=1
go test ./internal/proxy/... -count=1
go test ./cmd/flexinfer-proxy/... -count=1
```

Broader checks:

```bash
go test ./... -count=1
go vet ./...
```

Operational verification:

```bash
kubectl logs -n flexinfer-system deployment/flexinfer-proxy
kubectl get svc,endpoints -n flexinfer-system -l flexinfer.ai/model=<model>
```

## Done Criteria for Issue #8

Issue #8 can be closed when:
1. CAR-1 through CAR-3 are implemented with tests and docs.
2. CAR-4 observability signals are available and validated in benchmark runs.
3. CAR-5 E2E validation is green or explicitly split into owned follow-up issues.
