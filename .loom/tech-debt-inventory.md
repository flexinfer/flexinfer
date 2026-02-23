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

## Active Items

| ID | Component | Debt Statement | Evidence | Impact (1-5) | Risk Reduction (1-5) | Drag Reduction (1-5) | Effort (1-5) | Dependencies | Notes |
|---|---|---|---|---:|---:|---:|---:|---|---|
| DEBT-004 | `internal/daemon/session.go`, `cmd/loom/proxy.go`, `internal/daemon/http_handler.go` | No unified session lifecycle for local stdio proxy clients (lease, epoch, resume, heartbeat, graceful drain); HTTP has session semantics while local path does not. | `internal/daemon/session.go:8`, `internal/daemon/http_handler.go:13`, `docs/STREAMABLE_HTTP.md:88` | 4 | 5 | 4 | 4 | None (Wave 1+2 prereqs complete) | Carried from Wave 3; all blocking prereqs now resolved. |
| DEBT-009 | `libs/mcp-go/transport.go` | StdioTransport.Recv() context cancellation permanently closes the transport. Canceling a per-request context destroys the connection rather than just aborting the current read. | `libs/mcp-go/transport.go:127-130` (`t.Close()` on ctx.Done). Acceptable for loom-core (pool recycles), but library consumers expecting reusable transports after timeout will be surprised. | 2 | 2 | 1 | 4 | None | Library-level concern. Would require background reader goroutine architecture to fix properly. Low priority given loom-core's pool-based lifecycle. |

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
