# Technical Debt Inventory

## Scope

- Product/Service: `loom-core` (CLI `loom`, proxy, `loomd`, local MCP servers, `mcp-go` transport lib)
- Time horizon: 3-wave remediation over 4-8 weeks
- Owners: Daemon runtime + CLI/proxy + agentcontext + HUD maintainers
- Last updated: 2026-02-23 (Cycle 2)

## Completed Items (Cycle 1, verified 2026-02-23)

| ID | Status | Summary | Closed By |
|---|---|---|---|
| DEBT-001 | done | Proxy transport reset and reconnect classification hardened | Bounded autostart + structured error types (`proxyTransportError`) |
| DEBT-002 | done | End-to-end RPC deadlines enforced; response ID validation added | `ddd4e85` (response ID mismatch detection), call timeout tiers |
| DEBT-003 | done | One-shot `sync.Once` autostart replaced with bounded retry + cooldown | `cmd/loom/proxy.go:114-132` (5 attempts, 10s cooldown, reset on success) |
| DEBT-004 | done | Session lease/epoch management fully implemented | `internal/daemon/session.go`, `session_handlers.go`, `proxy.go` |
| DEBT-005 | done | Health monitor process churn reduced | Pool-reusing passive checks |
| DEBT-006 | done | Dev-upgrade safety gates strengthened | Session-aware drain + smoke checks |
| DEBT-007 | done | Restart/reconnect chaos tests added | `internal/integration/proxy_daemon_test.go` |
| DEBT-008 | done | Split daemon.go monolith into focused files | `1fc56e0` |
| DEBT-009 | done | StdioTransport background reader for non-destructive context cancel | Background reader goroutine in `libs/mcp-go/transport.go` |
| DEBT-010 | done | Migrate GCP MCP server from explicit credentials to ADC | `812c638` |
| DEBT-011 | done | Add signal handling and idle timeout to proxy process | `b6ca3da` |

## Active Items (Cycle 2)

| ID | Component | Debt Statement | Evidence | Impact | Risk | Drag | Effort(inv) | Dependencies | Notes |
|---|---|---|---|---:|---:|---:|---:|---|---|
| DEBT-012 | `pkg/agentcontext/service.go` | God object: 1,829 LOC, 15+ Qdrant clients, 50+ struct fields mixing 5 domains | 40 funcs, 111 defer Unlock calls across package | 4 | 3 | 4 | 2 | DEBT-022 (Qdrant registry) helps | Largest structural debt; split into sessions/presence/memory/graph/workflow modules |
| DEBT-013 | `pkg/codebase/service.go` | Monolith: 2,140 LOC mixing indexing, watch, embed, search | 33 funcs, ~65 lines/func avg, 2 nolint:noctx | 3 | 2 | 3 | 3 | None | Split into indexer/watcher/embed/search sub-packages |
| DEBT-014 | `cmd/loom/main.go` | Monolith: 1,907 LOC, 18 funcs, ~106 lines/func | Violates 20-line guideline by 5x; all Cobra commands in one file | 3 | 2 | 3 | 4 | None | Mechanical split into cmd_sync.go, cmd_tunnel.go, etc. |
| DEBT-015 | `cmd_agent.go`, `bridge/agent.go`, `api_agent.go` | Triplicate agent surfaces: 1,706 + 1,993 + 989 = 4,688 LOC with duplicated parsing/error code | Roadmap #21 Stage 2; shared contracts landed but not consumed | 3 | 3 | 4 | 3 | Shared contracts (Stage 1) already landed | Split each by feature; single contract model for all surfaces |
| DEBT-016 | `internal/daemon/callpipeline.go` | Call pipeline unit test coverage incomplete; side effects not isolated | Roadmap #20 Stage 2; blocks gateway hooks (#25) and observability (#12) | 4 | 4 | 3 | 4 | None | Highest priority; already in progress |
| DEBT-017 | `internal/devbox/backend/k8s.go` | Single 760 LOC file handles build + runtime + objects + wait | Roadmap #23; high churn from Kaniko->Buildah changes | 2 | 3 | 3 | 4 | None | 4-way split: k8s_build/runtime/objects/wait |
| DEBT-018 | `internal/hud/window`, daemon, devbox | Test coverage at 30.4%; 3 packages with 0 tests; daemon/devbox undertested | internal/hud/window: 5 files, 0 tests; Roadmap #2 | 3 | 3 | 2 | 3 | DEBT-017 unlocks devbox testing | Target: 40% overall |
| DEBT-019 | codebase, agentcontext, main | Hardcoded URLs, model names, retention policies, version | 8+ hardcoded values; pkg/env already exists but not used consistently | 2 | 2 | 2 | 5 | None | Quick fix; use existing pkg/env |
| DEBT-020 | `pkg/codebase`, `pkg/agentcontext` | Repeated type assertion + string slice extraction boilerplate (8+ instances) | pkg/validate exists but lacks these helpers | 2 | 1 | 3 | 5 | None | Add UnmarshalStringSlice, UnmarshalBool, etc. to pkg/validate |
| DEBT-021 | `cmd/mcp-gitlab`, `cmd/mcp-linkedin`, `cmd/mcp-k8s` | Large MCP server main.go files (1,594 + 1,403 + 1,089 LOC) | 28-58 funcs per file; all tool handlers in single file | 2 | 1 | 2 | 4 | None | Mechanical: split tools into per-resource files |
| DEBT-022 | `pkg/agentcontext/service.go`, `qdrant.go` | 15 individual Qdrant client fields; adding a collection requires 3 edits | Adding new collection = struct field + init + handler wiring | 2 | 2 | 3 | 4 | Prerequisite for DEBT-012 | Create QdrantRegistry with Get(collection) accessor |

## Source Links

- Incident(s): 88 orphaned proxy processes killed during 2026-02-23 session (resolved via DEBT-011).
- CI status: all suites green (`go test ./cmd/loom ./internal/daemon ./internal/integration` passes).
- TODO/FIXME scan: 0 actionable markers (only `context.TODO()` in tests).
- Code hotspots: `pkg/agentcontext/service.go` (1,829), `pkg/codebase/service.go` (2,140), `internal/hud/app.go` (2,132), `cmd/loom/main.go` (1,907).
- Codebase size: 179,403 lines across 564 Go files, 72 test files.
- Coverage: 30.4% (target 40%).
- Roadmap issues: #20 (callpipeline), #21 (agent contracts), #23 (devbox K8s), #2 (test coverage).
