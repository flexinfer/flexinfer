# 2026-02-19 Enterprise Scope Plan: Gateway, RBAC, Devbox Executor

## Objective

Define a delivery-ready scope for the highest-impact enterprise platform work in `loom-core`, focused on:

1. Gateway hardening and remote operations
2. RBAC/policy maturity
3. Devbox executor reliability and multi-tenant controls

This plan is designed to convert roadmap intent into backlog slices that can be implemented and verified in sequence.

## Baseline (As Of 2026-02-19)

Current sources indicate these are already shipped:

- Streamable HTTP transport plus auth modes (`token`, `oidc`, `mtls`, `oauth2`)
- Core enterprise controls: RBAC, audit trail, cost tracking, OAuth 2.1 endpoints
- Devbox executor with Docker + K8s backends, async exec/poll, metrics, and HUD integration

Active gaps affecting enterprise readiness:

- Gateway hardening beyond baseline auth (policy enforcement depth, stronger observability, release guardrails)
- RBAC feature depth for complex org policies and change safety
- Devbox K8s backend decomposition and integration reliability coverage

## Program Scope

### In Scope

- Gateway control-plane and policy hardening in daemon proxy path
- RBAC enhancements and policy operations workflow
- Devbox executor architecture split, reliability hardening, and test maturity
- Cross-cutting observability and acceptance gates for the above

### Out Of Scope (For This Program Slice)

- HUD-only UX initiatives not required for enterprise control-plane behavior
- MCP catalog/discovery workstreams
- Broad refactors not touching gateway, RBAC, or devbox execution paths

## Workstream 1: Gateway Hardening

### Goals

- Make remote gateway operation auditable, observable, and policy-governed under production load.
- Reduce security and operational regressions in HTTP/remote tool-call paths.

### Current Baseline

- `internal/daemon/http_handler.go` exposes `/mcp` and OAuth metadata/token endpoints.
- `internal/daemon/auth.go` supports auth type selection and middleware wiring.
- `internal/daemon/callpipeline.go` provides staged execution with audit/cost/cache hooks.

### Scope Items

1. Gateway policy guardrails (pre-forward checks)
- Add request schema validation hooks before forwarding tool calls.
- Add response policy hooks (secret/PII scanning and deny-on-match modes).
- Emit structured deny reasons and policy rule IDs into audit stream.

2. Gateway observability depth
- Extend daemon tracing/metrics on route/connect/send/recv stage boundaries.
- Add per-target (`local` vs `hub`) SLO panels and alertable metrics.
- Complete Issue `#12` alignment for daemon path instrumentation.

3. Gateway release hardening
- Add remote transport soak/integration tests with auth permutations.
- Add explicit compatibility tests for session timeout/reaper behavior.
- Add a release checklist gate for non-localhost auth+TLS config validation.

### Acceptance Criteria

- Policy hooks can block disallowed requests/responses with deterministic error envelopes.
- Gateway emits stage-level metrics and trace spans for all tool calls.
- Remote auth matrix tests pass for `token`, `oidc` (mock), `mtls` (fixture), and `oauth2`.
- No regression in existing Streamable HTTP integration tests.

## Workstream 2: RBAC and Policy Maturity

### Goals

- Evolve RBAC from baseline allow/deny into production policy operations for larger teams.
- Keep policy changes safe, explainable, and testable.

### Current Baseline

- `internal/daemon/rbac.go` supports global deny, role bindings, deny-over-allow precedence, and first-match rate limits.
- `internal/daemon/rbac_test.go` has broad core behavior coverage.

### Scope Items

1. Policy model extensions
- Add optional scope dimensions (namespace/project/environment tags) to bindings/rules.
- Add explicit policy versioning and hash fingerprint output for audit events.
- Add dry-run mode for evaluating policy changes without enforcement.

2. Rate-limit and deny semantics
- Add burst + sustained limit model (not only minute bucket).
- Add reason codes (`RBAC_DENY_*`, `RATE_LIMIT_*`) to standardize client handling.
- Add policy simulation command for pre-deploy validation.

3. Policy operations workflow
- Add config validation for unreachable/overlapping rules.
- Add deterministic rule-order linting in CI.
- Add migration docs/examples for common enterprise role sets.

### Acceptance Criteria

- Dry-run and enforcement mode produce consistent decision traces.
- Rate-limit semantics validated by deterministic tests with virtual clock.
- Policy lint catches conflicts before daemon restart/deploy.
- Audit entries include stable rule identifiers and reason codes.

## Workstream 3: Devbox Executor Reliability

### Goals

- Make devbox execution predictable under high concurrency and K8s-backed runtime variation.
- Reduce blast radius of backend changes with cleaner architecture boundaries.

### Current Baseline

- `cmd/mcp-devbox/manager.go` owns project resolution, lifecycle coordination, async exec, and mount validation.
- `internal/devbox/backend/k8s.go` currently combines build/runtime/object/wait concerns.
- Open backlog already tracks decomposition (`#23`) and broader maturity/testing (`#4`, `#2`).

### Scope Items

1. K8s backend decomposition
- Split `k8s.go` into concern-based modules:
  - build path
  - runtime path
  - object/spec builders
  - watch/wait/log helpers
- Preserve current behavior via focused unit tests for each module.

2. Async executor reliability
- Add cancellation/timeout state transitions with explicit terminal statuses.
- Add stale exec registry cleanup and recovery behavior tests.
- Add race-focused tests around `ensureRunning` + async exec concurrency.

3. Integration coverage and production controls
- Add Docker and K8s integration suites for monorepo/out-of-tree workspace paths.
- Add resource and runtime failure injection tests (image pull failures, watch closure, exec timeout).
- Add explicit SLO metrics for build time, exec success rate, and timeout rate.

### Acceptance Criteria

- `internal/devbox/backend/k8s.go` decomposition completed with no net behavior regression.
- Async exec lifecycle remains consistent across timeout, backend error, and cancel paths.
- Integration tests cover both backends and monorepo path assumptions.
- Devbox metrics surface reliability KPIs usable in CI/release gates.

## Delivery Plan (Phased)

### Phase 0 (2-3 days): Planning Lock

1. Finalize acceptance criteria and issue mapping in this doc.
2. Confirm owners for each workstream.
3. Create/refresh missing issues and labels for each scope item.

### Phase 1 (1.5 weeks): Gateway First

1. Implement gateway policy hook scaffolding and reason codes.
2. Land daemon tracing/metrics expansions (`#12` alignment).
3. Add gateway auth/transport regression suite.

Exit gate:
- Gateway policy + observability acceptance criteria met.

#### Phase 1 Week-By-Week Task Board (Owner-Ready Tickets)

##### Week 1: Foundations

1. **P1-W1-T1: Gateway policy hook scaffolding**
- Issue: [#25](https://gitlab.flexinfer.ai/services/loom-core/-/issues/25)
- Suggested owner: Runtime Security / Gateway Platform
- Scope:
  - Add pre-forward request policy hook in call pipeline.
  - Add policy reason-code envelope shape.
  - Wire audit fields (`policy_rule_id`, `policy_reason_code`) in deny events.
- Dependencies:
  - None.
- Done when:
  - Hook path merged with unit tests for allow/deny behavior.

2. **P1-W1-T2: Daemon stage-level observability alignment**
- Issue: [#12](https://gitlab.flexinfer.ai/services/loom-core/-/issues/12)
- Suggested owner: Observability Platform
- Scope:
  - Instrument route/connect/send/recv stage boundaries.
  - Define metric naming and trace attributes for policy/gateway stages.
- Dependencies:
  - Coordinate reason-code dimensions with `#25`.
- Done when:
  - Stage-level metrics/spans visible in local verification and test fixtures.

3. **P1-W1-T3: Auth transport regression harness (baseline)**
- Issue: [#29](https://gitlab.flexinfer.ai/services/loom-core/-/issues/29)
- Suggested owner: Platform QA / Release Engineering
- Scope:
  - Add baseline matrix skeleton for `token`, `oidc` (mock), `mtls` (fixture), `oauth2`.
  - Validate initialize/session handshake and auth failure paths.
- Dependencies:
  - None (can start in parallel with `#25` and `#12`).
- Done when:
  - Matrix test harness committed with at least one green path per auth mode.

##### Week 2: Enforcement + CI Gating

1. **P1-W2-T1: Response policy enforcement and deterministic denies**
- Issue: [#25](https://gitlab.flexinfer.ai/services/loom-core/-/issues/25)
- Suggested owner: Runtime Security / Gateway Platform
- Scope:
  - Add post-response policy hook (deny/redact modes).
  - Ensure deterministic deny envelopes and audit parity with request path.
- Dependencies:
  - Requires merged scaffolding from P1-W1-T1.
- Done when:
  - Response hook tests cover secret/PII policy cases and all pass.

2. **P1-W2-T2: Gateway SLO views and alertable telemetry signals**
- Issue: [#12](https://gitlab.flexinfer.ai/services/loom-core/-/issues/12)
- Suggested owner: Observability Platform
- Scope:
  - Add per-target SLO metrics (`local` vs `hub`) and stage breakdown views.
  - Document expected thresholds/alerts for gateway degradation.
- Dependencies:
  - Requires stage metrics from P1-W1-T2.
- Done when:
  - SLO telemetry exported and documented with threshold guidance.

3. **P1-W2-T3: CI-gated enterprise smoke suite for gateway path**
- Issue: [#29](https://gitlab.flexinfer.ai/services/loom-core/-/issues/29)
- Suggested owner: Platform QA / Release Engineering
- Scope:
  - Add CI job for enterprise gateway smoke suite.
  - Include pass/fail diagnostics by stage (auth, policy, transport).
- Dependencies:
  - Depends on final reason-code behavior from `#25`.
- Done when:
  - CI gate is required and produces actionable failure output.

##### Phase 1 Capacity/Ownership Notes

- One primary owner per ticket, one reviewer from adjacent domain (security/observability/qa).
- Keep PRs under one ticket each; avoid mixing policy and telemetry codepaths in one MR.
- Ship Week 1 tickets before starting Week 2 enforcement/gates unless blocker exceptions are documented.

### Phase 2 (1 week): RBAC Maturity

1. Implement dry-run + simulation path.
2. Add scoped rule semantics and policy lint checks.
3. Ship docs/examples for role migration.

Exit gate:
- RBAC acceptance criteria met and policy CI lint enabled.

### Phase 3 (1.5 weeks): Devbox Executor Hardening

1. Decompose K8s backend (`#23`) and land unit tests.
2. Complete async executor reliability improvements.
3. Expand integration coverage (`#4` and `#2` alignment).

Exit gate:
- Devbox acceptance criteria met with both backends green.

### Phase 4 (3-4 days): Program Verification

1. Run full hooks/tests/lint.
2. Run enterprise smoke runbook (gateway auth + RBAC policy + devbox exec).
3. Update roadmap/docs/status artifacts.

## Backlog Mapping

Existing tracked issues:

- `#12` OTel trace export from daemon (gateway observability)
- `#23` Decompose devbox K8s backend responsibilities
- `#4` Devbox maturity
- `#2` Coverage growth and integration quality

New concrete enterprise slice issues created on 2026-02-19:

1. `#25` Gateway policy hooks for request/response enforcement
2. `#26` RBAC dry-run and simulation tooling
3. `#27` RBAC policy lint and conflict detection in CI
4. `#28` Devbox async lifecycle reliability and race harness
5. `#29` End-to-end smoke suite for gateway + RBAC + devbox

Roadmap-linked prior issue:

- `#15` Security hardening layer (closed; superseded by concrete slices above)

## Risks and Mitigations

1. Risk: Policy hooks add latency to tool-call path.
- Mitigation: stage-level latency budget checks and fast-fail profiling.

2. Risk: RBAC semantics changes break existing role behavior.
- Mitigation: compatibility mode and migration tests against current configs.

3. Risk: Devbox decomposition introduces runtime regressions.
- Mitigation: golden tests for pod specs and backend behavior snapshots.

4. Risk: Test expansion increases CI cycle time.
- Mitigation: split targeted suites and nightly long-run integration jobs.

## Definition Of Done (Program)

1. All three workstreams pass their acceptance criteria.
2. CI includes mandatory tests/lint for new policy and executor paths.
3. Docs include operator runbooks and config examples for production rollout.
4. Roadmap and implementation status docs reflect shipped outcomes and residual gaps.
