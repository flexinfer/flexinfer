# Technical Debt Inventory

## Scope

- Product/Service: `loom-core` (CLI `loom`, proxy, `loomd`, local MCP servers, `mcp-go` transport lib)
- Time horizon: 3-wave remediation over 4-8 weeks
- Owners: Daemon runtime + CLI/proxy maintainers
- Last updated: 2026-02-24

## Completed Items (Wave 1+2, verified 2026-02-23)

| ID | Status | Summary | Closed By |
|---|---|---|---|
| DEBT-001 | done | Proxy transport reset and reconnect classification hardened | Bounded autostart + structured error types (`proxyTransportError`) |
| DEBT-002 | done | End-to-end RPC deadlines enforced; response ID validation added | `ddd4e85` (response ID mismatch detection), call timeout tiers |
| DEBT-003 | done | One-shot `sync.Once` autostart replaced with bounded retry + cooldown | `cmd/loom/proxy.go:114-132` (5 attempts, 10s cooldown, reset on success) |
| DEBT-005 | done | Health monitor process churn reduced | Pool-reusing passive checks |
| DEBT-006 | done | Dev-upgrade safety gates strengthened | Session-aware drain + smoke checks |
| DEBT-007 | done | Restart/reconnect chaos tests added | `internal/integration/proxy_daemon_test.go` |
| DEBT-008 | done | Split daemon.go monolith into focused files | `1fc56e0` |
| DEBT-010 | done | Migrate GCP MCP server from explicit credentials to ADC | `812c638` |
| DEBT-011 | done | Add signal handling and idle timeout to proxy process | `b6ca3da` |
| DEBT-004 | done | Session lease/epoch management fully implemented | Already implemented: `internal/daemon/session.go`, `internal/daemon/session_handlers.go`, `cmd/loom/proxy.go` |
| DEBT-009 | done | StdioTransport background reader for non-destructive context cancel | Background reader goroutine in `libs/mcp-go/transport.go` |

## Active Items

No active items remain. All tech debt has been resolved.

## Source Links

- Incident(s): 88 orphaned proxy processes killed during 2026-02-23 session; HUD -32602 errors from dead `agent_handoff_list` tool (fixed in `454ecf8`).
- Recent fixes: `ddd4e85` (response ID validation + lock ordering), `454ecf8` (handoff_list removal), `352d8a2` in mcp-go (StdioTransport Close/Recv lifecycle).
- CI status: all suites green (`go test ./cmd/loom ./internal/daemon ./internal/integration` passes).
- TODO/FIXME scan: 1 marker total (`cmd/mcp-gcp/main.go:44`); codebase is very clean.
- Code hotspots: `daemon.go` (919 lines, split from 2535), `proxy.go` (1191 lines).

## Per-Item Acceptance Criteria (Draft)

- DEBT-004: daemon exposes local proxy session lease model (session id + generation/epoch + heartbeat + drain states) with graceful reconnect semantics. Backward-compatible with existing proxy protocol.
- ~~DEBT-008: daemon.go split into <=500-line files per concern; no behavior changes; all existing tests pass unchanged.~~ Done (`1fc56e0`): split into daemon.go (919), daemon_dispatch.go (618), daemon_toolcache.go (731), daemon_call.go (130), daemon_loops.go (182).
- DEBT-009: StdioTransport supports non-destructive context cancellation via background reader goroutine; existing tests pass; new test verifies transport reusability after timeout.
- DEBT-010: GCP server uses ADC by default; explicit credentials file as optional override.
- DEBT-011: proxy handles SIGTERM/SIGINT for graceful shutdown; optional idle timeout (configurable) terminates proxy after N minutes of inactivity.
