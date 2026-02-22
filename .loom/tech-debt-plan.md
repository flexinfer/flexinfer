# Technical Debt Remediation Plan

## Summary

- Planning date: 2026-02-20
- Scope: stale CLI -> proxy -> `loomd` -> MCP session behavior, hot rebuild/reload survivability, stale socket prevention, local CPU/memory efficiency in loom-mode.
- Total items considered: 7

## Scoring Snapshot

- Ranking artifact: `.loom/tech-debt-priority.md`
- Scoring model: impact 35%, risk reduction 30%, drag reduction 20%, effort inverse 15%
- Top-ranked items: `DEBT-002` (94), `DEBT-001` (93), `DEBT-007` (84)

## Wave 1 (Immediate)

- Goal: Eliminate hung-agent failure modes and make reconnect deterministic under daemon/proxy churn.
- Items:
  - `DEBT-002` Enforce end-to-end RPC deadlines.
  - `DEBT-001` Harden proxy transport reset and reconnect classification.
  - `DEBT-007` Add restart/reconnect chaos tests for confidence gates.
- Acceptance criteria:
  - Proxy and CLI never block indefinitely on daemon send/recv; timeout errors are explicit and recoverable.
  - Proxy always clears poisoned daemon transports after init/send/recv failures and reconnects on next request.
  - New integration suite covers: daemon restart during active proxy session, stale socket path, and MCP server transport drop.
  - `go test ./cmd/loom ./internal/daemon ./internal/integration -run 'ProxyDaemon|CallPipeline|Daemon'` passes.
- Risks/mitigations:
  - Risk: timeout defaults may break long-running tools. Mitigation: add method-level timeout tiers and config overrides.
  - Risk: reconnect loops could amplify CPU if daemon is down. Mitigation: exponential backoff + jitter + cap.
- Not in this wave:
  - Full session lease protocol (`DEBT-004`).
  - Health monitor efficiency redesign (`DEBT-005`).

## Wave 2 (Near-Term)

- Goal: Stabilize developer hot-upgrade lifecycle and lower local runtime cost.
- Items:
  - `DEBT-003` Retryable autostart/reconnection policy (replace one-shot `sync.Once` behavior).
  - `DEBT-006` Make `dev-upgrade` restart guard session-aware and strengthen smoke checks.
  - `DEBT-005` Reduce health monitor process churn in loom-mode.
- Acceptance criteria:
  - Proxy reconnect/autostart survives daemon restarts without manual user intervention.
  - `scripts/dev/upgrade_local.sh` blocks/retries based on explicit proxy-session drain state, not just pool active connections.
  - Upgrade smoke validates `initialize`, `tools/list`, and a representative `tools/call` across a daemon restart.
  - Health monitor no longer forks per-server process every interval for local checks; CPU and RSS baseline improves versus current baseline capture.
- Risks/mitigations:
  - Risk: session-aware drain adds state complexity. Mitigation: keep read-only status endpoint first, then enforce.
  - Risk: health probe changes can hide server failures. Mitigation: keep fallback deep probe path on configurable interval.
- Not in this wave:
  - Cross-transport unified lease/epoch architecture finalization (`DEBT-004`).

## Wave 3 (Strategic)

- Goal: Enterprise-grade session lifecycle with seamless hot reload/restart semantics.
- Items:
  - `DEBT-004` Unified local+HTTP session lease/epoch management.
  - Complete shared daemon client abstraction reuse across proxy/CLI/HUD where practical.
- Acceptance criteria:
  - Local proxy sessions receive lease metadata (`session_id`, `daemon_epoch`, `last_seen`) and support resume after daemon restart.
  - Daemon supports graceful drain states for reload/restart: `accepting=false`, in-flight completion window, hard cutoff.
  - Proxy transparently re-initializes session on epoch mismatch without requiring CLI/IDE restart.
  - Backward compatibility preserved for existing `loom proxy` protocol behavior and generated client configs.
- Risks/mitigations:
  - Risk: protocol changes could break older clients. Mitigation: additive fields + capability negotiation + compatibility tests.
  - Risk: more session bookkeeping could increase memory footprint. Mitigation: bounded LRU session table + idle reap + lightweight structs.
- Not in this wave:
  - Non-session-related feature roadmap work (catalog UX, Fleet orchestration UX, etc.).

## Backlog Conversion

| Debt ID | Backlog ID | Owner | Target Sprint/Milestone | Status |
|---|---|---|---|---|
| DEBT-002 | TD-SESSION-01 | daemon/proxy runtime | Sprint 1 | done |
| DEBT-001 | TD-SESSION-02 | daemon/proxy runtime | Sprint 1 | done |
| DEBT-007 | TD-SESSION-03 | qa/runtime | Sprint 1 | done |
| DEBT-003 | TD-SESSION-04 | proxy lifecycle | Sprint 2 | done |
| DEBT-006 | TD-SESSION-05 | dev lifecycle | Sprint 2 | done |
| DEBT-005 | TD-PERF-01 | daemon runtime | Sprint 2 | done |
| DEBT-004 | TD-SESSION-06 | architecture/runtime | Sprint 3 | planned |

## Backlog-Ready Slices

### TD-SESSION-01 (DEBT-002)

- Problem statement: unbounded send/recv contexts in proxy/CLI can deadlock agent sessions when daemon or downstream server stalls.
- Acceptance criteria:
  - Configurable call timeout envelope (default <= 30s for control-plane calls, <= 60s for tool calls).
  - Timeout errors include phase (`dial`, `send`, `recv`) and recoverability hint.
- Test/verification strategy:
  - Unit tests for timeout paths in proxy + CLI call helper.
  - Integration test with intentionally blocking mock daemon transport.
- Rollback/safety notes:
  - Timeout config guarded with defaults and env override to quickly relax limits if a regression appears.

### TD-SESSION-02 (DEBT-001)

- Problem statement: proxy can retain a bad daemon transport after failed initialize, and reconnect reset logic misses non-EOF transport failures.
- Acceptance criteria:
  - Any initialize/send/recv failure resets transport state atomically.
  - Failure classification uses structured checks (`errors.Is`, `net.Error`) instead of brittle string matching only.
- Test/verification strategy:
  - Table tests for failure classes (`EOF`, `EPIPE`, `ECONNRESET`, closed network connection, timeout).
  - Regression tests for init-fail then successful reconnect.
- Rollback/safety notes:
  - Keep previous fallback classification as secondary path during rollout.

### TD-SESSION-03 (DEBT-007)

- Problem statement: no automated confidence for restart/reload chaos cases.
- Acceptance criteria:
  - New integration suite exercises restart during active proxy session and stale socket recovery.
  - Tests run in CI profile (or nightly) with deterministic pass/fail criteria.
- Test/verification strategy:
  - Extend `internal/integration/proxy_daemon_test.go` with restart scenario harness.
  - Add daemon lifecycle chaos fixtures under `internal/daemon/*_test.go`.
- Rollback/safety notes:
  - Mark as optional/nightly first if CI duration impact exceeds threshold.

### TD-SESSION-04 (DEBT-003)

- Problem statement: one-shot autostart leaves proxy stranded after subsequent daemon outages.
- Acceptance criteria:
  - Replace `sync.Once` autostart gate with bounded retry policy + jitter/backoff.
  - Proxy auto-recovers from daemon restarts without external commands.
- Test/verification strategy:
  - Simulated daemon-down -> daemon-up reconnection test.
  - Ensure no excessive spawn attempts when daemon is permanently unavailable.
- Rollback/safety notes:
  - Backoff max cap and attempt cap prevent process churn storms.

### TD-SESSION-05 (DEBT-006)

- Problem statement: `dev-upgrade` restart decision uses pool active connection count that does not represent user proxy session lifecycle; smoke test only covers initialize.
- Acceptance criteria:
  - Daemon exposes explicit proxy session count/drain readiness endpoint.
  - Upgrade script uses this endpoint and verifies restart resilience with an actual tool call.
- Test/verification strategy:
  - Script unit tests for parsing fallback behaviors.
  - End-to-end dev-upgrade dry-run in CI/dev sandbox.
- Rollback/safety notes:
  - Keep current status-based guard as fallback while session endpoint rolls out.

### TD-PERF-01 (DEBT-005)

- Problem statement: health checks spawn short-lived processes for each server every interval, creating avoidable local CPU/memory pressure.
- Acceptance criteria:
  - Health check mode reuses existing pools/running processes by default.
  - Introduce adaptive/degraded deep probe schedule for expensive checks.
  - Baseline comparison shows reduced CPU and process churn in loom-mode.
- Test/verification strategy:
  - Add benchmark/metrics capture before and after.
  - Verify unhealthy server detection quality is maintained.
- Rollback/safety notes:
  - Feature flag to revert to deep-probe mode if false negatives appear.

### TD-SESSION-06 (DEBT-004)

- Problem statement: local stdio proxy lacks enterprise-grade session semantics (lease/epoch/resume/drain), causing brittle behavior around daemon restart and hot reload.
- Acceptance criteria:
  - Session lease API for local proxy clients (ID, epoch, expiry, state).
  - Graceful daemon drain protocol for reload/restart with in-flight completion window.
  - Backward-compatible proxy behavior for old clients.
- Test/verification strategy:
  - Contract tests for lease creation, heartbeat, epoch mismatch, and resume paths.
  - Chaos tests for daemon restarts under concurrent tool traffic.
- Rollback/safety notes:
  - Additive protocol fields only; legacy flow preserved behind capability checks.

## Deferred / Not In Scope

- Broad MCP server refactors unrelated to session/restart path.
- HUD UI modernization tasks outside runtime reliability.
- New external transport features beyond current Streamable HTTP scope.
