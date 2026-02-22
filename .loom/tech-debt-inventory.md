# Technical Debt Inventory

## Scope

- Product/Service: `loom-core` (CLI `loom`, proxy, `loomd`, local MCP servers)
- Time horizon: 3-wave remediation over 4-8 weeks
- Owners: Daemon runtime + CLI/proxy maintainers

## Items

| ID | Component | Debt Statement | Evidence | Impact (1-5) | Risk Reduction (1-5) | Drag Reduction (1-5) | Effort (1-5) | Dependencies | Notes |
|---|---|---|---|---:|---:|---:|---:|---|---|
| DEBT-001 | `cmd/loom/proxy.go` | Local proxy connection bootstrap can leave a poisoned daemon transport after init failure, and reconnect reset only triggers on string matches for `broken pipe`/`EOF`. | `cmd/loom/proxy.go:93`, `cmd/loom/proxy.go:158`, `cmd/loom/proxy.go:167`, `cmd/loom/proxy.go:170`, `cmd/loom/proxy.go:243` | 5 | 5 | 4 | 2 | None | High likelihood of stale socket behavior after daemon bounce or partial startup failure. |
| DEBT-002 | `cmd/loom/proxy.go`, `cmd/loom/daemon.go`, daemon call path | Proxy and CLI daemon RPC paths use long-lived/background contexts for send/recv, allowing hung MCP flows to block agents indefinitely. | `cmd/loom/proxy.go:60`, `cmd/loom/proxy.go:181`, `cmd/loom/proxy.go:378`, `cmd/loom/proxy.go:382`, `cmd/loom/daemon.go:32`, `cmd/loom/daemon.go:43` | 5 | 5 | 5 | 3 | DEBT-001 | Violates target of bounded calls under ~60s client deadlines. |
| DEBT-003 | `cmd/loom/proxy.go`, `internal/daemon/ensure.go` | Daemon autostart strategy is single-shot (`sync.Once`) with a fixed 3s startup budget, so later daemon crashes/restarts are not proactively recovered by the proxy. | `cmd/loom/proxy.go:76`, `cmd/loom/proxy.go:144`, `cmd/loom/proxy.go:264`, `internal/daemon/ensure.go:31` | 4 | 4 | 4 | 3 | DEBT-001 | Produces repeated connect failures until external/manual daemon recovery. |
| DEBT-004 | Session handling across proxy/local/HTTP | No unified session lifecycle for local stdio proxy clients (lease, epoch, resume, heartbeat, graceful drain); HTTP has session semantics while local path does not. | `internal/daemon/http_handler.go:13`, `internal/daemon/http_handler.go:140`, `internal/daemon/session.go:8`, `internal/daemon/session.go:25`, `docs/STREAMABLE_HTTP.md:88` | 4 | 5 | 4 | 4 | DEBT-001, DEBT-002 | `internal/daemon/session.go` appears dead/unwired; local and HTTP session behavior diverge. |
| DEBT-005 | Health checks / local resource profile | Health monitor checks every registered server and probes via `fetchServerTools`, which spawns a fresh process per probe and kills it, adding avoidable CPU/memory churn in local loom-mode. | `internal/daemon/health.go:127`, `internal/daemon/health.go:154`, `internal/daemon/health.go:170`, `internal/daemon/daemon.go:1825`, `internal/daemon/daemon.go:1850`, `internal/daemon/daemon.go:1874` | 4 | 4 | 3 | 3 | None | Expensive with 40+ server catalog; competes with active proxy usage on dev machines. |
| DEBT-006 | Dev upgrade/reload safety gates | `dev-upgrade` restart guard keys off daemon pool active connections rather than explicit proxy session state, and smoke checks only proxy initialize, not tool-call resilience during restarts. | `scripts/dev/upgrade_local.sh:104`, `scripts/dev/upgrade_local.sh:107`, `scripts/dev/upgrade_local.sh:141`, `cmd/loom/status.go:20`, `internal/daemon/daemon.go:1190`, `internal/daemon/daemon.go:1196` | 4 | 4 | 3 | 2 | DEBT-004 | Can force/reject restarts based on incomplete signal and miss stale-call regressions. |
| DEBT-007 | Resilience test coverage | Integration coverage for proxy/daemon does not include daemon restart mid-session, stale socket reuse, hot reload while calls are inflight, or reconnect/epoch recovery assertions. | `internal/integration/proxy_daemon_test.go:59`, `internal/integration/proxy_daemon_test.go:110`, `internal/integration/proxy_daemon_test.go:158`, `internal/integration/proxy_daemon_test.go:224`, `internal/integration/proxy_daemon_test.go:272` | 4 | 4 | 5 | 2 | DEBT-001, DEBT-002, DEBT-004 | Limits confidence for enterprise-grade runtime guarantees. |

## Source Links

- Incident(s): user-reported stale sockets/hung agents during CLI -> proxy -> daemon -> MCP flow and daemon/proxy restart cycles.
- CI failures/flakes: no active red tests in focused suites (`go test ./cmd/loom ./internal/daemon ./pkg/toolexec` green), indicating resilience debt vs immediate correctness failures.
- SLO/metrics regressions: bounded-call objective in `ROADMAP.md` conflicts with unbounded proxy/CLI send/recv paths.
- TODO/FIXME scans: repository scan yielded mostly docs/test artifacts; no strong TODO cluster in proxy/daemon paths (`rg -n "TODO|FIXME|HACK|XXX" --glob '!vendor/**' .`).

## Per-Item Acceptance Criteria (Draft)

- DEBT-001: proxy reconnect logic classifies transport failures robustly (`errors.Is`/`net.Error`), and failed initialize always clears transport state before next call.
- DEBT-002: all proxy/CLI/daemon call hops enforce explicit per-call deadlines and return structured timeout errors without wedging client sessions.
- DEBT-003: daemon autostart/reconnect uses backoff + retry budget (not one-shot) and recovers after daemon restart without manual intervention.
- DEBT-004: daemon exposes a local proxy session lease model (session id + generation/epoch + heartbeat + drain states) with graceful reconnect semantics.
- DEBT-005: health monitor reuses pooled/running server connections and supports low-cost passive checks; baseline CPU/memory in loom-mode is lower than current state.
- DEBT-006: dev upgrade uses explicit proxy-session drain signal; smoke validates `initialize -> tools/list -> tools/call` across daemon restart.
- DEBT-007: chaos/integration matrix covers daemon restart, socket churn, reload during calls, and reconnect recovery, with deterministic assertions.
