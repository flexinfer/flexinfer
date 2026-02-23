# Technical Debt Remediation Plan

## Summary

- Planning date: 2026-02-23
- Previous cycle: 2026-02-20 (7 items; 6 completed, 1 carried forward)
- Scope: session lifecycle, proxy resilience, daemon maintainability, library transport correctness
- Total active items: 2
- Scoring model: impact 35%, risk reduction 30%, drag reduction 20%, effort inverse 15%

## Completed Since Last Cycle

| ID | Summary | Closed By |
|---|---|---|
| DEBT-001 | Proxy transport reset hardened | Structured error types + `errors.Is`/`errors.As` classification |
| DEBT-002 | End-to-end RPC deadlines + response ID validation | `ddd4e85` (ID mismatch detection, lock ordering fix) |
| DEBT-003 | Bounded autostart replaces `sync.Once` | `proxy.go:114-132` (5 attempts, 10s cooldown) |
| DEBT-005 | Health monitor process churn reduced | Pool-reusing passive checks |
| DEBT-006 | Dev-upgrade safety gates strengthened | Session-aware drain + smoke checks |
| DEBT-007 | Restart/reconnect chaos tests added | `internal/integration/proxy_daemon_test.go` |

Additional fixes this cycle (not in previous inventory):
- `454ecf8` - Removed dead `agent_handoff_list` tool causing -32602 HUD errors
- `352d8a2` (mcp-go) - StdioTransport Close/Recv lifecycle fix (was no-op + blocking)
- Killed 88 orphaned proxy processes manually

## Scoring Snapshot

- Ranking artifact: `.loom/tech-debt-priority.md`
- Top-ranked: `DEBT-004` (80), `DEBT-010` (61), `DEBT-011` (59), `DEBT-008` (58), `DEBT-009` (52)

## Wave 1 (Quick Wins + High Leverage)

- Goal: Eliminate remaining proxy orphan risk and low-effort credential cleanup.
- Items:
  - `DEBT-011` (score 59) Add signal handling and idle timeout to proxy process.
  - `DEBT-010` (score 61) Migrate GCP credentials to ADC.
- Acceptance criteria:
  - Proxy handles SIGTERM/SIGINT with graceful shutdown (close daemon transport, close session, exit).
  - Proxy self-terminates after configurable idle period (default 30m, env override).
  - GCP server uses ADC by default; explicit credentials file as optional override.
  - `go test ./cmd/loom -run Proxy` passes; manual test: kill parent process, verify proxy exits.
- Risks/mitigations:
  - Risk: idle timeout could kill proxies during legitimate long pauses. Mitigation: configurable via `LOOM_PROXY_IDLE_TIMEOUT`, default conservative (30m), reset on any message.
- Not in this wave:
  - Session lease protocol (`DEBT-004`).
  - Daemon file split (`DEBT-008`).

## Wave 2 (Medium Effort / Maintainability) — COMPLETED

- Goal: Improve daemon codebase maintainability and prepare for session architecture.
- Items:
  - `DEBT-008` (score 58) Split daemon.go monolith into focused files. **Done** (`1fc56e0`).
- Result:
  - `daemon.go` reduced from 2535 to 919 lines (core lifecycle, networking).
  - New files: `daemon_dispatch.go` (618), `daemon_toolcache.go` (731), `daemon_call.go` (130), `daemon_loops.go` (182).
  - No behavior changes; all existing tests pass unchanged; golangci-lint clean.

## Wave 3 (Strategic) — COMPLETED

- Goal: Enterprise-grade session lifecycle and transport library correctness.
- Items:
  - `DEBT-004` (score 80) Unified local+HTTP session lease/epoch management. **Done** (already implemented: `session.go`, `session_handlers.go`, `proxy.go`).
  - `DEBT-009` (score 52) Non-destructive context cancellation for StdioTransport. **Done** (background reader goroutine in `transport.go`).
- Result:
  - DEBT-004: All acceptance criteria met by existing code — `ProxySession` struct with lease/epoch/state, full session RPC handlers, epoch validation on heartbeat, graceful drain protocol, `prior_session_id` support.
  - DEBT-009: Background reader goroutine decouples blocking I/O from context cancellation. `useContentLength` converted to `atomic.Bool` to fix data race. Transport remains usable after context timeout. New test `TestStdioTransportRecv_ReusableAfterCancel` verifies.

## Backlog Conversion

| Debt ID | Backlog ID | Owner | Wave | Status |
|---|---|---|---|---|
| DEBT-011 | TD-PROXY-01 | proxy lifecycle | Wave 1 | done (`b6ca3da`) |
| DEBT-010 | TD-GCP-01 | mcp-gcp | Wave 1 | done (`812c638`) |
| DEBT-008 | TD-MAINT-01 | daemon runtime | Wave 2 | done (`1fc56e0`) |
| DEBT-004 | TD-SESSION-06 | architecture/runtime | Wave 3 | done (already implemented) |
| DEBT-009 | TD-TRANSPORT-01 | mcp-go library | Wave 3 | done (background reader goroutine) |

## Backlog-Ready Slices

### TD-PROXY-01 (DEBT-011)

- Problem statement: proxy process has no signal handling and no idle self-termination. Before the StdioTransport fix (`352d8a2`), orphaned proxies accumulated indefinitely (88 killed manually). Post-fix, stdin EOF propagates correctly, but no graceful signal handling or idle watchdog exists.
- Acceptance criteria:
  - `signal.Notify` for SIGTERM/SIGINT triggers graceful shutdown: close daemon transport, end proxy session, exit 0.
  - Configurable idle timeout (`LOOM_PROXY_IDLE_TIMEOUT`, default 30m) terminates proxy after no messages received.
  - Idle timer resets on every inbound message from stdin.
- Test/verification strategy:
  - Unit test: send SIGTERM to proxy subprocess, verify clean exit within 2s.
  - Integration test: start proxy, wait past idle timeout, verify process exited.
  - Manual: kill IDE process, verify proxy exits (stdin EOF path, already working post-fix).
- Rollback/safety notes:
  - Idle timeout disabled by setting `LOOM_PROXY_IDLE_TIMEOUT=0`.
  - Signal handling is additive; no existing behavior changes.

### TD-GCP-01 (DEBT-010)

- Problem statement: GCP MCP server hardcodes `option.WithCredentialsFile()` for auth, suppressing `staticcheck` with `//nolint`. Should use ADC as default.
- Acceptance criteria:
  - Remove explicit `WithCredentialsFile`; use `storage.NewClient(ctx)` which auto-discovers credentials via ADC.
  - Optionally accept `GOOGLE_APPLICATION_CREDENTIALS` env var (ADC already supports this natively).
  - Remove `//nolint:staticcheck` suppression.
- Test/verification strategy:
  - Verify GCP client initializes with ADC in local dev (gcloud auth application-default login).
  - Verify explicit credentials file still works via env var.
- Rollback/safety notes:
  - ADC is Google's recommended approach and supports all existing credential sources.

### TD-MAINT-01 (DEBT-008)

- Problem statement: `internal/daemon/daemon.go` at 2,535 lines mixes 6+ concerns, making code review, navigation, and targeted refactoring harder.
- Acceptance criteria:
  - Split into: `daemon.go` (lifecycle/init, ~500 lines), `daemon_cache.go` (tool/resource cache), `daemon_health.go` (health monitoring), `daemon_reaper.go` (idle/session reaping), `daemon_metrics.go` (metrics collection).
  - Same package, no API changes, no behavior changes.
  - All tests pass unchanged; golangci-lint clean.
- Test/verification strategy:
  - `go test ./internal/daemon/...` before and after; identical results.
  - `golangci-lint run ./internal/daemon/...` clean.
  - `git diff --stat` confirms no logic changes, only file moves.
- Rollback/safety notes:
  - Pure mechanical refactor; revert is a single commit.

### TD-SESSION-06 (DEBT-004)

- Problem statement: local stdio proxy lacks enterprise-grade session semantics (lease/epoch/resume/drain), causing brittle behavior around daemon restart and hot reload.
- Acceptance criteria:
  - Session lease API for local proxy clients (ID, epoch, expiry, state).
  - Graceful daemon drain protocol for reload/restart with in-flight completion window.
  - Proxy transparently re-initializes session on epoch mismatch without requiring IDE restart.
  - Backward-compatible proxy behavior for old clients.
- Test/verification strategy:
  - Contract tests for lease creation, heartbeat, epoch mismatch, and resume paths.
  - Chaos tests for daemon restarts under concurrent tool traffic.
- Rollback/safety notes:
  - Additive protocol fields only; legacy flow preserved behind capability checks.

### TD-TRANSPORT-01 (DEBT-009)

- Problem statement: StdioTransport.Recv() calls Close() on context cancellation, permanently destroying the transport. Library consumers expecting reusable connections after timeout are not served.
- Acceptance criteria:
  - Background reader goroutine architecture: persistent goroutine reads from reader and publishes to channel. Recv selects on channel + ctx.Done without closing reader.
  - Transport remains functional after context cancellation (next Recv call works).
  - Existing tests pass; new test verifies: cancel Recv, then successfully Recv again.
- Test/verification strategy:
  - `go test ./...` in mcp-go passes.
  - New test: Recv with short timeout (no data), cancel, write data, Recv again succeeds.
  - Benchmark: goroutine overhead vs current approach.
- Rollback/safety notes:
  - Feature flag or build tag to select between current (close-on-cancel) and new (background-reader) mode.

## Deferred / Not In Scope

- Broad MCP server refactors unrelated to session/restart path.
- HUD UI modernization tasks outside runtime reliability.
- New external transport features beyond current Streamable HTTP scope.
